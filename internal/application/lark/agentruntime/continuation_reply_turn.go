package agentruntime

import "context"

type ContinuationReplyTurnRequest struct {
	Session                 *AgentSession                  `json:"-"`
	Run                     *AgentRun                      `json:"-"`
	Source                  ResumeSource                   `json:"source,omitempty"`
	WaitingReason           WaitingReason                  `json:"waiting_reason,omitempty"`
	PreviousStepKind        StepKind                       `json:"previous_step_kind,omitempty"`
	PreviousStepTitle       string                         `json:"previous_step_title,omitempty"`
	PreviousStepExternalRef string                         `json:"previous_step_external_ref,omitempty"`
	ResumeSummary           string                         `json:"resume_summary,omitempty"`
	ResumePayloadJSON       []byte                         `json:"resume_payload_json,omitempty"`
	ThoughtFallback         string                         `json:"thought_fallback,omitempty"`
	ReplyFallback           string                         `json:"reply_fallback,omitempty"`
	Recorder                InitialCapabilityTraceRecorder `json:"-"`
	PlanRecorder            ReplyTurnPlanRecorder          `json:"-"`
}

type ContinuationReplyTurnResult struct {
	Executed          bool                      `json:"executed"`
	Plan              CapabilityReplyPlan       `json:"plan"`
	CapabilityCalls   []CompletedCapabilityCall `json:"capability_calls,omitempty"`
	PendingCapability *QueuedCapabilityCall     `json:"pending_capability,omitempty"`
}

type ContinuationReplyTurnExecutor interface {
	ExecuteContinuationReplyTurn(context.Context, ContinuationReplyTurnRequest) (ContinuationReplyTurnResult, error)
}

func WithContinuationReplyTurnExecutor(executor ContinuationReplyTurnExecutor) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.continuationReplyTurnExecutor = executor
		}
	}
}
