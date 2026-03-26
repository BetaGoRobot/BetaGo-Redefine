package agentruntime

import (
	"context"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/bytedance/gg/gresult"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type recordingCapabilityTraceRecorder struct {
	calls []CompletedCapabilityCall
	plans []recordedReplyTurnPlan
}

type recordedReplyTurnPlan struct {
	Plan     CapabilityReplyPlan
	ToolCall InitialChatToolCall
}

func (r *recordingCapabilityTraceRecorder) RecordCompletedCapabilityCall(_ context.Context, call CompletedCapabilityCall) error {
	r.calls = append(r.calls, call)
	return nil
}

func (r *recordingCapabilityTraceRecorder) RecordReplyTurnPlan(_ context.Context, plan CapabilityReplyPlan, toolCall *InitialChatToolCall) error {
	if toolCall == nil {
		return nil
	}
	r.plans = append(r.plans, recordedReplyTurnPlan{
		Plan:     plan,
		ToolCall: *toolCall,
	})
	return nil
}

func TestDefaultCapabilityReplyTurnExecutorOwnsToolLoop(t *testing.T) {
	toolCalls := 0
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("search_history").
			Desc("搜索历史").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				toolCalls++
				if args != `{"q":"agentic runtime"}` {
					t.Fatalf("tool args = %q, want %q", args, `{"q":"agentic runtime"}`)
				}
				return gresult.OK("搜索结果")
			}),
	)
	turnCalls := 0
	turnExecutor := func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			if req.PreviousResponseID != "resp_tool_1" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_tool_1")
			}
			if req.ToolOutput == nil || req.ToolOutput.CallID != "call_1" || req.ToolOutput.Output != "echo:hello" {
				t.Fatalf("unexpected tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先补一次检索"}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_2",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_nested_1",
							FunctionName: "search_history",
							Arguments:    `{"q":"agentic runtime"}`,
						},
					}
				},
			}, nil
		case 2:
			if req.PreviousResponseID != "resp_2" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_2")
			}
			if req.ToolOutput == nil || req.ToolOutput.CallID != "call_nested_1" || req.ToolOutput.Output != "搜索结果" {
				t.Fatalf("unexpected nested tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "先补检索，再给最终结论",
					Reply:   "我已经补充检索并整理好了。",
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

	executor := newCapabilityReplyTurnExecutorForTest(toolset, turnExecutor)
	result, err := executor.ExecuteCapabilityReplyTurn(context.Background(), CapabilityReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我处理下结果",
		},
		Step: &AgentStep{
			ID:             "step_1",
			CapabilityName: "echo_cap",
			ExternalRef:    "call_1",
		},
		Input: CapabilityCallInput{
			Request: CapabilityRequest{
				Scope:       CapabilityScopeGroup,
				ChatID:      "oc_chat",
				ActorOpenID: "ou_actor",
				InputText:   "帮我处理下结果",
			},
			Continuation: &CapabilityContinuationInput{
				PreviousResponseID: "resp_tool_1",
			},
		},
		Result: CapabilityResult{
			OutputText: "echo:hello",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteCapabilityReplyTurn() error = %v", err)
	}
	if !result.Executed {
		t.Fatal("expected executed reply turn result")
	}
	if result.Plan.ReplyText != "我已经补充检索并整理好了。" {
		t.Fatalf("reply text = %q, want %q", result.Plan.ReplyText, "我已经补充检索并整理好了。")
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

func TestDefaultCapabilityReplyTurnExecutorTurnsApprovalIntoPendingCapability(t *testing.T) {
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
	turnCalls := 0
	turnExecutor := func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			if req.PreviousResponseID != "resp_tool_1" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_tool_1")
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_2",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_pending_2",
							FunctionName: "send_message",
							Arguments:    `{"content":"hello"}`,
						},
					}
				},
			}, nil
		case 2:
			if req.PreviousResponseID != "resp_2" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_2")
			}
			if req.ToolOutput == nil || req.ToolOutput.Output != "已发起审批，等待确认后发送消息。" {
				t.Fatalf("unexpected pending tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "先把审批挂起，再告知用户",
					Reply:   "我已经发起新的发送审批。",
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

	executor := newCapabilityReplyTurnExecutorForTest(toolset, turnExecutor)
	result, err := executor.ExecuteCapabilityReplyTurn(context.Background(), CapabilityReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我处理下结果",
		},
		Step: &AgentStep{
			ID:             "step_1",
			CapabilityName: "echo_cap",
			ExternalRef:    "call_1",
		},
		Input: CapabilityCallInput{
			Request: CapabilityRequest{
				Scope:       CapabilityScopeGroup,
				ChatID:      "oc_chat",
				ActorOpenID: "ou_actor",
				InputText:   "帮我处理下结果",
			},
			Continuation: &CapabilityContinuationInput{
				PreviousResponseID: "resp_tool_1",
			},
		},
		Result: CapabilityResult{
			OutputText: "echo:hello",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteCapabilityReplyTurn() error = %v", err)
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
	if result.PendingCapability.Input.Continuation == nil || result.PendingCapability.Input.Continuation.PreviousResponseID != "resp_2" {
		t.Fatalf("unexpected continuation payload: %+v", result.PendingCapability.Input.Continuation)
	}
	if result.Plan.ReplyText != "我已经发起新的发送审批。" {
		t.Fatalf("reply text = %q, want %q", result.Plan.ReplyText, "我已经发起新的发送审批。")
	}
	if toolCalls != 0 {
		t.Fatalf("tool call count = %d, want 0", toolCalls)
	}
}

func TestDefaultCapabilityReplyTurnExecutorChainsMultiplePendingCapabilities(t *testing.T) {
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
	turnCalls := 0
	turnExecutor := func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			if req.PreviousResponseID != "resp_tool_1" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_tool_1")
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_2",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_pending_2",
							FunctionName: "send_message",
							Arguments:    `{"content":"hello-1"}`,
						},
					}
				},
			}, nil
		case 2:
			if req.PreviousResponseID != "resp_2" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_2")
			}
			if req.ToolOutput == nil || req.ToolOutput.Output != "已发起审批，等待确认后发送消息。" {
				t.Fatalf("unexpected first pending tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_3",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_pending_3",
							FunctionName: "send_message",
							Arguments:    `{"content":"hello-2"}`,
						},
					}
				},
			}, nil
		case 3:
			if req.PreviousResponseID != "resp_3" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_3")
			}
			if req.ToolOutput == nil || req.ToolOutput.Output != "已发起审批，等待确认后发送消息。" {
				t.Fatalf("unexpected second pending tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "先把两个发送动作排队",
					Reply:   "我已经把两个发送动作排队了。",
				}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_4"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}

	executor := newCapabilityReplyTurnExecutorForTest(toolset, turnExecutor)
	result, err := executor.ExecuteCapabilityReplyTurn(context.Background(), CapabilityReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我处理下结果",
		},
		Step: &AgentStep{
			ID:             "step_1",
			CapabilityName: "echo_cap",
			ExternalRef:    "call_1",
		},
		Input: CapabilityCallInput{
			Request: CapabilityRequest{
				Scope:       CapabilityScopeGroup,
				ChatID:      "oc_chat",
				ActorOpenID: "ou_actor",
				InputText:   "帮我处理下结果",
			},
			Continuation: &CapabilityContinuationInput{
				PreviousResponseID: "resp_tool_1",
			},
		},
		Result: CapabilityResult{
			OutputText: "echo:hello",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteCapabilityReplyTurn() error = %v", err)
	}
	if !result.Executed {
		t.Fatal("expected executed reply turn result")
	}
	if result.PendingCapability == nil {
		t.Fatal("expected pending capability")
	}
	if result.PendingCapability.Input.Continuation == nil || result.PendingCapability.Input.Continuation.PreviousResponseID != "resp_2" {
		t.Fatalf("unexpected root continuation payload: %+v", result.PendingCapability.Input.Continuation)
	}
	if len(result.PendingCapability.Input.QueueTail) != 1 {
		t.Fatalf("queue tail len = %d, want 1", len(result.PendingCapability.Input.QueueTail))
	}
	if result.PendingCapability.Input.QueueTail[0].Input.Continuation == nil || result.PendingCapability.Input.QueueTail[0].Input.Continuation.PreviousResponseID != "resp_3" {
		t.Fatalf("unexpected queue tail continuation: %+v", result.PendingCapability.Input.QueueTail[0].Input.Continuation)
	}
	if result.Plan.ReplyText != "我已经把两个发送动作排队了。" {
		t.Fatalf("reply text = %q, want %q", result.Plan.ReplyText, "我已经把两个发送动作排队了。")
	}
	if toolCalls != 0 {
		t.Fatalf("tool call count = %d, want 0", toolCalls)
	}
}

func TestDefaultCapabilityReplyTurnExecutorRecordsCompletedCapabilityTraceWithRecorder(t *testing.T) {
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("search_history").
			Desc("搜索历史").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				return gresult.OK("搜索结果")
			}),
	)
	turnCalls := 0
	turnExecutor := func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_2",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_nested_1",
							FunctionName: "search_history",
							Arguments:    `{"q":"agentic runtime"}`,
						},
					}
				},
			}, nil
		case 2:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Reply: "我已经补充检索并整理好了。",
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

	recorder := &recordingCapabilityTraceRecorder{}
	executor := newCapabilityReplyTurnExecutorForTest(toolset, turnExecutor)
	result, err := executor.ExecuteCapabilityReplyTurn(context.Background(), CapabilityReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我处理下结果",
		},
		Step: &AgentStep{
			ID:             "step_1",
			CapabilityName: "echo_cap",
			ExternalRef:    "call_1",
		},
		Input: CapabilityCallInput{
			Request: CapabilityRequest{
				Scope:       CapabilityScopeGroup,
				ChatID:      "oc_chat",
				ActorOpenID: "ou_actor",
				InputText:   "帮我处理下结果",
			},
			Continuation: &CapabilityContinuationInput{
				PreviousResponseID: "resp_tool_1",
			},
		},
		Result: CapabilityResult{
			OutputText: "echo:hello",
		},
		Recorder: recorder,
	})
	if err != nil {
		t.Fatalf("ExecuteCapabilityReplyTurn() error = %v", err)
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
	if recorder.calls[0].PreviousResponseID != "resp_2" {
		t.Fatalf("recorded previous response id = %q, want %q", recorder.calls[0].PreviousResponseID, "resp_2")
	}
}

func TestDefaultCapabilityReplyTurnExecutorRecordsIntermediatePlanTurn(t *testing.T) {
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("search_history").
			Desc("搜索历史").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				return gresult.OK("搜索结果")
			}),
	)
	turnCalls := 0
	turnExecutor := func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "先补一次检索",
				}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_2",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_nested_1",
							FunctionName: "search_history",
							Arguments:    `{"q":"agentic runtime"}`,
						},
					}
				},
			}, nil
		case 2:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Reply: "我已经补充检索并整理好了。",
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

	recorder := &recordingCapabilityTraceRecorder{}
	executor := newCapabilityReplyTurnExecutorForTest(toolset, turnExecutor)
	result, err := executor.ExecuteCapabilityReplyTurn(context.Background(), CapabilityReplyTurnRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ID:          "run_1",
			SessionID:   "sess_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我处理下结果",
		},
		Step: &AgentStep{
			ID:             "step_1",
			CapabilityName: "echo_cap",
			ExternalRef:    "call_1",
		},
		Input: CapabilityCallInput{
			Request: CapabilityRequest{
				Scope:       CapabilityScopeGroup,
				ChatID:      "oc_chat",
				ActorOpenID: "ou_actor",
				InputText:   "帮我处理下结果",
			},
			Continuation: &CapabilityContinuationInput{
				PreviousResponseID: "resp_tool_1",
			},
		},
		Result: CapabilityResult{
			OutputText: "echo:hello",
		},
		Recorder: recorder,
	})
	if err != nil {
		t.Fatalf("ExecuteCapabilityReplyTurn() error = %v", err)
	}
	if !result.Executed {
		t.Fatal("expected executed reply turn result")
	}
	if len(recorder.plans) != 1 {
		t.Fatalf("recorded plan count = %d, want 1", len(recorder.plans))
	}
	if recorder.plans[0].Plan.ThoughtText != "先补一次检索" {
		t.Fatalf("recorded plan thought = %q, want %q", recorder.plans[0].Plan.ThoughtText, "先补一次检索")
	}
	if recorder.plans[0].ToolCall.FunctionName != "search_history" {
		t.Fatalf("recorded tool name = %q, want %q", recorder.plans[0].ToolCall.FunctionName, "search_history")
	}
	if recorder.plans[0].ToolCall.Arguments != `{"q":"agentic runtime"}` {
		t.Fatalf("recorded tool args = %q, want %q", recorder.plans[0].ToolCall.Arguments, `{"q":"agentic runtime"}`)
	}
}

func newCapabilityReplyTurnExecutorForTest(
	toolset *arktools.Impl[larkim.P2MessageReceiveV1],
	turnExecutor func(context.Context, InitialChatTurnRequest) (InitialChatTurnResult, error),
) *defaultCapabilityReplyTurnExecutor {
	return &defaultCapabilityReplyTurnExecutor{
		selectModel: func(context.Context, string, string) replyTurnModelSelection {
			return replyTurnModelSelection{
				Mode:    appconfig.ChatModeAgentic,
				ModelID: "ep-test-agentic",
			}
		},
		deps: defaultRuntimeExecutorDeps{
			toolProvider: func() *arktools.Impl[larkim.P2MessageReceiveV1] {
				return toolset
			},
			initialChatTurnExecutor: turnExecutor,
		},
	}
}
