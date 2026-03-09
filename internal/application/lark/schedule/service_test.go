package schedule

import (
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
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
	task := model.NewScheduledTask("test", model.ScheduleTaskTypeOnce, "chat", "user", "send_message", `{"content":"hello"}`, model.ScheduleTaskDefaultTimezone)
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
