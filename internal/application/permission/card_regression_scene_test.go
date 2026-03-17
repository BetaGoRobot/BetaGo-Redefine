package permission

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
)

func TestRegisterRegressionScenesRegistersPermissionManage(t *testing.T) {
	registry := cardregression.NewRegistry()

	RegisterRegressionScenes(registry)

	scene, ok := registry.Get("permission.manage")
	if !ok || scene == nil {
		t.Fatal("expected permission.manage scene to be registered")
	}
}

func TestPermissionManageSceneBuildTestCard(t *testing.T) {
	scene := permissionManageRegressionScene{}

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
	if !strings.Contains(jsonStr, "权限面板") || !strings.Contains(jsonStr, "当前目标") {
		t.Fatalf("expected permission panel payload, got %s", jsonStr)
	}
}

func TestPermissionManageSceneLiveCaseRequiresChatAndActor(t *testing.T) {
	scene := permissionManageRegressionScene{}

	cases := scene.TestCases()
	if len(cases) < 2 {
		t.Fatalf("expected live case to exist, got %+v", cases)
	}
	live := cases[1]
	if live.Name != "live-default" {
		t.Fatalf("expected live-default case, got %+v", live)
	}
	if !live.Requires.NeedBusinessChatID || !live.Requires.NeedActorOpenID {
		t.Fatalf("expected live case to require chat and actor, got %+v", live.Requires)
	}
}
