package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

func TestChatGenerationPlanGenerateReturnsNotConfiguredWithoutRegisteredExecutor(t *testing.T) {
	agentruntime.SetChatGenerationPlanExecutor(nil)

	_, err := (agentruntime.ChatGenerationPlan{}).Generate(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when executor is not registered")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveChatExecutionModeUsesInteractionModeOverride(t *testing.T) {
	meta := &xhandler.BaseMetaData{}
	meta.SetIntentAnalysis(&intent.IntentAnalysis{InteractionMode: intent.InteractionModeStandard})

	if got := resolveChatExecutionMode(meta); got != intent.InteractionModeStandard {
		t.Fatalf("resolveChatExecutionMode() = %q, want %q", got, intent.InteractionModeStandard)
	}

	meta.SetIntentAnalysis(&intent.IntentAnalysis{InteractionMode: intent.InteractionModeAgentic})
	if got := resolveChatExecutionMode(meta); got != intent.InteractionModeAgentic {
		t.Fatalf("resolveChatExecutionMode() = %q, want %q", got, intent.InteractionModeAgentic)
	}
}

func TestResolveChatExecutionModeDefaultsToStandardWithoutDecision(t *testing.T) {
	if got := resolveChatExecutionMode(&xhandler.BaseMetaData{}); got != intent.InteractionModeStandard {
		t.Fatalf("resolveChatExecutionMode() = %q, want %q", got, intent.InteractionModeStandard)
	}
}
