package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type continuationStoreFake struct {
	step            *AgentStep
	context         ContinuationContext
	persisted       TurnDecision
	retries         int
	deliveryMessage string
	suppressed      int
	loadCalls       int
	leaseErr        error
}

func (f *continuationStoreFake) RepairContinuation(context.Context, string, time.Time) error {
	return nil
}

func (f *continuationStoreFake) ClaimContinuationStep(
	context.Context,
	ContinuationClaim,
) (*AgentStep, error) {
	if f.step == nil || f.step.Status != StepStatusQueued {
		return nil, ErrNotFound
	}
	f.step.Status = StepStatusRunning
	f.step.AttemptCount++
	f.step.WorkerID = "worker-1"
	return cloneStep(f.step), nil
}

func (f *continuationStoreFake) ValidateContinuationLease(context.Context, StepLease) error {
	return f.leaseErr
}

func TestContinuationProcessorLeaseLostBeforeReplySkipsDelivery(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{
			ID: "step-reply", RunID: "run-1", Kind: StepKindReply, Status: StepStatusQueued,
			InputJSON: `{"version":1,"step_id":"step-reply","run_id":"run-1","text":"完成","chat_id":"oc","idempotency_key":"step-reply"}`,
		},
		leaseErr: ErrLeaseLost,
	}
	deliverer := &replyDelivererFake{}
	processor := NewContinuationProcessor(store, &continuationGeneratorFake{}, deliverer, ContinuationProcessorConfig{
		WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Second,
	})
	if err := processor.ProcessRun(context.Background(), "run-1"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("ProcessRun() error = %v", err)
	}
	if deliverer.calls != 0 {
		t.Fatalf("deliverer calls = %d", deliverer.calls)
	}
}

func TestContinuationProcessorRejectsCrossRunFrozenReply(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{
			ID: "step-reply", RunID: "run-1", Kind: StepKindReply, Status: StepStatusQueued,
			InputJSON: `{"version":1,"step_id":"step-reply","run_id":"run-other","text":"完成","chat_id":"oc","idempotency_key":"step-reply"}`,
		},
	}
	deliverer := &replyDelivererFake{}
	processor := NewContinuationProcessor(store, &continuationGeneratorFake{}, deliverer, ContinuationProcessorConfig{
		WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Second,
	})
	if err := processor.ProcessRun(context.Background(), "run-1"); err == nil {
		t.Fatal("ProcessRun() error = nil")
	}
	if deliverer.calls != 0 || store.retries != 1 {
		t.Fatalf("deliverer=%d retries=%d", deliverer.calls, store.retries)
	}
}

func (f *continuationStoreFake) LoadContinuationContext(
	context.Context,
	LoadContinuationContextRequest,
) (ContinuationContext, error) {
	f.loadCalls++
	return f.context, nil
}

func (f *continuationStoreFake) PersistDecision(
	_ context.Context,
	req PersistDecisionRequest,
) (*AgentStep, error) {
	f.persisted = req.Decision
	f.step.Status = StepStatusCompleted
	if req.Decision.Decision != TurnDecisionReply {
		f.step = nil
		return nil, nil
	}
	input, _ := json.Marshal(ReplyRequest{
		Version: 1,
		StepID:  "step-reply", RunID: f.step.RunID, Text: req.Decision.Reply,
		TriggerMessageID: "om-trigger", ChatID: "oc-chat", IdempotencyKey: "step-reply",
	})
	f.step = &AgentStep{
		ID: "step-reply", RunID: f.step.RunID, Kind: StepKindReply,
		Status: StepStatusQueued, InputJSON: string(input),
	}
	return cloneStep(f.step), nil
}

func (f *continuationStoreFake) RetryContinuationStep(
	context.Context,
	RetryStepRequest,
) error {
	f.retries++
	f.step.Status = StepStatusQueued
	return nil
}

func (f *continuationStoreFake) CompleteReplyDelivery(
	_ context.Context,
	req CompleteReplyDeliveryRequest,
) error {
	f.deliveryMessage = req.MessageID
	f.step.Status = StepStatusCompleted
	f.step = nil
	return nil
}

func (f *continuationStoreFake) SuppressReplyDelivery(
	_ context.Context,
	req SuppressReplyDeliveryRequest,
) error {
	f.suppressed++
	f.step.Status = StepStatusCompleted
	f.step = nil
	return nil
}

type continuationGeneratorFake struct {
	calls    int
	decision TurnDecision
	err      error
}

type capabilityStepProcessorFake struct {
	calls int
	step  *AgentStep
	lease StepLease
	err   error
}

func (f *capabilityStepProcessorFake) ProcessCapabilityStep(
	_ context.Context,
	step *AgentStep,
	lease StepLease,
) error {
	f.calls++
	f.step = cloneStep(step)
	f.lease = lease
	return f.err
}

func TestContinuationProcessorDelegatesClaimedCapabilityStep(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{
			ID: "step-capability", RunID: "run-1",
			Kind: StepKindCapabilityCall, Status: StepStatusQueued,
			CapabilityName: "schedule.update", InputJSON: `{"version":1}`,
		},
	}
	capability := &capabilityStepProcessorFake{}
	processor := NewContinuationProcessor(
		store,
		&continuationGeneratorFake{},
		&replyDelivererFake{},
		ContinuationProcessorConfig{
			WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Second,
			CapabilityProcessor: capability,
		},
	)
	if err := processor.ProcessRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}
	if capability.calls != 1 || capability.step.Kind != StepKindCapabilityCall ||
		capability.lease.StepID != "step-capability" ||
		capability.lease.WorkerID != "worker-1" ||
		capability.lease.AttemptCount != 1 {
		t.Fatalf("capability step=%#v lease=%#v", capability.step, capability.lease)
	}
}

func (f *continuationGeneratorFake) Generate(context.Context, ContinuationContext) (TurnDecision, error) {
	f.calls++
	return f.decision, f.err
}

type replyDelivererFake struct {
	calls  int
	err    error
	errors []error
	keys   []string
}

func (f *replyDelivererFake) Deliver(_ context.Context, req ReplyRequest) (string, error) {
	f.calls++
	f.keys = append(f.keys, req.IdempotencyKey)
	if f.calls <= len(f.errors) {
		return "", f.errors[f.calls-1]
	}
	return "om-delivered", f.err
}

func TestContinuationProcessorDeliveryRetryDoesNotRegenerate(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{ID: "step-model", RunID: "run-1", Kind: StepKindDecide, Status: StepStatusQueued},
		context: ContinuationContext{
			RunID:         "run-1",
			LatestOutcome: ConversationEvent{Type: EventTypeCapabilityResult, Payload: []byte(`{}`)},
		},
	}
	generator := &continuationGeneratorFake{decision: TurnDecision{
		Decision: TurnDecisionReply, Reply: "完成", Reason: "反馈",
	}}
	sendErr := errors.New("send failed")
	deliverer := &replyDelivererFake{errors: []error{sendErr}}
	processor := NewContinuationProcessor(store, generator, deliverer, ContinuationProcessorConfig{
		WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Nanosecond,
	})
	if err := processor.ProcessRun(context.Background(), "run-1"); !errors.Is(err, sendErr) {
		t.Fatalf("first ProcessRun() error = %v", err)
	}
	if err := processor.ProcessRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("second ProcessRun() error = %v", err)
	}
	if generator.calls != 1 || deliverer.calls != 2 ||
		deliverer.keys[0] != deliverer.keys[1] {
		t.Fatalf("generator=%d deliverer=%d keys=%v", generator.calls, deliverer.calls, deliverer.keys)
	}
}

func TestContinuationProcessorObserveOnlySkipsDelivery(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{ID: "step-model", RunID: "run-1", Kind: StepKindDecide, Status: StepStatusQueued},
		context: ContinuationContext{
			RunID:         "run-1",
			LatestOutcome: ConversationEvent{Type: EventTypeCapabilityResult, Payload: []byte(`{}`)},
		},
	}
	generator := &continuationGeneratorFake{decision: TurnDecision{
		Decision: TurnDecisionObserveOnly, Reason: "卡片足够",
	}}
	deliverer := &replyDelivererFake{}
	processor := NewContinuationProcessor(store, generator, deliverer, ContinuationProcessorConfig{
		WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Second,
	})
	if err := processor.ProcessRun(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 || deliverer.calls != 0 {
		t.Fatalf("generator=%d deliverer=%d", generator.calls, deliverer.calls)
	}
}

func TestContinuationProcessorGeneratesOnceThenDeliversFrozenReply(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{ID: "step-model", RunID: "run-1", Kind: StepKindDecide, Status: StepStatusQueued},
		context: ContinuationContext{
			RunID: "run-1", ChatID: "oc-chat",
			LatestOutcome: ConversationEvent{Type: EventTypeCapabilityResult, Payload: []byte(`{}`)},
		},
	}
	generator := &continuationGeneratorFake{decision: TurnDecision{
		Decision: TurnDecisionReply, Reply: "完成", Reason: "需要反馈",
	}}
	deliverer := &replyDelivererFake{}
	processor := NewContinuationProcessor(store, generator, deliverer, ContinuationProcessorConfig{
		WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Second,
	})
	if err := processor.ProcessRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}
	if generator.calls != 1 || deliverer.calls != 1 ||
		store.deliveryMessage != "om-delivered" || len(deliverer.keys) != 1 ||
		deliverer.keys[0] != "step-reply" {
		t.Fatalf("generator=%d deliverer=%d message=%q keys=%v",
			generator.calls, deliverer.calls, store.deliveryMessage, deliverer.keys)
	}
}

func TestContinuationProcessorRetriesOnlyFailedStage(t *testing.T) {
	generatorErr := errors.New("model unavailable")
	store := &continuationStoreFake{
		step: &AgentStep{ID: "step-model", RunID: "run-1", Kind: StepKindDecide, Status: StepStatusQueued},
		context: ContinuationContext{
			RunID:         "run-1",
			LatestOutcome: ConversationEvent{Type: EventTypeCapabilityResult, Payload: []byte(`{}`)},
		},
	}
	generator := &continuationGeneratorFake{err: generatorErr}
	processor := NewContinuationProcessor(store, generator, &replyDelivererFake{}, ContinuationProcessorConfig{
		WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Second,
	})
	if err := processor.ProcessRun(context.Background(), "run-1"); !errors.Is(err, generatorErr) {
		t.Fatalf("ProcessRun() error = %v", err)
	}
	if store.retries != 1 || store.step.Kind != StepKindDecide {
		t.Fatalf("retries=%d step=%#v", store.retries, store.step)
	}
}

func TestContinuationProcessorWithMissingModelFailsOnlyWhenEnabledWorkExecutes(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{ID: "step-model", RunID: "run-1", Kind: StepKindDecide, Status: StepStatusQueued},
		context: ContinuationContext{
			RunID:         "run-1",
			LatestOutcome: ConversationEvent{Type: EventTypeCapabilityResult, Payload: []byte(`{}`)},
		},
	}
	processor := NewContinuationProcessor(
		store,
		NewContinuationGenerator(""),
		&replyDelivererFake{},
		ContinuationProcessorConfig{
			WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Second,
		},
	)
	err := processor.ProcessRun(context.Background(), "run-1")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("ProcessRun() error = %v, want missing model configuration", err)
	}
	if store.retries != 1 || store.step.Status != StepStatusQueued {
		t.Fatalf("missing model retry state retries=%d step=%#v", store.retries, store.step)
	}
}

func TestDisabledContinuationProcessorCompletesDecideWithoutModel(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{ID: "step-model", RunID: "run-1", Kind: StepKindDecide, Status: StepStatusQueued},
	}
	processor := NewDisabledContinuationProcessor(store, DisabledContinuationProcessorConfig{
		WorkerID: "worker-disabled", LeaseTTL: time.Minute,
	})
	if err := processor.ProcessRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}
	if store.persisted.Decision != TurnDecisionObserveOnly ||
		store.persisted.Reason != "callback continuation disabled" {
		t.Fatalf("persisted decision = %#v, want deterministic observe_only", store.persisted)
	}
	if store.loadCalls != 0 || store.deliveryMessage != "" {
		t.Fatalf("load calls=%d delivery=%q, want no model context or delivery",
			store.loadCalls, store.deliveryMessage)
	}
}

func TestDisabledContinuationProcessorSuppressesAlreadyQueuedReply(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{
			ID: "step-reply", RunID: "run-1", Kind: StepKindReply, Status: StepStatusQueued,
			InputJSON: `{"version":1,"step_id":"step-reply","run_id":"run-1","text":"完成","chat_id":"oc","idempotency_key":"step-reply"}`,
		},
	}
	processor := NewDisabledContinuationProcessor(store, DisabledContinuationProcessorConfig{
		WorkerID: "worker-disabled", LeaseTTL: time.Minute,
	})
	if err := processor.ProcessRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}
	if store.suppressed != 1 || store.deliveryMessage != "" {
		t.Fatalf("suppressed=%d delivery=%q, want suppressed reply", store.suppressed, store.deliveryMessage)
	}
}

func TestDisabledContinuationProcessorStillExecutesConfirmedCapability(t *testing.T) {
	store := &continuationStoreFake{
		step: &AgentStep{
			ID: "step-capability", RunID: "run-1",
			Kind: StepKindCapabilityCall, Status: StepStatusQueued,
			CapabilityName: "schedule.update", InputJSON: `{"version":1}`,
		},
	}
	capability := &capabilityStepProcessorFake{}
	processor := NewDisabledContinuationProcessor(
		store,
		DisabledContinuationProcessorConfig{
			WorkerID: "worker-disabled", LeaseTTL: time.Minute,
			CapabilityProcessor: capability,
		},
	)
	if err := processor.ProcessRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}
	if capability.calls != 1 ||
		capability.step.Kind != StepKindCapabilityCall ||
		capability.lease.StepID != "step-capability" {
		t.Fatalf(
			"capability step=%#v lease=%#v",
			capability.step,
			capability.lease,
		)
	}
}
