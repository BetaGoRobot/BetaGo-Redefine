package agentruntime_test

import (
	"context"
	"errors"
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
	acquireSeq   []bool
	acquireCalls []string
	releaseCalls []string
	requeueCalls []agentruntime.ResumeEvent
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

func (s *fakeResumeQueueStore) EnqueueResumeEvent(ctx context.Context, event agentruntime.ResumeEvent) error {
	s.mu.Lock()
	s.requeueCalls = append(s.requeueCalls, event)
	s.mu.Unlock()
	s.events <- event
	return nil
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
	if len(s.acquireSeq) > 0 {
		acquired := s.acquireSeq[0]
		s.acquireSeq = s.acquireSeq[1:]
		return acquired, nil
	}
	return s.acquireOK, nil
}

func (s *fakeResumeQueueStore) ReleaseRunLock(ctx context.Context, runID, owner string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseCalls = append(s.releaseCalls, runID)
	return true, nil
}

func (s *fakeResumeQueueStore) snapshot() (acquireCalls []string, releaseCalls []string, requeueCalls []agentruntime.ResumeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.acquireCalls...), append([]string(nil), s.releaseCalls...), append([]agentruntime.ResumeEvent(nil), s.requeueCalls...)
}

type fakeRunProcessor struct {
	handled chan agentruntime.RunProcessorInput
}

func (p *fakeRunProcessor) ProcessRun(ctx context.Context, input agentruntime.RunProcessorInput) error {
	p.handled <- input
	return nil
}

type scriptedRunProcessor struct {
	mu      sync.Mutex
	results []error
	handled chan agentruntime.RunProcessorInput
}

func (p *scriptedRunProcessor) ProcessRun(ctx context.Context, input agentruntime.RunProcessorInput) error {
	if p.handled != nil {
		p.handled <- input
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.results) == 0 {
		return nil
	}
	result := p.results[0]
	p.results = p.results[1:]
	return result
}

type blockingRunProcessor struct {
	handled chan agentruntime.RunProcessorInput
	release <-chan struct{}
}

func (p *blockingRunProcessor) ProcessRun(ctx context.Context, input agentruntime.RunProcessorInput) error {
	p.handled <- input
	<-p.release
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

	acquireCalls, releaseCalls, _ := store.snapshot()
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

func TestResumeWorkerWithWorkersProcessesEventsInParallel(t *testing.T) {
	store := newFakeResumeQueueStore()
	release := make(chan struct{})
	processor := &blockingRunProcessor{
		handled: make(chan agentruntime.RunProcessorInput, 2),
		release: release,
	}
	worker := agentruntime.NewResumeWorker(store, processor).WithWorkers(2)

	store.Enqueue(agentruntime.ResumeEvent{
		RunID:    "run_parallel_01",
		Revision: 1,
		Source:   agentruntime.ResumeSourceCallback,
		Token:    "cb_token_01",
	})
	store.Enqueue(agentruntime.ResumeEvent{
		RunID:    "run_parallel_02",
		Revision: 1,
		Source:   agentruntime.ResumeSourceCallback,
		Token:    "cb_token_02",
	})

	worker.Start()
	defer worker.Stop()

	for i := 0; i < 2; i++ {
		select {
		case <-processor.handled:
		case <-time.After(250 * time.Millisecond):
			t.Fatal("expected both queued resume events to be handled concurrently")
		}
	}

	close(release)
	waitForResumeWorkerRelease(t, store, "run_parallel_01")
	waitForResumeWorkerRelease(t, store, "run_parallel_02")
}

type fakeRunResumer struct {
	seen   agentruntime.ResumeEvent
	result *agentruntime.AgentRun
	err    error
}

func (f *fakeRunResumer) ResumeRun(ctx context.Context, event agentruntime.ResumeEvent) (*agentruntime.AgentRun, error) {
	f.seen = event
	return f.result, f.err
}

type fakeResumeEnqueuer struct {
	seen   agentruntime.ResumeEvent
	called bool
	err    error
}

func (f *fakeResumeEnqueuer) EnqueueResumeEvent(ctx context.Context, event agentruntime.ResumeEvent) error {
	f.called = true
	f.seen = event
	return f.err
}

func TestResumeDispatcherDispatchResumesRunAndEnqueuesEvent(t *testing.T) {
	resumer := &fakeRunResumer{
		result: &agentruntime.AgentRun{ID: "run_01"},
	}
	enqueuer := &fakeResumeEnqueuer{}
	dispatcher := agentruntime.NewResumeDispatcher(resumer, enqueuer)

	event := agentruntime.ResumeEvent{
		RunID:      "run_01",
		Revision:   2,
		Source:     agentruntime.ResumeSourceCallback,
		Token:      "cb_token",
		OccurredAt: time.Date(2026, 3, 18, 16, 0, 0, 0, time.UTC),
	}
	run, err := dispatcher.Dispatch(context.Background(), event)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if run == nil || run.ID != "run_01" {
		t.Fatalf("Dispatch() run = %+v, want run_01", run)
	}
	if !reflect.DeepEqual(resumer.seen, event) {
		t.Fatalf("resumer saw %+v, want %+v", resumer.seen, event)
	}
	if !enqueuer.called || !reflect.DeepEqual(enqueuer.seen, event) {
		t.Fatalf("enqueuer saw %+v called=%v, want %+v", enqueuer.seen, enqueuer.called, event)
	}
}

func TestResumeDispatcherDispatchStopsWhenResumeFails(t *testing.T) {
	resumeErr := errors.New("resume failed")
	resumer := &fakeRunResumer{err: resumeErr}
	enqueuer := &fakeResumeEnqueuer{}
	dispatcher := agentruntime.NewResumeDispatcher(resumer, enqueuer)

	_, err := dispatcher.Dispatch(context.Background(), agentruntime.ResumeEvent{
		RunID:    "run_02",
		Revision: 1,
		Source:   agentruntime.ResumeSourceSchedule,
	})
	if !errors.Is(err, resumeErr) {
		t.Fatalf("Dispatch() error = %v, want %v", err, resumeErr)
	}
	if enqueuer.called {
		t.Fatal("expected enqueue to be skipped when resume fails")
	}
}

func TestResumeDispatcherDispatchEnqueuesApprovalWhenResumeOnlyRecordsDecision(t *testing.T) {
	resumer := &fakeRunResumer{}
	enqueuer := &fakeResumeEnqueuer{}
	dispatcher := agentruntime.NewResumeDispatcher(resumer, enqueuer)

	run, err := dispatcher.Dispatch(context.Background(), agentruntime.ResumeEvent{
		RunID:      "run_03",
		StepID:     "step_reserved",
		Revision:   4,
		Source:     agentruntime.ResumeSourceApproval,
		Token:      "approval_token",
		OccurredAt: time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if run != nil {
		t.Fatalf("Dispatch() run = %+v, want nil when resume only records decision", run)
	}
	if !enqueuer.called || !reflect.DeepEqual(enqueuer.seen, resumer.seen) {
		t.Fatalf("enqueuer saw %+v called=%v, want %+v", enqueuer.seen, enqueuer.called, resumer.seen)
	}
}

func TestResumeWorkerRequeuesWhenRunLockIsHeld(t *testing.T) {
	store := newFakeResumeQueueStore()
	store.acquireSeq = []bool{false, true}
	processor := &fakeRunProcessor{handled: make(chan agentruntime.RunProcessorInput, 1)}
	worker := agentruntime.NewResumeWorker(store, processor)

	event := agentruntime.ResumeEvent{
		RunID:    "run_02",
		Revision: 4,
		Source:   agentruntime.ResumeSourceSchedule,
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
		t.Fatal("expected requeued event to be processed after lock becomes available")
	}

	waitForResumeWorkerRelease(t, store, "run_02")

	acquireCalls, releaseCalls, requeueCalls := store.snapshot()
	if len(acquireCalls) != 2 || acquireCalls[0] != "run_02" || acquireCalls[1] != "run_02" {
		t.Fatalf("AcquireRunLock() calls = %+v, want [run_02 run_02]", acquireCalls)
	}
	if len(releaseCalls) != 1 || releaseCalls[0] != "run_02" {
		t.Fatalf("ReleaseRunLock() calls = %+v, want [run_02]", releaseCalls)
	}
	if len(requeueCalls) != 1 || !reflect.DeepEqual(requeueCalls[0], event) {
		t.Fatalf("EnqueueResumeEvent() calls = %+v, want [%+v]", requeueCalls, event)
	}
	stats := worker.Stats()
	if stats["skipped_locked"] != int64(1) {
		t.Fatalf("skipped_locked stats = %+v", stats)
	}
}

func TestResumeWorkerRequeuesDeferredApprovalResume(t *testing.T) {
	store := newFakeResumeQueueStore()
	processor := &scriptedRunProcessor{
		results: []error{agentruntime.ErrResumeDeferred, nil},
		handled: make(chan agentruntime.RunProcessorInput, 2),
	}
	worker := agentruntime.NewResumeWorker(store, processor)

	event := agentruntime.ResumeEvent{
		RunID:    "run_03",
		StepID:   "step_reserved",
		Revision: 5,
		Source:   agentruntime.ResumeSourceApproval,
		Token:    "approval_token",
	}
	store.Enqueue(event)

	worker.Start()
	defer worker.Stop()

	for i := 0; i < 2; i++ {
		select {
		case handled := <-processor.handled:
			if handled.Resume == nil || !reflect.DeepEqual(*handled.Resume, event) {
				t.Fatalf("handled input = %+v, want resume %+v", handled, event)
			}
		case <-time.After(time.Second):
			t.Fatal("expected deferred approval resume to be retried")
		}
	}

	waitForResumeWorkerRelease(t, store, "run_03")

	acquireCalls, releaseCalls, requeueCalls := store.snapshot()
	if len(acquireCalls) != 2 || acquireCalls[0] != "run_03" || acquireCalls[1] != "run_03" {
		t.Fatalf("AcquireRunLock() calls = %+v, want [run_03 run_03]", acquireCalls)
	}
	if len(releaseCalls) != 2 || releaseCalls[0] != "run_03" || releaseCalls[1] != "run_03" {
		t.Fatalf("ReleaseRunLock() calls = %+v, want [run_03 run_03]", releaseCalls)
	}
	if len(requeueCalls) != 1 || !reflect.DeepEqual(requeueCalls[0], event) {
		t.Fatalf("EnqueueResumeEvent() calls = %+v, want [%+v]", requeueCalls, event)
	}
}

func TestResumeWorkerRequeuesWhenExecutionSlotIsBusy(t *testing.T) {
	store := newFakeResumeQueueStore()
	processor := &scriptedRunProcessor{
		results: []error{agentruntime.ErrRunSlotOccupied},
		handled: make(chan agentruntime.RunProcessorInput, 2),
	}
	worker := agentruntime.NewResumeWorker(store, processor)

	event := agentruntime.ResumeEvent{
		RunID:    "run_04",
		Revision: 6,
		Source:   agentruntime.ResumeSourceCallback,
		Token:    "cb_token",
	}
	store.Enqueue(event)

	worker.Start()
	defer worker.Stop()

	for i := 0; i < 2; i++ {
		select {
		case handled := <-processor.handled:
			if handled.Resume == nil || !reflect.DeepEqual(*handled.Resume, event) {
				t.Fatalf("handled input = %+v, want resume %+v", handled, event)
			}
		case <-time.After(time.Second):
			t.Fatal("expected busy execution slot resume to be retried")
		}
	}

	waitForResumeWorkerRelease(t, store, "run_04")

	acquireCalls, releaseCalls, requeueCalls := store.snapshot()
	if len(acquireCalls) != 2 || acquireCalls[0] != "run_04" || acquireCalls[1] != "run_04" {
		t.Fatalf("AcquireRunLock() calls = %+v, want [run_04 run_04]", acquireCalls)
	}
	if len(releaseCalls) != 2 || releaseCalls[0] != "run_04" || releaseCalls[1] != "run_04" {
		t.Fatalf("ReleaseRunLock() calls = %+v, want [run_04 run_04]", releaseCalls)
	}
	if len(requeueCalls) != 1 || !reflect.DeepEqual(requeueCalls[0], event) {
		t.Fatalf("EnqueueResumeEvent() calls = %+v, want [%+v]", requeueCalls, event)
	}
}

func waitForResumeWorkerRelease(t *testing.T, store *fakeResumeQueueStore, runID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, releaseCalls, _ := store.snapshot()
		for _, call := range releaseCalls {
			if call == runID {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, releaseCalls, _ := store.snapshot()
	t.Fatalf("ReleaseRunLock() calls = %+v, want contain %q", releaseCalls, runID)
}
