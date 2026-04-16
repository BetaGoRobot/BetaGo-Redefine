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

func SendAndReplyStreamingCard(ctx context.Context, msg *larkim.EventMessage, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning], inThread bool) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if msg == nil || msg.MessageId == nil {
		return errors.New("nil message")
	}

	var (
		latestText string
		replyMsgID string
	)
	for data := range msgSeq {
		chunk := streamingChunkText(data)
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		latestText = chunk
		if replyMsgID == "" {
			resp, replyErr := ReplyMsgText(ctx, latestText, *msg.MessageId, "_streaming_reply", inThread)
			if replyErr != nil {
				return replyErr
			}
			if !resp.Success() {
				return errors.New(resp.Error())
			}
			if resp.Data == nil || resp.Data.MessageId == nil || strings.TrimSpace(*resp.Data.MessageId) == "" {
				return errors.New("empty reply message id")
			}
			replyMsgID = strings.TrimSpace(*resp.Data.MessageId)
			continue
		}
		if patchErr := PatchTextMessage(ctx, replyMsgID, latestText); patchErr != nil {
			return patchErr
		}
	}
	if replyMsgID != "" || strings.TrimSpace(latestText) == "" {
		return nil
	}
	resp, replyErr := ReplyMsgText(ctx, latestText, *msg.MessageId, "_streaming_reply_final", inThread)
	if replyErr != nil {
		return replyErr
	}
	if !resp.Success() {
		return errors.New(resp.Error())
	}
	return nil
}

func SendAndUpdateStreamingCard(ctx context.Context, msg *larkim.EventMessage, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning]) error {
	return SendAndReplyStreamingCard(ctx, msg, msgSeq, false)
}

func streamingChunkText(data *ark_dal.ModelStreamRespReasoning) string {
	if data == nil {
		return ""
	}
	if text := strings.TrimSpace(data.ContentStruct.Reply); text != "" {
		return text
	}
	return strings.TrimSpace(data.Content)
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

// CreateMsgAudioRaw 发送音频消息到指定群组或对话
//
//	@param ctx context.Context
//	@param fileKey 音频文件的key，通过 UploadAudio 获取
//	@param chatID 群组ID
//	@param suffix 日志追踪后缀
//	@return error
func CreateMsgAudioRaw(ctx context.Context, fileKey, chatID, suffix string) error {
	_, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, nil) }()

	content := fmt.Sprintf(`{"file_key":"%s"}`, fileKey)
	_, err := createMsgRawContentTypeByReceiveID(ctx, larkim.ReceiveIdTypeChatId, chatID, larkim.MsgTypeAudio, content, "", suffix)
	return err
}

// ReplyMsgAudio 回复音频消息
//
//	@param ctx context.Context
//	@param fileKey 音频文件的key
//	@param msgID 要回复的消息ID
//	@param suffix 日志追踪后缀
//	@param inThread 是否在thread内回复
//	@return *larkim.ReplyMessageResp
//	@return error
func ReplyMsgAudio(ctx context.Context, fileKey, msgID, suffix string, inThread bool) (resp *larkim.ReplyMessageResp, err error) {
	_, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	content := fmt.Sprintf(`{"file_key":"%s"}`, fileKey)
	return ReplyMsgRawContentType(ctx, msgID, larkim.MsgTypeAudio, content, suffix, inThread)
}
