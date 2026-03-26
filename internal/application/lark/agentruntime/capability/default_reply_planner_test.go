package capability

import (
	"context"
	"strings"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
)

func TestDefaultReplyPlannerUsesConfiguredModelAndReturnsStructuredReply(t *testing.T) {
	planner := newDefaultReplyPlannerForTest(
		func(context.Context, string, string) replyPlannerModelSelection {
			return replyPlannerModelSelection{
				Mode:    appconfig.ChatModeAgentic,
				ModelID: "ep-reasoning",
			}
		},
		func(ctx context.Context, req replyPlannerGenerationRequest) (ark_dal.ContentStruct, error) {
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

	plan, err := planner.PlanCapabilityReply(context.Background(), ReplyPlanningRequest{
		ChatID:         "oc_chat",
		OpenID:         "ou_actor",
		InputText:      "帮我把结果整理一下",
		CapabilityName: "echo_cap",
		Result: Result{
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

func TestDefaultReplyPlannerFallsBackToStandardModel(t *testing.T) {
	planner := newDefaultReplyPlannerForTest(
		func(context.Context, string, string) replyPlannerModelSelection {
			return replyPlannerModelSelection{
				Mode:    appconfig.ChatModeStandard,
				ModelID: "ep-normal",
			}
		},
		func(ctx context.Context, req replyPlannerGenerationRequest) (ark_dal.ContentStruct, error) {
			if req.ModelID != "ep-normal" {
				t.Fatalf("model id = %q, want %q", req.ModelID, "ep-normal")
			}
			return ark_dal.ContentStruct{
				Reply: "普通模式也能生成收尾。",
			}, nil
		},
	)

	plan, err := planner.PlanCapabilityReply(context.Background(), ReplyPlanningRequest{
		ChatID:         "oc_chat",
		OpenID:         "ou_actor",
		InputText:      "整理结果",
		CapabilityName: "echo_cap",
		Result: Result{
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

func TestReplyPlannerSystemPromptEncouragesConversationalReply(t *testing.T) {
	prompt := replyPlannerSystemPrompt()
	if !strings.Contains(prompt, "像群里正常接话") {
		t.Fatalf("prompt = %q, want contain conversational tone hint", prompt)
	}
	if !strings.Contains(prompt, "不要写成工具执行报告") {
		t.Fatalf("prompt = %q, want contain non-reporting hint", prompt)
	}
	if !strings.Contains(prompt, "少用语气词") {
		t.Fatalf("prompt = %q, want contain filler-word constraint", prompt)
	}
	if !strings.Contains(prompt, "不要为了显得亲近而堆砌“哟”“呀”“啦”这类口头禅") {
		t.Fatalf("prompt = %q, want contain anthropomorphic-particle constraint", prompt)
	}
}
