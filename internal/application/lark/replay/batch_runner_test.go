package replay

import (
	"context"
	"errors"
	"testing"
)

func TestReplayBatchRunnerContinuesAfterPerSampleFailuresAndAggregatesSummary(t *testing.T) {
	runner := ReplayBatchRunner{
		replay: func(_ context.Context, chatID, messageID string, _ ReplayRunOptions) (ReplayReport, error) {
			if chatID != "oc_chat" {
				t.Fatalf("chatID = %q, want %q", chatID, "oc_chat")
			}
			switch messageID {
			case "om_success":
				return ReplayReport{
					Target: ReplayTarget{ChatID: "oc_chat", MessageID: "om_success"},
					Cases: []ReplayCase{
						{Name: ReplayCaseBaseline, RouteDecision: &ReplayRouteDecision{FinalMode: "standard"}},
						{Name: ReplayCaseAugmented, RouteDecision: &ReplayRouteDecision{FinalMode: "agentic"}},
					},
					Diff: ReplayDiff{
						RouteChanged:      true,
						GenerationChanged: true,
					},
				}, nil
			case "om_partial":
				return ReplayReport{
					Target: ReplayTarget{ChatID: "oc_chat", MessageID: "om_partial"},
					Cases: []ReplayCase{
						{Name: ReplayCaseBaseline, RouteDecision: &ReplayRouteDecision{FinalMode: "agentic"}},
						{
							Name:          ReplayCaseAugmented,
							RouteDecision: &ReplayRouteDecision{FinalMode: "agentic"},
							Conversation: &ReplayConversation{
								ToolIntent: &ReplayToolIntent{
									WouldCallTools: true,
									FunctionName:   "search_history",
								},
							},
						},
					},
					Diff: ReplayDiff{
						ToolIntentChanged: true,
					},
				}, errors.New("generation timeout")
			default:
				return ReplayReport{}, errors.New("load failed")
			}
		},
	}

	result, err := runner.Run(context.Background(), ReplayBatchRequest{
		ChatID:   "oc_chat",
		ChatName: "Alpha",
		Days:     3,
		Samples: []ReplaySample{
			{MessageID: "om_success", ChatID: "oc_chat"},
			{MessageID: "om_partial", ChatID: "oc_chat"},
			{MessageID: "om_failed", ChatID: "oc_chat"},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Cases) != 3 {
		t.Fatalf("len(result.Cases) = %d, want 3", len(result.Cases))
	}
	if result.Cases[0].Status != ReplayBatchCaseStatusSuccess {
		t.Fatalf("case[0].Status = %q, want success", result.Cases[0].Status)
	}
	if result.Cases[1].Status != ReplayBatchCaseStatusPartial {
		t.Fatalf("case[1].Status = %q, want partial", result.Cases[1].Status)
	}
	if result.Cases[2].Status != ReplayBatchCaseStatusFailed {
		t.Fatalf("case[2].Status = %q, want failed", result.Cases[2].Status)
	}
	if result.Summary.SuccessCount != 1 || result.Summary.PartialCount != 1 || result.Summary.FailedCount != 1 {
		t.Fatalf("summary counts = %+v, want 1/1/1", result.Summary)
	}
	if result.Summary.SelectedSampleCount != 3 {
		t.Fatalf("SelectedSampleCount = %d, want 3", result.Summary.SelectedSampleCount)
	}
	if result.Summary.BaselineStandardCount != 1 || result.Summary.BaselineAgenticCount != 1 {
		t.Fatalf("baseline mode counts = %+v", result.Summary)
	}
	if result.Summary.AugmentedStandardCount != 0 || result.Summary.AugmentedAgenticCount != 2 {
		t.Fatalf("augmented mode counts = %+v", result.Summary)
	}
	if result.Summary.RouteChangedCount != 1 || result.Summary.GenerationChangedCount != 1 || result.Summary.ToolIntentChangedCount != 1 {
		t.Fatalf("diff counts = %+v", result.Summary)
	}
}
