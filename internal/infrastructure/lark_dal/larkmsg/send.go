package larkmsg

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"runtime/debug"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// CreateMsgTextRaw 需要自行BuildText
func CreateMsgTextRaw(ctx context.Context, content, msgID, chatID string) (err error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("content", content, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	if msgID == "" {
		msgID = fmt.Sprintf("create-%d", time.Now().UnixNano())
	}
	span.SetAttributes(attribute.Key("msgID").String(msgID))
	resp, err := CreateMsgRawContentType(ctx, chatID, larkim.MsgTypeText, content, msgID, "_create")
	if err != nil {
		return
	}
	if !resp.Success() {
		return errors.New(resp.CodeError.Error())
	}
	go utils.AddTrace2DB(ctx, *resp.Data.MessageId)
	return err
}

func CreateMsgRawContentType(ctx context.Context, chatID, msgType, content, msgID, suffix string) (resp *larkim.CreateMessageResp, err error) {
	return CreateMsgRawContentTypeByReceiveID(ctx, larkim.ReceiveIdTypeChatId, chatID, msgType, content, msgID, suffix)
}

func CreateMsgRawContentTypeByReceiveID(ctx context.Context, receiveIDType, receiveID, msgType, content, msgID, suffix string) (resp *larkim.CreateMessageResp, err error) {
	return createMsgRawContentTypeByReceiveID(ctx, receiveIDType, receiveID, msgType, content, msgID, suffix)
}

func createMsgRawContentTypeByReceiveID(ctx context.Context, receiveIDType, receiveID, msgType, content, msgID, suffix string, recordContents ...string) (resp *larkim.CreateMessageResp, err error) {
	if msgID == "" {
		msgID = fmt.Sprintf("create-%d", time.Now().UnixNano())
	}
	uuid := msgID + suffix
	if len(uuid) > 50 {
		uuid = uuid[:50]
	}
	receiveIDType = strings.TrimSpace(receiveIDType)
	if receiveIDType == "" {
		receiveIDType = larkim.ReceiveIdTypeChatId
	}

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType).
		Body(
			larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(receiveID).
				Content(content).
				Uuid(utils.GenUUIDStr(uuid, 50)).
				MsgType(msgType).
				Build(),
		).
		Build()

	return sendCreateMessage(ctx, req, recordContents...)
}

func SendAndReplyStreamingCard(ctx context.Context, msg *larkim.EventMessage, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning], inThread bool, options ...AgentStreamingCardOptions) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	_, err = sendAgentStreamingReplyCard(ctx, msg, msgSeq, inThread, options...)
	return err
}

// SendAndReplyStreamingCardWithRefs replies with an agentic streaming card and returns both message/card refs.
func SendAndReplyStreamingCardWithRefs(
	ctx context.Context,
	msg *larkim.EventMessage,
	msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning],
	inThread bool,
	options ...AgentStreamingCardOptions,
) (refs AgentStreamingCardRefs, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	return sendAgentStreamingReplyCard(ctx, msg, msgSeq, inThread, options...)
}

// SendRecoveredMsg  SendRecoveredMsg
//
//	@param ctx
//	@param msgID
//	@param err
func SendRecoveredMsg(ctx context.Context, err any, msgID string) {
	_, span := otel.StartNamed(ctx, "RecoverMsg")
	defer span.End()

	traceID := span.SpanContext().TraceID().String()
	if e, ok := err.(error); ok {
		otel.RecordError(span, e)
	}
	stack := string(debug.Stack())
	logs.L().Ctx(ctx).Error("panic-detected!", zap.Any("Error", err), zap.String("trace_id", traceID), zap.String("msg_id", msgID), zap.Stack("stack"))
	card := larkcard.NewCardBuildHelper().
		SetTitle("Panic Detected!").
		SetSubTitle("Please check the log for more information.").
		SetContent("```go\n" + stack + "\n```").Build(ctx)
	err = ReplyCard(ctx, card, msgID, "", true)
	if err != nil {
		logs.L().Ctx(ctx).Error("send error", zap.Error(err.(error)))
	}
}

func SendAndUpdateStreamingCard(ctx context.Context, msg *larkim.EventMessage, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning], options ...AgentStreamingCardOptions) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	_, err = SendAndUpdateStreamingCardWithRefs(ctx, msg, msgSeq, options...)
	return err
}

func SendAndUpdateStreamingCardWithRefs(ctx context.Context, msg *larkim.EventMessage, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning], options ...AgentStreamingCardOptions) (refs AgentStreamingCardRefs, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	return sendAgentStreamingCreateCardFunc(ctx, msg, msgSeq, options...)
}

func RecoverMsg(ctx context.Context, msgID string) {
	if err := recover(); err != nil {
		SendRecoveredMsg(ctx, err, msgID)
	}
}

func RecoverMsgEvent(ctx context.Context, event *larkim.P2MessageReceiveV1) {
	if err := recover(); err != nil {
		SendRecoveredMsg(ctx, err, *event.Event.Message.MessageId)
	}
}
