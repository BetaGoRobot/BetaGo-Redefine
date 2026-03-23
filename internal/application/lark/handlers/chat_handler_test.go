package handlers

import (
	"context"
	"strings"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
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
	meta.SetExtra(intent.MetaKeyInteractionMode, string(intent.InteractionModeStandard))

	if got := resolveChatExecutionMode(meta, appconfig.ChatModeAgentic); got != appconfig.ChatModeStandard {
		t.Fatalf("resolveChatExecutionMode() = %q, want %q", got, appconfig.ChatModeStandard)
	}

	meta.SetExtra(intent.MetaKeyInteractionMode, string(intent.InteractionModeAgentic))
	if got := resolveChatExecutionMode(meta, appconfig.ChatModeStandard); got != appconfig.ChatModeAgentic {
		t.Fatalf("resolveChatExecutionMode() = %q, want %q", got, appconfig.ChatModeAgentic)
	}
}

func TestResolveChatExecutionModeFallsBackToConfiguredMode(t *testing.T) {
	if got := resolveChatExecutionMode(&xhandler.BaseMetaData{}, appconfig.ChatModeAgentic); got != appconfig.ChatModeAgentic {
		t.Fatalf("resolveChatExecutionMode() = %q, want %q", got, appconfig.ChatModeAgentic)
	}
}
