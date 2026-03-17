package config

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
)

func TestRegisterRegressionScenesRegistersConfigAndFeature(t *testing.T) {
	registry := cardregression.NewRegistry()

	RegisterRegressionScenes(registry)

	for _, key := range []string{"config.list", "feature.list"} {
		scene, ok := registry.Get(key)
		if !ok || scene == nil {
			t.Fatalf("expected scene %q to be registered", key)
		}
	}
}

func TestConfigListSceneBuildTestCard(t *testing.T) {
	useWorkspaceConfigPath(t)
	scene := configListRegressionScene{}

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
	if !strings.Contains(jsonStr, "配置面板") || !strings.Contains(jsonStr, `"name":"config_selected_key"`) {
		t.Fatalf("expected config panel payload, got %s", jsonStr)
	}
}

func TestFeatureListSceneBuildTestCard(t *testing.T) {
	scene := featureListRegressionScene{}

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
	if !strings.Contains(string(raw), "功能开关") {
		t.Fatalf("expected feature panel payload, got %s", string(raw))
	}
}

func TestFeatureListSceneLiveCaseDeclaresRuntimeRequirements(t *testing.T) {
	scene := featureListRegressionScene{}

	cases := scene.TestCases()
	if len(cases) < 2 {
		t.Fatalf("expected live case to exist, got %+v", cases)
	}
	live := cases[1]
	if live.Name != "live-default" {
		t.Fatalf("expected live-default case, got %+v", live)
	}
	if !live.Requires.NeedBusinessChatID || !live.Requires.NeedActorOpenID || !live.Requires.NeedFeatureRegistry {
		t.Fatalf("expected live case to require chat, actor, and feature registry, got %+v", live.Requires)
	}
}
