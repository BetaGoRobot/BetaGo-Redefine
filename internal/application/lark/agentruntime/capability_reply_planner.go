package agentruntime

import (
	"context"
	"strings"
)

type CapabilityReplyPlanningRequest struct {
	Session        *AgentSession    `json:"-"`
	Run            *AgentRun        `json:"-"`
	Step           *AgentStep       `json:"-"`
	CapabilityName string           `json:"capability_name,omitempty"`
	Result         CapabilityResult `json:"result"`
}

type CapabilityReplyPlan struct {
	ThoughtText string `json:"thought_text,omitempty"`
	ReplyText   string `json:"reply_text,omitempty"`
}

type CapabilityReplyPlanner interface {
	PlanCapabilityReply(context.Context, CapabilityReplyPlanningRequest) (CapabilityReplyPlan, error)
}

func WithCapabilityReplyPlanner(planner CapabilityReplyPlanner) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.capabilityReplyPlanner = planner
		}
	}
}

func normalizeCapabilityReplyPlan(plan CapabilityReplyPlan, fallback string) CapabilityReplyPlan {
	plan.ThoughtText = strings.TrimSpace(plan.ThoughtText)
	plan.ReplyText = strings.TrimSpace(plan.ReplyText)
	if plan.ReplyText == "" {
		plan.ReplyText = strings.TrimSpace(fallback)
	}
	return plan
}
