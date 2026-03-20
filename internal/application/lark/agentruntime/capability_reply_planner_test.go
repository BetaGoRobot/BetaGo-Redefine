package agentruntime

import (
	"context"
	"strings"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
)

func TestDefaultCapabilityReplyPlannerUsesConfiguredModelAndReturnsStructuredReply(t *testing.T) {
	planner := newDefaultCapabilityReplyPlannerForTest(
		func(context.Context, string, string) capabilityReplyPlannerModelSelection {
			return capabilityReplyPlannerModelSelection{
				Mode:    appconfig.ChatModeAgentic,
				ModelID: "ep-reasoning",
			}
		},
		func(ctx context.Context, req capabilityReplyPlannerGenerationRequest) (ark_dal.ContentStruct, error) {
			if req.ModelID != "ep-reasoning" {
				t.Fatalf("model id = %q, want %q", req.ModelID, "ep-reasoning")
			}
			if req.ChatID != "oc_chat" {
				t.Fatalf("chat id = %q, want %q", req.ChatID, "oc_chat")
			}
			if req.OpenID != "ou_actor" {
				t.Fatalf("open id = %q, want %q", req.OpenID, "ou_actor")
			}
			if req.InputText != "帮我把结果整理一下" {
				t.Fatalf("input text = %q, want %q", req.InputText, "帮我把结果整理一下")
			}
			if req.CapabilityName != "echo_cap" {
				t.Fatalf("capability name = %q, want %q", req.CapabilityName, "echo_cap")
			}
			if req.Result.OutputText != "echo:hello" {
				t.Fatalf("result output = %q, want %q", req.Result.OutputText, "echo:hello")
			}
			return ark_dal.ContentStruct{
				Thought: "结合能力结果组织最终回复",
				Reply:   "我已经把 echo 的结果整理好了。",
			}, nil
		},
	)

	plan, err := planner.PlanCapabilityReply(context.Background(), CapabilityReplyPlanningRequest{
		Session: &AgentSession{
			ChatID: "oc_chat",
		},
		Run: &AgentRun{
			ID:          "run_1",
			ActorOpenID: "ou_actor",
			InputText:   "帮我把结果整理一下",
		},
		Step: &AgentStep{
			ID:             "step_cap",
			CapabilityName: "echo_cap",
		},
		CapabilityName: "echo_cap",
		Result: CapabilityResult{
			OutputText: "echo:hello",
		},
	})
	if err != nil {
		t.Fatalf("PlanCapabilityReply() error = %v", err)
	}
	if plan.ThoughtText != "结合能力结果组织最终回复" {
		t.Fatalf("thought text = %q, want %q", plan.ThoughtText, "结合能力结果组织最终回复")
	}
	if plan.ReplyText != "我已经把 echo 的结果整理好了。" {
		t.Fatalf("reply text = %q, want %q", plan.ReplyText, "我已经把 echo 的结果整理好了。")
	}
}

func TestDefaultCapabilityReplyPlannerFallsBackToStandardModel(t *testing.T) {
	planner := newDefaultCapabilityReplyPlannerForTest(
		func(context.Context, string, string) capabilityReplyPlannerModelSelection {
			return capabilityReplyPlannerModelSelection{
				Mode:    appconfig.ChatModeStandard,
				ModelID: "ep-normal",
			}
		},
		func(ctx context.Context, req capabilityReplyPlannerGenerationRequest) (ark_dal.ContentStruct, error) {
			if req.ModelID != "ep-normal" {
				t.Fatalf("model id = %q, want %q", req.ModelID, "ep-normal")
			}
			return ark_dal.ContentStruct{
				Reply: "普通模式也能生成收尾。",
			}, nil
		},
	)

	plan, err := planner.PlanCapabilityReply(context.Background(), CapabilityReplyPlanningRequest{
		Session: &AgentSession{ChatID: "oc_chat"},
		Run: &AgentRun{
			ActorOpenID: "ou_actor",
			InputText:   "整理结果",
		},
		CapabilityName: "echo_cap",
		Result: CapabilityResult{
			OutputJSON: []byte(`{"echo":"hello"}`),
		},
	})
	if err != nil {
		t.Fatalf("PlanCapabilityReply() error = %v", err)
	}
	if plan.ReplyText != "普通模式也能生成收尾。" {
		t.Fatalf("reply text = %q, want %q", plan.ReplyText, "普通模式也能生成收尾。")
	}
}

func TestCapabilityReplyPlannerSystemPromptEncouragesConversationalReply(t *testing.T) {
	prompt := capabilityReplyPlannerSystemPrompt()
	if !strings.Contains(prompt, "像群里正常接话") {
		t.Fatalf("prompt = %q, want contain conversational tone hint", prompt)
	}
	if !strings.Contains(prompt, "不要写成工具执行报告") {
		t.Fatalf("prompt = %q, want contain non-reporting hint", prompt)
	}
}
