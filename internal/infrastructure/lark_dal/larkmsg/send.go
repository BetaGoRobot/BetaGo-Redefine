package larkmsg

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const (
	streamingReplyElementID = "streaming_reply_md"
	streamingReplyTitle     = "正在回复"
)

type streamingContentUpdate struct {
	CardID    string
	ElementID string
	Content   string
	UUID      string
	Sequence  int
}

type streamingSettingsUpdate struct {
	CardID        string
	UUID          string
	Sequence      int
	StreamingMode bool
}

var (
	streamingCreateCardEntity  = createStreamingCardEntity
	streamingReplyCardEntity   = replyStreamingCardEntity
	streamingUpdateCardContent = updateStreamingCardContent
	streamingSetCardStreaming  = setStreamingCardMode
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
	return CreateMsgRawContentTypeByReceiveID(ctx, larkim.CreateMessageV1ReceiveIDTypeChatId, chatID, msgType, content, msgID, suffix)
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
		receiveIDType = larkim.CreateMessageV1ReceiveIDTypeChatId
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
		cardID     string
		wg         sync.WaitGroup
		errMu      sync.Mutex
		asyncErr   error
	)
	nextSeq := newStreamingSequence()
	recordAsyncErr := func(updateErr error) {
		if updateErr == nil {
			return
		}
		errMu.Lock()
		defer errMu.Unlock()
		if asyncErr == nil {
			asyncErr = updateErr
		}
	}
	waitAsync := func() error {
		wg.Wait()
		errMu.Lock()
		defer errMu.Unlock()
		return asyncErr
	}
	dispatchContentUpdate := func(update streamingContentUpdate) {
		wg.Go(func() {
			recordAsyncErr(streamingUpdateCardContent(ctx, update))
		})
	}
	dispatchSettingsUpdate := func(update streamingSettingsUpdate) {
		wg.Go(func() {
			recordAsyncErr(streamingSetCardStreaming(ctx, update))
		})
	}

	for data := range msgSeq {
		chunk := streamingChunkText(data)
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		latestText = chunk
		if cardID == "" {
			card, cardErr := buildStreamingReplyCard(latestText)
			if cardErr != nil {
				return cardErr
			}
			cardID, cardErr = streamingCreateCardEntity(ctx, card)
			if cardErr != nil {
				return cardErr
			}
			resp, replyErr := streamingReplyCardEntity(ctx, *msg.MessageId, cardID, "_streaming_reply", inThread)
			if replyErr != nil {
				return replyErr
			}
			if !resp.Success() {
				return errors.New(resp.Error())
			}
			if resp.Data == nil || resp.Data.MessageId == nil || strings.TrimSpace(*resp.Data.MessageId) == "" {
				return errors.New("empty reply message id")
			}
			continue
		}
		sequence := nextSeq()
		dispatchContentUpdate(streamingContentUpdate{
			CardID:    cardID,
			ElementID: streamingReplyElementID,
			Content:   latestText,
			UUID:      streamingUUID("content", cardID, sequence),
			Sequence:  sequence,
		})
	}
	if cardID == "" {
		if strings.TrimSpace(latestText) == "" {
			return nil
		}
		card, cardErr := buildStreamingReplyCard(latestText)
		if cardErr != nil {
			return cardErr
		}
		cardID, cardErr = streamingCreateCardEntity(ctx, card)
		if cardErr != nil {
			return cardErr
		}
		resp, replyErr := streamingReplyCardEntity(ctx, *msg.MessageId, cardID, "_streaming_reply_final", inThread)
		if replyErr != nil {
			return replyErr
		}
		if !resp.Success() {
			return errors.New(resp.Error())
		}
	}
	sequence := nextSeq()
	dispatchSettingsUpdate(streamingSettingsUpdate{
		CardID:        cardID,
		UUID:          streamingUUID("settings", cardID, sequence),
		Sequence:      sequence,
		StreamingMode: false,
	})
	return waitAsync()
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

func buildStreamingReplyCard(content string) (RawCard, error) {
	card := NewCardV2(streamingReplyTitle, []any{map[string]any{
		"tag":        "markdown",
		"element_id": streamingReplyElementID,
		"content":    content,
	}}, CardV2Options{
		HeaderTemplate:  "wathet",
		VerticalSpacing: "8px",
		Padding:         "12px",
	})
	config, ok := card["config"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid card config")
	}
	config["streaming_mode"] = true
	return card, nil
}

func newStreamingSequence() func() int {
	seq := max(int(time.Now().Unix()%2000000000), 1)
	return func() int {
		seq++
		return seq
	}
}

func streamingUUID(prefix, cardID string, sequence int) string {
	raw := fmt.Sprintf("%s-%s-%d", prefix, cardID, sequence)
	if len(raw) <= 64 {
		return raw
	}
	return utils.GenUUIDStr(raw, 64)
}

func createStreamingCardEntity(ctx context.Context, cardData any) (string, error) {
	return createCardEntityFromData(ctx, cardData)
}

func replyStreamingCardEntity(ctx context.Context, msgID, cardID, suffix string, replyInThread bool) (*larkim.ReplyMessageResp, error) {
	return ReplyMsgRawContentType(ctx, msgID, larkim.MsgTypeInteractive, larkcard.NewCardEntityContent(cardID).String(), suffix, replyInThread)
}

func updateStreamingCardContent(ctx context.Context, update streamingContentUpdate) error {
	content := utils.MustMarshalString(map[string]string{"content": update.Content})
	resp, err := lark_dal.Client().Cardkit.V1.CardElement.Content(
		ctx,
		larkcardkit.NewContentCardElementReqBuilder().
			CardId(update.CardID).
			ElementId(update.ElementID).
			Body(
				larkcardkit.NewContentCardElementReqBodyBuilder().
					Uuid(update.UUID).
					Content(content).
					Sequence(update.Sequence).
					Build(),
			).
			Build(),
	)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.Error())
	}
	return nil
}

func setStreamingCardMode(ctx context.Context, update streamingSettingsUpdate) error {
	settings := larkcard.DisableCardStreaming().String()
	if update.StreamingMode {
		settings = larkcard.EnableCardStreaming().String()
	}
	resp, err := lark_dal.Client().Cardkit.V1.Card.Settings(
		ctx,
		larkcardkit.NewSettingsCardReqBuilder().
			CardId(update.CardID).
			Body(
				larkcardkit.NewSettingsCardReqBodyBuilder().
					Settings(settings).
					Uuid(update.UUID).
					Sequence(update.Sequence).
					Build(),
			).
			Build(),
	)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.Error())
	}
	return nil
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
	_, err := createMsgRawContentTypeByReceiveID(ctx, larkim.CreateMessageV1ReceiveIDTypeChatId, chatID, larkim.MsgTypeAudio, content, "", suffix)
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
