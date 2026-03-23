package ops

import (
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
)

func TestResolveInteractionModeKeepsStandardChatStandard(t *testing.T) {
	got := resolveInteractionMode(
		appconfig.ChatModeStandard,
		intent.InteractionModeAgentic,
		agentruntime.ShadowObservation{},
		false,
		true,
	)
	if got != intent.InteractionModeStandard {
		t.Fatalf("resolveInteractionMode() = %q, want %q", got, intent.InteractionModeStandard)
	}
}

func TestResolveInteractionModePromotesActiveRunContinuations(t *testing.T) {
	cases := []agentruntime.ShadowObservation{
		{
			PolicyDecision: agentruntime.PolicyDecision{
				EnterRuntime:  true,
				AttachToRunID: "run_active",
			},
		},
		{
			PolicyDecision: agentruntime.PolicyDecision{
				EnterRuntime:   true,
				SupersedeRunID: "run_active",
			},
		},
	}

	for _, observation := range cases {
		got := resolveInteractionMode(
			appconfig.ChatModeAgentic,
			intent.InteractionModeStandard,
			observation,
			true,
			true,
		)
		if got != intent.InteractionModeAgentic {
			t.Fatalf("resolveInteractionMode(%+v) = %q, want %q", observation.PolicyDecision, got, intent.InteractionModeAgentic)
		}
	}
}

func TestResolveInteractionModeUsesIntentForExplicitMessages(t *testing.T) {
	got := resolveInteractionMode(
		appconfig.ChatModeAgentic,
		intent.InteractionModeAgentic,
		agentruntime.ShadowObservation{},
		false,
		true,
	)
	if got != intent.InteractionModeAgentic {
		t.Fatalf("resolveInteractionMode() = %q, want %q", got, intent.InteractionModeAgentic)
	}

	got = resolveInteractionMode(
		appconfig.ChatModeAgentic,
		intent.InteractionModeStandard,
		agentruntime.ShadowObservation{},
		false,
		true,
	)
	if got != intent.InteractionModeStandard {
		t.Fatalf("resolveInteractionMode() = %q, want %q", got, intent.InteractionModeStandard)
	}
}

func TestResolveInteractionModeSkipsIntentPromotionForPassiveReplies(t *testing.T) {
	got := resolveInteractionMode(
		appconfig.ChatModeAgentic,
		intent.InteractionModeAgentic,
		agentruntime.ShadowObservation{},
		false,
		false,
	)
	if got != intent.InteractionModeStandard {
		t.Fatalf("resolveInteractionMode() = %q, want %q", got, intent.InteractionModeStandard)
	}
}

func TestReplyChatOperatorDependsOnIntentRecognizeFetcher(t *testing.T) {
	deps := (&ReplyChatOperator{}).Depends()
	if len(deps) != 1 || deps[0] != IntentRecognizeFetcher {
		t.Fatalf("ReplyChatOperator.Depends() = %+v, want [%p]", deps, IntentRecognizeFetcher)
	}
}

func TestCommandOperatorDependsOnIntentRecognizeFetcher(t *testing.T) {
	deps := (&CommandOperator{}).Depends()
	if len(deps) != 1 || deps[0] != IntentRecognizeFetcher {
		t.Fatalf("CommandOperator.Depends() = %+v, want [%p]", deps, IntentRecognizeFetcher)
	}
}
