package ops

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/ratelimit"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var _ Op = &AgenticReplyChatOperator{}

type AgenticReplyChatOperator struct {
	OpBase
}

func (r *AgenticReplyChatOperator) Name() string {
	return "AgenticReplyChatOperator"
}

func (r *AgenticReplyChatOperator) FeatureInfo() *xhandler.FeatureInfo {
	return &xhandler.FeatureInfo{
		ID:          "reply_chat",
		Name:        "回复聊天功能",
		Description: "@机器人时的聊天回复功能",
		Default:     true,
	}
}

func (r *AgenticReplyChatOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	if err := requireMentionOrP2P(r.Name(), event); err != nil {
		return err
	}
	return skipIfCommand(ctx, r.Name(), event)
}

func (r *AgenticReplyChatOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(event), 256)...)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	defer progressReactionHandler(ctx, *event.Event.Message.MessageId)()

	msg := messageText(ctx, event)
	msg = larkmsg.TrimAtMsg(ctx, msg)
	if observation, ok := runtimeMessageObservation(ctx, event, meta); ok {
		ctx = runtimeOwnershipContext(ctx, observation)
	}
	decider := ratelimit.GetDecider()
	decider.RecordReply(ctx, *event.Event.Message.ChatId, ratelimit.TriggerTypeMention)
	err = agenticChatInvoker(ctx, event, meta, strings.Split(msg, " ")...)
	doneReactionHandler(ctx, *event.Event.Message.MessageId, meta)

	return
}
