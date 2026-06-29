package handlers

import (
	"context"
	"iter"
	"strings"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers/twophase"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intentmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mention"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	"go.uber.org/zap"
)

// GenerateChatSeqTwoPhase 两阶段聊天回复生成入口。
// 当前实现：reply/skip 与工具线索完全复用 intent 阶段的产出（intentmeta.IntentAnalysis），
// 不再额外起 Planner 模型；本函数只负责拼装上下文 + 调用 Reply Generator 流式生成。
//
// 签名与 GenerateChatSeq 保持一致，便于通过 feature flag 切换。
func GenerateChatSeqTwoPhase(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	metaData *xhandler.BaseMetaData,
	modelID string,
	size *int,
	files []string,
	input ...string,
) (res iter.Seq[*ark_dal.ModelStreamRespReasoning], err error) {
	ctx, span := otel.StartNamed(ctx, "chat.two_phase")
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if size == nil {
		size = new(int)
		*size = 20
	}
	chatID := *event.Event.Message.ChatId
	accessor := appconfig.NewAccessor(ctx, chatID, currentOpenID(event, metaData))
	cutoffTime := getHistoryCutoffTime(ctx, chatID)

	// 复用 intent 阶段产出的决策与工具线索
	intent, hasIntent := metaData.GetIntentAnalysis()
	if hasIntent && !intent.NeedReply {
		return singleSkipSeq("intent: need_reply=false"), nil
	}

	// 历史消息（与单阶段相同的逻辑）
	currentMsgThreadID := pointerString(event.Event.Message.ThreadId)
	currentMsgParentID := pointerString(event.Event.Message.ParentId)

	var query *osquery.BoolQuery
	if cutoffTime != "" {
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
		Source("raw_message", "mentions", "create_time", "create_time_v2", "user_id", "chat_id", "user_name", "message_type", "message_id", "parent_id", "root_id", "thread_id").
		Size(uint64(*size*3)).Sort("create_time_v2", "desc").GetMsg()
	if err != nil {
		return
	}

	messageList, err = expandMissingParents(ctx, messageList, accessor.LarkMsgIndex(), cutoffTime, currentMsgThreadID, currentMsgParentID)
	if err != nil {
		logs.L().Ctx(ctx).Warn("expandMissingParents error", zap.Error(err))
	}

	userName, err := larkuser.GetUserNameCache(ctx, chatID, *event.Event.Sender.SenderId.OpenId)
	if err != nil {
		return
	}

	createTime := utils.EpoMil2DateStr(*event.Event.Message.CreateTime)
	currentInput := composeChatInput(metaData, fmtTwoPhaseInput(event, userName, createTime, input...))
	historyLines := messageList.ToThreadLines()
	promptMode := resolveStandardPromptMode(event)
	modeStr := string(promptMode)
	historyLimit := standardPromptHistoryLimit(promptMode, *size)
	if historyLimit == 0 {
		historyLines = nil
	} else if len(historyLines) > historyLimit {
		historyLines = historyLines[len(historyLines)-historyLimit:]
	}

	// 话题召回
	topicLines := buildTwoPhaseTopicLines(ctx, accessor, chatID, currentInput, cutoffTime)

	extraCtx := getChatExtraContext(ctx, chatID)
	correctionsCtx := buildCorrectionsContext(ctx, chatID)
	persona := getChatPersona(ctx, chatID)

	baseScope := buildUserLLMUsageScope(ctx, chatID, metaChatName(metaData), currentOpenID(event, metaData), userName, "chat", llmusage.SourceTypeUser)

	var (
		toolHints    []intentmeta.ToolHint
		intentReason string
	)
	if hasIntent {
		intentReason = intent.Reason
	}

	// 工具计划阶段：仅在 intent 表明需要时调用，避免随便闲聊也付一次 LLM token。
	if twophase.ShouldRunToolPlanner(intent) {
		hints, planErr := twophase.PlanToolsWithContext(
			ctx,
			chatID,
			currentOpenID(event, metaData),
			accessor.IntentLiteModel(),
			currentInput,
			historyLines,
			twophase.PlannerMessageContext{
				Direct:       promptMode == standardPromptModeDirect,
				MentionedBot: event != nil && event.Event != nil && event.Event.Message != nil && larkmsg.IsMentioned(event.Event.Message.Mentions),
			},
			baseScope,
		)
		if planErr != nil {
			logs.L().Ctx(ctx).Warn("tool planner failed, fallback to no hints", zap.Error(planErr))
		} else {
			toolHints = hints
		}
	}

	span.SetAttributes(
		attribute.Bool("intent.has_analysis", hasIntent),
		attribute.Bool("tool_planner.invoked", twophase.ShouldRunToolPlanner(intent)),
		attribute.Int("tool_planner.hint_count", len(toolHints)),
		attribute.String("intent.reason", intentReason),
	)

	logs.L().Ctx(ctx).Info("two_phase planning summary",
		zap.Bool("has_intent", hasIntent),
		zap.Bool("tool_planner_invoked", twophase.ShouldRunToolPlanner(intent)),
		zap.Any("tool_hints", toolHints),
		zap.String("reason", intentReason),
	)

	// ============= Reply Generator（流式纯文本） =============
	genSysPrompt := twophase.BuildReplyGeneratorPrompt(modeStr, persona, toolHints)
	genUserPrompt := twophase.BuildGeneratorUserPrompt(
		historyLines,
		topicLines,
		currentInput,
		intentReason,
		toolHints,
		extraCtx,
		correctionsCtx,
	)

	genScope := twophase.BuildGeneratorScope(baseScope)
	dal := ark_dal.
		New(chatID, currentOpenID(event, metaData), event).
		WithTools(BuildRuntimeCapabilityTools())
	if hasIntent {
		dal = dal.Effort(intent.ReasoningEffort)
	}

	logs.L().Ctx(ctx).Info("two_phase calling generator dal")
	genStream, err := dal.Do(ctx, genScope, genSysPrompt, genUserPrompt, files...)
	if err != nil {
		return nil, err
	}

	return wrapTwoPhaseStream(ctx, genStream, intentReason, messageList, chatID), nil
}

// wrapTwoPhaseStream 包装 Reply Generator 流式输出，在流结束时组装完整 FinalResult
func wrapTwoPhaseStream(
	ctx context.Context,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	intentReason string,
	messageList history.OpensearchMsgLogList,
	chatID string,
) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		var replyBuilder strings.Builder
		var reasoningBuilder strings.Builder

		for data := range stream {
			replyBuilder.WriteString(data.Content)
			reasoningBuilder.WriteString(data.ReasoningContent)

			data.ContentStruct.Reply = replyBuilder.String()

			if !yield(data) {
				return
			}
		}

		// 流结束后规范化 @提及
		finalReply := strings.TrimSpace(replyBuilder.String())
		if normalizedReply, normalizeErr := mention.NormalizeReplyText(ctx, chatID, messageList, finalReply); normalizeErr == nil {
			finalReply = normalizedReply
		}

		decision := "reply"
		thought := intentReason
		if finalReply == "" {
			decision = "skip"
			thought = intentReason + "；回复生成为空，转为跳过"
		}

		finalData := &ark_dal.ModelStreamRespReasoning{
			Content:          "",
			ReasoningContent: reasoningBuilder.String(),
			ContentStruct: ark_dal.ContentStruct{
				Decision: decision,
				Thought:  thought,
				Reply:    finalReply,
			},
		}

		logs.L().Ctx(ctx).Info("two_phase final result",
			zap.String("decision", finalData.ContentStruct.Decision),
			zap.Int("reply_len", len([]rune(finalReply))),
		)

		_ = yield(finalData)
	}
}

// singleSkipSeq 返回只包含一条 skip 结果的迭代器
func singleSkipSeq(reason string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		yield(&ark_dal.ModelStreamRespReasoning{
			ContentStruct: ark_dal.ContentStruct{
				Decision: "skip",
				Thought:  reason,
			},
		})
	}
}

// buildTwoPhaseTopicLines 构建话题行（从向量检索 + chunk 索引中获取）
func buildTwoPhaseTopicLines(ctx context.Context, accessor *appconfig.Accessor, chatID, currentInput, cutoffTime string) []string {
	topicLines := make([]string, 0)
	docs, err := retriever.Cli().RecallDocs(ctx, chatID, currentInput, 10, cutoffTime, "")
	if err != nil {
		logs.L().Ctx(ctx).Warn("RecallDocs err", zap.Error(err))
		return topicLines
	}
	for _, doc := range docs {
		msgID, ok := doc.Metadata["msg_id"]
		if !ok {
			continue
		}
		chunkQuery := osquery.Bool().Must(osquery.Term("msg_ids", msgID))
		if cutoffTime != "" {
			chunkQuery = osquery.Bool().Must(
				osquery.Term("msg_ids", msgID),
				osquery.Range("timestamp_v2").Gte(cutoffTime),
			)
		}
		resp, searchErr := opensearch.SearchData(ctx, accessor.LarkChunkIndex(), osquery.
			Search().Sort("timestamp_v2", osquery.OrderDesc).
			Query(chunkQuery).
			Size(1),
		)
		if searchErr != nil {
			continue
		}
		chunk := &xmodel.MessageChunkLogV3{}
		if len(resp.Hits.Hits) > 0 {
			if err := sonic.Unmarshal(resp.Hits.Hits[0].Source, &chunk); err != nil {
				continue
			}
			t := ""
			if chunk.TimestampV2 != nil {
				t = *chunk.TimestampV2
			} else {
				t = chunk.Timestamp
			}
			topicLines = append(topicLines, "["+t+"]"+chunk.Summary)
		}
	}
	return utils.Dedup(topicLines)
}

// fmtTwoPhaseInput 格式化当前输入消息
func fmtTwoPhaseInput(event *larkim.P2MessageReceiveV1, userName, createTime string, input ...string) string {
	if len(input) > 0 && strings.TrimSpace(input[0]) != "" {
		return "[" + createTime + "](" + *event.Event.Sender.SenderId.OpenId + ") <" + userName + ">: " + strings.TrimSpace(input[0])
	}
	return "[" + createTime + "](" + *event.Event.Sender.SenderId.OpenId + ") <" + userName + ">: " + larkmsg.PreGetTextMsg(context.Background(), event).GetText()
}

// isTwoPhaseEnabled 检查两阶段模式是否启用
func isTwoPhaseEnabled(ctx context.Context, chatID, openID string) bool {
	return appconfig.GetManager().GetBool(ctx, appconfig.KeyTwoPhaseChat, chatID, openID)
}
