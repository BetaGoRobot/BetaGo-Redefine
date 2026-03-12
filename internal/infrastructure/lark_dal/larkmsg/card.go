package larkmsg

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func ReplyCardText(ctx context.Context, text string, msgID, suffix string, replyInThread bool) (err error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("message.id", msgID))
	span.SetAttributes(otel.PreviewAttrs("message.text", text, 256)...)

	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	cardContent := larktpl.NewCardContent(
		ctx, larktpl.NormalCardReplyTemplate,
	).
		AddJaegerTraceInfo(span.SpanContext().TraceID().String()).
		AddVariable("content", text)
	logs.L().Ctx(ctx).Info(
		"reply card text",
		zap.String("msgID", msgID),
		zap.String("suffix", suffix),
		zap.Bool("replyInThread", replyInThread),
		zap.String("cardContent", cardContent.String()),
	)
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(msgID).
		Body(
			larkim.NewReplyMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeInteractive).
				Content(cardContent.String()).
				Uuid(utils.GenUUIDStr(msgID+suffix+config.Get().LarkConfig.BotOpenID, 50)).
				ReplyInThread(replyInThread).
				Build(),
		).
		Build()
	_, err = sendReplyMessage(ctx, req, cardContent.GetVariables()...)
	if err != nil {
		return
	}
	return
}

func CreateMsgCard(ctx context.Context, cardContent *larktpl.TemplateCardContent, chatID string) (err error) {
	return CreateMsgCardByReceiveID(ctx, cardContent, larkim.ReceiveIdTypeChatId, chatID)
}

func CreateMsgCardByReceiveID(ctx context.Context, cardContent *larktpl.TemplateCardContent, receiveIDType, receiveID string) (err error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("receive.id", receiveID),
		attribute.String("receive.id_type", receiveIDType),
		attribute.Int("card.variable.count", len(cardContent.Data.TemplateVariable)),
	)

	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	_, err = createMsgRawContentTypeByReceiveID(ctx, receiveIDType, receiveID, larkim.MsgTypeInteractive, cardContent.String(), "", "_card", cardContent.GetVariables()...)
	return err
}
