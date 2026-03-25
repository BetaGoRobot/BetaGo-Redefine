package agentruntime

import (
	"encoding/json"
	"strings"
	"time"

	capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"
)

func newCapabilityObserveStep(runID string, index int, capabilityName string, result CapabilityResult, observedAt time.Time) (*AgentStep, error) {
	output, err := json.Marshal(capabilityObservation{
		CapabilityName: strings.TrimSpace(capabilityName),
		OutputText:     result.OutputText,
		OutputJSON:     json.RawMessage(result.OutputJSON),
		ExternalRef:    strings.TrimSpace(result.ExternalRef),
		OccurredAt:     observedAt,
	})
	if err != nil {
		return nil, err
	}

	return &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       runID,
		Index:       index,
		Kind:        StepKindObserve,
		Status:      StepStatusCompleted,
		OutputJSON:  output,
		ExternalRef: strings.TrimSpace(result.ExternalRef),
		CreatedAt:   observedAt,
		StartedAt:   &observedAt,
		FinishedAt:  &observedAt,
	}, nil
}

func newCapabilityReplyStep(runID string, index int, capabilityName string, plan CapabilityReplyPlan, refs ReplyEmissionResult, observedAt time.Time) (*AgentStep, error) {
	output, err := json.Marshal(capabilityReply{
		CapabilityName:    strings.TrimSpace(capabilityName),
		ThoughtText:       strings.TrimSpace(plan.ThoughtText),
		ReplyText:         strings.TrimSpace(plan.ReplyText),
		Text:              strings.TrimSpace(plan.ReplyText),
		ResponseMessageID: strings.TrimSpace(refs.MessageID),
		ResponseCardID:    strings.TrimSpace(refs.CardID),
		DeliveryMode:      refs.DeliveryMode,
		LifecycleState:    ReplyLifecycleStateActive,
		TargetMessageID:   strings.TrimSpace(refs.TargetMessageID),
		TargetCardID:      strings.TrimSpace(refs.TargetCardID),
		TargetStepID:      strings.TrimSpace(refs.TargetStepID),
	})
	if err != nil {
		return nil, err
	}

	externalRef := strings.TrimSpace(refs.CardID)
	if externalRef == "" {
		externalRef = strings.TrimSpace(refs.MessageID)
	}

	return &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       runID,
		Index:       index,
		Kind:        StepKindReply,
		Status:      StepStatusCompleted,
		OutputJSON:  output,
		ExternalRef: externalRef,
		CreatedAt:   observedAt,
		StartedAt:   &observedAt,
		FinishedAt:  &observedAt,
	}, nil
}

func newContinuationReplyStep(runID string, index int, thoughtText, replyText string, refs ReplyEmissionResult, observedAt time.Time) (*AgentStep, error) {
	output, err := json.Marshal(capabilityReply{
		ThoughtText:       strings.TrimSpace(thoughtText),
		ReplyText:         strings.TrimSpace(replyText),
		Text:              strings.TrimSpace(replyText),
		ResponseMessageID: strings.TrimSpace(refs.MessageID),
		ResponseCardID:    strings.TrimSpace(refs.CardID),
		DeliveryMode:      refs.DeliveryMode,
		LifecycleState:    ReplyLifecycleStateActive,
		TargetMessageID:   strings.TrimSpace(refs.TargetMessageID),
		TargetCardID:      strings.TrimSpace(refs.TargetCardID),
		TargetStepID:      strings.TrimSpace(refs.TargetStepID),
	})
	if err != nil {
		return nil, err
	}

	externalRef := strings.TrimSpace(refs.CardID)
	if externalRef == "" {
		externalRef = strings.TrimSpace(refs.MessageID)
	}

	return &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       runID,
		Index:       index,
		Kind:        StepKindReply,
		Status:      StepStatusCompleted,
		OutputJSON:  output,
		ExternalRef: externalRef,
		CreatedAt:   observedAt,
		StartedAt:   &observedAt,
		FinishedAt:  &observedAt,
	}, nil
}

func hydrateQueuedCapabilityCall(call *QueuedCapabilityCall, run *AgentRun, request CapabilityRequest) *QueuedCapabilityCall {
	if call == nil {
		return nil
	}

	copied := *call
	copied.Input = call.Input
	copied.Input.Request = call.Input.Request

	if strings.TrimSpace(copied.Input.Request.SessionID) == "" && run != nil {
		copied.Input.Request.SessionID = run.SessionID
	}
	if strings.TrimSpace(copied.Input.Request.RunID) == "" && run != nil {
		copied.Input.Request.RunID = run.ID
	}
	if strings.TrimSpace(string(copied.Input.Request.Scope)) == "" {
		copied.Input.Request.Scope = request.Scope
	}
	if strings.TrimSpace(copied.Input.Request.ChatID) == "" {
		copied.Input.Request.ChatID = request.ChatID
	}
	if strings.TrimSpace(copied.Input.Request.ActorOpenID) == "" {
		copied.Input.Request.ActorOpenID = coalesceString(request.ActorOpenID, func() string {
			if run == nil {
				return ""
			}
			return run.ActorOpenID
		}())
	}
	if strings.TrimSpace(copied.Input.Request.InputText) == "" {
		copied.Input.Request.InputText = coalesceString(request.InputText, func() string {
			if run == nil {
				return ""
			}
			return run.InputText
		}())
	}
	copied.Input.QueueTail = hydrateQueuedCapabilityQueue(copied.Input.QueueTail, run, request)
	return &copied
}

func hydrateQueuedCapabilityQueue(queue []QueuedCapabilityCall, run *AgentRun, request CapabilityRequest) []QueuedCapabilityCall {
	if len(queue) == 0 {
		return nil
	}
	result := make([]QueuedCapabilityCall, 0, len(queue))
	for _, item := range queue {
		call := hydrateQueuedCapabilityCall(&item, run, request)
		if call == nil {
			continue
		}
		result = append(result, *call)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func mergeQueuedCapabilityQueue(head []QueuedCapabilityCall, tail []QueuedCapabilityCall) []QueuedCapabilityCall {
	if len(head) == 0 && len(tail) == 0 {
		return nil
	}
	merged := make([]QueuedCapabilityCall, 0, len(head)+len(tail))
	for _, item := range head {
		merged = append(merged, cloneQueuedCapabilityCall(item))
	}
	for _, item := range tail {
		merged = append(merged, cloneQueuedCapabilityCall(item))
	}
	return merged
}

func cloneQueuedCapabilityCall(src QueuedCapabilityCall) QueuedCapabilityCall {
	copied := src
	copied.Input = src.Input
	copied.Input.Request = src.Input.Request
	if src.Input.Approval != nil {
		approval := *src.Input.Approval
		copied.Input.Approval = &approval
	}
	if src.Input.Continuation != nil {
		continuation := *src.Input.Continuation
		copied.Input.Continuation = &continuation
	}
	if len(src.Input.Request.PayloadJSON) > 0 {
		copied.Input.Request.PayloadJSON = append([]byte(nil), src.Input.Request.PayloadJSON...)
	}
	if len(src.Input.QueueTail) > 0 {
		copied.Input.QueueTail = mergeQueuedCapabilityQueue(nil, src.Input.QueueTail)
	}
	return copied
}

func resolveCapabilityResultSummary(capabilityName string, result CapabilityResult) string {
	return capdef.ResolveResultSummary(capabilityName, result)
}

func normalizeObservedAt(observedAt time.Time) time.Time {
	if observedAt.IsZero() {
		return time.Now().UTC()
	}
	return observedAt.UTC()
}
