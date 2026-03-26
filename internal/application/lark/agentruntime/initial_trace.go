package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	chatflow "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	message "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/message"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type initialCapabilityTraceRecorderContextKey struct{}

type initialPendingApprovalDispatcher interface {
	DispatchInitialPendingApproval(context.Context, QueuedCapabilityCall) (*ApprovalRequest, error)
}

type initialPendingApprovalDispatcherContextKey struct{}

type initialPendingApprovalDispatchState struct {
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning]
	err    error
}

type initialReplyStreamRecordState struct {
	stream        iter.Seq[*ark_dal.ModelStreamRespReasoning]
	recordedCount int
	err           error
}

type runInitialCapabilityTraceRecorder struct {
	coordinator *RunCoordinator
	runID       string
	recordedAt  time.Time

	mu        sync.Mutex
	nextIndex int
}

type immediateInitialPendingApprovalDispatcher struct {
	coordinator    *RunCoordinator
	approvalSender ApprovalSender
	run            *AgentRun
	event          *larkim.P2MessageReceiveV1
	requestedAt    time.Time
	sentByCallID   map[string]ApprovalRequest
}

// WithInitialCapabilityTraceRecorder implements agent runtime behavior.
func WithInitialCapabilityTraceRecorder(ctx context.Context, recorder InitialTraceRecorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, initialCapabilityTraceRecorderContextKey{}, recorder)
}

func initialCapabilityTraceRecorderFromContext(ctx context.Context) InitialTraceRecorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(initialCapabilityTraceRecorderContextKey{}).(InitialTraceRecorder)
	return recorder
}

// WithInitialPendingApprovalDispatcher implements agent runtime behavior.
func WithInitialPendingApprovalDispatcher(ctx context.Context, dispatcher initialPendingApprovalDispatcher) context.Context {
	if ctx == nil || dispatcher == nil {
		return ctx
	}
	return context.WithValue(ctx, initialPendingApprovalDispatcherContextKey{}, dispatcher)
}

func initialPendingApprovalDispatcherFromContext(ctx context.Context) initialPendingApprovalDispatcher {
	if ctx == nil {
		return nil
	}
	dispatcher, _ := ctx.Value(initialPendingApprovalDispatcherContextKey{}).(initialPendingApprovalDispatcher)
	return dispatcher
}

func buildAgenticInitialReplyLoopOptions(plan ChatGenerationPlan) RuntimeInitialChatLoopOptions {
	builder := defaultAgenticInitialChatPlanBuilder
	if builder == nil {
		builder = chatflow.BuildAgenticChatExecutionPlan
	}
	return RuntimeInitialChatLoopOptions{
		Plan:                plan,
		Builder:             builder,
		DefaultToolTurns:    chatflow.DefaultAgenticInitialChatToolTurns,
		Finalizer:           defaultInitialChatStreamFinalizer,
		EnsureDeferredTools: true,
	}
}

// WithInitialReplyEmitter implements agent runtime behavior.
func WithInitialReplyEmitter(emitter InitialReplyEmitter) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.initialReplyEmitter = emitter
		}
	}
}

// WithInitialReplyExecutorFactory implements agent runtime behavior.
func WithInitialReplyExecutorFactory(factory InitialReplyExecutorFactory) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.initialReplyExecutorFactory = factory
		}
	}
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

// RecordCompletedCapabilityCall implements agent runtime behavior.
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

// RecordReplyTurnPlan implements agent runtime behavior.
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

func wrapInitialPendingApprovalDispatcher(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	dispatcher initialPendingApprovalDispatcher,
) *initialPendingApprovalDispatchState {
	state := &initialPendingApprovalDispatchState{stream: stream}
	if stream == nil || dispatcher == nil {
		return state
	}

	baseRequest := CapabilityRequest{
		Scope:       initialReplyCapabilityScope(event),
		ChatID:      strings.TrimSpace(message.ChatID(event)),
		ActorOpenID: strings.TrimSpace(initialReplyActorOpenID(event)),
	}
	state.stream = func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for item := range stream {
			if item == nil {
				continue
			}
			if trace := item.CapabilityCall; trace != nil && trace.Pending {
				if strings.TrimSpace(trace.ApprovalStepID) == "" || strings.TrimSpace(trace.ApprovalToken) == "" {
					call := buildQueuedCapabilityCallFromTrace(baseRequest, *trace)
					if call != nil && call.Input.Approval != nil {
						request, err := dispatcher.DispatchInitialPendingApproval(ctx, *call)
						if err != nil {
							state.err = err
							return
						}
						if request != nil {
							trace.ApprovalStepID = strings.TrimSpace(request.StepID)
							trace.ApprovalToken = strings.TrimSpace(request.Token)
						}
					}
				}
			}
			if !yield(item) {
				return
			}
		}
	}
	return state
}

func newImmediateInitialPendingApprovalDispatcher(
	coordinator *RunCoordinator,
	approvalSender ApprovalSender,
	run *AgentRun,
	event *larkim.P2MessageReceiveV1,
	requestedAt time.Time,
) initialPendingApprovalDispatcher {
	if coordinator == nil || approvalSender == nil || run == nil || coordinator.runtimeStore == nil {
		return nil
	}
	return &immediateInitialPendingApprovalDispatcher{
		coordinator:    coordinator,
		approvalSender: approvalSender,
		run:            run,
		event:          event,
		requestedAt:    normalizeObservedAt(requestedAt),
		sentByCallID:   make(map[string]ApprovalRequest),
	}
}

// DispatchInitialPendingApproval implements agent runtime behavior.
func (d *immediateInitialPendingApprovalDispatcher) DispatchInitialPendingApproval(ctx context.Context, call QueuedCapabilityCall) (*ApprovalRequest, error) {
	if d == nil || d.coordinator == nil || d.approvalSender == nil {
		return nil, nil
	}

	callID := strings.TrimSpace(call.CallID)
	if callID != "" {
		if existing, ok := d.sentByCallID[callID]; ok {
			copied := existing
			return &copied, nil
		}
	}

	spec := normalizeQueuedCapabilityApproval(call, d.requestedAt)
	if strings.TrimSpace(spec.Title) == "" {
		return nil, nil
	}
	request, err := d.coordinator.ReserveApproval(ctx, RequestApprovalInput{
		RunID:          d.run.ID,
		ApprovalType:   spec.Type,
		Title:          spec.Title,
		Summary:        spec.Summary,
		CapabilityName: strings.TrimSpace(call.CapabilityName),
		PayloadJSON:    append([]byte(nil), call.Input.Request.PayloadJSON...),
		ExpiresAt:      spec.ExpiresAt,
		RequestedAt:    d.requestedAt,
	})
	if err != nil {
		return nil, err
	}

	target, err := d.resolveTarget(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.approvalSender.SendApprovalCard(ctx, target, *request); err != nil {
		return nil, err
	}
	if callID != "" {
		d.sentByCallID[callID] = *request
	}
	return request, nil
}

func (d *immediateInitialPendingApprovalDispatcher) resolveTarget(ctx context.Context) (ApprovalCardTarget, error) {
	target := ApprovalCardTarget{
		ChatID:           strings.TrimSpace(message.ChatID(d.event)),
		ReplyToMessageID: strings.TrimSpace(d.run.TriggerMessageID),
		VisibleOpenID:    strings.TrimSpace(d.run.ActorOpenID),
	}
	if root, ok := runtimecontext.RootAgenticReplyTarget(ctx); ok && strings.TrimSpace(root.MessageID) != "" {
		target.ReplyToMessageID = strings.TrimSpace(root.MessageID)
		target.ReplyInThread = true
		return target, nil
	}
	if target.ChatID != "" {
		return target, nil
	}
	if d.coordinator == nil || d.coordinator.sessionRepo == nil {
		return target, nil
	}
	session, err := d.coordinator.sessionRepo.GetByID(ctx, d.run.SessionID)
	if err != nil {
		return ApprovalCardTarget{}, fmt.Errorf("load approval target session: %w", err)
	}
	if session != nil {
		target.ChatID = strings.TrimSpace(session.ChatID)
	}
	return target, nil
}

func normalizeQueuedCapabilityApproval(call QueuedCapabilityCall, now time.Time) CapabilityApprovalSpec {
	spec := CapabilityApprovalSpec{}
	if call.Input.Approval != nil {
		spec = *call.Input.Approval
	}
	if strings.TrimSpace(spec.Type) == "" {
		spec.Type = "capability"
	}
	if strings.TrimSpace(spec.Title) == "" {
		spec.Title = "审批执行能力"
		if name := strings.TrimSpace(call.CapabilityName); name != "" {
			spec.Title = "审批执行 " + name
		}
	}
	if strings.TrimSpace(spec.Summary) == "" {
		spec.Summary = "该能力需要审批后才能继续执行。"
	}
	if spec.ExpiresAt.IsZero() {
		spec.ExpiresAt = normalizeObservedAt(now).Add(defaultCapabilityApprovalTTL)
	}
	return spec
}

func wrapInitialReplyStreamRecorder(
	ctx context.Context,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	recorder InitialTraceRecorder,
) *initialReplyStreamRecordState {
	state := &initialReplyStreamRecordState{
		stream: stream,
	}
	if stream == nil || recorder == nil {
		return state
	}

	state.stream = func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for item := range stream {
			if item == nil {
				continue
			}
			if trace := item.CapabilityCall; trace != nil && !trace.Pending {
				if err := recorder.RecordCompletedCapabilityCall(ctx, CompletedCapabilityCall{
					CallID:             strings.TrimSpace(trace.CallID),
					CapabilityName:     strings.TrimSpace(trace.FunctionName),
					Arguments:          strings.TrimSpace(trace.Arguments),
					Output:             strings.TrimSpace(trace.Output),
					PreviousResponseID: strings.TrimSpace(trace.PreviousResponseID),
				}); err != nil {
					state.err = err
					return
				}
				state.recordedCount++
			}
			if !yield(item) {
				return
			}
		}
	}
	return state
}

func buildInitialReplyResultFromEmission(event *larkim.P2MessageReceiveV1, result InitialReplyEmissionResult) (InitialReplyResult, error) {
	initial := InitialReplyResult{
		CapabilityCalls:   append([]CompletedCapabilityCall(nil), result.Reply.CapabilityCalls...),
		ThoughtText:       strings.TrimSpace(result.Reply.ThoughtText),
		ReplyText:         strings.TrimSpace(result.Reply.ReplyText),
		ResponseMessageID: strings.TrimSpace(result.ResponseMessageID),
		ResponseCardID:    strings.TrimSpace(result.ResponseCardID),
		DeliveryMode:      result.DeliveryMode,
		TargetMessageID:   strings.TrimSpace(result.TargetMessageID),
		TargetCardID:      strings.TrimSpace(result.TargetCardID),
	}
	if result.Reply.PendingCapability == nil {
		return initial, nil
	}

	queued := buildQueuedCapabilityCallFromCaptured(event, *result.Reply.PendingCapability)
	if queued == nil {
		return InitialReplyResult{}, fmt.Errorf("runtime pending capability is invalid")
	}
	initial.PendingCapability = queued
	return initial, nil
}
