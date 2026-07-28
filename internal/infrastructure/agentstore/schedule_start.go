package agentstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ agentruntime.ScheduleEditInteractionCreator = (*Repository)(nil)

func (r *Repository) CreateScheduleEditInteraction(
	ctx context.Context,
	req agentruntime.CreateScheduleEditInteractionRequest,
) (agentruntime.StartScheduleEditInteractionResult, error) {
	if err := req.Validate(); err != nil {
		return agentruntime.StartScheduleEditInteractionResult{}, err
	}
	if r == nil || r.db == nil {
		return agentruntime.StartScheduleEditInteractionResult{}, errors.New("agent runtime repository is not configured")
	}

	var result agentruntime.StartScheduleEditInteractionResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sessionCandidate := agentruntime.NewSessionForRun(req.Run)
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(toDBSession(sessionCandidate)).Error; err != nil {
			return err
		}

		var session model.AgentSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", sessionCandidate.ID).
			Take(&session).Error; err != nil {
			return mapNotFound(err)
		}

		var existing model.AgentRun
		existingErr := tx.
			Where("session_id = ? AND trigger_message_id = ?",
				session.ID, req.Run.TriggerMessageID).
			Order("created_at DESC").
			Take(&existing).Error
		switch {
		case existingErr == nil:
			replayed, err := replayScheduleEditInteraction(tx, &session, &existing, req)
			if err != nil {
				return err
			}
			result = replayed
			return nil
		case !errors.Is(existingErr, gorm.ErrRecordNotFound):
			return existingErr
		}

		if err := ensureReplaceableActiveRun(tx, &session); err != nil {
			return err
		}

		now := time.Now().UTC().Truncate(time.Microsecond)
		run := agentruntime.NewRun(agentruntime.NewRunRequest{
			SessionID:        session.ID,
			TriggerType:      req.Run.TriggerType,
			TriggerMessageID: req.Run.TriggerMessageID,
			TriggerEventID:   req.Run.TriggerEventID,
			ActorOpenID:      req.Run.ActorOpenID,
			ParentRunID:      req.Run.ParentRunID,
			Goal:             req.Run.Goal,
			InputText:        req.Run.InputText,
		})
		run.CreatedAt = now
		run.UpdatedAt = now
		if err := tx.Create(toDBRun(run)).Error; err != nil {
			return err
		}

		plan := agentruntime.NewStep(agentruntime.NewStepRequest{
			RunID: run.ID, Index: 0, Kind: agentruntime.StepKindPlan,
			CapabilityName: "shadow", InputJSON: "{}",
		})
		plan.Status = agentruntime.StepStatusCompleted
		plan.OutputJSON = "{}"
		plan.StartedAt = now
		plan.FinishedAt = now
		plan.CreatedAt = now
		if err := tx.Create(toDBStep(plan)).Error; err != nil {
			return err
		}

		revision := int64(1)
		expiresAt := now.Add(req.WaitTTL)
		waitInput, err := json.Marshal(interactionWaitPayload{
			Version:       1,
			InteractionID: req.InteractionID,
			Kind:          "schedule_edit",
			Revision:      revision,
			ExpiresAt:     expiresAt,
			TokenHash:     req.TokenHash,
			TrustedInput:  normalizedTrustedInput(req.TrustedInput),
		})
		if err != nil {
			return err
		}
		wait := &agentruntime.AgentStep{
			ID: req.StepID, RunID: run.ID, Index: 1,
			Kind: agentruntime.StepKindWait, Status: agentruntime.StepStatusCompleted,
			InputJSON: string(waitInput), OutputJSON: "{}", ExternalRef: req.InteractionID,
			StartedAt: now, FinishedAt: now, CreatedAt: now,
			DedupeKey: interactionDedupeKey(req.InteractionID, revision),
		}
		if err := tx.Create(toDBStep(wait)).Error; err != nil {
			return err
		}
		if err := insertProjectionOutbox(tx, wait.ID, req.Projection, now); err != nil {
			return err
		}

		runUpdate := tx.Model(&model.AgentRun{}).
			Where("id = ? AND revision = ?", run.ID, 0).
			Updates(map[string]any{
				"status":             string(agentruntime.RunStatusWaitingApproval),
				"waiting_reason":     string(agentruntime.WaitingReasonApproval),
				"waiting_token":      req.TokenHash,
				"revision":           revision,
				"current_step_index": int32(1),
				"updated_at":         now,
			})
		if runUpdate.Error != nil {
			return runUpdate.Error
		}
		if runUpdate.RowsAffected != 1 {
			return agentruntime.ErrInteractionConflict
		}
		sessionUpdate := tx.Model(&model.AgentSession{}).
			Where("id = ? AND active_run_id = ?", session.ID, session.ActiveRunID).
			Updates(map[string]any{
				"active_run_id":      run.ID,
				"last_message_id":    req.Run.TriggerMessageID,
				"last_actor_open_id": req.Run.ActorOpenID,
				"updated_at":         now,
			})
		if sessionUpdate.Error != nil {
			return sessionUpdate.Error
		}
		if sessionUpdate.RowsAffected != 1 {
			return agentruntime.ErrActiveRunConflict
		}
		result = agentruntime.StartScheduleEditInteractionResult{
			RunID:         run.ID,
			StepID:        wait.ID,
			InteractionID: req.InteractionID,
			Revision:      revision,
			ExpiresAt:     expiresAt,
		}
		return nil
	})
	return result, err
}

func ensureReplaceableActiveRun(tx *gorm.DB, session *model.AgentSession) error {
	if strings.TrimSpace(session.ActiveRunID) == "" {
		return nil
	}
	var active model.AgentRun
	if err := tx.Where("id = ? AND session_id = ?", session.ActiveRunID, session.ID).
		Take(&active).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return agentruntime.ErrActiveRunConflict
		}
		return err
	}
	if !terminalRunStatus(agentruntime.RunStatus(active.Status)) {
		return agentruntime.ErrActiveRunConflict
	}
	return nil
}

func replayScheduleEditInteraction(
	tx *gorm.DB,
	session *model.AgentSession,
	run *model.AgentRun,
	req agentruntime.CreateScheduleEditInteractionRequest,
) (agentruntime.StartScheduleEditInteractionResult, error) {
	if session.ActiveRunID != run.ID ||
		run.Status != string(agentruntime.RunStatusWaitingApproval) ||
		run.WaitingReason != string(agentruntime.WaitingReasonApproval) ||
		run.Revision != 1 ||
		!strings.EqualFold(run.WaitingToken, req.TokenHash) {
		return agentruntime.StartScheduleEditInteractionResult{}, agentruntime.ErrInteractionConflict
	}
	var wait model.AgentStep
	if err := tx.Where("id = ? AND run_id = ?", req.StepID, run.ID).Take(&wait).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return agentruntime.StartScheduleEditInteractionResult{}, agentruntime.ErrInteractionConflict
		}
		return agentruntime.StartScheduleEditInteractionResult{}, err
	}
	var payload interactionWaitPayload
	if json.Unmarshal([]byte(wait.InputJSON), &payload) != nil ||
		wait.Kind != string(agentruntime.StepKindWait) ||
		wait.Status != string(agentruntime.StepStatusCompleted) ||
		wait.ExternalRef != req.InteractionID ||
		payload.Version != 1 ||
		payload.Kind != "schedule_edit" ||
		payload.InteractionID != req.InteractionID ||
		payload.Revision != 1 ||
		payload.ExpiresAt.IsZero() ||
		!strings.EqualFold(payload.TokenHash, req.TokenHash) ||
		!equalJSONDocument(payload.TrustedInput, normalizedTrustedInput(req.TrustedInput)) {
		return agentruntime.StartScheduleEditInteractionResult{}, agentruntime.ErrInteractionConflict
	}
	outbox, err := findProjectionOutboxByStep(tx, wait.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return agentruntime.StartScheduleEditInteractionResult{}, agentruntime.ErrInteractionConflict
		}
		return agentruntime.StartScheduleEditInteractionResult{}, err
	}
	expectedOutbox := newProjectionOutbox(wait.ID, req.Projection, wait.CreatedAt)
	if outbox.IndexAlias != expectedOutbox.IndexAlias ||
		outbox.DocumentID != expectedOutbox.DocumentID ||
		!equalJSONDocument([]byte(outbox.PayloadJSON), []byte(expectedOutbox.PayloadJSON)) {
		return agentruntime.StartScheduleEditInteractionResult{}, agentruntime.ErrInteractionConflict
	}
	return agentruntime.StartScheduleEditInteractionResult{
		RunID:         run.ID,
		StepID:        wait.ID,
		InteractionID: req.InteractionID,
		Revision:      payload.Revision,
		ExpiresAt:     payload.ExpiresAt,
	}, nil
}
