package agentruntime

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	chatflow "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initialcore"
	message "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/message"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// InitialReplyExecutor produces the first user-visible reply for a freshly
// started runtime run.
type InitialReplyExecutor interface {
	ProduceInitialReply(context.Context) (InitialReplyResult, error)
}

// InitialReplyExecutorFactory constructs an initial-reply executor for one run input.
type InitialReplyExecutorFactory func(InitialRunInput, InitialReplyEmitter) (InitialReplyExecutor, error)

// InitialRunInput packages the event, plan, and ownership information needed to
// execute an initial runtime turn.
type InitialRunInput struct {
	Start      StartShadowRunRequest
	Event      *larkim.P2MessageReceiveV1 `json:"event,omitempty"`
	Plan       ChatGenerationPlan         `json:"plan,omitempty"`
	OutputMode InitialReplyOutputMode     `json:"output_mode,omitempty"`
}

// InitialReplyResult is the normalized outcome of one initial runtime turn,
// including any completed or pending capability calls.
type InitialReplyResult struct {
	CapabilityCalls   []CompletedCapabilityCall `json:"capability_calls,omitempty"`
	PendingCapability *QueuedCapabilityCall     `json:"pending_capability,omitempty"`
	ThoughtText       string                    `json:"thought_text,omitempty"`
	ReplyText         string                    `json:"reply_text,omitempty"`
	ResponseMessageID string                    `json:"response_message_id,omitempty"`
	ResponseCardID    string                    `json:"response_card_id,omitempty"`
	DeliveryMode      ReplyDeliveryMode         `json:"delivery_mode,omitempty"`
	TargetMessageID   string                    `json:"target_message_id,omitempty"`
	TargetCardID      string                    `json:"target_card_id,omitempty"`
}

// RunProcessorInput is the union input accepted by the runtime processor for
// either initial execution or resume execution.
type RunProcessorInput struct {
	Initial *InitialRunInput `json:"initial,omitempty"`
	Resume  *ResumeEvent     `json:"resume,omitempty"`
}

// RunProcessor processes one runtime work item, whether it is an initial run or a resume event.
type RunProcessor interface {
	ProcessRun(context.Context, RunProcessorInput) error
}

type defaultInitialReplyExecutor struct {
	mode      InitialReplyOutputMode
	event     *larkim.P2MessageReceiveV1
	plan      ChatGenerationPlan
	emitter   InitialReplyEmitter
	deps      defaultRuntimeExecutorDeps
	generator InitialReplyStreamGenerator
}

var agenticInitialReplyStreamGenerator InitialReplyStreamGenerator = GenerateAgenticInitialReplyStream

// SetAgenticInitialReplyStreamGenerator overrides the generator used to produce
// agentic initial-reply streams.
func SetAgenticInitialReplyStreamGenerator(generator InitialReplyStreamGenerator) {
	if generator == nil {
		agenticInitialReplyStreamGenerator = GenerateAgenticInitialReplyStream
		return
	}
	agenticInitialReplyStreamGenerator = generator
}

// NewDefaultInitialReplyExecutor constructs the default executor for initial replies.
func NewDefaultInitialReplyExecutor(
	mode InitialReplyOutputMode,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	emitter InitialReplyEmitter,
) InitialReplyExecutor {
	return NewDefaultInitialReplyExecutorWithDeps(mode, event, plan, emitter, snapshotDefaultRuntimeExecutorDeps(), agenticInitialReplyStreamGenerator)
}

// NewDefaultInitialReplyExecutorWithGenerator constructs the default executor
// while overriding the stream generator.
func NewDefaultInitialReplyExecutorWithGenerator(
	mode InitialReplyOutputMode,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	emitter InitialReplyEmitter,
	generator InitialReplyStreamGenerator,
) InitialReplyExecutor {
	return NewDefaultInitialReplyExecutorWithDeps(mode, event, plan, emitter, snapshotDefaultRuntimeExecutorDeps(), generator)
}

// NewDefaultInitialReplyExecutorWithPlanExecutor constructs the default executor
// while overriding the underlying chat plan executor.
func NewDefaultInitialReplyExecutorWithPlanExecutor(
	mode InitialReplyOutputMode,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	emitter InitialReplyEmitter,
	executor ChatGenerationPlanExecutor,
) InitialReplyExecutor {
	deps := snapshotDefaultRuntimeExecutorDeps()
	deps.chatGenerationExecutor = executor
	return NewDefaultInitialReplyExecutorWithDeps(mode, event, plan, emitter, deps, agenticInitialReplyStreamGenerator)
}

// NewDefaultInitialReplyExecutorWithDeps constructs the default executor with
// fully injectable dependencies for tests and custom wiring.
func NewDefaultInitialReplyExecutorWithDeps(
	mode InitialReplyOutputMode,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	emitter InitialReplyEmitter,
	deps defaultRuntimeExecutorDeps,
	generator InitialReplyStreamGenerator,
) InitialReplyExecutor {
	if generator == nil {
		generator = func(ctx context.Context, event *larkim.P2MessageReceiveV1, plan ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
			return generateAgenticInitialReplyStreamWithDeps(ctx, event, plan, deps)
		}
	}
	return defaultInitialReplyExecutor{
		mode:      mode,
		event:     event,
		plan:      plan,
		emitter:   emitter,
		deps:      deps,
		generator: generator,
	}
}

// ProduceInitialReply executes the initial turn, records any approval or trace
// side effects, emits the reply, and returns the normalized result.
func (e defaultInitialReplyExecutor) ProduceInitialReply(ctx context.Context) (InitialReplyResult, error) {
	if e.emitter == nil {
		return InitialReplyResult{}, fmt.Errorf("initial reply emitter is not configured")
	}

	stream, err := e.generateStream(ctx)
	if err != nil {
		return InitialReplyResult{}, err
	}
	dispatchState := wrapInitialPendingApprovalDispatcher(ctx, e.event, stream, initialPendingApprovalDispatcherFromContext(ctx))
	stream = dispatchState.stream
	recordState := wrapInitialReplyStreamRecorder(ctx, stream, initialCapabilityTraceRecorderFromContext(ctx))
	stream = recordState.stream

	target, _ := InitialReplyTargetFromContext(ctx)
	result, err := e.emitter.EmitInitialReply(ctx, InitialReplyEmissionRequest{
		OutputKind:      AgenticOutputKindModelReply,
		Mode:            e.mode,
		MentionOpenID:   strings.TrimSpace(initialReplyActorOpenID(e.event)),
		Message:         message.EventMessage(e.event),
		TargetMode:      target.Mode,
		TargetMessageID: target.MessageID,
		TargetCardID:    target.CardID,
		ReplyInThread:   target.ReplyInThread,
		Stream:          stream,
	})
	if err != nil {
		return InitialReplyResult{}, err
	}
	if dispatchState.err != nil {
		return InitialReplyResult{}, dispatchState.err
	}
	if recordState.err != nil {
		return InitialReplyResult{}, recordState.err
	}
	if recordState.recordedCount > 0 {
		result.Reply.CapabilityCalls = nil
	}
	return buildInitialReplyResultFromEmission(e.event, result)
}

func (e defaultInitialReplyExecutor) generateStream(ctx context.Context) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	switch e.mode {
	case InitialReplyOutputModeAgentic:
		generator := e.generator
		if generator == nil {
			generator = func(ctx context.Context, event *larkim.P2MessageReceiveV1, plan ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
				return generateAgenticInitialReplyStreamWithDeps(ctx, event, plan, e.deps)
			}
		}
		return generator(ctx, e.event, e.plan)
	default:
		if e.deps.chatGenerationExecutor != nil {
			return e.deps.chatGenerationExecutor.Generate(ctx, e.event, e.plan)
		}
		return e.plan.Generate(ctx, e.event)
	}
}

// GenerateAgenticInitialReplyStream runs the agentic initial chat loop with the
// default runtime dependencies.
func GenerateAgenticInitialReplyStream(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return generateAgenticInitialReplyStreamWithDeps(ctx, event, plan, snapshotDefaultRuntimeExecutorDeps())
}

func generateAgenticInitialReplyStreamWithDeps(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	deps defaultRuntimeExecutorDeps,
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	opts := buildAgenticInitialReplyLoopOptions(plan)
	if deps.toolProvider != nil {
		opts.Tools = decorateRuntimeChatTools(deps.toolProvider())
	}
	opts.Builder = deps.agenticInitialPlanBuilder
	opts.TurnExecutor = deps.initialChatTurnExecutor
	opts.Finalizer = deps.initialChatStreamFinalizer
	return BuildRuntimeInitialChatLoop(ctx, event, opts)
}

func mapCapturedInitialReplySnapshot(reply initialcore.ReplySnapshot) CapturedInitialReply {
	mapped := initialcore.MapSnapshot(reply)
	result := CapturedInitialReply{
		ThoughtText: mapped.ThoughtText,
		ReplyText:   mapped.ReplyText,
	}
	if len(mapped.CapabilityCalls) > 0 {
		result.CapabilityCalls = make([]CompletedCapabilityCall, 0, len(mapped.CapabilityCalls))
		for _, call := range mapped.CapabilityCalls {
			result.CapabilityCalls = append(result.CapabilityCalls, CompletedCapabilityCall{
				CallID:             call.CallID,
				CapabilityName:     call.CapabilityName,
				Arguments:          call.Arguments,
				Output:             call.Output,
				PreviousResponseID: call.PreviousResponseID,
			})
		}
	}
	result.PendingCapability = mapCapturedPendingCapabilitySnapshot(mapped.PendingCapability)
	return result
}

func mapCapturedPendingCapabilitySnapshot(pending *initialcore.PendingCapability) *CapturedInitialPendingCapability {
	if pending == nil {
		return nil
	}
	result := &CapturedInitialPendingCapability{
		CallID:             pending.CallID,
		CapabilityName:     pending.CapabilityName,
		Arguments:          pending.Arguments,
		PreviousResponseID: pending.PreviousResponseID,
	}
	if pending.Approval != nil {
		result.Approval = &CapabilityApprovalSpec{
			Type:              pending.Approval.Type,
			Title:             pending.Approval.Title,
			Summary:           pending.Approval.Summary,
			ExpiresAt:         pending.Approval.ExpiresAt.UTC(),
			ReservationStepID: pending.Approval.ReservationStepID,
			ReservationToken:  pending.Approval.ReservationToken,
		}
	}
	if len(pending.QueueTail) > 0 {
		result.QueueTail = make([]CapturedInitialPendingCapability, 0, len(pending.QueueTail))
		for _, item := range pending.QueueTail {
			mapped := mapCapturedPendingCapabilitySnapshot(&item)
			if mapped == nil {
				continue
			}
			result.QueueTail = append(result.QueueTail, *mapped)
		}
	}
	return result
}

func buildQueuedCapabilityCallFromCaptured(event *larkim.P2MessageReceiveV1, pending CapturedInitialPendingCapability) *QueuedCapabilityCall {
	if strings.TrimSpace(pending.CapabilityName) == "" {
		return nil
	}

	request := CapabilityRequest{
		Scope:       initialReplyCapabilityScope(event),
		ChatID:      strings.TrimSpace(message.ChatID(event)),
		ActorOpenID: strings.TrimSpace(initialReplyActorOpenID(event)),
		PayloadJSON: []byte(strings.TrimSpace(pending.Arguments)),
	}
	call := &QueuedCapabilityCall{
		CallID:         strings.TrimSpace(pending.CallID),
		CapabilityName: strings.TrimSpace(pending.CapabilityName),
		Input: CapabilityCallInput{
			Request: request,
		},
	}
	if previousResponseID := strings.TrimSpace(pending.PreviousResponseID); previousResponseID != "" {
		call.Input.Continuation = &CapabilityContinuationInput{
			PreviousResponseID: previousResponseID,
		}
	}
	if pending.Approval != nil {
		approval := *pending.Approval
		call.Input.Approval = &approval
	}
	if len(pending.QueueTail) > 0 {
		call.Input.QueueTail = buildQueuedCapabilityTailFromCaptured(event, pending.QueueTail)
	}
	return call
}

func buildQueuedCapabilityTailFromCaptured(event *larkim.P2MessageReceiveV1, pending []CapturedInitialPendingCapability) []QueuedCapabilityCall {
	if len(pending) == 0 {
		return nil
	}
	queue := make([]QueuedCapabilityCall, 0, len(pending))
	for _, item := range pending {
		call := buildQueuedCapabilityCallFromCaptured(event, item)
		if call == nil {
			continue
		}
		queue = append(queue, *call)
	}
	if len(queue) == 0 {
		return nil
	}
	return queue
}

// ProcessRun implements agent runtime behavior.
func (p *ContinuationProcessor) ProcessRun(ctx context.Context, input RunProcessorInput) error {
	switch {
	case input.Initial != nil && input.Resume != nil:
		return fmt.Errorf("run processor input cannot contain both initial and resume payload")
	case input.Initial != nil:
		return p.processInitialRun(ctx, *input.Initial)
	case input.Resume != nil:
		return p.ProcessResume(ctx, *input.Resume)
	default:
		return fmt.Errorf("run processor input is empty")
	}
}

func (p *ContinuationProcessor) processInitialRun(ctx context.Context, input InitialRunInput) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	return p.withExecutionLease(ctx, strings.TrimSpace(input.Start.ChatID), strings.TrimSpace(input.Start.ActorOpenID), executionLeaseHolderForInitial(input), func() error {
		return p.processInitialRunWithLease(ctx, input)
	})
}

func (p *ContinuationProcessor) processInitialRunWithLease(ctx context.Context, input InitialRunInput) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	input.Event = withInitialRunActorOpenID(input.Event, input.Start.ActorOpenID)
	executor, err := input.BuildExecutor(p.initialReplyEmitter, p.initialReplyExecutorFactory)
	if err != nil {
		return err
	}

	run, err := p.coordinator.StartShadowRun(ctx, input.Start)
	if err != nil {
		return err
	}
	if run == nil || run.Status.IsTerminal() {
		return nil
	}
	return p.withRunExecutionHeartbeat(ctx, run, normalizeObservedAt(input.Start.Now), func(currentRun *AgentRun) error {
		run = currentRun
		decideStepIndex := run.CurrentStepIndex
		ctx, err = p.withAgenticReplyTargetState(ctx, NewRunProjection(run, nil))
		if err != nil {
			return err
		}

		if target, err := p.resolveInitialReplyTarget(ctx, run, input.Event); err != nil {
			return err
		} else if target.Mode != "" {
			ctx = WithInitialReplyTarget(ctx, target)
		}
		if dispatcher := newImmediateInitialPendingApprovalDispatcher(p.coordinator, p.approvalSender, run, input.Event, normalizeObservedAt(input.Start.Now)); dispatcher != nil {
			ctx = WithInitialPendingApprovalDispatcher(ctx, dispatcher)
		}
		if recorder := newRunInitialCapabilityTraceRecorder(p.coordinator, run, normalizeObservedAt(input.Start.Now)); recorder != nil {
			ctx = WithInitialCapabilityTraceRecorder(ctx, recorder)
		}

		reply, err := executor.ProduceInitialReply(ctx)
		if err != nil {
			_ = p.coordinator.CancelRun(ctx, run.ID, err.Error())
			return err
		}
		if refreshedRun, refreshErr := p.coordinator.runRepo.GetByID(ctx, run.ID); refreshErr == nil && refreshedRun != nil {
			run = refreshedRun
		}
		run, err = p.coordinator.QueuePlanStep(ctx, QueuePlanStepInput{
			RunID:             run.ID,
			Revision:          run.Revision,
			FromStepIndex:     decideStepIndex,
			ThoughtText:       strings.TrimSpace(reply.ThoughtText),
			ReplyText:         strings.TrimSpace(reply.ReplyText),
			PendingCapability: buildPlanPendingCapability(reply.PendingCapability),
			PlannedAt:         normalizeObservedAt(input.Start.Now),
		})
		if err != nil {
			return err
		}

		if reply.PendingCapability == nil {
			_, err = p.coordinator.CompleteRunWithReply(ctx, CompleteRunWithReplyInput{
				RunID:             run.ID,
				Revision:          run.Revision,
				CapabilityCalls:   reply.CapabilityCalls,
				ThoughtText:       strings.TrimSpace(reply.ThoughtText),
				ReplyText:         strings.TrimSpace(reply.ReplyText),
				ResponseMessageID: strings.TrimSpace(reply.ResponseMessageID),
				ResponseCardID:    strings.TrimSpace(reply.ResponseCardID),
				DeliveryMode:      reply.DeliveryMode,
				TargetMessageID:   strings.TrimSpace(reply.TargetMessageID),
				TargetCardID:      strings.TrimSpace(reply.TargetCardID),
				CompletedAt:       normalizeObservedAt(input.Start.Now),
			})
			return err
		}

		continuedRun, err := p.coordinator.ContinueRunWithReply(ctx, ContinueRunWithReplyInput{
			RunID:             run.ID,
			Revision:          run.Revision,
			CapabilityCalls:   reply.CapabilityCalls,
			ThoughtText:       strings.TrimSpace(reply.ThoughtText),
			ReplyText:         strings.TrimSpace(reply.ReplyText),
			ResponseMessageID: strings.TrimSpace(reply.ResponseMessageID),
			ResponseCardID:    strings.TrimSpace(reply.ResponseCardID),
			DeliveryMode:      reply.DeliveryMode,
			TargetMessageID:   strings.TrimSpace(reply.TargetMessageID),
			TargetCardID:      strings.TrimSpace(reply.TargetCardID),
			QueuedCapability:  reply.PendingCapability,
			ContinuedAt:       normalizeObservedAt(input.Start.Now),
		})
		if err != nil {
			return err
		}
		if continuedRun == nil {
			return nil
		}
		return p.processQueuedRun(ctx, continuedRun.ID, normalizeObservedAt(input.Start.Now))
	})
}

func buildPlanPendingCapability(call *QueuedCapabilityCall) *PlanPendingCapability {
	if call == nil {
		return nil
	}

	pending := &PlanPendingCapability{
		CallID:         strings.TrimSpace(call.CallID),
		CapabilityName: strings.TrimSpace(call.CapabilityName),
		Arguments:      strings.TrimSpace(string(call.Input.Request.PayloadJSON)),
	}
	if pending.CallID == "" && pending.CapabilityName == "" && pending.Arguments == "" {
		return nil
	}
	return pending
}

func (p *ContinuationProcessor) resolveInitialReplyTarget(
	ctx context.Context,
	run *AgentRun,
	event *larkim.P2MessageReceiveV1,
) (InitialReplyTarget, error) {
	if root, ok := runtimecontext.RootAgenticReplyTarget(ctx); ok && strings.TrimSpace(root.MessageID) != "" {
		if strings.TrimSpace(root.CardID) != "" {
			return InitialReplyTarget{
				Mode:          InitialReplyTargetModePatch,
				MessageID:     strings.TrimSpace(root.MessageID),
				CardID:        strings.TrimSpace(root.CardID),
				ReplyInThread: false,
			}, nil
		}
		return InitialReplyTarget{
			Mode:          InitialReplyTargetModeReply,
			MessageID:     strings.TrimSpace(root.MessageID),
			CardID:        strings.TrimSpace(root.CardID),
			ReplyInThread: true,
		}, nil
	}
	if target, err := p.resolveRootReplyTarget(ctx, run); err != nil {
		return InitialReplyTarget{}, err
	} else if target.MessageID != "" {
		mode := InitialReplyTargetModeReply
		replyInThread := true
		if strings.TrimSpace(target.CardID) != "" {
			mode = InitialReplyTargetModePatch
			replyInThread = false
		}
		return InitialReplyTarget{
			Mode:          mode,
			MessageID:     strings.TrimSpace(target.MessageID),
			CardID:        strings.TrimSpace(target.CardID),
			ReplyInThread: replyInThread,
		}, nil
	}
	return resolveEventInitialReplyTarget(event), nil
}

func resolveEventInitialReplyTarget(event *larkim.P2MessageReceiveV1) InitialReplyTarget {
	messageID := message.MessageID(event)
	if messageID == "" {
		return InitialReplyTarget{}
	}
	if message.ThreadID(event) != "" {
		return InitialReplyTarget{
			Mode:          InitialReplyTargetModeReply,
			MessageID:     messageID,
			ReplyInThread: true,
		}
	}
	if message.ParentID(event) != "" {
		return InitialReplyTarget{
			Mode:      InitialReplyTargetModeReply,
			MessageID: messageID,
		}
	}
	return InitialReplyTarget{
		Mode:      InitialReplyTargetModeReply,
		MessageID: messageID,
	}
}

// BuildExecutor implements agent runtime behavior.
func (input InitialRunInput) BuildExecutor(emitter InitialReplyEmitter, factory InitialReplyExecutorFactory) (InitialReplyExecutor, error) {
	if emitter == nil {
		return nil, fmt.Errorf("initial run reply emitter is required")
	}
	if input.Event == nil || input.Event.Event == nil || input.Event.Event.Message == nil {
		return nil, fmt.Errorf("initial run event is required")
	}
	if factory != nil {
		return factory(input, emitter)
	}
	mode := input.OutputMode
	if mode == "" {
		mode = InitialReplyOutputModeAgentic
	}
	return NewDefaultInitialReplyExecutor(mode, input.Event, chatflow.ClonePlan(input.Plan), emitter), nil
}

func withInitialRunActorOpenID(event *larkim.P2MessageReceiveV1, actorOpenID string) *larkim.P2MessageReceiveV1 {
	return message.CloneWithActorOpenID(event, actorOpenID)
}

func (p *ContinuationProcessor) processQueuedRun(ctx context.Context, runID string, observedAt time.Time) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("queued run id is required")
	}

	run, err := p.coordinator.runRepo.GetByID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil || run.Status.IsTerminal() {
		return nil
	}

	steps, err := p.loadSteps(ctx, run.ID)
	if err != nil {
		return err
	}
	currentStep := findStepByIndex(steps, run.CurrentStepIndex)
	if currentStep == nil {
		return fmt.Errorf("agent runtime current step missing: run_id=%s index=%d", run.ID, run.CurrentStepIndex)
	}
	if currentStep.Kind != StepKindCapabilityCall {
		return nil
	}

	return p.processCapabilityCall(ctx, run, currentStep, ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		OccurredAt: normalizeObservedAt(observedAt),
	})
}
