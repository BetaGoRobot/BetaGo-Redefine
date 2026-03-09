package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func currentChatID(data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) string {
	if data != nil && data.Event.Message.ChatId != nil {
		return *data.Event.Message.ChatId
	}
	if metaData != nil {
		return metaData.ChatID
	}
	return ""
}

func currentUserID(data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) string {
	if data != nil {
		if data.Event.Sender.SenderId.OpenId != nil {
			return *data.Event.Sender.SenderId.OpenId
		}
		if data.Event.Sender.SenderId.UserId != nil {
			return *data.Event.Sender.SenderId.UserId
		}
	}
	if metaData != nil {
		return metaData.UserID
	}
	return ""
}

func currentMessageID(data *larkim.P2MessageReceiveV1) string {
	if data != nil && data.Event.Message.MessageId != nil {
		return *data.Event.Message.MessageId
	}
	return ""
}

func sendCompatibleText(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, text, suffix string, replyInThread bool) error {
	msgID := currentMessageID(data)
	if msgID != "" {
		_, err := larkmsg.ReplyMsgText(ctx, text, msgID, suffix, replyInThread)
		return err
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	msgID = fmt.Sprintf("schedule-compat-%d", time.Now().UnixNano())
	return larkmsg.CreateMsgTextRaw(ctx, larkmsg.NewTextMsgBuilder().Text(text).Build(), msgID, chatID)
}

func sendCompatibleCard(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, cardContent *larktpl.TemplateCardContent, suffix string, replyInThread bool) error {
	msgID := currentMessageID(data)
	if msgID != "" {
		if metaData != nil && metaData.Refresh {
			return larkmsg.PatchCard(ctx, cardContent, msgID)
		}
		return larkmsg.ReplyCard(ctx, cardContent, msgID, suffix, replyInThread)
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	return larkmsg.CreateMsgCard(ctx, cardContent, chatID)
}
