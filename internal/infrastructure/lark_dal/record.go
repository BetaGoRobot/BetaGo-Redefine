package lark_dal

import (
	"context"
	"strings"
	"time"

	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriver"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo/consts"
	handlertypes "github.com/BetaGoRobot/BetaGo/handler/handler_types"
	opensearchdal "github.com/BetaGoRobot/BetaGo/utility/opensearch_dal"
	"github.com/BetaGoRobot/BetaGo/utility/otel"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/kevinmatthe/gojieba"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"go.uber.org/zap"
)

func RecordReplyMessage2Opensearch(ctx context.Context, resp *larkim.ReplyMessageResp, contents ...string) {
	ctx, span := otel.LarkRobotOtelTracer.Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	defer larkchunking.M.SubmitMessage(ctx, &larkchunking.LarkMessageRespReply{resp})
	var content string
	if len(contents) > 0 {
		content = strings.Join(contents, "\n")
	} else {
		content = getContentFromTextMsg(utils.AddrOrNil(resp.Data.Body.Content))
	}
	msgLog := &handlertypes.MessageLog{
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

	err = opensearchdal.InsertData(ctx, consts.LarkMsgIndex, utils.AddrOrNil(resp.Data.MessageId),
		&model.MessageIndex{
			MessageLog:      msgLog,
			ChatName:        GetChatName(ctx, utils.AddrOrNil(resp.Data.ChatId)),
			RawMessage:      content,
			RawMessageJieba: strings.Join(ws, " "),
			CreateTime:      utils.Epo2DateZoneMil(utils.MustInt(*resp.Data.CreateTime), time.UTC, time.DateTime),
			CreateTimeV2:    utils.Epo2DateZoneMil(utils.MustInt(*resp.Data.CreateTime), utility.UTCPlus8Loc(), time.RFC3339),
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
				"create_time": utility.EpoMil2DateStr(*resp.Data.CreateTime),
				"user_name":   "你",
			},
		}},
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("AddDocuments error", zap.Error(err))
	}
}
