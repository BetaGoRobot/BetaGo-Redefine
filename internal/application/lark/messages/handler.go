package messages

import (
	"context"
	"strings"
	"time"

	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/ops"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriver"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	"github.com/BetaGoRobot/go_utils/reflecting"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"github.com/yanyiwu/gojieba"
	"go.uber.org/zap"
)

// Handler  消息处理器
var Handler = &xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]{}

type (
	OpBase = xhandler.OperatorBase[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
	Op     = xhandler.Operator[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
)

func larkDeferFunc(ctx context.Context, err error, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) {
	larkmsg.SendRecoveredMsg(ctx, err, *event.Event.Message.MessageId)
}

func CollectMessage(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) {
	go func() {
		ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
		defer span.End()

		chatID, err := larkmsg.GetChatIDFromMsgID(ctx, *event.Event.Message.MessageId)
		if err != nil {
			return
		}

		userInfo, err := larkuser.GetUserInfoCache(ctx, *event.Event.Message.ChatId, *event.Event.Sender.SenderId.OpenId)
		if err != nil {
			return
		}
		userName := ""
		if userInfo == nil {
			userName = "NULL"
		} else {
			userName = *userInfo.Name
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
			logs.L().Ctx(ctx).Error("EmbeddingText error", zap.Error(err))
		}
		jieba := gojieba.NewJieba()
		defer jieba.Free()
		for _, mention := range event.Event.Message.Mentions {
			jieba.AddWord("@" + *mention.Name)
		}
		ws := jieba.Cut(content, true)
		wts := jieba.Tag(content)
		wsTags := []*xmodel.WordWithTag{}
		for _, tag := range wts {
			sp := strings.Split(tag, "/")
			if sp[0] = strings.TrimSpace(sp[0]); sp[0] == "" {
				continue
			}
			wsTags = append(wsTags, &xmodel.WordWithTag{Word: sp[0], Tag: sp[1]})
		}
		err = opensearch.InsertData(
			ctx, config.Get().OpensearchConfig.LarkMsgIndex, *event.Event.Message.MessageId,
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
				UserID:               *event.Event.Sender.SenderId.OpenId,
				UserName:             userName,
				TokenUsage:           usage,
				IsCommand:            metaData.IsCommand,
				MainCommand:          metaData.MainCommand,
			},
		)
		if err != nil {
			logs.L().Ctx(ctx).Error("InsertData error", zap.Error(err))
		}
		err = retriver.Cli().AddDocuments(ctx, utils.AddrOrNil(event.Event.Message.ChatId),
			[]schema.Document{{
				PageContent: content,
				Metadata: map[string]any{
					"chat_id":     utils.AddrOrNil(event.Event.Message.ChatId),
					"user_id":     utils.AddrOrNil(event.Event.Sender.SenderId.OpenId),
					"msg_id":      utils.AddrOrNil(event.Event.Message.MessageId),
					"create_time": utils.EpoMil2DateStr(*event.Event.Message.CreateTime),
					"user_name":   userName,
				},
			}})
		if err != nil {
			logs.L().Ctx(ctx).Error("AddDocuments error", zap.Error(err))
		}
	}()
}

func init() {
	Handler = Handler.
		OnPanic(larkDeferFunc).
		WithMetaDataProcess(metaInit).
		WithPreRun(func(p *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]) {
			go func() { utils.AddTrace2DB(p, *p.Data().Event.Message.MessageId) }()
		}).
		WithDefer(CollectMessage).
		WithDefer(func(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) {
			if !meta.IsCommand { // 过滤Command
				larkchunking.M.SubmitMessage(ctx, &larkchunking.LarkMessageEvent{P2MessageReceiveV1: event})
			}
		}).
		AddParallelStages(&ops.RecordMsgOperator{}).
		AddParallelStages(&ops.RepeatMsgOperator{}).
		AddParallelStages(&ops.ReactMsgOperator{}).
		AddParallelStages(&ops.WordReplyMsgOperator{}).
		AddParallelStages(&ops.ReplyChatOperator{}).
		AddParallelStages(&ops.CommandOperator{}).
		AddParallelStages(&ops.ChatMsgOperator{})
}

func metaInit(event *larkim.P2MessageReceiveV1) *xhandler.BaseMetaData {
	return &xhandler.BaseMetaData{
		ChatID: *event.Event.Message.ChatId,
		IsP2P:  *event.Event.Message.ChatType == "p2p",
		UserID: *event.Event.Sender.SenderId.UserId,
	}
}
