package ark_dal

import (
	"testing"

	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestBuildTurnRequestUsesExplicitReasoningEffort(t *testing.T) {
	turn := New[struct{}]("oc_chat", "ou_actor", nil)

	req, err := turn.buildTurnRequest(ResponseTurnRequest{
		ModelID:         "ep-test",
		SystemPrompt:    "system prompt",
		UserPrompt:      "user prompt",
		ReasoningEffort: responses.ReasoningEffort_high,
	})
	if err != nil {
		t.Fatalf("buildTurnRequest() error = %v", err)
	}
	if req.GetReasoning() == nil || req.GetReasoning().GetEffort() != responses.ReasoningEffort_high {
		t.Fatalf("reasoning effort = %+v, want %v", req.GetReasoning(), responses.ReasoningEffort_high)
	}
}

func TestBuildTurnRequestDefaultsReasoningEffortToMedium(t *testing.T) {
	turn := New[struct{}]("oc_chat", "ou_actor", nil)

	req, err := turn.buildTurnRequest(ResponseTurnRequest{
		ModelID:      "ep-test",
		SystemPrompt: "system prompt",
		UserPrompt:   "user prompt",
	})
	if err != nil {
		t.Fatalf("buildTurnRequest() error = %v", err)
	}
	if req.GetReasoning() == nil || req.GetReasoning().GetEffort() != responses.ReasoningEffort_medium {
		t.Fatalf("reasoning effort = %+v, want %v", req.GetReasoning(), responses.ReasoningEffort_medium)
	}
}
