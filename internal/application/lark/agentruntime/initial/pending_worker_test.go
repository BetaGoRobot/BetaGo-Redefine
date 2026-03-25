package initial_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initial"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
)

type fakeRunStore struct {
	mu sync.Mutex

	scopes chan pendingScope

	payloads map[string][][]byte
	active   map[string]string
	indexed  map[string]bool

	acquireOK    bool
	acquireSeq   []bool
	acquireCalls []string
	releaseCalls []string
	prependCalls []string
	clearCalls   []string
}

type pendingScope struct {
	chatID      string
	actorOpenID string
}

func newFakeRunStore() *fakeRunStore {
	return &fakeRunStore{
		scopes:    make(chan pendingScope, 8),
		payloads:  make(map[string][][]byte),
		active:    make(map[string]string),
		indexed:   make(map[string]bool),
		acquireOK: true,
	}
}

func (s *fakeRunStore) EnqueueScope(chatID, actorOpenID string) {
	s.scopes <- pendingScope{chatID: chatID, actorOpenID: actorOpenID}
}

func (s *fakeRunStore) SetPayload(chatID, actorOpenID string, raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.scopeKey(chatID, actorOpenID)
	s.payloads[key] = append(s.payloads[key], append([]byte(nil), raw...))
	s.indexed[key] = true
}

func (s *fakeRunStore) DequeuePendingInitialScope(ctx context.Context, timeout time.Duration) (string, string, error) {
	select {
	case scope := <-s.scopes:
		return scope.chatID, scope.actorOpenID, nil
	case <-ctx.Done():
		return "", "", ctx.Err()
	case <-time.After(timeout):
		return "", "", nil
	}
}

func (s *fakeRunStore) ConsumePendingInitialRun(ctx context.Context, chatID, actorOpenID string) ([]byte, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.scopeKey(chatID, actorOpenID)
	queue := s.payloads[key]
	if len(queue) == 0 {
		return nil, 0, nil
	}
	raw := append([]byte(nil), queue[0]...)
	s.payloads[key] = append([][]byte(nil), queue[1:]...)
	return raw, int64(len(s.payloads[key])), nil
}

func (s *fakeRunStore) PrependPendingInitialRun(ctx context.Context, chatID, actorOpenID string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.scopeKey(chatID, actorOpenID)
	s.prependCalls = append(s.prependCalls, key)
	queue := append([][]byte{append([]byte(nil), raw...)}, s.payloads[key]...)
	s.payloads[key] = queue
	s.indexed[key] = true
	return nil
}

func (s *fakeRunStore) NotifyPendingInitialRun(ctx context.Context, chatID, actorOpenID string) error {
	s.EnqueueScope(chatID, actorOpenID)
	return nil
}

func (s *fakeRunStore) ClearPendingInitialScopeIfEmpty(ctx context.Context, chatID, actorOpenID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.scopeKey(chatID, actorOpenID)
	s.clearCalls = append(s.clearCalls, key)
	if len(s.payloads[key]) == 0 {
		delete(s.indexed, key)
	}
	return nil
}

func (s *fakeRunStore) PendingInitialRunCount(ctx context.Context, chatID, actorOpenID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.payloads[s.scopeKey(chatID, actorOpenID)])), nil
}

func (s *fakeRunStore) AcquirePendingInitialScopeLock(ctx context.Context, chatID, actorOpenID, owner string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.scopeKey(chatID, actorOpenID)
	s.acquireCalls = append(s.acquireCalls, key)
	if len(s.acquireSeq) > 0 {
		acquired := s.acquireSeq[0]
		s.acquireSeq = s.acquireSeq[1:]
		return acquired, nil
	}
	return s.acquireOK, nil
}

func (s *fakeRunStore) ReleasePendingInitialScopeLock(ctx context.Context, chatID, actorOpenID, owner string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseCalls = append(s.releaseCalls, s.scopeKey(chatID, actorOpenID))
	return true, nil
}

func (s *fakeRunStore) snapshot() (acquireCalls []string, releaseCalls []string, prependCalls []string, clearCalls []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.acquireCalls...), append([]string(nil), s.releaseCalls...), append([]string(nil), s.prependCalls...), append([]string(nil), s.clearCalls...)
}

func (s *fakeRunStore) scopeKey(chatID, actorOpenID string) string {
	return chatID + "::" + actorOpenID
}

func (s *fakeRunStore) isIndexed(chatID, actorOpenID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.indexed[s.scopeKey(chatID, actorOpenID)]
}

type observingRunProcessor struct {
	mu      sync.Mutex
	results []error
	handled chan agentruntime.RunProcessorInput
	roots   chan runtimecontext.AgenticReplyTarget
}

func (p *observingRunProcessor) ProcessRun(ctx context.Context, input agentruntime.RunProcessorInput) error {
	if p.handled != nil {
		p.handled <- input
	}
	if p.roots != nil {
		root, _ := runtimecontext.RootAgenticReplyTarget(ctx)
		p.roots <- root
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

type observingStatusUpdater struct {
	mu    sync.Mutex
	items []initialcore.PendingRun
	err   error
}

func (u *observingStatusUpdater) MarkStarted(_ context.Context, item initialcore.PendingRun) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.items = append(u.items, item)
	return u.err
}

func (u *observingStatusUpdater) MarkExpired(_ context.Context, item initialcore.PendingRun) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.items = append(u.items, item)
	return u.err
}

func (u *observingStatusUpdater) snapshot() []initialcore.PendingRun {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]initialcore.PendingRun(nil), u.items...)
}

type blockingPendingRunProcessor struct {
	handled chan agentruntime.RunProcessorInput
	release <-chan struct{}
}

func (p *blockingPendingRunProcessor) ProcessRun(ctx context.Context, input agentruntime.RunProcessorInput) error {
	p.handled <- input
	<-p.release
	return nil
}

func TestRunWorkerProcessesQueuedInitialRun(t *testing.T) {
	store := newFakeRunStore()
	processor := &observingRunProcessor{
		handled: make(chan agentruntime.RunProcessorInput, 1),
		roots:   make(chan runtimecontext.AgenticReplyTarget, 1),
	}
	worker := initialcore.NewRunWorker(store, processor)

	raw := mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start: agentruntime.StartShadowRunRequest{
			ChatID:           "oc_chat",
			ActorOpenID:      "ou_actor",
			TriggerType:      agentruntime.TriggerTypeMention,
			TriggerMessageID: "om_trigger",
			InputText:        "帮我查金价",
			Now:              time.Date(2026, 3, 24, 4, 0, 0, 0, time.UTC),
		},
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Size:    20,
			Args:    []string{"帮我查金价"},
		},
		OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		Event: initialcore.Event{
			ChatID:      "oc_chat",
			MessageID:   "om_trigger",
			ChatType:    "group",
			ActorOpenID: "ou_actor",
		},
		RootTarget: initialcore.RootTarget{
			MessageID: "om_pending_root",
			CardID:    "card_pending_root",
		},
		RequestedAt: time.Date(2026, 3, 24, 4, 0, 0, 0, time.UTC),
	})
	store.SetPayload("oc_chat", "ou_actor", raw)
	store.EnqueueScope("oc_chat", "ou_actor")

	worker.Start()
	defer worker.Stop()

	select {
	case handled := <-processor.handled:
		if handled.Initial == nil {
			t.Fatalf("handled input = %+v, want initial run", handled)
		}
		if handled.Initial.Start.ChatID != "oc_chat" || handled.Initial.Start.ActorOpenID != "ou_actor" {
			t.Fatalf("initial start = %+v, want chat=%q actor=%q", handled.Initial.Start, "oc_chat", "ou_actor")
		}
		if handled.Initial.Start.Now.Before(time.Date(2026, 3, 24, 4, 0, 0, 0, time.UTC)) {
			t.Fatalf("initial start time = %s, want >= requested_at", handled.Initial.Start.Now)
		}
	case <-time.After(time.Second):
		t.Fatal("expected pending initial worker to process queued initial run")
	}

	select {
	case root := <-processor.roots:
		want := runtimecontext.AgenticReplyTarget{MessageID: "om_pending_root", CardID: "card_pending_root"}
		if !reflect.DeepEqual(root, want) {
			t.Fatalf("root target = %+v, want %+v", root, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected pending initial worker to seed root target")
	}

	waitForWorkerRelease(t, store, "oc_chat::ou_actor")
}

func TestRunWorkerWithWorkersProcessesScopesInParallel(t *testing.T) {
	store := newFakeRunStore()
	release := make(chan struct{})
	processor := &blockingPendingRunProcessor{
		handled: make(chan agentruntime.RunProcessorInput, 2),
		release: release,
	}
	worker := initialcore.NewRunWorker(store, processor).WithWorkers(2)

	store.SetPayload("oc_chat_01", "ou_actor_01", mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start: agentruntime.StartShadowRunRequest{
			ChatID:           "oc_chat_01",
			ActorOpenID:      "ou_actor_01",
			TriggerMessageID: "om_parallel_01",
			InputText:        "task 1",
		},
		Event: initialcore.Event{ChatID: "oc_chat_01", MessageID: "om_parallel_01", ChatType: "group", ActorOpenID: "ou_actor_01"},
	}))
	store.SetPayload("oc_chat_02", "ou_actor_02", mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start: agentruntime.StartShadowRunRequest{
			ChatID:           "oc_chat_02",
			ActorOpenID:      "ou_actor_02",
			TriggerMessageID: "om_parallel_02",
			InputText:        "task 2",
		},
		Event: initialcore.Event{ChatID: "oc_chat_02", MessageID: "om_parallel_02", ChatType: "group", ActorOpenID: "ou_actor_02"},
	}))
	store.EnqueueScope("oc_chat_01", "ou_actor_01")
	store.EnqueueScope("oc_chat_02", "ou_actor_02")

	worker.Start()
	defer worker.Stop()

	for i := 0; i < 2; i++ {
		select {
		case <-processor.handled:
		case <-time.After(250 * time.Millisecond):
			t.Fatal("expected both pending scopes to be processed concurrently")
		}
	}

	close(release)
	waitForWorkerRelease(t, store, "oc_chat_01::ou_actor_01")
	waitForWorkerRelease(t, store, "oc_chat_02::ou_actor_02")
}

func TestRunWorkerMarksPendingCardStartedBeforeProcessing(t *testing.T) {
	store := newFakeRunStore()
	processor := &observingRunProcessor{handled: make(chan agentruntime.RunProcessorInput, 1)}
	updater := &observingStatusUpdater{}
	worker := initialcore.NewRunWorkerWithMetricsAndStatusUpdater(store, processor, nil, updater)

	raw := mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start: agentruntime.StartShadowRunRequest{
			ChatID:           "oc_chat",
			ActorOpenID:      "ou_actor",
			TriggerType:      agentruntime.TriggerTypeMention,
			TriggerMessageID: "om_trigger_status",
			InputText:        "帮我查金价",
			Now:              time.Date(2026, 3, 24, 4, 0, 30, 0, time.UTC),
		},
		Plan:        agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
		OutputMode:  agentruntime.InitialReplyOutputModeAgentic,
		Event:       initialcore.Event{ChatID: "oc_chat", MessageID: "om_trigger_status", ChatType: "group", ActorOpenID: "ou_actor"},
		RootTarget:  initialcore.RootTarget{MessageID: "om_pending_root", CardID: "card_pending_root"},
		RequestedAt: time.Date(2026, 3, 24, 4, 0, 30, 0, time.UTC),
	})
	store.SetPayload("oc_chat", "ou_actor", raw)
	store.EnqueueScope("oc_chat", "ou_actor")

	worker.Start()
	defer worker.Stop()

	select {
	case handled := <-processor.handled:
		if handled.Initial == nil || handled.Initial.Start.TriggerMessageID != "om_trigger_status" {
			t.Fatalf("handled input = %+v, want initial run for om_trigger_status", handled)
		}
	case <-time.After(time.Second):
		t.Fatal("expected pending initial worker to process queued initial run")
	}

	waitForWorkerRelease(t, store, "oc_chat::ou_actor")

	items := updater.snapshot()
	if len(items) != 1 {
		t.Fatalf("status updater calls = %d, want 1", len(items))
	}
	if items[0].RootTarget.MessageID != "om_pending_root" || items[0].RootTarget.CardID != "card_pending_root" {
		t.Fatalf("updated root target = %+v, want pending root refs", items[0].RootTarget)
	}
}

func TestRunWorkerRequeuesWhenProcessorReportsSlotOccupied(t *testing.T) {
	store := newFakeRunStore()
	processor := &observingRunProcessor{
		results: []error{agentruntime.ErrRunSlotOccupied},
		handled: make(chan agentruntime.RunProcessorInput, 1),
	}
	worker := initialcore.NewRunWorker(store, processor)

	raw := mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start:      agentruntime.StartShadowRunRequest{ChatID: "oc_chat", ActorOpenID: "ou_actor", TriggerType: agentruntime.TriggerTypeMention, TriggerMessageID: "om_trigger", InputText: "帮我查金价", Now: time.Date(2026, 3, 24, 4, 1, 0, 0, time.UTC)},
		Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
		OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		Event:      initialcore.Event{ChatID: "oc_chat", MessageID: "om_trigger", ChatType: "group", ActorOpenID: "ou_actor"},
		RootTarget: initialcore.RootTarget{MessageID: "om_pending_root", CardID: "card_pending_root"},
	})
	store.SetPayload("oc_chat", "ou_actor", raw)
	store.EnqueueScope("oc_chat", "ou_actor")

	worker.Start()
	defer worker.Stop()

	select {
	case handled := <-processor.handled:
		if handled.Initial == nil || handled.Initial.Start.TriggerMessageID != "om_trigger" {
			t.Fatalf("handled input = %+v, want initial run for om_trigger", handled)
		}
	case <-time.After(time.Second):
		t.Fatal("expected pending initial worker to attempt queued initial run")
	}

	waitForWorkerRelease(t, store, "oc_chat::ou_actor")

	_, _, prependCalls, _ := store.snapshot()
	if len(prependCalls) != 1 || prependCalls[0] != "oc_chat::ou_actor" {
		t.Fatalf("PrependPendingInitialRun() calls = %+v, want [oc_chat::ou_actor]", prependCalls)
	}
}

func TestRunWorkerRetriesAfterSlotBecomesAvailable(t *testing.T) {
	store := newFakeRunStore()
	processor := &observingRunProcessor{
		results: []error{agentruntime.ErrRunSlotOccupied, nil},
		handled: make(chan agentruntime.RunProcessorInput, 2),
	}
	worker := initialcore.NewRunWorker(store, processor)

	raw := mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start:      agentruntime.StartShadowRunRequest{ChatID: "oc_chat", ActorOpenID: "ou_actor", TriggerType: agentruntime.TriggerTypeMention, TriggerMessageID: "om_trigger_retry", InputText: "帮我查金价", Now: time.Date(2026, 3, 24, 4, 2, 0, 0, time.UTC)},
		Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
		OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		Event:      initialcore.Event{ChatID: "oc_chat", MessageID: "om_trigger_retry", ChatType: "group", ActorOpenID: "ou_actor"},
		RootTarget: initialcore.RootTarget{MessageID: "om_pending_root", CardID: "card_pending_root"},
	})
	store.SetPayload("oc_chat", "ou_actor", raw)
	store.EnqueueScope("oc_chat", "ou_actor")

	worker.Start()
	defer worker.Stop()

	select {
	case handled := <-processor.handled:
		if handled.Initial == nil || handled.Initial.Start.TriggerMessageID != "om_trigger_retry" {
			t.Fatalf("first handled input = %+v, want initial run for om_trigger_retry", handled)
		}
	case <-time.After(time.Second):
		t.Fatal("expected pending initial worker to attempt queued run the first time")
	}

	select {
	case handled := <-processor.handled:
		if handled.Initial == nil || handled.Initial.Start.TriggerMessageID != "om_trigger_retry" {
			t.Fatalf("second handled input = %+v, want retried initial run for om_trigger_retry", handled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected pending initial worker to retry queued run after slot becomes available")
	}
}

func TestRunWorkerRequeuesWhenProcessorReturnsGenericError(t *testing.T) {
	store := newFakeRunStore()
	processor := &observingRunProcessor{
		results: []error{errors.New("transient processor failure")},
		handled: make(chan agentruntime.RunProcessorInput, 1),
	}
	worker := initialcore.NewRunWorker(store, processor)

	raw := mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start:      agentruntime.StartShadowRunRequest{ChatID: "oc_chat", ActorOpenID: "ou_actor", TriggerType: agentruntime.TriggerTypeMention, TriggerMessageID: "om_trigger_generic_error", InputText: "帮我查金价", Now: time.Date(2026, 3, 24, 4, 2, 30, 0, time.UTC)},
		Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
		OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		Event:      initialcore.Event{ChatID: "oc_chat", MessageID: "om_trigger_generic_error", ChatType: "group", ActorOpenID: "ou_actor"},
		RootTarget: initialcore.RootTarget{MessageID: "om_pending_root", CardID: "card_pending_root"},
	})
	store.SetPayload("oc_chat", "ou_actor", raw)
	store.EnqueueScope("oc_chat", "ou_actor")

	worker.Start()
	defer worker.Stop()

	select {
	case handled := <-processor.handled:
		if handled.Initial == nil || handled.Initial.Start.TriggerMessageID != "om_trigger_generic_error" {
			t.Fatalf("handled input = %+v, want initial run for om_trigger_generic_error", handled)
		}
	case <-time.After(time.Second):
		t.Fatal("expected pending initial worker to attempt queued run")
	}

	waitForWorkerRelease(t, store, "oc_chat::ou_actor")

	_, _, prependCalls, _ := store.snapshot()
	if len(prependCalls) != 1 || prependCalls[0] != "oc_chat::ou_actor" {
		t.Fatalf("PrependPendingInitialRun() calls = %+v, want [oc_chat::ou_actor]", prependCalls)
	}
}

func TestRunWorkerClearsScopeIndexWhenQueueIsDrained(t *testing.T) {
	store := newFakeRunStore()
	processor := &observingRunProcessor{handled: make(chan agentruntime.RunProcessorInput, 1)}
	worker := initialcore.NewRunWorker(store, processor)

	raw := mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start:      agentruntime.StartShadowRunRequest{ChatID: "oc_chat", ActorOpenID: "ou_actor", TriggerType: agentruntime.TriggerTypeMention, TriggerMessageID: "om_trigger_clear", InputText: "帮我查金价", Now: time.Date(2026, 3, 24, 4, 3, 0, 0, time.UTC)},
		Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
		OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		Event:      initialcore.Event{ChatID: "oc_chat", MessageID: "om_trigger_clear", ChatType: "group", ActorOpenID: "ou_actor"},
	})
	store.SetPayload("oc_chat", "ou_actor", raw)
	store.EnqueueScope("oc_chat", "ou_actor")

	worker.Start()
	defer worker.Stop()

	select {
	case <-processor.handled:
	case <-time.After(time.Second):
		t.Fatal("expected pending initial worker to process queued run")
	}

	waitForWorkerRelease(t, store, "oc_chat::ou_actor")

	_, _, _, clearCalls := store.snapshot()
	if len(clearCalls) == 0 || clearCalls[len(clearCalls)-1] != "oc_chat::ou_actor" {
		t.Fatalf("ClearPendingInitialScopeIfEmpty() calls = %+v, want contain oc_chat::ou_actor", clearCalls)
	}
	if store.isIndexed("oc_chat", "ou_actor") {
		t.Fatal("expected scope index to be cleared after queue drained")
	}
}

func TestRunWorkerDropsExpiredPendingRunWithoutProcessing(t *testing.T) {
	store := newFakeRunStore()
	processor := &observingRunProcessor{
		handled: make(chan agentruntime.RunProcessorInput, 1),
	}
	worker := initialcore.NewRunWorker(store, processor)

	requestedAt := time.Now().Add(-7 * 24 * time.Hour).UTC()
	raw := mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start: agentruntime.StartShadowRunRequest{
			ChatID:           "oc_chat",
			ActorOpenID:      "ou_actor",
			TriggerType:      agentruntime.TriggerTypeMention,
			TriggerMessageID: "om_expired_pending",
			InputText:        "帮我查金价",
			Now:              requestedAt,
		},
		Plan:        agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
		OutputMode:  agentruntime.InitialReplyOutputModeAgentic,
		Event:       initialcore.Event{ChatID: "oc_chat", MessageID: "om_expired_pending", ChatType: "group", ActorOpenID: "ou_actor"},
		RootTarget:  initialcore.RootTarget{MessageID: "om_pending_root", CardID: "card_pending_root"},
		RequestedAt: requestedAt,
	})
	store.SetPayload("oc_chat", "ou_actor", raw)
	store.EnqueueScope("oc_chat", "ou_actor")

	worker.Start()
	defer worker.Stop()

	select {
	case handled := <-processor.handled:
		t.Fatalf("handled input = %+v, want no processing for expired pending run", handled)
	case <-time.After(300 * time.Millisecond):
	}

	waitForWorkerRelease(t, store, "oc_chat::ou_actor")

	_, _, prependCalls, clearCalls := store.snapshot()
	if len(prependCalls) != 0 {
		t.Fatalf("PrependPendingInitialRun() calls = %+v, want empty", prependCalls)
	}
	if len(clearCalls) == 0 || clearCalls[0] != "oc_chat::ou_actor" {
		t.Fatalf("ClearPendingInitialScopeIfEmpty() calls = %+v, want contain oc_chat::ou_actor", clearCalls)
	}
	if store.isIndexed("oc_chat", "ou_actor") {
		t.Fatal("expected expired pending scope index to be cleared")
	}
}

func TestRunWorkerClearsStaleScopeWhenWakeupHasNoPayload(t *testing.T) {
	store := newFakeRunStore()
	processor := &observingRunProcessor{handled: make(chan agentruntime.RunProcessorInput, 1)}
	worker := initialcore.NewRunWorker(store, processor)

	store.indexed["oc_chat::ou_actor"] = true
	store.EnqueueScope("oc_chat", "ou_actor")

	worker.Start()
	defer worker.Stop()

	waitForWorkerRelease(t, store, "oc_chat::ou_actor")

	select {
	case handled := <-processor.handled:
		t.Fatalf("handled input = %+v, want no processing for stale wakeup", handled)
	default:
	}

	_, _, _, clearCalls := store.snapshot()
	if len(clearCalls) == 0 || clearCalls[len(clearCalls)-1] != "oc_chat::ou_actor" {
		t.Fatalf("ClearPendingInitialScopeIfEmpty() calls = %+v, want contain oc_chat::ou_actor", clearCalls)
	}
	if store.isIndexed("oc_chat", "ou_actor") {
		t.Fatal("expected stale scope index to be cleared when queue is empty")
	}
}

func mustMarshalPendingInitialRun(t *testing.T, item initialcore.PendingRun) []byte {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func waitForWorkerRelease(t *testing.T, store *fakeRunStore, scopeKey string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, releaseCalls, _, _ := store.snapshot()
		for _, call := range releaseCalls {
			if call == scopeKey {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, releaseCalls, _, _ := store.snapshot()
	t.Fatalf("ReleasePendingInitialScopeLock() calls = %+v, want contain %q", releaseCalls, scopeKey)
}
