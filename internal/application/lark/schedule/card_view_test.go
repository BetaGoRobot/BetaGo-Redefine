package schedule

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func useWorkspaceConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

func TestBuildTaskListCardUsesSchemaV2AndFooter(t *testing.T) {
	useWorkspaceConfigPath(t)
	card := BuildTaskListCard(context.Background(), "Schedule 查询", []*model.ScheduledTask{
		{
			ID:              "task-1",
			Name:            "早报提醒",
			Status:          model.ScheduleTaskStatusEnabled,
			Type:            model.ScheduleTaskTypeCron,
			ToolName:        "send_message",
			CronExpr:        "0 9 * * 1-5",
			Timezone:        model.ScheduleTaskDefaultTimezone,
			SourceMessageID: "om_source",
		},
		{
			ID:       "task-2",
			Name:     "晚间复盘",
			Status:   model.ScheduleTaskStatusPaused,
			Type:     model.ScheduleTaskTypeOnce,
			ToolName: "search_history",
			Timezone: model.ScheduleTaskDefaultTimezone,
		},
	}, NewTaskQueryCardView("", TaskQuery{Name: "提醒"}, 20))

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"schema":"2.0"`) {
		t.Fatalf("expected schema v2 card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"template":"wathet"`) || !strings.Contains(jsonStr, `"padding":"12px"`) {
		t.Fatalf("expected unified panel style in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `早报提醒`) {
		t.Fatalf("expected task name in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, "来源消息: `om_source`") {
		t.Fatalf("expected source message in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"撤回"`) || !strings.Contains(jsonStr, `更新于 `) {
		t.Fatalf("expected footer actions in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"刷新"`) || !strings.Contains(jsonStr, cardactionproto.ActionScheduleView) {
		t.Fatalf("expected schedule refresh action in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, cardactionproto.ActionSchedulePause) ||
		!strings.Contains(jsonStr, cardactionproto.ActionScheduleResume) ||
		!strings.Contains(jsonStr, cardactionproto.ActionScheduleDelete) {
		t.Fatalf("expected schedule actions in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"恢复"`) || !strings.Contains(jsonStr, `"type":"primary_filled"`) {
		t.Fatalf("expected filled primary style for resume action: %s", jsonStr)
	}
}
