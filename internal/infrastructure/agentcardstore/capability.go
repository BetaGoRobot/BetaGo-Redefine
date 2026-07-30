package agentcardstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ agentcard.CapabilityExecutionStore = (*Repository)(nil)

type storedCapabilityExecutionInput struct {
	Version       int                            `json:"version"`
	SourceStepID  string                         `json:"source_step_id"`
	InteractionID string                         `json:"interaction_id"`
	ActionID      string                         `json:"action_id"`
	Invocation    agentcard.CapabilityInvocation `json:"invocation"`
}

type storedCardActionSource struct {
	ActorOpenID string          `json:"actor_open_id"`
	ChatID      string          `json:"chat_id"`
	ActionID    string          `json:"action_id"`
	Outcome     json.RawMessage `json:"outcome"`
}

func (r *Repository) BeginCapabilityExecution(
	ctx context.Context,
	request agentcard.CapabilityExecutionRequest,
) (agentcard.CapabilityExecutionState, error) {
	if err := validateCapabilityExecutionRequest(request); err != nil {
		return agentcard.CapabilityExecutionState{}, err
	}
	var state agentcard.CapabilityExecutionState
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, run, step, source, surface, err := lockCapabilityExecution(
			tx,
			request,
		)
		if err != nil {
			return err
		}
		if err := validateCapabilityLease(
			session,
			run,
			step,
			request.Lease,
			request.StartedAt,
		); err != nil {
			return err
		}
		storedInvocation, err := agentcard.DecodeCapabilityInvocation(
			toCapabilityRuntimeStep(step),
		)
		if err != nil || !sameCapabilityInvocation(storedInvocation, request.Invocation) {
			return agentcard.ErrCardConflict
		}
		var actionSource storedCardActionSource
		if json.Unmarshal([]byte(source.InputJSON), &actionSource) != nil ||
			actionSource.ActorOpenID == "" ||
			actionSource.ChatID != surface.ChatID ||
			actionSource.ActionID != request.ActionID {
			return agentcard.ErrCardConflict
		}
		effectiveInvocation := storedInvocation
		if effectiveInvocation.ActorPolicy.Mode == agentcard.ActorPolicyAnyMember {
			effectiveInvocation.Permission.ActorOpenID = actionSource.ActorOpenID
		}
		if effectiveInvocation.Permission.ActorOpenID != actionSource.ActorOpenID {
			return agentcard.ErrCardConflict
		}
		input := storedCapabilityExecutionInput{
			Version: 1, SourceStepID: request.SourceStepID,
			InteractionID: request.InteractionID, ActionID: request.ActionID,
			Invocation: effectiveInvocation,
		}
		inputJSON, err := json.Marshal(input)
		if err != nil {
			return err
		}
		var execution model.AgentCapabilityExecution
		loadErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(
				&execution,
				"idempotency_key = ?",
				effectiveInvocation.IdempotencyKey,
			).Error
		switch {
		case loadErr == nil:
			if execution.RunID != request.RunID ||
				execution.CapabilityName != effectiveInvocation.Name ||
				!equalJSON([]byte(execution.InputJSON), inputJSON) {
				return agentcard.ErrCardConflict
			}
			switch execution.Status {
			case "completed", "failed":
				state.Terminal = true
				return nil
			case "running":
				if execution.StepID != request.StepID {
					return agentcard.ErrCardConflict
				}
			default:
				return agentcard.ErrCardConflict
			}
		case errors.Is(loadErr, gorm.ErrRecordNotFound):
			if err := tx.Create(&model.AgentCapabilityExecution{
				IdempotencyKey: effectiveInvocation.IdempotencyKey,
				RunID:          request.RunID, StepID: request.StepID,
				CapabilityName: effectiveInvocation.Name, Status: "running",
				InputJSON: string(inputJSON), OutputJSON: "{}",
				StartedAt: request.StartedAt, CreatedAt: request.StartedAt,
				UpdatedAt: request.StartedAt,
			}).Error; err != nil {
				return err
			}
		default:
			return loadErr
		}
		var spec agentcard.CardSpec
		if json.Unmarshal([]byte(surface.SpecJSON), &spec) != nil {
			return agentcard.ErrCardConflict
		}
		state.Invocation = &effectiveInvocation
		state.SurfaceSpec = &spec
		return nil
	})
	return state, err
}

func (r *Repository) CompleteCapabilityExecution(
	ctx context.Context,
	completion agentcard.CapabilityExecutionCompletion,
) error {
	if err := validateCapabilityCompletion(completion); err != nil {
		return err
	}
	request := completion.Request
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, run, step, source, surface, err := lockCapabilityExecution(
			tx,
			request,
		)
		if err != nil {
			return err
		}
		if err := validateCapabilityLease(
			session,
			run,
			step,
			request.Lease,
			completion.FinishedAt,
		); err != nil {
			return err
		}
		var execution model.AgentCapabilityExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(
				&execution,
				"idempotency_key = ?",
				request.Invocation.IdempotencyKey,
			).Error; err != nil {
			return mapStoreNotFound(err)
		}
		if execution.RunID != request.RunID ||
			execution.StepID != request.StepID ||
			execution.CapabilityName != request.Invocation.Name {
			return agentcard.ErrCardConflict
		}
		if execution.Status == "completed" || execution.Status == "failed" {
			return nil
		}
		if execution.Status != "running" {
			return agentcard.ErrCardConflict
		}
		var executionInput storedCapabilityExecutionInput
		if json.Unmarshal([]byte(execution.InputJSON), &executionInput) != nil ||
			executionInput.Version != 1 ||
			executionInput.SourceStepID != request.SourceStepID ||
			executionInput.InteractionID != request.InteractionID ||
			executionInput.ActionID != request.ActionID ||
			!sameCapabilityInvocation(
				executionInput.Invocation,
				request.Invocation,
			) {
			return agentcard.ErrCardConflict
		}
		outcome, err := capabilityOutcomeJSON(completion)
		if err != nil {
			return err
		}
		resultStep, _, continuation, err := persistCapabilityOutcome(
			tx,
			run,
			step,
			source,
			surface,
			completion,
			outcome,
		)
		if err != nil {
			return err
		}
		callStatus := agentruntime.StepStatusCompleted
		executionStatus := "completed"
		if !completion.Succeeded {
			callStatus = agentruntime.StepStatusFailed
			executionStatus = "failed"
		}
		callUpdate := tx.Model(&model.AgentStep{}).
			Where(
				"id = ? AND status = ? AND worker_id = ? AND attempt_count = ?",
				step.ID,
				string(agentruntime.StepStatusRunning),
				request.Lease.WorkerID,
				request.Lease.AttemptCount,
			).
			Updates(map[string]any{
				"status": string(callStatus), "output_json": string(outcome),
				"error_text":  completion.ErrorText,
				"finished_at": completion.FinishedAt,
				"worker_id":   "", "lease_expires_at": nil,
			})
		if callUpdate.Error != nil {
			return callUpdate.Error
		}
		if callUpdate.RowsAffected != 1 {
			return agentruntime.ErrLeaseLost
		}
		executionUpdate := tx.Model(&model.AgentCapabilityExecution{}).
			Where(
				"idempotency_key = ? AND status = ?",
				execution.IdempotencyKey,
				"running",
			).
			Updates(map[string]any{
				"step_id": resultStep.ID, "status": executionStatus,
				"output_json": string(completion.Output),
				"error_text":  completion.ErrorText,
				"finished_at": completion.FinishedAt,
				"updated_at":  completion.FinishedAt,
			})
		if executionUpdate.Error != nil {
			return executionUpdate.Error
		}
		if executionUpdate.RowsAffected != 1 {
			return agentruntime.ErrLeaseLost
		}
		surfaceStatus := completion.Request.Invocation.ResultPolicy.SuccessSurfaceState
		surfaceTimeColumn := "resolved_at"
		if !completion.Succeeded {
			surfaceStatus = completion.Request.Invocation.ResultPolicy.FailureSurfaceState
			surfaceTimeColumn = "failed_at"
		}
		surfaceUpdates := map[string]any{
			"status":                 string(surfaceStatus),
			"compiled_json_redacted": completion.CompiledJSONRedacted,
			"patch_status":           string(agentcard.PatchStatusPending),
			"next_patch_at":          completion.FinishedAt,
			"patch_worker_id":        "", "patch_lease_expires_at": nil,
			"last_error":      completion.ErrorText,
			"updated_at":      completion.FinishedAt,
			surfaceTimeColumn: completion.FinishedAt,
		}
		surfaceUpdate := tx.Model(&model.AgentCardSurface{}).
			Where(
				"id = ? AND revision = ? AND status = ?",
				surface.ID,
				surface.Revision,
				string(agentcard.SurfaceStatusProcessing),
			).
			Updates(surfaceUpdates)
		if surfaceUpdate.Error != nil {
			return surfaceUpdate.Error
		}
		if surfaceUpdate.RowsAffected != 1 {
			return agentcard.ErrCardConflict
		}
		runUpdate := tx.Model(&model.AgentRun{}).
			Where(
				"id = ? AND status = ?",
				run.ID,
				string(agentruntime.RunStatusRunning),
			).
			Updates(map[string]any{
				"status":             string(agentruntime.RunStatusQueued),
				"current_step_index": continuation.Index,
				"updated_at":         completion.FinishedAt,
			})
		if runUpdate.Error != nil {
			return runUpdate.Error
		}
		if runUpdate.RowsAffected != 1 {
			return agentruntime.ErrLeaseLost
		}
		return nil
	})
}

func lockCapabilityExecution(
	tx *gorm.DB,
	request agentcard.CapabilityExecutionRequest,
) (
	*model.AgentSession,
	*model.AgentRun,
	*model.AgentStep,
	*model.AgentStep,
	*model.AgentCardSurface,
	error,
) {
	var run model.AgentRun
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&run, "id = ?", request.RunID).Error; err != nil {
		return nil, nil, nil, nil, nil, mapStoreNotFound(err)
	}
	var session model.AgentSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&session, "id = ?", run.SessionID).Error; err != nil {
		return nil, nil, nil, nil, nil, mapStoreNotFound(err)
	}
	var step model.AgentStep
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&step, "id = ? AND run_id = ?", request.StepID, request.RunID).Error; err != nil {
		return nil, nil, nil, nil, nil, mapStoreNotFound(err)
	}
	var source model.AgentStep
	if err := tx.First(
		&source,
		"id = ? AND run_id = ? AND kind = ? AND status = ? AND \"index\" < ?",
		request.SourceStepID,
		request.RunID,
		string(agentruntime.StepKindCardAction),
		string(agentruntime.StepStatusCompleted),
		step.Index,
	).Error; err != nil {
		return nil, nil, nil, nil, nil, mapStoreNotFound(err)
	}
	var surface model.AgentCardSurface
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(
			&surface,
			"run_id = ? AND interaction_id = ?",
			request.RunID,
			request.InteractionID,
		).Error; err != nil {
		return nil, nil, nil, nil, nil, mapStoreNotFound(err)
	}
	if step.Kind != string(agentruntime.StepKindCapabilityCall) ||
		step.ExternalRef != request.InteractionID ||
		step.CapabilityName != request.Invocation.Name ||
		source.ExternalRef != request.InteractionID ||
		surface.Status != string(agentcard.SurfaceStatusProcessing) ||
		surface.LastActionID != request.ActionID {
		return nil, nil, nil, nil, nil, agentcard.ErrCardConflict
	}
	return &session, &run, &step, &source, &surface, nil
}

func validateCapabilityLease(
	session *model.AgentSession,
	run *model.AgentRun,
	step *model.AgentStep,
	lease agentruntime.StepLease,
	now time.Time,
) error {
	if session.ActiveRunID != run.ID ||
		run.Status != string(agentruntime.RunStatusRunning) ||
		step.Status != string(agentruntime.StepStatusRunning) ||
		step.WorkerID != lease.WorkerID ||
		step.AttemptCount != lease.AttemptCount ||
		!step.LeaseExpiresAt.After(now) {
		return agentruntime.ErrLeaseLost
	}
	return nil
}

func validateCapabilityExecutionRequest(
	request agentcard.CapabilityExecutionRequest,
) error {
	if strings.TrimSpace(request.StepID) == "" ||
		strings.TrimSpace(request.RunID) == "" ||
		strings.TrimSpace(request.SourceStepID) == "" ||
		strings.TrimSpace(request.InteractionID) == "" ||
		strings.TrimSpace(request.ActionID) == "" ||
		request.StartedAt.IsZero() ||
		request.Lease.StepID != request.StepID ||
		request.Invocation.Name == "" ||
		request.Invocation.IdempotencyKey == "" {
		return agentruntime.ErrInvalidRuntimeContract
	}
	return nil
}

func validateCapabilityCompletion(
	completion agentcard.CapabilityExecutionCompletion,
) error {
	if err := validateCapabilityExecutionRequest(completion.Request); err != nil {
		return err
	}
	if completion.FinishedAt.IsZero() ||
		!json.Valid(completion.Output) ||
		!json.Valid([]byte(completion.CompiledJSONRedacted)) ||
		jsonContainsToken([]byte(completion.CompiledJSONRedacted)) ||
		(completion.Succeeded && completion.ErrorText != "") ||
		(!completion.Succeeded && strings.TrimSpace(completion.ErrorText) == "") {
		return agentruntime.ErrInvalidRuntimeContract
	}
	return nil
}

func capabilityOutcomeJSON(
	completion agentcard.CapabilityExecutionCompletion,
) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"version":            1,
		"capability_name":    completion.Request.Invocation.Name,
		"capability_version": completion.Request.Invocation.Version,
		"interaction_id":     completion.Request.InteractionID,
		"action_id":          completion.Request.ActionID,
		"succeeded":          completion.Succeeded,
		"output":             json.RawMessage(completion.Output),
		"error":              completion.ErrorText,
		"finished_at":        completion.FinishedAt,
	})
}

func persistCapabilityOutcome(
	tx *gorm.DB,
	run *model.AgentRun,
	call *model.AgentStep,
	source *model.AgentStep,
	surface *model.AgentCardSurface,
	completion agentcard.CapabilityExecutionCompletion,
	outcome json.RawMessage,
) (*model.AgentStep, *model.AgentStep, *model.AgentStep, error) {
	index, err := nextCardStepIndex(tx, run)
	if err != nil {
		return nil, nil, nil, err
	}
	resultDedupe := call.DedupeKey + ":result"
	publicInput, err := json.Marshal(map[string]any{
		"version": 1, "source_step_id": source.ID,
		"interaction_id":  surface.InteractionID,
		"action_id":       completion.Request.ActionID,
		"capability_name": completion.Request.Invocation.Name,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	result := &model.AgentStep{
		ID:    stableCardStepID(run.ID, resultDedupe),
		RunID: run.ID, Index: index,
		Kind:           string(agentruntime.StepKindCapabilityResult),
		Status:         string(agentruntime.StepStatusCompleted),
		CapabilityName: completion.Request.Invocation.Name,
		InputJSON:      string(publicInput), OutputJSON: string(outcome),
		ExternalRef: surface.InteractionID,
		StartedAt:   completion.FinishedAt, FinishedAt: completion.FinishedAt,
		CreatedAt: completion.FinishedAt, DedupeKey: resultDedupe,
	}
	if err := tx.Create(result).Error; err != nil {
		return nil, nil, nil, err
	}
	resumeDedupe := resultDedupe + ":resume"
	resume := &model.AgentStep{
		ID:    stableCardStepID(run.ID, resumeDedupe),
		RunID: run.ID, Index: index + 1,
		Kind:      string(agentruntime.StepKindResume),
		Status:    string(agentruntime.StepStatusCompleted),
		InputJSON: source.InputJSON, OutputJSON: string(outcome),
		ExternalRef: surface.InteractionID,
		StartedAt:   completion.FinishedAt, FinishedAt: completion.FinishedAt,
		CreatedAt: completion.FinishedAt, DedupeKey: resumeDedupe,
	}
	if err := tx.Create(resume).Error; err != nil {
		return nil, nil, nil, err
	}
	continuationInput, err := json.Marshal(map[string]any{
		"version": 1, "source_step_id": resume.ID,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	continuation := &model.AgentStep{
		ID:    stableCardStepID(run.ID, resumeDedupe+":continuation"),
		RunID: run.ID, Index: index + 2,
		Kind:      string(agentruntime.StepKindDecide),
		Status:    string(agentruntime.StepStatusQueued),
		InputJSON: string(continuationInput), OutputJSON: "{}",
		CreatedAt: completion.FinishedAt,
		DedupeKey: resumeDedupe + ":continuation",
	}
	if err := tx.Create(continuation).Error; err != nil {
		return nil, nil, nil, err
	}
	var projectionSource model.AgentProjectionOutbox
	if err := tx.Where("step_id = ?", surface.WaitStepID).
		First(&projectionSource).Error; err != nil {
		return nil, nil, nil, err
	}
	documentID := strings.TrimSuffix(
		projectionSource.DocumentID,
		":"+surface.WaitStepID,
	)
	projectionPayload, err := json.Marshal(map[string]any{
		"schema_version": "1", "event_id": result.ID,
		"event_type": "capability_result", "run_id": run.ID,
		"step_id": result.ID, "source_step_id": source.ID,
		"chat_id":            surface.ChatID,
		"status":             map[bool]string{true: "completed", false: "failed"}[completion.Succeeded],
		"occurred_at":        completion.FinishedAt,
		"structured_payload": json.RawMessage(outcome),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if err := insertCardProjectionOutbox(
		tx,
		result.ID,
		agentruntime.ProjectionDocument{
			IndexAlias: projectionSource.IndexAlias,
			DocumentID: documentID,
			Payload:    projectionPayload,
		},
		completion.FinishedAt,
	); err != nil {
		return nil, nil, nil, err
	}
	resumeProjectionPayload, err := json.Marshal(map[string]any{
		"schema_version": "1", "event_id": resume.ID,
		"event_type": "capability_resume", "run_id": run.ID,
		"step_id": resume.ID, "source_step_id": result.ID,
		"chat_id": surface.ChatID,
		"status":  "completed", "occurred_at": completion.FinishedAt,
		"structured_payload": json.RawMessage(outcome),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if err := insertCardProjectionOutbox(
		tx,
		resume.ID,
		agentruntime.ProjectionDocument{
			IndexAlias: projectionSource.IndexAlias,
			DocumentID: documentID,
			Payload:    resumeProjectionPayload,
		},
		completion.FinishedAt,
	); err != nil {
		return nil, nil, nil, err
	}
	return result, resume, continuation, nil
}

func sameCapabilityInvocation(
	left, right agentcard.CapabilityInvocation,
) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && equalJSON(leftJSON, rightJSON)
}

func toCapabilityRuntimeStep(step *model.AgentStep) *agentruntime.AgentStep {
	return &agentruntime.AgentStep{
		ID: step.ID, RunID: step.RunID,
		Kind:           agentruntime.StepKind(step.Kind),
		Status:         agentruntime.StepStatus(step.Status),
		CapabilityName: step.CapabilityName,
		InputJSON:      step.InputJSON, OutputJSON: step.OutputJSON,
		ErrorText: step.ErrorText, ExternalRef: step.ExternalRef,
		StartedAt: step.StartedAt, FinishedAt: step.FinishedAt,
		CreatedAt: step.CreatedAt, DedupeKey: step.DedupeKey,
		AttemptCount: step.AttemptCount, WorkerID: step.WorkerID,
		LeaseExpiresAt: step.LeaseExpiresAt,
	}
}
