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

// system prompt 走 ARK prefix cache，需要 > 256 input tokens 才能命中前缀缓存，
// 因此略微补充工具边界与 few-shot，既过阈值也提升判别准确率。
const toolPlannerSystemPrompt = `你只负责判断后续生成阶段需要调用哪些工具，不输出任何回复正文，不复述用户消息。

可选工具（仅以下三种，名称必须完全一致）：
- search_history: 用户问不确定的人物/作品/节目/术语/网络梗/专有名词，或显式需要群内历史上下文（"之前""上次""群里说过"），或要求"查一下/搜一下"。
- finance: 行情、K线、估值、财报；股票/基金/ETF/指数/期货/外汇/数字货币；证券代码（如 600519、AAPL）；CPI/PPI/GDP/PMI/利率/汇率；黄金、原油等大宗商品；财经新闻或政策对市场影响。
- luckin: 明确想点咖啡/奶茶、瑞幸下单、看菜单、查门店、查订单、加购、改规格、用券。

规则：
- 仅在确实需要时返回；多个并存按上述顺序输出，不重复。
- 闲聊/问候/情绪表达/写作润色/翻译/数学计算/纯主观偏好 → 返回空数组。
- 不输出解释、思考过程或 Markdown，只输出严格 JSON 对象，字段仅 tool_hints。

示例：
- "贵州茅台今天怎么走" → {"tool_hints":["finance"]}
- "来杯生椰拿铁" → {"tool_hints":["luckin"]}
- "你好呀" → {"tool_hints":[]}
- "先查下上次提的那只基金最近表现" → {"tool_hints":["search_history","finance"]}

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
