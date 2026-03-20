package runtimecutover

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/runtimewire"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type runProcessor interface {
	ProcessRun(context.Context, agentruntime.RunProcessorInput) error
}

type capturedPendingCapability struct {
	CallID             string
	CapabilityName     string
	Arguments          string
	PreviousResponseID string
	Approval           *agentruntime.CapabilityApprovalSpec
	QueueTail          []capturedPendingCapability
}

type capturedRuntimeReply struct {
	CapabilityCalls   []agentruntime.CompletedCapabilityCall
	PendingCapability *capturedPendingCapability
	ThoughtText       string
	ReplyText         string
}

type Handler struct {
	now              func() time.Time
	processorBuilder func(context.Context, agentruntime.InitialReplyEmitter) runProcessor
	output           *replyOrchestrator
	cardSender       func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error)
}

func BuildDefaultHandler(context.Context) agentruntime.RuntimeAgenticCutoverHandler {
	return &Handler{
		now: func() time.Time { return time.Now().UTC() },
		processorBuilder: func(ctx context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			return runtimewire.BuildRunProcessor(ctx, emitter)
		},
		output: &replyOrchestrator{
			agenticSender:  larkmsg.SendAndUpdateStreamingCardWithRefs,
			agenticPatcher: larkmsg.PatchAgentStreamingCardWithRefs,
		},
		cardSender: larkmsg.SendAndUpdateStreamingCardWithRefs,
	}
}

func (h *Handler) Handle(ctx context.Context, req agentruntime.RuntimeAgenticCutoverRequest) error {
	if req.Event == nil || req.Event.Event == nil || req.Event.Event.Message == nil {
		return fmt.Errorf("runtime agentic cutover event is required")
	}
	if h == nil || h.cardSender == nil {
		return fmt.Errorf("runtime agentic cutover card sender is not configured")
	}

	output := h.outputOrchestrator()
	processor := h.buildProcessor(ctx, output)
	initial := agentruntime.InitialRunInput{
		Start: agentruntime.StartShadowRunRequest{
			ChatID:           strings.TrimSpace(messageChatID(req.Event)),
			ActorOpenID:      strings.TrimSpace(botidentity.MessageSenderOpenID(req.Event)),
			TriggerType:      resolveInitialTriggerType(ctx, req.Event, req.Ownership),
			TriggerMessageID: strings.TrimSpace(messageID(req.Event)),
			AttachToRunID:    strings.TrimSpace(req.Ownership.AttachToRunID),
			SupersedeRunID:   strings.TrimSpace(req.Ownership.SupersedeRunID),
			InputText:        resolveInputText(ctx, req),
			Now:              h.resolveStartedAt(req.StartedAt),
		},
		Event:      req.Event,
		Plan:       req.Plan,
		OutputMode: agentruntime.InitialReplyOutputModeAgentic,
	}
	if processor == nil {
		executor, err := initial.BuildExecutor(output)
		if err != nil {
			return err
		}
		_, err = executor.ProduceInitialReply(ctx)
		return err
	}
	return processor.ProcessRun(ctx, agentruntime.RunProcessorInput{
		Initial: &initial,
	})
}

func (h *Handler) buildProcessor(ctx context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
	if h != nil && h.processorBuilder != nil {
		return h.processorBuilder(ctx, emitter)
	}
	return nil
}

func (h *Handler) resolveStartedAt(v time.Time) time.Time {
	if !v.IsZero() {
		return v.UTC()
	}
	if h != nil && h.now != nil {
		return h.now().UTC()
	}
	return time.Now().UTC()
}

func (h *Handler) outputOrchestrator() *replyOrchestrator {
	if h != nil && h.output != nil {
		return h.output
	}
	if h != nil && h.cardSender != nil {
		return &replyOrchestrator{
			agenticSender:  h.cardSender,
			agenticPatcher: larkmsg.PatchAgentStreamingCardWithRefs,
		}
	}
	return nil
}

func captureRuntimeReplyStream(seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (iter.Seq[*ark_dal.ModelStreamRespReasoning], func() capturedRuntimeReply) {
	var thoughtBuilder strings.Builder
	var replyBuilder strings.Builder
	result := capturedRuntimeReply{}
	seenCapabilityCalls := make(map[string]struct{})

	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
			for item := range seq {
				if item == nil {
					continue
				}
				if item.CapabilityCall != nil {
					if item.CapabilityCall.Pending {
						if result.PendingCapability == nil {
							result.PendingCapability = &capturedPendingCapability{
								CallID:             strings.TrimSpace(item.CapabilityCall.CallID),
								CapabilityName:     strings.TrimSpace(item.CapabilityCall.FunctionName),
								Arguments:          strings.TrimSpace(item.CapabilityCall.Arguments),
								PreviousResponseID: strings.TrimSpace(item.CapabilityCall.PreviousResponseID),
							}
							if title := strings.TrimSpace(item.CapabilityCall.ApprovalTitle); title != "" ||
								strings.TrimSpace(item.CapabilityCall.ApprovalSummary) != "" ||
								strings.TrimSpace(item.CapabilityCall.ApprovalType) != "" ||
								!item.CapabilityCall.ApprovalExpiresAt.IsZero() {
								result.PendingCapability.Approval = &agentruntime.CapabilityApprovalSpec{
									Type:      strings.TrimSpace(item.CapabilityCall.ApprovalType),
									Title:     strings.TrimSpace(item.CapabilityCall.ApprovalTitle),
									Summary:   strings.TrimSpace(item.CapabilityCall.ApprovalSummary),
									ExpiresAt: item.CapabilityCall.ApprovalExpiresAt.UTC(),
								}
							}
						} else {
							result.PendingCapability.QueueTail = append(result.PendingCapability.QueueTail, capturedPendingCapability{
								CallID:             strings.TrimSpace(item.CapabilityCall.CallID),
								CapabilityName:     strings.TrimSpace(item.CapabilityCall.FunctionName),
								Arguments:          strings.TrimSpace(item.CapabilityCall.Arguments),
								PreviousResponseID: strings.TrimSpace(item.CapabilityCall.PreviousResponseID),
								Approval:           buildCapturedPendingApproval(item.CapabilityCall),
							})
						}
						if !yield(item) {
							return
						}
						continue
					}
					callID := strings.TrimSpace(item.CapabilityCall.CallID)
					if callID == "" {
						result.CapabilityCalls = append(result.CapabilityCalls, agentruntime.CompletedCapabilityCall{
							CallID:             callID,
							CapabilityName:     strings.TrimSpace(item.CapabilityCall.FunctionName),
							Arguments:          strings.TrimSpace(item.CapabilityCall.Arguments),
							Output:             strings.TrimSpace(item.CapabilityCall.Output),
							PreviousResponseID: strings.TrimSpace(item.CapabilityCall.PreviousResponseID),
						})
					} else if _, exists := seenCapabilityCalls[callID]; !exists {
						seenCapabilityCalls[callID] = struct{}{}
						result.CapabilityCalls = append(result.CapabilityCalls, agentruntime.CompletedCapabilityCall{
							CallID:             callID,
							CapabilityName:     strings.TrimSpace(item.CapabilityCall.FunctionName),
							Arguments:          strings.TrimSpace(item.CapabilityCall.Arguments),
							Output:             strings.TrimSpace(item.CapabilityCall.Output),
							PreviousResponseID: strings.TrimSpace(item.CapabilityCall.PreviousResponseID),
						})
					}
				}
				if item.ReasoningContent != "" {
					thoughtBuilder.WriteString(item.ReasoningContent)
					result.ThoughtText = strings.TrimSpace(thoughtBuilder.String())
				}
				if thought := strings.TrimSpace(item.ContentStruct.Thought); thought != "" {
					result.ThoughtText = thought
				}
				if reply := strings.TrimSpace(item.ContentStruct.Reply); reply != "" {
					result.ReplyText = reply
				} else if item.Content != "" {
					replyBuilder.WriteString(item.Content)
					if strings.TrimSpace(result.ReplyText) == "" {
						result.ReplyText = strings.TrimSpace(replyBuilder.String())
					}
				}
				if !yield(item) {
					return
				}
			}
		}, func() capturedRuntimeReply {
			var pendingCopy *capturedPendingCapability
			if result.PendingCapability != nil {
				pending := *result.PendingCapability
				if pending.Approval != nil {
					approval := *pending.Approval
					pending.Approval = &approval
				}
				if len(pending.QueueTail) > 0 {
					pending.QueueTail = cloneCapturedPendingQueue(pending.QueueTail)
				}
				pendingCopy = &pending
			}
			return capturedRuntimeReply{
				CapabilityCalls:   append([]agentruntime.CompletedCapabilityCall(nil), result.CapabilityCalls...),
				PendingCapability: pendingCopy,
				ThoughtText:       strings.TrimSpace(result.ThoughtText),
				ReplyText:         strings.TrimSpace(result.ReplyText),
			}
		}
}

func buildQueuedCapabilityCall(event *larkim.P2MessageReceiveV1, pending capturedPendingCapability) *agentruntime.QueuedCapabilityCall {
	if strings.TrimSpace(pending.CapabilityName) == "" {
		return nil
	}

	request := agentruntime.CapabilityRequest{
		Scope:       resolveCapabilityScope(event),
		ChatID:      strings.TrimSpace(messageChatID(event)),
		PayloadJSON: []byte(strings.TrimSpace(pending.Arguments)),
	}
	call := &agentruntime.QueuedCapabilityCall{
		CallID:         strings.TrimSpace(pending.CallID),
		CapabilityName: strings.TrimSpace(pending.CapabilityName),
		Input: agentruntime.CapabilityCallInput{
			Request: request,
		},
	}
	if previousResponseID := strings.TrimSpace(pending.PreviousResponseID); previousResponseID != "" {
		call.Input.Continuation = &agentruntime.CapabilityContinuationInput{
			PreviousResponseID: previousResponseID,
		}
	}
	if pending.Approval != nil {
		approval := *pending.Approval
		call.Input.Approval = &approval
	}
	if len(pending.QueueTail) > 0 {
		call.Input.QueueTail = buildQueuedCapabilityTail(event, pending.QueueTail)
	}
	return call
}

func buildQueuedCapabilityTail(event *larkim.P2MessageReceiveV1, queue []capturedPendingCapability) []agentruntime.QueuedCapabilityCall {
	if len(queue) == 0 {
		return nil
	}
	result := make([]agentruntime.QueuedCapabilityCall, 0, len(queue))
	for _, item := range queue {
		call := buildQueuedCapabilityCall(event, item)
		if call == nil {
			continue
		}
		result = append(result, *call)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func buildCapturedPendingApproval(trace *ark_dal.CapabilityCallTrace) *agentruntime.CapabilityApprovalSpec {
	if trace == nil {
		return nil
	}
	if strings.TrimSpace(trace.ApprovalTitle) == "" &&
		strings.TrimSpace(trace.ApprovalSummary) == "" &&
		strings.TrimSpace(trace.ApprovalType) == "" &&
		trace.ApprovalExpiresAt.IsZero() {
		return nil
	}
	return &agentruntime.CapabilityApprovalSpec{
		Type:      strings.TrimSpace(trace.ApprovalType),
		Title:     strings.TrimSpace(trace.ApprovalTitle),
		Summary:   strings.TrimSpace(trace.ApprovalSummary),
		ExpiresAt: trace.ApprovalExpiresAt.UTC(),
	}
}

func cloneCapturedPendingQueue(src []capturedPendingCapability) []capturedPendingCapability {
	if len(src) == 0 {
		return nil
	}
	dst := make([]capturedPendingCapability, 0, len(src))
	for _, item := range src {
		copied := item
		if item.Approval != nil {
			approval := *item.Approval
			copied.Approval = &approval
		}
		if len(item.QueueTail) > 0 {
			copied.QueueTail = cloneCapturedPendingQueue(item.QueueTail)
		}
		dst = append(dst, copied)
	}
	return dst
}

func resolveCapabilityScope(event *larkim.P2MessageReceiveV1) agentruntime.CapabilityScope {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return agentruntime.CapabilityScopeGroup
	}
	if event.Event.Message.ChatType != nil && strings.EqualFold(strings.TrimSpace(*event.Event.Message.ChatType), "p2p") {
		return agentruntime.CapabilityScopeP2P
	}
	return agentruntime.CapabilityScopeGroup
}

func resolveInputText(ctx context.Context, req agentruntime.RuntimeAgenticCutoverRequest) string {
	if input := strings.TrimSpace(strings.Join(req.Plan.Args, " ")); input != "" {
		return input
	}
	return strings.TrimSpace(extractEventText(ctx, req.Event))
}

func resolveTriggerType(ctx context.Context, event *larkim.P2MessageReceiveV1) agentruntime.TriggerType {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return agentruntime.TriggerTypeFollowUp
	}
	if event.Event.Message.ChatType != nil && strings.EqualFold(strings.TrimSpace(*event.Event.Message.ChatType), "p2p") {
		return agentruntime.TriggerTypeP2P
	}
	if larkmsg.IsMentioned(event.Event.Message.Mentions) {
		return agentruntime.TriggerTypeMention
	}
	if strings.HasPrefix(strings.TrimSpace(extractEventText(ctx, event)), "/") {
		return agentruntime.TriggerTypeCommandBridge
	}
	return agentruntime.TriggerTypeFollowUp
}

func resolveInitialTriggerType(ctx context.Context, event *larkim.P2MessageReceiveV1, ownership agentruntime.InitialRunOwnership) agentruntime.TriggerType {
	if ownership.TriggerType != "" {
		return ownership.TriggerType
	}
	return resolveTriggerType(ctx, event)
}

func extractEventText(ctx context.Context, event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.Content == nil || event.Event.Message.MessageType == nil {
		return ""
	}
	return larkmsg.PreGetTextMsg(ctx, event).GetText()
}

func messageChatID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ChatId == nil {
		return ""
	}
	return *event.Event.Message.ChatId
}

func messageID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.MessageId == nil {
		return ""
	}
	return *event.Event.Message.MessageId
}
