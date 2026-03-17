package schedule

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
)

func TestRegisterRegressionScenesRegistersScheduleScenes(t *testing.T) {
	registry := cardregression.NewRegistry()

	RegisterRegressionScenes(registry)

	for _, key := range []string{"schedule.list", "schedule.query"} {
		scene, ok := registry.Get(key)
		if !ok || scene == nil {
			t.Fatalf("expected scene %q to be registered", key)
		}
	}
}

func TestScheduleListSceneBuildTestCard(t *testing.T) {
	useWorkspaceConfigPath(t)
	scene := scheduleListRegressionScene{}

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
	if !strings.Contains(jsonStr, "Schedule 列表") || !strings.Contains(jsonStr, "午休提醒") {
		t.Fatalf("expected schedule list payload, got %s", jsonStr)
	}
}

func TestScheduleQuerySceneBuildTestCard(t *testing.T) {
	useWorkspaceConfigPath(t)
	scene := scheduleQueryRegressionScene{}

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
	if !strings.Contains(jsonStr, "Schedule 查询") || !strings.Contains(jsonStr, "20260312093000-debugA") {
		t.Fatalf("expected schedule query payload, got %s", jsonStr)
	}
}

func TestScheduleScenesLiveCasesDeclareRuntimeRequirements(t *testing.T) {
	listScene := scheduleListRegressionScene{}
	queryScene := scheduleQueryRegressionScene{}

	listLive := listScene.TestCases()[1]
	if !listLive.Requires.NeedBusinessChatID || !listLive.Requires.NeedDB {
		t.Fatalf("expected schedule.list live case to require chat_id and db, got %+v", listLive.Requires)
	}

	queryLive := queryScene.TestCases()[1]
	if !queryLive.Requires.NeedObjectID || !queryLive.Requires.NeedDB {
		t.Fatalf("expected schedule.query live case to require object_id and db, got %+v", queryLive.Requires)
	}
}
