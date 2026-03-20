package handlers

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"iter"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mention"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"gorm.io/gorm"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"

	redis "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	commonutils "github.com/BetaGoRobot/go_utils/common_utils"
	jsonrepair "github.com/RealAlexandreAI/json-repair"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const (
	MODEL_TYPE_REASON = "reason"
	MODEL_TYPE_NORMAL = "normal"
)

type ChatArgs struct {
	Reason    bool   `cli:"r,flag" help:"启用推理模型"`
	NoContext bool   `cli:"c,flag" help:"不携带上下文消息"`
	Input     string `cli:"input,input" help:"聊天输入内容"`
}

type standardChatHandler struct{}
type agenticChatHandler struct{}

var (
	Chat        standardChatHandler
	AgenticChat agenticChatHandler

	agenticChatEntryHandler  = agentruntime.NewDefaultChatEntryHandler()
	standardChatSeqGenerator = generateStandardChatSeq
)

func (standardChatHandler) CommandDescription() string {
	return "与机器人对话"
}

func (agenticChatHandler) CommandDescription() string {
	return "与机器人对话"
}

func (standardChatHandler) CommandExamples() []string {
	return []string{
		"/bb 今天天气怎么样",
		"/bb --r 帮我总结一下这周讨论",
	}
}

func (agenticChatHandler) CommandExamples() []string {
	return []string{
		"/bb 今天天气怎么样",
		"/bb --r 帮我总结一下这周讨论",
	}
}

func (standardChatHandler) ParseCLI(args []string) (ChatArgs, error) {
	argMap, input := parseArgs(args...)
	_, reason := argMap["r"]
	_, noContext := argMap["c"]
	return ChatArgs{
		Reason:    reason,
		NoContext: noContext,
		Input:     input,
	}, nil
}

func (agenticChatHandler) ParseCLI(args []string) (ChatArgs, error) {
	argMap, input := parseArgs(args...)
	_, reason := argMap["r"]
	_, noContext := argMap["c"]
	return ChatArgs{
		Reason:    reason,
		NoContext: noContext,
		Input:     input,
	}, nil
}

func (standardChatHandler) Handle(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ChatArgs) error {
	defer func() { metaData.SetSkipDone(true) }()

	chatType := "chat"
	size := 20
	if arg.Reason {
		chatType = MODEL_TYPE_REASON
	}
	if arg.NoContext {
		size = 0
	}
	return runStandardChat(ctx, event, chatType, &size, arg.Input)
}

func (agenticChatHandler) Handle(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ChatArgs) error {
	defer func() { metaData.SetSkipDone(true) }()

	chatType := "chat"
	size := 20
	if arg.Reason {
		chatType = MODEL_TYPE_REASON
	}
	if arg.NoContext {
		size = 0
	}
	return runAgenticChat(ctx, event, chatType, &size, arg.Input)
}

func runStandardChat(ctx context.Context, event *larkim.P2MessageReceiveV1, chatType string, size *int, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	accessor := appconfig.NewAccessor(ctx, currentChatID(event, nil), currentOpenID(event, nil))

	var (
		res   iter.Seq[*ark_dal.ModelStreamRespReasoning]
		files = make([]string, 0)
	)
	if !larkmsg.IsMentioned(event.Event.Message.Mentions) {
		if ext, extErr := redis.GetRedisClient().
			Exists(ctx, MuteRedisKey(*event.Event.Message.ChatId)).Result(); extErr != nil {
			return extErr
		} else if ext != 0 {
			return nil
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
	urlSeq, err = larkimg.GetAllImgURLFromParent(ctx, event)
	if err != nil {
		return err
	}
	if urlSeq != nil {
		for url := range urlSeq {
			files = append(files, url)
		}
	}
	if chatType == MODEL_TYPE_REASON {
		res, err = GenerateChatSeq(ctx, event, accessor.ChatReasoningModel(), size, files, args...)
		if err != nil {
			return
		}
		err = larkmsg.SendAndUpdateStreamingCard(ctx, event.Event.Message, res)
		if err != nil {
			return
		}
	} else {
		res, err = GenerateChatSeq(ctx, event, accessor.ChatNormalModel(), size, files, args...)
		if err != nil {
			return err
		}
		lastData := &ark_dal.ModelStreamRespReasoning{}
		for data := range res {
			span.SetAttributes(attribute.String("lastData", data.Content))
			lastData = data
			logs.L().Debug("lastData", zap.Any("lastData", lastData))
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

		resp, replyErr := larkmsg.ReplyMsgText(
			ctx, lastData.ContentStruct.Reply, *event.Event.Message.MessageId, "_chat_random", false,
		)
		if replyErr != nil {
			return replyErr
		}
		if !resp.Success() {
			return errors.New(resp.Error())
		}
	}
	return
}

func runAgenticChat(ctx context.Context, event *larkim.P2MessageReceiveV1, chatType string, size *int, args ...string) (err error) {
	if agenticChatEntryHandler == nil {
		return nil
	}
	return agenticChatEntryHandler.Handle(ctx, event, chatType, size, args...)
}

func GenerateChatSeq(ctx context.Context, event *larkim.P2MessageReceiveV1, modelID string, size *int, files []string, input ...string) (res iter.Seq[*ark_dal.ModelStreamRespReasoning], err error) {
	if standardChatSeqGenerator == nil {
		return nil, nil
	}
	return standardChatSeqGenerator(ctx, event, modelID, size, files, input...)
}

func generateStandardChatSeq(ctx context.Context, event *larkim.P2MessageReceiveV1, modelID string, size *int, files []string, input ...string) (res iter.Seq[*ark_dal.ModelStreamRespReasoning], err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if size == nil {
		size = new(int)
		*size = 20
	}

	chatID := *event.Event.Message.ChatId
	accessor := appconfig.NewAccessor(ctx, chatID, currentOpenID(event, nil))
	messageList, err := history.New(ctx).
		Query(osquery.Bool().Must(osquery.Term("chat_id", chatID))).
		Source("raw_message", "mentions", "create_time", "user_id", "chat_id", "user_name", "message_type").
		Size(uint64(*size*3)).Sort("create_time", "desc").GetMsg()
	if err != nil {
		return
	}
	ins := query.Q.PromptTemplateArg
	tpls, err := ins.WithContext(ctx).Where(ins.PromptID.Eq(5)).Find()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if len(tpls) == 0 {
		return nil, errors.New("prompt template not found")
	}
	fullTpl := xmodel.PromptTemplateArg{
		PromptTemplateArg: tpls[0],
		CurrentTimeStamp:  time.Now().In(utils.UTC8Loc()).Format(time.DateTime),
	}
	promptTemplateStr := tpls[0].TemplateStr
	tp, err := template.New("prompt").Parse(promptTemplateStr)
	if err != nil {
		return nil, err
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
	createTime := utils.EpoMil2DateStr(*event.Event.Message.CreateTime)
	fullTpl.UserInput = []string{fmt.Sprintf("[%s](%s) <%s>: %s", createTime, *event.Event.Sender.SenderId.OpenId, userName, larkmsg.PreGetTextMsg(ctx, event).GetText())}
	fullTpl.HistoryRecords = messageList.ToLines()
	if len(fullTpl.HistoryRecords) > *size {
		fullTpl.HistoryRecords = fullTpl.HistoryRecords[len(fullTpl.HistoryRecords)-*size:]
	}
	docs, err := retriever.Cli().RecallDocs(ctx, chatID, *event.Event.Message.Content, 10)
	if err != nil {
		logs.L().Ctx(ctx).Error("RecallDocs err", zap.Error(err))
	}
	fullTpl.Context = commonutils.TransSlice(docs, func(doc schema.Document) string {
		if doc.Metadata == nil {
			doc.Metadata = map[string]any{}
		}
		createTime, _ := doc.Metadata["create_time"].(string)
		openID, _ := doc.Metadata["user_id"].(string)
		userName, _ := doc.Metadata["user_name"].(string)
		return fmt.Sprintf("[%s](%s) <%s>: %s", createTime, openID, userName, doc.PageContent)
	})
	fullTpl.Topics = make([]string, 0)
	for _, doc := range docs {
		msgID, ok := doc.Metadata["msg_id"]
		if ok {
			resp, searchErr := opensearch.SearchData(ctx, accessor.LarkChunkIndex(), osquery.
				Search().Sort("timestamp_v2", osquery.OrderDesc).
				Query(osquery.Bool().Must(osquery.Term("msg_ids", msgID))).
				Size(1),
			)
			if searchErr != nil {
				return nil, searchErr
			}
			chunk := &xmodel.MessageChunkLogV3{}
			if len(resp.Hits.Hits) > 0 {
				sonic.Unmarshal(resp.Hits.Hits[0].Source, &chunk)
				fullTpl.Topics = append(fullTpl.Topics, chunk.Summary)
			}
		}
	}
	fullTpl.Topics = utils.Dedup(fullTpl.Topics)
	b := &strings.Builder{}
	err = tp.Execute(b, fullTpl)
	if err != nil {
		return nil, err
	}

	iterSeq, err := ark_dal.
		New(chatID, currentOpenID(event, nil), event).
		WithTools(larktools()).
		Do(ctx, b.String(), strings.Join(fullTpl.UserInput, "\n"), files...)
	if err != nil {
		return nil, err
	}

	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		contentBuilder := &strings.Builder{}
		reasonBuilder := &strings.Builder{}
		lastData := &ark_dal.ModelStreamRespReasoning{}
		for data := range iterSeq {
			lastData = data
			contentBuilder.WriteString(data.Content)
			reasonBuilder.WriteString(data.ReasoningContent)

			if !yield(data) {
				return
			}
		}

		fullContent := contentBuilder.String()
		parseErr := sonic.UnmarshalString(fullContent, &lastData.ContentStruct)
		if parseErr != nil {
			fullContent, parseErr = jsonrepair.RepairJSON(fullContent)
			if parseErr != nil {
				return
			}
			parseErr = sonic.UnmarshalString(fullContent, &lastData.ContentStruct)
			if parseErr != nil {
				return
			}
		}
		if normalizedReply, normalizeErr := mention.NormalizeReplyText(ctx, chatID, messageList, lastData.ContentStruct.Reply); normalizeErr == nil {
			lastData.ContentStruct.Reply = normalizedReply
		}
		lastData.ReasoningContent = reasonBuilder.String()
		yield(lastData)
	}, nil
}
