package twophase

import (
	"context"
	"iter"
	"strings"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
)

// Generator 第二阶段回复生成器：流式输出纯文本回复
type Generator struct {
	// toolBuilder 构建第二阶段可用的工具集
	toolBuilder func() *ark_dalTools
}

// ark_dalTools 工具集（用接口避免循环依赖，实际由外部传入）
type ark_dalTools = any // placeholder，实际使用时由 handlers 包注入

// generatorInput 生成器输入参数
type generatorInput struct {
	SystemPrompt    string
	UserPrompt      string
	Files           []string
	Scope           llmusage.Scope
	ToolImpl        any            // *tools.Impl[larkim.P2MessageReceiveV1]
	AdditionalTools any            // *tools.Impl[larkim.P2MessageReceiveV1] 仅 handler
	Event           *larkim.P2MessageReceiveV1
	ReasoningEffort any            // responses.ReasoningEffort_Enum
}

// NewGenerator 创建回复生成器
func NewGenerator() *Generator {
	return &Generator{}
}

// Run 执行第二阶段生成，返回流式迭代器。
// 注意：此函数为占位实现，真正的流式调用在 orchestrator 中完成，
// 因为需要访问 handlers 包内的工具注册函数（避免循环依赖）。
func (g *Generator) Run(ctx context.Context, input generatorInput) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	logs.L().Ctx(ctx).Debug("generator run called",
		zap.String("scope_source", input.Scope.Source),
	)
	// 实际实现移到 orchestrator 中，通过闭包调用 ark_dal
	return nil, nil
}

// BuildGeneratorUserPrompt 构建生成器的 user prompt，包含工具结果
func BuildGeneratorUserPrompt(
	historyLines []string,
	topicLines []string,
	currentInput string,
	toolResults []*ToolExecutionResult,
	decisionSummary string,
	extraCtx string,
	correctionsCtx string,
) string {
	var builder strings.Builder
	builder.WriteString("请基于下面输入生成回复。\n")
	builder.WriteString("最近对话:\n")
	builder.WriteString(promptLinesBlock(historyLines))
	builder.WriteString("\n相关话题:\n")
	builder.WriteString(promptLinesBlock(topicLines))
	builder.WriteString("\n当前用户消息:\n")
	builder.WriteString(strings.TrimSpace(currentInput))

	if decisionSummary != "" {
		builder.WriteString("\n\n决策摘要:\n")
		builder.WriteString(decisionSummary)
	}

	// 工具结果
	if len(toolResults) > 0 {
		builder.WriteString("\n\n工具结果:\n")
		for _, r := range toolResults {
			builder.WriteString("---\n")
			builder.WriteString("工具: " + r.ToolName + "\n")
			if !r.Success {
				builder.WriteString("状态: 失败，错误: " + r.Error + "\n")
				continue
			}
			builder.WriteString("结果:\n")
			builder.WriteString(r.RawOutput + "\n")
		}
		builder.WriteString("---\n")
	}

	if extraCtx != "" {
		builder.WriteString("\n\n=== 额外上下文 ===\n" + extraCtx)
	}

	if correctionsCtx != "" {
		builder.WriteString(correctionsCtx)
	}

	return builder.String()
}

// BuildGeneratorScope 构建 LLM usage scope（标记为 generator 阶段）
func BuildGeneratorScope(base llmusage.Scope) llmusage.Scope {
	scope := base
	scope.Source = "chat_generator"
	return scope
}

// FormatToolResultsForPrompt 将工具执行结果格式化为 prompt 中的文本块
func FormatToolResultsForPrompt(results []*ToolExecutionResult) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n=== 工具检索结果 ===\n")
	for _, r := range results {
		b.WriteString("【" + r.ToolName + "】")
		if !r.Success {
			b.WriteString(" (调用失败: " + r.Error + ")\n")
			continue
		}
		b.WriteString("\n")
		// 只展示前 500 字，避免 prompt 过长
		output := r.RawOutput
		if len([]rune(output)) > 500 {
			output = string([]rune(output)[:500]) + "..."
		}
		b.WriteString(output + "\n")
	}
	return b.String()
}
