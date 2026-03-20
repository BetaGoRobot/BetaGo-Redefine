package agentruntime

import "context"

type CapabilityReplyTurnRequest struct {
	Session      *AgentSession                  `json:"-"`
	Run          *AgentRun                      `json:"-"`
	Step         *AgentStep                     `json:"-"`
	Input        CapabilityCallInput            `json:"input"`
	Result       CapabilityResult               `json:"result"`
	Recorder     InitialCapabilityTraceRecorder `json:"-"`
	PlanRecorder ReplyTurnPlanRecorder          `json:"-"`
}

type CapabilityReplyTurnResult struct {
	Executed          bool                      `json:"executed"`
	Plan              CapabilityReplyPlan       `json:"plan"`
	CapabilityCalls   []CompletedCapabilityCall `json:"capability_calls,omitempty"`
	PendingCapability *QueuedCapabilityCall     `json:"pending_capability,omitempty"`
}

type CapabilityReplyTurnExecutor interface {
	ExecuteCapabilityReplyTurn(context.Context, CapabilityReplyTurnRequest) (CapabilityReplyTurnResult, error)
}

func WithCapabilityReplyTurnExecutor(executor CapabilityReplyTurnExecutor) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.capabilityReplyTurnExecutor = executor
		}
	}
}
