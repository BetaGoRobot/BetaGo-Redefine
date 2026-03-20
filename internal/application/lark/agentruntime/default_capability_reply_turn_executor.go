package agentruntime

import (
	"context"
	"iter"
	"strings"

	jsonrepair "github.com/RealAlexandreAI/json-repair"
	"github.com/bytedance/sonic"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
)

type defaultCapabilityReplyTurnExecutor struct {
	selectModel func(context.Context, string, string) capabilityReplyPlannerModelSelection
}

func NewDefaultCapabilityReplyTurnExecutor() CapabilityReplyTurnExecutor {
	return &defaultCapabilityReplyTurnExecutor{
		selectModel: defaultCapabilityReplyPlannerSelectModel,
	}
}

func (e *defaultCapabilityReplyTurnExecutor) ExecuteCapabilityReplyTurn(ctx context.Context, req CapabilityReplyTurnRequest) (CapabilityReplyTurnResult, error) {
	if req.Input.Continuation == nil {
		return CapabilityReplyTurnResult{}, nil
	}
	previousResponseID := strings.TrimSpace(req.Input.Continuation.PreviousResponseID)
	if previousResponseID == "" {
		return CapabilityReplyTurnResult{}, nil
	}
	if req.Session == nil || req.Run == nil || req.Step == nil {
		return CapabilityReplyTurnResult{}, nil
	}

	chatID := strings.TrimSpace(req.Session.ChatID)
	openID := strings.TrimSpace(req.Run.ActorOpenID)
	callID := strings.TrimSpace(req.Step.ExternalRef)
	if chatID == "" || openID == "" || callID == "" {
		return CapabilityReplyTurnResult{}, nil
	}

	tools := decorateRuntimeChatTools(defaultChatToolProvider())
	if tools == nil {
		return CapabilityReplyTurnResult{}, nil
	}
	registry := buildInitialChatCapabilityRegistry(nil, tools)

	selectModel := defaultCapabilityReplyPlannerSelectModel
	if e != nil && e.selectModel != nil {
		selectModel = e.selectModel
	}
	modelID := strings.TrimSpace(selectModel(ctx, chatID, openID).ModelID)
	if modelID == "" {
		return CapabilityReplyTurnResult{}, nil
	}

	baseRequest := req.Input.Request
	baseRequest.ChatID = coalesceString(baseRequest.ChatID, chatID)
	baseRequest.ActorOpenID = coalesceString(baseRequest.ActorOpenID, openID)
	if req.Run != nil {
		baseRequest.InputText = coalesceString(baseRequest.InputText, req.Run.InputText)
	}

	result := CapabilityReplyTurnResult{}
	turnReq := InitialChatTurnRequest{
		Plan: InitialChatExecutionPlan{
			ModelID: modelID,
			ChatID:  chatID,
			OpenID:  openID,
			Tools:   tools,
		},
		PreviousResponseID: previousResponseID,
		ToolOutput: &InitialChatToolOutput{
			CallID: callID,
			Output: resolveCapabilityReplyTurnToolOutput(req.Step.CapabilityName, req.Result),
		},
	}

	for turn := 0; turn < defaultInitialChatToolTurns; turn++ {
		turnResult, err := defaultInitialChatTurnExecutor(ctx, turnReq)
		if err != nil {
			return CapabilityReplyTurnResult{}, nil
		}

		result.Plan = collectCapabilityReplyTurnPlan(turnResult.Stream)
		snapshot := turnResult.Snapshot()
		if snapshot.ToolCall == nil {
			result.Executed = strings.TrimSpace(result.Plan.ThoughtText) != "" ||
				strings.TrimSpace(result.Plan.ReplyText) != "" ||
				result.PendingCapability != nil
			return result, nil
		}
		if req.PlanRecorder != nil {
			if err := req.PlanRecorder.RecordReplyTurnPlan(ctx, result.Plan, snapshot.ToolCall); err != nil {
				return CapabilityReplyTurnResult{}, err
			}
		}

		execution := executeChatToolCall(ctx, registry, baseRequest, *snapshot.ToolCall)
		if execution.Trace != nil {
			traceCopy := *execution.Trace
			traceCopy.PreviousResponseID = strings.TrimSpace(snapshot.ResponseID)
			if traceCopy.Pending {
				appendQueuedPendingCapability(&result.PendingCapability, buildQueuedCapabilityCallFromTrace(baseRequest, traceCopy))
			} else {
				call := CompletedCapabilityCall{
					CallID:             traceCopy.CallID,
					CapabilityName:     traceCopy.FunctionName,
					Arguments:          traceCopy.Arguments,
					Output:             traceCopy.Output,
					PreviousResponseID: traceCopy.PreviousResponseID,
				}
				if req.Recorder != nil {
					if err := req.Recorder.RecordCompletedCapabilityCall(ctx, call); err != nil {
						return CapabilityReplyTurnResult{}, err
					}
				} else {
					result.CapabilityCalls = append(result.CapabilityCalls, call)
				}
			}
		}
		if execution.NextOutput == nil || strings.TrimSpace(snapshot.ResponseID) == "" {
			return CapabilityReplyTurnResult{}, nil
		}

		turnReq = InitialChatTurnRequest{
			Plan:               turnReq.Plan,
			PreviousResponseID: strings.TrimSpace(snapshot.ResponseID),
			ToolOutput:         execution.NextOutput,
		}
	}

	return CapabilityReplyTurnResult{}, nil
}

func resolveCapabilityReplyTurnToolOutput(capabilityName string, result CapabilityResult) string {
	if text := strings.TrimSpace(result.OutputText); text != "" {
		return text
	}
	if raw := strings.TrimSpace(string(result.OutputJSON)); raw != "" {
		return raw
	}
	return resolveCapabilityResultSummary(capabilityName, result)
}

func collectCapabilityReplyTurnPlan(stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) CapabilityReplyPlan {
	var (
		thoughtBuilder strings.Builder
		rawContent     strings.Builder
		plan           CapabilityReplyPlan
	)
	for item := range stream {
		if item == nil {
			continue
		}
		if item.ReasoningContent != "" {
			thoughtBuilder.WriteString(item.ReasoningContent)
			if strings.TrimSpace(plan.ThoughtText) == "" {
				plan.ThoughtText = strings.TrimSpace(thoughtBuilder.String())
			}
		}
		if thought := strings.TrimSpace(item.ContentStruct.Thought); thought != "" {
			plan.ThoughtText = thought
		}
		if item.Content != "" {
			rawContent.WriteString(item.Content)
		}
		if reply := strings.TrimSpace(item.ContentStruct.Reply); reply != "" {
			plan.ReplyText = reply
		}
	}

	parsed := parseCapabilityReplyContent(strings.TrimSpace(rawContent.String()))
	if strings.TrimSpace(plan.ThoughtText) == "" {
		plan.ThoughtText = strings.TrimSpace(parsed.Thought)
	}
	if strings.TrimSpace(plan.ReplyText) == "" {
		plan.ReplyText = strings.TrimSpace(parsed.Reply)
	}
	return plan
}

func appendQueuedPendingCapability(root **QueuedCapabilityCall, next *QueuedCapabilityCall) {
	if root == nil || next == nil {
		return
	}
	if *root == nil {
		copied := cloneQueuedCapabilityCall(*next)
		*root = &copied
		return
	}
	(*root).Input.QueueTail = mergeQueuedCapabilityQueue((*root).Input.QueueTail, []QueuedCapabilityCall{*next})
}

func parseCapabilityReplyContent(raw string) ark_dal.ContentStruct {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ark_dal.ContentStruct{}
	}

	content := ark_dal.ContentStruct{}
	if err := sonic.UnmarshalString(raw, &content); err == nil {
		return content
	}

	repaired, repairErr := jsonrepair.RepairJSON(raw)
	if repairErr == nil {
		if err := sonic.UnmarshalString(repaired, &content); err == nil {
			return content
		}
	}

	return ark_dal.ContentStruct{Reply: raw}
}

func buildQueuedCapabilityCallFromTrace(baseRequest CapabilityRequest, trace ark_dal.CapabilityCallTrace) *QueuedCapabilityCall {
	if !trace.Pending || strings.TrimSpace(trace.FunctionName) == "" {
		return nil
	}

	request := baseRequest
	request.PayloadJSON = []byte(strings.TrimSpace(trace.Arguments))
	call := &QueuedCapabilityCall{
		CallID:         strings.TrimSpace(trace.CallID),
		CapabilityName: strings.TrimSpace(trace.FunctionName),
		Input: CapabilityCallInput{
			Request: request,
		},
	}
	if previousResponseID := strings.TrimSpace(trace.PreviousResponseID); previousResponseID != "" {
		call.Input.Continuation = &CapabilityContinuationInput{
			PreviousResponseID: previousResponseID,
		}
	}
	if strings.TrimSpace(trace.ApprovalType) != "" ||
		strings.TrimSpace(trace.ApprovalTitle) != "" ||
		strings.TrimSpace(trace.ApprovalSummary) != "" ||
		!trace.ApprovalExpiresAt.IsZero() {
		call.Input.Approval = &CapabilityApprovalSpec{
			Type:      strings.TrimSpace(trace.ApprovalType),
			Title:     strings.TrimSpace(trace.ApprovalTitle),
			Summary:   strings.TrimSpace(trace.ApprovalSummary),
			ExpiresAt: trace.ApprovalExpiresAt.UTC(),
		}
	}
	return call
}
