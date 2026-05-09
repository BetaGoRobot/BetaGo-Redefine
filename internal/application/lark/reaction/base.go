package reaction

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Handler 反应处理器模板（保留用于向后兼容）
var Handler *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]

type (
	OpBase = xhandler.OperatorBase[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
	Op     = xhandler.Operator[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
)

func NewReactionProcessor() *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData] {
	return (&xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]{}).
		OnPanic(func(ctx context.Context, err error, event *larkim.P2MessageReactionCreatedV1, metaData *xhandler.BaseMetaData) {
			larkmsg.SendRecoveredMsg(ctx, err, *event.Event.MessageId)
		}).
		WithMetaDataProcess(metaInit).
		AddAsync(&FollowReactionOperator{}).
		AddAsync(&RecordReactionOperator{})
}

func metaInit(event *larkim.P2MessageReactionCreatedV1) *xhandler.BaseMetaData {
	chatName := "unknown"
	if event.Event != nil && event.Event.MessageId != nil {
		chatName = ResolveChatNameForReaction(context.Background(), *event.Event.MessageId)
	}
	return &xhandler.BaseMetaData{
		OpenID:   botidentity.ReactionOpenID(event),
		ChatName: chatName,
	}
}

// ResolveChatNameForReaction resolves the chat name from a message ID for reaction events.
func ResolveChatNameForReaction(ctx context.Context, msgID string) string {
	chatID, err := larkmsg.GetChatIDFromMsgID(ctx, msgID)
	if err != nil || chatID == "" {
		return "unknown"
	}
	if name := larkchat.GetChatName(ctx, chatID); name != "" {
		return name
	}
	return "unknown"
}

func init() {
	Handler = NewReactionProcessor()
}
