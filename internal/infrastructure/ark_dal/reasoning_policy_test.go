package ark_dal

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestPrepareResponsesRequestFiltersReasoningByModelAllowlist(t *testing.T) {
	reasoning := &responses.ResponsesReasoning{Effort: responses.ReasoningEffort_high}
	original := &responses.ResponsesRequest{Model: "unsupported-model", Reasoning: reasoning}

	prepared := prepareResponsesRequest(&config.ArkConfig{
		ReasoningEffortModels: []string{" supported-model ", "", "supported-model"},
	}, original)

	if prepared == original {
		t.Fatal("prepareResponsesRequest() returned the caller-owned request")
	}
	if prepared.GetReasoning() != nil {
		t.Fatalf("prepared Reasoning = %+v, want nil", prepared.GetReasoning())
	}
	if original.GetReasoning() != reasoning {
		t.Fatalf("original Reasoning = %+v, want unchanged", original.GetReasoning())
	}
}

func TestPrepareResponsesRequestKeepsReasoningForExactAllowlistedModel(t *testing.T) {
	reasoning := &responses.ResponsesReasoning{Effort: responses.ReasoningEffort_low}
	original := &responses.ResponsesRequest{Model: "supported-model", Reasoning: reasoning}

	prepared := prepareResponsesRequest(&config.ArkConfig{
		ReasoningEffortModels: []string{" supported-model "},
	}, original)

	if prepared.GetReasoning() != reasoning {
		t.Fatalf("prepared Reasoning = %+v, want original reasoning", prepared.GetReasoning())
	}
}
