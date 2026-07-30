package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
)

func TestContinuationGeneratorStrictlyParsesDecisionAndBuildsSafePrompt(t *testing.T) {
	var captured ark_dal.CachedResponseRequest
	generator := NewContinuationGenerator("model-test")
	generator.responseText = func(
		_ context.Context,
		req ark_dal.CachedResponseRequest,
		_ llmusage.Scope,
	) (string, error) {
		captured = req
		return `{"decision":"reply","reply":"已修改","reason":"用户需要确认"}`, nil
	}
	decision, err := generator.Generate(context.Background(), ContinuationContext{
		RunID: "run-1", Goal: "修改日程", ChatID: "oc-chat", ActorOpenID: "ou-user",
		LatestOutcome: ConversationEvent{
			Type:    EventTypeCapabilityResult,
			Payload: []byte(`{"status":"updated"}`),
		},
		RecentSteps: []*AgentStep{{
			Kind: StepKindWait, Status: StepStatusCompleted,
			InputJSON: `{"token_hash":"secret-token-hash"}`,
			WorkerID:  "secret-worker", DedupeKey: "secret-dedupe",
		}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if decision.Decision != TurnDecisionReply || decision.Reply != "已修改" {
		t.Fatalf("decision = %#v", decision)
	}
	if captured.Text == nil || captured.Text.Format == nil ||
		captured.Reasoning == nil || captured.Thinking == nil {
		t.Fatalf("response controls = %#v", captured)
	}
	if !strings.Contains(captured.SystemPrompt, "不得重复") ||
		!strings.Contains(captured.SystemPrompt, "observe_only") {
		t.Fatalf("system prompt = %q", captured.SystemPrompt)
	}
	for _, secret := range []string{"secret-token-hash", "secret-worker", "secret-dedupe"} {
		if strings.Contains(captured.UserPrompt, secret) {
			t.Fatalf("user prompt leaked %q: %s", secret, captured.UserPrompt)
		}
	}
}

func TestContinuationGeneratorRejectsUnknownTrailingAndInvalidReplyShapes(t *testing.T) {
	cases := []string{
		`{"decision":"reply","reply":"","reason":"x"}`,
		`{"decision":"observe_only","reply":"unexpected","reason":"x"}`,
		`{"decision":"close","reply":"","reason":"x","extra":true}`,
		`{"decision":"wait","reply":"","reason":"x"} trailing`,
	}
	for _, response := range cases {
		t.Run(response, func(t *testing.T) {
			generator := NewContinuationGenerator("model-test")
			generator.responseText = func(
				context.Context,
				ark_dal.CachedResponseRequest,
				llmusage.Scope,
			) (string, error) {
				return response, nil
			}
			if _, err := generator.Generate(context.Background(), ContinuationContext{
				RunID: "run-1", ChatID: "oc-chat",
				LatestOutcome: ConversationEvent{Type: EventTypeCapabilityResult, Payload: []byte(`{}`)},
			}); err == nil {
				t.Fatal("Generate() error = nil")
			}
		})
	}
}
