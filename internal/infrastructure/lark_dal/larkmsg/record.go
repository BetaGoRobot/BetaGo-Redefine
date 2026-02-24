package larkmsg

import (
	"context"
	"errors"
	"strings"
	"time"

	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriver"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"gorm.io/gorm"

	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"github.com/yanyiwu/gojieba"
	"go.uber.org/zap"
)

func RecordReplyMessage2Opensearch(ctx context.Context, resp *larkim.ReplyMessageResp, contents ...string) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	defer larkchunking.M.SubmitMessage(ctx, &larkchunking.LarkMessageRespReply{resp})
	var content string
	if len(contents) > 0 {
		content = strings.Join(contents, "\n")
	} else {
		content = GetContentFromTextMsg(utils.AddrOrNil(resp.Data.Body.Content))
	}
	msgLog := &xmodel.MessageLog{
		MessageID:   utils.AddrOrNil(resp.Data.MessageId),
		RootID:      utils.AddrOrNil(resp.Data.RootId),
		ParentID:    utils.AddrOrNil(resp.Data.ParentId),
		ChatID:      utils.AddrOrNil(resp.Data.ChatId),
		ThreadID:    utils.AddrOrNil(resp.Data.ThreadId),
		ChatType:    "",
		MessageType: utils.AddrOrNil(resp.Data.MsgType),
		UserAgent:   "",
		Mentions:    utils.MustMarshalString(resp.Data.Mentions),
		RawBody:     utils.MustMarshalString(resp),
		Content:     content,
		TraceID:     span.SpanContext().TraceID().String(),
	}

	embedded, usage, err := ark_dal.EmbeddingText(ctx, utils.AddrOrNil(resp.Data.Body.Content))
	if err != nil {
		logs.L().Ctx(ctx).Error("EmbeddingText error", zap.Error(err))
	}
	jieba := gojieba.NewJieba()
	defer jieba.Free()
	ws := jieba.Cut(content, true)

	err = opensearch.InsertData(ctx, config.Get().OpensearchConfig.LarkMsgIndex, utils.AddrOrNil(resp.Data.MessageId),
		&xmodel.MessageIndex{
			MessageLog:      msgLog,
			ChatName:        larkchat.GetChatName(ctx, utils.AddrOrNil(resp.Data.ChatId)),
			RawMessage:      content,
			RawMessageJieba: strings.Join(ws, " "),
			CreateTime:      utils.Epo2DateZoneMil(utils.MustInt(*resp.Data.CreateTime), time.UTC, time.DateTime),
			CreateTimeV2:    utils.Epo2DateZoneMil(utils.MustInt(*resp.Data.CreateTime), utils.UTC8Loc(), time.RFC3339),
			Message:         embedded,
			UserID:          "你",
			UserName:        "你",
			TokenUsage:      usage,
		},
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("InsertData", zap.Error(err))
		return
	}
	err = retriver.Cli().AddDocuments(ctx, utils.AddrOrNil(resp.Data.ChatId),
		[]schema.Document{{
			PageContent: content,
			Metadata: map[string]any{
				"chat_id":     utils.AddrOrNil(resp.Data.ChatId),
				"user_id":     utils.AddrOrNil(resp.Data.Sender.Id),
				"msg_id":      utils.AddrOrNil(resp.Data.MessageId),
				"create_time": utils.EpoMil2DateStr(*resp.Data.CreateTime),
				"user_name":   "你",
			},
		}},
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("AddDocuments error", zap.Error(err))
	}
}

func RecordMessage2Opensearch(ctx context.Context, resp *larkim.CreateMessageResp, contents ...string) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	defer larkchunking.M.SubmitMessage(ctx, &larkchunking.LarkMessageRespCreate{resp})

	var content string
	if len(contents) > 0 {
		content = strings.Join(contents, "\n")
	} else {
		content = GetContentFromTextMsg(utils.AddrOrNil(resp.Data.Body.Content))
	}
	ins := query.Q.PrivateMode
	configs, err := ins.WithContext(ctx).Where(ins.ChatID.Eq(utils.AddrOrNil(resp.Data.ChatId))).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logs.L().Ctx(ctx).Error("FindByCacheFunc error", zap.Error(err))
		return
	}
	if configs != nil && configs.Enable {
		// 隐私模式，不存了
		logs.L().Ctx(ctx).Info("ChatID hit private config, will not record data...",
			zap.String("chat_id", utils.AddrOrNil(resp.Data.ChatId)),
		)
		return
	}

	msgLog := &xmodel.MessageLog{
		MessageID:   utils.AddrOrNil(resp.Data.MessageId),
		RootID:      utils.AddrOrNil(resp.Data.RootId),
		ParentID:    utils.AddrOrNil(resp.Data.ParentId),
		ChatID:      utils.AddrOrNil(resp.Data.ChatId),
		ThreadID:    utils.AddrOrNil(resp.Data.ThreadId),
		ChatType:    "",
		MessageType: utils.AddrOrNil(resp.Data.MsgType),
		UserAgent:   "",
		Mentions:    utils.MustMarshalString(resp.Data.Mentions),
		RawBody:     utils.MustMarshalString(resp),
		Content:     content,
		TraceID:     span.SpanContext().TraceID().String(),
	}
	embedded, usage, err := ark_dal.EmbeddingText(ctx, utils.AddrOrNil(resp.Data.Body.Content))
	if err != nil {
		logs.L().Ctx(ctx).Error("EmbeddingText error", zap.Error(err))
	}
	jieba := gojieba.NewJieba()
	defer jieba.Free()
	ws := jieba.Cut(content, true)

	err = opensearch.InsertData(ctx, config.Get().OpensearchConfig.LarkMsgIndex,
		utils.AddrOrNil(resp.Data.MessageId),
		&xmodel.MessageIndex{
			MessageLog:      msgLog,
			ChatName:        larkchat.GetChatName(ctx, utils.AddrOrNil(resp.Data.ChatId)),
			RawMessage:      content,
			RawMessageJieba: strings.Join(ws, " "),
			CreateTime:      utils.Epo2DateZoneMil(utils.MustInt(*resp.Data.CreateTime), time.UTC, time.DateTime),
			CreateTimeV2:    utils.Epo2DateZoneMil(utils.MustInt(*resp.Data.CreateTime), utils.UTC8Loc(), time.RFC3339),
			Message:         embedded,
			UserID:          "你",
			UserName:        "你",
			TokenUsage:      usage,
		},
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("InsertData", zap.Error(err))
		return
	}
	err = retriver.Cli().AddDocuments(ctx, utils.AddrOrNil(resp.Data.ChatId),
		[]schema.Document{{
			PageContent: content,
			Metadata: map[string]any{
				"chat_id":     utils.AddrOrNil(resp.Data.ChatId),
				"user_id":     utils.AddrOrNil(resp.Data.Sender.Id),
				"msg_id":      utils.AddrOrNil(resp.Data.MessageId),
				"create_time": utils.EpoMil2DateStr(*resp.Data.CreateTime),
				"user_name":   "你",
			},
		}},
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("AddDocuments error", zap.Error(err))
	}
}

func RecordCardAction2Opensearch(ctx context.Context, cardAction *callback.CardActionTriggerEvent) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	chatID := cardAction.Event.Context.OpenChatID
	userID := cardAction.Event.Operator.OpenID
	userInfo, err := larkuser.GetUserInfoCache(ctx, cardAction.Event.Context.OpenChatID, userID)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetUserInfo error", zap.Error(err))
		return
	}
	idxData := &xmodel.CardActionIndex{
		CardActionTriggerEvent: cardAction,
		ChatName:               larkchat.GetChatName(ctx, chatID),
		CreateTime:             utils.EpoMicro2DateStr(cardAction.EventV2Base.Header.CreateTime),
		UserID:                 userID,
		UserName:               utils.AddrOrNil(userInfo.Name),
		ActionValue:            cardAction.Event.Action.Value,
	}
	err = opensearch.InsertData(ctx,
		config.Get().OpensearchConfig.LarkCardActionIndex,
		cardAction.Event.Operator.OpenID,
		idxData,
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("InsertData", zap.Error(err))
		return
	}
}
