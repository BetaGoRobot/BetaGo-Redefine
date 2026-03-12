package larkmsg

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func ReplyMsgRawContentType(ctx context.Context, msgID, msgType, content, suffix string, replyInThread bool) (resp *larkim.ReplyMessageResp, err error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("message.id", msgID), attribute.String("message.type", msgType))
	span.SetAttributes(otel.PreviewAttrs("message.content", content, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	uuid := (msgID + suffix)
	if len(uuid) > 50 {
		uuid = uuid[:50]
	}

	req := larkim.NewReplyMessageReqBuilder().Body(
		larkim.NewReplyMessageReqBodyBuilder().
			MsgType(msgType).
			Content(content).
			ReplyInThread(replyInThread).
			Uuid(utils.GenUUIDStr(uuid, 50)).Build(),
	).MessageId(msgID).Build()

	return sendReplyMessage(ctx, req, content)
}

// ReplyMsgText ReplyMsgText 注意：不要传入已经Build过的文本
//
//	@param ctx
//	@param text
//	@param msgID
func ReplyMsgText(ctx context.Context, text, msgID, suffix string, replyInThread bool) (resp *larkim.ReplyMessageResp, err error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("message.id", msgID))
	span.SetAttributes(otel.PreviewAttrs("message.text", text, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	return ReplyMsgRawAsText(ctx, msgID, larkim.MsgTypeText, text, suffix, replyInThread)
}

func ReplyMsgRawAsText(ctx context.Context, msgID, msgType, content, suffix string, replyInThread bool) (resp *larkim.ReplyMessageResp, err error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("message.id", msgID), attribute.String("message.type", msgType))
	span.SetAttributes(otel.PreviewAttrs("message.content", content, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	uuid := (msgID + suffix)
	if len(uuid) > 50 {
		uuid = uuid[:50]
	}

	req := larkim.NewReplyMessageReqBuilder().Body(
		larkim.NewReplyMessageReqBodyBuilder().
			MsgType(msgType).
			Content(NewTextMsgBuilder().Text(content).Build()).
			ReplyInThread(replyInThread).
			Uuid(utils.GenUUIDStr(uuid, 50)).Build(),
	).MessageId(msgID).Build()

	return sendReplyMessage(ctx, req, content)
}

// ReplyCard  注意：不要传入已经Build过的文本
//
//	@param ctx
//	@param text
//	@param msgID
func ReplyCard(ctx context.Context, cardContent *larktpl.TemplateCardContent, msgID, suffix string, replyInThread bool) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("message.id", msgID),
		attribute.Int("card.variable.count", len(cardContent.Data.TemplateVariable)),
	)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	uuid := msgID + suffix
	if len(uuid) > 50 {
		uuid = uuid[:50]
	}

	req := larkim.NewReplyMessageReqBuilder().
		MessageId(msgID).
		Body(
			larkim.NewReplyMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeInteractive).
				Content(cardContent.String()).
				Uuid(utils.GenUUIDStr(uuid, 50)).
				ReplyInThread(replyInThread).
				Build(),
		).
		Build()

	_, err = sendReplyMessage(ctx, req, cardContent.GetVariables()...)
	if err != nil {
		logs.L().Ctx(ctx).Error("ReplyCard failed", zap.Error(err))
		return
	}

	span.SetAttributes(otel.PreviewAttrs("card.content", cardContent.String(), 256)...)
	logs.L().Ctx(ctx).Info(
		"reply card",
		zap.String("msgID", msgID),
		zap.String("suffix", suffix),
		zap.Bool("replyInThread", replyInThread),
		zap.String("cardContent", cardContent.String()),
	)
	return
}
