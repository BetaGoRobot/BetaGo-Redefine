package ops

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/consts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
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
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	return requireCommand(ctx, r.Name(), event)
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *CommandOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(event), 256)...)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	rawCommand := messageText(ctx, event)

	return ExecuteFromRawCommand(ctx, event, meta, rawCommand)
}

func ExecuteFromRawCommand(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, rawCommand string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(event), 256)...)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	rawCommand = strings.ReplaceAll(rawCommand, "<b>", " ")
	rawCommand = strings.ReplaceAll(rawCommand, "</b>", " ")
	ctx = context.WithValue(ctx, consts.ContextVarSrcCmd, rawCommand)
	commands := xcommand.GetCommand(ctx, rawCommand)
	if len(commands) > 0 {
		meta.SetIsCommand(true)
		meta.SetMainCommand(commands[0])
		defer withProgressReaction(ctx, *event.Event.Message.MessageId)()
		err = command.LarkRootCommand.Execute(ctx, event, meta, commands)
		if err != nil {
			otel.RecordError(span, err)
			if errors.Is(err, xerror.ErrCommandNotFound) {
				meta.SetIsCommand(false)
				meta.SetMainCommand("")
				if larkmsg.IsMentioned(event.Event.Message.Mentions) {
					larkmsg.ReplyCardText(ctx, err.Error(), *event.Event.Message.MessageId, "_OpErr", true)
					return
				}
			} else {
				larkmsg.ReplyCardText(ctx, err.Error(), *event.Event.Message.MessageId, "_OpErr", true)
				logs.L().Ctx(ctx).Error("CommandOperator", zap.Error(err))
				return
			}
		}
		addDoneReactionIfNeeded(ctx, *event.Event.Message.MessageId, meta)
	}
	return
}
