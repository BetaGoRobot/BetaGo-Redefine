package larkmsg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"github.com/yanyiwu/gojieba"
	"go.uber.org/zap"
)

func RecordReplyMessage2Opensearch(ctx context.Context, resp *larkim.ReplyMessageResp, contents ...string) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	if resp == nil || resp.Data == nil {
		return
	}
	if privateModeEnabled, err := IsPrivateModeEnabled(ctx, utils.AddrOrNil(resp.Data.ChatId)); err != nil {
		logs.L().Ctx(ctx).Warn("check private mode failed", zap.Error(err))
		return
	} else if privateModeEnabled {
		return
	}
	if shouldRecord, err := ClaimMessageRecord(ctx, utils.AddrOrNil(resp.Data.MessageId)); err != nil || !shouldRecord {
		if err != nil {
			logs.L().Ctx(ctx).Warn("skip reply message record dedup check failed", zap.Error(err))
		}
		return
	}

	go utils.AddTrace2DB(ctx, *resp.Data.MessageId)
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
			OpenID:          "你",
			UserName:        "你",
			TokenUsage:      usage,
		},
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("InsertData", zap.Error(err))
		return
	}
	err = retriever.Cli().AddDocuments(ctx, utils.AddrOrNil(resp.Data.ChatId),
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
	ctx, span := otel.Start(ctx)
	defer span.End()
	if resp == nil || resp.Data == nil {
		return
	}
	if privateModeEnabled, err := IsPrivateModeEnabled(ctx, utils.AddrOrNil(resp.Data.ChatId)); err != nil {
		logs.L().Ctx(ctx).Warn("check private mode failed", zap.Error(err))
		return
	} else if privateModeEnabled {
		logs.L().Ctx(ctx).Info("ChatID hit private config, will not record data...",
			zap.String("chat_id", utils.AddrOrNil(resp.Data.ChatId)),
		)
		return
	}
	if shouldRecord, err := ClaimMessageRecord(ctx, utils.AddrOrNil(resp.Data.MessageId)); err != nil || !shouldRecord {
		if err != nil {
			logs.L().Ctx(ctx).Warn("skip create message record dedup check failed", zap.Error(err))
		}
		return
	}
	go utils.AddTrace2DB(ctx, *resp.Data.MessageId)
	defer larkchunking.M.SubmitMessage(ctx, &larkchunking.LarkMessageRespCreate{resp})

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
			OpenID:          "你",
			UserName:        "你",
			TokenUsage:      usage,
		},
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("InsertData", zap.Error(err))
		return
	}
	err = retriever.Cli().AddDocuments(ctx, utils.AddrOrNil(resp.Data.ChatId),
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
	ctx, span := otel.Start(ctx)
	defer span.End()
	if cardAction == nil || cardAction.Event == nil || cardAction.Event.Context == nil || cardAction.Event.Operator == nil {
		return
	}

	chatID := cardAction.Event.Context.OpenChatID
	openMessageID := strings.TrimSpace(cardAction.Event.Context.OpenMessageID)
	openID := cardAction.Event.Operator.OpenID
	userInfo, err := larkuser.GetUserInfoCache(ctx, cardAction.Event.Context.OpenChatID, openID)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetUserInfo error", zap.Error(err))
		return
	}
	actionName, actionTag, selectedOption := cardActionMetadata(cardAction)
	createTime := ""
	if cardAction.EventV2Base != nil && cardAction.EventV2Base.Header != nil {
		createTime = utils.EpoMicro2DateStr(cardAction.EventV2Base.Header.CreateTime)
	}
	actionValue := map[string]any(nil)
	if cardAction.Event.Action != nil {
		actionValue = cardAction.Event.Action.Value
	}
	idxData := &xmodel.CardActionIndex{
		CardActionTriggerEvent: cardAction,
		ChatName:               larkchat.GetChatName(ctx, chatID),
		CreateTime:             createTime,
		CreateTimeUnix:         parseCardActionCreateTime(cardAction),
		OpenID:                 openID,
		UserName:               utils.AddrOrNil(userInfo.Name),
		OpenMessageID:          openMessageID,
		OpenChatID:             chatID,
		ActionName:             actionName,
		ActionTag:              actionTag,
		SelectedOption:         selectedOption,
		ActionValue:            actionValue,
	}
	err = opensearch.InsertData(ctx,
		config.Get().OpensearchConfig.LarkCardActionIndex,
		cardActionDocID(cardAction),
		idxData,
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("InsertData", zap.Error(err))
		return
	}
}

func cardActionMetadata(cardAction *callback.CardActionTriggerEvent) (actionName, actionTag, selectedOption string) {
	if cardAction == nil || cardAction.Event == nil || cardAction.Event.Action == nil {
		return "", "", ""
	}

	actionTag = strings.TrimSpace(cardAction.Event.Action.Tag)
	selectedOption = strings.TrimSpace(cardAction.Event.Action.Option)
	if parsed, err := cardactionproto.Parse(cardAction); err == nil {
		actionName = strings.TrimSpace(parsed.Name)
		if actionTag == "" {
			actionTag = strings.TrimSpace(parsed.Tag)
		}
		if selectedOption == "" {
			selectedOption = parsed.SelectedOption()
		}
		return actionName, actionTag, selectedOption
	}

	if value, ok := cardAction.Event.Action.Value[cardactionproto.ActionField].(string); ok {
		actionName = strings.TrimSpace(value)
	}
	return actionName, actionTag, selectedOption
}

func cardActionDocID(cardAction *callback.CardActionTriggerEvent) string {
	if cardAction == nil {
		return ""
	}
	if cardAction.EventV2Base != nil && cardAction.EventV2Base.Header != nil {
		if eventID := strings.TrimSpace(cardAction.EventV2Base.Header.EventID); eventID != "" {
			return eventID
		}
	}
	if cardAction.EventReq != nil {
		if requestID := strings.TrimSpace(cardAction.RequestId()); requestID != "" {
			return requestID
		}
	}

	openMessageID := ""
	operatorOpenID := ""
	createTime := ""
	actionValueJSON := []byte("null")
	if cardAction.Event != nil {
		if cardAction.Event.Context != nil {
			openMessageID = strings.TrimSpace(cardAction.Event.Context.OpenMessageID)
		}
		if cardAction.Event.Operator != nil {
			operatorOpenID = strings.TrimSpace(cardAction.Event.Operator.OpenID)
		}
		if cardAction.Event.Action != nil {
			if data, err := json.Marshal(cardAction.Event.Action.Value); err == nil {
				actionValueJSON = data
			}
		}
	}
	if cardAction.EventV2Base != nil && cardAction.EventV2Base.Header != nil {
		createTime = strings.TrimSpace(cardAction.EventV2Base.Header.CreateTime)
	}

	sum := sha256.Sum256(actionValueJSON)
	return fmt.Sprintf(
		"card_action:%s:%s:%s:%s",
		openMessageID,
		operatorOpenID,
		createTime,
		hex.EncodeToString(sum[:]),
	)
}

func parseCardActionCreateTime(cardAction *callback.CardActionTriggerEvent) int64 {
	if cardAction == nil || cardAction.EventV2Base == nil || cardAction.EventV2Base.Header == nil {
		return 0
	}
	value, _ := strconv.ParseInt(strings.TrimSpace(cardAction.EventV2Base.Header.CreateTime), 10, 64)
	return value
}
