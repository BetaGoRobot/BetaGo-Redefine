package capability

import (
	"context"
	"strings"
)

// ReplyPlanningRequest carries capability runtime state.
type ReplyPlanningRequest struct {
	ChatID         string `json:"chat_id,omitempty"`
	OpenID         string `json:"open_id,omitempty"`
	InputText      string `json:"input_text,omitempty"`
	CapabilityName string `json:"capability_name,omitempty"`
	Result         Result `json:"result"`
}

// ReplyPlan carries capability runtime state.
type ReplyPlan struct {
	ThoughtText string `json:"thought_text,omitempty"`
	ReplyText   string `json:"reply_text,omitempty"`
}

// ReplyPlanner defines a capability runtime contract.
type ReplyPlanner interface {
	PlanCapabilityReply(context.Context, ReplyPlanningRequest) (ReplyPlan, error)
}

// NormalizeReplyPlan implements capability runtime behavior.
func NormalizeReplyPlan(plan ReplyPlan, fallback string) ReplyPlan {
	plan.ThoughtText = strings.TrimSpace(plan.ThoughtText)
	plan.ReplyText = strings.TrimSpace(plan.ReplyText)
	if plan.ReplyText == "" {
		plan.ReplyText = strings.TrimSpace(fallback)
	}
	return plan
}
