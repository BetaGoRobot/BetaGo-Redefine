package ops

import (
	"context"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/consts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

var _ Op = &CommandOperator{}

// CommandOperator Repeat
type CommandOperator struct {
	OpBase
	command string
}

func (r *CommandOperator) Name() string {
	return "CommandOperator"
}

// PreRun Music
//
//	@receiver r *MusicMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:34:09
func (r *CommandOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	defer span.RecordError(err)

	if !command.LarkRootCommand.IsCommand(ctx, larkmsg.PreGetTextMsg(ctx, event).GetText()) {
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
func (r *CommandOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(event)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	defer span.RecordError(err)
	rawCommand := larkmsg.PreGetTextMsg(ctx, event).GetText()

	return ExecuteFromRawCommand(ctx, event, meta, rawCommand)
}

func ExecuteFromRawCommand(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, rawCommand string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(event)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	defer span.RecordError(err)

	rawCommand = strings.ReplaceAll(rawCommand, "<b>", " ")
	rawCommand = strings.ReplaceAll(rawCommand, "</b>", " ")
	ctx = context.WithValue(ctx, consts.ContextVarSrcCmd, rawCommand)
	commands := xcommand.GetCommand(ctx, rawCommand)
	if len(commands) > 0 {
		meta.IsCommand = true
		var reactionID string
		reactionID, err = larkmsg.AddReaction(ctx, "OnIt", *event.Event.Message.MessageId)
		if err != nil {
			logs.L().Ctx(ctx).Error("Add reaction to msg failed", zap.Error(err))
		} else {
			defer larkmsg.RemoveReactionAsync(ctx, reactionID, *event.Event.Message.MessageId)
		}
		err = command.LarkRootCommand.Execute(ctx, event, meta, commands)
		if err != nil {
			span.RecordError(err)
			if errors.Is(err, xerror.ErrCommandNotFound) {
				meta.IsCommand = false
				if larkmsg.IsMentioned(event.Event.Message.Mentions) {
					larkmsg.ReplyCardText(ctx, err.Error(), *event.Event.Message.MessageId, "_OpErr", true)
					return
				}
			} else {
				text := fmt.Sprintf("%v\n[Jaeger Trace](%s)", err.Error(), utils.GenTraceURL(span.SpanContext().TraceID().String()))
				larkmsg.ReplyCardText(ctx, text, *event.Event.Message.MessageId, "_OpErr", true)
				logs.L().Ctx(ctx).Error("CommandOperator", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
				return
			}
		}
		if !meta.SkipDone {
			larkmsg.AddReactionAsync(ctx, "DONE", *event.Event.Message.MessageId)
		}
	}
	return
}
