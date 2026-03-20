package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
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
