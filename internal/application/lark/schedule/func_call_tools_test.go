package schedule

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

func TestFilterQueriedSchedules(t *testing.T) {
	tasks := []*model.ScheduledTask{
		{
			ID:        "task-1",
			Name:      "早报提醒",
			CreatorID: "ou_creator_1",
			Status:    model.ScheduleTaskStatusEnabled,
			Type:      model.ScheduleTaskTypeCron,
			ToolName:  "send_message",
		},
		{
			ID:        "task-2",
			Name:      "晚间复盘",
			CreatorID: "ou_creator_2",
			Status:    model.ScheduleTaskStatusPaused,
			Type:      model.ScheduleTaskTypeOnce,
			ToolName:  "search_history",
		},
	}

	filtered := FilterTasks(tasks, TaskQuery{
		Name:     "提醒",
		Status:   model.ScheduleTaskStatusEnabled,
		Type:     model.ScheduleTaskTypeCron,
		ToolName: "send_message",
	})
	if len(filtered) != 1 {
		t.Fatalf("unexpected filtered count: %d", len(filtered))
	}
	if filtered[0].ID != "task-1" {
		t.Fatalf("unexpected filtered task: %+v", filtered[0])
	}
}

func TestFilterQueriedSchedulesByCreatorOpenID(t *testing.T) {
	tasks := []*model.ScheduledTask{
		{ID: "task-1", CreatorID: "ou_creator_1"},
		{ID: "task-2", CreatorID: "ou_creator_2"},
	}

	filtered := FilterTasks(tasks, TaskQuery{CreatorOpenID: "ou_creator_2"})
	if len(filtered) != 1 {
		t.Fatalf("unexpected filtered count: %d", len(filtered))
	}
	if filtered[0].ID != "task-2" {
		t.Fatalf("unexpected filtered task: %+v", filtered[0])
	}
}
