package twophase

import (
	"context"
	"strings"

	jsonrepair "github.com/RealAlexandreAI/json-repair"
	"github.com/bytedance/sonic"
	"go.uber.org/zap"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
)

// Planner 第一阶段决策器：非流式调用 LLM，输出 DecisionResult JSON
type Planner struct{}

// NewPlanner 创建决策器
func NewPlanner() *Planner {
	return &Planner{}
}

// PlannerInput 决策器的输入参数
type PlannerInput struct {
	SystemPrompt string
	UserPrompt   string
	Files        []string
	Scope        llmusage.Scope
}

// Run 执行第一阶段决策
// 返回 DecisionResult。若解析失败，返回 fallback 结果（decision=reply，空 tool_plan）。
func (p *Planner) Run(ctx context.Context, input PlannerInput) (*DecisionResult, error) {
	// 第一次调用
	result, err := p.callAndParse(ctx, input)
	if err == nil {
		return result, nil
	}

	logs.L().Ctx(ctx).Warn("planner first call parse failed, retry with jsonrepair",
		zap.Error(err),
	)

	// 解析失败，用 jsonrepair 修复原始输出再试（callAndParse 内部已尝试一次 repair）
	// 这里重试一次 LLM 调用，附加格式提醒
	retryPrompt := input.SystemPrompt + "\n\n# 重要提醒\n你上次输出可能不是合法 JSON。请严格只输出一个 JSON 对象，不要 Markdown、不要代码块、不要额外文字。"
	retryInput := PlannerInput{
		SystemPrompt: retryPrompt,
		UserPrompt:   input.UserPrompt,
		Files:        input.Files,
		Scope:        input.Scope,
	}

	result, err2 := p.callAndParse(ctx, retryInput)
	if err2 == nil {
		return result, nil
	}

	logs.L().Ctx(ctx).Error("planner retry also failed, using fallback",
		zap.Error(err2),
	)

	// 最终兜底：偏向 reply，由第二阶段兜底生成
	return &DecisionResult{
		Decision:      "reply",
		ReasonSummary: "决策阶段解析失败，fallback 为 reply",
		ToolPlan:      nil,
	}, nil
}

// callAndParse 单次调用 + JSON 解析（含 jsonrepair 自动修复）
func (p *Planner) callAndParse(ctx context.Context, input PlannerInput) (*DecisionResult, error) {
	dal := ark_dal.New[any](input.Scope.ChatID, input.Scope.OpenID, nil)

	content, err := dal.DoSync(ctx, input.Scope, input.SystemPrompt, input.UserPrompt, input.Files...)
	if err != nil {
		return nil, err
	}

	// 尝试直接解析
	var result DecisionResult
	if parseErr := sonic.UnmarshalString(content, &result); parseErr == nil {
		return &result, nil
	}

	// 用 jsonrepair 修复后重试
	repaired, repairErr := jsonrepair.RepairJSON(content)
	if repairErr != nil {
		return nil, repairErr
	}

	if parseErr := sonic.UnmarshalString(repaired, &result); parseErr != nil {
		return nil, parseErr
	}

	return &result, nil
}

// BuildPlannerUserPrompt 构建决策器的 user prompt
// 复用与第二阶段相同的上下文格式，确保决策有足够信息
func BuildPlannerUserPrompt(
	selfProfile botidentity.Profile,
	historyLines []string,
	topicLines []string,
	currentInput string,
	extraCtx string,
) string {
	var builder strings.Builder
	builder.WriteString("请基于下面输入完成决策。\n")
	if identityLines := botidentity.PromptIdentityLines(selfProfile); len(identityLines) > 0 {
		builder.WriteString("机器人身份:\n")
		builder.WriteString(promptLinesBlock(identityLines))
		builder.WriteString("\n")
	}
	builder.WriteString("最近对话:\n")
	builder.WriteString(promptLinesBlock(historyLines))
	builder.WriteString("\n相关话题:\n")
	builder.WriteString(promptLinesBlock(topicLines))
	builder.WriteString("\n当前用户消息:\n")
	builder.WriteString(strings.TrimSpace(currentInput))

	if extraCtx != "" {
		builder.WriteString("\n\n=== 额外上下文 ===\n" + extraCtx)
	}

	return builder.String()
}

// BuildPlannerScope 构建 LLM usage scope（标记为 planner 阶段）
func BuildPlannerScope(base llmusage.Scope) llmusage.Scope {
	scope := base
	scope.Source = "chat_planner"
	return scope
}

// GetTwoPhaseEnabled 从配置读取两阶段模式开关
func GetTwoPhaseEnabled(ctx context.Context, chatID, openID string) bool {
	return appconfig.GetManager().GetBool(ctx, appconfig.KeyTwoPhaseChat, chatID, openID)
}

// promptLinesBlock 格式化多行文本块
func promptLinesBlock(lines []string) string {
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
