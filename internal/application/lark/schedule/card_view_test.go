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
			CreatorID:       "ou_creator_1",
			Status:          model.ScheduleTaskStatusEnabled,
			Type:            model.ScheduleTaskTypeCron,
			ToolName:        "send_message",
			CronExpr:        "0 9 * * 1-5",
			Timezone:        model.ScheduleTaskDefaultTimezone,
			SourceMessageID: "om_source",
		},
		{
			ID:        "task-2",
			Name:      "晚间复盘",
			CreatorID: "ou_creator_2",
			Status:    model.ScheduleTaskStatusPaused,
			Type:      model.ScheduleTaskTypeOnce,
			ToolName:  "search_history",
			Timezone:  model.ScheduleTaskDefaultTimezone,
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
	if !strings.Contains(jsonStr, "来源: `om_source`") {
		t.Fatalf("expected source message in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"weight":3`) || !strings.Contains(jsonStr, `"weight":2`) {
		t.Fatalf("expected compact split column layout in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"horizontal_spacing":"12px"`) || !strings.Contains(jsonStr, `"flex_mode":"stretch"`) {
		t.Fatalf("expected compact row options in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"撤回"`) || !strings.Contains(jsonStr, `更新于 `) {
		t.Fatalf("expected footer actions in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"刷新"`) || !strings.Contains(jsonStr, cardactionproto.ActionScheduleView) {
		t.Fatalf("expected schedule refresh action in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) || !strings.Contains(jsonStr, `首次发送后可在此查看操作记录`) {
		t.Fatalf("expected shared action history panel in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, cardactionproto.ActionSchedulePause) ||
		!strings.Contains(jsonStr, cardactionproto.ActionScheduleResume) ||
		!strings.Contains(jsonStr, cardactionproto.ActionScheduleDelete) {
		t.Fatalf("expected schedule actions in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"恢复"`) || !strings.Contains(jsonStr, `"type":"primary_filled"`) {
		t.Fatalf("expected filled primary style for resume action: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, "创建者") || !strings.Contains(jsonStr, "ou_creator") {
		t.Fatalf("expected creator info and filter row in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"tag":"select_person"`) || !strings.Contains(jsonStr, `"element_id":"sched_creator_pick"`) {
		t.Fatalf("expected select_person creator picker in card json: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"options":[`) {
		t.Fatalf("expected creator picker to default to current chat members, not static options: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) || !strings.Contains(jsonStr, `操作记录`) {
		t.Fatalf("expected operation history panel in schedule card: %s", jsonStr)
	}
}

func TestBuildTaskListCardRehydratesCreatorPickerSelection(t *testing.T) {
	useWorkspaceConfigPath(t)
	card := BuildTaskListCard(context.Background(), "Schedule 查询", nil, NewTaskQueryCardView("", TaskQuery{
		CreatorOpenID: "ou_creator_selected",
	}, 20))

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"initial_option":"ou_creator_selected"`) {
		t.Fatalf("expected creator picker to rehydrate selected open id: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"当前筛选"`) || !strings.Contains(jsonStr, `"user_id":"ou_creator_selected"`) {
		t.Fatalf("expected creator filter summary to show selected open id: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"show_name":false`) {
		t.Fatalf("expected creator filter summary avatar-only display: %s", jsonStr)
	}
}
