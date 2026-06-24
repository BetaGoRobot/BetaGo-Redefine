package twophase

import "context"

// ToolRunner 后端工具执行器：根据 DecisionResult.ToolPlan 调用前置工具
type ToolRunner struct{}

// NewToolRunner 创建工具执行器
func NewToolRunner() *ToolRunner {
	return &ToolRunner{}
}

// Run 执行所有前置工具，返回执行结果列表。
// 单个工具失败不终止流程，错误记录在 ToolExecutionResult.Error 中。
//
// Phase 1 阶段：空实现，tool_plan 暂不执行，工具仍由第二阶段 LLM 自主调用。
// Phase 2 阶段：接入 search_history / finance 等前置工具。
func (r *ToolRunner) Run(ctx context.Context, toolPlan []ToolPlanItem, metaData any) ([]*ToolExecutionResult, error) {
	if len(toolPlan) == 0 {
		return nil, nil
	}
	// Phase 1: 暂不执行，返回空结果
	// TODO(Phase 2): 实现真实的工具执行逻辑
	return nil, nil
}

// ExtractReferenceFromHistory 从历史搜索结果中提取 reference_from_history
func ExtractReferenceFromHistory(results []*ToolExecutionResult) string {
	for _, r := range results {
		if r == nil {
			continue
		}
		if r.ReferenceFromHistory != "" {
			return r.ReferenceFromHistory
		}
	}
	return ""
}

// ExtractReferenceFromWeb 从工具结果中提取 reference_from_web
func ExtractReferenceFromWeb(results []*ToolExecutionResult) string {
	for _, r := range results {
		if r == nil {
			continue
		}
		if r.ReferenceFromWeb != "" {
			return r.ReferenceFromWeb
		}
	}
	return ""
}

// HasDirectCardSent 检查是否有工具已经直接发送了卡片（如瑞幸入口卡）
// 若为 true，可能不需要 LLM 再生成回复文本
func HasDirectCardSent(results []*ToolExecutionResult) bool {
	for _, r := range results {
		if r != nil && r.DirectCardSent {
			return true
		}
	}
	return false
}
