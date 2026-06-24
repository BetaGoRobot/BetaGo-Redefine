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
	"github.com/sourcegraph/conc/pool"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const (
	streamingReplyElementID = "streaming_reply_md"
	streamingReplyTitle     = "正在回复"
	streamingTickInterval   = 200 * time.Millisecond
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

type configUnit struct {
	Default int `json:"default"`
}
type streamingConfig struct {
	PrintStep        configUnit `json:"print_step"`
	PrintFrequencyMS configUnit `json:"print_frequency_ms"`
	PrintStrategy    string     `json:"print_strategy"`
}

var (
	streamingCreateCardEntity  = createStreamingCardEntity
	streamingReplyCardEntity   = replyStreamingCardEntity
	streamingUpdateCardContent = updateStreamingCardContent
	streamingSetCardStreaming  = setStreamingCardMode
)

// streamingCardPusher 封装流式卡片的节流更新逻辑。职责：
//   - 用 ticker 合并多个 chunk 为固定频率的 update 调用
//   - 每次 tick 只 snapshot 最新文本，丢弃中间值，天然节流
//   - content update 异步 dispatch，避免单次 HTTP 延迟阻塞下一次 tick
//   - finalize 先等待所有 content update 完成，再关 streaming，
//     保证 settings 一定晚于最后一条 content 被 Lark 端 apply
type streamingCardPusher struct {
	ctx      context.Context
	cardID   string
	nextSeq  func() int
	errPool  *pool.ContextPool
	stopOnce sync.Once
	ticker   *time.Ticker
	done     chan struct{}

	stateMu    sync.Mutex
	latestText string
	dirty      bool
}

func newStreamingCardPusher(ctx context.Context) *streamingCardPusher {
	p := &streamingCardPusher{
		ctx:     ctx,
		nextSeq: newStreamingSequence(),
		errPool: pool.New().WithContext(ctx).WithFirstError(),
		ticker:  time.NewTicker(streamingTickInterval),
		done:    make(chan struct{}),
	}
	go p.loop()
	return p
}

// loop 是节流 goroutine 的主循环。tick 到点时做一次 snapshot 并异步 dispatch。
func (p *streamingCardPusher) loop() {
	for {
		select {
		case <-p.ticker.C:
			p.dispatchContent()
		case <-p.done:
			return
		case <-p.ctx.Done():
			return
		}
	}
}

// Stop 停止节流器。幂等。
func (p *streamingCardPusher) Stop() {
	p.stopOnce.Do(func() {
		p.ticker.Stop()
		close(p.done)
	})
}

// setLatest 写入最新 chunk 文本并标记 dirty。
func (p *streamingCardPusher) setLatest(text string) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	p.latestText = text
	p.dirty = true
}

// snapshotLatest 原子地取出 dirty 时的最新文本并清 dirty。
func (p *streamingCardPusher) snapshotLatest() (string, bool) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if !p.dirty {
		return "", false
	}
	p.dirty = false
	return p.latestText, true
}

// dispatchContent 做一次 snapshot+异步调用 update；没脏数据或 cardID 未就绪时什么都不做。
func (p *streamingCardPusher) dispatchContent() {
	if p.cardID == "" {
		return
	}
	text, ok := p.snapshotLatest()
	if !ok {
		return
	}
	sequence := p.nextSeq()
	update := streamingContentUpdate{
		CardID:    p.cardID,
		ElementID: streamingReplyElementID,
		Content:   text,
		UUID:      streamingUUID("content", p.cardID, sequence),
		Sequence:  sequence,
	}
	p.errPool.Go(func(context.Context) error {
		return updateStreamingCard(p.ctx, update)
	})
}

// Flush 把当前 dirty 的文本立刻推一次（不走 ticker），用于流结束时保底。
func (p *streamingCardPusher) Flush() {
	p.dispatchContent()
}

// WaitContents 等所有已 dispatch 的 content update 全部返回。首个错误会被返回。
func (p *streamingCardPusher) WaitContents() error {
	return p.errPool.Wait()
}

// CloseStreaming 先 WaitContents 确保所有内容落地，再发送 streaming=false 设置。
// 这样保证 Lark 端永远先收完最后一次 content，再看到 streaming_mode 关闭，
// 避免 settings 先到被 apply 后最后一条 content 被丢弃。
func (p *streamingCardPusher) CloseStreaming() error {
	if p.cardID == "" {
		return p.WaitContents()
	}
	if err := p.WaitContents(); err != nil {
		return err
	}
	sequence := p.nextSeq()
	update := streamingSettingsUpdate{
		CardID:        p.cardID,
		UUID:          streamingUUID("settings", p.cardID, sequence),
		Sequence:      sequence,
		StreamingMode: false,
	}
	setPool := pool.New().WithContext(p.ctx).WithFirstError()
	setPool.Go(func(context.Context) error {
		return streamingSetCardStreaming(p.ctx, update)
	})
	return setPool.Wait()
}

func SendAndReplyStreamingCard(ctx context.Context, msg *larkim.EventMessage, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning], inThread bool) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if msg == nil || msg.MessageId == nil {
		return errors.New("nil message")
	}

	pusher := newStreamingCardPusher(ctx)
	defer pusher.Stop()

	var initialText string
	for data := range msgSeq {
		chunk := streamingChunkText(data)
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		if pusher.cardID == "" {
			initialText = chunk
			if err = createAndReplyCard(ctx, msg, pusher, initialText, inThread, false); err != nil {
				return err
			}
			continue
		}
		pusher.setLatest(chunk)
	}
	pusher.Stop()

	if pusher.cardID == "" {
		if strings.TrimSpace(initialText) == "" {
			return nil
		}
		return createAndReplyCard(ctx, msg, pusher, initialText, inThread, true)
	}

	pusher.Flush()
	return pusher.CloseStreaming()
}

// createAndReplyCard 构造卡片 entity 并回复到原消息。isFinal 用来区分 "_streaming_reply"
// 与 "_streaming_reply_final" 两种 suffix（后者只在流结束时才发、没有后续 update）。
func createAndReplyCard(ctx context.Context, msg *larkim.EventMessage, pusher *streamingCardPusher, content string, inThread, isFinal bool) error {
	card, err := buildStreamingReplyCard(content)
	if err != nil {
		return err
	}
	cardID, err := streamingCreateCardEntity(ctx, card)
	if err != nil {
		return err
	}
	suffix := "_streaming_reply"
	if isFinal {
		suffix = "_streaming_reply_final"
	}
	resp, err := streamingReplyCardEntity(ctx, *msg.MessageId, cardID, suffix, inThread)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.Error())
	}
	if resp.Data == nil || resp.Data.MessageId == nil || strings.TrimSpace(*resp.Data.MessageId) == "" {
		return errors.New("empty reply message id")
	}
	pusher.cardID = cardID
	return nil
}

func SendAndUpdateStreamingCard(ctx context.Context, msg *larkim.EventMessage, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning]) error {
	return SendAndReplyStreamingCard(ctx, msg, msgSeq, false)
}

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

func streamingChunkText(data *ark_dal.ModelStreamRespReasoning) string {
	if data == nil {
		return ""
	}
	if text := strings.TrimSpace(data.ContentStruct.Reply); text != "" {
		return text
	}
	return ""
}

func buildStreamingReplyCard(content string) (RawCard, error) {
	card := NewCardV2("", []any{map[string]any{
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

func updateStreamingCard(ctx context.Context, update streamingContentUpdate) error {
	resp, err := lark_dal.Client().Cardkit.V1.CardElement.Content(
		ctx,
		larkcardkit.NewContentCardElementReqBuilder().
			CardId(update.CardID).
			ElementId(update.ElementID).
			Body(
				larkcardkit.NewContentCardElementReqBodyBuilder().
					Uuid(update.UUID).
					Content(update.Content).
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
