package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CompleteRunWithReplyInput struct {
	RunID             string                    `json:"run_id"`
	Revision          int64                     `json:"revision"`
	CapabilityCalls   []CompletedCapabilityCall `json:"capability_calls,omitempty"`
	ThoughtText       string                    `json:"thought_text,omitempty"`
	ReplyText         string                    `json:"reply_text,omitempty"`
	ResponseMessageID string                    `json:"response_message_id,omitempty"`
	ResponseCardID    string                    `json:"response_card_id,omitempty"`
	DeliveryMode      ReplyDeliveryMode         `json:"delivery_mode,omitempty"`
	TargetMessageID   string                    `json:"target_message_id,omitempty"`
	TargetCardID      string                    `json:"target_card_id,omitempty"`
	TargetStepID      string                    `json:"target_step_id,omitempty"`
	CompletedAt       time.Time                 `json:"completed_at,omitempty"`
}

type ContinueRunWithReplyInput struct {
	RunID             string                    `json:"run_id"`
	Revision          int64                     `json:"revision"`
	CapabilityCalls   []CompletedCapabilityCall `json:"capability_calls,omitempty"`
	ThoughtText       string                    `json:"thought_text,omitempty"`
	ReplyText         string                    `json:"reply_text,omitempty"`
	ResponseMessageID string                    `json:"response_message_id,omitempty"`
	ResponseCardID    string                    `json:"response_card_id,omitempty"`
	DeliveryMode      ReplyDeliveryMode         `json:"delivery_mode,omitempty"`
	TargetMessageID   string                    `json:"target_message_id,omitempty"`
	TargetCardID      string                    `json:"target_card_id,omitempty"`
	TargetStepID      string                    `json:"target_step_id,omitempty"`
	QueuedCapability  *QueuedCapabilityCall     `json:"queued_capability,omitempty"`
	ContinuedAt       time.Time                 `json:"continued_at,omitempty"`
}

type QueuePlanStepInput struct {
	RunID             string                 `json:"run_id"`
	Revision          int64                  `json:"revision"`
	FromStepIndex     int                    `json:"from_step_index,omitempty"`
	ThoughtText       string                 `json:"thought_text,omitempty"`
	ReplyText         string                 `json:"reply_text,omitempty"`
	PendingCapability *PlanPendingCapability `json:"pending_capability,omitempty"`
	PlannedAt         time.Time              `json:"planned_at,omitempty"`
}

type CompletedCapabilityCall struct {
	CallID             string `json:"call_id,omitempty"`
	CapabilityName     string `json:"capability_name,omitempty"`
	Arguments          string `json:"arguments,omitempty"`
	Output             string `json:"output,omitempty"`
	PreviousResponseID string `json:"previous_response_id,omitempty"`
}

type QueuedCapabilityCall struct {
	CallID         string              `json:"call_id,omitempty"`
	CapabilityName string              `json:"capability_name,omitempty"`
	Input          CapabilityCallInput `json:"input"`
}

type replyCompletionOutput struct {
	ThoughtText       string              `json:"thought_text,omitempty"`
	ReplyText         string              `json:"reply_text,omitempty"`
	ResponseMessageID string              `json:"response_message_id,omitempty"`
	ResponseCardID    string              `json:"response_card_id,omitempty"`
	DeliveryMode      ReplyDeliveryMode   `json:"delivery_mode,omitempty"`
	LifecycleState    ReplyLifecycleState `json:"lifecycle_state,omitempty"`
	TargetMessageID   string              `json:"target_message_id,omitempty"`
	TargetCardID      string              `json:"target_card_id,omitempty"`
	TargetStepID      string              `json:"target_step_id,omitempty"`
}

type PlanPendingCapability struct {
	CallID         string `json:"call_id,omitempty"`
	CapabilityName string `json:"capability_name,omitempty"`
	Arguments      string `json:"arguments,omitempty"`
}

type replyPlanStepInput struct {
	ThoughtText       string                 `json:"thought_text,omitempty"`
	ReplyText         string                 `json:"reply_text,omitempty"`
	PendingCapability *PlanPendingCapability `json:"pending_capability,omitempty"`
}

func (c *RunCoordinator) QueuePlanStep(ctx context.Context, input QueuePlanStepInput) (*AgentRun, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	if strings.TrimSpace(input.RunID) == "" {
		return nil, fmt.Errorf("queue plan step run_id is required")
	}
	if input.Revision < 0 {
		return nil, fmt.Errorf("queue plan step revision must be >= 0")
	}
	if strings.TrimSpace(input.ThoughtText) == "" &&
		strings.TrimSpace(input.ReplyText) == "" &&
		input.PendingCapability == nil {
		return c.runRepo.GetByID(ctx, strings.TrimSpace(input.RunID))
	}

	plannedAt := input.PlannedAt
	if plannedAt.IsZero() {
		plannedAt = time.Now().UTC()
	} else {
		plannedAt = plannedAt.UTC()
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, err
	}
	if run.Status.IsTerminal() {
		return run, nil
	}

	runningRun, err := c.moveRunToRunning(ctx, run, plannedAt)
	if err != nil {
		return nil, err
	}

	steps, err := c.stepRepo.ListByRun(ctx, runningRun.ID)
	if err != nil {
		return nil, err
	}
	if currentStep := findStepByIndex(steps, runningRun.CurrentStepIndex); currentStep != nil && currentStep.Kind == StepKindPlan {
		return runningRun, nil
	}

	if sourceStep := findStepByIndex(steps, input.FromStepIndex); sourceStep != nil && !sourceStep.Status.IsTerminal() {
		if err := c.completeCurrentStep(ctx, sourceStep, plannedAt); err != nil {
			return nil, err
		}
	}

	nextStepIndex := nextAvailableStepIndex(steps, maxStepIndex(runningRun.CurrentStepIndex, input.FromStepIndex))
	planInput, err := json.Marshal(replyPlanStepInput{
		ThoughtText:       strings.TrimSpace(input.ThoughtText),
		ReplyText:         strings.TrimSpace(input.ReplyText),
		PendingCapability: clonePlanPendingCapability(input.PendingCapability),
	})
	if err != nil {
		return nil, err
	}
	planStep := &AgentStep{
		ID:        newRuntimeID("step"),
		RunID:     runningRun.ID,
		Index:     nextStepIndex,
		Kind:      StepKindPlan,
		Status:    StepStatusQueued,
		InputJSON: planInput,
		CreatedAt: plannedAt,
	}
	if err := c.stepRepo.Append(ctx, planStep); err != nil {
		return nil, err
	}

	return c.runRepo.UpdateStatus(ctx, runningRun.ID, runningRun.Revision, func(current *AgentRun) error {
		current.Status = RunStatusRunning
		current.CurrentStepIndex = planStep.Index
		current.UpdatedAt = plannedAt
		if current.StartedAt == nil {
			current.StartedAt = &plannedAt
		}
		return nil
	})
}

func (c *RunCoordinator) CompleteRunWithReply(ctx context.Context, input CompleteRunWithReplyInput) (*AgentRun, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	if strings.TrimSpace(input.RunID) == "" {
		return nil, fmt.Errorf("complete reply run_id is required")
	}
	if input.Revision < 0 {
		return nil, fmt.Errorf("complete reply revision must be >= 0")
	}

	completedAt := input.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	} else {
		completedAt = completedAt.UTC()
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, err
	}
	if run.Status.IsTerminal() {
		return run, nil
	}

	runningRun, err := c.moveRunToRunning(ctx, run, completedAt)
	if err != nil {
		return nil, err
	}

	steps, err := c.stepRepo.ListByRun(ctx, runningRun.ID)
	if err != nil {
		return nil, err
	}
	currentStep := findStepByIndex(steps, runningRun.CurrentStepIndex)
	if currentStep == nil {
		return nil, fmt.Errorf("agent runtime current step missing: run_id=%s index=%d", runningRun.ID, runningRun.CurrentStepIndex)
	}
	if err := c.completeCurrentStep(ctx, currentStep, completedAt); err != nil {
		return nil, err
	}

	nextStepIndex := nextAvailableStepIndex(steps, currentStep.Index)
	for _, call := range input.CapabilityCalls {
		step, err := newCompletedCapabilityStep(runningRun.ID, nextStepIndex, call, completedAt)
		if err != nil {
			return nil, err
		}
		if err := c.stepRepo.Append(ctx, step); err != nil {
			return nil, err
		}
		nextStepIndex++

		observeStep, err := newCompletedCapabilityObserveStep(runningRun.ID, nextStepIndex, call, completedAt)
		if err != nil {
			return nil, err
		}
		if err := c.stepRepo.Append(ctx, observeStep); err != nil {
			return nil, err
		}
		nextStepIndex++
	}

	replyOutput, err := json.Marshal(replyCompletionOutput{
		ThoughtText:       strings.TrimSpace(input.ThoughtText),
		ReplyText:         strings.TrimSpace(input.ReplyText),
		ResponseMessageID: strings.TrimSpace(input.ResponseMessageID),
		ResponseCardID:    strings.TrimSpace(input.ResponseCardID),
		DeliveryMode:      input.DeliveryMode,
		LifecycleState:    ReplyLifecycleStateActive,
		TargetMessageID:   strings.TrimSpace(input.TargetMessageID),
		TargetCardID:      strings.TrimSpace(input.TargetCardID),
		TargetStepID:      strings.TrimSpace(input.TargetStepID),
	})
	if err != nil {
		return nil, err
	}

	replyExternalRef := strings.TrimSpace(input.ResponseCardID)
	if replyExternalRef == "" {
		replyExternalRef = strings.TrimSpace(input.ResponseMessageID)
	}
	replyStep := &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       runningRun.ID,
		Index:       nextStepIndex,
		Kind:        StepKindReply,
		Status:      StepStatusCompleted,
		OutputJSON:  replyOutput,
		ExternalRef: replyExternalRef,
		CreatedAt:   completedAt,
		StartedAt:   &completedAt,
		FinishedAt:  &completedAt,
	}
	if err := c.stepRepo.Append(ctx, replyStep); err != nil {
		return nil, err
	}

	resultSummary := strings.TrimSpace(input.ReplyText)
	if resultSummary == "" {
		resultSummary = "runtime reply completed"
	}
	completedRun, err := c.runRepo.UpdateStatus(ctx, runningRun.ID, runningRun.Revision, func(current *AgentRun) error {
		current.Status = RunStatusCompleted
		current.CurrentStepIndex = replyStep.Index
		current.LastResponseID = strings.TrimSpace(input.ResponseMessageID)
		current.ResultSummary = resultSummary
		current.ErrorText = ""
		current.UpdatedAt = completedAt
		current.FinishedAt = &completedAt
		if current.StartedAt == nil {
			current.StartedAt = &completedAt
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := c.clearFinishedRunState(ctx, completedRun.ID, completedRun.SessionID, completedAt); err != nil {
		return nil, err
	}
	return completedRun, nil
}

func (c *RunCoordinator) ContinueRunWithReply(ctx context.Context, input ContinueRunWithReplyInput) (*AgentRun, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	if strings.TrimSpace(input.RunID) == "" {
		return nil, fmt.Errorf("continue reply run_id is required")
	}
	if input.Revision < 0 {
		return nil, fmt.Errorf("continue reply revision must be >= 0")
	}
	if input.QueuedCapability == nil {
		return nil, fmt.Errorf("continue reply queued capability is required")
	}

	continuedAt := input.ContinuedAt
	if continuedAt.IsZero() {
		continuedAt = time.Now().UTC()
	} else {
		continuedAt = continuedAt.UTC()
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, err
	}
	if run.Status.IsTerminal() {
		return run, nil
	}

	runningRun, err := c.moveRunToRunning(ctx, run, continuedAt)
	if err != nil {
		return nil, err
	}

	steps, err := c.stepRepo.ListByRun(ctx, runningRun.ID)
	if err != nil {
		return nil, err
	}
	currentStep := findStepByIndex(steps, runningRun.CurrentStepIndex)
	if currentStep == nil {
		return nil, fmt.Errorf("agent runtime current step missing: run_id=%s index=%d", runningRun.ID, runningRun.CurrentStepIndex)
	}
	if err := c.completeCurrentStep(ctx, currentStep, continuedAt); err != nil {
		return nil, err
	}

	nextStepIndex := nextAvailableStepIndex(steps, currentStep.Index)
	for _, call := range input.CapabilityCalls {
		step, err := newCompletedCapabilityStep(runningRun.ID, nextStepIndex, call, continuedAt)
		if err != nil {
			return nil, err
		}
		if err := c.stepRepo.Append(ctx, step); err != nil {
			return nil, err
		}
		nextStepIndex++

		observeStep, err := newCompletedCapabilityObserveStep(runningRun.ID, nextStepIndex, call, continuedAt)
		if err != nil {
			return nil, err
		}
		if err := c.stepRepo.Append(ctx, observeStep); err != nil {
			return nil, err
		}
		nextStepIndex++
	}

	replyOutput, err := json.Marshal(replyCompletionOutput{
		ThoughtText:       strings.TrimSpace(input.ThoughtText),
		ReplyText:         strings.TrimSpace(input.ReplyText),
		ResponseMessageID: strings.TrimSpace(input.ResponseMessageID),
		ResponseCardID:    strings.TrimSpace(input.ResponseCardID),
		DeliveryMode:      input.DeliveryMode,
		LifecycleState:    ReplyLifecycleStateActive,
		TargetMessageID:   strings.TrimSpace(input.TargetMessageID),
		TargetCardID:      strings.TrimSpace(input.TargetCardID),
		TargetStepID:      strings.TrimSpace(input.TargetStepID),
	})
	if err != nil {
		return nil, err
	}

	replyExternalRef := strings.TrimSpace(input.ResponseCardID)
	if replyExternalRef == "" {
		replyExternalRef = strings.TrimSpace(input.ResponseMessageID)
	}
	replyStep := &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       runningRun.ID,
		Index:       nextStepIndex,
		Kind:        StepKindReply,
		Status:      StepStatusCompleted,
		OutputJSON:  replyOutput,
		ExternalRef: replyExternalRef,
		CreatedAt:   continuedAt,
		StartedAt:   &continuedAt,
		FinishedAt:  &continuedAt,
	}
	if err := c.stepRepo.Append(ctx, replyStep); err != nil {
		return nil, err
	}
	nextStepIndex++

	queuedStep, err := newQueuedCapabilityStep(runningRun.ID, nextStepIndex, *input.QueuedCapability, continuedAt)
	if err != nil {
		return nil, err
	}
	if err := c.stepRepo.Append(ctx, queuedStep); err != nil {
		return nil, err
	}

	resultSummary := strings.TrimSpace(input.ReplyText)
	if resultSummary == "" {
		resultSummary = "runtime reply continued"
	}
	return c.runRepo.UpdateStatus(ctx, runningRun.ID, runningRun.Revision, func(current *AgentRun) error {
		current.Status = RunStatusQueued
		current.CurrentStepIndex = queuedStep.Index
		current.LastResponseID = strings.TrimSpace(input.ResponseMessageID)
		current.ResultSummary = resultSummary
		current.ErrorText = ""
		current.UpdatedAt = continuedAt
		if current.StartedAt == nil {
			current.StartedAt = &continuedAt
		}
		return nil
	})
}

func newCompletedCapabilityStep(runID string, index int, call CompletedCapabilityCall, completedAt time.Time) (*AgentStep, error) {
	input := CapabilityCallInput{
		Request: CapabilityRequest{
			PayloadJSON: runtimePayloadBytes(call.Arguments),
		},
	}
	if previousResponseID := strings.TrimSpace(call.PreviousResponseID); previousResponseID != "" {
		input.Continuation = &CapabilityContinuationInput{
			PreviousResponseID: previousResponseID,
		}
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	result := CapabilityResult{
		OutputText:  strings.TrimSpace(call.Output),
		ExternalRef: strings.TrimSpace(call.CallID),
	}
	if raw := runtimeValidJSONPayload(call.Output); len(raw) > 0 {
		result.OutputJSON = raw
		result.OutputText = ""
	}

	return &AgentStep{
		ID:             newRuntimeID("step"),
		RunID:          runID,
		Index:          index,
		Kind:           StepKindCapabilityCall,
		Status:         StepStatusCompleted,
		CapabilityName: strings.TrimSpace(call.CapabilityName),
		InputJSON:      inputJSON,
		OutputJSON:     encodeCapabilityResult(result),
		ExternalRef:    strings.TrimSpace(call.CallID),
		CreatedAt:      completedAt,
		StartedAt:      &completedAt,
		FinishedAt:     &completedAt,
	}, nil
}

func newCompletedCapabilityObserveStep(runID string, index int, call CompletedCapabilityCall, observedAt time.Time) (*AgentStep, error) {
	result := completedCapabilityCallResult(call)
	return newCapabilityObserveStep(runID, index, call.CapabilityName, result, observedAt)
}

func newQueuedCapabilityStep(runID string, index int, call QueuedCapabilityCall, createdAt time.Time) (*AgentStep, error) {
	inputJSON, err := json.Marshal(call.Input)
	if err != nil {
		return nil, err
	}

	return &AgentStep{
		ID:             newRuntimeID("step"),
		RunID:          runID,
		Index:          index,
		Kind:           StepKindCapabilityCall,
		Status:         StepStatusQueued,
		CapabilityName: strings.TrimSpace(call.CapabilityName),
		InputJSON:      inputJSON,
		ExternalRef:    strings.TrimSpace(call.CallID),
		CreatedAt:      createdAt,
	}, nil
}

func runtimePayloadBytes(raw string) []byte {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return []byte(trimmed)
}

func runtimeValidJSONPayload(raw string) []byte {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if !json.Valid([]byte(trimmed)) {
		return nil
	}
	return []byte(trimmed)
}

func completedCapabilityCallResult(call CompletedCapabilityCall) CapabilityResult {
	result := CapabilityResult{
		OutputText:  strings.TrimSpace(call.Output),
		ExternalRef: strings.TrimSpace(call.CallID),
	}
	if raw := runtimeValidJSONPayload(call.Output); len(raw) > 0 {
		result.OutputJSON = raw
		result.OutputText = ""
	}
	return result
}

func clonePlanPendingCapability(src *PlanPendingCapability) *PlanPendingCapability {
	if src == nil {
		return nil
	}
	return &PlanPendingCapability{
		CallID:         strings.TrimSpace(src.CallID),
		CapabilityName: strings.TrimSpace(src.CapabilityName),
		Arguments:      strings.TrimSpace(src.Arguments),
	}
}

func maxStepIndex(values ...int) int {
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func nextAvailableStepIndex(steps []*AgentStep, currentIndex int) int {
	next := currentIndex + 1
	for _, step := range steps {
		if step == nil {
			continue
		}
		if step.Index >= next {
			next = step.Index + 1
		}
	}
	return next
}

func (c *RunCoordinator) moveRunToRunning(ctx context.Context, run *AgentRun, startedAt time.Time) (*AgentRun, error) {
	if run == nil {
		return nil, fmt.Errorf("agent runtime run is nil")
	}
	if run.Status == RunStatusRunning {
		if run.StartedAt == nil {
			run.StartedAt = &startedAt
		}
		return run, nil
	}
	return c.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.Status = RunStatusRunning
		current.WaitingReason = WaitingReasonNone
		current.WaitingToken = ""
		current.UpdatedAt = startedAt
		if current.StartedAt == nil {
			current.StartedAt = &startedAt
		}
		return nil
	})
}

func (c *RunCoordinator) completeCurrentStep(ctx context.Context, step *AgentStep, finishedAt time.Time) error {
	if step == nil {
		return fmt.Errorf("agent runtime step is nil")
	}

	switch step.Status {
	case StepStatusCompleted:
		return nil
	case StepStatusQueued:
		runningStep, err := c.stepRepo.UpdateStatus(ctx, step.ID, StepStatusQueued, func(current *AgentStep) error {
			current.Status = StepStatusRunning
			current.StartedAt = &finishedAt
			return nil
		})
		if err != nil {
			return err
		}
		step = runningStep
	case StepStatusRunning:
	default:
		return fmt.Errorf("agent runtime step not completable: run_id=%s step_id=%s status=%s", step.RunID, step.ID, step.Status)
	}

	_, err := c.stepRepo.UpdateStatus(ctx, step.ID, StepStatusRunning, func(current *AgentStep) error {
		current.Status = StepStatusCompleted
		current.FinishedAt = &finishedAt
		if current.StartedAt == nil {
			current.StartedAt = &finishedAt
		}
		return nil
	})
	return err
}

func (c *RunCoordinator) clearFinishedRunState(ctx context.Context, runID, sessionID string, now time.Time) error {
	if c == nil {
		return nil
	}

	session, err := c.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if session != nil && session.ActiveRunID == runID {
		if err := c.sessionRepo.SetActiveRun(ctx, session.ID, "", "", "", now); err != nil {
			return err
		}
	}

	if c.runtimeStore != nil && session != nil && strings.TrimSpace(session.ChatID) != "" {
		swapped, err := c.runtimeStore.SwapActiveChatRun(ctx, session.ChatID, runID, "", c.activeRunTTL)
		if err != nil {
			return err
		}
		if !swapped {
			current, err := c.runtimeStore.ActiveChatRun(ctx, session.ChatID)
			if err != nil {
				return err
			}
			if current == runID {
				return fmt.Errorf("active chat slot still points to completed run: chat_id=%s run_id=%s", session.ChatID, runID)
			}
		}
	}
	return nil
}
