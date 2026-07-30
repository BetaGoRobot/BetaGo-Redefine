package handlers

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
)

func TestLarkToolsForContextOnlyIncludesAgentCardToolsWhenScoped(t *testing.T) {
	t.Setenv(
		"BETAGO_CONFIG_PATH",
		"/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml",
	)
	if _, exists := larktools(context.Background()).Get("compose_card"); exists {
		t.Fatal("compose_card exposed without scoped service")
	}
	ctx := agentcardtool.WithService(
		context.Background(),
		&agentCardToolServiceFake{},
	)
	if _, exists := larktools(ctx).Get("compose_card"); !exists {
		t.Fatal("compose_card missing with scoped service")
	}
}
