package agentstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ agentruntime.ScheduleInteractionStore = (*Repository)(nil)

type scheduleCapabilityClaimInput struct {
	Version      int             `json:"version"`
	ClaimID      string          `json:"claim_id"`
	Action       string          `json:"action"`
	ActorOpenID  string          `json:"actor_open_id"`
	TrustedInput json.RawMessage `json:"trusted_input"`
}

func (r *Repository) InspectScheduleInteraction(
	ctx context.Context,
	req agentruntime.ScheduleInteractionRequest,
) (agentruntime.ScheduleInteractionInspection, error) {
	if err := req.Validate(); err != nil {
		return agentruntime.ScheduleInteractionInspection{}, err
	}
	var wait model.AgentStep
	if err := r.db.WithContext(ctx).First(&wait, "id = ?", req.StepID).Error; err != nil {
		return agentruntime.ScheduleInteractionInspection{}, mapNotFound(err)
	}
	payload, err := validateScheduleInteractionWait(&wait, req)
	if err != nil {
		return agentruntime.ScheduleInteractionInspection{}, err
	}
	inspection := agentruntime.ScheduleInteractionInspection{
		TrustedInput: append(json.RawMessage(nil), payload.TrustedInput...),
	}
	var execution model.AgentCapabilityExecution
	err = r.db.WithContext(ctx).First(&execution, "idempotency_key = ?", scheduleInteractionKey(req)).Error
	switch {
	case err == nil && execution.Status == "completed":
		claimInput, decodeErr := decodeScheduleClaimInput(execution.InputJSON, payload.TrustedInput)
		if decodeErr != nil {
			return agentruntime.ScheduleInteractionInspection{}, decodeErr
		}
		outcome, decodeErr := decodeScheduleOutcome(execution.OutputJSON)
		if decodeErr != nil {
			return agentruntime.ScheduleInteractionInspection{}, decodeErr
		}
		trusted, decodeErr := agentruntime.DecodeScheduleEditTrustedInput(payload.TrustedInput)
		if decodeErr != nil {
			return agentruntime.ScheduleInteractionInspection{}, decodeErr
		}
		if decodeErr := validateScheduleOutcomeValues(
			outcome,
			agentruntime.ScheduleInteractionAction(claimInput.Action),
			req.InteractionID,
			trusted,
		); decodeErr != nil {
			return agentruntime.ScheduleInteractionInspection{}, decodeErr
		}
		inspection.CompletedOutcome = &outcome
		inspection.ResolvedActorOpenID = claimInput.ActorOpenID
	case err == nil:
	case errors.Is(err, gorm.ErrRecordNotFound):
	default:
		return agentruntime.ScheduleInteractionInspection{}, err
	}
	return inspection, nil
}

func (r *Repository) ClaimScheduleInteraction(
	ctx context.Context,
	req agentruntime.ScheduleInteractionRequest,
) (agentruntime.ScheduleInteractionClaim, error) {
	if err := req.Validate(); err != nil {
		return agentruntime.ScheduleInteractionClaim{}, err
	}
	var result agentruntime.ScheduleInteractionClaim
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		run, wait, payload, err := lockScheduleInteraction(tx, req)
		if err != nil {
			return err
		}
		var execution model.AgentCapabilityExecution
		executionErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&execution, "idempotency_key = ?", scheduleInteractionKey(req)).Error
		var storedClaim scheduleCapabilityClaimInput
		if executionErr == nil {
			storedClaim, err = decodeScheduleClaimInput(execution.InputJSON, payload.TrustedInput)
			if err != nil {
				return err
			}
			if execution.Status == "completed" {
				outcome, err := decodeScheduleOutcome(execution.OutputJSON)
				if err != nil {
					return err
				}
				trusted, err := agentruntime.DecodeScheduleEditTrustedInput(payload.TrustedInput)
				if err != nil {
					return err
				}
				if err := validateScheduleOutcomeValues(
					outcome,
					agentruntime.ScheduleInteractionAction(storedClaim.Action),
					req.InteractionID,
					trusted,
				); err != nil {
					return err
				}
				result = agentruntime.ScheduleInteractionClaim{
					State: agentruntime.ScheduleClaimCompleted, Outcome: outcome,
					ResolvedActorOpenID: storedClaim.ActorOpenID,
				}
				return nil
			}
			if storedClaim.Action != string(req.Action) {
				return agentruntime.ErrInteractionConflict
			}
			if execution.Status != "running" && execution.Status != "failed" {
				return agentruntime.ErrInteractionConflict
			}
		}
		if executionErr != nil && !errors.Is(executionErr, gorm.ErrRecordNotFound) {
			return executionErr
		}
		if executionErr == nil && execution.Status == "running" &&
			execution.UpdatedAt.Add(req.RunningTTL).After(req.ResolvedAt) {
			result = agentruntime.ScheduleInteractionClaim{State: agentruntime.ScheduleClaimRunning}
			return nil
		}
		if err := validateUnresolvedScheduleRun(run, payload, req); err != nil {
			return err
		}
		claimInput, err := json.Marshal(scheduleCapabilityClaimInput{
			Version: 1, ClaimID: req.ClaimID, Action: string(req.Action),
			ActorOpenID: req.ActorOpenID, TrustedInput: payload.TrustedInput,
		})
		if err != nil {
			return err
		}
		now := req.ResolvedAt
		if errors.Is(executionErr, gorm.ErrRecordNotFound) {
			execution = model.AgentCapabilityExecution{
				IdempotencyKey: scheduleInteractionKey(req), RunID: req.RunID, StepID: wait.ID,
				CapabilityName: "edit_schedule", Status: "running", InputJSON: string(claimInput),
				OutputJSON: "{}", StartedAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&execution).Error; err != nil {
				return err
			}
		} else {
			updates := map[string]any{
				"step_id": wait.ID, "status": "running", "input_json": string(claimInput),
				"output_json": "{}", "error_text": "", "started_at": now,
				"finished_at": nil, "updated_at": now,
			}
			if err := tx.Model(&model.AgentCapabilityExecution{}).
				Where("idempotency_key = ?", execution.IdempotencyKey).Updates(updates).Error; err != nil {
				return err
			}
		}
		result = agentruntime.ScheduleInteractionClaim{
			State:        agentruntime.ScheduleClaimAcquired,
			TrustedInput: append(json.RawMessage(nil), payload.TrustedInput...),
		}
		return nil
	})
	return result, err
}

func (r *Repository) CompleteScheduleInteraction(
	ctx context.Context,
	req agentruntime.CompleteScheduleInteractionRequest,
) (agentruntime.ScheduleInteractionOutcome, error) {
	if err := req.Request.Validate(); err != nil {
		return agentruntime.ScheduleInteractionOutcome{}, err
	}
	var outcomeJSON []byte
	var stored agentruntime.ScheduleInteractionOutcome
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		run, _, payload, err := lockScheduleInteraction(tx, req.Request)
		if err != nil {
			return err
		}
		var execution model.AgentCapabilityExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&execution, "idempotency_key = ?", scheduleInteractionKey(req.Request)).Error; err != nil {
			return mapNotFound(err)
		}
		claimInput, err := decodeScheduleClaimInput(execution.InputJSON, payload.TrustedInput)
		if err != nil {
			return err
		}
		if !scheduleClaimMatches(claimInput, req.Request) {
			return agentruntime.ErrScheduleInteractionClaimLost
		}
		trusted, err := agentruntime.DecodeScheduleEditTrustedInput(payload.TrustedInput)
		if err != nil {
			return err
		}
		if err := validateScheduleOutcome(req.Outcome, req.Request, trusted); err != nil {
			return err
		}
		if execution.Status == "completed" {
			stored, err = decodeScheduleOutcome(execution.OutputJSON)
			if err != nil {
				return err
			}
			return validateScheduleOutcomeValues(
				stored,
				agentruntime.ScheduleInteractionAction(claimInput.Action),
				req.Request.InteractionID,
				trusted,
			)
		}
		if execution.Status != "running" {
			return agentruntime.ErrScheduleInteractionClaimLost
		}
		if err := validateUnresolvedScheduleRun(run, payload, req.Request); err != nil {
			return err
		}
		outcomeJSON, err = json.Marshal(req.Outcome)
		if err != nil {
			return agentruntime.ErrInteractionConflict
		}
		firstIndex, err := nextStepIndex(tx, run)
		if err != nil {
			return err
		}
		event := agentruntime.ConversationEvent{
			ID: req.Request.EventID, Type: agentruntime.EventTypeCardAction,
			ChatID: trusted.ChatID, ActorOpenID: req.Request.ActorOpenID, RunID: req.Request.RunID,
			InteractionID: req.Request.InteractionID, Revision: req.Request.Revision,
			Action: string(req.Request.Action), SourceRef: req.Request.SourceRef,
			OccurredAt: req.Request.ResolvedAt, Payload: outcomeJSON,
		}
		eventJSON, err := json.Marshal(event)
		if err != nil {
			return err
		}
		cardStep := &agentruntime.AgentStep{
			ID:    stableScheduleStepID(req.Request.RunID, scheduleInteractionKey(req.Request), "card_action"),
			RunID: req.Request.RunID, Index: firstIndex, Kind: agentruntime.StepKindCardAction,
			Status: agentruntime.StepStatusCompleted, InputJSON: string(eventJSON),
			OutputJSON: string(outcomeJSON), ExternalRef: req.Request.InteractionID,
			StartedAt: req.Request.ResolvedAt, FinishedAt: req.Request.ResolvedAt,
			CreatedAt: req.Request.ResolvedAt, DedupeKey: scheduleInteractionKey(req.Request) + ":card_action",
		}
		if err := tx.Create(toDBStep(cardStep)).Error; err != nil {
			return err
		}
		resultStep := &agentruntime.AgentStep{
			ID:    stableScheduleStepID(req.Request.RunID, scheduleInteractionKey(req.Request), "capability_result"),
			RunID: req.Request.RunID, Index: firstIndex + 1, Kind: agentruntime.StepKindCapabilityResult,
			Status: agentruntime.StepStatusCompleted, CapabilityName: "edit_schedule",
			InputJSON: string(payload.TrustedInput), OutputJSON: string(outcomeJSON),
			ExternalRef: req.Request.InteractionID, StartedAt: req.Request.ResolvedAt,
			FinishedAt: req.Request.ResolvedAt, CreatedAt: req.Request.ResolvedAt,
			DedupeKey: scheduleInteractionKey(req.Request) + ":capability_result",
		}
		if err := tx.Create(toDBStep(resultStep)).Error; err != nil {
			return err
		}
		cardProjection, err := scheduleFactProjection(
			req.Request.Projection,
			cardStep.ID,
			string(agentruntime.StepKindCardAction),
			map[string]any{
				"run_id": req.Request.RunID, "interaction_id": req.Request.InteractionID,
				"revision": req.Request.Revision, "chat_id": trusted.ChatID,
				"actor_open_id": req.Request.ActorOpenID, "action": req.Request.Action,
				"status": req.Outcome.Status, "occurred_at": req.Request.ResolvedAt,
				"structured_payload": req.Outcome,
			},
		)
		if err != nil {
			return err
		}
		if err := insertProjectionOutbox(tx, cardStep.ID, cardProjection, req.Request.ResolvedAt); err != nil {
			return err
		}
		resultProjection, err := scheduleFactProjection(
			req.Request.Projection,
			resultStep.ID,
			string(agentruntime.StepKindCapabilityResult),
			map[string]any{
				"run_id": req.Request.RunID, "interaction_id": req.Request.InteractionID,
				"revision": req.Request.Revision, "chat_id": trusted.ChatID,
				"actor_open_id": req.Request.ActorOpenID, "action": req.Request.Action,
				"status": req.Outcome.Status, "occurred_at": req.Request.ResolvedAt,
				"capability_name": "edit_schedule", "structured_payload": req.Outcome,
			},
		)
		if err != nil {
			return err
		}
		if err := insertProjectionOutbox(tx, resultStep.ID, resultProjection, req.Request.ResolvedAt); err != nil {
			return err
		}
		runUpdate := tx.Model(&model.AgentRun{}).Where("id = ?", req.Request.RunID).Updates(map[string]any{
			"status": string(agentruntime.RunStatusQueued), "waiting_reason": "", "waiting_token": "",
			"revision": req.Request.Revision + 1, "current_step_index": firstIndex + 1,
			"updated_at": req.Request.ResolvedAt,
		})
		if runUpdate.Error != nil {
			return runUpdate.Error
		}
		if runUpdate.RowsAffected != 1 {
			return agentruntime.ErrNotFound
		}
		finishedAt := req.Request.ResolvedAt
		executionUpdate := tx.Model(&model.AgentCapabilityExecution{}).
			Where("idempotency_key = ? AND status = ?", execution.IdempotencyKey, "running").
			Updates(map[string]any{
				"step_id": resultStep.ID, "status": "completed", "output_json": string(outcomeJSON),
				"error_text": "", "finished_at": finishedAt, "updated_at": finishedAt,
			})
		if executionUpdate.Error != nil {
			return executionUpdate.Error
		}
		if executionUpdate.RowsAffected != 1 {
			return agentruntime.ErrScheduleInteractionClaimLost
		}
		stored = req.Outcome
		return nil
	})
	return stored, err
}

func (r *Repository) FailScheduleInteraction(
	ctx context.Context,
	req agentruntime.FailScheduleInteractionRequest,
) error {
	if err := req.Request.Validate(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, _, _, err := lockScheduleInteraction(tx, req.Request); err != nil {
			return err
		}
		var execution model.AgentCapabilityExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&execution, "idempotency_key = ?", scheduleInteractionKey(req.Request)).Error; err != nil {
			return mapNotFound(err)
		}
		claimInput, err := decodeScheduleClaimInput(execution.InputJSON, nil)
		if err != nil {
			return err
		}
		if !scheduleClaimMatches(claimInput, req.Request) {
			return agentruntime.ErrScheduleInteractionClaimLost
		}
		if execution.Status == "completed" {
			return nil
		}
		if execution.Status != "running" {
			return agentruntime.ErrScheduleInteractionClaimLost
		}
		return tx.Model(&model.AgentCapabilityExecution{}).
			Where("idempotency_key = ?", execution.IdempotencyKey).
			Updates(map[string]any{
				"status": "failed", "error_text": req.ErrorText, "updated_at": time.Now().UTC(),
			}).Error
	})
}

func lockScheduleInteraction(
	tx *gorm.DB,
	req agentruntime.ScheduleInteractionRequest,
) (*model.AgentRun, *model.AgentStep, *interactionWaitPayload, error) {
	var run model.AgentRun
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&run, "id = ?", req.RunID).Error; err != nil {
		return nil, nil, nil, mapNotFound(err)
	}
	var wait model.AgentStep
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&wait, "id = ?", req.StepID).Error; err != nil {
		return nil, nil, nil, mapNotFound(err)
	}
	payload, err := validateScheduleInteractionWait(&wait, req)
	return &run, &wait, payload, err
}

func validateScheduleInteractionWait(
	wait *model.AgentStep,
	req agentruntime.ScheduleInteractionRequest,
) (*interactionWaitPayload, error) {
	if wait.RunID != req.RunID || wait.Kind != string(agentruntime.StepKindWait) ||
		wait.Status != string(agentruntime.StepStatusCompleted) || wait.ExternalRef != req.InteractionID {
		return nil, agentruntime.ErrInteractionConflict
	}
	var payload interactionWaitPayload
	if json.Unmarshal([]byte(wait.InputJSON), &payload) != nil ||
		payload.Version != 1 || payload.Kind != "schedule_edit" ||
		payload.InteractionID != req.InteractionID || payload.Revision != req.Revision ||
		payload.ExpiresAt.IsZero() || len(payload.TrustedInput) == 0 {
		return nil, agentruntime.ErrInteractionConflict
	}
	if !agentruntime.MatchInteractionToken(req.PresentedToken, payload.TokenHash) {
		return nil, agentruntime.ErrInteractionTokenMismatch
	}
	if _, err := agentruntime.DecodeScheduleEditTrustedInput(payload.TrustedInput); err != nil {
		return nil, agentruntime.ErrInteractionConflict
	}
	return &payload, nil
}

func validateUnresolvedScheduleRun(
	run *model.AgentRun,
	payload *interactionWaitPayload,
	req agentruntime.ScheduleInteractionRequest,
) error {
	if run.Status != string(agentruntime.RunStatusWaitingApproval) ||
		run.WaitingReason != string(agentruntime.WaitingReasonApproval) ||
		run.Revision != req.Revision || run.WaitingToken == "" {
		return agentruntime.ErrInteractionConflict
	}
	if !req.ResolvedAt.Before(payload.ExpiresAt) {
		return agentruntime.ErrInteractionExpired
	}
	if !agentruntime.MatchInteractionToken(req.PresentedToken, run.WaitingToken) {
		return agentruntime.ErrInteractionTokenMismatch
	}
	return nil
}

func scheduleInteractionKey(req agentruntime.ScheduleInteractionRequest) string {
	return fmt.Sprintf("schedule_edit:%s:%s:%d", req.RunID, req.InteractionID, req.Revision)
}

func scheduleClaimMatches(
	claim scheduleCapabilityClaimInput,
	req agentruntime.ScheduleInteractionRequest,
) bool {
	return claim.ClaimID == req.ClaimID &&
		claim.Action == string(req.Action) &&
		claim.ActorOpenID == req.ActorOpenID
}

func decodeScheduleClaimInput(
	inputJSON string,
	expectedTrustedInput json.RawMessage,
) (scheduleCapabilityClaimInput, error) {
	var input scheduleCapabilityClaimInput
	if json.Unmarshal([]byte(inputJSON), &input) != nil ||
		input.Version != 1 ||
		strings.TrimSpace(input.ClaimID) == "" || strings.TrimSpace(input.ClaimID) != input.ClaimID ||
		strings.TrimSpace(input.ActorOpenID) == "" || strings.TrimSpace(input.ActorOpenID) != input.ActorOpenID ||
		(input.Action != string(agentruntime.ScheduleInteractionConfirm) &&
			input.Action != string(agentruntime.ScheduleInteractionCancel)) {
		return scheduleCapabilityClaimInput{}, agentruntime.ErrInteractionConflict
	}
	if _, err := agentruntime.DecodeScheduleEditTrustedInput(input.TrustedInput); err != nil {
		return scheduleCapabilityClaimInput{}, agentruntime.ErrInteractionConflict
	}
	if len(expectedTrustedInput) > 0 &&
		!equalJSONDocument(input.TrustedInput, expectedTrustedInput) {
		return scheduleCapabilityClaimInput{}, agentruntime.ErrInteractionConflict
	}
	return input, nil
}

func validateScheduleOutcome(
	outcome agentruntime.ScheduleInteractionOutcome,
	req agentruntime.ScheduleInteractionRequest,
	trusted agentruntime.ScheduleEditTrustedInput,
) error {
	return validateScheduleOutcomeValues(outcome, req.Action, req.InteractionID, trusted)
}

func validateScheduleOutcomeValues(
	outcome agentruntime.ScheduleInteractionOutcome,
	expectedAction agentruntime.ScheduleInteractionAction,
	expectedInteractionID string,
	trusted agentruntime.ScheduleEditTrustedInput,
) error {
	if outcome.TaskID != trusted.TaskID ||
		outcome.InteractionID != expectedInteractionID ||
		outcome.Action != expectedAction {
		return agentruntime.ErrInteractionConflict
	}
	switch expectedAction {
	case agentruntime.ScheduleInteractionConfirm:
		if outcome.Status != "updated" {
			return agentruntime.ErrInteractionConflict
		}
		var result struct {
			Status string          `json:"status"`
			TaskID string          `json:"task_id"`
			Name   json.RawMessage `json:"name,omitempty"`
		}
		if err := decodeStrictScheduleResultObject(outcome.Result, &result); err != nil ||
			result.Status != outcome.Status ||
			result.TaskID != outcome.TaskID {
			return agentruntime.ErrInteractionConflict
		}
		if len(result.Name) == 0 ||
			bytes.Equal(bytes.TrimSpace(result.Name), []byte("null")) {
			return agentruntime.ErrInteractionConflict
		}
		var name string
		if json.Unmarshal(result.Name, &name) != nil {
			return agentruntime.ErrInteractionConflict
		}
		if trusted.NewValues.Name != nil && name != *trusted.NewValues.Name {
			return agentruntime.ErrInteractionConflict
		}
	case agentruntime.ScheduleInteractionCancel:
		if outcome.Status != "cancelled_by_user" {
			return agentruntime.ErrInteractionConflict
		}
		var result map[string]json.RawMessage
		if err := decodeStrictScheduleResultObject(outcome.Result, &result); err != nil ||
			len(result) != 0 {
			return agentruntime.ErrInteractionConflict
		}
	default:
		return agentruntime.ErrInteractionConflict
	}
	return nil
}

func decodeStrictScheduleResultObject(raw json.RawMessage, destination any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return agentruntime.ErrInteractionConflict
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return agentruntime.ErrInteractionConflict
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return agentruntime.ErrInteractionConflict
	}
	return nil
}

func decodeScheduleOutcome(outputJSON string) (agentruntime.ScheduleInteractionOutcome, error) {
	var outcome agentruntime.ScheduleInteractionOutcome
	if err := json.Unmarshal([]byte(outputJSON), &outcome); err != nil ||
		outcome.Status == "" || outcome.TaskID == "" || outcome.InteractionID == "" {
		return agentruntime.ScheduleInteractionOutcome{}, agentruntime.ErrInteractionConflict
	}
	return outcome, nil
}

func stableScheduleStepID(runID, key, kind string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + key + "\x00" + kind))
	return "step_" + kind + "_" + hex.EncodeToString(sum[:])
}

func scheduleFactProjection(
	projection agentruntime.ProjectionDocument,
	stepID string,
	eventType string,
	fields map[string]any,
) (agentruntime.ProjectionDocument, error) {
	var base any
	if err := json.Unmarshal(projection.Payload, &base); err != nil {
		return agentruntime.ProjectionDocument{}, err
	}
	document, ok := base.(map[string]any)
	if !ok {
		document = map[string]any{"base_payload": base}
	}
	document["event_type"] = eventType
	document["step_id"] = stepID
	for key, value := range fields {
		document[key] = value
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return agentruntime.ProjectionDocument{}, err
	}
	projection.DocumentID += ":" + stepID
	projection.Payload = payload
	return projection, nil
}
