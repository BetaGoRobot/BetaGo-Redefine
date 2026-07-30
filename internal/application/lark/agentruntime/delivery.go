package agentruntime

import (
	"context"
	"errors"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ReplyRequest struct {
	Version          int    `json:"version"`
	StepID           string `json:"step_id"`
	RunID            string `json:"run_id"`
	Text             string `json:"text"`
	TriggerMessageID string `json:"trigger_message_id"`
	ChatID           string `json:"chat_id"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type ReplyDeliverer interface {
	Deliver(context.Context, ReplyRequest) (string, error)
}

type textMessenger interface {
	ReplyText(context.Context, string, string, string) (string, error)
	CreateText(context.Context, string, string, string) (string, error)
}

type larkReplyDeliverer struct{ messenger textMessenger }

func NewLarkReplyDeliverer() ReplyDeliverer {
	return newLarkReplyDeliverer(larkTextMessenger{})
}

func newLarkReplyDeliverer(messenger textMessenger) *larkReplyDeliverer {
	return &larkReplyDeliverer{messenger: messenger}
}

func (d *larkReplyDeliverer) Deliver(ctx context.Context, req ReplyRequest) (string, error) {
	if d == nil || d.messenger == nil || strings.TrimSpace(req.Text) == "" ||
		strings.TrimSpace(req.IdempotencyKey) == "" {
		return "", errors.New("invalid reply delivery request")
	}
	if req.TriggerMessageID != "" {
		return d.messenger.ReplyText(ctx, req.TriggerMessageID, req.Text, req.IdempotencyKey)
	}
	if req.ChatID == "" {
		return "", errors.New("reply delivery chat id is required")
	}
	return d.messenger.CreateText(ctx, req.ChatID, req.Text, req.IdempotencyKey)
}

type larkTextMessenger struct{}

func (larkTextMessenger) ReplyText(
	ctx context.Context,
	messageID string,
	text string,
	key string,
) (string, error) {
	resp, err := larkmsg.ReplyMsgText(ctx, text, messageID, "_agent_continuation_"+key, false)
	if err != nil {
		return "", err
	}
	if resp == nil || !resp.Success() || resp.Data == nil || resp.Data.MessageId == nil {
		return "", errors.New("lark reply did not return a message id")
	}
	return *resp.Data.MessageId, nil
}

func (larkTextMessenger) CreateText(
	ctx context.Context,
	chatID string,
	text string,
	key string,
) (string, error) {
	content := larkmsg.NewTextMsgBuilder().Text(text).Build()
	resp, err := larkmsg.CreateMsgRawContentType(
		ctx, chatID, larkim.MsgTypeText, content, key, "_agent_continuation",
	)
	if err != nil {
		return "", err
	}
	if resp == nil || !resp.Success() || resp.Data == nil || resp.Data.MessageId == nil {
		return "", errors.New("lark create did not return a message id")
	}
	return *resp.Data.MessageId, nil
}
