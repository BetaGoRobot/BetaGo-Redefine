package recording

import (
	"context"
	"strings"
	"sync"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"github.com/yanyiwu/gojieba"
	"go.uber.org/zap"
)

type taskSubmitter interface {
	Submit(context.Context, string, func(context.Context) error) error
}

var (
	backgroundSubmitter taskSubmitter
	submitterMu         sync.RWMutex
)

func SetBackgroundSubmitter(submitter taskSubmitter) {
	submitterMu.Lock()
	defer submitterMu.Unlock()
	backgroundSubmitter = submitter
}

func getBackgroundSubmitter() taskSubmitter {
	submitterMu.RLock()
	defer submitterMu.RUnlock()
	return backgroundSubmitter
}

func CollectMessage(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) {
	recordFunc := func(taskCtx context.Context) (err error) {
		ctx = taskCtx
		ctx, span := otel.Start(ctx)
		defer span.End()
		chatID := *event.Event.Message.ChatId
		if privateModeEnabled, err := larkmsg.IsPrivateModeEnabled(ctx, chatID); err != nil {
			logs.L().Ctx(ctx).Warn("check private mode failed", zap.Error(err))
			return err
		} else if privateModeEnabled {
			return nil
		}
		if shouldRecord, err := larkmsg.ClaimMessageRecord(ctx, utils.AddrOrNil(event.Event.Message.MessageId)); err != nil || !shouldRecord {
			if err != nil {
				logs.L().Ctx(ctx).Warn("skip inbound message record dedup check failed", zap.Error(err))
				return err
			}
			return nil
		}

		openID := botidentity.MessageSenderOpenID(event)
		if openID == "" && metaData != nil {
			openID = metaData.OpenID
		}
		userName := ""
		if openID != "" {
			userName, err = larkuser.GetUserNameCache(ctx, *event.Event.Message.ChatId, openID)
			if err != nil {
				return err
			}
		} else {
			userName = "NULL"
			logs.L().Ctx(ctx).Warn("record message without open_id",
				zap.String("message_id", utils.AddrOrNil(event.Event.Message.MessageId)),
			)
		}
		msgLog := &xmodel.MessageLog{
			MessageID:   utils.AddrOrNil(event.Event.Message.MessageId),
			RootID:      utils.AddrOrNil(event.Event.Message.RootId),
			ParentID:    utils.AddrOrNil(event.Event.Message.ParentId),
			ChatID:      utils.AddrOrNil(event.Event.Message.ChatId),
			ThreadID:    utils.AddrOrNil(event.Event.Message.ThreadId),
			ChatType:    utils.AddrOrNil(event.Event.Message.ChatType),
			MessageType: utils.AddrOrNil(event.Event.Message.MessageType),
			UserAgent:   utils.AddrOrNil(event.Event.Message.UserAgent),
			Mentions:    utils.MustMarshalString(event.Event.Message.Mentions),
			RawBody:     utils.MustMarshalString(event),
			Content:     utils.AddrOrNil(event.Event.Message.Content),
			TraceID:     span.SpanContext().TraceID().String(),
		}
		content := larkmsg.PreGetTextMsg(ctx, event).GetText()
		embedded, usage, err := ark_dal.EmbeddingText(ctx, content)
		if err != nil {
			logs.L().Ctx(ctx).Error("EmbeddingText error", zap.Error(err), zap.String("content", content))
			return err
		}
		jieba := gojieba.NewJieba()
		defer jieba.Free()
		for _, mention := range event.Event.Message.Mentions {
			jieba.AddWord("@" + *mention.Name)
		}
		ws := jieba.Cut(content, true)
		wts := jieba.Tag(content)
		wsTags := make([]*xmodel.WordWithTag, 0, len(wts))
		for _, tag := range wts {
			sp := strings.Split(tag, "/")
			if sp[0] = strings.TrimSpace(sp[0]); sp[0] == "" {
				continue
			}
			wsTags = append(wsTags, &xmodel.WordWithTag{Word: sp[0], Tag: sp[1]})
		}

		isCommand := metaData.IsCommandMarked()
		mainCommand := metaData.GetMainCommand()
		accessor := appconfig.NewAccessor(ctx, chatID, openID)
		err = opensearch.InsertData(
			ctx, accessor.LarkMsgIndex(), *event.Event.Message.MessageId,
			&xmodel.MessageIndex{
				MessageLog:           msgLog,
				ChatName:             larkchat.GetChatName(ctx, chatID),
				RawMessage:           content,
				RawMessageJieba:      strings.Join(ws, " "),
				RawMessageJiebaArray: ws,
				RawMessageJiebaTag:   wsTags,
				CreateTime:           utils.Epo2DateZoneMil(utils.MustInt(*event.Event.Message.CreateTime), time.UTC, time.DateTime),
				CreateTimeV2:         utils.Epo2DateZoneMil(utils.MustInt(*event.Event.Message.CreateTime), utils.UTC8Loc(), time.RFC3339),
				Message:              embedded,
				OpenID:               openID,
				UserName:             userName,
				TokenUsage:           usage,
				IsCommand:            isCommand,
				MainCommand:          mainCommand,
			},
		)
		if err != nil {
			logs.L().Ctx(ctx).Error("InsertData error", zap.Error(err))
		}

		err = retriever.Cli().AddDocuments(ctx, utils.AddrOrNil(event.Event.Message.ChatId),
			[]schema.Document{{
				PageContent: content,
				Metadata: map[string]any{
					"chat_id":     utils.AddrOrNil(event.Event.Message.ChatId),
					"user_id":     openID,
					"msg_id":      utils.AddrOrNil(event.Event.Message.MessageId),
					"create_time": utils.EpoMil2DateStr(*event.Event.Message.CreateTime),
					"user_name":   userName,
				},
			}})
		if err != nil {
			logs.L().Ctx(ctx).Error("AddDocuments error", zap.Error(err), zap.String("content", content))
		}
		return nil
	}

	if submitter := getBackgroundSubmitter(); submitter != nil {
		if err := submitter.Submit(ctx, "record_message:"+utils.AddrOrNil(event.Event.Message.MessageId), recordFunc); err != nil {
			logs.L().Ctx(ctx).Error("submit record message task failed", zap.Error(err))
		}
		return
	}

	go func() {
		_ = recordFunc(ctx)
	}()
}
