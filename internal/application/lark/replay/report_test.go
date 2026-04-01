package replay

import (
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestReplayReportRenderTextIncludesCoreSections(t *testing.T) {
	report := ReplayReport{
		Target: ReplayTarget{
			ChatID:    "oc_chat",
			MessageID: "om_msg",
			OpenID:    "ou_actor",
			ChatType:  "group",
			Text:      "帮我总结今天讨论",
		},
		Cases: []ReplayCase{
			{
				Name:                 ReplayCaseBaseline,
				IntentContextEnabled: false,
				IntentInput:          "当前消息:\n帮我总结今天讨论",
			},
			{
				Name:                 ReplayCaseAugmented,
				IntentContextEnabled: true,
				HistoryLimit:         4,
				ProfileLimit:         2,
				IntentInput:          "当前消息:\n帮我总结今天讨论\n\n最近上下文(新到旧):\n[10:00] <Alice>: 先看上周数据",
				IntentContext: ReplayIntentContext{
					HistoryLines: []string{"[10:00] <Alice>: 先看上周数据"},
					ProfileLines: []string{"画像线索: role=pm"},
				},
				IntentAnalysis: &intent.IntentAnalysis{
					IntentType:      intent.IntentTypeQuestion,
					NeedReply:       true,
					ReplyConfidence: 88,
					InteractionMode: intent.InteractionModeAgentic,
					ReplyMode:       intent.ReplyModeDirect,
					NeedsHistory:    true,
					NeedsWeb:        false,
					ReasoningEffort: responses.ReasoningEffort_medium,
				},
				RouteDecision: &ReplayRouteDecision{
					FinalMode:         "agentic",
					ForcedByRuntime:   false,
					ForcedDirectReply: false,
				},
			},
		},
		Diff: ReplayDiff{
			IntentInputChanged:     true,
			InteractionModeChanged: true,
			RouteChanged:           true,
			ChangedFields: []string{
				"intent_input",
				"intent_analysis.interaction_mode",
				"route_decision.final_mode",
			},
		},
	}

	text := report.RenderText()
	for _, want := range []string{
		"Target",
		"Baseline",
		"Augmented",
		"Diff Summary",
		"帮我总结今天讨论",
		"画像线索: role=pm",
		"intent_analysis.interaction_mode",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("RenderText() = %q, want contain %q", text, want)
		}
	}
}

func TestReplayDiffChangedFieldNamesFiltersEmptyValues(t *testing.T) {
	diff := ReplayDiff{
		IntentInputChanged: true,
		ChangedFields: []string{
			"intent_input",
			"",
			"route_decision.final_mode",
			"intent_input",
		},
	}

	got := diff.ChangedFieldNames()
	want := []string{"intent_input", "route_decision.final_mode"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ChangedFieldNames() = %v, want %v", got, want)
	}
}

func TestReplayReportRenderTextToleratesDryRunWithoutIntentAnalysis(t *testing.T) {
	report := ReplayReport{
		Target: ReplayTarget{Text: "只看 dry-run"},
		Cases: []ReplayCase{
			{
				Name:        ReplayCaseBaseline,
				IntentInput: "只看 dry-run",
			},
		},
	}

	text := report.RenderText()
	if !strings.Contains(text, "intent_analysis: <dry-run>") {
		t.Fatalf("RenderText() = %q, want dry-run placeholder", text)
	}
}
