package runtimecutover

import (
	"context"
	"fmt"
	"iter"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type replyOutputMode string

const (
	replyOutputModeAgentic  replyOutputMode = "agentic"
	replyOutputModeStandard replyOutputMode = "standard"
)

type replyOutputRequest struct {
	Mode            replyOutputMode
	Message         *larkim.EventMessage
	TargetMessageID string
	TargetCardID    string
	Stream          iter.Seq[*ark_dal.ModelStreamRespReasoning]
}

type replyOutputResult struct {
	Refs            larkmsg.AgentStreamingCardRefs
	Reply           capturedRuntimeReply
	DeliveryMode    agentruntime.ReplyDeliveryMode
	TargetMessageID string
	TargetCardID    string
}

type replyOrchestrator struct {
	agenticSender   func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error)
	agenticPatcher  func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error)
	standardSender  func(context.Context, *larkim.EventMessage, string) (string, error)
	standardPatcher func(context.Context, string, string) error
}

func (o *replyOrchestrator) EmitInitialReply(ctx context.Context, req agentruntime.InitialReplyEmissionRequest) (agentruntime.InitialReplyEmissionResult, error) {
	result, err := o.emit(ctx, replyOutputRequest{
		Mode:            mapInitialReplyOutputMode(req.Mode),
		Message:         req.Message,
		TargetMessageID: req.TargetMessageID,
		TargetCardID:    req.TargetCardID,
		Stream:          req.Stream,
	})
	if err != nil {
		return agentruntime.InitialReplyEmissionResult{}, err
	}
	return agentruntime.InitialReplyEmissionResult{
		Reply:             mapCapturedInitialReply(result.Reply),
		ResponseMessageID: result.Refs.MessageID,
		ResponseCardID:    result.Refs.CardID,
		DeliveryMode:      result.DeliveryMode,
		TargetMessageID:   result.TargetMessageID,
		TargetCardID:      result.TargetCardID,
	}, nil
}

func (o *replyOrchestrator) emit(ctx context.Context, req replyOutputRequest) (replyOutputResult, error) {
	if o == nil {
		return replyOutputResult{}, fmt.Errorf("reply orchestrator is nil")
	}

	stream, snapshot := captureRuntimeReplyStream(req.Stream)
	switch req.Mode {
	case replyOutputModeAgentic:
		if req.TargetCardID != "" && o.agenticPatcher != nil {
			target := larkmsg.AgentStreamingCardRefs{
				MessageID: req.TargetMessageID,
				CardID:    req.TargetCardID,
			}
			refs, err := o.agenticPatcher(ctx, target, stream)
			if err != nil {
				return replyOutputResult{}, err
			}
			if refs.MessageID == "" {
				refs.MessageID = target.MessageID
			}
			if refs.CardID == "" {
				refs.CardID = target.CardID
			}
			return replyOutputResult{
				Refs:            refs,
				Reply:           snapshot(),
				DeliveryMode:    agentruntime.ReplyDeliveryModePatch,
				TargetMessageID: target.MessageID,
				TargetCardID:    target.CardID,
			}, nil
		}
		if o.agenticSender == nil {
			return replyOutputResult{}, fmt.Errorf("reply orchestrator agentic sender is not configured")
		}
		refs, err := o.agenticSender(ctx, req.Message, stream)
		if err != nil {
			return replyOutputResult{}, err
		}
		return replyOutputResult{
			Refs:            refs,
			Reply:           snapshot(),
			DeliveryMode:    agentruntime.ReplyDeliveryModeCreate,
			TargetMessageID: req.TargetMessageID,
			TargetCardID:    req.TargetCardID,
		}, nil
	case replyOutputModeStandard:
		for range stream {
		}
		result := snapshot()
		refs := larkmsg.AgentStreamingCardRefs{}
		if result.ReplyText != "" && req.TargetMessageID != "" && o.standardPatcher != nil {
			if err := o.standardPatcher(ctx, req.TargetMessageID, result.ReplyText); err != nil {
				return replyOutputResult{}, err
			}
			refs.MessageID = req.TargetMessageID
			return replyOutputResult{
				Refs:            refs,
				Reply:           result,
				DeliveryMode:    agentruntime.ReplyDeliveryModePatch,
				TargetMessageID: req.TargetMessageID,
				TargetCardID:    req.TargetCardID,
			}, nil
		}
		if result.ReplyText != "" {
			if o.standardSender == nil {
				return replyOutputResult{}, fmt.Errorf("reply orchestrator standard sender is not configured")
			}
			messageID, err := o.standardSender(ctx, req.Message, result.ReplyText)
			if err != nil {
				return replyOutputResult{}, err
			}
			refs.MessageID = messageID
		}
		return replyOutputResult{
			Refs:            refs,
			Reply:           result,
			DeliveryMode:    resolveStandardDeliveryMode(result.ReplyText, refs.MessageID),
			TargetMessageID: req.TargetMessageID,
			TargetCardID:    req.TargetCardID,
		}, nil
	default:
		return replyOutputResult{}, fmt.Errorf("unsupported reply output mode: %s", req.Mode)
	}
}

func resolveStandardDeliveryMode(replyText, messageID string) agentruntime.ReplyDeliveryMode {
	if messageID == "" || replyText == "" {
		return ""
	}
	return agentruntime.ReplyDeliveryModeReply
}

func mapInitialReplyOutputMode(mode agentruntime.InitialReplyOutputMode) replyOutputMode {
	switch mode {
	case agentruntime.InitialReplyOutputModeStandard:
		return replyOutputModeStandard
	default:
		return replyOutputModeAgentic
	}
}

func mapCapturedInitialReply(reply capturedRuntimeReply) agentruntime.CapturedInitialReply {
	var pending *agentruntime.CapturedInitialPendingCapability
	if reply.PendingCapability != nil {
		pending = &agentruntime.CapturedInitialPendingCapability{
			CallID:             reply.PendingCapability.CallID,
			CapabilityName:     reply.PendingCapability.CapabilityName,
			Arguments:          reply.PendingCapability.Arguments,
			PreviousResponseID: reply.PendingCapability.PreviousResponseID,
		}
		if reply.PendingCapability.Approval != nil {
			approval := *reply.PendingCapability.Approval
			pending.Approval = &approval
		}
		if len(reply.PendingCapability.QueueTail) > 0 {
			pending.QueueTail = mapCapturedPendingQueue(reply.PendingCapability.QueueTail)
		}
	}
	return agentruntime.CapturedInitialReply{
		CapabilityCalls:   append([]agentruntime.CompletedCapabilityCall(nil), reply.CapabilityCalls...),
		PendingCapability: pending,
		ThoughtText:       reply.ThoughtText,
		ReplyText:         reply.ReplyText,
	}
}

func mapCapturedPendingQueue(queue []capturedPendingCapability) []agentruntime.CapturedInitialPendingCapability {
	if len(queue) == 0 {
		return nil
	}
	result := make([]agentruntime.CapturedInitialPendingCapability, 0, len(queue))
	for _, item := range queue {
		pending := agentruntime.CapturedInitialPendingCapability{
			CallID:             item.CallID,
			CapabilityName:     item.CapabilityName,
			Arguments:          item.Arguments,
			PreviousResponseID: item.PreviousResponseID,
		}
		if item.Approval != nil {
			approval := *item.Approval
			pending.Approval = &approval
		}
		if len(item.QueueTail) > 0 {
			pending.QueueTail = mapCapturedPendingQueue(item.QueueTail)
		}
		result = append(result, pending)
	}
	return result
}
