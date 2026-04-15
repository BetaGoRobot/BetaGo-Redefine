package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mention"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	jsonrepair "github.com/RealAlexandreAI/json-repair"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
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

type (
	chatHandler struct{}
)

var Chat = chatHandler{}

var standardChatBotProfileLoader = botidentity.CurrentProfile

type standardPromptMode string

const (
	standardPromptModeDirect  standardPromptMode = "direct"
	standardPromptModeAmbient standardPromptMode = "ambient"
)

func (chatHandler) CommandDescription() string {
	return "与机器人对话"
}

func getHistoryCutoffTime(ctx context.Context, chatID string) string {
	cfgManager := appconfig.GetManager()
	return cfgManager.GetString(ctx, appconfig.KeyHistoryCutoffTime, chatID, "")
}

func buildCorrectionsContext(ctx context.Context, chatID string) string {
	cfgManager := appconfig.GetManager()
	correctionsJSON := cfgManager.GetString(ctx, appconfig.KeyChatCorrections, chatID, "")
	if correctionsJSON == "" {
		return ""
	}
	var corrections []ChatCorrection
	if err := json.Unmarshal([]byte(correctionsJSON), &corrections); err != nil {
		return ""
	}
	if len(corrections) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "\n\n=== 历史纠正记录 ===")
	for _, c := range corrections {
		lines = append(lines, fmt.Sprintf("- 纠正: %s → 正确: %s", c.Correction))
	}
	return strings.Join(lines, "\n")
}

func getChatPersona(ctx context.Context, chatID string) string {
	cfgManager := appconfig.GetManager()
	return cfgManager.GetString(ctx, appconfig.KeyChatPersona, chatID, "")
}

func getChatExtraContext(ctx context.Context, chatID string) string {
	cfgManager := appconfig.GetManager()
	return cfgManager.GetString(ctx, appconfig.KeyChatExtraContext, chatID, "")
}

func (chatHandler) CommandExamples() []string {
	return []string{
		"/bb 今天天气怎么样",
		"/bb --r 帮我总结一下这周讨论",
	}
}

func (chatHandler) ParseCLI(args []string) (ChatArgs, error) {
	argMap, input := parseArgs(args...)
	_, reason := argMap["r"]
	_, noContext := argMap["c"]
	return ChatArgs{
		Reason:    reason,
		NoContext: noContext,
		Input:     input,
	}, nil
}

func (chatHandler) Handle(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ChatArgs) error {
	defer func() { metaData.SetSkipDone(true) }()

	chatType := "chat"
	size := 20
	if arg.Reason {
		chatType = MODEL_TYPE_REASON
	}
	if arg.NoContext {
		size = 0
	}
	return runStandardChat(ctx, event, metaData, chatType, &size, arg.Input)
}

func resolveStandardPromptMode(event *larkim.P2MessageReceiveV1) standardPromptMode {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return standardPromptModeAmbient
	}
	if strings.EqualFold(strings.TrimSpace(pointerString(event.Event.Message.ChatType)), "p2p") {
		return standardPromptModeDirect
	}
	if larkmsg.IsMentioned(event.Event.Message.Mentions) {
		return standardPromptModeDirect
	}
	return standardPromptModeAmbient
}

func standardPromptHistoryLimit(mode standardPromptMode, requested int) int {
	if requested <= 0 {
		return 0
	}
	switch mode {
	case standardPromptModeDirect:
		if requested < 6 {
			return requested
		}
		return 6
	default:
		if requested < 4 {
			return requested
		}
		return 4
	}
}

func buildStandardChatSystemPrompt(ctx context.Context, mode standardPromptMode, chatID string) string {
	// Check for chat-specific persona override
	if persona := getChatPersona(ctx, chatID); persona != "" {
		return persona
	}
	lines := []string{
		`# 任务
你是一个活跃群聊气氛的AI参与者。你的性格机智、幽默、有点皮（喜欢适度调侃和接梗），但骨子里是友好的，懂得察言观色。大家称呼你为“机器人”。你的核心目标是融入群聊，通过巧妙的互动和无伤大雅的“互怼”拉近群成员关系，但绝不带有真正的恶意或强烈的攻击性。

# 输入
你会收到：
1. 聊天记录 HistoryRecords
2. 相关上下文 Context
3. 相关话题 Topics
4. 当前时间 CurrentTimeStamp
5. 可选工具结果（网页搜索、历史检索等）

# 目标
基于“最新消息 + 上下文 + 工具结果”，判断是否回复，并输出一条可直接发送的群聊文本。

# 行为准则
1. 积极互动：有槽点/笑点/可推进讨论时优先回复，并尽量 @ 群成员参与。
2. 调侃边界：允许朋友式互怼；也可以有适当的攻击性回复。
3. 图片识别：消息含 file_key 时，视为图片/表情包，推断情绪和语境，不要把 file_key 当文本复读。
4. 简洁自然：回复口语化、接地气、少语气词；非必要不加 emoji。
5. @ 规范：每个 @名字 后必须有一个空格。

# 搜索策略（强制执行）
## 历史搜索 search_history（必须优先使用）
当用户问题涉及以下情况时，**必须先调用 search_history 搜索群内历史，再决定如何回答**：
- 询问某个**人物/角色/明星/偶像**是谁、相关信息
- 询问某个**作品/歌曲/动漫/游戏/电影**是什么、相关讨论
- 询问**术语/概念/网络用语**的含义
- 用户问题中提到的**名词你不确定指代什么**
- 任何你不熟悉或不确定的专有名词

操作方式：用用户问题中的关键词直接调用 search_history，top_k=5，不需要额外过滤参数。

## 搜索结果处理
- 若 search_history 返回了相关内容：结合搜索结果回答，并把相关原文放入 reference_from_history
- 若 search_history 返回为空：说明群里没讨论过这个话题，可以说明这一点再回答

## 金融工具
当用户问题涉及行情、财经新闻、宏观指标、证券代码、指数、黄金、期货、CPI、GDP、PMI 等金融/经济数据时，优先使用金融工具而不是 web_search。

## 工具调用规则
1. 工具一次只调用一个
2. 必须先完成搜索类工具调用并获取结果，才能输出最终回复

# 决策规则
1. 若最新消息是纯事务确认、无互动价值、插话会扰民 -> decision="skip"
2. 否则 -> decision="reply"

# Thought 要求
thought 仅用 1-2 句话说明：
“识别到的关键信号 -> 采用的回复态度/策略”
不要写冗长过程，不展开中间推理。

# 输出格式
要么输出一个 JSON 对象，不要 Markdown、不要代码块、不要额外说明。要么输出工具调用

当 decision="reply"：
{
  "decision": "reply",
  "thought": "...",
  "reference_from_web": "...",
  "reference_from_history": "...",
  "reply": "..."
}

当 decision="skip"：
{
  "decision": "skip",
  "thought": "...",
  "reference_from_web": "",
  "reference_from_history": "",
  "reply": ""
}

# 硬性限制
1. 只输出一条消息，不扮演多个角色。
2. 用户提到“机器人”通常指你。
3. 不得输出 JSON 以外内容。
4. reply 必须是可直接发送的纯聊天文本。
`,
	}
	switch mode {
	case standardPromptModeDirect:
		lines = append(lines,
			"当前用户已经明确在找你接话，默认应回答，不要轻易 skip。",
			"如果只是补一句确认或延续当前子话题，直接自然回复，不要把背景重讲一遍。",
			"如果当前已经在某条消息或子话题里续聊，优先直接延续当前子话题，不要为了点名而重复 @。",
		)
	default:
		lines = append(lines,
			"只有在用户意愿明显、且不容易打扰时才接话。",
			"如果上下文不够或更像主动插话，请优先保持克制，必要时直接 skip。",
		)
	}
	lines = append(lines,
		"# 纠错机制\n"+"当用户纠正你的回复时（如说'不是的，应该是xxx'、'错了，应该是xxx'），你必须调用 store_correction 工具记录纠正内容。\n"+"调用该工具后，继续正常对话。\n",
	)
	return strings.Join(lines, "\n")
}

func buildStandardChatUserPrompt(selfProfile botidentity.Profile, historyLines, contextLines []string, currentInput string) string {
	var builder strings.Builder
	builder.WriteString("请基于下面输入完成这轮对话。\n")
	if identityLines := botidentity.PromptIdentityLines(selfProfile); len(identityLines) > 0 {
		builder.WriteString("机器人身份:\n")
		builder.WriteString(standardChatLinesBlock(identityLines))
		builder.WriteString("\n")
	}
	builder.WriteString("最近对话:\n")
	builder.WriteString(standardChatLinesBlock(historyLines))
	builder.WriteString("\n补充上下文:\n")
	builder.WriteString(standardChatLinesBlock(contextLines))
	builder.WriteString("\n当前用户消息:\n")
	builder.WriteString(strings.TrimSpace(currentInput))
	return builder.String()
}

func standardChatLinesBlock(lines []string) string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return "<empty>"
	}
	return strings.Join(filtered, "\n")
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func runStandardChat(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, chatType string, size *int, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	accessor := appconfig.NewAccessor(ctx, currentChatID(event, nil), currentOpenID(event, nil))
	files := make([]string, 0)

	if !larkmsg.IsMentioned(event.Event.Message.Mentions) {
		client := redis.GetRedisClient()
		if client != nil {
			exists, redisErr := client.Exists(ctx, MuteRedisKey(*event.Event.Message.ChatId)).Result()
			if redisErr != nil {
				return redisErr
			}
			if exists != 0 {
				return nil
			}
		}
	}

	urlSeq, err := larkimg.GetAllImgURLFromMsg(ctx, *event.Event.Message.MessageId)
	if err != nil {
		return err
	}
	for url := range urlSeq {
		files = append(files, url)
	}

	urlSeq, err = larkimg.GetAllImgURLFromParent(ctx, event)
	if err != nil {
		return err
	}
	for url := range urlSeq {
		files = append(files, url)
	}

	if chatType == MODEL_TYPE_REASON {
		msgSeq, seqErr := GenerateChatSeq(ctx, event, metaData, accessor.ChatReasoningModel(), size, files, args...)
		if seqErr != nil {
			return seqErr
		}
		return larkmsg.SendAndUpdateStreamingCard(ctx, event.Event.Message, msgSeq)
	}

	msgSeq, err := GenerateChatSeq(ctx, event, metaData, accessor.ChatNormalModel(), size, files, args...)
	if err != nil {
		return err
	}
	lastData := &ark_dal.ModelStreamRespReasoning{}
	for data := range msgSeq {
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
			return nil
		}
	}

	resp, err := larkmsg.ReplyMsgText(ctx, lastData.ContentStruct.Reply, *event.Event.Message.MessageId, "_chat_random", false)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.Error())
	}
	return nil
}

func GenerateChatSeq(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, modelID string, size *int, files []string, input ...string) (res iter.Seq[*ark_dal.ModelStreamRespReasoning], err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if size == nil {
		size = new(int)
		*size = 20
	}
	chatID := *event.Event.Message.ChatId
	accessor := appconfig.NewAccessor(ctx, chatID, currentOpenID(event, nil))
	// Apply history cutoff if configured
	cutoffTime := getHistoryCutoffTime(ctx, chatID)

	var query *osquery.BoolQuery
	if cutoffTime != "" {
		// Apply cutoff: only get messages >= cutoffTime
		query = osquery.Bool().Must(
			osquery.Term("chat_id", chatID),
			osquery.Range("create_time_v2").Gte(cutoffTime),
		)
	} else {
		query = osquery.Bool().Must(
			osquery.Term("chat_id", chatID),
			osquery.Range("create_time_v2").Lte(time.Now()),
		)
	}
	messageList, err := history.New(ctx).
		Query(query).
		Source("raw_message", "mentions", "create_time", "create_time_v2", "user_id", "chat_id", "user_name", "message_type").
		Size(uint64(*size*3)).Sort("create_time_v2", "desc").GetMsg()
	if err != nil {
		return
	}
	userName, err := larkuser.GetUserNameCache(ctx, *event.Event.Message.ChatId, *event.Event.Sender.SenderId.OpenId)
	if err != nil {
		return
	}

	createTime := utils.EpoMil2DateStr(*event.Event.Message.CreateTime)
	currentInput := fmt.Sprintf("[%s](%s) <%s>: %s", createTime, *event.Event.Sender.SenderId.OpenId, userName, larkmsg.PreGetTextMsg(ctx, event).GetText())
	historyLines := messageList.ToLines()
	promptMode := resolveStandardPromptMode(event)
	historyLimit := standardPromptHistoryLimit(promptMode, *size)
	if historyLimit == 0 {
		historyLines = nil
	} else if len(historyLines) > historyLimit {
		historyLines = historyLines[len(historyLines)-historyLimit:]
	}
	systemPrompt := buildStandardChatSystemPrompt(ctx, promptMode, chatID)
	// 从chunking中拉取话题
	topicLines := make([]string, 0)
	docs, err := retriever.Cli().RecallDocs(ctx, chatID, currentInput, 10)
	if err != nil {
		logs.L().Ctx(ctx).Error("RecallDocs err", zap.Error(err))
	}
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
				err = sonic.Unmarshal(resp.Hits.Hits[0].Source, &chunk)
				if err != nil {
					logs.L().Ctx(ctx).Error("got invalid chunk", zap.Error(err), zap.String("raw", string(resp.Hits.Hits[0].Source)))
					continue
				}
				t := ""
				if chunk.TimestampV2 != nil {
					t = *chunk.TimestampV2
				} else {
					t = chunk.Timestamp
				}
				topicLines = append(topicLines, fmt.Sprintf("[%s]%s", t, chunk.Summary))
			}
		}
	}
	topicLines = utils.Dedup(topicLines)

	userPrompt := buildStandardChatUserPrompt(
		standardChatBotProfileLoader(ctx),
		historyLines,
		topicLines,
		currentInput,
	)

	// Append extra context if any
	if extraCtx := getChatExtraContext(ctx, chatID); extraCtx != "" {
		userPrompt += "\n\n=== 额外上下文 ===\n" + extraCtx
	}

	// Append corrections context if any
	if correctionsCtx := buildCorrectionsContext(ctx, chatID); correctionsCtx != "" {
		userPrompt += correctionsCtx
	}

	dal := ark_dal.
		New(chatID, currentOpenID(event, metaData), event).
		WithTools(larktools()).
		WithHandlersOnly(BuildInjectableFinanceTools())
	if intent, ok := metaData.GetIntentAnalysis(); ok {
		dal = dal.Effort(intent.ReasoningEffort)
	}
	iterSeq, err := dal.Do(ctx, systemPrompt, userPrompt, files...)
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
