package agentruntime

import (
	"context"
	"strings"
)

type defaultContinuationReplyTurnExecutor struct {
	selectModel func(context.Context, string, string) capabilityReplyPlannerModelSelection
}

func NewDefaultContinuationReplyTurnExecutor() ContinuationReplyTurnExecutor {
	return &defaultContinuationReplyTurnExecutor{
		selectModel: defaultCapabilityReplyPlannerSelectModel,
	}
}

func (e *defaultContinuationReplyTurnExecutor) ExecuteContinuationReplyTurn(ctx context.Context, req ContinuationReplyTurnRequest) (ContinuationReplyTurnResult, error) {
	if req.Session == nil || req.Run == nil {
		return ContinuationReplyTurnResult{}, nil
	}

	chatID := strings.TrimSpace(req.Session.ChatID)
	openID := strings.TrimSpace(req.Run.ActorOpenID)
	if chatID == "" || openID == "" {
		return ContinuationReplyTurnResult{}, nil
	}

	tools := decorateRuntimeChatTools(defaultChatToolProvider())
	if tools == nil {
		return ContinuationReplyTurnResult{}, nil
	}
	registry := buildInitialChatCapabilityRegistry(nil, tools)

	selectModel := defaultCapabilityReplyPlannerSelectModel
	if e != nil && e.selectModel != nil {
		selectModel = e.selectModel
	}
	modelID := strings.TrimSpace(selectModel(ctx, chatID, openID).ModelID)
	if modelID == "" {
		return ContinuationReplyTurnResult{}, nil
	}

	baseRequest := continuationCapabilityRequest(req)
	result := ContinuationReplyTurnResult{}
	turnReq := InitialChatTurnRequest{
		Plan: InitialChatExecutionPlan{
			ModelID:   modelID,
			ChatID:    chatID,
			OpenID:    openID,
			Prompt:    continuationReplyTurnSystemPrompt(),
			UserInput: buildContinuationReplyTurnUserPrompt(req),
			Tools:     tools,
		},
	}

	for turn := 0; turn < defaultInitialChatToolTurns; turn++ {
		turnResult, err := defaultInitialChatTurnExecutor(ctx, turnReq)
		if err != nil {
			return ContinuationReplyTurnResult{}, nil
		}

		result.Plan = collectCapabilityReplyTurnPlan(turnResult.Stream)
		snapshot := turnResult.Snapshot()
		if snapshot.ToolCall == nil {
			fallbackPlan := CapabilityReplyPlan{
				ThoughtText: strings.TrimSpace(req.ThoughtFallback),
				ReplyText:   strings.TrimSpace(req.ReplyFallback),
			}
			result.Plan = normalizeCapabilityReplyPlan(result.Plan, fallbackPlan.ReplyText)
			if strings.TrimSpace(result.Plan.ThoughtText) == "" {
				result.Plan.ThoughtText = strings.TrimSpace(fallbackPlan.ThoughtText)
			}
			result.Executed = strings.TrimSpace(result.Plan.ThoughtText) != "" ||
				strings.TrimSpace(result.Plan.ReplyText) != "" ||
				result.PendingCapability != nil
			return result, nil
		}
		if req.PlanRecorder != nil {
			if err := req.PlanRecorder.RecordReplyTurnPlan(ctx, result.Plan, snapshot.ToolCall); err != nil {
				return ContinuationReplyTurnResult{}, err
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
						return ContinuationReplyTurnResult{}, err
					}
				} else {
					result.CapabilityCalls = append(result.CapabilityCalls, call)
				}
			}
		}
		if execution.NextOutput == nil || strings.TrimSpace(snapshot.ResponseID) == "" {
			return ContinuationReplyTurnResult{}, nil
		}

		turnReq = InitialChatTurnRequest{
			Plan:               turnReq.Plan,
			PreviousResponseID: strings.TrimSpace(snapshot.ResponseID),
			ToolOutput:         execution.NextOutput,
		}
	}

	return ContinuationReplyTurnResult{}, nil
}

func continuationCapabilityRequest(req ContinuationReplyTurnRequest) CapabilityRequest {
	request := CapabilityRequest{
		Scope:       continuationCapabilityScope(req.Run),
		ChatID:      strings.TrimSpace(req.Session.ChatID),
		ActorOpenID: strings.TrimSpace(req.Run.ActorOpenID),
		InputText:   coalesceString(req.Run.Goal, req.Run.InputText),
	}
	if req.Run != nil {
		request.SessionID = strings.TrimSpace(req.Run.SessionID)
		request.RunID = strings.TrimSpace(req.Run.ID)
	}
	return request
}

func continuationCapabilityScope(run *AgentRun) CapabilityScope {
	if run == nil {
		return CapabilityScopeGroup
	}
	switch run.TriggerType {
	case TriggerTypeP2P:
		return CapabilityScopeP2P
	default:
		return CapabilityScopeGroup
	}
}

func continuationReplyTurnSystemPrompt() string {
	return strings.TrimSpace(`
你是一个 durable agent runtime 的恢复阶段规划器。
当前发生了一个外部恢复事件（例如回调、定时任务、审批完成），你需要基于上下文继续处理原请求。

要求：
- 优先围绕原始用户意图继续，而不是只复述“已经恢复”
- 如有必要可以调用工具补充读取信息
- 当前 runtime 每一轮最多只接受一个需要继续喂回结果的工具调用；如果需要多个工具，请串行规划，一次只发起一个
- 你的输出只能二选一：要么直接给最终回答，要么只发起一个 function call
- 如果需要调用工具，不要输出任何 JSON、解释或额外文本，只发起一个 function call
- 如果不需要调用工具，只输出 JSON object
- 不能同时输出最终回答和 function call
- 字段只使用 thought 和 reply
- thought: 一句简短内部思路摘要
- reply: 给用户看的最终续写回复
- reply 默认像群里正常成员接话，不要写成系统播报、审批回执或工单状态单
- 能直接说结果就直接说结果，非必要不要拉长
`)
}

func buildContinuationReplyTurnUserPrompt(req ContinuationReplyTurnRequest) string {
	var builder strings.Builder
	builder.WriteString("请继续这次 agent runtime 恢复。\n")
	builder.WriteString("原始目标:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(coalesceString(req.Run.Goal, req.Run.InputText)))
	builder.WriteString("\n恢复来源:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(continuationSourceLabel(req.Source)))
	builder.WriteString("\n等待原因:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(string(req.WaitingReason)))
	builder.WriteString("\n前置步骤类型:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(continuationStepKindLabel(req.PreviousStepKind)))
	builder.WriteString("\n前置步骤标题:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(req.PreviousStepTitle))
	builder.WriteString("\n前置步骤引用:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(req.PreviousStepExternalRef))
	builder.WriteString("\n恢复摘要:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(req.ResumeSummary))
	builder.WriteString("\n恢复 payload:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(strings.TrimSpace(string(req.ResumePayloadJSON))))
	builder.WriteString("\n静态续写候选思路:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(req.ThoughtFallback))
	builder.WriteString("\n静态续写候选回复:\n")
	builder.WriteString(defaultCapabilityReplyPlannerTextBlock(req.ReplyFallback))
	builder.WriteString("\n请基于这些信息判断：如果需要补充工具，就只发起一个 function call；如果不需要，就输出最终 JSON。不要同时输出两种格式。")
	return builder.String()
}
