package agentruntime

import (
	"context"
	"iter"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ReplyEmissionRequest struct {
	Session         *AgentSession `json:"-"`
	Run             *AgentRun     `json:"-"`
	ThoughtText     string        `json:"thought_text,omitempty"`
	ReplyText       string        `json:"reply_text,omitempty"`
	TargetMessageID string        `json:"target_message_id,omitempty"`
	TargetCardID    string        `json:"target_card_id,omitempty"`
}

type ReplyDeliveryMode string

const (
	ReplyDeliveryModeCreate ReplyDeliveryMode = "create"
	ReplyDeliveryModeReply  ReplyDeliveryMode = "reply"
	ReplyDeliveryModePatch  ReplyDeliveryMode = "patch"
)

type ReplyLifecycleState string

const (
	ReplyLifecycleStateActive      ReplyLifecycleState = "active"
	ReplyLifecycleStateSuperseded  ReplyLifecycleState = "superseded"
)

type ReplyEmissionResult struct {
	MessageID       string            `json:"message_id,omitempty"`
	CardID          string            `json:"card_id,omitempty"`
	DeliveryMode    ReplyDeliveryMode `json:"delivery_mode,omitempty"`
	TargetMessageID string            `json:"target_message_id,omitempty"`
	TargetCardID    string            `json:"target_card_id,omitempty"`
	TargetStepID    string            `json:"target_step_id,omitempty"`
}

type ReplyEmitter interface {
	EmitReply(context.Context, ReplyEmissionRequest) (ReplyEmissionResult, error)
}

func WithReplyEmitter(emitter ReplyEmitter) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.replyEmitter = emitter
		}
	}
}

type LarkReplyEmitter struct {
	resolveChatMode func(context.Context, string, string) appconfig.ChatMode
	sendAgentic     func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error)
	patchAgentic    func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error)
	replyText       func(context.Context, string, string, string, bool) (string, error)
	createText      func(context.Context, string, string, string, string) (string, error)
	patchText       func(context.Context, string, string) error
}

func NewLarkReplyEmitter() *LarkReplyEmitter {
	return NewLarkReplyEmitterForTest(
		func(ctx context.Context, chatID, openID string) appconfig.ChatMode {
			return appconfig.NewAccessor(ctx, chatID, openID).ChatMode()
		},
		larkmsg.SendAndUpdateStreamingCardWithRefs,
		larkmsg.PatchAgentStreamingCardWithRefs,
		func(ctx context.Context, text, msgID, suffix string, replyInThread bool) (string, error) {
			resp, err := larkmsg.ReplyMsgText(ctx, text, msgID, suffix, replyInThread)
			if err != nil {
				return "", err
			}
			if resp != nil && resp.Data != nil && resp.Data.MessageId != nil {
				return *resp.Data.MessageId, nil
			}
			return "", nil
		},
		func(ctx context.Context, chatID, text, msgID, suffix string) (string, error) {
			resp, err := larkmsg.CreateMsgRawContentType(ctx, chatID, larkim.MsgTypeText, larkmsg.NewTextMsgBuilder().Text(text).Build(), msgID, suffix)
			if err != nil {
				return "", err
			}
			if resp != nil && resp.Data != nil && resp.Data.MessageId != nil {
				return *resp.Data.MessageId, nil
			}
			return "", nil
		},
		larkmsg.PatchTextMessage,
	)
}

func NewLarkReplyEmitterForTest(
	resolveChatMode func(context.Context, string, string) appconfig.ChatMode,
	sendAgentic func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error),
	patchAgentic func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error),
	replyText func(context.Context, string, string, string, bool) (string, error),
	createText func(context.Context, string, string, string, string) (string, error),
	patchText func(context.Context, string, string) error,
) *LarkReplyEmitter {
	return &LarkReplyEmitter{
		resolveChatMode: resolveChatMode,
		sendAgentic:     sendAgentic,
		patchAgentic:    patchAgentic,
		replyText:       replyText,
		createText:      createText,
		patchText:       patchText,
	}
}

func (e *LarkReplyEmitter) EmitReply(ctx context.Context, req ReplyEmissionRequest) (ReplyEmissionResult, error) {
	if e == nil {
		return ReplyEmissionResult{}, nil
	}
	if strings.TrimSpace(req.ReplyText) == "" || req.Session == nil || req.Run == nil {
		return ReplyEmissionResult{}, nil
	}

	mode := appconfig.ChatModeStandard
	if e.resolveChatMode != nil {
		mode = e.resolveChatMode(ctx, strings.TrimSpace(req.Session.ChatID), strings.TrimSpace(req.Run.ActorOpenID)).Normalize()
	}

	if mode == appconfig.ChatModeAgentic {
		refsTarget := larkmsg.AgentStreamingCardRefs{
			MessageID: strings.TrimSpace(req.TargetMessageID),
			CardID:    strings.TrimSpace(req.TargetCardID),
		}
		if refsTarget.CardID != "" && e.patchAgentic != nil {
			refs, err := e.patchAgentic(ctx, refsTarget, singleReplySeq(req.ThoughtText, req.ReplyText))
			if err != nil {
				return ReplyEmissionResult{}, err
			}
			if refs.MessageID == "" {
				refs.MessageID = refsTarget.MessageID
			}
			if refs.CardID == "" {
				refs.CardID = refsTarget.CardID
			}
			return ReplyEmissionResult{
				MessageID:       refs.MessageID,
				CardID:          refs.CardID,
				DeliveryMode:    ReplyDeliveryModePatch,
				TargetMessageID: refsTarget.MessageID,
				TargetCardID:    refsTarget.CardID,
			}, nil
		}
		if e.sendAgentic == nil {
			return ReplyEmissionResult{}, nil
		}
		chatID := strings.TrimSpace(req.Session.ChatID)
		triggerID := strings.TrimSpace(req.Run.TriggerMessageID)
		msg := &larkim.EventMessage{ChatId: &chatID, MessageId: &triggerID}
		refs, err := e.sendAgentic(ctx, msg, singleReplySeq(req.ThoughtText, req.ReplyText))
		if err != nil {
			return ReplyEmissionResult{}, err
		}
		return ReplyEmissionResult{
			MessageID:    refs.MessageID,
			CardID:       refs.CardID,
			DeliveryMode: ReplyDeliveryModeCreate,
		}, nil
	}

	if targetMessageID := strings.TrimSpace(req.TargetMessageID); targetMessageID != "" && e.patchText != nil {
		if err := e.patchText(ctx, targetMessageID, strings.TrimSpace(req.ReplyText)); err != nil {
			return ReplyEmissionResult{}, err
		}
		return ReplyEmissionResult{
			MessageID:       targetMessageID,
			DeliveryMode:    ReplyDeliveryModePatch,
			TargetMessageID: targetMessageID,
		}, nil
	}

	if triggerID := strings.TrimSpace(req.Run.TriggerMessageID); triggerID != "" && e.replyText != nil {
		messageID, err := e.replyText(ctx, strings.TrimSpace(req.ReplyText), triggerID, "_agent_runtime_reply", false)
		if err != nil {
			return ReplyEmissionResult{}, err
		}
		return ReplyEmissionResult{
			MessageID:    messageID,
			DeliveryMode: ReplyDeliveryModeReply,
		}, nil
	}
	if e.createText == nil {
		return ReplyEmissionResult{}, nil
	}
	messageID, err := e.createText(ctx, strings.TrimSpace(req.Session.ChatID), strings.TrimSpace(req.ReplyText), strings.TrimSpace(req.Run.ID), "_agent_runtime_reply")
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	return ReplyEmissionResult{
		MessageID:    messageID,
		DeliveryMode: ReplyDeliveryModeCreate,
	}, nil
}

func singleReplySeq(thoughtText, replyText string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		yield(&ark_dal.ModelStreamRespReasoning{
			ContentStruct: ark_dal.ContentStruct{
				Thought: strings.TrimSpace(thoughtText),
				Reply:   strings.TrimSpace(replyText),
			},
		})
	}
}
