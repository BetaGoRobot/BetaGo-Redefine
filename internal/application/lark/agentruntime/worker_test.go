package agentruntime

import (
	"context"
	"errors"
	"testing"
	"time"
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
