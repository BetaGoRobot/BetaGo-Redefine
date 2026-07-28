package agentstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrLeaseLost                = agentruntime.ErrLeaseLost
	ErrActiveRunConflict        = agentruntime.ErrActiveRunConflict
	ErrInteractionConflict      = agentruntime.ErrInteractionConflict
	ErrInteractionExpired       = agentruntime.ErrInteractionExpired
	ErrInteractionTokenMismatch = agentruntime.ErrInteractionTokenMismatch
	ErrTerminalRun              = agentruntime.ErrTerminalRun
)

type interactionWaitPayload struct {
	Version       int             `json:"version"`
	InteractionID string          `json:"interaction_id"`
	Kind          string          `json:"kind"`
	Revision      int64           `json:"revision"`
	ExpiresAt     time.Time       `json:"expires_at"`
	TokenHash     string          `json:"token_hash"`
	TrustedInput  json.RawMessage `json:"trusted_input"`
}

func (r *Repository) FindActiveRun(ctx context.Context, sessionID string) (*agentruntime.AgentRun, error) {
	if err := validateCanonicalValue("session id", sessionID, false); err != nil {
		return nil, err
	}
	var run model.AgentRun
	result := r.db.WithContext(ctx).
		Table("agent_runs AS runs").
		Select("runs.*").
		Joins(`JOIN agent_sessions AS sessions
			ON sessions.active_run_id = runs.id
			AND runs.session_id = sessions.id`).
		Where("sessions.id = ?", sessionID).
		Where("runs.status NOT IN ?", []string{
			string(agentruntime.RunStatusCompleted),
			string(agentruntime.RunStatusFailed),
			string(agentruntime.RunStatusCancelled),
		}).
		Limit(1).
		Scan(&run)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, agentruntime.ErrNotFound
	}
	return toRuntimeRun(&run), nil
}

func (r *Repository) StartInteraction(ctx context.Context, req agentruntime.StartInteractionRequest) (*agentruntime.AgentRun, *agentruntime.AgentStep, error) {
	if err := req.Validate(); err != nil {
		return nil, nil, err
	}
	var storedRun *agentruntime.AgentRun
	var storedStep *agentruntime.AgentStep
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var runReference struct {
			SessionID string
		}
		if err := tx.Model(&model.AgentRun{}).
			Select("session_id").
			Where("id = ?", req.RunID).
			Take(&runReference).Error; err != nil {
			return mapNotFound(err)
		}
		var session model.AgentSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&session, "id = ?", runReference.SessionID).Error; err != nil {
			return mapNotFound(err)
		}
		var run model.AgentRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND session_id = ?", req.RunID, runReference.SessionID).
			First(&run).Error; err != nil {
			return mapNotFound(err)
		}
		if session.ActiveRunID != "" && session.ActiveRunID != req.RunID {
			return ErrActiveRunConflict
		}

		var existing model.AgentStep
		existingErr := tx.First(&existing, "id = ?", req.StepID).Error
		switch {
		case existingErr == nil:
			if !sameInteractionStart(&run, &existing, req) {
				return ErrInteractionConflict
			}
			storedRun = toRuntimeRun(&run)
			storedStep = toRuntimeStep(&existing)
			return nil
		case !errors.Is(existingErr, gorm.ErrRecordNotFound):
			return existingErr
		}
		if waitingRunStatus(agentruntime.RunStatus(run.Status)) ||
			run.WaitingReason != "" || run.WaitingToken != "" {
			return ErrInteractionConflict
		}
		var interactionStep model.AgentStep
		interactionErr := tx.Where("run_id = ? AND kind = ? AND external_ref = ?",
			req.RunID, string(agentruntime.StepKindWait), req.InteractionID).
			First(&interactionStep).Error
		if interactionErr == nil {
			return ErrInteractionConflict
		}
		if !errors.Is(interactionErr, gorm.ErrRecordNotFound) {
			return interactionErr
		}
		if terminalRunStatus(agentruntime.RunStatus(run.Status)) || req.Revision != run.Revision+1 {
			return ErrInteractionConflict
		}

		index, err := nextStepIndex(tx, &run)
		if err != nil {
			return err
		}
		payload := interactionWaitPayload{
			Version: 1, InteractionID: req.InteractionID, Kind: req.InteractionKind,
			Revision: req.Revision, ExpiresAt: req.ExpiresAt, TokenHash: req.TokenHash,
			TrustedInput: normalizedTrustedInput(req.TrustedInput),
		}
		input, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		wait := &agentruntime.AgentStep{
			ID: req.StepID, RunID: req.RunID, Index: index,
			Kind: agentruntime.StepKindWait, Status: agentruntime.StepStatusCompleted,
			InputJSON: string(input), OutputJSON: "{}", ExternalRef: req.InteractionID,
			StartedAt: now, FinishedAt: now, CreatedAt: now,
			DedupeKey: interactionDedupeKey(req.InteractionID, req.Revision),
		}
		if err := tx.Create(toDBStep(wait)).Error; err != nil {
			return err
		}
		if err := insertProjectionOutbox(tx, wait.ID, req.Projection, now); err != nil {
			return err
		}

		status, reason := waitingState(req.InteractionKind)
		result := tx.Model(&model.AgentRun{}).Where("id = ?", req.RunID).Updates(map[string]any{
			"status":             string(status),
			"waiting_reason":     string(reason),
			"waiting_token":      req.TokenHash,
			"revision":           req.Revision,
			"current_step_index": index,
			"updated_at":         now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentruntime.ErrNotFound
		}
		result = tx.Model(&model.AgentSession{}).Where("id = ?", session.ID).
			Updates(map[string]any{"active_run_id": req.RunID, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentruntime.ErrNotFound
		}
		run.Status = string(status)
		run.WaitingReason = string(reason)
		run.WaitingToken = req.TokenHash
		run.Revision = req.Revision
		run.CurrentStepIndex = index
		run.UpdatedAt = now
		storedRun = toRuntimeRun(&run)
		storedStep = wait
		return nil
	})
	return storedRun, storedStep, err
}

func (r *Repository) ResolveInteraction(ctx context.Context, req agentruntime.ResolveInteractionRequest) (*agentruntime.AgentRun, *agentruntime.AgentStep, error) {
	if err := req.Validate(); err != nil {
		return nil, nil, err
	}
	event := agentruntime.ConversationEvent{
		ID: req.EventID, Type: agentruntime.EventTypeCardAction, RunID: req.RunID,
		InteractionID: req.InteractionID, Revision: req.Revision, Action: req.Action,
		SourceRef: req.SourceRef, OccurredAt: req.ResolvedAt, Payload: req.Outcome,
	}
	dedupeKey, err := event.DedupeKey()
	if err != nil {
		return nil, nil, err
	}

	var storedRun *agentruntime.AgentRun
	var storedStep *agentruntime.AgentStep
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run model.AgentRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, "id = ?", req.RunID).Error; err != nil {
			return mapNotFound(err)
		}
		var wait model.AgentStep
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&wait, "id = ?", req.StepID).Error; err != nil {
			return mapNotFound(err)
		}
		payload, err := validateWaitStep(&wait, req)
		if err != nil {
			return err
		}

		var existing model.AgentStep
		existingErr := tx.Where("run_id = ? AND dedupe_key = ?", req.RunID, dedupeKey).First(&existing).Error
		if existingErr == nil {
			if !resumeMatchesResolveRequest(&existing, req) {
				return ErrInteractionConflict
			}
			storedRun = toRuntimeRun(&run)
			storedStep = toRuntimeStep(&existing)
			return nil
		}
		if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}

		expectedStatus, _ := waitingState(payload.Kind)
		if run.Status != string(expectedStatus) || run.Revision != req.Revision ||
			run.WaitingReason == "" || run.WaitingToken == "" {
			return ErrInteractionConflict
		}
		if !req.ResolvedAt.Before(payload.ExpiresAt) {
			return ErrInteractionExpired
		}
		if !agentruntime.MatchInteractionToken(req.PresentedToken, run.WaitingToken) {
			return ErrInteractionTokenMismatch
		}

		index, err := nextStepIndex(tx, &run)
		if err != nil {
			return err
		}
		eventJSON, err := json.Marshal(event)
		if err != nil {
			return err
		}
		resume := &agentruntime.AgentStep{
			ID: stableResumeStepID(req.RunID, dedupeKey), RunID: req.RunID, Index: index,
			Kind: agentruntime.StepKindResume, Status: agentruntime.StepStatusCompleted,
			InputJSON: string(eventJSON), OutputJSON: string(req.Outcome),
			ExternalRef: req.InteractionID, StartedAt: req.ResolvedAt,
			FinishedAt: req.ResolvedAt, CreatedAt: req.ResolvedAt, DedupeKey: dedupeKey,
		}
		if err := tx.Create(toDBStep(resume)).Error; err != nil {
			return err
		}
		if err := insertProjectionOutbox(tx, resume.ID, req.Projection, req.ResolvedAt); err != nil {
			return err
		}
		result := tx.Model(&model.AgentRun{}).Where("id = ?", req.RunID).Updates(map[string]any{
			"status":             string(agentruntime.RunStatusQueued),
			"waiting_reason":     "",
			"waiting_token":      "",
			"revision":           req.Revision + 1,
			"current_step_index": index,
			"updated_at":         req.ResolvedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentruntime.ErrNotFound
		}
		run.Status = string(agentruntime.RunStatusQueued)
		run.WaitingReason = ""
		run.WaitingToken = ""
		run.Revision = req.Revision + 1
		run.CurrentStepIndex = index
		run.UpdatedAt = req.ResolvedAt
		storedRun = toRuntimeRun(&run)
		storedStep = resume
		return nil
	})
	return storedRun, storedStep, err
}

func resumeMatchesResolveRequest(existing *model.AgentStep, req agentruntime.ResolveInteractionRequest) bool {
	if existing.Kind != string(agentruntime.StepKindResume) ||
		existing.ExternalRef != req.InteractionID {
		return false
	}
	var event agentruntime.ConversationEvent
	if err := json.Unmarshal([]byte(existing.InputJSON), &event); err != nil {
		return false
	}
	return event.Type == agentruntime.EventTypeCardAction &&
		event.RunID == req.RunID &&
		event.InteractionID == req.InteractionID &&
		event.Revision == req.Revision &&
		event.Action == req.Action &&
		event.SourceRef == req.SourceRef
}

func sameInteractionStart(run *model.AgentRun, wait *model.AgentStep, req agentruntime.StartInteractionRequest) bool {
	if run.ID != req.RunID || terminalRunStatus(agentruntime.RunStatus(run.Status)) ||
		run.Revision != req.Revision || !strings.EqualFold(run.WaitingToken, req.TokenHash) ||
		wait.RunID != req.RunID || wait.Kind != string(agentruntime.StepKindWait) ||
		wait.Status != string(agentruntime.StepStatusCompleted) || wait.ExternalRef != req.InteractionID {
		return false
	}
	expectedStatus, expectedReason := waitingState(req.InteractionKind)
	if run.Status != string(expectedStatus) || run.WaitingReason != string(expectedReason) {
		return false
	}
	var payload interactionWaitPayload
	if err := json.Unmarshal([]byte(wait.InputJSON), &payload); err != nil {
		return false
	}
	return payload.Version == 1 && payload.InteractionID == req.InteractionID &&
		payload.Kind == req.InteractionKind && payload.Revision == req.Revision &&
		payload.ExpiresAt.Equal(req.ExpiresAt) && strings.EqualFold(payload.TokenHash, req.TokenHash) &&
		equalJSONDocument(payload.TrustedInput, normalizedTrustedInput(req.TrustedInput))
}

func normalizedTrustedInput(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), input...)
}

func equalJSONDocument(left, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func validateWaitStep(wait *model.AgentStep, req agentruntime.ResolveInteractionRequest) (*interactionWaitPayload, error) {
	if wait.RunID != req.RunID || wait.Kind != string(agentruntime.StepKindWait) ||
		wait.Status != string(agentruntime.StepStatusCompleted) || wait.ExternalRef != req.InteractionID {
		return nil, ErrInteractionConflict
	}
	var payload interactionWaitPayload
	if err := json.Unmarshal([]byte(wait.InputJSON), &payload); err != nil {
		return nil, ErrInteractionConflict
	}
	if payload.Version != 1 || payload.InteractionID != req.InteractionID ||
		payload.Revision != req.Revision || payload.ExpiresAt.IsZero() {
		return nil, ErrInteractionConflict
	}
	if !agentruntime.MatchInteractionToken(req.PresentedToken, payload.TokenHash) {
		return nil, ErrInteractionTokenMismatch
	}
	return &payload, nil
}

func nextStepIndex(tx *gorm.DB, run *model.AgentRun) (int32, error) {
	var maxIndex int32
	if err := tx.Model(&model.AgentStep{}).
		Select(`COALESCE(MAX("index"), -1)`).
		Where("run_id = ?", run.ID).
		Scan(&maxIndex).Error; err != nil {
		return 0, err
	}
	if run.CurrentStepIndex > maxIndex {
		maxIndex = run.CurrentStepIndex
	}
	return maxIndex + 1, nil
}

func waitingState(kind string) (agentruntime.RunStatus, agentruntime.WaitingReason) {
	switch kind {
	case "approval", "schedule_edit":
		return agentruntime.RunStatusWaitingApproval, agentruntime.WaitingReasonApproval
	case "schedule":
		return agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule
	default:
		return agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback
	}
}

func interactionDedupeKey(interactionID string, revision int64) string {
	return "interaction:" + interactionID + ":" + strconv.FormatInt(revision, 10)
}

func stableResumeStepID(runID, dedupeKey string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + dedupeKey))
	return "step_resume_" + hex.EncodeToString(sum[:])
}

func terminalRunStatus(status agentruntime.RunStatus) bool {
	switch status {
	case agentruntime.RunStatusCompleted, agentruntime.RunStatusFailed, agentruntime.RunStatusCancelled:
		return true
	default:
		return false
	}
}

func waitingRunStatus(status agentruntime.RunStatus) bool {
	switch status {
	case agentruntime.RunStatusWaitingApproval,
		agentruntime.RunStatusWaitingSchedule,
		agentruntime.RunStatusWaitingCallback:
		return true
	default:
		return false
	}
}
