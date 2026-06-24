package twophase

import "encoding/json"

// DecisionResult 第一阶段决策器输出的结构化结果。
// 只做 reply/skip 判断和工具需求判断，不生成最终回复文本。
type DecisionResult struct {
	// Decision 决策结果："reply" 或 "skip"
	Decision string `json:"decision"`

	// ReasonSummary 一句话决策理由，格式："识别到的关键信号 -> 采用的策略"
	ReasonSummary string `json:"reason_summary"`

	// ToolPlan 需要前置执行的工具列表，按顺序执行
	ToolPlan []ToolPlanItem `json:"tool_plan"`
}

// ToolPlanItem 单个前置工具的执行计划
type ToolPlanItem struct {
	// ToolName 工具名称，如 "search_history", "finance_tool_discover", "luckin_shop_search"
	ToolName string `json:"tool_name"`

	// Args 工具参数 JSON
	Args json.RawMessage `json:"args"`

	// Reason 为什么调用这个工具，用于调试和审计
	Reason string `json:"reason,omitempty"`
}

// ToolExecutionResult 单个工具的执行结果
type ToolExecutionResult struct {
	// ToolName 工具名称
	ToolName string `json:"tool_name"`

	// Success 是否执行成功
	Success bool `json:"success"`

	// RawOutput 工具原始输出 JSON 字符串
	RawOutput string `json:"raw_output"`

	// Error 错误信息，成功时为空
	Error string `json:"error,omitempty"`

	// ReferenceFromWeb 从工具结果中提取的网页引用（如金融新闻）
	ReferenceFromWeb string `json:"reference_from_web,omitempty"`

	// ReferenceFromHistory 从工具结果中提取的历史消息引用
	ReferenceFromHistory string `json:"reference_from_history,omitempty"`

	// DirectCardSent 工具是否已直接发送了卡片消息（如瑞幸入口卡）
	// 若为 true，则第二阶段可能不需要 LLM 再生成回复
	DirectCardSent bool `json:"direct_card_sent"`
}

// FinalResult 最终组装结果，字段与现有 ContentStruct 保持兼容
type FinalResult struct {
	Decision             string `json:"decision"`
	Thought              string `json:"thought"`
	ReferenceFromWeb     string `json:"reference_from_web"`
	ReferenceFromHistory string `json:"reference_from_history"`
	Reply                string `json:"reply"`
}
