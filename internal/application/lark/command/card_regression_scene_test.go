package command

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
)

func TestRegisterRegressionScenesRegistersHelpAndCommandForm(t *testing.T) {
	registry := cardregression.NewRegistry()

	RegisterRegressionScenes(registry)

	for _, key := range []string{"help.view", "command.form"} {
		scene, ok := registry.Get(key)
		if !ok || scene == nil {
			t.Fatalf("expected scene %q to be registered", key)
		}
	}
}

func TestHelpViewSceneBuildTestCard(t *testing.T) {
	scene := helpViewRegressionScene{}
	built, err := scene.BuildTestCard(context.Background(), cardregression.TestCardBuildRequest{
		Case: cardregression.CardRegressionCase{
			Name: "smoke-default",
			Args: map[string]string{"command": "config set"},
		},
		Args:   map[string]string{"command": "config set"},
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("BuildTestCard() error = %v", err)
	}
	if built == nil || built.Mode != cardregression.BuiltCardModeCardJSON || len(built.CardJSON) == 0 {
		t.Fatalf("expected non-empty card json, got %+v", built)
	}
}

func TestCommandFormSceneBuildTestCard(t *testing.T) {
	scene := commandFormRegressionScene{}
	built, err := scene.BuildTestCard(context.Background(), cardregression.TestCardBuildRequest{
		Case: cardregression.CardRegressionCase{
			Name: "smoke-default",
			Args: map[string]string{"command": "/config set --key=intent_recognition_enabled"},
		},
		Args:   map[string]string{"command": "/config set --key=intent_recognition_enabled"},
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("BuildTestCard() error = %v", err)
	}
	if built == nil || built.Mode != cardregression.BuiltCardModeCardJSON || len(built.CardJSON) == 0 {
		t.Fatalf("expected non-empty card json, got %+v", built)
	}
}
