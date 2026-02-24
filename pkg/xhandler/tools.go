package xhandler

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
)

func NewBaseMetaDataWithChatIDUID(ctx context.Context, chatID, userID string) *BaseMetaData {
	chat, err := larkchat.GetChatInfoCache(ctx, chatID)
	if err != nil {
		return &BaseMetaData{
			ChatID: chatID,
			UserID: userID,
		}
	}
	isP2P := *chat.ChatMode == "p2p"
	return &BaseMetaData{
		ChatID: chatID,
		UserID: userID,

		IsP2P: isP2P,
	}
}
