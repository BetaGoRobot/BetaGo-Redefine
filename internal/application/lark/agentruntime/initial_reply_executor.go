package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type InitialReplyOutputMode string

const (
	InitialReplyOutputModeAgentic  InitialReplyOutputMode = "agentic"
	InitialReplyOutputModeStandard InitialReplyOutputMode = "standard"
)

type CapturedInitialPendingCapability struct {
	CallID             string                  `json:"call_id,omitempty"`
	CapabilityName     string                  `json:"capability_name,omitempty"`
	Arguments          string                  `json:"arguments,omitempty"`
	PreviousResponseID string                  `json:"previous_response_id,omitempty"`
	Approval           *CapabilityApprovalSpec `json:"approval,omitempty"`
	QueueTail          []CapturedInitialPendingCapability `json:"queue_tail,omitempty"`
}

type CapturedInitialReply struct {
	CapabilityCalls   []CompletedCapabilityCall         `json:"capability_calls,omitempty"`
	PendingCapability *CapturedInitialPendingCapability `json:"pending_capability,omitempty"`
	ThoughtText       string                            `json:"thought_text,omitempty"`
	ReplyText         string                            `json:"reply_text,omitempty"`
}

type InitialReplyEmissionRequest struct {
	Mode            InitialReplyOutputMode
	Message         *larkim.EventMessage
	TargetMessageID string
	TargetCardID    string
	Stream          iter.Seq[*ark_dal.ModelStreamRespReasoning]
}

type InitialReplyEmissionResult struct {
	Reply             CapturedInitialReply `json:"reply"`
	ResponseMessageID string               `json:"response_message_id,omitempty"`
	ResponseCardID    string               `json:"response_card_id,omitempty"`
	DeliveryMode      ReplyDeliveryMode    `json:"delivery_mode,omitempty"`
	TargetMessageID   string               `json:"target_message_id,omitempty"`
	TargetCardID      string               `json:"target_card_id,omitempty"`
}

type InitialReplyEmitter interface {
	EmitInitialReply(context.Context, InitialReplyEmissionRequest) (InitialReplyEmissionResult, error)
}

type InitialReplyStreamGenerator func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error)

var agenticInitialReplyStreamGenerator InitialReplyStreamGenerator = GenerateAgenticInitialReplyStream

func SetAgenticInitialReplyStreamGenerator(generator InitialReplyStreamGenerator) {
	if generator == nil {
		agenticInitialReplyStreamGenerator = GenerateAgenticInitialReplyStream
		return
	}
	agenticInitialReplyStreamGenerator = generator
}

func WithInitialReplyEmitter(emitter InitialReplyEmitter) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.initialReplyEmitter = emitter
		}
	}
}

type defaultInitialReplyExecutor struct {
	mode    InitialReplyOutputMode
	event   *larkim.P2MessageReceiveV1
	plan    ChatGenerationPlan
	emitter InitialReplyEmitter
}

func NewDefaultInitialReplyExecutor(
	mode InitialReplyOutputMode,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	emitter InitialReplyEmitter,
) InitialReplyExecutor {
	return defaultInitialReplyExecutor{
		mode:    mode,
		event:   event,
		plan:    plan,
		emitter: emitter,
	}
}

func (e defaultInitialReplyExecutor) ProduceInitialReply(ctx context.Context) (InitialReplyResult, error) {
	if e.emitter == nil {
		return InitialReplyResult{}, fmt.Errorf("initial reply emitter is not configured")
	}

	stream, err := e.generateStream(ctx)
	if err != nil {
		return InitialReplyResult{}, err
	}
	recordState := wrapInitialReplyStreamRecorder(ctx, stream, initialCapabilityTraceRecorderFromContext(ctx))
	stream = recordState.stream

	target, _ := InitialReplyTargetFromContext(ctx)
	result, err := e.emitter.EmitInitialReply(ctx, InitialReplyEmissionRequest{
		Mode:            e.mode,
		Message:         initialReplyEventMessage(e.event),
		TargetMessageID: target.MessageID,
		TargetCardID:    target.CardID,
		Stream:          stream,
	})
	if err != nil {
		return InitialReplyResult{}, err
	}
	if recordState.err != nil {
		return InitialReplyResult{}, recordState.err
	}
	if recordState.recordedCount > 0 {
		result.Reply.CapabilityCalls = nil
	}
	return buildInitialReplyResultFromEmission(e.event, result)
}

type initialReplyStreamRecordState struct {
	stream        iter.Seq[*ark_dal.ModelStreamRespReasoning]
	recordedCount int
	err           error
}

func wrapInitialReplyStreamRecorder(
	ctx context.Context,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	recorder InitialCapabilityTraceRecorder,
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

func (e defaultInitialReplyExecutor) generateStream(ctx context.Context) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	switch e.mode {
	case InitialReplyOutputModeAgentic:
		generator := agenticInitialReplyStreamGenerator
		if generator == nil {
			generator = GenerateAgenticInitialReplyStream
		}
		return generator(ctx, e.event, e.plan)
	default:
		return e.plan.Generate(ctx, e.event)
	}
}

func GenerateAgenticInitialReplyStream(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	if plan.EnableDeferredToolCollector && runtimecontext.CollectorFromContext(ctx) == nil {
		ctx = runtimecontext.WithDeferredToolCallCollector(ctx, runtimecontext.NewDeferredToolCallCollector())
	}

	tools := decorateRuntimeChatTools(defaultChatToolProvider())
	if tools == nil {
		return nil, errors.New("default chat tool provider is not configured")
	}

	builder := defaultAgenticInitialChatPlanBuilder
	if builder == nil {
		builder = BuildAgenticChatExecutionPlan
	}
	initialPlan, err := builder(ctx, InitialChatGenerationRequest{
		Event:   event,
		ModelID: plan.ModelID,
		Size:    plan.Size,
		Files:   append([]string(nil), plan.Files...),
		Input:   append([]string(nil), plan.Args...),
		Tools:   tools,
	})
	if err != nil {
		return nil, err
	}

	registry := buildInitialChatCapabilityRegistry(event, tools)
	finalizer := defaultInitialChatStreamFinalizer
	toolTurns := defaultAgenticInitialChatToolTurns
	if toolTurns <= 0 {
		toolTurns = defaultInitialChatToolTurns
	}

	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		turnReq := InitialChatTurnRequest{Plan: initialPlan}

		for turn := 0; turn < toolTurns; turn++ {
			turnResult, turnErr := defaultInitialChatTurnExecutor(ctx, turnReq)
			if turnErr != nil {
				return
			}

			stream := turnResult.Stream
			if finalizer != nil {
				stream = finalizer(ctx, initialPlan, stream)
			}
			for item := range stream {
				if !yield(item) {
					return
				}
			}

			snapshot := turnResult.Snapshot()
			if snapshot.ToolCall == nil {
				return
			}

			execution := executeInitialChatToolCall(ctx, registry, event, *snapshot.ToolCall)
			if execution.Trace != nil {
				traceCopy := *execution.Trace
				traceCopy.PreviousResponseID = strings.TrimSpace(snapshot.ResponseID)
				if !yield(&ark_dal.ModelStreamRespReasoning{CapabilityCall: &traceCopy}) {
					return
				}
			}
			if execution.NextOutput == nil || strings.TrimSpace(snapshot.ResponseID) == "" {
				return
			}

			turnReq = InitialChatTurnRequest{
				Plan:               initialPlan,
				PreviousResponseID: strings.TrimSpace(snapshot.ResponseID),
				ToolOutput:         execution.NextOutput,
			}
		}
	}, nil
}

func buildQueuedCapabilityCallFromCaptured(event *larkim.P2MessageReceiveV1, pending CapturedInitialPendingCapability) *QueuedCapabilityCall {
	if strings.TrimSpace(pending.CapabilityName) == "" {
		return nil
	}

	request := CapabilityRequest{
		Scope:       initialReplyCapabilityScope(event),
		ChatID:      strings.TrimSpace(initialReplyChatID(event)),
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

func initialReplyCapabilityScope(event *larkim.P2MessageReceiveV1) CapabilityScope {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return CapabilityScopeGroup
	}
	if event.Event.Message.ChatType != nil && strings.EqualFold(strings.TrimSpace(*event.Event.Message.ChatType), "p2p") {
		return CapabilityScopeP2P
	}
	return CapabilityScopeGroup
}

func initialReplyEventMessage(event *larkim.P2MessageReceiveV1) *larkim.EventMessage {
	if event == nil || event.Event == nil {
		return nil
	}
	return event.Event.Message
}

func initialReplyChatID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ChatId == nil {
		return ""
	}
	return *event.Event.Message.ChatId
}

func initialReplyActorOpenID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil || event.Event.Sender.SenderId.OpenId == nil {
		return ""
	}
	return *event.Event.Sender.SenderId.OpenId
}
