package ratelimit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
)

func TestRegisterRegressionScenesRegistersRateLimitStats(t *testing.T) {
	registry := cardregression.NewRegistry()

	RegisterRegressionScenes(registry)

	scene, ok := registry.Get("ratelimit.stats")
	if !ok || scene == nil {
		t.Fatal("expected ratelimit.stats scene to be registered")
	}
}

func TestRateLimitStatsSceneBuildTestCard(t *testing.T) {
	scene := rateLimitStatsRegressionScene{}

	built, err := scene.BuildTestCard(context.Background(), cardregression.TestCardBuildRequest{
		Case:   cardregression.CardRegressionCase{Name: "smoke-default"},
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("BuildTestCard() error = %v", err)
	}
	if built == nil || built.Mode != cardregression.BuiltCardModeCardJSON || len(built.CardJSON) == 0 {
		t.Fatalf("expected non-empty card json, got %+v", built)
	}

	raw, err := json.Marshal(built.CardJSON)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, "频控详情") || !strings.Contains(jsonStr, "最近发送") {
		t.Fatalf("expected stats panel payload, got %s", jsonStr)
	}
}
