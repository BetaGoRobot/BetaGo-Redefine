package messages

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/ops"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

const messagePolicyRestrictedExtraKey = "message_policy.chat_restricted"

var messagePolicyModerationPermission = func(ctx context.Context, chatID string) (string, error) {
	chat, err := larkchat.GetChatInfoCache(ctx, chatID)
	if err != nil || chat == nil || chat.ModerationPermission == nil {
		return "", err
	}
	return strings.TrimSpace(*chat.ModerationPermission), nil
}

func newMessageStageFilter() xhandler.StageFilterFunc[larkim.P2MessageReceiveV1, xhandler.BaseMetaData] {
	return func(ctx context.Context, stage xhandler.Stage[larkim.P2MessageReceiveV1, xhandler.BaseMetaData], event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) bool {
		return allowMessageStage(ctx, stage, event, meta)
	}
}

func allowMessageStage(ctx context.Context, stage xhandler.Stage[larkim.P2MessageReceiveV1, xhandler.BaseMetaData], event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) bool {
	switch stage.(type) {
	case *ops.RecordMsgOperator, *ops.ReactMsgOperator:
		return true
	}

	isCommand := command.LarkRootCommand.IsCommand(ctx, larkmsg.PreGetTextMsg(ctx, event).GetText())
	if isCommand && meta != nil {
		meta.SetIsCommand(true)
	}
	restricted := messagePolicyChatRestricted(ctx, event, meta)

	switch stage.(type) {
	case *ops.CommandOperator:
		return isCommand && !restricted
	case *ops.ReplyChatOperator:
		return !isCommand && !restricted && (messagePolicyIsP2P(event) || messagePolicyMentioned(event))
	case *ops.ChatMsgOperator, *ops.IntentRecognizeOperator:
		return !isCommand && !restricted && !messagePolicyIsP2P(event) && !messagePolicyMentioned(event)
	case *ops.RepeatMsgOperator, *ops.WordReplyMsgOperator:
		return !isCommand && !restricted
	default:
		return true
	}
}

func messagePolicyChatRestricted(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) bool {
	if messagePolicyIsP2P(event) {
		return false
	}
	if meta != nil {
		if cached, ok := meta.GetExtra(messagePolicyRestrictedExtraKey); ok {
			return cached == "true"
		}
	}
	chatID := messagePolicyChatID(event, meta)
	if chatID == "" {
		return false
	}
	permission, err := messagePolicyModerationPermission(ctx, chatID)
	if err != nil {
		logs.L().Ctx(ctx).Warn("get chat moderation permission for message policy failed", zap.String("chat_id", chatID), zap.Error(err))
		return false
	}
	restricted := permission != "" && permission != "all_members"
	if meta != nil {
		if restricted {
			meta.SetExtra(messagePolicyRestrictedExtraKey, "true")
		} else {
			meta.SetExtra(messagePolicyRestrictedExtraKey, "false")
		}
	}
	return restricted
}

func messagePolicyChatID(event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) string {
	if event != nil && event.Event != nil && event.Event.Message != nil && event.Event.Message.ChatId != nil {
		return *event.Event.Message.ChatId
	}
	if meta != nil {
		return meta.ChatID
	}
	return ""
}

func messagePolicyIsP2P(event *larkim.P2MessageReceiveV1) bool {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ChatType == nil {
		return false
	}
	return strings.TrimSpace(*event.Event.Message.ChatType) == "p2p"
}

func messagePolicyMentioned(event *larkim.P2MessageReceiveV1) bool {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return false
	}
	return larkmsg.IsMentioned(event.Event.Message.Mentions)
}
