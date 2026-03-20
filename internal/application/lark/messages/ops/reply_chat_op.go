package ops

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/ratelimit"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var _ Op = &ReplyChatOperator{}

// ReplyChatOperator Repeat
//
//	@author heyuhengmatt
//	@update 2024-07-17 01:36:07
type ReplyChatOperator struct {
	OpBase
}

func (r *ReplyChatOperator) Name() string {
	return "ReplyChatOperator"
}

// FeatureInfo 返回功能信息
func (r *ReplyChatOperator) FeatureInfo() *xhandler.FeatureInfo {
	return &xhandler.FeatureInfo{
		ID:          "reply_chat",
		Name:        "回复聊天功能",
		Description: "@机器人时的聊天回复功能",
		Default:     true,
	}
}

// PreRun Music
//
//	@receiver r *MusicMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:34:09
func (r *ReplyChatOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	if err := requireMentionOrP2P(r.Name(), event); err != nil {
		return err
	}

	return skipIfCommand(ctx, r.Name(), event)
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *ReplyChatOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(event), 256)...)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	defer withProgressReaction(ctx, *event.Event.Message.MessageId)()

	msg := messageText(ctx, event)
	msg = larkmsg.TrimAtMsg(ctx, msg)
	observation, ok := observeRuntimeMessage(ctx, event, meta)
	ctx = runtimeContextForObservedMessage(ctx, messageConfigAccessor(ctx, event, meta).ChatMode().Normalize(), observation, ok,
		agentruntime.TriggerTypeMention,
		agentruntime.TriggerTypeReplyToBot,
		agentruntime.TriggerTypeFollowUp,
	)
	// 记录回复
	decider := ratelimit.GetDecider()
	decider.RecordReply(ctx, *event.Event.Message.ChatId, ratelimit.TriggerTypeMention)
	err = xcommand.BindCLI(handlers.Chat)(ctx, event, meta, strings.Split(msg, " ")...)
	addDoneReactionIfNeeded(ctx, *event.Event.Message.MessageId, meta)

	return
}
