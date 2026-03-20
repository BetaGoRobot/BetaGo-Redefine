package agentruntime_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

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
