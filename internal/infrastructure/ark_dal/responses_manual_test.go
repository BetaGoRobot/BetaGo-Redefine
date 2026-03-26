package ark_dal

import (
	"testing"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/bytedance/gg/gptr"
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

func TestBuildTurnRequestAppendsAdditionalToolsForContinuation(t *testing.T) {
	turn := New[struct{}]("oc_chat", "ou_actor", nil).WithTools(
		arktools.New[struct{}]().Add(
			arktools.NewUnit[struct{}]().
				Name("search_history").
				Desc("搜索历史").
				Params(arktools.NewParams("object")),
		),
	)

	req, err := turn.buildTurnRequest(ResponseTurnRequest{
		ModelID:            "ep-test",
		PreviousResponseID: "resp_prev",
		ToolOutput:         &ToolOutputInput{CallID: "call_1", Output: "ok"},
		AdditionalTools: []*responses.ResponsesTool{
			testResponseFunctionTool("finance_market_data_get"),
		},
	})
	if err != nil {
		t.Fatalf("buildTurnRequest() error = %v", err)
	}
	if req.GetPreviousResponseId() != "resp_prev" {
		t.Fatalf("previous response id = %q, want %q", req.GetPreviousResponseId(), "resp_prev")
	}
	if len(req.Tools) != 2 {
		t.Fatalf("tool count = %d, want 2", len(req.Tools))
	}
	if req.Tools[1].GetToolFunction().GetName() != "finance_market_data_get" {
		t.Fatalf("additional tool name = %q, want %q", req.Tools[1].GetToolFunction().GetName(), "finance_market_data_get")
	}
}

func TestBuildTurnRequestDedupesAdditionalToolsByName(t *testing.T) {
	turn := New[struct{}]("oc_chat", "ou_actor", nil).WithTools(
		arktools.New[struct{}]().Add(
			arktools.NewUnit[struct{}]().
				Name("finance_market_data_get").
				Desc("默认工具").
				Params(arktools.NewParams("object")),
		),
	)

	req, err := turn.buildTurnRequest(ResponseTurnRequest{
		ModelID:      "ep-test",
		SystemPrompt: "system",
		UserPrompt:   "user",
		AdditionalTools: []*responses.ResponsesTool{
			testResponseFunctionTool("finance_market_data_get"),
		},
	})
	if err != nil {
		t.Fatalf("buildTurnRequest() error = %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(req.Tools))
	}
}

func testResponseFunctionTool(name string) *responses.ResponsesTool {
	return &responses.ResponsesTool{
		Union: &responses.ResponsesTool_ToolFunction{
			ToolFunction: &responses.ToolFunction{
				Name:        name,
				Type:        responses.ToolType_function,
				Description: gptr.Of("dynamic tool"),
				Parameters:  &responses.Bytes{Value: []byte(`{"type":"object","properties":{}}`)},
			},
		},
	}
}
