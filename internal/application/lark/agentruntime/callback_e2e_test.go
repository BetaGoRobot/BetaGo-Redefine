package agentruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func TestConversationCallbackRuntimeEndToEnd(t *testing.T) {
	harness := newCallbackE2EHarness(t)
	envelope := harness.startScheduleEdit(t)

	waiting := harness.store.snapshot()
	if waiting.runStatus != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("run status after start = %q, want waiting approval", waiting.runStatus)
	}
	if len(waiting.trustedInput) == 0 {
		t.Fatal("trusted schedule payload was not persisted")
	}

	event := harness.confirmEvent(envelope, "event-confirm-1")
	parsed, err := cardactionproto.Parse(event)
	if err != nil {
		t.Fatalf("Parse(confirm card envelope) error = %v", err)
	}
	if !harness.dispatcher.CanHandle(parsed) {
		t.Fatal("parsed confirm card envelope was not handled by runtime dispatcher")
	}
	for range 2 {
		if _, err := harness.dispatch(event); err != nil {
			t.Fatalf("Dispatch(confirm) error = %v", err)
		}
	}
	if got := harness.updater.calls(); got != 1 {
		t.Fatalf("schedule updater calls = %d, want 1", got)
	}
	if harness.generator.calls() != 0 || harness.deliverer.calls() != 0 {
		t.Fatal("callback synchronously executed LLM continuation")
	}
	if queued := harness.conversationExecutor.queued(); queued != 1 {
		t.Fatalf("queued continuation tasks = %d, want one deduplicated wake", queued)
	}
	if err := harness.conversationExecutor.drainOne(context.Background()); err != nil {
		t.Fatalf("drain callback continuation: %v", err)
	}
	if got := harness.deliverer.calls(); got != 1 {
		t.Fatalf("reply deliveries = %d, want 1", got)
	}
	completed := harness.store.snapshot()
	if completed.runStatus != agentruntime.RunStatusCompleted {
		t.Fatalf("run status after continuation = %q, want completed", completed.runStatus)
	}
	if completed.outboxCount != 5 {
		t.Fatalf("durable projection events = %d, want wait+callback+decision+reply events", completed.outboxCount)
	}
}

func TestConversationCallbackRuntimeRejectsInvalidConfirmation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*callbackE2EHarness, *callback.CardActionTriggerEvent)
		wantErr error
	}{
		{
			name: "wrong actor",
			mutate: func(_ *callbackE2EHarness, event *callback.CardActionTriggerEvent) {
				event.Event.Operator.OpenID = "actor-other"
			},
		},
		{
			name: "expired wait",
			mutate: func(harness *callbackE2EHarness, _ *callback.CardActionTriggerEvent) {
				harness.now = harness.now.Add(16 * time.Minute)
			},
			wantErr: agentruntime.ErrInteractionExpired,
		},
		{
			name: "wrong token",
			mutate: func(_ *callbackE2EHarness, event *callback.CardActionTriggerEvent) {
				event.Event.Action.Value[cardactionproto.TokenField] = "wrong-token"
			},
			wantErr: agentruntime.ErrInteractionTokenMismatch,
		},
		{
			name: "stale revision",
			mutate: func(_ *callbackE2EHarness, event *callback.CardActionTriggerEvent) {
				event.Event.Action.Value[cardactionproto.RevisionField] = "2"
			},
			wantErr: agentruntime.ErrInteractionConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newCallbackE2EHarness(t)
			envelope := harness.startScheduleEdit(t)
			event := harness.confirmEvent(envelope, "event-invalid")
			tt.mutate(harness, event)

			_, err := harness.dispatch(event)
			if err == nil {
				t.Fatal("Dispatch(invalid confirmation) error = nil")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Dispatch(invalid confirmation) error = %v, want %v", err, tt.wantErr)
			}
			snapshot := harness.store.snapshot()
			if harness.updater.calls() != 0 || harness.deliverer.calls() != 0 ||
				snapshot.runStatus != agentruntime.RunStatusWaitingApproval ||
				snapshot.outboxCount != 1 || snapshot.continuationRetries != 0 {
				t.Fatalf("invalid confirmation mutated state: %#v", snapshot)
			}
		})
	}
}

func TestConversationCallbackRuntimeGeneratorFailureRemainsRetryable(t *testing.T) {
	harness := newCallbackE2EHarness(t)
	generatorErr := errors.New("generator unavailable")
	harness.generator.err = generatorErr
	envelope := harness.startScheduleEdit(t)
	if _, err := harness.dispatch(harness.confirmEvent(envelope, "event-generator-failure")); err != nil {
		t.Fatalf("Dispatch(confirm) error = %v", err)
	}

	if err := harness.conversationExecutor.drainOne(context.Background()); !errors.Is(err, generatorErr) {
		t.Fatalf("continuation task error = %v, want generator failure", err)
	}
	snapshot := harness.store.snapshot()
	if harness.updater.calls() != 1 || harness.deliverer.calls() != 0 ||
		snapshot.runStatus != agentruntime.RunStatusQueued ||
		snapshot.continuationRetries != 1 ||
		snapshot.continuationStatus != agentruntime.StepStatusQueued ||
		!snapshot.continuationRetryAt.After(harness.now) ||
		snapshot.continuationError != generatorErr.Error() ||
		snapshot.continuationWorker != "" ||
		!snapshot.continuationLease.IsZero() ||
		snapshot.outboxCount != 3 {
		t.Fatalf("generator failure state = %#v", snapshot)
	}
}

func TestConversationCallbackRuntimeOpenSearchFailureDoesNotBlockContinuation(t *testing.T) {
	harness := newCallbackE2EHarness(t)
	openSearchErr := errors.New("opensearch unavailable")
	harness.writer.err = openSearchErr
	harness.writer.entered = make(chan struct{})
	harness.writer.release = make(chan struct{})
	envelope := harness.startScheduleEdit(t)

	if err := harness.runtime.SubmitNextProjection(context.Background()); err != nil {
		t.Fatalf("SubmitNextProjection() queue error = %v", err)
	}
	projectionDone := make(chan error, 1)
	go func() {
		projectionDone <- harness.projectionExecutor.drainOne(context.Background())
	}()
	select {
	case <-harness.writer.entered:
	case <-time.After(time.Second):
		t.Fatal("projection writer did not enter blocked OpenSearch call")
	}

	if _, err := harness.dispatch(harness.confirmEvent(envelope, "event-opensearch")); err != nil {
		t.Fatalf("Dispatch(confirm) error = %v", err)
	}
	if harness.updater.calls() != 1 || harness.generator.calls() != 0 ||
		harness.conversationExecutor.queued() != 1 {
		t.Fatal("blocked OpenSearch delayed callback mutation or started LLM work")
	}
	close(harness.writer.release)
	if err := <-projectionDone; !errors.Is(err, openSearchErr) {
		t.Fatalf("projection task error = %v, want OpenSearch failure", err)
	}
	if err := harness.conversationExecutor.drainOne(context.Background()); err != nil {
		t.Fatalf("drain callback continuation: %v", err)
	}
	snapshot := harness.store.snapshot()
	if harness.updater.calls() != 1 || harness.deliverer.calls() != 1 ||
		snapshot.runStatus != agentruntime.RunStatusCompleted ||
		snapshot.projectionRetries != 1 ||
		snapshot.projectionStatus != agentruntime.ProjectionStatusPending ||
		!snapshot.projectionRetryAt.After(harness.now) ||
		snapshot.projectionError != openSearchErr.Error() ||
		snapshot.projectionWorker != "" ||
		!snapshot.projectionLease.IsZero() ||
		snapshot.outboxCount != 5 {
		t.Fatalf("OpenSearch failure blocked durable continuation: %#v", snapshot)
	}
}

func TestConversationCallbackRuntimeFreshRuntimeConsumesDurableContinuation(t *testing.T) {
	first := newCallbackE2EHarness(t)
	envelope := first.startScheduleEdit(t)
	if _, err := first.dispatch(first.confirmEvent(envelope, "event-before-restart")); err != nil {
		t.Fatalf("Dispatch(confirm) error = %v", err)
	}
	if snapshot := first.store.snapshot(); snapshot.runStatus != agentruntime.RunStatusQueued {
		t.Fatalf("pre-restart run status = %q, want queued", snapshot.runStatus)
	}

	restarted := newCallbackE2EHarnessWithStore(t, first.store)
	if submitted, err := restarted.runtime.SubmitDueContinuations(context.Background(), 8); err != nil {
		t.Fatalf("restarted SubmitDueContinuations() error = %v", err)
	} else if submitted != 1 {
		t.Fatalf("restarted submitted continuations = %d, want 1", submitted)
	}
	if err := restarted.conversationExecutor.drainOne(context.Background()); err != nil {
		t.Fatalf("drain restarted continuation: %v", err)
	}
	snapshot := restarted.store.snapshot()
	if first.updater.calls() != 1 || restarted.deliverer.calls() != 1 ||
		snapshot.runStatus != agentruntime.RunStatusCompleted ||
		snapshot.outboxCount < 4 {
		t.Fatalf("restart recovery state = %#v", snapshot)
	}
}

type callbackE2EHarness struct {
	now                  time.Time
	store                *callbackE2EStore
	updater              *callbackE2EUpdater
	generator            *callbackE2EGenerator
	deliverer            *callbackE2EDeliverer
	writer               *callbackE2EProjectionWriter
	conversationExecutor *callbackE2EQueuedExecutor
	projectionExecutor   *callbackE2EQueuedExecutor
	runtime              *agentruntime.Runtime
	dispatcher           *appcardaction.ScheduleInteractionDispatcher
}

func newCallbackE2EHarness(t *testing.T) *callbackE2EHarness {
	t.Helper()
	return newCallbackE2EHarnessWithStore(t, &callbackE2EStore{})
}

func newCallbackE2EHarnessWithStore(
	t *testing.T,
	store *callbackE2EStore,
) *callbackE2EHarness {
	t.Helper()
	harness := &callbackE2EHarness{
		now:       time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC),
		store:     store,
		updater:   &callbackE2EUpdater{},
		generator: &callbackE2EGenerator{},
		deliverer: &callbackE2EDeliverer{},
		writer:    &callbackE2EProjectionWriter{},
	}
	conversationExecutor := &callbackE2EQueuedExecutor{}
	projectionExecutor := &callbackE2EQueuedExecutor{}
	runtime, err := agentruntime.NewRuntime(agentruntime.RuntimeOptions{
		ConversationExecutor: conversationExecutor,
		CallbackContinuationEnabled: func(context.Context, string) bool {
			return true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	starter, err := agentruntime.NewDurableScheduleEditStarter(
		agentruntime.DurableScheduleEditStarterOptions{
			Store: harness.store, AppID: "app-e2e", BotOpenID: "bot-e2e",
			TokenSecret: []byte("callback-e2e-secret"), WaitTTL: 15 * time.Minute,
			ProjectionIndex: "agent-conversation-events",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolver := agentruntime.NewScheduleInteractionService(harness.store, harness.updater, runtime)
	enabled := agentruntime.NewContinuationProcessor(
		harness.store,
		harness.generator,
		harness.deliverer,
		agentruntime.ContinuationProcessorConfig{
			WorkerID: "worker-e2e", LeaseTTL: time.Minute, RetryDelay: time.Second,
			Now: func() time.Time { return harness.now },
		},
	)
	disabled := agentruntime.NewDisabledContinuationProcessor(
		harness.store,
		agentruntime.DisabledContinuationProcessorConfig{
			WorkerID: "worker-disabled-e2e", LeaseTTL: time.Minute,
			Now: func() time.Time { return harness.now },
		},
	)
	projector := agentruntime.NewProjector(
		harness.store,
		harness.writer,
		projectionExecutor,
		agentruntime.ProjectorConfig{
			WorkerID: "projection-worker-e2e", LeaseTTL: time.Minute,
			WriteTimeout: 10 * time.Second,
			Now:          func() time.Time { return harness.now },
		},
	)
	if err := runtime.Bind(agentruntime.RuntimeDependencies{
		InteractionStarter: starter,
		ScheduleResolver:   resolver,
		EnabledProcessor:   enabled,
		DisabledProcessor:  disabled,
		Catalog:            harness.store,
		Projector:          projector,
		Expirer:            harness.store,
	}); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := appcardaction.NewScheduleInteractionDispatcher(
		runtime,
		appcardaction.ScheduleInteractionDispatcherOptions{
			Now:        func() time.Time { return harness.now },
			NewClaimID: func() string { return "claim-e2e" },
			RunningTTL: time.Minute,
			IndexAlias: "agent-conversation-events",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	harness.runtime = runtime
	harness.dispatcher = dispatcher
	harness.conversationExecutor = conversationExecutor
	harness.projectionExecutor = projectionExecutor
	return harness
}

func (h *callbackE2EHarness) dispatch(
	event *callback.CardActionTriggerEvent,
) (*callback.CardActionTriggerResponse, error) {
	return appcardaction.DispatchWithOptions(context.Background(), event, nil, appcardaction.DispatchOptions{
		Continuation: h.dispatcher,
	})
}

func (h *callbackE2EHarness) startScheduleEdit(t *testing.T) *agentruntime.RuntimeEnvelope {
	t.Helper()
	envelope, err := h.runtime.StartScheduleEdit(context.Background(), agentruntime.StartScheduleEditRequest{
		TaskID: "task-e2e", ActorOpenID: "actor-e2e", ChatID: "chat-e2e",
		SourceMessageID: "message-e2e",
		NewValues:       map[string]any{"name": "renamed schedule"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func (h *callbackE2EHarness) confirmEvent(
	envelope *agentruntime.RuntimeEnvelope,
	eventID string,
) *callback.CardActionTriggerEvent {
	payload := cardactionproto.New(cardactionproto.ActionScheduleEditConfirm).
		WithRunID(envelope.RunID).
		WithStepID(envelope.StepID).
		WithInteractionID(envelope.InteractionID).
		WithRevision(strconv.FormatInt(envelope.Revision, 10)).
		WithToken(envelope.Token).
		WithInteractionKind(envelope.InteractionKind).
		WithContinueAgent(envelope.ContinueAgent).
		Payload()
	value := make(map[string]any, len(payload))
	for key, item := range payload {
		value[key] = item
	}
	return &callback.CardActionTriggerEvent{
		EventV2Base: &larkevent.EventV2Base{
			Header: &larkevent.EventHeader{EventID: eventID},
		},
		Event: &callback.CardActionTriggerRequest{
			Operator: &callback.Operator{OpenID: "actor-e2e"},
			Context:  &callback.Context{OpenMessageID: "card-message-e2e"},
			Action:   &callback.CallBackAction{Value: value},
		},
	}
}

type callbackE2ESnapshot struct {
	runStatus           agentruntime.RunStatus
	trustedInput        json.RawMessage
	outboxCount         int
	continuationRetries int
	continuationStatus  agentruntime.StepStatus
	continuationRetryAt time.Time
	continuationError   string
	continuationWorker  string
	continuationLease   time.Time
	projectionRetries   int
	projectionStatus    agentruntime.ProjectionStatus
	projectionRetryAt   time.Time
	projectionError     string
	projectionWorker    string
	projectionLease     time.Time
}

type callbackE2EStore struct {
	mu sync.Mutex

	runID         string
	chatID        string
	triggerID     string
	runStatus     agentruntime.RunStatus
	stepID        string
	interactionID string
	revision      int64
	tokenHash     string
	trustedInput  json.RawMessage
	expiresAt     time.Time

	claimed       bool
	claimID       string
	resolvedActor string
	outcome       *agentruntime.ScheduleInteractionOutcome
	continuation  *agentruntime.AgentStep
	latestOutcome agentruntime.ConversationEvent
	retryAt       time.Time
	outboxCount   int

	continuationRetries int
	projectionRetries   int
	projection          *agentruntime.ProjectionOutbox
}

func (s *callbackE2EStore) snapshot() callbackE2ESnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := callbackE2ESnapshot{
		runStatus:           s.runStatus,
		trustedInput:        append(json.RawMessage(nil), s.trustedInput...),
		outboxCount:         s.outboxCount,
		continuationRetries: s.continuationRetries,
		continuationRetryAt: s.retryAt,
		projectionRetries:   s.projectionRetries,
	}
	if s.continuation != nil {
		snapshot.continuationStatus = s.continuation.Status
		snapshot.continuationError = s.continuation.ErrorText
		snapshot.continuationWorker = s.continuation.WorkerID
		snapshot.continuationLease = s.continuation.LeaseExpiresAt
	}
	if s.projection != nil {
		snapshot.projectionStatus = s.projection.Status
		snapshot.projectionRetryAt = s.projection.NextAttemptAt
		snapshot.projectionError = s.projection.LastError
		snapshot.projectionWorker = s.projection.WorkerID
		snapshot.projectionLease = s.projection.LeaseExpiresAt
	}
	return snapshot
}

func (s *callbackE2EStore) CreateScheduleEditInteraction(
	_ context.Context,
	req agentruntime.CreateScheduleEditInteractionRequest,
) (agentruntime.StartScheduleEditInteractionResult, error) {
	if err := req.Validate(); err != nil {
		return agentruntime.StartScheduleEditInteractionResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runID != "" {
		if s.triggerID != req.Run.TriggerMessageID {
			return agentruntime.StartScheduleEditInteractionResult{}, agentruntime.ErrActiveRunConflict
		}
		return s.startResult(), nil
	}
	s.runID = "run-e2e"
	s.chatID = req.Run.ChatID
	s.triggerID = req.Run.TriggerMessageID
	s.runStatus = agentruntime.RunStatusWaitingApproval
	s.stepID = req.StepID
	s.interactionID = req.InteractionID
	s.revision = 1
	s.tokenHash = req.TokenHash
	s.trustedInput = append(json.RawMessage(nil), req.TrustedInput...)
	s.expiresAt = time.Date(2026, time.July, 29, 10, 15, 0, 0, time.UTC)
	s.outboxCount++
	s.projection = &agentruntime.ProjectionOutbox{
		ID: "outbox-start-e2e", StepID: req.StepID,
		IndexAlias: req.Projection.IndexAlias, DocumentID: req.Projection.DocumentID,
		Payload:       append(json.RawMessage(nil), req.Projection.Payload...),
		Status:        agentruntime.ProjectionStatusPending,
		NextAttemptAt: time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC),
	}
	return s.startResult(), nil
}

func (s *callbackE2EStore) startResult() agentruntime.StartScheduleEditInteractionResult {
	return agentruntime.StartScheduleEditInteractionResult{
		RunID: s.runID, StepID: s.stepID, InteractionID: s.interactionID,
		Revision: s.revision, ExpiresAt: s.expiresAt,
	}
}

func (s *callbackE2EStore) InspectScheduleInteraction(
	_ context.Context,
	req agentruntime.ScheduleInteractionRequest,
) (agentruntime.ScheduleInteractionInspection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateInteraction(req); err != nil {
		return agentruntime.ScheduleInteractionInspection{}, err
	}
	inspection := agentruntime.ScheduleInteractionInspection{
		TrustedInput: append(json.RawMessage(nil), s.trustedInput...),
	}
	if s.outcome != nil {
		outcome := *s.outcome
		inspection.CompletedOutcome = &outcome
		inspection.ResolvedActorOpenID = s.resolvedActor
	}
	return inspection, nil
}

func (s *callbackE2EStore) ClaimScheduleInteraction(
	_ context.Context,
	req agentruntime.ScheduleInteractionRequest,
) (agentruntime.ScheduleInteractionClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateInteraction(req); err != nil {
		return agentruntime.ScheduleInteractionClaim{}, err
	}
	if s.outcome != nil {
		return agentruntime.ScheduleInteractionClaim{
			State: agentruntime.ScheduleClaimCompleted, Outcome: *s.outcome,
			ResolvedActorOpenID: s.resolvedActor,
		}, nil
	}
	if s.runStatus != agentruntime.RunStatusWaitingApproval {
		return agentruntime.ScheduleInteractionClaim{}, agentruntime.ErrInteractionConflict
	}
	if !req.ResolvedAt.Before(s.expiresAt) {
		return agentruntime.ScheduleInteractionClaim{}, agentruntime.ErrInteractionExpired
	}
	if s.claimed {
		return agentruntime.ScheduleInteractionClaim{State: agentruntime.ScheduleClaimRunning}, nil
	}
	s.claimed = true
	s.claimID = req.ClaimID
	return agentruntime.ScheduleInteractionClaim{
		State: agentruntime.ScheduleClaimAcquired, TrustedInput: append(json.RawMessage(nil), s.trustedInput...),
	}, nil
}

func (s *callbackE2EStore) ExecuteScheduleInteraction(
	ctx context.Context,
	req agentruntime.ScheduleInteractionRequest,
	executor agentruntime.ScheduleInteractionExecutor,
) (agentruntime.ScheduleInteractionOutcome, error) {
	s.mu.Lock()
	if !s.claimed || s.claimID != req.ClaimID {
		s.mu.Unlock()
		return agentruntime.ScheduleInteractionOutcome{}, agentruntime.ErrScheduleInteractionClaimLost
	}
	trustedRaw := append(json.RawMessage(nil), s.trustedInput...)
	s.mu.Unlock()
	trusted, err := agentruntime.DecodeScheduleEditTrustedInput(trustedRaw)
	if err != nil {
		return agentruntime.ScheduleInteractionOutcome{}, err
	}
	outcome, err := executor(ctx, trusted)
	if err != nil {
		return agentruntime.ScheduleInteractionOutcome{}, err
	}
	rawOutcome, err := json.Marshal(outcome)
	if err != nil {
		return agentruntime.ScheduleInteractionOutcome{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimed = false
	s.outcome = &outcome
	s.resolvedActor = req.ActorOpenID
	s.runStatus = agentruntime.RunStatusQueued
	s.latestOutcome = agentruntime.ConversationEvent{
		ID: "capability-result-e2e", Type: agentruntime.EventTypeCapabilityResult,
		ChatID: s.chatID, ActorOpenID: req.ActorOpenID, RunID: s.runID,
		InteractionID: s.interactionID, Revision: s.revision,
		Action: string(req.Action), OccurredAt: req.ResolvedAt, Payload: rawOutcome,
	}
	s.continuation = &agentruntime.AgentStep{
		ID: "step-decide-e2e", RunID: s.runID, Kind: agentruntime.StepKindDecide,
		Status: agentruntime.StepStatusQueued,
	}
	s.outboxCount += 2
	return outcome, nil
}

func (s *callbackE2EStore) FailScheduleInteraction(
	context.Context,
	agentruntime.FailScheduleInteractionRequest,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimed = false
	return nil
}

func (s *callbackE2EStore) validateInteraction(req agentruntime.ScheduleInteractionRequest) error {
	if req.RunID != s.runID || req.StepID != s.stepID ||
		req.InteractionID != s.interactionID || req.Revision != s.revision {
		return agentruntime.ErrInteractionConflict
	}
	if !agentruntime.MatchInteractionToken(req.PresentedToken, s.tokenHash) {
		return agentruntime.ErrInteractionTokenMismatch
	}
	return nil
}

func (s *callbackE2EStore) FindRunChatID(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runID == "" {
		return "", agentruntime.ErrNotFound
	}
	return s.chatID, nil
}

func (s *callbackE2EStore) ListDueContinuationRunIDs(
	_ context.Context,
	limit int,
) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		return nil, agentruntime.ErrInvalidRuntimeContract
	}
	if s.continuation == nil || s.continuation.Status != agentruntime.StepStatusQueued {
		return nil, nil
	}
	return []string{s.runID}, nil
}

func (s *callbackE2EStore) RepairContinuation(context.Context, string, time.Time) error {
	return nil
}

func (s *callbackE2EStore) ClaimContinuationStep(
	_ context.Context,
	claim agentruntime.ContinuationClaim,
) (*agentruntime.AgentStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.continuation == nil || s.continuation.RunID != claim.RunID ||
		s.continuation.Status != agentruntime.StepStatusQueued {
		return nil, agentruntime.ErrNotFound
	}
	s.continuation.Status = agentruntime.StepStatusRunning
	s.continuation.WorkerID = claim.WorkerID
	s.continuation.AttemptCount++
	s.continuation.LeaseExpiresAt = claim.Now.Add(claim.LeaseTTL)
	s.runStatus = agentruntime.RunStatusRunning
	return cloneCallbackE2EStep(s.continuation), nil
}

func (s *callbackE2EStore) ValidateContinuationLease(
	_ context.Context,
	lease agentruntime.StepLease,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.continuation == nil || s.continuation.ID != lease.StepID ||
		s.continuation.Status != agentruntime.StepStatusRunning ||
		s.continuation.WorkerID != lease.WorkerID ||
		s.continuation.AttemptCount != lease.AttemptCount {
		return agentruntime.ErrLeaseLost
	}
	return nil
}

func (s *callbackE2EStore) LoadContinuationContext(
	context.Context,
	agentruntime.LoadContinuationContextRequest,
) (agentruntime.ContinuationContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return agentruntime.ContinuationContext{
		RunID: s.runID, Goal: "confirm schedule edit", TriggerMessageID: s.triggerID,
		ChatID: s.chatID, ActorOpenID: s.resolvedActor, LatestOutcome: s.latestOutcome,
	}, nil
}

func (s *callbackE2EStore) PersistDecision(
	_ context.Context,
	req agentruntime.PersistDecisionRequest,
) (*agentruntime.AgentStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.matchContinuationLease(req.StepID, req.WorkerID, req.AttemptCount); err != nil {
		return nil, err
	}
	s.outboxCount++
	if req.Decision.Decision != agentruntime.TurnDecisionReply {
		s.continuation = nil
		s.runStatus = agentruntime.RunStatusCompleted
		return nil, nil
	}
	replyID := "step-reply-e2e"
	frozen, err := json.Marshal(agentruntime.ReplyRequest{
		Version: 1, StepID: replyID, RunID: s.runID, Text: req.Decision.Reply,
		TriggerMessageID: s.triggerID, ChatID: s.chatID, IdempotencyKey: replyID,
	})
	if err != nil {
		return nil, err
	}
	s.continuation = &agentruntime.AgentStep{
		ID: replyID, RunID: s.runID, Kind: agentruntime.StepKindReply,
		Status: agentruntime.StepStatusQueued, InputJSON: string(frozen),
	}
	s.runStatus = agentruntime.RunStatusQueued
	return cloneCallbackE2EStep(s.continuation), nil
}

func (s *callbackE2EStore) RetryContinuationStep(
	_ context.Context,
	req agentruntime.RetryStepRequest,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.matchContinuationLease(req.StepID, req.WorkerID, req.AttemptCount); err != nil {
		return err
	}
	s.continuation.Status = agentruntime.StepStatusQueued
	s.continuation.WorkerID = ""
	s.continuation.LeaseExpiresAt = time.Time{}
	s.continuation.ErrorText = req.ErrorText
	s.retryAt = req.RetryAt
	s.runStatus = agentruntime.RunStatusQueued
	s.continuationRetries++
	return nil
}

func (s *callbackE2EStore) CompleteReplyDelivery(
	_ context.Context,
	req agentruntime.CompleteReplyDeliveryRequest,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.matchContinuationLease(req.StepID, req.WorkerID, req.AttemptCount); err != nil {
		return err
	}
	s.continuation = nil
	s.runStatus = agentruntime.RunStatusCompleted
	s.outboxCount++
	return nil
}

func (s *callbackE2EStore) SuppressReplyDelivery(
	ctx context.Context,
	req agentruntime.SuppressReplyDeliveryRequest,
) error {
	return s.CompleteReplyDelivery(ctx, agentruntime.CompleteReplyDeliveryRequest{
		StepID: req.StepID, WorkerID: req.WorkerID, AttemptCount: req.AttemptCount,
		MessageID: "suppressed-e2e", FinishedAt: req.FinishedAt,
	})
}

func (s *callbackE2EStore) matchContinuationLease(stepID, workerID string, attempt int32) error {
	if s.continuation == nil || s.continuation.ID != stepID ||
		s.continuation.Status != agentruntime.StepStatusRunning ||
		s.continuation.WorkerID != workerID || s.continuation.AttemptCount != attempt {
		return agentruntime.ErrLeaseLost
	}
	return nil
}

func (s *callbackE2EStore) ExpireScheduleEditInteractions(
	context.Context,
	time.Time,
	int,
) (int, error) {
	return 0, nil
}

func (s *callbackE2EStore) ClaimProjection(
	_ context.Context,
	claim agentruntime.ProjectionClaim,
) (*agentruntime.ProjectionOutbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := claim.Validate(); err != nil {
		return nil, err
	}
	if s.projection == nil || s.projection.Status != agentruntime.ProjectionStatusPending ||
		s.projection.NextAttemptAt.After(claim.Now) {
		return nil, agentruntime.ErrNotFound
	}
	s.projection.Status = agentruntime.ProjectionStatusRunning
	s.projection.AttemptCount++
	s.projection.WorkerID = claim.WorkerID
	s.projection.LeaseExpiresAt = claim.Now.Add(claim.LeaseTTL)
	return cloneCallbackE2EProjection(s.projection), nil
}

func (s *callbackE2EStore) RenewProjectionLease(
	_ context.Context,
	req agentruntime.RenewProjectionLeaseRequest,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := req.Validate(); err != nil {
		return err
	}
	if !s.matchesProjectionLease(req.OutboxID, req.WorkerID, req.AttemptCount) {
		return agentruntime.ErrProjectionLeaseLost
	}
	s.projection.LeaseExpiresAt = req.Now.Add(req.LeaseTTL)
	return nil
}

func (s *callbackE2EStore) CompleteProjection(
	_ context.Context,
	req agentruntime.CompleteProjectionRequest,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := req.Validate(); err != nil {
		return err
	}
	if !s.matchesProjectionLease(req.OutboxID, req.WorkerID, req.AttemptCount) {
		return agentruntime.ErrProjectionLeaseLost
	}
	s.projection.Status = agentruntime.ProjectionStatusCompleted
	s.projection.WorkerID = ""
	s.projection.LeaseExpiresAt = time.Time{}
	return nil
}

func (s *callbackE2EStore) RetryProjection(
	_ context.Context,
	req agentruntime.RetryProjectionRequest,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := req.Validate(); err != nil {
		return err
	}
	if !s.matchesProjectionLease(req.OutboxID, req.WorkerID, req.AttemptCount) {
		return agentruntime.ErrProjectionLeaseLost
	}
	s.projection.Status = agentruntime.ProjectionStatusPending
	s.projection.WorkerID = ""
	s.projection.LeaseExpiresAt = time.Time{}
	s.projection.LastError = req.ErrorText
	s.projection.NextAttemptAt = req.RetryAt
	s.projectionRetries++
	return nil
}

func (s *callbackE2EStore) matchesProjectionLease(
	outboxID string,
	workerID string,
	attempt int32,
) bool {
	return s.projection != nil &&
		s.projection.ID == outboxID &&
		s.projection.Status == agentruntime.ProjectionStatusRunning &&
		s.projection.WorkerID == workerID &&
		s.projection.AttemptCount == attempt
}

func cloneCallbackE2EStep(step *agentruntime.AgentStep) *agentruntime.AgentStep {
	if step == nil {
		return nil
	}
	cloned := *step
	return &cloned
}

func cloneCallbackE2EProjection(
	outbox *agentruntime.ProjectionOutbox,
) *agentruntime.ProjectionOutbox {
	if outbox == nil {
		return nil
	}
	cloned := *outbox
	cloned.Payload = append(json.RawMessage(nil), outbox.Payload...)
	return &cloned
}

type callbackE2EUpdater struct{ count atomic.Int32 }

func (u *callbackE2EUpdater) ValidateScheduleEdit(
	_ context.Context,
	actor string,
	trusted agentruntime.ScheduleEditTrustedInput,
) error {
	if actor != trusted.InitiatorOpenID {
		return errors.New("schedule actor is not authorized")
	}
	return nil
}

func (u *callbackE2EUpdater) ExecuteScheduleEdit(
	context.Context,
	string,
	agentruntime.ScheduleEditTrustedInput,
) (json.RawMessage, error) {
	u.count.Add(1)
	return json.RawMessage(`{"status":"updated","task_id":"task-e2e","name":"renamed schedule"}`), nil
}

func (u *callbackE2EUpdater) calls() int { return int(u.count.Load()) }

type callbackE2EGenerator struct {
	count atomic.Int32
	err   error
}

func (g *callbackE2EGenerator) Generate(
	context.Context,
	agentruntime.ContinuationContext,
) (agentruntime.TurnDecision, error) {
	g.count.Add(1)
	if g.err != nil {
		return agentruntime.TurnDecision{}, g.err
	}
	return agentruntime.TurnDecision{
		Decision: agentruntime.TurnDecisionReply,
		Reply:    "Schedule 已更新。",
		Reason:   "向用户确认结果",
	}, nil
}

func (g *callbackE2EGenerator) calls() int { return int(g.count.Load()) }

type callbackE2EDeliverer struct{ count atomic.Int32 }

func (d *callbackE2EDeliverer) Deliver(
	context.Context,
	agentruntime.ReplyRequest,
) (string, error) {
	call := d.count.Add(1)
	return fmt.Sprintf("message-delivered-%d", call), nil
}

func (d *callbackE2EDeliverer) calls() int { return int(d.count.Load()) }

type callbackE2EQueuedExecutor struct {
	mu    sync.Mutex
	tasks []func(context.Context) error
}

func (e *callbackE2EQueuedExecutor) Submit(
	_ context.Context,
	_ string,
	task func(context.Context) error,
) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tasks = append(e.tasks, task)
	return nil
}

func (e *callbackE2EQueuedExecutor) queued() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.tasks)
}

func (e *callbackE2EQueuedExecutor) drainOne(ctx context.Context) error {
	e.mu.Lock()
	if len(e.tasks) == 0 {
		e.mu.Unlock()
		return errors.New("callback E2E executor has no queued task")
	}
	task := e.tasks[0]
	e.tasks = e.tasks[1:]
	e.mu.Unlock()
	return task(ctx)
}

type callbackE2EProjectionWriter struct {
	count       atomic.Int32
	err         error
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
}

func (w *callbackE2EProjectionWriter) Upsert(
	ctx context.Context,
	_ string,
	_ string,
	_ json.RawMessage,
) error {
	w.count.Add(1)
	if w.entered != nil {
		w.enteredOnce.Do(func() { close(w.entered) })
	}
	if w.release != nil {
		select {
		case <-w.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return w.err
}
