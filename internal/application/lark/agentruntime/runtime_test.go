package agentruntime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type runtimeExecutorStub struct {
	mu    sync.Mutex
	err   error
	names []string
	tasks []func(context.Context) error
}

func (s *runtimeExecutorStub) Submit(_ context.Context, name string, task func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.names = append(s.names, name)
	s.tasks = append(s.tasks, task)
	return nil
}

type runtimeCatalogStub struct {
	chatID     string
	candidates []string
}

func (s *runtimeCatalogStub) FindRunChatID(context.Context, string) (string, error) {
	return s.chatID, nil
}

func (s *runtimeCatalogStub) ListDueContinuationRunIDs(context.Context, int) ([]string, error) {
	return append([]string(nil), s.candidates...), nil
}

type runtimeRunnerStub struct {
	mu    sync.Mutex
	calls []string
}

func (s *runtimeRunnerStub) ProcessRun(_ context.Context, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, runID)
	return nil
}

type runtimeStarterStub struct{}

func (runtimeStarterStub) StartScheduleEdit(context.Context, StartScheduleEditRequest) (*RuntimeEnvelope, error) {
	return &RuntimeEnvelope{
		RunID: "run_delegate", StepID: "step_delegate", InteractionID: "interaction_delegate",
		Revision: 1, Token: "token_delegate", InteractionKind: "schedule_edit", ContinueAgent: true,
	}, nil
}

type runtimeScheduleResolverStub struct{}

func (runtimeScheduleResolverStub) Resolve(
	context.Context,
	ScheduleInteractionRequest,
) (ScheduleInteractionOutcome, error) {
	return ScheduleInteractionOutcome{Status: "resolved"}, nil
}

type runtimeProjectorStub struct {
	calls int
	err   error
}

func (s *runtimeProjectorStub) SubmitNext(context.Context) error {
	s.calls++
	return s.err
}

type runtimeExpirerStub struct{}

func (*runtimeExpirerStub) ExpireScheduleEditInteractions(context.Context, time.Time, int) (int, error) {
	return 0, nil
}

func TestRuntimeLateBindingDelegatesAndRechecksCallbackGateInsideTask(t *testing.T) {
	executor := &runtimeExecutorStub{}
	catalog := &runtimeCatalogStub{chatID: "chat_1"}
	enabled := &runtimeRunnerStub{}
	disabled := &runtimeRunnerStub{}
	callbackEnabled := false
	runtime, err := NewRuntime(RuntimeOptions{
		ConversationExecutor: executor,
		CallbackContinuationEnabled: func(context.Context, string) bool {
			return callbackEnabled
		},
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Bind(RuntimeDependencies{
		InteractionStarter: runtimeStarterStub{},
		ScheduleResolver:   runtimeScheduleResolverStub{},
		EnabledProcessor:   enabled,
		DisabledProcessor:  disabled,
		Catalog:            catalog,
		Projector:          &runtimeProjectorStub{},
		Expirer:            &runtimeExpirerStub{},
	}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	envelope, err := runtime.StartScheduleEdit(context.Background(), StartScheduleEditRequest{})
	if err != nil || envelope.RunID != "run_delegate" {
		t.Fatalf("StartScheduleEdit() = %#v, %v", envelope, err)
	}
	outcome, err := runtime.Resolve(context.Background(), ScheduleInteractionRequest{})
	if err != nil || outcome.Status != "resolved" {
		t.Fatalf("Resolve() = %#v, %v", outcome, err)
	}
	// Two wakes before execution must collapse to one executor task.
	if err := runtime.SubmitRun(context.Background(), "run_1"); err != nil {
		t.Fatalf("first SubmitRun() error = %v", err)
	}
	if err := runtime.SubmitRun(context.Background(), "run_1"); err != nil {
		t.Fatalf("deduplicated SubmitRun() error = %v", err)
	}
	if len(executor.tasks) != 1 || executor.names[0] != "conversation-run:run_1" {
		t.Fatalf("executor submissions = %#v, want one stable run task", executor.names)
	}

	// The flag is intentionally read when the task runs, not when it is queued.
	if err := executor.tasks[0](context.Background()); err != nil {
		t.Fatalf("disabled continuation task error = %v", err)
	}
	if len(disabled.calls) != 1 || len(enabled.calls) != 0 {
		t.Fatalf("processor calls enabled=%v disabled=%v, want disabled only", enabled.calls, disabled.calls)
	}

	callbackEnabled = true
	if err := runtime.SubmitRun(context.Background(), "run_1"); err != nil {
		t.Fatalf("re-enabled SubmitRun() error = %v", err)
	}
	if err := executor.tasks[1](context.Background()); err != nil {
		t.Fatalf("enabled continuation task error = %v", err)
	}
	if len(enabled.calls) != 1 {
		t.Fatalf("enabled processor calls = %v, want run_1", enabled.calls)
	}
}

func TestRuntimeSubmitFailureLeavesRunRetryable(t *testing.T) {
	queueFull := errors.New("queue full")
	executor := &runtimeExecutorStub{err: queueFull}
	runtime, err := NewRuntime(RuntimeOptions{
		ConversationExecutor:        executor,
		CallbackContinuationEnabled: func(context.Context, string) bool { return true },
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Bind(RuntimeDependencies{
		InteractionStarter: runtimeStarterStub{},
		ScheduleResolver:   runtimeScheduleResolverStub{},
		EnabledProcessor:   &runtimeRunnerStub{},
		DisabledProcessor:  &runtimeRunnerStub{},
		Catalog:            &runtimeCatalogStub{chatID: "chat_1"},
		Projector:          &runtimeProjectorStub{},
		Expirer:            &runtimeExpirerStub{},
	}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	if err := runtime.SubmitRun(context.Background(), "run_1"); !errors.Is(err, queueFull) {
		t.Fatalf("SubmitRun() error = %v, want queue full", err)
	}
	executor.err = nil
	if err := runtime.SubmitRun(context.Background(), "run_1"); err != nil {
		t.Fatalf("retry SubmitRun() error = %v", err)
	}
	if len(executor.tasks) != 1 {
		t.Fatalf("retry executor task count = %d, want 1", len(executor.tasks))
	}
}

func TestRuntimeSubmitDueStopsOnExecutorBackpressureWithoutClaiming(t *testing.T) {
	queueFull := errors.New("queue full")
	executor := &runtimeExecutorStub{err: queueFull}
	catalog := &runtimeCatalogStub{
		chatID:     "chat_1",
		candidates: []string{"run_1", "run_2"},
	}
	runtime, err := NewRuntime(RuntimeOptions{
		ConversationExecutor:        executor,
		CallbackContinuationEnabled: func(context.Context, string) bool { return true },
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Bind(RuntimeDependencies{
		InteractionStarter: runtimeStarterStub{},
		ScheduleResolver:   runtimeScheduleResolverStub{},
		EnabledProcessor:   &runtimeRunnerStub{},
		DisabledProcessor:  &runtimeRunnerStub{},
		Catalog:            catalog,
		Projector:          &runtimeProjectorStub{},
		Expirer:            &runtimeExpirerStub{},
	}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	submitted, err := runtime.SubmitDueContinuations(context.Background(), 32)
	if !errors.Is(err, queueFull) || submitted != 0 {
		t.Fatalf("SubmitDueContinuations() = %d, %v, want 0 and queue full", submitted, err)
	}
}

func TestRuntimeRejectsUseBeforeLateBinding(t *testing.T) {
	runtime, err := NewRuntime(RuntimeOptions{
		ConversationExecutor:        &runtimeExecutorStub{},
		CallbackContinuationEnabled: func(context.Context, string) bool { return true },
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if _, err := runtime.StartScheduleEdit(context.Background(), StartScheduleEditRequest{}); err == nil {
		t.Fatal("StartScheduleEdit() error = nil before Bind")
	}
	if err := runtime.SubmitRun(context.Background(), "run_1"); err == nil {
		t.Fatal("SubmitRun() error = nil before Bind")
	}
}

func TestRuntimePreservesProjectionNotFoundIdleSignal(t *testing.T) {
	projector := &runtimeProjectorStub{err: ErrNotFound}
	runtime, err := NewRuntime(RuntimeOptions{
		ConversationExecutor:        &runtimeExecutorStub{},
		CallbackContinuationEnabled: func(context.Context, string) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Bind(RuntimeDependencies{
		InteractionStarter: runtimeStarterStub{},
		ScheduleResolver:   runtimeScheduleResolverStub{},
		EnabledProcessor:   &runtimeRunnerStub{},
		DisabledProcessor:  &runtimeRunnerStub{},
		Catalog:            &runtimeCatalogStub{},
		Projector:          projector,
		Expirer:            &runtimeExpirerStub{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SubmitNextProjection(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SubmitNextProjection() error = %v, want ErrNotFound", err)
	}
}
