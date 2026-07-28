package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	scheduleinfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/schedule"
	"github.com/bytedance/mockey"
)

func TestComputeNextRun(t *testing.T) {
	from := time.Date(2026, 3, 9, 0, 30, 0, 0, time.UTC)

	next, err := computeNextRun("0 9 * * 1-5", "Asia/Shanghai", from)
	if err != nil {
		t.Fatalf("computeNextRun returned error: %v", err)
	}

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	expected := time.Date(2026, 3, 9, 9, 0, 0, 0, loc)
	if !next.Equal(expected) {
		t.Fatalf("unexpected next run: got %s want %s", next.Format(time.RFC3339), expected.Format(time.RFC3339))
	}
}

func TestUpdateTaskSameTimeFieldsDoesNotMoveNextRunAt(t *testing.T) {
	nextRunAt := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	task := &model.ScheduledTask{
		ID: "task-1", CreatorID: "ou-actor", Type: model.ScheduleTaskTypeCron,
		CronExpr: "0 10 * * *", Timezone: "Asia/Shanghai", NextRunAt: nextRunAt,
	}
	repo := &scheduleinfra.Repository{}
	getPatch := mockey.Mock((*scheduleinfra.Repository).GetTaskByID).
		Return(task, nil).
		Build()
	updateCalls := 0
	updatePatch := mockey.Mock((*scheduleinfra.Repository).UpdateTaskFields).
		To(func(*scheduleinfra.Repository, context.Context, string, map[string]any) error {
			updateCalls++
			return nil
		}).
		Build()
	t.Cleanup(func() {
		updatePatch.UnPatch()
		getPatch.UnPatch()
	})
	service := NewService(repo, &ToolExecutor{}, botidentity.Identity{AppID: "app-test"})
	sameCron := task.CronExpr
	sameTimezone := task.Timezone

	got, err := service.UpdateTask(context.Background(), &UpdateTaskRequest{
		ID: "task-1", ActorOpenID: "ou-actor",
		CronExpr: &sameCron, Timezone: &sameTimezone,
	})

	if !errors.Is(err, ErrNoScheduleFieldsToUpdate) {
		t.Fatalf("UpdateTask() error = %v, want ErrNoScheduleFieldsToUpdate", err)
	}
	if got != nil {
		t.Fatalf("UpdateTask() task = %#v, want nil with existing no-op error contract", got)
	}
	if updateCalls != 0 {
		t.Fatalf("UpdateTaskFields() calls = %d, want 0", updateCalls)
	}
	if !task.NextRunAt.Equal(nextRunAt) {
		t.Fatalf("next_run_at drifted: got %v want %v", task.NextRunAt, nextRunAt)
	}
}

func TestUpdateTaskSameAbsoluteRunAtDoesNotMoveNextRunAt(t *testing.T) {
	runAt := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	task := &model.ScheduledTask{
		ID: "task-once", CreatorID: "ou-actor", Type: model.ScheduleTaskTypeOnce,
		Timezone: model.ScheduleTaskDefaultTimezone, RunAt: &runAt, NextRunAt: runAt,
	}
	repo := &scheduleinfra.Repository{}
	getPatch := mockey.Mock((*scheduleinfra.Repository).GetTaskByID).
		Return(task, nil).
		Build()
	updateCalls := 0
	updatePatch := mockey.Mock((*scheduleinfra.Repository).UpdateTaskFields).
		To(func(*scheduleinfra.Repository, context.Context, string, map[string]any) error {
			updateCalls++
			return nil
		}).
		Build()
	t.Cleanup(func() {
		updatePatch.UnPatch()
		getPatch.UnPatch()
	})
	service := NewService(repo, &ToolExecutor{}, botidentity.Identity{AppID: "app-test"})
	sameInstant := runAt.In(time.FixedZone("UTC+8", 8*60*60))

	got, err := service.UpdateTask(context.Background(), &UpdateTaskRequest{
		ID: "task-once", ActorOpenID: "ou-actor", RunAt: &sameInstant,
	})

	if !errors.Is(err, ErrNoScheduleFieldsToUpdate) || got != nil {
		t.Fatalf("UpdateTask() = %#v, %v; want no-op sentinel", got, err)
	}
	if updateCalls != 0 || !task.NextRunAt.Equal(runAt) {
		t.Fatalf("update calls = %d, next_run_at = %v, want unchanged %v",
			updateCalls, task.NextRunAt, runAt)
	}
}

func TestValidateToolArgs(t *testing.T) {
	if err := validateToolArgs(`{"content":"hello"}`); err != nil {
		t.Fatalf("expected valid JSON, got %v", err)
	}
	if err := validateToolArgs(`{"content":`); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestComputeResumeRunForPastOnceTask(t *testing.T) {
	now := time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC)
	runAt := now.Add(-10 * time.Minute)
	task := model.NewScheduledTask("test", model.ScheduleTaskTypeOnce, "chat", "user", "send_message", `{"content":"hello"}`, model.ScheduleTaskDefaultTimezone, "app-test", "bot-test")
	task.RunAt = &runAt
	task.Status = model.ScheduleTaskStatusPaused

	svc := &Service{}
	nextRunAt, err := svc.computeResumeRun(task, now)
	if err != nil {
		t.Fatalf("computeResumeRun returned error: %v", err)
	}
	if !nextRunAt.Equal(now) {
		t.Fatalf("unexpected resume time: got %s want %s", nextRunAt.Format(time.RFC3339), now.Format(time.RFC3339))
	}
}
