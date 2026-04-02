package ops

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/consts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
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

type CommandOperator struct {
	OpBase
}

func (r *CommandOperator) Name() string {
	return "CommandOperator"
}

func (r *CommandOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	return requireCommand(ctx, r.Name(), event)
}

func (r *CommandOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(event), 256)...)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	return ExecuteFromRawCommand(ctx, event, meta, messageText(ctx, event))
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
	if len(commands) == 0 {
		return nil
	}

	meta.SetIsCommand(true)
	meta.SetMainCommand(commands[0])
	defer withProgressReaction(ctx, *event.Event.Message.MessageId)()

	err = command.LarkRootCommand.Execute(ctx, event, meta, commands)
	if err != nil {
		otel.RecordError(span, err)
		return handleCommandError(ctx, event, meta, rawCommand, err)
	}
	addDoneReactionIfNeeded(ctx, *event.Event.Message.MessageId, meta)
	return nil
}

func handleCommandError(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, rawCommand string, err error) error {
	if handled := trySendCommandForm(ctx, event, meta, rawCommand, err); handled {
		return nil
	}
	if errors.Is(err, xerror.ErrCommandNotFound) {
		meta.SetIsCommand(false)
		meta.SetMainCommand("")
		if larkmsg.IsMentioned(event.Event.Message.Mentions) {
			sendCommandErrorCard(ctx, event, meta, err.Error())
			return nil
		}
		return nil
	}
	sendCommandErrorCard(ctx, event, meta, err.Error())
	logs.L().Ctx(ctx).Error("command execution failed", zap.Error(err))
	return nil
}

func trySendCommandForm(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, rawCommand string, err error) bool {
	if !shouldReplyCommandForm(err) || !command.CanBuildCommandForm(command.LarkRootCommand, rawCommand) {
		return false
	}
	cardData, buildErr := command.BuildCommandFormCardJSON(command.LarkRootCommand, rawCommand)
	if buildErr != nil {
		logs.L().Ctx(ctx).Warn("build command form failed", zap.Error(buildErr), zap.String("raw_command", rawCommand))
		return false
	}
	msgID := currentMessageID(event)
	if msgID == "" {
		return false
	}
	if meta != nil && meta.Refresh {
		if patchErr := larkmsg.PatchCardJSON(ctx, msgID, cardData); patchErr == nil {
			return true
		}
	}
	if replyErr := larkmsg.ReplyCardJSON(ctx, msgID, cardData, "_cmd_form", true); replyErr != nil {
		logs.L().Ctx(ctx).Warn("reply command form failed", zap.Error(replyErr), zap.String("raw_command", rawCommand))
		return false
	}
	return true
}

func shouldReplyCommandForm(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, xerror.ErrArgsIncompelete) || errors.Is(err, xerror.ErrCommandIncomplete) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(err.Error())), "usage:")
}

func sendCommandErrorCard(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, message string) {
	msgID := currentMessageID(event)
	if msgID == "" {
		return
	}
	if meta != nil && meta.Refresh {
		cardContent := larkcard.NewCardBuildHelper().
			SetTitle("命令执行失败").
			SetContent(message).
			Build(ctx)
		if err := larkmsg.PatchCard(ctx, cardContent, msgID); err == nil {
			return
		}
	}
	_ = larkmsg.ReplyCardText(ctx, message, msgID, "_OpErr", true)
}

func currentMessageID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.MessageId == nil {
		return ""
	}
	return *event.Event.Message.MessageId
}
