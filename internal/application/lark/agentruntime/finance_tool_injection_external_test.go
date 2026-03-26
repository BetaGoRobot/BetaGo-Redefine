package agentruntime_test

import (
	"context"
	"iter"
	"testing"

	agentruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestExecuteReplyTurnLoopInjectsDiscoveredFinanceToolsIntoNextTurn(t *testing.T) {
	registry := agentruntime.NewCapabilityRegistry()
	capabilityTools := handlers.BuildRuntimeCapabilityTools()
	for _, capability := range capdef.BuildToolCapabilities(capabilityTools, nil, (*larkim.P2MessageReceiveV1)(nil)) {
		if capability == nil {
			continue
		}
		if err := registry.Register(capability); err != nil {
			t.Fatalf("register capability: %v", err)
		}
	}

	turnCalls := 0
	turnExecutor := func(ctx context.Context, req agentruntime.InitialChatTurnRequest) (agentruntime.InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			if req.AdditionalTools != nil {
				t.Fatalf("unexpected additional tools on first turn: %+v", req.AdditionalTools)
			}
			return agentruntime.InitialChatTurnResult{
				Stream: testSeqFromItems(&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先发现可用金融工具"}),
				Snapshot: func() agentruntime.InitialChatTurnSnapshot {
					return agentruntime.InitialChatTurnSnapshot{
						ResponseID: "resp_discover",
						ToolCall: &agentruntime.InitialChatToolCall{
							CallID:       "call_discover",
							FunctionName: "finance_tool_discover",
							Arguments:    `{"category":"market","tool_names":["finance_market_data_get"],"limit":1}`,
						},
					}
				},
			}, nil
		case 2:
			if req.AdditionalTools == nil {
				t.Fatal("expected discovered finance tools on second turn")
			}
			if _, ok := req.AdditionalTools.Get("finance_market_data_get"); !ok {
				t.Fatal("expected finance_market_data_get to be injected")
			}
			if _, ok := req.AdditionalTools.Get("finance_news_get"); ok {
				t.Fatal("did not expect finance_news_get to be injected")
			}
			if req.PreviousResponseID != "resp_discover" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_discover")
			}
			return agentruntime.InitialChatTurnResult{
				Stream: testSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "已经注入工具",
					Reply:   "下一轮已经可以直接调用金融工具。",
				}}),
				Snapshot: func() agentruntime.InitialChatTurnSnapshot {
					return agentruntime.InitialChatTurnSnapshot{ResponseID: "resp_final"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return agentruntime.InitialChatTurnResult{}, nil
		}
	}

	result, err := agentruntime.ExecuteReplyTurnLoop(context.Background(), agentruntime.ReplyTurnLoopRequest{
		TurnRequest: agentruntime.InitialChatTurnRequest{
			Plan: agentruntime.InitialChatExecutionPlan{
				ModelID:   "ep-test",
				ChatID:    "oc_chat",
				OpenID:    "ou_user",
				Prompt:    "system prompt",
				UserInput: "帮我先发现金融工具，再决定调用哪个",
				Tools:     handlers.BuildLarkTools(),
			},
		},
		ToolTurns:    3,
		TurnExecutor: turnExecutor,
		BaseRequest: agentruntime.CapabilityRequest{
			Scope:       agentruntime.CapabilityScopeGroup,
			ChatID:      "oc_chat",
			ActorOpenID: "ou_user",
			InputText:   "帮我先发现金融工具，再决定调用哪个",
		},
		Registry:        registry,
		CapabilityTools: capabilityTools,
		FallbackPlan:    agentruntime.CapabilityReplyPlan{ReplyText: "fallback"},
	})
	if err != nil {
		t.Fatalf("ExecuteReplyTurnLoop() error = %v", err)
	}
	if !result.Executed {
		t.Fatal("expected executed loop result")
	}
	if result.Plan.ReplyText != "下一轮已经可以直接调用金融工具。" {
		t.Fatalf("reply text = %q, want %q", result.Plan.ReplyText, "下一轮已经可以直接调用金融工具。")
	}
}

func testSeqFromItems(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if item == nil {
				continue
			}
			if !yield(item) {
				return
			}
		}
	}
}
