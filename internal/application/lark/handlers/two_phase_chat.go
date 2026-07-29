package handlers

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
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
	anchorAt, err := eventAnchorTime(event)
	if err != nil {
		return nil, err
	}
	accessor := appconfig.NewAccessor(ctx, chatID, currentOpenID(event, metaData))
	captureEnabled := conversationeval.CaptureEnabled(ctx)
	cutoffTime := getHistoryCutoffTime(ctx, chatID)

	// 复用 intent 阶段产出的决策与工具线索
	intent, hasIntent := metaData.GetIntentAnalysis()
	if hasIntent && !intent.NeedReply {
		if captureEnabled {
			recordStandardChatPlan(ctx, event, StandardChatPlan{
				ModelID: modelID,
				Files:   append([]string(nil), files...),
			})
		}
		return singleSkipSeq("intent: need_reply=false"), nil
	}

	// 历史消息（与单阶段相同的逻辑）
	currentMsgThreadID := pointerString(event.Event.Message.ThreadId)
	currentMsgParentID := pointerString(event.Event.Message.ParentId)

	query := buildHistoryQuery(chatID, cutoffTime, anchorAt)
	messageList, err := history.New(ctx).
		Query(query).
		Source("raw_message", "mentions", "create_time", "create_time_v2", "user_id", "chat_id", "user_name", "message_type", "message_id", "parent_id", "root_id", "thread_id").
		Size(uint64(*size*3)).Sort("create_time_v2", "desc").GetMsg()
	if err != nil {
		return
	}

	messageList, err = expandMissingParents(ctx, messageList, accessor.LarkMsgIndex(), cutoffTime, anchorAt, currentMsgThreadID, currentMsgParentID)
	if err != nil {
		logs.L().Ctx(ctx).Warn("expandMissingParents error", zap.Error(err))
	}
	messageList, droppedHistory := filterHistoryAtAnchor(messageList, anchorAt)
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
	var (
		historyItems           []conversationeval.ContextItem
		excludedItems          []conversationeval.ExcludedContextItem
		historyDegradedSources []string
	)
	runCaptureBuild(ctx, func() {
		historyItems, excludedItems = captureHistoryPrompt(messageList, historyLines, historyLimit == 0)
		causalExcluded, causalDegraded := captureDroppedHistory(droppedHistory, anchorAt)
		excludedItems = append(excludedItems, causalExcluded...)
		historyDegradedSources = append(historyDegradedSources, causalDegraded...)
	})

	// 话题召回
	topicLines, retrievedItems, retrievedExcluded, degradedSources := buildTwoPhaseTopicContext(
		ctx, accessor, chatID, currentInput, cutoffTime, anchorAt, captureEnabled,
	)
	excludedItems = append(excludedItems, retrievedExcluded...)
	degradedSources = append(historyDegradedSources, degradedSources...)

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
			recordTwoPhaseToolHints(ctx, toolHints)
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
	if captureEnabled {
		recordStandardChatPlan(ctx, event, StandardChatPlan{
			ModelID:         modelID,
			SystemPrompt:    genSysPrompt,
			UserPrompt:      genUserPrompt,
			HistoryItems:    historyItems,
			RetrievedItems:  retrievedItems,
			ExcludedItems:   excludedItems,
			DegradedSources: degradedSources,
			Files:           append([]string(nil), files...),
			chatID:          chatID,
			openID:          currentOpenID(event, metaData),
			userName:        userName,
			messageList:     messageList,
		})
	}

	genScope := twophase.BuildGeneratorScope(baseScope)
	dal := ark_dal.
		New(chatID, currentOpenID(event, metaData), event).
		WithModelID(modelID).
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

func recordTwoPhaseToolHints(ctx context.Context, hints []intentmeta.ToolHint) {
	if !conversationeval.CaptureEnabled(ctx) {
		return
	}
	capture := conversationeval.FromContext(ctx)
	for _, hint := range hints {
		capture.RecordToolPlan(ctx, conversationeval.ToolTrace{
			Name:         string(hint),
			OutputSource: conversationeval.ToolOutputSourcePlanner,
		})
	}
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
	lines, _, _, _ := buildTwoPhaseTopicContext(ctx, accessor, chatID, currentInput, cutoffTime, time.Now(), false)
	return lines
}

func buildTwoPhaseTopicContext(
	ctx context.Context,
	accessor *appconfig.Accessor,
	chatID, currentInput, cutoffTime string,
	anchorAt time.Time,
	captureEnabled bool,
) (
	[]string,
	[]conversationeval.ContextItem,
	[]conversationeval.ExcludedContextItem,
	[]string,
) {
	topicLines := make([]string, 0)
	retrievedItems := make([]conversationeval.ContextItem, 0)
	excludedItems := make([]conversationeval.ExcludedContextItem, 0)
	degradedSources := make([]string, 0)
	docs, err := retriever.Cli().RecallDocs(ctx, chatID, currentInput, 10, cutoffTime, retrievalAnchorEnd(anchorAt))
	if err != nil {
		logs.L().Ctx(ctx).Warn("RecallDocs err", zap.Error(err))
		if captureEnabled {
			return topicLines, retrievedItems, excludedItems, []string{conversationeval.ContextSourceRetrieved}
		}
		return topicLines, nil, nil, nil
	}
	for docIndex, doc := range docs {
		rawMsgID, ok := doc.Metadata["msg_id"]
		msgID := strings.TrimSpace(fmt.Sprint(rawMsgID))
		if !ok || msgID == "" {
			if captureEnabled {
				item := newContextItem(
					conversationeval.ContextSourceRetrieved,
					fmt.Sprintf("document-%d", docIndex+1),
					conversationeval.ContextKindChunk,
					doc.PageContent,
					docIndex+1,
					time.UnixMilli(1).UTC(),
					nil,
				)
				item.Score = float64(doc.Score)
				excludedItems = append(excludedItems, excludedContextItem(item, excludeReasonMissingMsgID))
			}
			continue
		}
		chunkQuery := buildChunkQuery(msgID, cutoffTime, anchorAt)
		resp, searchErr := opensearch.SearchData(ctx, accessor.LarkChunkIndex(), osquery.
			Search().Sort("timestamp_v2", osquery.OrderDesc).
			Query(chunkQuery).
			Size(1),
		)
		if searchErr != nil {
			if captureEnabled {
				item := newContextItem(
					conversationeval.ContextSourceRetrieved,
					fmt.Sprintf("%s-missing-%d", msgID, docIndex+1),
					conversationeval.ContextKindChunk,
					doc.PageContent,
					docIndex+1,
					time.UnixMilli(1).UTC(),
					map[string]string{"message_id": msgID},
				)
				excludedItems = append(excludedItems, excludedContextItem(item, excludeReasonChunkMissing))
			}
			continue
		}
		chunk := &xmodel.MessageChunkLogV3{}
		if len(resp.Hits.Hits) > 0 {
			if err := sonic.Unmarshal(resp.Hits.Hits[0].Source, &chunk); err != nil {
				if captureEnabled {
					item := newContextItem(
						conversationeval.ContextSourceRetrieved,
						fmt.Sprintf("%s-invalid-%d", msgID, docIndex+1),
						conversationeval.ContextKindChunk,
						doc.PageContent,
						docIndex+1,
						time.UnixMilli(1).UTC(),
						map[string]string{"message_id": msgID},
					)
					excludedItems = append(excludedItems, excludedContextItem(item, excludeReasonChunkInvalid))
				}
				continue
			}
			t := ""
			if chunk.TimestampV2 != nil {
				t = *chunk.TimestampV2
			} else {
				t = chunk.Timestamp
			}
			topicLine := "[" + t + "]" + chunk.Summary
			sourceID := strings.TrimSpace(chunk.ID)
			if sourceID == "" {
				sourceID = msgID
			}
			occurredAt, causalReason := parseCausalContextTime(t, anchorAt)
			if causalReason != "" {
				if captureEnabled {
					excluded, degraded := captureDroppedRetrieved(
						sourceID, msgID, topicLine, t, causalReason,
						docIndex+1, float64(doc.Score), anchorAt,
					)
					excludedItems = append(excludedItems, excluded)
					degradedSources = append(degradedSources, degraded)
				}
				continue
			}
			topicLines = append(topicLines, topicLine)
			if captureEnabled {
				item := newContextItem(
					conversationeval.ContextSourceRetrieved,
					sourceID,
					conversationeval.ContextKindChunk,
					topicLine,
					docIndex+1,
					occurredAt,
					map[string]string{"message_id": msgID},
				)
				item.Score = float64(doc.Score)
				retrievedItems = append(retrievedItems, item)
			}
		} else if captureEnabled {
			item := newContextItem(
				conversationeval.ContextSourceRetrieved,
				fmt.Sprintf("%s-missing-%d", msgID, docIndex+1),
				conversationeval.ContextKindChunk,
				doc.PageContent,
				docIndex+1,
				time.UnixMilli(1).UTC(),
				map[string]string{"message_id": msgID},
			)
			excludedItems = append(excludedItems, excludedContextItem(item, excludeReasonChunkMissing))
		}
	}
	deduplicated := utils.Dedup(topicLines)
	if captureEnabled && len(deduplicated) != len(topicLines) {
		selected := make([]conversationeval.ContextItem, 0, len(deduplicated))
		seen := make(map[string]struct{}, len(deduplicated))
		for index, item := range retrievedItems {
			if _, exists := seen[item.Content]; exists {
				item.SourceID = fmt.Sprintf("%s-duplicate-%d", item.SourceID, index+1)
				item.ID = item.Source + ":" + item.SourceID
				excludedItems = append(excludedItems, excludedContextItem(item, excludeReasonDeduplicated))
				continue
			}
			seen[item.Content] = struct{}{}
			item.Rank = len(selected) + 1
			selected = append(selected, item)
		}
		retrievedItems = selected
	}
	return deduplicated, retrievedItems, excludedItems, degradedSources
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
