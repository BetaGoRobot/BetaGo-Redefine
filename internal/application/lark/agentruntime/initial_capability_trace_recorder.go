package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

type InitialCapabilityTraceRecorder interface {
	RecordCompletedCapabilityCall(context.Context, CompletedCapabilityCall) error
}

type ReplyTurnPlanRecorder interface {
	RecordReplyTurnPlan(context.Context, CapabilityReplyPlan, *InitialChatToolCall) error
}

type initialCapabilityTraceRecorderContextKey struct{}

func WithInitialCapabilityTraceRecorder(ctx context.Context, recorder InitialCapabilityTraceRecorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, initialCapabilityTraceRecorderContextKey{}, recorder)
}

func initialCapabilityTraceRecorderFromContext(ctx context.Context) InitialCapabilityTraceRecorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(initialCapabilityTraceRecorderContextKey{}).(InitialCapabilityTraceRecorder)
	return recorder
}

type runInitialCapabilityTraceRecorder struct {
	coordinator *RunCoordinator
	runID       string
	recordedAt  time.Time

	mu        sync.Mutex
	nextIndex int
}

func newRunInitialCapabilityTraceRecorder(coordinator *RunCoordinator, run *AgentRun, recordedAt time.Time) *runInitialCapabilityTraceRecorder {
	if coordinator == nil || run == nil {
		return nil
	}
	return newRunCapabilityTraceRecorder(coordinator, run.ID, run.CurrentStepIndex+1, recordedAt)
}

func newRunCapabilityTraceRecorder(coordinator *RunCoordinator, runID string, nextIndex int, recordedAt time.Time) *runInitialCapabilityTraceRecorder {
	if coordinator == nil || runID == "" {
		return nil
	}
	if nextIndex < 0 {
		nextIndex = 0
	}
	return &runInitialCapabilityTraceRecorder{
		coordinator: coordinator,
		runID:       runID,
		recordedAt:  normalizeObservedAt(recordedAt),
		nextIndex:   nextIndex,
	}
}

func (r *runInitialCapabilityTraceRecorder) RecordCompletedCapabilityCall(ctx context.Context, call CompletedCapabilityCall) error {
	if r == nil || r.coordinator == nil || r.coordinator.stepRepo == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	capabilityStep, err := newCompletedCapabilityStep(r.runID, r.nextIndex, call, r.recordedAt)
	if err != nil {
		return err
	}
	if err := r.coordinator.stepRepo.Append(ctx, capabilityStep); err != nil {
		return err
	}
	if err := r.advanceRunCursor(ctx, capabilityStep.Index); err != nil {
		return err
	}
	r.nextIndex++

	observeStep, err := newCompletedCapabilityObserveStep(r.runID, r.nextIndex, call, r.recordedAt)
	if err != nil {
		return err
	}
	if err := r.coordinator.stepRepo.Append(ctx, observeStep); err != nil {
		return err
	}
	if err := r.advanceRunCursor(ctx, observeStep.Index); err != nil {
		return err
	}
	r.nextIndex++
	return nil
}

func (r *runInitialCapabilityTraceRecorder) RecordReplyTurnPlan(ctx context.Context, plan CapabilityReplyPlan, toolCall *InitialChatToolCall) error {
	if r == nil || r.coordinator == nil || r.coordinator.stepRepo == nil {
		return nil
	}

	planInput := replyPlanStepInput{
		ThoughtText:       strings.TrimSpace(plan.ThoughtText),
		ReplyText:         strings.TrimSpace(plan.ReplyText),
		PendingCapability: buildReplyTurnPlanPendingCapability(toolCall),
	}
	if planInput.ThoughtText == "" && planInput.ReplyText == "" && planInput.PendingCapability == nil {
		return nil
	}

	inputJSON, err := json.Marshal(planInput)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	step := &AgentStep{
		ID:         newRuntimeID("step"),
		RunID:      r.runID,
		Index:      r.nextIndex,
		Kind:       StepKindPlan,
		Status:     StepStatusCompleted,
		InputJSON:  inputJSON,
		CreatedAt:  r.recordedAt,
		StartedAt:  &r.recordedAt,
		FinishedAt: &r.recordedAt,
	}
	if err := r.coordinator.stepRepo.Append(ctx, step); err != nil {
		return err
	}
	if err := r.advanceRunCursor(ctx, step.Index); err != nil {
		return err
	}
	r.nextIndex++
	return nil
}

func buildReplyTurnPlanPendingCapability(toolCall *InitialChatToolCall) *PlanPendingCapability {
	if toolCall == nil {
		return nil
	}

	pending := &PlanPendingCapability{
		CallID:         strings.TrimSpace(toolCall.CallID),
		CapabilityName: strings.TrimSpace(toolCall.FunctionName),
		Arguments:      strings.TrimSpace(toolCall.Arguments),
	}
	if pending.CallID == "" && pending.CapabilityName == "" && pending.Arguments == "" {
		return nil
	}
	return pending
}

func (r *runInitialCapabilityTraceRecorder) advanceRunCursor(ctx context.Context, index int) error {
	if r == nil || r.coordinator == nil || r.coordinator.runRepo == nil {
		return nil
	}

	run, err := r.coordinator.runRepo.GetByID(ctx, r.runID)
	if err != nil || run == nil || run.Status.IsTerminal() {
		return err
	}
	if run.CurrentStepIndex >= index {
		return nil
	}

	_, err = r.coordinator.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.CurrentStepIndex = index
		current.UpdatedAt = r.recordedAt
		return nil
	})
	return err
}
