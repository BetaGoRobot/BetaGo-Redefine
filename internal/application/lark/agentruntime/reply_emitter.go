package agentruntime

import (
	"context"
	"iter"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// ReplyEmissionRequest describes the final user-visible reply the runtime wants
// to emit together with the preferred reply target.
type ReplyEmissionRequest struct {
	Session         *AgentSession     `json:"-"`
	Run             *AgentRun         `json:"-"`
	OutputKind      AgenticOutputKind `json:"output_kind,omitempty"`
	MentionOpenID   string            `json:"mention_open_id,omitempty"`
	ThoughtText     string            `json:"thought_text,omitempty"`
	ReplyText       string            `json:"reply_text,omitempty"`
	TargetMessageID string            `json:"target_message_id,omitempty"`
	TargetCardID    string            `json:"target_card_id,omitempty"`
	ReplyInThread   bool              `json:"reply_in_thread,omitempty"`
}

// ReplyDeliveryMode records whether a reply created a new message, replied to an
// existing message, or patched an existing card or text target.
type ReplyDeliveryMode string

const (
	ReplyDeliveryModeCreate ReplyDeliveryMode = "create"
	ReplyDeliveryModeReply  ReplyDeliveryMode = "reply"
	ReplyDeliveryModePatch  ReplyDeliveryMode = "patch"
)

// ReplyLifecycleState marks whether a stored reply step is still active or has
// already been superseded by a newer reply.
type ReplyLifecycleState string

const (
	ReplyLifecycleStateActive     ReplyLifecycleState = "active"
	ReplyLifecycleStateSuperseded ReplyLifecycleState = "superseded"
)

// ReplyEmissionResult captures the identifiers and delivery mode produced by a reply emission.
type ReplyEmissionResult struct {
	MessageID       string            `json:"message_id,omitempty"`
	CardID          string            `json:"card_id,omitempty"`
	DeliveryMode    ReplyDeliveryMode `json:"delivery_mode,omitempty"`
	TargetMessageID string            `json:"target_message_id,omitempty"`
	TargetCardID    string            `json:"target_card_id,omitempty"`
	TargetStepID    string            `json:"target_step_id,omitempty"`
}

// ReplyEmitter defines the contract for components that emit user-visible runtime replies.
type ReplyEmitter interface {
	EmitReply(context.Context, ReplyEmissionRequest) (ReplyEmissionResult, error)
}

// WithReplyEmitter injects the reply emitter used by continuation processing.
func WithReplyEmitter(emitter ReplyEmitter) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.replyEmitter = emitter
		}
	}
}

// LarkReplyEmitter is the production reply emitter that targets Lark cards or
// text messages depending on chat mode and reply target availability.
type LarkReplyEmitter struct {
	resolveChatMode func(context.Context, string, string) appconfig.ChatMode
	sendAgentic     func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error)
	replyAgentic    func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], bool, ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error)
	patchAgentic    func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error)
	replyText       func(context.Context, string, string, string, bool) (string, error)
	createText      func(context.Context, string, string, string, string) (string, error)
	patchText       func(context.Context, string, string) error
}

// NewLarkReplyEmitter constructs the production Lark-backed reply emitter.
func NewLarkReplyEmitter() *LarkReplyEmitter {
	return NewLarkReplyEmitterForTest(
		func(ctx context.Context, chatID, openID string) appconfig.ChatMode {
			return appconfig.NewAccessor(ctx, chatID, openID).ChatMode()
		},
		larkmsg.SendAndUpdateStreamingCardWithRefs,
		larkmsg.SendAndReplyStreamingCardWithRefs,
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

// NewLarkReplyEmitterForTest constructs a Lark reply emitter with injectable transport functions.
func NewLarkReplyEmitterForTest(
	resolveChatMode func(context.Context, string, string) appconfig.ChatMode,
	sendAgentic func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error),
	replyAgentic func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], bool, ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error),
	patchAgentic func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error),
	replyText func(context.Context, string, string, string, bool) (string, error),
	createText func(context.Context, string, string, string, string) (string, error),
	patchText func(context.Context, string, string) error,
) *LarkReplyEmitter {
	return &LarkReplyEmitter{
		resolveChatMode: resolveChatMode,
		sendAgentic:     sendAgentic,
		replyAgentic:    replyAgentic,
		patchAgentic:    patchAgentic,
		replyText:       replyText,
		createText:      createText,
		patchText:       patchText,
	}
}

// EmitReply sends, replies to, or patches the user-visible runtime reply according to the configured chat mode and reply target.
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
		outputKind := normalizeAgenticOutputKind(req.OutputKind)
		refsTarget := larkmsg.AgentStreamingCardRefs{
			MessageID: strings.TrimSpace(req.TargetMessageID),
			CardID:    strings.TrimSpace(req.TargetCardID),
		}
		cardOptions := larkmsg.AgentStreamingCardOptions{}
		if outputKind == AgenticOutputKindModelReply {
			cardOptions.MentionOpenID = strings.TrimSpace(req.MentionOpenID)
			if cardOptions.MentionOpenID == "" && req.Run != nil {
				cardOptions.MentionOpenID = strings.TrimSpace(req.Run.ActorOpenID)
			}
		}
		if outputKind == AgenticOutputKindModelReply && refsTarget.CardID != "" && e.patchAgentic != nil {
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
			runtimecontext.RecordActiveAgenticReplyTarget(ctx, refs.MessageID, refs.CardID)
			return ReplyEmissionResult{
				MessageID:       refs.MessageID,
				CardID:          refs.CardID,
				DeliveryMode:    ReplyDeliveryModePatch,
				TargetMessageID: refsTarget.MessageID,
				TargetCardID:    refsTarget.CardID,
			}, nil
		}
		cardOptionArgs := agenticCardOptionArgs(cardOptions)
		if refsTarget.MessageID != "" && e.replyAgentic != nil {
			replyMessage := &larkim.EventMessage{MessageId: &refsTarget.MessageID}
			if req.Session != nil {
				chatID := strings.TrimSpace(req.Session.ChatID)
				replyMessage.ChatId = &chatID
			}
			refs, err := e.replyAgentic(ctx, replyMessage, singleReplySeq(req.ThoughtText, req.ReplyText), req.ReplyInThread, cardOptionArgs...)
			if err != nil {
				return ReplyEmissionResult{}, err
			}
			runtimecontext.RecordActiveAgenticReplyTarget(ctx, refs.MessageID, refs.CardID)
			return ReplyEmissionResult{
				MessageID:       refs.MessageID,
				CardID:          refs.CardID,
				DeliveryMode:    ReplyDeliveryModeReply,
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
		refs, err := e.sendAgentic(ctx, msg, singleReplySeq(req.ThoughtText, req.ReplyText), cardOptionArgs...)
		if err != nil {
			return ReplyEmissionResult{}, err
		}
		runtimecontext.RecordActiveAgenticReplyTarget(ctx, refs.MessageID, refs.CardID)
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

func agenticCardOptionArgs(opts larkmsg.AgentStreamingCardOptions) []larkmsg.AgentStreamingCardOptions {
	if strings.TrimSpace(opts.MentionOpenID) == "" {
		return nil
	}
	return []larkmsg.AgentStreamingCardOptions{opts}
}
