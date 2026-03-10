package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAppAllowsOptionalDegradedModule(t *testing.T) {
	app := NewApp(
		NewFuncModule(FuncModuleOptions{
			Name:     "critical",
			Critical: true,
		}),
		NewFuncModule(FuncModuleOptions{
			Name:     "optional",
			Critical: false,
			Ready: func(context.Context) error {
				return errors.New("degraded")
			},
		}),
	)

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := app.Registry().Snapshot()
	if !snapshot.Live || !snapshot.Ready || !snapshot.Degraded {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestAppFailsWhenCriticalModuleIsNotReady(t *testing.T) {
	app := NewApp(
		NewFuncModule(FuncModuleOptions{
			Name:     "critical",
			Critical: true,
			Ready: func(context.Context) error {
				return errors.New("boom")
			},
		}),
	)

	if err := app.Start(context.Background()); err == nil {
		t.Fatal("expected Start() to fail")
	}
}

func TestExecutorProcessesTasksAndStops(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{
		Name:      "test_executor",
		Workers:   2,
		QueueSize: 4,
	})
	if err := executor.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	done := make(chan struct{}, 1)
	if err := executor.Submit(context.Background(), "task", func(context.Context) error {
		done <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task was not executed")
	}

	if err := executor.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	stats := executor.Stats()
	if stats["completed"].(int64) == 0 {
		t.Fatalf("expected completed tasks, got stats=%+v", stats)
	}
}
