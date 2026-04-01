package replay

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestConversationReplayDryRunBuildsArtifactsWithoutModelOutput(t *testing.T) {
	service := IntentReplayService{
		standardPlanBuilder: func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error) {
			return chatflow.InitialChatExecutionPlan{
				ChatID:       "oc_chat",
				OpenID:       "ou_actor",
				Prompt:       "standard prompt",
				UserInput:    "当前用户消息",
				MaxToolTurns: 0,
			}, nil
		},
	}

	loaded := loadedReplayTarget{
		Target: ReplayTarget{ChatID: "oc_chat", OpenID: "ou_actor", Text: "帮我总结今天讨论"},
		Event:  &larkim.P2MessageReceiveV1{},
	}
	cases := []ReplayCase{
		{
			Name: ReplayCaseBaseline,
			RouteDecision: &ReplayRouteDecision{
				FinalMode: "standard",
			},
		},
	}

	got, err := service.replayConversation(context.Background(), loaded, cases, false)
	if err != nil {
		t.Fatalf("replayConversation() error = %v", err)
	}
	if got[0].Conversation == nil {
		t.Fatalf("Conversation = nil")
	}
	if got[0].Conversation.Prompt != "standard prompt" || got[0].Conversation.UserInput != "当前用户消息" {
		t.Fatalf("Conversation = %+v", got[0].Conversation)
	}
	if got[0].Conversation.Output != nil {
		t.Fatalf("dry-run output = %+v, want nil", got[0].Conversation.Output)
	}
}

func TestConversationReplayLiveModelCapturesStandardOutput(t *testing.T) {
	service := IntentReplayService{
		standardPlanBuilder: func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error) {
			return chatflow.InitialChatExecutionPlan{
				ChatID:    "oc_chat",
				OpenID:    "ou_actor",
				Prompt:    "standard prompt",
				UserInput: "当前用户消息",
			}, nil
		},
		executeTurn: func(context.Context, chatflow.InitialChatTurnRequest) (chatflow.InitialChatTurnResult, error) {
			return chatflow.InitialChatTurnResult{
				Stream: testReasoningStream(
					&ark_dal.ModelStreamRespReasoning{Content: `{"decision":"reply","thought":"简短判断","reply":"这是最终回复","reference_from_web":"","reference_from_history":"来自历史"}`},
				),
				Snapshot: func() chatflow.InitialChatTurnSnapshot {
					return chatflow.InitialChatTurnSnapshot{}
				},
			}, nil
		},
	}

	loaded := loadedReplayTarget{
		Target: ReplayTarget{ChatID: "oc_chat", OpenID: "ou_actor", Text: "帮我总结今天讨论"},
		Event:  &larkim.P2MessageReceiveV1{},
	}
	cases := []ReplayCase{
		{
			Name:           ReplayCaseBaseline,
			IntentAnalysis: &intent.IntentAnalysis{InteractionMode: intent.InteractionModeStandard},
			RouteDecision:  &ReplayRouteDecision{FinalMode: "standard"},
		},
	}

	got, err := service.replayConversation(context.Background(), loaded, cases, true)
	if err != nil {
		t.Fatalf("replayConversation() error = %v", err)
	}
	if got[0].Conversation == nil || got[0].Conversation.Output == nil {
		t.Fatalf("Conversation output missing: %+v", got[0].Conversation)
	}
	if got[0].Conversation.Output.Decision != "reply" || got[0].Conversation.Output.Reply != "这是最终回复" {
		t.Fatalf("Conversation output = %+v", got[0].Conversation.Output)
	}
	if got[0].Conversation.Output.ReferenceFromHistory != "来自历史" {
		t.Fatalf("ReferenceFromHistory = %q, want %q", got[0].Conversation.Output.ReferenceFromHistory, "来自历史")
	}
}

func TestConversationReplayLiveModelCapturesAgenticToolIntentWithoutExecutingTools(t *testing.T) {
	service := IntentReplayService{
		agenticPlanBuilder: func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error) {
			return chatflow.InitialChatExecutionPlan{
				ChatID:       "oc_chat",
				OpenID:       "ou_actor",
				Prompt:       "agentic prompt",
				UserInput:    "当前用户消息",
				MaxToolTurns: 6,
			}, nil
		},
		executeTurn: func(context.Context, chatflow.InitialChatTurnRequest) (chatflow.InitialChatTurnResult, error) {
			return chatflow.InitialChatTurnResult{
				Stream: testReasoningStream(),
				Snapshot: func() chatflow.InitialChatTurnSnapshot {
					return chatflow.InitialChatTurnSnapshot{
						ToolCall: &chatflow.InitialChatToolCall{
							CallID:       "call_1",
							FunctionName: "search_history",
							Arguments:    `{"query":"金价"}`,
						},
					}
				},
			}, nil
		},
	}

	loaded := loadedReplayTarget{
		Target: ReplayTarget{ChatID: "oc_chat", OpenID: "ou_actor", Text: "帮我分析金价波动"},
		Event:  &larkim.P2MessageReceiveV1{},
	}
	cases := []ReplayCase{
		{
			Name:           ReplayCaseAugmented,
			IntentAnalysis: &intent.IntentAnalysis{InteractionMode: intent.InteractionModeAgentic},
			RouteDecision:  &ReplayRouteDecision{FinalMode: "agentic"},
		},
	}

	got, err := service.replayConversation(context.Background(), loaded, cases, true)
	if err != nil {
		t.Fatalf("replayConversation() error = %v", err)
	}
	if got[0].Conversation == nil || got[0].Conversation.ToolIntent == nil {
		t.Fatalf("ToolIntent missing: %+v", got[0].Conversation)
	}
	if !got[0].Conversation.ToolIntent.WouldCallTools {
		t.Fatalf("WouldCallTools = false, want true")
	}
	if got[0].Conversation.ToolIntent.FunctionName != "search_history" {
		t.Fatalf("FunctionName = %q, want %q", got[0].Conversation.ToolIntent.FunctionName, "search_history")
	}
}

func TestConversationReplayDiffMarksReplyAndToolChanges(t *testing.T) {
	diff := buildReplayDiff([]ReplayCase{
		{
			Name: ReplayCaseBaseline,
			Conversation: &ReplayConversation{
				Output: &ReplayConversationOutput{
					Decision: "reply",
					Reply:    "标准回复",
				},
			},
		},
		{
			Name: ReplayCaseAugmented,
			Conversation: &ReplayConversation{
				Output: &ReplayConversationOutput{
					Decision: "reply",
					Reply:    "增强回复",
				},
				ToolIntent: &ReplayToolIntent{
					WouldCallTools: true,
					FunctionName:   "search_history",
				},
			},
		},
	})

	changed := strings.Join(diff.ChangedFieldNames(), ",")
	for _, want := range []string{"conversation.output.reply", "conversation.tool_intent.function_name"} {
		if !strings.Contains(changed, want) {
			t.Fatalf("changed fields = %q, want contain %q", changed, want)
		}
	}
}

func TestConversationReplayReturnsExecutorError(t *testing.T) {
	service := IntentReplayService{
		standardPlanBuilder: func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error) {
			return chatflow.InitialChatExecutionPlan{ChatID: "oc_chat", OpenID: "ou_actor"}, nil
		},
		executeTurn: func(context.Context, chatflow.InitialChatTurnRequest) (chatflow.InitialChatTurnResult, error) {
			return chatflow.InitialChatTurnResult{}, errors.New("boom")
		},
	}

	_, err := service.replayConversation(context.Background(), loadedReplayTarget{
		Target: ReplayTarget{ChatID: "oc_chat", OpenID: "ou_actor", Text: "测试"},
		Event:  &larkim.P2MessageReceiveV1{},
	}, []ReplayCase{{Name: ReplayCaseBaseline, RouteDecision: &ReplayRouteDecision{FinalMode: "standard"}}}, true)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("replayConversation() error = %v, want executor error", err)
	}
}

func testReasoningStream(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}

var _ = xhandler.BaseMetaData{}
