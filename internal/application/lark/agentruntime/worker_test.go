package agentruntime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
)

type conversationWorkerRuntimeStub struct {
	submitted   int
	expired     int
	submitErr   error
	expireErr   error
	submitCalls int
	expireCalls int
}

func (s *conversationWorkerRuntimeStub) SubmitDueContinuations(context.Context, int) (int, error) {
	s.submitCalls++
	return s.submitted, s.submitErr
}

func (s *conversationWorkerRuntimeStub) ExpireInteractions(context.Context, time.Time, int) (int, error) {
	s.expireCalls++
	return s.expired, s.expireErr
}

type projectionWorkerRuntimeStub struct {
	results []error
	calls   int
}

func (s *projectionWorkerRuntimeStub) SubmitNextProjection(context.Context) error {
	s.calls++
	if len(s.results) == 0 {
		return ErrNotFound
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result
}

func TestConversationWorkerRunsBoundedRecoveryCycleAndReportsLiveStats(t *testing.T) {
	runtime := &conversationWorkerRuntimeStub{submitted: 3, expired: 2}
	worker, err := NewConversationWorker(runtime, ConversationWorkerOptions{
		Interval: time.Second, BatchSize: 32,
		Now: func() time.Time {
			return time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("NewConversationWorker() error = %v", err)
	}
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce() error = %v", err)
	}
	if runtime.submitCalls != 1 || runtime.expireCalls != 1 {
		t.Fatalf("calls submit=%d expire=%d, want 1/1", runtime.submitCalls, runtime.expireCalls)
	}
	stats := worker.Stats()
	if stats["last_submitted"] != 3 || stats["last_expired"] != 2 ||
		stats["last_success_at"] == "" || stats["last_error"] != nil {
		t.Fatalf("worker stats = %#v", stats)
	}
	if worker.Critical() {
		t.Fatal("conversation worker must be non-critical because its gate is dynamic")
	}
	if state, message := worker.DynamicHealth(); state != appruntime.StateReady || message != "" {
		t.Fatalf("successful worker health = %q/%q, want ready", state, message)
	}
}

func TestConversationWorkerBackoffIsBoundedAndCancellable(t *testing.T) {
	runtime := &conversationWorkerRuntimeStub{submitErr: errors.New("postgres unavailable")}
	worker, err := NewConversationWorker(runtime, ConversationWorkerOptions{
		Interval: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond, BatchSize: 1,
		Jitter: func(delay time.Duration) time.Duration { return delay },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := worker.nextDelay(1); got != 20*time.Millisecond {
		t.Fatalf("first retry delay = %s, want 20ms", got)
	}
	if got := worker.nextDelay(20); got != 40*time.Millisecond {
		t.Fatalf("bounded retry delay = %s, want 40ms", got)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("idempotent Start() error = %v", err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
}

func TestProjectionWorkerDrainsBoundedBatchAndTreatsNotFoundAsIdle(t *testing.T) {
	runtime := &projectionWorkerRuntimeStub{results: []error{nil, nil, ErrNotFound}}
	worker, err := NewProjectionWorker(runtime, ProjectionWorkerOptions{
		Interval: time.Second, BatchSize: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce() error = %v", err)
	}
	if runtime.calls != 3 {
		t.Fatalf("projection calls = %d, want drain until ErrNotFound", runtime.calls)
	}
	stats := worker.Stats()
	if stats["last_submitted"] != 2 || stats["last_error"] != nil {
		t.Fatalf("projection worker stats = %#v", stats)
	}
	if worker.Critical() {
		t.Fatal("OpenSearch projection worker must always be non-critical")
	}
}

type panicThenRecoverRuntime struct {
	panicNext atomic.Bool
	calls     atomic.Int64
	entered   chan struct{}
}

func (s *panicThenRecoverRuntime) ExpireInteractions(context.Context, time.Time, int) (int, error) {
	return 0, nil
}

func (s *panicThenRecoverRuntime) SubmitDueContinuations(context.Context, int) (int, error) {
	s.calls.Add(1)
	s.entered <- struct{}{}
	if s.panicNext.Swap(false) {
		panic("runtime boom")
	}
	return 0, nil
}

func TestConversationWorkerPanicClearsRunningAndAllowsRestart(t *testing.T) {
	runtime := &panicThenRecoverRuntime{entered: make(chan struct{}, 4)}
	runtime.panicNext.Store(true)
	worker, err := NewConversationWorker(runtime, ConversationWorkerOptions{
		Interval: 5 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, BatchSize: 1,
		Jitter: func(delay time.Duration) time.Duration { return delay },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter panicking cycle")
	}
	waitForWorkerStat(t, worker.Stats, "running", false)
	stats := worker.Stats()
	if stats["terminal_error"] == nil || stats["last_error"] == nil {
		t.Fatalf("panic stats = %#v, want terminal and last error", stats)
	}
	if state, message := worker.DynamicHealth(); state != appruntime.StateDegraded || message == "" {
		t.Fatalf("panic health = %q/%q, want degraded terminal error", state, message)
	}

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("restart after panic error = %v", err)
	}
	select {
	case <-runtime.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not restart after panic")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if runtime.calls.Load() < 2 {
		t.Fatalf("runtime calls = %d, want panic and restarted cycle", runtime.calls.Load())
	}
}

func TestConversationWorkerHealthDegradesAfterThresholdAndRecovers(t *testing.T) {
	runtime := &conversationWorkerRuntimeStub{submitErr: errors.New("postgres unavailable")}
	worker, err := NewConversationWorker(runtime, ConversationWorkerOptions{
		Interval: time.Second, BatchSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_ = worker.runOnce(context.Background())
	}
	if state, _ := worker.DynamicHealth(); state != appruntime.StateReady {
		t.Fatalf("health before failure threshold = %q, want ready", state)
	}
	_ = worker.runOnce(context.Background())
	if state, message := worker.DynamicHealth(); state != appruntime.StateDegraded || message == "" {
		t.Fatalf("health at failure threshold = %q/%q, want degraded", state, message)
	}
	runtime.submitErr = nil
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state, message := worker.DynamicHealth(); state != appruntime.StateReady || message != "" {
		t.Fatalf("health after recovery = %q/%q, want ready", state, message)
	}
}

func TestProjectionWorkerHealthDegradesWithoutBecomingCritical(t *testing.T) {
	projectionErr := errors.New("opensearch unavailable")
	runtime := &projectionWorkerRuntimeStub{
		results: []error{projectionErr, projectionErr, projectionErr},
	}
	worker, err := NewProjectionWorker(runtime, ProjectionWorkerOptions{
		Interval: time.Second, BatchSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		_ = worker.runOnce(context.Background())
	}
	if state, message := worker.DynamicHealth(); state != appruntime.StateDegraded || message == "" {
		t.Fatalf("projection health = %q/%q, want degraded", state, message)
	}
	if worker.Critical() {
		t.Fatal("degraded projection worker became critical")
	}
	runtime.results = []error{ErrNotFound}
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state, message := worker.DynamicHealth(); state != appruntime.StateReady || message != "" {
		t.Fatalf("projection recovery health = %q/%q, want ready", state, message)
	}
}

type blockingConversationRuntime struct {
	entered chan struct{}
	release chan struct{}
	active  atomic.Int64
	max     atomic.Int64
}

func (s *blockingConversationRuntime) ExpireInteractions(context.Context, time.Time, int) (int, error) {
	return 0, nil
}

func (s *blockingConversationRuntime) SubmitDueContinuations(context.Context, int) (int, error) {
	active := s.active.Add(1)
	for {
		current := s.max.Load()
		if active <= current || s.max.CompareAndSwap(current, active) {
			break
		}
	}
	s.entered <- struct{}{}
	<-s.release
	s.active.Add(-1)
	return 0, nil
}

func TestConversationWorkerStartDuringStopCannotCreateSecondLoop(t *testing.T) {
	runtime := &blockingConversationRuntime{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	worker, err := NewConversationWorker(runtime, ConversationWorkerOptions{
		Interval: time.Hour, BatchSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-runtime.entered
	stopDone := make(chan error, 1)
	go func() {
		stopDone <- worker.Stop(context.Background())
	}()
	waitForWorkerStat(t, worker.Stats, "stopping", true)
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() during Stop() error = %v", err)
	}
	select {
	case <-runtime.entered:
		t.Fatal("Start() during Stop() launched a second loop")
	case <-time.After(30 * time.Millisecond):
	}
	close(runtime.release)
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}
	if runtime.max.Load() != 1 {
		t.Fatalf("maximum concurrent loops = %d, want 1", runtime.max.Load())
	}
}

func waitForWorkerStat(
	t *testing.T,
	stats func() map[string]any,
	key string,
	want any,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := stats()[key]; got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("worker stat %q = %#v, want %#v; stats=%#v", key, stats()[key], want, stats())
}
