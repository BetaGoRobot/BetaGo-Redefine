package agentruntime

import (
	"context"
	"strings"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/bytedance/gg/gresult"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestDefaultContinuationReplyTurnExecutorOwnsToolLoop(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalTurnExecutor := defaultInitialChatTurnExecutor
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultInitialChatTurnExecutor = originalTurnExecutor
	}()

	toolCalls := 0
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("search_history").
			Desc("搜索历史").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				toolCalls++
				if args != `{"q":"日报"}` {
					t.Fatalf("tool args = %q, want %q", args, `{"q":"日报"}`)
				}
				return gresult.OK("搜索结果")
			}),
	)
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}

	turnCalls := 0
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			if req.PreviousResponseID != "" || req.ToolOutput != nil {
				t.Fatalf("unexpected first turn request: %+v", req)
			}
			if !strings.Contains(req.Plan.UserInput, "日报推送任务") {
				t.Fatalf("user input = %q, want contain %q", req.Plan.UserInput, "日报推送任务")
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先补查历史"}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_1",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_1",
							FunctionName: "search_history",
							Arguments:    `{"q":"日报"}`,
						},
					}
				},
			}, nil
		case 2:
			if req.PreviousResponseID != "resp_1" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_1")
			}
			if req.ToolOutput == nil || req.ToolOutput.CallID != "call_1" || req.ToolOutput.Output != "搜索结果" {
				t.Fatalf("unexpected tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "结合历史结果继续收尾",
					Reply:   "我已经补充历史信息并处理完成。",
				}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_2"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}

	executor := &defaultContinuationReplyTurnExecutor{
		selectModel: func(context.Context, string, string) capabilityReplyPlannerModelSelection {
			return capabilityReplyPlannerModelSelection{
				Mode:    appconfig.ChatModeAgentic,
				ModelID: "ep-test-agentic",
			}
		},
	}
	result, err := executor.ExecuteContinuationReplyTurn(context.Background(), ContinuationReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我继续日报任务",
		},
		Source:                  ResumeSourceSchedule,
		WaitingReason:           WaitingReasonSchedule,
		PreviousStepKind:        StepKindWait,
		PreviousStepTitle:       "日报推送任务",
		PreviousStepExternalRef: "schedule_job_daily",
		ThoughtFallback:         "恢复来源：定时任务",
		ReplyFallback:           "定时任务已恢复执行并完成。",
	})
	if err != nil {
		t.Fatalf("ExecuteContinuationReplyTurn() error = %v", err)
	}
	if !result.Executed {
		t.Fatal("expected executed reply turn result")
	}
	if result.Plan.ReplyText != "我已经补充历史信息并处理完成。" {
		t.Fatalf("reply text = %q, want %q", result.Plan.ReplyText, "我已经补充历史信息并处理完成。")
	}
	if len(result.CapabilityCalls) != 1 {
		t.Fatalf("capability call count = %d, want 1", len(result.CapabilityCalls))
	}
	if result.CapabilityCalls[0].CapabilityName != "search_history" {
		t.Fatalf("capability name = %q, want %q", result.CapabilityCalls[0].CapabilityName, "search_history")
	}
	if toolCalls != 1 {
		t.Fatalf("tool call count = %d, want 1", toolCalls)
	}
}

func TestContinuationReplyTurnSystemPromptConstrainsSingleToolCallPerTurn(t *testing.T) {
	prompt := continuationReplyTurnSystemPrompt()
	if !strings.Contains(prompt, "一次只发起一个") {
		t.Fatalf("prompt = %q, want contain single-tool-call constraint", prompt)
	}
	if !strings.Contains(prompt, "像群里正常成员接话") {
		t.Fatalf("prompt = %q, want contain conversational tone hint", prompt)
	}
	if !strings.Contains(prompt, "只发起一个 function call") {
		t.Fatalf("prompt = %q, want contain function-call-only hint", prompt)
	}
	if !strings.Contains(prompt, "不能同时输出") {
		t.Fatalf("prompt = %q, want contain mutually-exclusive-output hint", prompt)
	}
}

func TestDefaultContinuationReplyTurnExecutorTurnsApprovalIntoPendingCapability(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalTurnExecutor := defaultInitialChatTurnExecutor
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultInitialChatTurnExecutor = originalTurnExecutor
	}()

	toolCalls := 0
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("send_message").
			Desc("发送消息").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				toolCalls++
				return gresult.OK("should not execute")
			}),
	)
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}

	turnCalls := 0
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_1",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_pending_1",
							FunctionName: "send_message",
							Arguments:    `{"content":"日报已更新"}`,
						},
					}
				},
			}, nil
		case 2:
			if req.PreviousResponseID != "resp_1" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_1")
			}
			if req.ToolOutput == nil || req.ToolOutput.Output != "已发起审批，等待确认后发送消息。" {
				t.Fatalf("unexpected pending tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "先挂起发送审批，再告知用户",
					Reply:   "我已经发起新的发送审批。",
				}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_2"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}

	executor := &defaultContinuationReplyTurnExecutor{
		selectModel: func(context.Context, string, string) capabilityReplyPlannerModelSelection {
			return capabilityReplyPlannerModelSelection{
				Mode:    appconfig.ChatModeAgentic,
				ModelID: "ep-test-agentic",
			}
		},
	}
	result, err := executor.ExecuteContinuationReplyTurn(context.Background(), ContinuationReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我继续日报任务",
		},
		Source:                  ResumeSourceSchedule,
		WaitingReason:           WaitingReasonSchedule,
		PreviousStepKind:        StepKindWait,
		PreviousStepTitle:       "日报推送任务",
		PreviousStepExternalRef: "schedule_job_daily",
		ThoughtFallback:         "恢复来源：定时任务",
		ReplyFallback:           "定时任务已恢复执行并完成。",
	})
	if err != nil {
		t.Fatalf("ExecuteContinuationReplyTurn() error = %v", err)
	}
	if !result.Executed {
		t.Fatal("expected executed reply turn result")
	}
	if result.PendingCapability == nil {
		t.Fatal("expected pending capability")
	}
	if result.PendingCapability.CapabilityName != "send_message" {
		t.Fatalf("pending capability name = %q, want %q", result.PendingCapability.CapabilityName, "send_message")
	}
	if result.Plan.ReplyText != "我已经发起新的发送审批。" {
		t.Fatalf("reply text = %q, want %q", result.Plan.ReplyText, "我已经发起新的发送审批。")
	}
	if toolCalls != 0 {
		t.Fatalf("tool call count = %d, want 0", toolCalls)
	}
}

func TestDefaultContinuationReplyTurnExecutorChainsMultiplePendingCapabilities(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalTurnExecutor := defaultInitialChatTurnExecutor
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultInitialChatTurnExecutor = originalTurnExecutor
	}()

	toolCalls := 0
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("send_message").
			Desc("发送消息").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				toolCalls++
				return gresult.OK("should not execute")
			}),
	)
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}

	turnCalls := 0
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_1",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_pending_1",
							FunctionName: "send_message",
							Arguments:    `{"content":"日报一"}`,
						},
					}
				},
			}, nil
		case 2:
			if req.PreviousResponseID != "resp_1" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_1")
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_2",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_pending_2",
							FunctionName: "send_message",
							Arguments:    `{"content":"日报二"}`,
						},
					}
				},
			}, nil
		case 3:
			if req.PreviousResponseID != "resp_2" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_2")
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "先排队两个发送动作",
					Reply:   "我已经把两个发送动作排队了。",
				}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_3"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}

	executor := &defaultContinuationReplyTurnExecutor{
		selectModel: func(context.Context, string, string) capabilityReplyPlannerModelSelection {
			return capabilityReplyPlannerModelSelection{
				Mode:    appconfig.ChatModeAgentic,
				ModelID: "ep-test-agentic",
			}
		},
	}
	result, err := executor.ExecuteContinuationReplyTurn(context.Background(), ContinuationReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我继续日报任务",
		},
		Source:                  ResumeSourceSchedule,
		WaitingReason:           WaitingReasonSchedule,
		PreviousStepKind:        StepKindWait,
		PreviousStepTitle:       "日报推送任务",
		PreviousStepExternalRef: "schedule_job_daily",
		ThoughtFallback:         "恢复来源：定时任务",
		ReplyFallback:           "定时任务已恢复执行并完成。",
	})
	if err != nil {
		t.Fatalf("ExecuteContinuationReplyTurn() error = %v", err)
	}
	if !result.Executed {
		t.Fatal("expected executed reply turn result")
	}
	if result.PendingCapability == nil {
		t.Fatal("expected pending capability")
	}
	if len(result.PendingCapability.Input.QueueTail) != 1 {
		t.Fatalf("queue tail len = %d, want 1", len(result.PendingCapability.Input.QueueTail))
	}
	if result.PendingCapability.Input.QueueTail[0].Input.Continuation == nil || result.PendingCapability.Input.QueueTail[0].Input.Continuation.PreviousResponseID != "resp_2" {
		t.Fatalf("unexpected queue tail continuation: %+v", result.PendingCapability.Input.QueueTail[0].Input.Continuation)
	}
	if result.Plan.ReplyText != "我已经把两个发送动作排队了。" {
		t.Fatalf("reply text = %q, want %q", result.Plan.ReplyText, "我已经把两个发送动作排队了。")
	}
	if toolCalls != 0 {
		t.Fatalf("tool call count = %d, want 0", toolCalls)
	}
}

func TestBuildContinuationReplyTurnUserPromptIncludesResumeSummaryAndPayload(t *testing.T) {
	prompt := buildContinuationReplyTurnUserPrompt(ContinuationReplyTurnRequest{
		Run: &AgentRun{
			InputText: "帮我继续日报任务",
		},
		Source:            ResumeSourceSchedule,
		WaitingReason:     WaitingReasonSchedule,
		ResumeSummary:     "定时任务触发：日报窗口已到。",
		ResumePayloadJSON: []byte(`{"task_id":"task_daily","window":"morning"}`),
	})

	if !strings.Contains(prompt, "恢复摘要") {
		t.Fatalf("prompt = %q, want contain %q", prompt, "恢复摘要")
	}
	if !strings.Contains(prompt, "定时任务触发：日报窗口已到。") {
		t.Fatalf("prompt = %q, want contain resume summary", prompt)
	}
	if !strings.Contains(prompt, `"task_id":"task_daily"`) {
		t.Fatalf("prompt = %q, want contain payload json", prompt)
	}
}

func TestDefaultContinuationReplyTurnExecutorRecordsCompletedCapabilityTraceWithRecorder(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalTurnExecutor := defaultInitialChatTurnExecutor
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultInitialChatTurnExecutor = originalTurnExecutor
	}()

	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("search_history").
			Desc("搜索历史").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				return gresult.OK("搜索结果")
			}),
	)
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}

	turnCalls := 0
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_1",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_1",
							FunctionName: "search_history",
							Arguments:    `{"q":"日报"}`,
						},
					}
				},
			}, nil
		case 2:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Reply: "我已经补充历史信息并处理完成。",
				}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_2"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}

	recorder := &recordingCapabilityTraceRecorder{}
	executor := &defaultContinuationReplyTurnExecutor{
		selectModel: func(context.Context, string, string) capabilityReplyPlannerModelSelection {
			return capabilityReplyPlannerModelSelection{
				Mode:    appconfig.ChatModeAgentic,
				ModelID: "ep-test-agentic",
			}
		},
	}
	result, err := executor.ExecuteContinuationReplyTurn(context.Background(), ContinuationReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我继续日报任务",
		},
		Source:                  ResumeSourceSchedule,
		WaitingReason:           WaitingReasonSchedule,
		PreviousStepKind:        StepKindWait,
		PreviousStepTitle:       "日报推送任务",
		PreviousStepExternalRef: "schedule_job_daily",
		ThoughtFallback:         "恢复来源：定时任务",
		ReplyFallback:           "定时任务已恢复执行并完成。",
		Recorder:                recorder,
	})
	if err != nil {
		t.Fatalf("ExecuteContinuationReplyTurn() error = %v", err)
	}
	if !result.Executed {
		t.Fatal("expected executed reply turn result")
	}
	if len(result.CapabilityCalls) != 0 {
		t.Fatalf("capability call count = %d, want 0 after recorder", len(result.CapabilityCalls))
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("recorded call count = %d, want 1", len(recorder.calls))
	}
	if recorder.calls[0].CapabilityName != "search_history" {
		t.Fatalf("recorded capability name = %q, want %q", recorder.calls[0].CapabilityName, "search_history")
	}
	if recorder.calls[0].PreviousResponseID != "resp_1" {
		t.Fatalf("recorded previous response id = %q, want %q", recorder.calls[0].PreviousResponseID, "resp_1")
	}
}

func TestDefaultContinuationReplyTurnExecutorRecordsIntermediatePlanTurn(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalTurnExecutor := defaultInitialChatTurnExecutor
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultInitialChatTurnExecutor = originalTurnExecutor
	}()

	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("search_history").
			Desc("搜索历史").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				return gresult.OK("搜索结果")
			}),
	)
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}

	turnCalls := 0
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "先补查一次历史",
				}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_1",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_1",
							FunctionName: "search_history",
							Arguments:    `{"q":"日报"}`,
						},
					}
				},
			}, nil
		case 2:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Reply: "我已经补充历史信息并处理完成。",
				}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_2"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}

	recorder := &recordingCapabilityTraceRecorder{}
	executor := &defaultContinuationReplyTurnExecutor{
		selectModel: func(context.Context, string, string) capabilityReplyPlannerModelSelection {
			return capabilityReplyPlannerModelSelection{
				Mode:    appconfig.ChatModeAgentic,
				ModelID: "ep-test-agentic",
			}
		},
	}
	result, err := executor.ExecuteContinuationReplyTurn(context.Background(), ContinuationReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我继续日报任务",
		},
		Source:                  ResumeSourceSchedule,
		WaitingReason:           WaitingReasonSchedule,
		PreviousStepKind:        StepKindWait,
		PreviousStepTitle:       "日报推送任务",
		PreviousStepExternalRef: "schedule_job_daily",
		ThoughtFallback:         "恢复来源：定时任务",
		ReplyFallback:           "定时任务已恢复执行并完成。",
		PlanRecorder:            recorder,
	})
	if err != nil {
		t.Fatalf("ExecuteContinuationReplyTurn() error = %v", err)
	}
	if !result.Executed {
		t.Fatal("expected executed reply turn result")
	}
	if len(recorder.plans) != 1 {
		t.Fatalf("recorded plan count = %d, want 1", len(recorder.plans))
	}
	if recorder.plans[0].Plan.ThoughtText != "先补查一次历史" {
		t.Fatalf("recorded plan thought = %q, want %q", recorder.plans[0].Plan.ThoughtText, "先补查一次历史")
	}
	if recorder.plans[0].ToolCall.FunctionName != "search_history" {
		t.Fatalf("recorded tool name = %q, want %q", recorder.plans[0].ToolCall.FunctionName, "search_history")
	}
	if recorder.plans[0].ToolCall.Arguments != `{"q":"日报"}` {
		t.Fatalf("recorded tool args = %q, want %q", recorder.plans[0].ToolCall.Arguments, `{"q":"日报"}`)
	}
}
