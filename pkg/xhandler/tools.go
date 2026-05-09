package xhandler

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
)

func NewBaseMetaDataWithChatIDOpenID(ctx context.Context, chatID, openID string) *BaseMetaData {
	chat, err := larkchat.GetChatInfoCache(ctx, chatID)
	if err != nil {
		return &BaseMetaData{
			ChatID:   chatID,
			OpenID:   openID,
			ChatName: "unknown",
		}
	}
	isP2P := *chat.ChatMode == "p2p"
	chatName := "unknown"
	if isP2P {
		chatName = "p2p"
	} else if chat.Name != nil && *chat.Name != "" {
		chatName = *chat.Name
	}
	return &BaseMetaData{
		ChatID:   chatID,
		OpenID:   openID,
		IsP2P:    isP2P,
		ChatName: chatName,
	}
}
