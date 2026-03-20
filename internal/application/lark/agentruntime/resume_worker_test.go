package agentruntime_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

type fakeResumeQueueStore struct {
	mu           sync.Mutex
	events       chan agentruntime.ResumeEvent
	acquireOK    bool
	acquireCalls []string
	releaseCalls []string
}

func newFakeResumeQueueStore() *fakeResumeQueueStore {
	return &fakeResumeQueueStore{
		events:    make(chan agentruntime.ResumeEvent, 8),
		acquireOK: true,
	}
}

func (s *fakeResumeQueueStore) Enqueue(event agentruntime.ResumeEvent) {
	s.events <- event
}

func (s *fakeResumeQueueStore) DequeueResumeEvent(ctx context.Context, timeout time.Duration) (*agentruntime.ResumeEvent, error) {
	select {
	case event := <-s.events:
		return &event, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, nil
	}
}

func (s *fakeResumeQueueStore) AcquireRunLock(ctx context.Context, runID, owner string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acquireCalls = append(s.acquireCalls, runID)
	return s.acquireOK, nil
}

func (s *fakeResumeQueueStore) ReleaseRunLock(ctx context.Context, runID, owner string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseCalls = append(s.releaseCalls, runID)
	return true, nil
}

func (s *fakeResumeQueueStore) snapshot() (acquireCalls []string, releaseCalls []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.acquireCalls...), append([]string(nil), s.releaseCalls...)
}

type fakeRunProcessor struct {
	handled chan agentruntime.RunProcessorInput
}

func (p *fakeRunProcessor) ProcessRun(ctx context.Context, input agentruntime.RunProcessorInput) error {
	p.handled <- input
	return nil
}

func TestResumeWorkerProcessesQueuedEventWithRunLock(t *testing.T) {
	store := newFakeResumeQueueStore()
	processor := &fakeRunProcessor{handled: make(chan agentruntime.RunProcessorInput, 1)}
	worker := agentruntime.NewResumeWorker(store, processor)

	event := agentruntime.ResumeEvent{
		RunID:    "run_01",
		Revision: 3,
		Source:   agentruntime.ResumeSourceCallback,
		Token:    "cb_token",
	}
	store.Enqueue(event)

	worker.Start()
	defer worker.Stop()

	select {
	case handled := <-processor.handled:
		if handled.Resume == nil || !reflect.DeepEqual(*handled.Resume, event) {
			t.Fatalf("handled input = %+v, want resume %+v", handled, event)
		}
	case <-time.After(time.Second):
		t.Fatal("expected resume worker to process queued event")
	}

	waitForResumeWorkerRelease(t, store, "run_01")

	acquireCalls, releaseCalls := store.snapshot()
	if len(acquireCalls) != 1 || acquireCalls[0] != "run_01" {
		t.Fatalf("AcquireRunLock() calls = %+v, want [run_01]", acquireCalls)
	}
	if len(releaseCalls) != 1 || releaseCalls[0] != "run_01" {
		t.Fatalf("ReleaseRunLock() calls = %+v, want [run_01]", releaseCalls)
	}
	stats := worker.Stats()
	if stats["processed"] != int64(1) {
		t.Fatalf("processed stats = %+v", stats)
	}
}

func TestResumeWorkerSkipsWhenRunLockIsHeld(t *testing.T) {
	store := newFakeResumeQueueStore()
	store.acquireOK = false
	processor := &fakeRunProcessor{handled: make(chan agentruntime.RunProcessorInput, 1)}
	worker := agentruntime.NewResumeWorker(store, processor)

	store.Enqueue(agentruntime.ResumeEvent{
		RunID:    "run_02",
		Revision: 4,
		Source:   agentruntime.ResumeSourceSchedule,
	})

	worker.Start()
	defer worker.Stop()

	select {
	case handled := <-processor.handled:
		t.Fatalf("expected event to be skipped when lock is held, got %+v", handled)
	case <-time.After(200 * time.Millisecond):
	}

	acquireCalls, releaseCalls := store.snapshot()
	if len(acquireCalls) != 1 || acquireCalls[0] != "run_02" {
		t.Fatalf("AcquireRunLock() calls = %+v, want [run_02]", acquireCalls)
	}
	if len(releaseCalls) != 0 {
		t.Fatalf("ReleaseRunLock() calls = %+v, want none", releaseCalls)
	}
	stats := worker.Stats()
	if stats["skipped_locked"] != int64(1) {
		t.Fatalf("skipped_locked stats = %+v", stats)
	}
}

func waitForResumeWorkerRelease(t *testing.T, store *fakeResumeQueueStore, runID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, releaseCalls := store.snapshot()
		for _, call := range releaseCalls {
			if call == runID {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, releaseCalls := store.snapshot()
	t.Fatalf("ReleaseRunLock() calls = %+v, want contain %q", releaseCalls, runID)
}
