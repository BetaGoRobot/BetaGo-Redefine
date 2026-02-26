package larkmsg

import (
	"context"
	"errors"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func ReplyMsgRawContentType(ctx context.Context, msgID, msgType, content, suffix string, replyInThread bool) (resp *larkim.ReplyMessageResp, err error) {
	_, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("msgID").String(msgID), attribute.Key("msgType").String(msgType), attribute.Key("content").String(content))
	defer span.End()
	defer func() { span.RecordError(err) }()
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

	resp, err = lark_dal.Client().Im.V1.Message.Reply(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.Error(err))
		return nil, err
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.String("Error", larkcore.Prettify(resp.CodeError.Err)))
		return nil, errors.New(resp.Error())
	}
	go RecordReplyMessage2Opensearch(ctx, resp, content)
	return
}

// ReplyMsgText ReplyMsgText 注意：不要传入已经Build过的文本
//
//	@param ctx
//	@param text
//	@param msgID
func ReplyMsgText(ctx context.Context, text, msgID, suffix string, replyInThread bool) (resp *larkim.ReplyMessageResp, err error) {
	_, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("msgID").String(msgID), attribute.Key("content").String(text))
	defer span.End()
	defer func() { span.RecordError(err) }()
	return ReplyMsgRawAsText(ctx, msgID, larkim.MsgTypeText, text, suffix, replyInThread)
}

func ReplyMsgRawAsText(ctx context.Context, msgID, msgType, content, suffix string, replyInThread bool) (resp *larkim.ReplyMessageResp, err error) {
	_, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("msgID").String(msgID), attribute.Key("msgType").String(msgType), attribute.Key("content").String(content))
	defer span.End()
	defer func() { span.RecordError(err) }()
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

	resp, err = lark_dal.Client().Im.V1.Message.Reply(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.Error(err))
		return nil, err
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.String("Error", larkcore.Prettify(resp.CodeError.Err)))
		return nil, errors.New(resp.Error())
	}
	go RecordReplyMessage2Opensearch(ctx, resp, content)
	return
}

// ReplyCard  注意：不要传入已经Build过的文本
//
//	@param ctx
//	@param text
//	@param msgID
func ReplyCard(ctx context.Context, cardContent *larktpl.TemplateCardContent, msgID, suffix string, replyInThread bool) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	// 先把卡片发送了，再记录日志和指标，避免指标记录的耗时过程拖慢整个请求
	resp, err := doSendCard(ctx, msgID, suffix, cardContent, replyInThread)
	if err != nil {
		logs.L().Ctx(ctx).Error("doSendCard failed", zap.Error(err))
		return
	}

	span.SetAttributes(attribute.Key("msgID").String(msgID))
	for k, v := range cardContent.Data.TemplateVariable {
		span.SetAttributes(attribute.Key(k).String(fmt.Sprintf("%v", v)))
	}
	logs.L().Ctx(ctx).Info(
		"reply card",
		zap.String("msgID", msgID),
		zap.String("suffix", suffix),
		zap.Bool("replyInThread", replyInThread),
		zap.String("cardContent", cardContent.String()),
	)
	go RecordReplyMessage2Opensearch(ctx, resp, cardContent.GetVariables()...)
	return
}

func doSendCard(ctx context.Context, msgID, suffix string, cardContent *larktpl.TemplateCardContent, replyInThread bool) (resp *larkim.ReplyMessageResp, err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	resp, err = lark_dal.Client().Im.V1.Message.Reply(
		ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(msgID).
			Body(
				larkim.NewReplyMessageReqBodyBuilder().
					MsgType(larkim.MsgTypeInteractive).
					Content(cardContent.String()).
					// Uuid(utils.GenUUIDStr(msgID+suffix, 50)).
					ReplyInThread(replyInThread).
					Build(),
			).
			Build(),
	)
	if err != nil {
		return
	}
	if !resp.Success() {
		return resp, errors.New(resp.Error())
	}
	return
}
