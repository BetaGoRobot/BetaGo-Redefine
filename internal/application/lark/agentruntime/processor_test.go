package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type continuationStoreFake struct {
	step            *AgentStep
	context         ContinuationContext
	persisted       TurnDecision
	retries         int
	deliveryMessage string
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

func (f *continuationStoreFake) LoadContinuationContext(
	context.Context,
	LoadContinuationContextRequest,
) (ContinuationContext, error) {
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

type continuationGeneratorFake struct {
	calls    int
	decision TurnDecision
	err      error
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
