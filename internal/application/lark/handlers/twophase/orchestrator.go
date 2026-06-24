package twophase

import (
	"context"
	"iter"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

// PlannerFunc 第一阶段决策函数类型
// 由 handlers 包注入具体实现（调用 ark_dal.DoSync）
type PlannerFunc func(ctx context.Context, systemPrompt, userPrompt string, files []string, scope llmusage.Scope) (*DecisionResult, error)

// GeneratorFunc 第二阶段生成函数类型
// 由 handlers 包注入具体实现（调用 ark_dal.Do 流式）
type GeneratorFunc func(ctx context.Context, systemPrompt, userPrompt string, files []string, scope llmusage.Scope) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error)

// ToolRunnerFunc 工具执行函数类型
type ToolRunnerFunc func(ctx context.Context, toolPlan []ToolPlanItem, metaData any) ([]*ToolExecutionResult, error)

// Orchestrator 两阶段编排器：串联决策 → 工具 → 生成，组装 FinalResult
type Orchestrator struct {
	planner    PlannerFunc
	generator  GeneratorFunc
	toolRunner ToolRunnerFunc
}

// NewOrchestrator 创建编排器
func NewOrchestrator(planner PlannerFunc, generator GeneratorFunc, toolRunner ToolRunnerFunc) *Orchestrator {
	return &Orchestrator{
		planner:    planner,
		generator:  generator,
		toolRunner: toolRunner,
	}
}

// OrchestratorInput 编排器输入
type OrchestratorInput struct {
	// 阶段一
	PlannerSystemPrompt string
	PlannerUserPrompt   string

	// 阶段二
	GeneratorSystemPrompt string
	GeneratorUserPromptFn func(toolResults []*ToolExecutionResult) string // 根据工具结果动态生成

	Files []string

	// LLM usage scope 基础信息
	BaseScope llmusage.Scope

	// 元数据（透传给 toolRunner）
	MetaData any
}

// Run 执行完整的两阶段流程，返回流式迭代器
// 迭代器中的每条数据：
//   - Content: 纯文本 delta（第二阶段输出）
//   - ContentStruct.Reply: 与 Content 同步更新，保证下游兼容
//   - 最后一条数据包含完整的 FinalResult（decision/thought/reference 等字段都填充完毕）
func (o *Orchestrator) Run(ctx context.Context, input OrchestratorInput) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	// Step 1: 第一阶段决策
	plannerScope := BuildPlannerScope(input.BaseScope)
	decision, err := o.planner(ctx, input.PlannerSystemPrompt, input.PlannerUserPrompt, input.Files, plannerScope)
	if err != nil {
		logs.L().Ctx(ctx).Error("planner failed, fallback to reply", zap.Error(err))
		// 决策失败也不终止，fallback 为 reply，由第二阶段兜底
		decision = &DecisionResult{
			Decision:      "reply",
			ReasonSummary: "决策阶段失败，fallback 为 reply",
			ToolPlan:      nil,
		}
	}

	logs.L().Ctx(ctx).Info("two_phase planner result",
		zap.String("decision", decision.Decision),
		zap.String("reason_summary", decision.ReasonSummary),
		zap.Int("tool_plan_count", len(decision.ToolPlan)),
	)

	// 若 decision=skip，直接返回单条 skip 结果
	if decision.Decision == "skip" {
		return singleSkipResult(decision.ReasonSummary), nil
	}

	// Step 2: 执行前置工具
	toolResults, err := o.toolRunner(ctx, decision.ToolPlan, input.MetaData)
	if err != nil {
		logs.L().Ctx(ctx).Warn("tool runner failed, continue without tools", zap.Error(err))
		toolResults = nil
	}

	// 若有工具直接发了卡片（如瑞幸入口卡），则直接返回 skip（不再生成回复）
	if HasDirectCardSent(toolResults) {
		return singleSkipResult("工具已直接发送卡片，无需生成回复"), nil
	}

	// Step 3: 第二阶段生成（流式）
	generatorScope := BuildGeneratorScope(input.BaseScope)
	generatorUserPrompt := input.GeneratorUserPromptFn(toolResults)

	stream, err := o.generator(ctx, input.GeneratorSystemPrompt, generatorUserPrompt, input.Files, generatorScope)
	if err != nil {
		return nil, err
	}

	// Step 4: 包装流，组装 FinalResult
	return o.wrapStream(ctx, stream, decision, toolResults), nil
}

// wrapStream 包装生成器的流式输出，在流结束时组装完整 FinalResult
func (o *Orchestrator) wrapStream(
	ctx context.Context,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	decision *DecisionResult,
	toolResults []*ToolExecutionResult,
) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		var replyBuilder strings.Builder
		var reasoningBuilder strings.Builder

		for data := range stream {
			replyBuilder.WriteString(data.Content)
			reasoningBuilder.WriteString(data.ReasoningContent)

			// 同步填充 ContentStruct.Reply，保证下游发送逻辑兼容
			data.ContentStruct.Reply = replyBuilder.String()

			if !yield(data) {
				return
			}
		}

		// 流结束，发送 final 数据包，包含完整的 FinalResult
		finalReply := strings.TrimSpace(replyBuilder.String())

		finalData := &ark_dal.ModelStreamRespReasoning{
			Content:          "",
			ReasoningContent: reasoningBuilder.String(),
			ContentStruct: ark_dal.ContentStruct{
				Decision:             decision.Decision,
				Thought:              decision.ReasonSummary,
				ReferenceFromWeb:     ExtractReferenceFromWeb(toolResults),
				ReferenceFromHistory: ExtractReferenceFromHistory(toolResults),
				Reply:                finalReply,
			},
		}

		// 如果回复为空，转为 skip
		if finalReply == "" {
			finalData.ContentStruct.Decision = "skip"
			finalData.ContentStruct.Thought = decision.ReasonSummary + "；回复生成为空，转为跳过"
		}

		logs.L().Ctx(ctx).Info("two_phase final result",
			zap.String("decision", finalData.ContentStruct.Decision),
			zap.Int("reply_len", len([]rune(finalReply))),
			zap.Int("tool_results_count", len(toolResults)),
		)

		_ = yield(finalData)
	}
}

// singleSkipResult 返回只包含一条 skip 结果的迭代器
func singleSkipResult(reason string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		yield(&ark_dal.ModelStreamRespReasoning{
			ContentStruct: ark_dal.ContentStruct{
				Decision: "skip",
				Thought:  reason,
			},
		})
	}
}
