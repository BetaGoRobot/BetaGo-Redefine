package ops

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
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

// PreRun Music
//
//	@receiver r *MusicMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:34:09
func (r *ReplyChatOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	if *event.Event.Message.ChatType != "p2p" && !larkmsg.IsMentioned(event.Event.Message.Mentions) {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" Not Mentioned")
	}

	if command.LarkRootCommand.IsCommand(ctx, larkmsg.PreGetTextMsg(ctx, event).GetText()) {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" Not Mentioned")
	}
	return
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *ReplyChatOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(event)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	defer span.RecordError(err)

	reactionID, err := larkmsg.AddReaction(ctx, "OnIt", *event.Event.Message.MessageId)
	if err != nil {
		logs.L().Ctx(ctx).Error("Add reaction to msg failed", zap.Error(err))
	} else {
		defer larkmsg.RemoveReactionAsync(ctx, reactionID, *event.Event.Message.MessageId)
	}

	msg := larkmsg.PreGetTextMsg(ctx, event).GetText()
	msg = larkmsg.TrimAtMsg(ctx, msg)
	err = handlers.ChatHandler("chat")(ctx, event, meta, strings.Split(msg, " ")...)
	if !meta.SkipDone {
		larkmsg.AddReactionAsync(ctx, "DONE", *event.Event.Message.MessageId)
	}
	return
}
