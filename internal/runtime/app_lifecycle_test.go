package runtime

import (
	"bytes"
	"context"
	"errors"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAppCleansCurrentModuleBeforeCriticalRollback(t *testing.T) {
	for _, stage := range []string{"init", "start", "ready"} {
		t.Run(stage, func(t *testing.T) {
			var stopped []string
			failure := errors.New("startup failed")
			first := NewFuncModule(FuncModuleOptions{Name: "first", Stop: func(context.Context) error {
				stopped = append(stopped, "first")
				return nil
			}})
			second := NewFuncModule(FuncModuleOptions{Name: "second", Stop: func(context.Context) error {
				stopped = append(stopped, "second")
				return nil
			}})
			opts := FuncModuleOptions{Name: "broken", Critical: true, Stop: func(context.Context) error {
				stopped = append(stopped, "broken")
				return nil
			}}
			fail := func(context.Context) error { return failure }
			switch stage {
			case "init":
				opts.Init = fail
			case "start":
				opts.Start = fail
			case "ready":
				opts.Ready = fail
			}
			laterStarted := false
			app := NewApp(first, second, NewFuncModule(opts), NewFuncModule(FuncModuleOptions{
				Name: "later", Start: func(context.Context) error { laterStarted = true; return nil },
			}))
			if err := app.Start(context.Background()); !errors.Is(err, failure) {
				t.Fatalf("Start error = %v, want original failure", err)
			}
			if err := app.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if want := []string{"broken", "second", "first"}; !reflect.DeepEqual(stopped, want) {
				t.Fatalf("Stop calls = %v, want %v exactly once", stopped, want)
			}
			if laterStarted {
				t.Fatal("module after critical failure was started")
			}
			snapshot := app.Registry().Snapshot()
			if snapshot.Live || snapshot.Ready {
				t.Fatalf("failed app is live/ready: %+v", snapshot)
			}
			for _, name := range []string{"first", "second"} {
				if state := componentByName(t, snapshot, name).State; state != StateStopped {
					t.Errorf("%s state = %s, want stopped", name, state)
				}
			}
			if status := componentByName(t, snapshot, "broken"); !strings.Contains(status.Message, failure.Error()) {
				t.Errorf("failed module lost startup error: %+v", status)
			}
		})
	}
}

func TestAppCleansOptionalFailuresAndPreservesReadyDegradation(t *testing.T) {
	for _, stage := range []string{"init", "start", "ready"} {
		t.Run(stage, func(t *testing.T) {
			stops := 0
			opts := FuncModuleOptions{Name: "optional", Stop: func(context.Context) error { stops++; return nil }}
			fail := func(context.Context) error { return errors.New("unavailable") }
			switch stage {
			case "init":
				opts.Init = fail
			case "start":
				opts.Start = fail
			case "ready":
				opts.Ready = fail
			}
			laterStarted := false
			app := NewApp(NewFuncModule(opts), NewFuncModule(FuncModuleOptions{
				Name: "later", Start: func(context.Context) error { laterStarted = true; return nil },
			}))
			if err := app.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			wantStops := 1
			if stage == "ready" {
				wantStops = 0
			}
			if stops != wantStops {
				t.Errorf("Stop calls after Start = %d, want %d", stops, wantStops)
			}
			if !laterStarted || componentByName(t, app.Registry().Snapshot(), "optional").State != StateDegraded {
				t.Fatal("optional failure must degrade and continue")
			}
			if err := app.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if stops != 1 {
				t.Errorf("total Stop calls = %d, want exactly 1", stops)
			}
		})
	}
}

func TestAppDisablesModulesWithoutRetainingResources(t *testing.T) {
	for _, stage := range []string{"init", "start", "ready"} {
		t.Run(stage, func(t *testing.T) {
			stops := 0
			opts := FuncModuleOptions{Name: "disabled", Critical: true, Stop: func(context.Context) error { stops++; return nil }}
			disabled := func(context.Context) error { return ErrDisabled }
			switch stage {
			case "init":
				opts.Init = disabled
			case "start":
				opts.Start = disabled
			case "ready":
				opts.Ready = disabled
			}
			app := NewApp(NewFuncModule(opts))
			if err := app.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			wantStops := 1
			if stage == "init" {
				wantStops = 0
			}
			if stops != wantStops {
				t.Errorf("Stop calls before shutdown = %d, want %d", stops, wantStops)
			}
			if err := app.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if stops != wantStops {
				t.Errorf("Stop called again for disabled module: %d", stops)
			}
			if state := componentByName(t, app.Registry().Snapshot(), "disabled").State; state != StateDisabled {
				t.Errorf("state = %s, want disabled", state)
			}
		})
	}
}

func TestAppRollsBackWithFreshBoundedContext(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "trace-value"))
	defer cancel()
	var stopped []string
	stop := func(name string) func(context.Context) error {
		return func(cleanupCtx context.Context) error {
			stopped = append(stopped, name)
			if cleanupCtx.Err() != nil {
				t.Errorf("cleanup inherited startup cancellation: %v", cleanupCtx.Err())
			}
			if _, ok := cleanupCtx.Deadline(); !ok {
				t.Error("cleanup has no deadline")
			}
			if cleanupCtx.Value(contextKey{}) != "trace-value" {
				t.Error("cleanup lost context values")
			}
			return nil
		}
	}
	app := NewApp(
		NewFuncModule(FuncModuleOptions{Name: "first", Stop: stop("first")}),
		NewFuncModule(FuncModuleOptions{Name: "broken", Critical: true, Start: func(context.Context) error {
			cancel()
			return context.Canceled
		}, Stop: stop("broken")}),
	)
	if err := app.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want canceled", err)
	}
	if !reflect.DeepEqual(stopped, []string{"broken", "first"}) {
		t.Fatalf("Stop calls = %v", stopped)
	}
}

func TestAppPreservesStartupAndCleanupFailures(t *testing.T) {
	startupErr := errors.New("start failure")
	currentStopErr := errors.New("current stop failure")
	previousStopErr := errors.New("previous stop failure")
	app := NewApp(
		NewFuncModule(FuncModuleOptions{Name: "first", Stop: func(context.Context) error { return previousStopErr }}),
		NewFuncModule(FuncModuleOptions{Name: "broken", Critical: true,
			Start: func(context.Context) error { return startupErr },
			Stop:  func(context.Context) error { return currentStopErr },
		}),
	)
	err := app.Start(context.Background())
	for _, want := range []error{startupErr, currentStopErr, previousStopErr} {
		if !errors.Is(err, want) {
			t.Errorf("Start error = %v, missing %v", err, want)
		}
	}
	snapshot := app.Registry().Snapshot()
	for _, name := range []string{"first", "broken"} {
		if state := componentByName(t, snapshot, name).State; state != StateFailed {
			t.Errorf("%s state = %s, want failed", name, state)
		}
	}
	if message := componentByName(t, snapshot, "broken").Message; !strings.Contains(message, currentStopErr.Error()) {
		t.Errorf("cleanup failure missing from status: %s", message)
	}
}

func TestAppCleanupFailureRemainsVisibleForOptionalModule(t *testing.T) {
	var output bytes.Buffer
	stops := 0
	app := NewAppWithOptions(AppOptions{Logger: log.New(&output, "", 0)}, NewFuncModule(FuncModuleOptions{
		Name: "optional",
		Init: func(context.Context) error { return errors.New("init failure") },
		Stop: func(context.Context) error { stops++; return errors.New("cleanup failure") },
	}))
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("optional cleanup failure aborted startup: %v", err)
	}
	status := componentByName(t, app.Registry().Snapshot(), "optional")
	if status.State != StateDegraded {
		t.Fatalf("state = %s, want degraded", status.State)
	}
	for _, want := range []string{"init failure", "cleanup failure"} {
		if !strings.Contains(status.Message, want) || !strings.Contains(output.String(), want) {
			t.Errorf("failure %q missing from status %q or log %q", want, status.Message, output.String())
		}
	}
	if err := app.Stop(context.Background()); err != nil || stops != 1 {
		t.Fatalf("Stop error = %v, calls = %d, want no duplicate cleanup", err, stops)
	}
}

func TestAppCleanupFailureCannotSilentlyDisableModule(t *testing.T) {
	for _, critical := range []bool{false, true} {
		for _, stage := range []string{"start", "ready"} {
			name := stage + "/optional"
			if critical {
				name = stage + "/critical"
			}
			t.Run(name, func(t *testing.T) {
				cleanupErr := errors.New("cleanup failed")
				var stopped []string
				opts := FuncModuleOptions{Name: "disabled", Critical: critical, Stop: func(context.Context) error {
					stopped = append(stopped, "disabled")
					return cleanupErr
				}}
				if stage == "start" {
					opts.Start = func(context.Context) error { return ErrDisabled }
				} else {
					opts.Ready = func(context.Context) error { return ErrDisabled }
				}
				laterStarted := false
				app := NewApp(
					NewFuncModule(FuncModuleOptions{Name: "first", Stop: func(context.Context) error {
						stopped = append(stopped, "first")
						return nil
					}}),
					NewFuncModule(opts),
					NewFuncModule(FuncModuleOptions{Name: "later", Start: func(context.Context) error { laterStarted = true; return nil }}),
				)
				err := app.Start(context.Background())
				wantState := StateDegraded
				if critical {
					wantState = StateFailed
					if !errors.Is(err, cleanupErr) || !errors.Is(err, ErrDisabled) || laterStarted {
						t.Errorf("critical cleanup failure: error=%v, later started=%v", err, laterStarted)
					}
				} else if err != nil || !laterStarted {
					t.Errorf("optional cleanup should continue: error=%v, later started=%v", err, laterStarted)
				}
				status := componentByName(t, app.Registry().Snapshot(), "disabled")
				if status.State != wantState || !strings.Contains(status.Message, cleanupErr.Error()) {
					t.Errorf("cleanup error lost in status: %+v", status)
				}
				if err := app.Stop(context.Background()); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(stopped, []string{"disabled", "first"}) {
					t.Errorf("Stop calls = %v, want disabled then first exactly once", stopped)
				}
			})
		}
	}
}

func TestAppCleanupTimeoutIsSharedAcrossRollback(t *testing.T) {
	var firstDeadline time.Time
	var rollbackCalled bool
	startupErr := errors.New("startup failed")
	app := NewAppWithOptions(AppOptions{CleanupTimeout: 10 * time.Millisecond},
		NewFuncModule(FuncModuleOptions{Name: "first", Stop: func(ctx context.Context) error {
			rollbackCalled = true
			deadline, ok := ctx.Deadline()
			if !ok || !deadline.Equal(firstDeadline) {
				t.Errorf("rollback got a new cleanup budget: %v, first %v", deadline, firstDeadline)
			}
			return ctx.Err()
		}}),
		NewFuncModule(FuncModuleOptions{Name: "broken", Critical: true,
			Start: func(context.Context) error { return startupErr },
			Stop: func(ctx context.Context) error {
				var ok bool
				firstDeadline, ok = ctx.Deadline()
				if !ok {
					t.Error("no cleanup deadline")
					return errors.New("missing deadline")
				}
				<-ctx.Done()
				return ctx.Err()
			},
		}),
	)
	err := app.Start(context.Background())
	if !errors.Is(err, startupErr) || !errors.Is(err, context.DeadlineExceeded) || !rollbackCalled {
		t.Fatalf("error = %v, rollback called = %v", err, rollbackCalled)
	}
}

func TestAppPreservesNormalStopOrderAndErrors(t *testing.T) {
	var stopped []string
	firstErr, secondErr := errors.New("first failure"), errors.New("second failure")
	app := NewApp(
		NewFuncModule(FuncModuleOptions{Name: "first", Stop: func(context.Context) error {
			stopped = append(stopped, "first")
			return firstErr
		}}),
		NewFuncModule(FuncModuleOptions{Name: "second", Stop: func(context.Context) error {
			stopped = append(stopped, "second")
			return secondErr
		}}),
	)
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.Stop(context.Background()); !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Stop lost errors: %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stopped, []string{"second", "first"}) {
		t.Errorf("Stop calls = %v, want second then first exactly once", stopped)
	}
}
