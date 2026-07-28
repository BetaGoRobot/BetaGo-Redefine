package agentstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ agentruntime.ContinuationStore = (*Repository)(nil)

func validateStoredDecision(decision agentruntime.TurnDecision) error {
	if strings.TrimSpace(decision.Reason) == "" || strings.TrimSpace(decision.Reason) != decision.Reason {
		return agentruntime.ErrInvalidRuntimeContract
	}
	switch decision.Decision {
	case agentruntime.TurnDecisionReply:
		if strings.TrimSpace(decision.Reply) == "" || strings.TrimSpace(decision.Reply) != decision.Reply {
			return agentruntime.ErrInvalidRuntimeContract
		}
	case agentruntime.TurnDecisionObserveOnly, agentruntime.TurnDecisionClose:
		if decision.Reply != "" {
			return agentruntime.ErrInvalidRuntimeContract
		}
	default:
		return agentruntime.ErrInvalidRuntimeContract
	}
	return nil
}

func (r *Repository) ClaimContinuationStep(
	ctx context.Context,
	claim agentruntime.ContinuationClaim,
) (*agentruntime.AgentStep, error) {
	if claim.RunID == "" || claim.WorkerID == "" || claim.LeaseTTL <= 0 || claim.Now.IsZero() {
		return nil, agentruntime.ErrInvalidRuntimeContract
	}
	var claimed model.AgentStep
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Raw(`
			SELECT steps.*
			FROM agent_steps AS steps
			JOIN agent_runs AS runs ON runs.id = steps.run_id
			JOIN agent_sessions AS sessions
			  ON sessions.id = runs.session_id
			 AND sessions.active_run_id = runs.id
			WHERE steps.run_id = ?
			  AND (
			    (
			      steps.kind = ?
			      AND steps.input_json->>'version' = '1'
			      AND COALESCE(steps.input_json->>'source_step_id', '') <> ''
			      AND steps.dedupe_key LIKE '%:continuation'
			      AND EXISTS (
			        SELECT 1 FROM agent_steps AS source
			        WHERE source.id = steps.input_json->>'source_step_id'
			          AND source.run_id = steps.run_id
			          AND source.index < steps.index
			      )
			    )
			    OR (
			      steps.kind = ?
			      AND steps.input_json->>'version' = '1'
			      AND steps.input_json->>'step_id' = steps.id
			      AND steps.input_json->>'idempotency_key' = steps.id
			      AND steps.dedupe_key LIKE '%:continuation:reply'
			    )
			  )
			  AND steps.status = ?
			  AND (steps.lease_expires_at IS NULL OR steps.lease_expires_at <= ?)
			  AND runs.status IN (?, ?)
			  AND NOT EXISTS (
				  SELECT 1 FROM agent_steps AS earlier
				  WHERE earlier.run_id = steps.run_id
				    AND earlier.index < steps.index
				    AND earlier.status IN (?, ?)
			  )
			ORDER BY steps.index, steps.id
			FOR UPDATE OF sessions, runs, steps SKIP LOCKED
			LIMIT 1`,
			claim.RunID, string(agentruntime.StepKindDecide), string(agentruntime.StepKindReply),
			string(agentruntime.StepStatusQueued), claim.Now,
			string(agentruntime.RunStatusQueued), string(agentruntime.RunStatusRunning),
			string(agentruntime.StepStatusQueued), string(agentruntime.StepStatusRunning),
		).Scan(&claimed)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return agentruntime.ErrNotFound
		}
		leaseUntil := claim.Now.Add(claim.LeaseTTL)
		result = tx.Model(&model.AgentStep{}).
			Where("id = ? AND status = ?", claimed.ID, string(agentruntime.StepStatusQueued)).
			Updates(map[string]any{
				"status":        string(agentruntime.StepStatusRunning),
				"attempt_count": gorm.Expr("attempt_count + 1"),
				"worker_id":     claim.WorkerID, "lease_expires_at": leaseUntil,
				"started_at": claim.Now,
			})
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return agentruntime.ErrLeaseLost
		}
		if err := tx.Model(&model.AgentRun{}).Where("id = ?", claim.RunID).
			Updates(map[string]any{"status": string(agentruntime.RunStatusRunning), "updated_at": claim.Now}).Error; err != nil {
			return err
		}
		claimed.Status = string(agentruntime.StepStatusRunning)
		claimed.AttemptCount++
		claimed.WorkerID = claim.WorkerID
		claimed.LeaseExpiresAt = leaseUntil
		claimed.StartedAt = claim.Now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toRuntimeStep(&claimed), nil
}

func (r *Repository) ValidateContinuationLease(ctx context.Context, lease agentruntime.StepLease) error {
	if lease.StepID == "" || lease.WorkerID == "" || lease.AttemptCount <= 0 ||
		lease.LeaseTTL <= 0 || lease.Now.IsZero() {
		return agentruntime.ErrInvalidRuntimeContract
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, run, step, err := lockContinuationStage(tx, lease.StepID)
		if err != nil {
			return err
		}
		if run.Status != string(agentruntime.RunStatusRunning) || session.ActiveRunID != run.ID ||
			step.Status != string(agentruntime.StepStatusRunning) ||
			step.WorkerID != lease.WorkerID || step.AttemptCount != lease.AttemptCount ||
			!step.LeaseExpiresAt.After(lease.Now) ||
			(step.Kind != string(agentruntime.StepKindDecide) && step.Kind != string(agentruntime.StepKindReply)) {
			return agentruntime.ErrLeaseLost
		}
		result := tx.Model(&model.AgentStep{}).
			Where("id = ? AND worker_id = ? AND attempt_count = ?",
				step.ID, lease.WorkerID, lease.AttemptCount).
			Update("lease_expires_at", lease.Now.Add(lease.LeaseTTL))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentruntime.ErrLeaseLost
		}
		return nil
	})
}

func (r *Repository) LoadContinuationContext(
	ctx context.Context,
	req agentruntime.LoadContinuationContextRequest,
) (agentruntime.ContinuationContext, error) {
	if req.RunID == "" || req.AnchorStepID == "" || req.RecentLimit <= 0 {
		return agentruntime.ContinuationContext{}, agentruntime.ErrInvalidRuntimeContract
	}
	var output agentruntime.ContinuationContext
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run model.AgentRun
		if err := tx.First(&run, "id = ?", req.RunID).Error; err != nil {
			return mapNotFound(err)
		}
		var session model.AgentSession
		if err := tx.First(&session, "id = ?", run.SessionID).Error; err != nil {
			return mapNotFound(err)
		}
		var anchor model.AgentStep
		if err := tx.First(&anchor,
			"id = ? AND run_id = ? AND kind = ?", req.AnchorStepID, req.RunID, string(agentruntime.StepKindDecide),
		).Error; err != nil {
			return mapNotFound(err)
		}
		var anchorInput continuationStepInput
		if json.Unmarshal([]byte(anchor.InputJSON), &anchorInput) != nil ||
			anchorInput.Version != 1 || anchorInput.SourceStepID == "" {
			return agentruntime.ErrInteractionConflict
		}
		var source model.AgentStep
		if err := tx.First(&source,
			"id = ? AND run_id = ? AND \"index\" < ?", anchorInput.SourceStepID, req.RunID, anchor.Index,
		).Error; err != nil {
			return mapNotFound(err)
		}
		outcome, err := capabilityOutcomeEventTx(tx, &run, &session, &source)
		if err != nil {
			return err
		}
		var recent []*model.AgentStep
		if err := tx.Where("run_id = ? AND \"index\" < ?", req.RunID, anchor.Index).
			Order(`"index" DESC`).Limit(req.RecentLimit).Find(&recent).Error; err != nil {
			return err
		}
		steps := make([]*agentruntime.AgentStep, 0, len(recent))
		for index := len(recent) - 1; index >= 0; index-- {
			steps = append(steps, toRuntimeStep(recent[index]))
		}
		output = agentruntime.ContinuationContext{
			RunID: run.ID, Goal: run.Goal, TriggerMessageID: run.TriggerMessageID,
			ChatID: session.ChatID, ActorOpenID: run.ActorOpenID,
			LatestOutcome: outcome, RecentSteps: steps,
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return output, err
}

func capabilityOutcomeEventTx(
	tx *gorm.DB,
	run *model.AgentRun,
	session *model.AgentSession,
	source *model.AgentStep,
) (agentruntime.ConversationEvent, error) {
	event := agentruntime.ConversationEvent{
		ID: source.ID, Type: agentruntime.EventTypeCapabilityResult,
		ChatID: session.ChatID, ActorOpenID: run.ActorOpenID, RunID: run.ID,
		OccurredAt: source.FinishedAt, Payload: json.RawMessage(source.OutputJSON),
	}
	switch agentruntime.StepKind(source.Kind) {
	case agentruntime.StepKindCapabilityResult:
		var card model.AgentStep
		if err := tx.
			Where("run_id = ? AND kind = ? AND \"index\" < ?",
				run.ID, string(agentruntime.StepKindCardAction), source.Index).
			Where("external_ref = ?", source.ExternalRef).
			Order(`"index" DESC`).First(&card).Error; err != nil {
			return agentruntime.ConversationEvent{}, mapNotFound(err)
		}
		var cardEvent agentruntime.ConversationEvent
		if json.Unmarshal([]byte(card.InputJSON), &cardEvent) != nil {
			return agentruntime.ConversationEvent{}, agentruntime.ErrInteractionConflict
		}
		event.ActorOpenID = cardEvent.ActorOpenID
		event.InteractionID = cardEvent.InteractionID
		event.Revision = cardEvent.Revision
		event.Action = cardEvent.Action
		event.SourceRef = cardEvent.SourceRef
	case agentruntime.StepKindResume:
		var resumeEvent agentruntime.ConversationEvent
		if json.Unmarshal([]byte(source.InputJSON), &resumeEvent) != nil {
			return agentruntime.ConversationEvent{}, agentruntime.ErrInteractionConflict
		}
		event.ActorOpenID = resumeEvent.ActorOpenID
		event.InteractionID = resumeEvent.InteractionID
		event.Revision = resumeEvent.Revision
		event.Action = resumeEvent.Action
		event.SourceRef = resumeEvent.SourceRef
	default:
		return agentruntime.ConversationEvent{}, agentruntime.ErrInteractionConflict
	}
	return event, nil
}

func (r *Repository) PersistDecision(
	ctx context.Context,
	req agentruntime.PersistDecisionRequest,
) (*agentruntime.AgentStep, error) {
	if req.StepID == "" || req.WorkerID == "" || req.AttemptCount <= 0 || req.FinishedAt.IsZero() {
		return nil, agentruntime.ErrInvalidRuntimeContract
	}
	if req.Decision.Decision == agentruntime.TurnDecisionWait {
		return nil, agentruntime.ErrInvalidRuntimeContract
	}
	if err := validateStoredDecision(req.Decision); err != nil {
		return nil, err
	}
	var reply *agentruntime.AgentStep
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, run, step, err := lockContinuationStage(tx, req.StepID)
		if err != nil {
			return err
		}
		if !continuationLeaseMatches(step, req.WorkerID, req.AttemptCount, agentruntime.StepKindDecide) {
			return agentruntime.ErrLeaseLost
		}
		if run.Status != string(agentruntime.RunStatusRunning) || session.ActiveRunID != run.ID {
			return agentruntime.ErrLeaseLost
		}
		output, err := json.Marshal(req.Decision)
		if err != nil {
			return err
		}
		currentIndex := run.CurrentStepIndex
		if req.Decision.Decision == agentruntime.TurnDecisionReply {
			replyID := stableContinuationChildID(run.ID, step.ID, "reply")
			frozen := agentruntime.ReplyRequest{
				Version: 1,
				StepID:  replyID, RunID: run.ID, Text: req.Decision.Reply,
				TriggerMessageID: run.TriggerMessageID, ChatID: session.ChatID,
				IdempotencyKey: replyID,
			}
			input, err := json.Marshal(frozen)
			if err != nil {
				return err
			}
			index, err := nextStepIndex(tx, run)
			if err != nil {
				return err
			}
			dbReply := &model.AgentStep{
				ID: replyID, RunID: run.ID, Index: index, Kind: string(agentruntime.StepKindReply),
				Status: string(agentruntime.StepStatusQueued), InputJSON: string(input), OutputJSON: "{}",
				CreatedAt: req.FinishedAt, DedupeKey: step.DedupeKey + ":reply",
			}
			if err := tx.Create(dbReply).Error; err != nil {
				return err
			}
			reply = toRuntimeStep(dbReply)
			currentIndex = index
		}
		result := tx.Model(&model.AgentStep{}).
			Where("id = ? AND status = ? AND worker_id = ? AND attempt_count = ?",
				step.ID, string(agentruntime.StepStatusRunning), req.WorkerID, req.AttemptCount).
			Updates(map[string]any{
				"status": string(agentruntime.StepStatusCompleted), "output_json": string(output),
				"error_text": "", "finished_at": req.FinishedAt, "worker_id": "", "lease_expires_at": nil,
			})
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return agentruntime.ErrLeaseLost
		}
		if req.Decision.Decision == agentruntime.TurnDecisionReply {
			result := tx.Model(&model.AgentRun{}).
				Where("id = ? AND status = ?", run.ID, string(agentruntime.RunStatusRunning)).Updates(map[string]any{
				"status": string(agentruntime.RunStatusQueued), "current_step_index": currentIndex,
				"updated_at": req.FinishedAt,
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return agentruntime.ErrLeaseLost
			}
			return nil
		}
		if req.Decision.Decision != agentruntime.TurnDecisionObserveOnly &&
			req.Decision.Decision != agentruntime.TurnDecisionClose {
			return agentruntime.ErrInvalidRuntimeContract
		}
		if err := completeContinuationRunTx(tx, session, run, req.Decision.Reason, req.FinishedAt); err != nil {
			return err
		}
		return nil
	})
	return reply, err
}

func (r *Repository) RetryContinuationStep(ctx context.Context, req agentruntime.RetryStepRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, run, step, err := lockContinuationStage(tx, req.StepID)
		if err != nil {
			return err
		}
		if !continuationLeaseMatches(step, req.WorkerID, req.AttemptCount, agentruntime.StepKind(step.Kind)) ||
			run.Status != string(agentruntime.RunStatusRunning) || session.ActiveRunID != run.ID {
			return agentruntime.ErrLeaseLost
		}
		result := tx.Model(&model.AgentStep{}).Where("id = ? AND worker_id = ? AND attempt_count = ?",
			req.StepID, req.WorkerID, req.AttemptCount).Updates(map[string]any{
			"status": string(agentruntime.StepStatusQueued), "error_text": req.ErrorText,
			"worker_id": "", "lease_expires_at": req.RetryAt,
		})
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return agentruntime.ErrLeaseLost
		}
		runResult := tx.Model(&model.AgentRun{}).
			Where("id = ? AND status = ?", step.RunID, string(agentruntime.RunStatusRunning)).
			Updates(map[string]any{"status": string(agentruntime.RunStatusQueued), "updated_at": time.Now().UTC()})
		if runResult.Error != nil {
			return runResult.Error
		}
		if runResult.RowsAffected != 1 {
			return agentruntime.ErrLeaseLost
		}
		return nil
	})
}

func (r *Repository) CompleteReplyDelivery(
	ctx context.Context,
	req agentruntime.CompleteReplyDeliveryRequest,
) error {
	if req.StepID == "" || req.WorkerID == "" || req.AttemptCount <= 0 ||
		strings.TrimSpace(req.MessageID) == "" || strings.TrimSpace(req.MessageID) != req.MessageID || req.FinishedAt.IsZero() {
		return agentruntime.ErrInvalidRuntimeContract
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, run, step, err := lockContinuationStage(tx, req.StepID)
		if err != nil {
			return err
		}
		if !continuationLeaseMatches(step, req.WorkerID, req.AttemptCount, agentruntime.StepKindReply) {
			return agentruntime.ErrLeaseLost
		}
		if run.Status != string(agentruntime.RunStatusRunning) || session.ActiveRunID != run.ID {
			return agentruntime.ErrLeaseLost
		}
		output, _ := json.Marshal(map[string]string{"message_id": req.MessageID})
		result := tx.Model(&model.AgentStep{}).
			Where("id = ? AND status = ? AND worker_id = ? AND attempt_count = ?",
				step.ID, string(agentruntime.StepStatusRunning), req.WorkerID, req.AttemptCount).
			Updates(map[string]any{
				"status": string(agentruntime.StepStatusCompleted), "output_json": string(output),
				"external_ref": req.MessageID, "error_text": "", "finished_at": req.FinishedAt,
				"worker_id": "", "lease_expires_at": nil,
			})
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return agentruntime.ErrLeaseLost
		}
		return completeContinuationRunTx(tx, session, run, req.MessageID, req.FinishedAt)
	})
}

func lockContinuationStage(
	tx *gorm.DB,
	stepID string,
) (*model.AgentSession, *model.AgentRun, *model.AgentStep, error) {
	var reference model.AgentStep
	if err := tx.First(&reference, "id = ?", stepID).Error; err != nil {
		return nil, nil, nil, mapNotFound(err)
	}
	var runRef model.AgentRun
	if err := tx.First(&runRef, "id = ?", reference.RunID).Error; err != nil {
		return nil, nil, nil, mapNotFound(err)
	}
	var session model.AgentSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "id = ?", runRef.SessionID).Error; err != nil {
		return nil, nil, nil, mapNotFound(err)
	}
	var run model.AgentRun
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, "id = ?", runRef.ID).Error; err != nil {
		return nil, nil, nil, mapNotFound(err)
	}
	var step model.AgentStep
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&step, "id = ?", stepID).Error; err != nil {
		return nil, nil, nil, mapNotFound(err)
	}
	return &session, &run, &step, nil
}

func continuationLeaseMatches(
	step *model.AgentStep,
	workerID string,
	attempt int32,
	kind agentruntime.StepKind,
) bool {
	return step.Status == string(agentruntime.StepStatusRunning) &&
		step.WorkerID == workerID && step.AttemptCount == attempt && step.Kind == string(kind)
}

func completeContinuationRunTx(
	tx *gorm.DB,
	session *model.AgentSession,
	run *model.AgentRun,
	summary string,
	finishedAt time.Time,
) error {
	result := tx.Model(&model.AgentRun{}).
		Where("id = ? AND status = ?", run.ID, string(agentruntime.RunStatusRunning)).Updates(map[string]any{
		"status": string(agentruntime.RunStatusCompleted), "result_summary": summary,
		"finished_at": finishedAt, "updated_at": finishedAt,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agentruntime.ErrLeaseLost
	}
	return tx.Model(&model.AgentSession{}).
		Where("id = ? AND active_run_id = ?", session.ID, run.ID).
		Updates(map[string]any{"active_run_id": "", "updated_at": finishedAt}).Error
}

func stableContinuationChildID(runID, parentID, kind string) string {
	return stableScheduleStepID(runID, parentID, kind)
}

func (r *Repository) RepairContinuation(ctx context.Context, runID string, now time.Time) error {
	if runID == "" || now.IsZero() {
		return agentruntime.ErrInvalidRuntimeContract
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run model.AgentRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, "id = ?", runID).Error; err != nil {
			return mapNotFound(err)
		}
		if run.Status != string(agentruntime.RunStatusQueued) {
			return nil
		}
		var anchor model.AgentStep
		if err := tx.Where("run_id = ? AND kind IN ?", runID, []string{
			string(agentruntime.StepKindCapabilityResult), string(agentruntime.StepKindResume),
		}).Where("status = ? AND dedupe_key <> ''", string(agentruntime.StepStatusCompleted)).
			Order(`"index" DESC`).First(&anchor).Error; err != nil {
			return mapNotFound(err)
		}
		if !json.Valid([]byte(anchor.OutputJSON)) {
			return agentruntime.ErrInteractionConflict
		}
		continuationDedupe := anchor.DedupeKey + ":continuation"
		var existing model.AgentStep
		existingErr := tx.Where(
			"run_id = ? AND dedupe_key = ?", runID, continuationDedupe,
		).First(&existing).Error
		if existingErr == nil {
			if existing.Kind == string(agentruntime.StepKindDecide) &&
				(existing.Status == string(agentruntime.StepStatusQueued) ||
					existing.Status == string(agentruntime.StepStatusRunning)) {
				return nil
			}
			return agentruntime.ErrInteractionConflict
		}
		if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}
		var later int64
		if err := tx.Model(&model.AgentStep{}).Where(
			`run_id = ? AND "index" > ? AND kind IN ?`, runID, anchor.Index,
			[]string{string(agentruntime.StepKindWait), string(agentruntime.StepKindReply)},
		).Count(&later).Error; err != nil {
			return err
		}
		if later > 0 {
			return agentruntime.ErrInteractionConflict
		}
		continuation, err := enqueueContinuationStepTx(
			tx, &run, anchor.ID, anchor.DedupeKey, now,
		)
		if err != nil {
			return err
		}
		return tx.Model(&model.AgentRun{}).Where("id = ?", run.ID).
			Updates(map[string]any{"current_step_index": continuation.Index, "updated_at": now}).Error
	})
}
