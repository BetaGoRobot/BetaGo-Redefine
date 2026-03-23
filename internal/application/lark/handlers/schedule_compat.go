package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var (
	scheduleCompatReplyText = func(ctx context.Context, text, msgID, suffix string, replyInThread bool) (string, error) {
		resp, err := larkmsg.ReplyMsgText(ctx, text, msgID, suffix, replyInThread)
		if err != nil {
			return "", err
		}
		if resp != nil && resp.Data != nil && resp.Data.MessageId != nil && *resp.Data.MessageId != "" {
			return *resp.Data.MessageId, nil
		}
		return "", errors.New("reply text succeeded but message_id is empty")
	}
	scheduleCompatCreateText = func(ctx context.Context, text, msgID, chatID string) (string, error) {
		resp, err := larkmsg.CreateMsgRawContentType(ctx, chatID, larkim.MsgTypeText, larkmsg.NewTextMsgBuilder().Text(text).Build(), msgID, "_schedule_compat_text")
		if err != nil {
			return "", err
		}
		return createdMessageID(resp)
	}
	scheduleCompatReplyCardWithMessageID = func(ctx context.Context, msgID string, cardContent *larktpl.TemplateCardContent, suffix string, replyInThread bool) (string, error) {
		resp, err := larkmsg.ReplyCardWithResp(ctx, cardContent, msgID, suffix, replyInThread)
		if err != nil {
			return "", err
		}
		if resp != nil && resp.Data != nil && resp.Data.MessageId != nil && *resp.Data.MessageId != "" {
			return *resp.Data.MessageId, nil
		}
		return "", errors.New("reply card succeeded but message_id is empty")
	}
	scheduleCompatCreateCardWithMessageID = func(ctx context.Context, chatID string, cardContent *larktpl.TemplateCardContent) (string, error) {
		resp, err := larkmsg.CreateMsgCardWithResp(ctx, cardContent, chatID)
		if err != nil {
			return "", err
		}
		if resp != nil && resp.Data != nil && resp.Data.MessageId != nil && *resp.Data.MessageId != "" {
			return *resp.Data.MessageId, nil
		}
		return "", errors.New("create card succeeded but message_id is empty")
	}
	scheduleCompatReplyCardJSON = func(ctx context.Context, msgID string, cardData any, suffix string, replyInThread bool) (string, error) {
		content, err := larkmsg.BuildCardEntityContent(ctx, cardData)
		if err != nil {
			return "", err
		}
		resp, err := larkmsg.ReplyMsgRawContentType(ctx, msgID, larkim.MsgTypeInteractive, content, suffix, replyInThread)
		if err != nil {
			return "", err
		}
		if resp != nil && resp.Data != nil && resp.Data.MessageId != nil && *resp.Data.MessageId != "" {
			return *resp.Data.MessageId, nil
		}
		return "", errors.New("reply card json succeeded but message_id is empty")
	}
	scheduleCompatCreateCardJSON = func(ctx context.Context, chatID string, cardData any, msgID, suffix string) (string, error) {
		content, err := larkmsg.BuildCardEntityContent(ctx, cardData)
		if err != nil {
			return "", err
		}
		resp, err := larkmsg.CreateMsgRawContentType(ctx, chatID, larkim.MsgTypeInteractive, content, msgID, suffix)
		if err != nil {
			return "", err
		}
		return createdMessageID(resp)
	}
	scheduleCompatCreateRawCard = func(ctx context.Context, chatID, content, msgID, suffix string) (string, error) {
		resp, err := larkmsg.CreateMsgRawContentType(ctx, chatID, larkim.MsgTypeInteractive, content, msgID, suffix)
		if err != nil {
			return "", err
		}
		return createdMessageID(resp)
	}
)

func createdMessageID(resp *larkim.CreateMessageResp) (string, error) {
	if resp != nil && resp.Data != nil && resp.Data.MessageId != nil && *resp.Data.MessageId != "" {
		return *resp.Data.MessageId, nil
	}
	return "", errors.New("create message succeeded but message_id is empty")
}

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

func recordCompatibleReply(ctx context.Context, metaData *xhandler.BaseMetaData, messageID, kind string) {
	if metaData != nil && messageID != "" {
		metaData.SetLastReplyRef(messageID, kind)
	}
	runtimecontext.RecordCompatibleReplyRef(ctx, messageID, kind)
}

func sendCompatibleText(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, text, suffix string, replyInThread bool) error {
	if runtimecontext.ShouldSuppressCompatibleOutput(ctx) {
		return nil
	}
	msgID := currentMessageID(data)
	if msgID != "" {
		if replyMsgID, err := scheduleCompatReplyText(ctx, text, msgID, suffix, replyInThread); err == nil {
			recordCompatibleReply(ctx, metaData, replyMsgID, "text")
			return nil
		}
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	msgID = fmt.Sprintf("schedule-compat-%d", time.Now().UnixNano())
	replyMsgID, err := scheduleCompatCreateText(ctx, text, msgID, chatID)
	if err != nil {
		return err
	}
	recordCompatibleReply(ctx, metaData, replyMsgID, "text")
	return nil
}

func sendCompatibleCard(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, cardContent *larktpl.TemplateCardContent, suffix string, replyInThread bool) error {
	if runtimecontext.ShouldSuppressCompatibleOutput(ctx) {
		return nil
	}
	_, err := sendCompatibleCardWithMessageID(ctx, data, metaData, cardContent, suffix, replyInThread)
	return err
}

func sendCompatibleCardWithMessageID(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, cardContent *larktpl.TemplateCardContent, suffix string, replyInThread bool) (string, error) {
	if runtimecontext.ShouldSuppressCompatibleOutput(ctx) {
		return "", nil
	}
	replyInThread = compatibleCardReplyInThread(metaData, replyInThread)
	msgID := currentMessageID(data)
	if msgID != "" {
		if metaData != nil && metaData.Refresh {
			if err := larkmsg.PatchCard(ctx, cardContent, msgID); err != nil {
				return msgID, err
			}
			recordCompatibleReply(ctx, metaData, msgID, "card")
			return msgID, nil
		}
		if replyMsgID, err := scheduleCompatReplyCardWithMessageID(ctx, msgID, cardContent, suffix, replyInThread); err == nil {
			recordCompatibleReply(ctx, metaData, replyMsgID, "card")
			return replyMsgID, nil
		}
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return "", errors.New("chat_id is required")
	}
	replyMsgID, err := scheduleCompatCreateCardWithMessageID(ctx, chatID, cardContent)
	if err != nil {
		return "", err
	}
	recordCompatibleReply(ctx, metaData, replyMsgID, "card")
	return replyMsgID, nil
}

func sendCompatibleRawCard(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, content, suffix string, replyInThread bool) error {
	if runtimecontext.ShouldSuppressCompatibleOutput(ctx) {
		return nil
	}
	replyInThread = compatibleCardReplyInThread(metaData, replyInThread)
	msgID := currentMessageID(data)
	if msgID != "" {
		if metaData != nil && metaData.Refresh {
			if err := larkmsg.PatchRawCard(ctx, msgID, content); err != nil {
				return err
			}
			recordCompatibleReply(ctx, metaData, msgID, "raw_card")
			return nil
		}
		if resp, err := larkmsg.ReplyMsgRawContentType(ctx, msgID, larkim.MsgTypeInteractive, content, suffix, replyInThread); err == nil {
			replyMsgID := msgID
			if resp != nil && resp.Data != nil && resp.Data.MessageId != nil && *resp.Data.MessageId != "" {
				replyMsgID = *resp.Data.MessageId
			}
			recordCompatibleReply(ctx, metaData, replyMsgID, "raw_card")
			return nil
		}
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	msgID = fmt.Sprintf("schedule-compat-card-%d", time.Now().UnixNano())
	replyMsgID, err := scheduleCompatCreateRawCard(ctx, chatID, content, msgID, suffix)
	if err != nil {
		return err
	}
	recordCompatibleReply(ctx, metaData, replyMsgID, "raw_card")
	return nil
}

func sendCompatibleCardJSON(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, cardData any, suffix string, replyInThread bool) error {
	if runtimecontext.ShouldSuppressCompatibleOutput(ctx) {
		return nil
	}
	replyInThread = compatibleCardReplyInThread(metaData, replyInThread)
	msgID := currentMessageID(data)
	if msgID != "" {
		if metaData != nil && metaData.Refresh {
			if err := larkmsg.PatchCardJSON(ctx, msgID, cardData); err != nil {
				return err
			}
			recordCompatibleReply(ctx, metaData, msgID, "card_json")
			return nil
		}
		if replyMsgID, err := scheduleCompatReplyCardJSON(ctx, msgID, cardData, suffix, replyInThread); err == nil {
			recordCompatibleReply(ctx, metaData, replyMsgID, "card_json")
			return nil
		}
	}

	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	msgID = fmt.Sprintf("schedule-compat-cardjson-%d", time.Now().UnixNano())
	replyMsgID, err := scheduleCompatCreateCardJSON(ctx, chatID, cardData, msgID, suffix)
	if err != nil {
		return err
	}
	recordCompatibleReply(ctx, metaData, replyMsgID, "card_json")
	return nil
}

func compatibleCardReplyInThread(metaData *xhandler.BaseMetaData, replyInThread bool) bool {
	if metaData == nil || metaData.IsP2P {
		return false
	}
	if mode, ok := metaData.IntentInteractionMode(); ok && mode == intent.InteractionModeAgentic {
		return true
	}
	return replyInThread
}
