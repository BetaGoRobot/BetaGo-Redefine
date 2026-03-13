package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func currentChatID(data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) string {
	if data != nil && data.Event != nil && data.Event.Message != nil && data.Event.Message.ChatId != nil {
		return *data.Event.Message.ChatId
	}
	if metaData != nil {
		return metaData.ChatID
	}
	return ""
}

func currentOpenID(data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) string {
	if openID := botidentity.MessageSenderOpenID(data); openID != "" {
		return openID
	}
	if metaData != nil {
		return metaData.OpenID
	}
	return ""
}

func currentMessageID(data *larkim.P2MessageReceiveV1) string {
	if data != nil && data.Event != nil && data.Event.Message != nil && data.Event.Message.MessageId != nil {
		return *data.Event.Message.MessageId
	}
	return ""
}

func sendCompatibleText(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, text, suffix string, replyInThread bool) error {
	msgID := currentMessageID(data)
	if msgID != "" {
		if _, err := larkmsg.ReplyMsgText(ctx, text, msgID, suffix, replyInThread); err == nil {
			return nil
		}
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	msgID = fmt.Sprintf("schedule-compat-%d", time.Now().UnixNano())
	return larkmsg.CreateMsgTextRaw(ctx, larkmsg.NewTextMsgBuilder().Text(text).Build(), msgID, chatID)
}

func sendCompatibleCard(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, cardContent *larktpl.TemplateCardContent, suffix string, replyInThread bool) error {
	_, err := sendCompatibleCardWithMessageID(ctx, data, metaData, cardContent, suffix, replyInThread)
	return err
}

func sendCompatibleCardWithMessageID(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, cardContent *larktpl.TemplateCardContent, suffix string, replyInThread bool) (string, error) {
	msgID := currentMessageID(data)
	if msgID != "" {
		if metaData != nil && metaData.Refresh {
			return msgID, larkmsg.PatchCard(ctx, cardContent, msgID)
		}
		if resp, err := larkmsg.ReplyCardWithResp(ctx, cardContent, msgID, suffix, replyInThread); err == nil {
			if resp != nil && resp.Data != nil && resp.Data.MessageId != nil && *resp.Data.MessageId != "" {
				return *resp.Data.MessageId, nil
			}
			return "", errors.New("reply card succeeded but message_id is empty")
		}
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return "", errors.New("chat_id is required")
	}
	resp, err := larkmsg.CreateMsgCardWithResp(ctx, cardContent, chatID)
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Data == nil || resp.Data.MessageId == nil || *resp.Data.MessageId == "" {
		return "", errors.New("create card succeeded but message_id is empty")
	}
	return *resp.Data.MessageId, nil
}

func sendCompatibleRawCard(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, content, suffix string, replyInThread bool) error {
	msgID := currentMessageID(data)
	if msgID != "" {
		if metaData != nil && metaData.Refresh {
			return larkmsg.PatchRawCard(ctx, msgID, content)
		}
		if err := larkmsg.ReplyRawCard(ctx, msgID, content, suffix, replyInThread); err == nil {
			return nil
		}
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	msgID = fmt.Sprintf("schedule-compat-card-%d", time.Now().UnixNano())
	return larkmsg.CreateRawCard(ctx, chatID, content, msgID, suffix)
}

func sendCompatibleCardJSON(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, cardData any, suffix string, replyInThread bool) error {
	msgID := currentMessageID(data)
	if msgID != "" {
		if metaData != nil && metaData.Refresh {
			return larkmsg.PatchCardJSON(ctx, msgID, cardData)
		}
		if err := larkmsg.ReplyCardJSON(ctx, msgID, cardData, suffix, replyInThread); err == nil {
			return nil
		}
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	msgID = fmt.Sprintf("schedule-compat-cardjson-%d", time.Now().UnixNano())
	return larkmsg.CreateCardJSON(ctx, chatID, cardData, msgID, suffix)
}
