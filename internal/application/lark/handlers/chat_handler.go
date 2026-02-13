package handlers

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"iter"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/msg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/user"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriver"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/BetaGo/consts"
	"github.com/BetaGoRobot/BetaGo/utility"

	"github.com/BetaGoRobot/BetaGo/utility/larkutils"
	"github.com/BetaGoRobot/BetaGo/utility/message"
	opensearchdal "github.com/BetaGoRobot/BetaGo/utility/opensearch_dal"
	"github.com/BetaGoRobot/BetaGo/utility/redis"
	commonutils "github.com/BetaGoRobot/go_utils/common_utils"
	"github.com/BetaGoRobot/go_utils/reflecting"
	jsonrepair "github.com/RealAlexandreAI/json-repair"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func ChatHandler(chatType string) func(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	return func(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
		defer func() { metaData.SkipDone = true }()
		newChatType := chatType
		size := new(int)
		*size = 20
		argMap, input := parseArgs(args...)
		if _, ok := argMap["r"]; ok {
			newChatType = consts.MODEL_TYPE_REASON
		}
		if _, ok := argMap["c"]; ok {
			// no context
			*size = 0
		}
		return ChatHandlerInner(ctx, event, newChatType, size, input)
	}
}

func ChatHandlerInner(ctx context.Context, event *larkim.P2MessageReceiveV1, chatType string, size *int, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	var (
		res   iter.Seq[*ark_dal.ModelStreamRespReasoning]
		files = make([]string, 0)
	)
	if !msg.IsMentioned(event.Event.Message.Mentions) { // 禁言判断只对非at的生效
		if ext, err := redis.GetRedisClient().
			Exists(ctx, MuteRedisKeyPrefix+*event.Event.Message.ChatId).Result(); err != nil {
			return err
		} else if ext != 0 {
			return nil // Do nothing
		}
	}
	urlSeq, err := larkimg.GetAllImgURLFromMsg(ctx, *event.Event.Message.MessageId)
	if err != nil {
		return err
	}
	if urlSeq != nil {
		for url := range urlSeq {
			files = append(files, url)
		}
	}
	// 看看有没有quote的消息包含图片
	urlSeq, err = larkimg.GetAllImgURLFromParent(ctx, event)
	if err != nil {
		return err
	}
	if urlSeq != nil {
		for url := range urlSeq {
			files = append(files, url)
		}
	}
	if chatType == consts.MODEL_TYPE_REASON {
		res, err = GenerateChatSeq(ctx, event, config.Get().ArkConfig.ReasoningModel, size, files, args...)
		if err != nil {
			return
		}
		err = message.SendAndUpdateStreamingCard(ctx, event.Event.Message, res)
		if err != nil {
			return
		}
	} else {
		res, err = GenerateChatSeq(ctx, event, config.Get().ArkConfig.NormalModel, size, files, args...)
		if err != nil {
			return err
		}
		lastData := &ark_dal.ModelStreamRespReasoning{}
		for data := range res {
			span.SetAttributes(attribute.String("lastData", data.Content))
			lastData = data
			logs.L().Info("lastData", zap.Any("lastData", lastData))
			span.SetAttributes(
				attribute.String("lastData.ReasoningContent", data.ReasoningContent),
				attribute.String("lastData.Content", data.Content),
				attribute.String("lastData.ContentStruct.Reply", data.ContentStruct.Reply),
				attribute.String("lastData.ContentStruct.Decision", data.ContentStruct.Decision),
				attribute.String("lastData.ContentStruct.Thought", data.ContentStruct.Thought),
				attribute.String("lastData.ContentStruct.ReferenceFromWeb", data.ContentStruct.ReferenceFromWeb),
				attribute.String("lastData.ContentStruct.ReferenceFromHistory", data.ContentStruct.ReferenceFromHistory),
			)
			if lastData.ContentStruct.Decision == "skip" {
				return
			}
		}

		resp, err := larkutils.ReplyMsgText(
			ctx, strings.ReplaceAll(lastData.ContentStruct.Reply, "\\n", "\n"), *event.Event.Message.MessageId, "_chat_random", false,
		)
		if err != nil {
			return err
		}
		if !resp.Success() {
			return errors.New(resp.Error())
		}
	}
	return
}

func GenerateChatSeq(ctx context.Context, event *larkim.P2MessageReceiveV1, modelID string, size *int, files []string, input ...string) (res iter.Seq[*ark_dal.ModelStreamRespReasoning], err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	// 默认获取最近20条消息
	if size == nil {
		size = new(int)
		*size = 20
	}

	chatID := *event.Event.Message.ChatId
	messageList, err := history.New(ctx).
		Query(osquery.Bool().Must(osquery.Term("chat_id", chatID))).
		Source("raw_message", "mentions", "create_time", "user_id", "chat_id", "user_name", "message_type").
		Size(uint64(*size*3)).Sort("create_time", "desc").GetMsg()
	if err != nil {
		return
	}
	ins := query.Q.PromptTemplateArg
	tpl, err := ins.WithContext(ctx).Where(ins.PromptID.Eq(4)).First()
	if err != nil {
		return nil, err
	}

	promptTemplateStr := tpl.TemplateStr
	tp, err := template.New("prompt").Parse(promptTemplateStr)
	if err != nil {
		return nil, err
	}
	userInfo, err := user.GetUserInfoCache(ctx, *event.Event.Message.ChatId, *event.Event.Sender.SenderId.OpenId)
	if err != nil {
		return
	}
	userName := ""
	if userInfo == nil {
		userName = "NULL"
	} else {
		userName = *userInfo.Name
	}
	createTime := utility.EpoMil2DateStr(*event.Event.Message.CreateTime)
	tpl.UserInput = []string{fmt.Sprintf("[%s](%s) <%s>: %s", createTime, *event.Event.Sender.SenderId.OpenId, userName, larkutils.PreGetTextMsg(ctx, event))}
	tpl.HistoryRecords = messageList.ToLines()
	if len(tpl.HistoryRecords) > *size {
		tpl.HistoryRecords = tpl.HistoryRecords[len(tpl.HistoryRecords)-*size:]
	}
	docs, err := retriver.Cli().RecallDocs(ctx, chatID, *event.Event.Message.Content, 10)
	if err != nil {
		logs.L().Ctx(ctx).Error("RecallDocs err", zap.Error(err))
	}
	tpl.Context = commonutils.TransSlice(docs, func(doc schema.Document) string {
		if doc.Metadata == nil {
			doc.Metadata = map[string]any{}
		}
		createTime, _ := doc.Metadata["create_time"].(string)
		userID, _ := doc.Metadata["user_id"].(string)
		userName, _ := doc.Metadata["user_name"].(string)
		return fmt.Sprintf("[%s](%s) <%s>: %s", createTime, userID, userName, doc.PageContent)
	})
	tpl.Topics = make([]string, 0)
	for _, doc := range docs {
		msgID, ok := doc.Metadata["msg_id"]
		if ok {
			resp, err := opensearchdal.SearchData(ctx, config.Get().OpensearchConfig.LarkMsgIndex, osquery.
				Search().Sort("timestamp_v2", osquery.OrderDesc).
				Query(osquery.Bool().Must(osquery.Term("msg_ids", msgID))).
				Size(1),
			)
			if err != nil {
				return nil, err
			}
			chunk := &msg.MessageChunkLogV3{}
			if len(resp.Hits.Hits) > 0 {
				sonic.Unmarshal(resp.Hits.Hits[0].Source, &chunk)
				tpl.Topics = append(tpl.Topics, chunk.Summary)
			}
		}
	}
	tpl.Topics = utils.Dedup(tpl.Topics)
	b := &strings.Builder{}
	err = tp.Execute(b, tpl)
	if err != nil {
		return nil, err
	}

	iter, err := ark_dal.New[*larkim.P2MessageReceiveV1](
		"chat_id", "user_id", nil,
	).Do(context.Background(), b.String(), "")

	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		mentionMap := make(map[string]string)
		for _, item := range messageList {
			mentionMap[item.UserName] = msg.AtUser(item.UserID, item.UserName)
			mentionMap[item.UserID] = msg.AtUser(item.UserID, item.UserName)
			for _, mention := range item.MentionList {
				mentionMap[*mention.Name] = msg.AtUser(*mention.Id, *mention.Name)
				mentionMap[*mention.Id] = msg.AtUser(*mention.Id, *mention.Name)
			}
		}
		memberMap, err := user.GetUserMapFromChatIDCache(ctx, chatID)
		if err != nil {
			return
		}
		for _, member := range memberMap {
			mentionMap[*member.Name] = msg.AtUser(*member.MemberId, *member.Name)
			mentionMap[*member.MemberId] = msg.AtUser(*member.MemberId, *member.Name)
		}
		trie := utils.BuildTrie(mentionMap)
		lastData := &ark_dal.ModelStreamRespReasoning{}
		for data := range iter {
			lastData = data
			if !yield(data) {
				return
			}
		}
		err = sonic.UnmarshalString(lastData.Content, &lastData.ContentStruct)
		if err != nil {
			lastData.Content, err = jsonrepair.RepairJSON(lastData.Content)
			if err != nil {
				return
			}
		}
		lastData.ContentStruct.Reply = trie.ReplaceMentionsWithTrie(lastData.ContentStruct.Reply)
		if !yield(lastData) {
			return
		}
	}, err
}
