package ops

import (
	"context"
	stderrors "errors"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	pkgerrors "github.com/pkg/errors"
	"go.uber.org/zap"
)

type (
	OpBase = xhandler.OperatorBase[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
	Op     = xhandler.Operator[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
)

func messageText(ctx context.Context, event *larkim.P2MessageReceiveV1) string {
	return larkmsg.PreGetTextMsg(ctx, event).GetText()
}

func messageChatID(event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) string {
	if event != nil && event.Event != nil && event.Event.Message != nil && event.Event.Message.ChatId != nil {
		return *event.Event.Message.ChatId
	}
	if meta != nil {
		return meta.ChatID
	}
	return ""
}

func messageOpenID(event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) string {
	if openID := botidentity.MessageSenderOpenID(event); openID != "" {
		return openID
	}
	if meta != nil {
		return meta.OpenID
	}
	return ""
}

func messageConfigAccessor(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) *appconfig.Accessor {
	return appconfig.NewAccessor(ctx, messageChatID(event, meta), messageOpenID(event, meta))
}

func isCommandMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) bool {
	return command.LarkRootCommand.IsCommand(ctx, messageText(ctx, event))
}

func skipStage(opName, reason string) error {
	return pkgerrors.Wrap(xerror.ErrStageSkip, opName+" "+reason)
}

func skipIfCommand(ctx context.Context, opName string, event *larkim.P2MessageReceiveV1) error {
	if isCommandMessage(ctx, event) {
		return skipStage(opName, "is command")
	}
	return nil
}

func requireCommand(ctx context.Context, opName string, event *larkim.P2MessageReceiveV1) error {
	if !isCommandMessage(ctx, event) {
		return skipStage(opName, "is not command")
	}
	return nil
}

func skipIfMentioned(opName string, event *larkim.P2MessageReceiveV1) error {
	if larkmsg.IsMentioned(event.Event.Message.Mentions) {
		return skipStage(opName, "is mentioned")
	}
	return nil
}

func requireMentionOrP2P(opName string, event *larkim.P2MessageReceiveV1) error {
	if *event.Event.Message.ChatType != "p2p" && !larkmsg.IsMentioned(event.Event.Message.Mentions) {
		return skipStage(opName, "requires mention or p2p")
	}
	return nil
}

func withProgressReaction(ctx context.Context, msgID string) func() {
	reactionID, err := larkmsg.AddReaction(ctx, "OnIt", msgID)
	if err != nil {
		logs.L().Ctx(ctx).Error("Add reaction to msg failed", zap.Error(err))
		return func() {}
	}
	return func() {
		larkmsg.RemoveReactionAsync(ctx, reactionID, msgID)
	}
}

func addDoneReactionIfNeeded(ctx context.Context, msgID string, meta *xhandler.BaseMetaData) {
	if meta != nil && meta.ShouldSkipDone() {
		return
	}
	larkmsg.AddReactionAsync(ctx, "DONE", msgID)
}

func buildMediaReply(msgType, fileID string) (string, error) {
	var payload map[string]string
	switch msgType {
	case larkim.MsgTypeImage:
		payload = map[string]string{"image_key": fileID}
	case larkim.MsgTypeSticker:
		payload = map[string]string{"file_key": fileID}
	default:
		return "", stderrors.New("unsupported media message type")
	}
	return sonic.MarshalString(payload)
}

func replyMediaMessage(ctx context.Context, msgID, msgType, fileID, suffix string) error {
	content, err := buildMediaReply(msgType, fileID)
	if err != nil {
		return err
	}
	_, err = larkmsg.ReplyMsgRawContentType(ctx, msgID, msgType, content, suffix, false)
	return err
}

func replyTypedMessage(ctx context.Context, msgID string, reply *xmodel.ReplyNType, suffix string) error {
	switch reply.ReplyType {
	case xmodel.ReplyTypeText:
		_, err := larkmsg.ReplyMsgText(ctx, reply.Reply, msgID, suffix, false)
		return err
	case xmodel.ReplyTypeImg:
		msgType := larkim.MsgTypeSticker
		if strings.HasPrefix(reply.Reply, "img") {
			msgType = larkim.MsgTypeImage
		}
		return replyMediaMessage(ctx, msgID, msgType, reply.Reply, suffix)
	default:
		return stderrors.New("unknown reply type")
	}
}
