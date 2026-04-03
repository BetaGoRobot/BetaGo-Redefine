package handlers

import arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"

type FinanceToolDiscoverArgs struct {
	Category  string   `json:"category"`
	ToolNames []string `json:"tool_names"`
	Limit     int      `json:"limit"`
}

type FinanceToolDiscoverResult struct {
	Tools []FinanceToolDiscoverItem `json:"tools"`
}

type FinanceToolDiscoverItem struct {
	ToolName    string          `json:"tool_name"`
	Description string          `json:"description"`
	Schema      *arktools.Param `json:"schema,omitempty"`
	Required    []string        `json:"required,omitempty"`
	Examples    []string        `json:"examples,omitempty"`
	Categories  []string        `json:"categories,omitempty"`
}
