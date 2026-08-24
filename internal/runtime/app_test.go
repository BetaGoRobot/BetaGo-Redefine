package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppLogsOptionalModuleFailure(t *testing.T) {
	oldLogf := optionalModuleErrorLogf
	var logged string
	optionalModuleErrorLogf = func(format string, args ...any) {
		logged = fmt.Sprintf(format, args...)
	}
	t.Cleanup(func() { optionalModuleErrorLogf = oldLogf })

	app := NewApp(NewFuncModule(FuncModuleOptions{
		Name: "telemetry",
		Start: func(context.Context) error {
			return errors.New("collector unavailable")
		},
	}))
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for _, want := range []string{"telemetry", "start", "collector unavailable"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("optional module log = %q, want it to contain %q", logged, want)
		}
	}
	status := componentByName(t, app.Registry().Snapshot(), "telemetry")
	if status.State != StateDegraded {
		t.Fatalf("optional module state = %s, want %s", status.State, StateDegraded)
	}
}

func TestAppModuleNamesPreservesRegistrationOrderAndReturnsDefensiveCopy(t *testing.T) {
	app := NewApp(
		NewFuncModule(FuncModuleOptions{Name: "first"}),
		NewFuncModule(FuncModuleOptions{Name: "second"}),
	)

	names := app.ModuleNames()
	if want := []string{"first", "second"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("ModuleNames() = %v, want %v", names, want)
	}

	names[0] = "mutated"
	if got := app.ModuleNames(); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("ModuleNames() exposed internal storage: %v", got)
	}
}

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

func TestRegistrySnapshotReadsLiveModuleStatsOutsideRegistryLock(t *testing.T) {
	app := NewApp()
	value := int64(1)
	app.AddModule(NewFuncModule(FuncModuleOptions{
		Name: "live-stats",
		Stats: func() map[string]any {
			// This would deadlock if Snapshot invoked providers while holding
			// the registry read lock.
			app.Registry().Update("side-effect", StateReady, "", nil)
			return map[string]any{"value": value}
		},
	}))

	firstDone := make(chan Snapshot, 1)
	go func() {
		firstDone <- app.Registry().Snapshot()
	}()
	var first Snapshot
	select {
	case first = <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("Snapshot deadlocked while invoking live stats provider")
	}
	if got := componentStatsValue(first, "live-stats"); got != int64(1) {
		t.Fatalf("first live stats value = %#v, want 1", got)
	}

	value = 2
	second := app.Registry().Snapshot()
	if got := componentStatsValue(second, "live-stats"); got != int64(2) {
		t.Fatalf("second live stats value = %#v, want 2", got)
	}
}

type dynamicHealthTestModule struct {
	registry *Registry
	state    State
	message  string
	panicNow bool
}

var _ Module = (*dynamicHealthTestModule)(nil)
var _ StatsProvider = (*dynamicHealthTestModule)(nil)
var _ DynamicHealthProvider = (*dynamicHealthTestModule)(nil)

func (*dynamicHealthTestModule) Name() string                { return "dynamic-worker" }
func (*dynamicHealthTestModule) Critical() bool              { return false }
func (*dynamicHealthTestModule) Init(context.Context) error  { return nil }
func (*dynamicHealthTestModule) Start(context.Context) error { return nil }
func (*dynamicHealthTestModule) Ready(context.Context) error { return nil }
func (*dynamicHealthTestModule) Stop(context.Context) error  { return nil }
func (*dynamicHealthTestModule) Stats() map[string]any       { return map[string]any{"live": true} }
func (m *dynamicHealthTestModule) DynamicHealth() (State, string) {
	// Snapshot must invoke this outside the registry lock.
	m.registry.Update("health-side-effect", StateReady, "", nil)
	if m.panicNow {
		panic("dynamic health boom")
	}
	return m.state, m.message
}

func TestRegistryAppliesDynamicHealthOutsideLockAndRecoversToReady(t *testing.T) {
	app := NewApp()
	module := &dynamicHealthTestModule{
		registry: app.Registry(),
		state:    StateDegraded,
		message:  "three consecutive failures",
	}
	app.AddModule(module)
	app.Registry().Update(module.Name(), StateReady, "", nil)
	app.Registry().SetLive(true)

	done := make(chan Snapshot, 1)
	go func() {
		done <- app.Registry().Snapshot()
	}()
	var degraded Snapshot
	select {
	case degraded = <-done:
	case <-time.After(time.Second):
		t.Fatal("Snapshot deadlocked while invoking dynamic health provider")
	}
	status := componentByName(t, degraded, module.Name())
	if status.State != StateDegraded || status.Message != module.message ||
		!degraded.Degraded || !degraded.Ready {
		t.Fatalf("degraded dynamic snapshot = %#v", degraded)
	}

	module.state = StateReady
	module.message = ""
	recovered := app.Registry().Snapshot()
	status = componentByName(t, recovered, module.Name())
	if status.State != StateReady || status.Message != "" ||
		recovered.Degraded || !recovered.Ready {
		t.Fatalf("recovered dynamic snapshot = %#v", recovered)
	}

	module.panicNow = true
	panicked := app.Registry().Snapshot()
	status = componentByName(t, panicked, module.Name())
	if status.State != StateDegraded || status.Message == "" ||
		!panicked.Degraded || !panicked.Ready {
		t.Fatalf("panicked dynamic snapshot = %#v", panicked)
	}
}

func componentByName(t *testing.T, snapshot Snapshot, name string) ComponentStatus {
	t.Helper()
	for _, component := range snapshot.Components {
		if component.Name == name {
			return component
		}
	}
	t.Fatalf("component %q not found in %#v", name, snapshot)
	return ComponentStatus{}
}

func componentStatsValue(snapshot Snapshot, name string) any {
	for _, component := range snapshot.Components {
		if component.Name == name {
			return component.Stats["value"]
		}
	}
	return nil
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

func TestExecutorSubmitAndStopAreSafeWhenConcurrent(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		executor := NewExecutor(ExecutorConfig{
			Name:      "concurrent_stop_executor",
			Workers:   1,
			QueueSize: 1,
		})
		if err := executor.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		release := make(chan struct{})
		if err := executor.Submit(context.Background(), "block-worker", func(context.Context) error {
			<-release
			return nil
		}); err != nil {
			t.Fatalf("Submit(block-worker) error = %v", err)
		}

		var submitters sync.WaitGroup
		submitters.Add(16)
		for index := 0; index < 16; index++ {
			go func() {
				defer submitters.Done()
				err := executor.Submit(context.Background(), "concurrent", func(context.Context) error {
					return nil
				})
				if err != nil &&
					!errors.Is(err, ErrExecutorClosed) &&
					!errors.Is(err, ErrExecutorQueueFull) {
					t.Errorf("Submit() error = %v", err)
				}
			}()
		}

		stopped := make(chan error, 1)
		go func() {
			stopped <- executor.Stop(context.Background())
		}()
		close(release)
		submitters.Wait()
		if err := <-stopped; err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}
}
