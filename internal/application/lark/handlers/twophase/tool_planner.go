package twophase

import (
	"context"
	"errors"
	"strings"

	"github.com/bytedance/gg/gptr"
	"github.com/bytedance/sonic"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	"go.uber.org/zap"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intentmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
)

// 极短 system prompt：只问该不该调三种工具，输出固定 JSON。
// 单独的工具计划阶段，避免把工具枚举塞进 intent prompt 让每条消息都付 token。
const toolPlannerSystemPrompt = `你只负责判断后续生成阶段需要调用哪些工具，不输出回复内容。

可选工具（仅以下三种）：
- search_history: 用户问到不确定的人物/作品/术语/网络用语/专有名词，或明显需要群内历史上下文。
- finance: 涉及行情、财经新闻、宏观指标、证券代码、指数、黄金、期货、CPI/GDP/PMI 等金融/经济数据。
- luckin: 用户表达想点咖啡、买咖啡、瑞幸点单、查门店或要开始点单。

规则：
- 仅在确实需要时返回；不需要则返回空数组。
- 多个并存时按上述列表顺序输出。
- 不输出解释、不输出 Markdown，只输出严格 JSON。

输出格式：
{"tool_hints": ["search_history"]}`

// ToolPlanResult 工具计划阶段的结构化结果
type ToolPlanResult struct {
	ToolHints []intentmeta.ToolHint `json:"tool_hints"`
}

// PlanTools 调用轻量模型 + 缓存的 system prompt 决定下一步要调的工具集合。
// 失败时返回 nil error 和空 hints，由调用方决定 fallback 策略。
func PlanTools(ctx context.Context, chatID, openID string, modelID, currentInput string, recentLines []string, scope llmusage.Scope) ([]intentmeta.ToolHint, error) {
	ctx, span := otel.StartNamed(ctx, "twophase.tool_planner")
	defer span.End()

	if strings.TrimSpace(currentInput) == "" {
		return nil, errors.New("empty current input")
	}
	if strings.TrimSpace(modelID) == "" {
		return nil, errors.New("empty tool planner model id")
	}

	userPrompt := buildToolPlannerUserPrompt(currentInput, recentLines)

	plannerScope := scope
	plannerScope.Source = "chat_tool_planner"

	respText, err := ark_dal.ResponseTextWithCache(ctx, ark_dal.CachedResponseRequest{
		CacheScene:   "tool_plan",
		SystemPrompt: toolPlannerSystemPrompt,
		UserPrompt:   userPrompt,
		ModelID:      modelID,
		Text: &responses.ResponsesText{
			Format: &responses.TextFormat{
				Type: responses.TextType_json_object,
			},
		},
		Reasoning: &responses.ResponsesReasoning{
			Effort: responses.ReasoningEffort_minimal,
		},
		Thinking: &responses.ResponsesThinking{
			Type: gptr.Of(responses.ThinkingType_disabled),
		},
	}, plannerScope)
	if err != nil {
		logs.L().Ctx(ctx).Warn("tool planner call failed", zap.Error(err))
		return nil, err
	}
	if strings.TrimSpace(respText) == "" {
		return nil, errors.New("empty tool planner response")
	}

	var parsed ToolPlanResult
	if err := sonic.Unmarshal([]byte(respText), &parsed); err != nil {
		logs.L().Ctx(ctx).Warn("tool planner unmarshal failed",
			zap.String("response", respText),
			zap.Error(err),
		)
		return nil, err
	}

	hints := intentmeta.SanitizeToolHints(parsed.ToolHints)
	logs.L().Ctx(ctx).Info("tool planner result",
		zap.Any("tool_hints", hints),
	)
	return hints, nil
}

// ShouldRunToolPlanner 决定是否对本次消息跑工具规划阶段。
// 规则：
// - need_reply=false：上层会直接 skip，不应调用本函数（保险起见也返回 false）
// - reply_mode=direct：用户明确点名机器人，跑工具规划
// - intent_type=question：问句通常需要查证或工具支持
// - 其它情况（chat/share/ambient 闲聊）：默认跳过工具规划
func ShouldRunToolPlanner(intent *intentmeta.IntentAnalysis) bool {
	if intent == nil || !intent.NeedReply {
		return false
	}
	if intent.ReplyMode == intentmeta.ReplyModeDirect {
		return true
	}
	if intent.IntentType == intentmeta.IntentTypeQuestion {
		return true
	}
	return false
}

func buildToolPlannerUserPrompt(currentInput string, recentLines []string) string {
	var b strings.Builder
	if len(recentLines) > 0 {
		b.WriteString("最近对话:\n")
		for _, line := range recentLines {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				b.WriteString(trimmed)
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("当前用户消息:\n")
	b.WriteString(strings.TrimSpace(currentInput))
	return b.String()
}
