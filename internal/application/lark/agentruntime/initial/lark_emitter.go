package initial

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initialcore"
	message "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/message"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// LarkInitialReplyEmitter carries initial-run flow state.
type LarkInitialReplyEmitter struct {
	sendAgentic   func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error)
	replyAgentic  func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], bool, ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error)
	patchAgentic  func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error)
	sendStandard  func(context.Context, *larkim.EventMessage, string) (string, error)
	patchStandard func(context.Context, string, string) error
}

// NewLarkInitialReplyEmitter implements initial-run flow behavior.
func NewLarkInitialReplyEmitter() *LarkInitialReplyEmitter {
	return NewLarkInitialReplyEmitterForTest(
		larkmsg.SendAndUpdateStreamingCardWithRefs,
		larkmsg.SendAndReplyStreamingCardWithRefs,
		larkmsg.PatchAgentStreamingCardWithRefs,
		sendStandardInitialReply,
		larkmsg.PatchTextMessage,
	)
}

// NewLarkInitialReplyEmitterForTest implements initial-run flow behavior.
func NewLarkInitialReplyEmitterForTest(
	sendAgentic func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error),
	replyAgentic func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], bool, ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error),
	patchAgentic func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error),
	sendStandard func(context.Context, *larkim.EventMessage, string) (string, error),
	patchStandard func(context.Context, string, string) error,
) *LarkInitialReplyEmitter {
	return &LarkInitialReplyEmitter{
		sendAgentic:   sendAgentic,
		replyAgentic:  replyAgentic,
		patchAgentic:  patchAgentic,
		sendStandard:  sendStandard,
		patchStandard: patchStandard,
	}
}

// EmitInitialReply implements initial-run flow behavior.
func (e *LarkInitialReplyEmitter) EmitInitialReply(ctx context.Context, req agentruntime.InitialReplyEmissionRequest) (agentruntime.InitialReplyEmissionResult, error) {
	if e == nil {
		return agentruntime.InitialReplyEmissionResult{}, fmt.Errorf("lark initial reply emitter is nil")
	}

	stream, snapshot := initialcore.CaptureInitialReplyStream(req.Stream)
	switch req.Mode {
	case agentruntime.InitialReplyOutputModeAgentic:
		return e.emitAgenticInitialReply(ctx, req, stream, snapshot)
	case agentruntime.InitialReplyOutputModeStandard:
		return e.emitStandardInitialReply(ctx, req, stream, snapshot)
	default:
		return agentruntime.InitialReplyEmissionResult{}, fmt.Errorf("unsupported initial reply mode: %s", req.Mode)
	}
}

func (e *LarkInitialReplyEmitter) emitAgenticInitialReply(
	ctx context.Context,
	req agentruntime.InitialReplyEmissionRequest,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	snapshot func() initialcore.ReplySnapshot,
) (agentruntime.InitialReplyEmissionResult, error) {
	outputKind := normalizeAgenticOutputKind(req.OutputKind)
	cardOptions := larkmsg.AgentStreamingCardOptions{}
	if outputKind == agentruntime.AgenticOutputKindModelReply {
		cardOptions.MentionOpenID = strings.TrimSpace(req.MentionOpenID)
	}
	cardOptionArgs := initialReplyCardOptionArgs(cardOptions)

	switch resolveAgenticInitialReplyDelivery(req, e.patchAgentic != nil, e.replyAgentic != nil) {
	case initialcore.DeliveryPatch:
		return e.patchAgenticInitialReply(ctx, req, stream, snapshot)
	case initialcore.DeliveryReply:
		return e.replyAgenticInitialReply(ctx, req, stream, snapshot, cardOptionArgs)
	}

	if e.sendAgentic == nil {
		return agentruntime.InitialReplyEmissionResult{}, fmt.Errorf("lark initial reply emitter agentic sender is nil")
	}
	refs, err := e.sendAgentic(ctx, req.Message, stream, cardOptionArgs...)
	if err != nil {
		return agentruntime.InitialReplyEmissionResult{}, err
	}
	return agentruntime.InitialReplyEmissionResult{
		Reply:             mapCapturedInitialReplySnapshot(snapshot()),
		ResponseMessageID: refs.MessageID,
		ResponseCardID:    refs.CardID,
		DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
		TargetMessageID:   strings.TrimSpace(req.TargetMessageID),
		TargetCardID:      strings.TrimSpace(req.TargetCardID),
	}, nil
}

func resolveAgenticInitialReplyDelivery(
	req agentruntime.InitialReplyEmissionRequest,
	canPatch bool,
	canReply bool,
) initialcore.Delivery {
	return initialcore.ResolveDelivery(initialcore.DeliveryRequest{
		OutputKind:      string(normalizeAgenticOutputKind(req.OutputKind)),
		TargetMode:      string(req.TargetMode),
		TargetMessageID: req.TargetMessageID,
		TargetCardID:    req.TargetCardID,
		CanPatch:        canPatch,
		CanReply:        canReply,
	})
}

func (e *LarkInitialReplyEmitter) patchAgenticInitialReply(
	ctx context.Context,
	req agentruntime.InitialReplyEmissionRequest,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	snapshot func() initialcore.ReplySnapshot,
) (agentruntime.InitialReplyEmissionResult, error) {
	target := larkmsg.AgentStreamingCardRefs{
		MessageID: strings.TrimSpace(req.TargetMessageID),
		CardID:    strings.TrimSpace(req.TargetCardID),
	}
	refs, err := e.patchAgentic(ctx, target, stream)
	if err != nil {
		return agentruntime.InitialReplyEmissionResult{}, err
	}
	if refs.MessageID == "" {
		refs.MessageID = target.MessageID
	}
	if refs.CardID == "" {
		refs.CardID = target.CardID
	}
	return agentruntime.InitialReplyEmissionResult{
		Reply:             mapCapturedInitialReplySnapshot(snapshot()),
		ResponseMessageID: refs.MessageID,
		ResponseCardID:    refs.CardID,
		DeliveryMode:      agentruntime.ReplyDeliveryModePatch,
		TargetMessageID:   target.MessageID,
		TargetCardID:      target.CardID,
	}, nil
}

func (e *LarkInitialReplyEmitter) replyAgenticInitialReply(
	ctx context.Context,
	req agentruntime.InitialReplyEmissionRequest,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	snapshot func() initialcore.ReplySnapshot,
	cardOptionArgs []larkmsg.AgentStreamingCardOptions,
) (agentruntime.InitialReplyEmissionResult, error) {
	replyMessage := cloneInitialReplyMessage(req.Message, req.TargetMessageID)
	refs, err := e.replyAgentic(ctx, replyMessage, stream, req.ReplyInThread, cardOptionArgs...)
	if err != nil {
		return agentruntime.InitialReplyEmissionResult{}, err
	}
	return agentruntime.InitialReplyEmissionResult{
		Reply:             mapCapturedInitialReplySnapshot(snapshot()),
		ResponseMessageID: refs.MessageID,
		ResponseCardID:    refs.CardID,
		DeliveryMode:      agentruntime.ReplyDeliveryModeReply,
		TargetMessageID:   strings.TrimSpace(req.TargetMessageID),
		TargetCardID:      strings.TrimSpace(req.TargetCardID),
	}, nil
}

func (e *LarkInitialReplyEmitter) emitStandardInitialReply(
	ctx context.Context,
	req agentruntime.InitialReplyEmissionRequest,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	snapshot func() initialcore.ReplySnapshot,
) (agentruntime.InitialReplyEmissionResult, error) {
	for range stream {
	}
	reply := mapCapturedInitialReplySnapshot(snapshot())
	if reply.ReplyText != "" && strings.TrimSpace(req.TargetMessageID) != "" && e.patchStandard != nil {
		targetMessageID := strings.TrimSpace(req.TargetMessageID)
		if err := e.patchStandard(ctx, targetMessageID, reply.ReplyText); err != nil {
			return agentruntime.InitialReplyEmissionResult{}, err
		}
		return agentruntime.InitialReplyEmissionResult{
			Reply:             reply,
			ResponseMessageID: targetMessageID,
			DeliveryMode:      agentruntime.ReplyDeliveryModePatch,
			TargetMessageID:   targetMessageID,
			TargetCardID:      strings.TrimSpace(req.TargetCardID),
		}, nil
	}
	if reply.ReplyText == "" {
		return agentruntime.InitialReplyEmissionResult{Reply: reply}, nil
	}
	if e.sendStandard == nil {
		return agentruntime.InitialReplyEmissionResult{}, fmt.Errorf("lark initial reply emitter standard sender is nil")
	}
	messageID, err := e.sendStandard(ctx, req.Message, reply.ReplyText)
	if err != nil {
		return agentruntime.InitialReplyEmissionResult{}, err
	}
	return agentruntime.InitialReplyEmissionResult{
		Reply:             reply,
		ResponseMessageID: messageID,
		DeliveryMode:      resolveStandardInitialDeliveryMode(reply.ReplyText, messageID),
		TargetMessageID:   strings.TrimSpace(req.TargetMessageID),
		TargetCardID:      strings.TrimSpace(req.TargetCardID),
	}, nil
}

func sendStandardInitialReply(ctx context.Context, msg *larkim.EventMessage, replyText string) (string, error) {
	if msg == nil || msg.MessageId == nil {
		return "", nil
	}
	resp, err := larkmsg.ReplyMsgText(ctx, replyText, *msg.MessageId, "_chat_random", false)
	if err != nil {
		return "", err
	}
	if resp != nil && resp.Data != nil && resp.Data.MessageId != nil {
		return *resp.Data.MessageId, nil
	}
	return "", nil
}

func resolveStandardInitialDeliveryMode(replyText, messageID string) agentruntime.ReplyDeliveryMode {
	if strings.TrimSpace(replyText) == "" || strings.TrimSpace(messageID) == "" {
		return ""
	}
	return agentruntime.ReplyDeliveryModeReply
}

func cloneInitialReplyMessage(msg *larkim.EventMessage, messageID string) *larkim.EventMessage {
	targetMessageID := strings.TrimSpace(messageID)
	if msg == nil {
		return &larkim.EventMessage{MessageId: &targetMessageID}
	}
	cloned := *msg
	cloned.MessageId = &targetMessageID
	return &cloned
}

func initialReplyCardOptionArgs(opts larkmsg.AgentStreamingCardOptions) []larkmsg.AgentStreamingCardOptions {
	if strings.TrimSpace(opts.MentionOpenID) == "" {
		return nil
	}
	return []larkmsg.AgentStreamingCardOptions{opts}
}

func normalizeAgenticOutputKind(kind agentruntime.AgenticOutputKind) agentruntime.AgenticOutputKind {
	switch strings.TrimSpace(string(kind)) {
	case string(agentruntime.AgenticOutputKindSideEffect):
		return agentruntime.AgenticOutputKindSideEffect
	default:
		return agentruntime.AgenticOutputKindModelReply
	}
}

func mapCapturedInitialReplySnapshot(reply initialcore.ReplySnapshot) agentruntime.CapturedInitialReply {
	mapped := initialcore.MapSnapshot(reply)
	result := agentruntime.CapturedInitialReply{
		ThoughtText: mapped.ThoughtText,
		ReplyText:   mapped.ReplyText,
	}
	if len(mapped.CapabilityCalls) > 0 {
		result.CapabilityCalls = make([]agentruntime.CompletedCapabilityCall, 0, len(mapped.CapabilityCalls))
		for _, call := range mapped.CapabilityCalls {
			result.CapabilityCalls = append(result.CapabilityCalls, agentruntime.CompletedCapabilityCall{
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

func mapCapturedPendingCapabilitySnapshot(pending *initialcore.PendingCapability) *agentruntime.CapturedInitialPendingCapability {
	if pending == nil {
		return nil
	}
	result := &agentruntime.CapturedInitialPendingCapability{
		CallID:             pending.CallID,
		CapabilityName:     pending.CapabilityName,
		Arguments:          pending.Arguments,
		PreviousResponseID: pending.PreviousResponseID,
	}
	if pending.Approval != nil {
		result.Approval = &agentruntime.CapabilityApprovalSpec{
			Type:              pending.Approval.Type,
			Title:             pending.Approval.Title,
			Summary:           pending.Approval.Summary,
			ExpiresAt:         pending.Approval.ExpiresAt.UTC(),
			ReservationStepID: pending.Approval.ReservationStepID,
			ReservationToken:  pending.Approval.ReservationToken,
		}
	}
	if len(pending.QueueTail) > 0 {
		result.QueueTail = make([]agentruntime.CapturedInitialPendingCapability, 0, len(pending.QueueTail))
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

func initialReplyCapabilityScope(event *larkim.P2MessageReceiveV1) agentruntime.CapabilityScope {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return agentruntime.CapabilityScopeGroup
	}
	if strings.EqualFold(message.ChatType(event), "p2p") {
		return agentruntime.CapabilityScopeP2P
	}
	return agentruntime.CapabilityScopeGroup
}
