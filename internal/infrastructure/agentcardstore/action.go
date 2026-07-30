package agentcardstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) ClaimAction(
	ctx context.Context,
	request agentcard.ClaimActionRequest,
) (*agentcard.ActionClaim, error) {
	if err := validateActionClaimRequest(request); err != nil {
		return nil, err
	}
	var claim *agentcard.ActionClaim
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var surface model.AgentCardSurface
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"run_id = ? AND interaction_id = ?",
				request.RunID,
				request.InteractionID,
			).
			First(&surface).Error; err != nil {
			return mapStoreNotFound(err)
		}
		if surface.WaitStepID != request.StepID ||
			surface.Revision != request.ExpectedRevision ||
			surface.MessageID != request.MessageID ||
			surface.ChatID != request.ChatID ||
			surface.InteractionKind != request.InteractionKind {
			return agentcard.ErrCardConflict
		}
		var wait model.AgentStep
		if err := tx.First(&wait, "id = ?", request.StepID).Error; err != nil {
			return mapStoreNotFound(err)
		}
		var waitPayload runtimeWaitPayload
		if wait.RunID != request.RunID ||
			wait.Kind != string(agentruntime.StepKindWait) ||
			wait.Status != string(agentruntime.StepStatusCompleted) ||
			wait.ExternalRef != request.InteractionID ||
			json.Unmarshal([]byte(wait.InputJSON), &waitPayload) != nil ||
			waitPayload.InteractionID != request.InteractionID ||
			waitPayload.Revision != request.ExpectedRevision ||
			waitPayload.Kind != request.InteractionKind {
			return agentcard.ErrCardConflict
		}
		if !agentruntime.MatchInteractionToken(
			request.PresentedToken,
			waitPayload.TokenHash,
		) {
			return agentruntime.ErrInteractionTokenMismatch
		}
		if !request.ClaimedAt.Before(waitPayload.ExpiresAt) {
			return agentruntime.ErrInteractionExpired
		}
		var trusted agentcard.TrustedWaitInput
		if json.Unmarshal(waitPayload.TrustedInput, &trusted) != nil ||
			trusted.Version != 1 {
			return agentcard.ErrCardConflict
		}
		if trusted.ActorPolicy.Mode == agentcard.ActorPolicyOwner &&
			trusted.ActorPolicy.OpenID != request.ActorOpenID {
			return agentcard.ErrCardConflict
		}
		if surface.ExpectedActorOpenID != "" &&
			surface.ExpectedActorOpenID != request.ActorOpenID {
			return agentcard.ErrCardConflict
		}
		descriptor, ok := trustedDescriptor(trusted, request.ActionID)
		if !ok || descriptor.ContinueAgent != request.ContinueAgent {
			return agentcard.ErrCardConflict
		}
		var spec agentcard.CardSpec
		if json.Unmarshal([]byte(surface.SpecJSON), &spec) != nil {
			return agentcard.ErrCardConflict
		}
		publicAction, ok := publicActionByID(spec, request.ActionID)
		if !ok || publicAction.Mode != descriptor.Mode ||
			publicAction.Intent != descriptor.Intent ||
			expectedClaimStatus(publicAction) != request.DesiredStatus {
			return agentcard.ErrCardConflict
		}
		outcome, err := agentcard.NormalizeActionOutcome(
			spec,
			request.ActionID,
			request.FormValues,
			request.InputName,
			request.InputValue,
			request.SelectedOption,
			request.SelectedOptions,
			request.Checked,
		)
		if err != nil {
			return err
		}

		if surface.Status != string(agentcard.SurfaceStatusSent) {
			if surface.Status == string(request.DesiredStatus) &&
				surface.LastActionID == request.ActionID &&
				surface.LastSourceRef == request.SourceRef {
				claim = &agentcard.ActionClaim{
					Surface:    toApplicationSurface(&surface),
					Descriptor: descriptor, Outcome: outcome, Replay: true,
				}
				return nil
			}
			return agentcard.ErrCardConflict
		}
		if !json.Valid([]byte(request.CompiledJSONRedacted)) ||
			jsonContainsToken([]byte(request.CompiledJSONRedacted)) {
			return agentcard.ErrCardConflict
		}
		var run model.AgentRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&run, "id = ?", request.RunID).Error; err != nil {
			return mapStoreNotFound(err)
		}
		expectedRunStatus, expectedReason := cardWaitingState(request.InteractionKind)
		if run.Status != string(expectedRunStatus) ||
			run.WaitingReason != string(expectedReason) ||
			run.Revision != request.ExpectedRevision ||
			run.WaitingToken == "" {
			return agentcard.ErrCardConflict
		}
		event, nextStep, err := persistClaimedAction(
			tx,
			&run,
			&surface,
			descriptor,
			spec,
			publicAction,
			request,
			outcome,
		)
		if err != nil {
			return err
		}
		if err := updateClaimedSurface(
			tx,
			&surface,
			request,
		); err != nil {
			return err
		}
		if err := updateRunAfterClaim(
			tx,
			&run,
			descriptor,
			request,
			event,
			nextStep,
		); err != nil {
			return err
		}
		if err := tx.First(&surface, "id = ?", surface.ID).Error; err != nil {
			return mapStoreNotFound(err)
		}
		claim = &agentcard.ActionClaim{
			Surface:    toApplicationSurface(&surface),
			Descriptor: descriptor, Outcome: outcome,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claim, nil
}

func persistClaimedAction(
	tx *gorm.DB,
	run *model.AgentRun,
	surface *model.AgentCardSurface,
	descriptor agentcard.TrustedActionDescriptor,
	spec agentcard.CardSpec,
	publicAction agentcard.Action,
	request agentcard.ClaimActionRequest,
	outcome json.RawMessage,
) (*model.AgentStep, *model.AgentStep, error) {
	index, err := nextCardStepIndex(tx, run)
	if err != nil {
		return nil, nil, err
	}
	dedupeBase := "agent_card_action:" + surface.InteractionID + ":" +
		request.SourceRef
	eventID := stableCardStepID(run.ID, dedupeBase+":event")
	eventPayload, err := json.Marshal(map[string]any{
		"id": eventID, "type": string(agentruntime.EventTypeCardAction),
		"version": 1, "event_id": request.EventID,
		"event_type": "agent_card_action", "run_id": run.ID,
		"interaction_id": surface.InteractionID,
		"revision":       surface.Revision, "action": request.ActionID,
		"action_id": request.ActionID, "intent": descriptor.Intent,
		"action_label":  publicAction.Label,
		"actor_open_id": request.ActorOpenID, "message_id": request.MessageID,
		"chat_id": request.ChatID, "source_ref": request.SourceRef,
		"occurred_at": request.ClaimedAt,
		"payload":     json.RawMessage(outcome), "outcome": json.RawMessage(outcome),
		"form_labels": publicFormLabels(spec, publicAction.FormRef),
		"context_refs": map[string]string{
			"wait_step_id":        surface.WaitStepID,
			"card_message_id":     request.MessageID,
			"reply_to_message_id": surface.ReplyToMessageID,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	event := &model.AgentStep{
		ID:    eventID,
		RunID: run.ID, Index: index,
		Kind:      string(agentruntime.StepKindCardAction),
		Status:    string(agentruntime.StepStatusCompleted),
		InputJSON: string(eventPayload), OutputJSON: string(outcome),
		ExternalRef: surface.InteractionID,
		StartedAt:   request.ClaimedAt, FinishedAt: request.ClaimedAt,
		CreatedAt: request.ClaimedAt, DedupeKey: dedupeBase + ":event",
	}
	if err := tx.Create(event).Error; err != nil {
		return nil, nil, err
	}
	if err := insertActionProjection(
		tx,
		surface,
		event,
		request,
		outcome,
	); err != nil {
		return nil, nil, err
	}
	var next *model.AgentStep
	switch descriptor.Mode {
	case agentcard.ActionModeUI:
		resumeDedupe := dedupeBase + ":resume"
		resume := &model.AgentStep{
			ID:    stableCardStepID(run.ID, resumeDedupe),
			RunID: run.ID, Index: index + 1,
			Kind:      string(agentruntime.StepKindResume),
			Status:    string(agentruntime.StepStatusCompleted),
			InputJSON: string(eventPayload), OutputJSON: string(outcome),
			ExternalRef: surface.InteractionID,
			StartedAt:   request.ClaimedAt, FinishedAt: request.ClaimedAt,
			CreatedAt: request.ClaimedAt, DedupeKey: resumeDedupe,
		}
		if err := tx.Create(resume).Error; err != nil {
			return nil, nil, err
		}
		if err := insertActionProjection(
			tx,
			surface,
			resume,
			request,
			outcome,
		); err != nil {
			return nil, nil, err
		}
		input, err := json.Marshal(map[string]any{
			"version": 1, "source_step_id": resume.ID,
		})
		if err != nil {
			return nil, nil, err
		}
		next = &model.AgentStep{
			ID:    stableCardStepID(run.ID, resumeDedupe+":continuation"),
			RunID: run.ID, Index: index + 2,
			Kind:      string(agentruntime.StepKindDecide),
			Status:    string(agentruntime.StepStatusQueued),
			InputJSON: string(input), OutputJSON: "{}",
			CreatedAt: request.ClaimedAt,
			DedupeKey: resumeDedupe + ":continuation",
		}
	case agentcard.ActionModeCapabilityConfirm:
		input, err := json.Marshal(map[string]any{
			"version": 1, "source_step_id": event.ID,
			"interaction_id": surface.InteractionID,
			"action_id":      request.ActionID, "descriptor": descriptor,
		})
		if err != nil {
			return nil, nil, err
		}
		next = &model.AgentStep{
			ID:    stableCardStepID(run.ID, dedupeBase+":capability"),
			RunID: run.ID, Index: index + 1,
			Kind:           string(agentruntime.StepKindCapabilityCall),
			Status:         string(agentruntime.StepStatusQueued),
			CapabilityName: descriptor.CapabilityName,
			InputJSON:      string(input), OutputJSON: "{}",
			ExternalRef: surface.InteractionID,
			CreatedAt:   request.ClaimedAt,
			DedupeKey:   dedupeBase + ":capability",
		}
	}
	if next != nil {
		if err := tx.Create(next).Error; err != nil {
			return nil, nil, err
		}
	}
	return event, next, nil
}

func publicFormLabels(
	spec agentcard.CardSpec,
	formID string,
) map[string]string {
	labels := make(map[string]string)
	if formID == "" {
		return labels
	}
	var visit func([]agentcard.Block)
	visit = func(blocks []agentcard.Block) {
		for _, block := range blocks {
			switch block.Kind {
			case agentcard.BlockTextInput:
				if block.TextInput != nil &&
					block.TextInput.Field.FormID == formID {
					labels[block.TextInput.Field.FieldID] =
						block.TextInput.Field.Label
				}
			case agentcard.BlockSingleSelect:
				if block.SingleSelect != nil &&
					block.SingleSelect.Field.FormID == formID {
					labels[block.SingleSelect.Field.FieldID] =
						block.SingleSelect.Field.Label
				}
			case agentcard.BlockMultiSelect:
				if block.MultiSelect != nil &&
					block.MultiSelect.Field.FormID == formID {
					labels[block.MultiSelect.Field.FieldID] =
						block.MultiSelect.Field.Label
				}
			case agentcard.BlockColumns:
				if block.Columns != nil {
					for _, column := range block.Columns.Columns {
						visit(column.Blocks)
					}
				}
			case agentcard.BlockSection:
				if block.Section != nil {
					visit(block.Section.Blocks)
				}
			}
		}
	}
	visit(spec.Blocks)
	return labels
}

func insertActionProjection(
	tx *gorm.DB,
	surface *model.AgentCardSurface,
	event *model.AgentStep,
	request agentcard.ClaimActionRequest,
	outcome json.RawMessage,
) error {
	var source model.AgentProjectionOutbox
	if err := tx.Where("step_id = ?", surface.WaitStepID).
		First(&source).Error; err != nil {
		return err
	}
	documentID := strings.TrimSuffix(source.DocumentID, ":"+surface.WaitStepID)
	payload, err := json.Marshal(map[string]any{
		"schema_version": "1", "event_id": event.ID,
		"event_type": "agent_card_action", "run_id": surface.RunID,
		"step_id": event.ID, "source_step_id": surface.WaitStepID,
		"chat_id": request.ChatID, "actor_open_id": request.ActorOpenID,
		"status": "completed", "occurred_at": request.ClaimedAt,
		"structured_payload": map[string]any{
			"surface_id": surface.ID, "interaction_id": surface.InteractionID,
			"revision": surface.Revision, "action_id": request.ActionID,
			"outcome": json.RawMessage(outcome),
		},
	})
	if err != nil {
		return err
	}
	return insertCardProjectionOutbox(tx, event.ID, agentruntime.ProjectionDocument{
		IndexAlias: source.IndexAlias, DocumentID: documentID, Payload: payload,
	}, request.ClaimedAt)
}

func updateClaimedSurface(
	tx *gorm.DB,
	surface *model.AgentCardSurface,
	request agentcard.ClaimActionRequest,
) error {
	updates := map[string]any{
		"status":                 string(request.DesiredStatus),
		"compiled_json_redacted": request.CompiledJSONRedacted,
		"last_action_id":         request.ActionID,
		"last_source_ref":        request.SourceRef,
		"patch_status":           string(agentcard.PatchStatusPending),
		"next_patch_at":          request.ClaimedAt,
		"patch_worker_id":        "", "patch_lease_expires_at": nil,
		"last_error": "", "updated_at": request.ClaimedAt,
	}
	switch request.DesiredStatus {
	case agentcard.SurfaceStatusSubmitted:
		updates["submitted_at"] = request.ClaimedAt
	case agentcard.SurfaceStatusProcessing:
		updates["submitted_at"] = request.ClaimedAt
		updates["processing_at"] = request.ClaimedAt
	case agentcard.SurfaceStatusResolved:
		updates["resolved_at"] = request.ClaimedAt
	case agentcard.SurfaceStatusCancelled:
		updates["cancelled_at"] = request.ClaimedAt
	}
	result := tx.Model(&model.AgentCardSurface{}).
		Where(
			"id = ? AND revision = ? AND status = ?",
			surface.ID,
			request.ExpectedRevision,
			string(agentcard.SurfaceStatusSent),
		).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agentcard.ErrCardConflict
	}
	return nil
}

func updateRunAfterClaim(
	tx *gorm.DB,
	run *model.AgentRun,
	descriptor agentcard.TrustedActionDescriptor,
	request agentcard.ClaimActionRequest,
	event *model.AgentStep,
	next *model.AgentStep,
) error {
	updates := map[string]any{
		"waiting_reason": "", "waiting_token": "",
		"revision":         request.ExpectedRevision + 1,
		"updated_at":       request.ClaimedAt,
		"last_relevant_at": request.ClaimedAt,
	}
	if descriptor.Mode == agentcard.ActionModeServer {
		updates["status"] = string(agentruntime.RunStatusCompleted)
		updates["finished_at"] = request.ClaimedAt
		updates["current_step_index"] = event.Index
		updates["result_summary"] = "agent card server action completed"
	} else {
		if next == nil {
			return agentcard.ErrCardConflict
		}
		updates["status"] = string(agentruntime.RunStatusQueued)
		updates["current_step_index"] = next.Index
	}
	result := tx.Model(&model.AgentRun{}).
		Where("id = ? AND revision = ?", run.ID, request.ExpectedRevision).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agentcard.ErrCardConflict
	}
	return nil
}

func validateActionClaimRequest(request agentcard.ClaimActionRequest) error {
	for _, value := range []string{
		request.RunID, request.StepID, request.InteractionID,
		request.ActionID, request.ActorOpenID, request.MessageID,
		request.ChatID, request.PresentedToken, request.InteractionKind,
		request.SourceRef,
	} {
		if strings.TrimSpace(value) == "" {
			return errors.New("invalid agent card action claim")
		}
	}
	if request.ExpectedRevision <= 0 || request.ClaimedAt.IsZero() ||
		!json.Valid([]byte(request.CompiledJSONRedacted)) {
		return errors.New("invalid agent card action claim")
	}
	return nil
}

func trustedDescriptor(
	input agentcard.TrustedWaitInput,
	actionID string,
) (agentcard.TrustedActionDescriptor, bool) {
	for _, descriptor := range input.ActionBindings {
		if descriptor.ActionID == actionID {
			return descriptor, true
		}
	}
	return agentcard.TrustedActionDescriptor{}, false
}

func publicActionByID(
	spec agentcard.CardSpec,
	actionID string,
) (agentcard.Action, bool) {
	for _, action := range spec.Actions {
		if action.ID == actionID {
			return action, true
		}
	}
	return agentcard.Action{}, false
}

func expectedClaimStatus(action agentcard.Action) agentcard.SurfaceStatus {
	if action.Kind == agentcard.ActionCancel {
		return agentcard.SurfaceStatusCancelled
	}
	switch action.Mode {
	case agentcard.ActionModeCapabilityConfirm:
		return agentcard.SurfaceStatusProcessing
	case agentcard.ActionModeServer:
		return agentcard.SurfaceStatusResolved
	default:
		return agentcard.SurfaceStatusSubmitted
	}
}
