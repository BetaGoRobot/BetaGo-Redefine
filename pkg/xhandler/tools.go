package xhandler

import (
	"context"
)

func NewBaseMetaDataWithChatIDUID(ctx context.Context, chatID, userID string) *BaseMetaData {
	// chat, err := chatutil.GetChatInfoCache(ctx, chatID)
	// if err != nil {
	// 	return &BaseMetaData{
	// 		ChatID: chatID,
	// 		UserID: userID,
	// 	}
	// }
	// isP2P := *chat.ChatMode == "p2p"
	// return &BaseMetaData{
	// 	ChatID: chatID,
	// 	UserID: userID,

	// 	IsP2P: isP2P,
	// }
	return nil
}
