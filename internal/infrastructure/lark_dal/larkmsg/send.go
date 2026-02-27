package larkmsg

import (
	"context"
	"errors"
	"iter"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"golang.org/x/sync/errgroup"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

type KV[K comparable, V any] struct {
	Key K
	Val V
}

// CreateMsgTextRaw 需要自行BuildText
func CreateMsgTextRaw(ctx context.Context, content, msgID, chatID string) (err error) {
	_, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("msgID").String(msgID), attribute.Key("content").String(content))
	defer span.End()
	defer func() { span.RecordError(err) }()
	// TODO: Add id saving
	uuid := (msgID + "_create")
	if len(uuid) > 50 {
		uuid = uuid[:50]
	}
	resp, err := lark_dal.Client().Im.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(
				larkim.NewCreateMessageReqBodyBuilder().
					ReceiveId(chatID).
					Content(content).
					Uuid(utils.GenUUIDStr(uuid, 50)).
					MsgType(larkim.MsgTypeText).
					Build(),
			).
			Build(),
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("CreateMessage", zap.Error(err))
		return err
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("CreateMessage", zap.String("respError", resp.Error()))
		return errors.New(resp.Error())
	}
	go RecordMessage2Opensearch(ctx, resp)
	return
}

func SendAndReplyStreamingCard(ctx context.Context, msg *larkim.EventMessage, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning], inThread bool) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	// create Card
	// 创建卡片实体
	// template := larktpl.GetTemplate(larktpl.StreamingReasonTemplate)
	// cardSrc := template.TemplateSrc
	cardContent := larktpl.NewCardContent(ctx, larktpl.NormalCardReplyTemplate)
	// 首先Create卡片实体
	cardEntiReq := larkcardkit.NewCreateCardReqBuilder().Body(
		larkcardkit.NewCreateCardReqBodyBuilder().
			// Type(`card_json`).
			Type(`template`).
			Data(cardContent.DataString()).
			Build(),
	).Build()
	createEntiResp, err := lark_dal.Client().Cardkit.V1.Card.Create(ctx, cardEntiReq)
	if err != nil {
		return err
	}
	if !createEntiResp.Success() {
		return errors.New(createEntiResp.CodeError.Error())
	}
	cardID := *createEntiResp.Data.CardId

	// 发送卡片
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(*msg.MessageId).
		Body(
			larkim.NewReplyMessageReqBodyBuilder().ReplyInThread(inThread).
				MsgType(larkim.MsgTypeInteractive).
				Content(larkcard.NewCardEntityContent(cardID).String()).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Im.V1.Message.Reply(ctx, req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.Error())
	}

	go RecordReplyMessage2Opensearch(ctx, resp)

	err, lastIdx := updateCardFunc(ctx, msgSeq, cardID)
	if err != nil {
		return err
	}
	settingUpdateReq := larkcardkit.NewSettingsCardReqBuilder().
		CardId(cardID).
		Body(larkcardkit.NewSettingsCardReqBodyBuilder().
			Settings(larkcard.DisableCardStreaming().String()).
			Sequence(lastIdx + 1).
			Build()).
		Build()
	// 发起请求
	settingUpdateResp, err := lark_dal.Client().Cardkit.V1.Card.
		Settings(ctx, settingUpdateReq)
	if err != nil {
		return err
	}
	if !settingUpdateResp.Success() {
		return errors.New(settingUpdateResp.CodeError.Error())
	}
	return nil
}

func updateCardFunc(ctx context.Context, res iter.Seq[*ark_dal.ModelStreamRespReasoning], cardID string) (err error, lastIdx int) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	idx := &atomic.Int32{}
	idx.Store(0)

	defer func() {
		lastIdx = int(idx.Load())
	}()
	sendFunc := func(key, content string) {
		ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
		defer span.End()
		defer func() { span.RecordError(err) }()
		body := larkcardkit.NewContentCardElementReqBodyBuilder().Content(content).Sequence(int(idx.Add(1))).Build()
		req := larkcardkit.NewContentCardElementReqBuilder().CardId(cardID).ElementId(key).Body(body).Build()
		resp, err := lark_dal.Client().Cardkit.V1.CardElement.Content(ctx, req)
		if err != nil {
			logs.L().Ctx(ctx).Error("patch message failed with error msg", zap.Error(err))
			return
		}
		if !resp.Success() {
			logs.L().Ctx(ctx).Error("patch message failed with error msg", zap.String("CodeError.Error", resp.CodeError.Error()))
			return
		}
	}
	var (
		msgChan = make(chan KV[string, string], 10)
		ticker  = time.NewTicker(time.Millisecond * 20)
	)
	defer ticker.Stop()
	eg := errgroup.Group{}

	eg.Go(func() error {
		defer close(msgChan)

		writeFunc := func(data ark_dal.ModelStreamRespReasoning) error {
			_, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
			defer span.End()
			defer func() { span.RecordError(err) }()

			if data.ReasoningContent != "" {
				contentSlice := []string{}
				for _, item := range strings.Split(data.ReasoningContent, "\n") {
					contentSlice = append(contentSlice, "> "+item)
				}
				data.ReasoningContent = strings.Join(contentSlice, "\n")
			}

			if data.Content != "" {
				msgChan <- KV[string, string]{"content", data.Content}
			}

			if data.ReasoningContent != "" {
				msgChan <- KV[string, string]{"cot", data.ReasoningContent}
			}
			return nil
		}

		for data := range res {
			err = writeFunc(*data)
			if err != nil {
				return err
			}
		}
		return nil
	})

	chunkQueue := make(map[string]string)
	clearQueue := func() {
		if len(chunkQueue) > 0 {
			for key, content := range chunkQueue {
				if key == "content" {
					// 尝试修复 JSON 字符串
					contentStruct := &ark_dal.ContentStruct{}
					if !strings.HasSuffix(content, "}") {
						content += "}"
					}
					if sonic.UnmarshalString(content+"}", &contentStruct); contentStruct != nil {
						content = contentStruct.BuildOutput()
					}
				}
				sendFunc(key, content)
			}
			chunkQueue = map[string]string{}
		}
	}
updateChunkLoop:
	for {
		select {
		case chunk, ok := <-msgChan:
			if !ok {
				break updateChunkLoop
			}
			chunkQueue[chunk.Key] = chunk.Val
		case <-ticker.C:
			clearQueue()
		}
	}
	clearQueue()
	return
}

// SendRecoveredMsg  SendRecoveredMsg
//
//	@param ctx
//	@param msgID
//	@param err
func SendRecoveredMsg(ctx context.Context, err any, msgID string) {
	_, span := otel.T().Start(ctx, "RecoverMsg")
	defer span.End()

	traceID := span.SpanContext().TraceID().String()
	if e, ok := err.(error); ok {
		span.RecordError(e)
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

func SendAndUpdateStreamingCard(ctx context.Context, msg *larkim.EventMessage, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	// create Card
	// 创建卡片实体

	cardContent := larktpl.NewCardContent(ctx, larktpl.NormalCardReplyTemplate)
	// 首先Create卡片实体
	cardEntiReq := larkcardkit.NewCreateCardReqBuilder().Body(
		larkcardkit.NewCreateCardReqBodyBuilder().
			// Type(`card_json`).
			Type(`template`).
			Data(cardContent.DataString()).
			Build(),
	).Build()
	createEntiResp, err := lark_dal.Client().Cardkit.V1.Card.Create(ctx, cardEntiReq)
	if err != nil {
		return err
	}
	if !createEntiResp.Success() {
		return errors.New(createEntiResp.CodeError.Error())
	}
	cardID := *createEntiResp.Data.CardId

	// 发送卡片
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(
			larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(*msg.ChatId).
				MsgType(larkim.MsgTypeInteractive).
				Content(larkcard.NewCardEntityContent(cardID).String()).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Im.V1.Message.Create(ctx, req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.Error())
	}

	RecordMessage2Opensearch(ctx, resp)

	err, lastIdx := updateCardFunc(ctx, msgSeq, cardID)
	if err != nil {
		return err
	}
	settingUpdateReq := larkcardkit.NewSettingsCardReqBuilder().
		CardId(cardID).
		Body(larkcardkit.NewSettingsCardReqBodyBuilder().
			Settings(larkcard.DisableCardStreaming().String()).
			Sequence(lastIdx + 1).
			Build()).
		Build()
	// 发起请求
	settingUpdateResp, err := lark_dal.Client().Cardkit.V1.Card.
		Settings(ctx, settingUpdateReq)
	if err != nil {
		return err
	}
	if !settingUpdateResp.Success() {
		return errors.New(settingUpdateResp.CodeError.Error())
	}
	return nil
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
