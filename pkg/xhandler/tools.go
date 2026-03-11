package xhandler

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
)

func NewBaseMetaDataWithChatIDOpenID(ctx context.Context, chatID, openID string) *BaseMetaData {
	chat, err := larkchat.GetChatInfoCache(ctx, chatID)
	if err != nil {
		return &BaseMetaData{
			ChatID: chatID,
			OpenID: openID,
		}
	}
	isP2P := *chat.ChatMode == "p2p"
	return &BaseMetaData{
		ChatID: chatID,
		OpenID: openID,

		IsP2P: isP2P,
	}
}
