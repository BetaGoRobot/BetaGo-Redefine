package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

type ContinuationClaim struct {
	RunID    string
	WorkerID string
	LeaseTTL time.Duration
	Now      time.Time
}

type StepLease struct {
	StepID       string
	WorkerID     string
	AttemptCount int32
	LeaseTTL     time.Duration
	Now          time.Time
}

type LoadContinuationContextRequest struct {
	RunID        string
	AnchorStepID string
	RecentLimit  int
}

type PersistDecisionRequest struct {
	StepID       string
	WorkerID     string
	AttemptCount int32
	Decision     TurnDecision
	FinishedAt   time.Time
}

type CompleteReplyDeliveryRequest struct {
	StepID       string
	WorkerID     string
	AttemptCount int32
	MessageID    string
	FinishedAt   time.Time
}

type ContinuationStore interface {
	RepairContinuation(context.Context, string, time.Time) error
	ClaimContinuationStep(context.Context, ContinuationClaim) (*AgentStep, error)
	ValidateContinuationLease(context.Context, StepLease) error
	LoadContinuationContext(context.Context, LoadContinuationContextRequest) (ContinuationContext, error)
	PersistDecision(context.Context, PersistDecisionRequest) (*AgentStep, error)
	RetryContinuationStep(context.Context, RetryStepRequest) error
	CompleteReplyDelivery(context.Context, CompleteReplyDeliveryRequest) error
}

type ContinuationProcessorConfig struct {
	WorkerID        string
	LeaseTTL        time.Duration
	RetryDelay      time.Duration
	RecentStepLimit int
	Now             func() time.Time
}

type ContinuationProcessor struct {
	store     ContinuationStore
	generator ContinuationGenerator
	deliverer ReplyDeliverer
	config    ContinuationProcessorConfig
}

func NewContinuationProcessor(
	store ContinuationStore,
	generator ContinuationGenerator,
	deliverer ReplyDeliverer,
	config ContinuationProcessorConfig,
) *ContinuationProcessor {
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.RecentStepLimit <= 0 {
		config.RecentStepLimit = 32
	}
	return &ContinuationProcessor{store: store, generator: generator, deliverer: deliverer, config: config}
}

func (p *ContinuationProcessor) ProcessRun(ctx context.Context, runID string) error {
	if p == nil || isNilRuntimeDependency(p.store) || isNilRuntimeDependency(p.generator) ||
		isNilRuntimeDependency(p.deliverer) ||
		strings.TrimSpace(runID) == "" || strings.TrimSpace(p.config.WorkerID) == "" ||
		p.config.LeaseTTL <= 0 || p.config.RetryDelay <= 0 {
		return errors.New("continuation processor is not configured")
	}
	repairAttempted := false
	for range 4 {
		step, err := p.store.ClaimContinuationStep(ctx, ContinuationClaim{
			RunID: runID, WorkerID: p.config.WorkerID,
			LeaseTTL: p.config.LeaseTTL, Now: p.config.Now(),
		})
		if errors.Is(err, ErrNotFound) {
			if !repairAttempted {
				repairAttempted = true
				if repairErr := p.store.RepairContinuation(ctx, runID, p.config.Now()); repairErr != nil &&
					!errors.Is(repairErr, ErrNotFound) {
					return repairErr
				}
				continue
			}
			return nil
		}
		if err != nil {
			return err
		}
		if err := p.processClaimed(ctx, step); err != nil {
			return err
		}
	}
	return errors.New("continuation stage limit exceeded")
}

func (p *ContinuationProcessor) processClaimed(ctx context.Context, step *AgentStep) error {
	lease := StepLease{
		StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
		LeaseTTL: p.config.LeaseTTL, Now: p.config.Now(),
	}
	if err := p.store.ValidateContinuationLease(ctx, lease); err != nil {
		return err
	}
	switch step.Kind {
	case StepKindDecide:
		input, err := p.store.LoadContinuationContext(ctx, LoadContinuationContextRequest{
			RunID: step.RunID, AnchorStepID: step.ID, RecentLimit: p.config.RecentStepLimit,
		})
		if err != nil {
			return p.retry(ctx, step, err)
		}
		decision, err := p.generator.Generate(ctx, input)
		if err != nil {
			return p.retry(ctx, step, err)
		}
		if decision.Decision == TurnDecisionWait {
			return p.retry(ctx, step, errors.New("bare wait decision has no durable wait contract"))
		}
		if err := p.store.ValidateContinuationLease(ctx, StepLease{
			StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
			LeaseTTL: p.config.LeaseTTL, Now: p.config.Now(),
		}); err != nil {
			return err
		}
		_, err = p.store.PersistDecision(ctx, PersistDecisionRequest{
			StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
			Decision: decision, FinishedAt: p.config.Now(),
		})
		if err != nil {
			if errors.Is(err, ErrLeaseLost) {
				return err
			}
			return p.retry(ctx, step, err)
		}
		return nil
	case StepKindReply:
		req, err := decodeReplyRequest(step.InputJSON)
		if err != nil {
			return p.retry(ctx, step, err)
		}
		messageID, err := p.deliverer.Deliver(ctx, req)
		if err != nil {
			return p.retry(ctx, step, err)
		}
		if err := p.store.CompleteReplyDelivery(ctx, CompleteReplyDeliveryRequest{
			StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
			MessageID: messageID, FinishedAt: p.config.Now(),
		}); err != nil {
			return err
		}
		return nil
	default:
		return p.retry(ctx, step, errors.New("unsupported continuation step kind"))
	}
}

func (p *ContinuationProcessor) retry(ctx context.Context, step *AgentStep, cause error) error {
	err := p.store.RetryContinuationStep(ctx, RetryStepRequest{
		StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
		ErrorText: cause.Error(), RetryAt: p.config.Now().Add(p.config.RetryDelay),
	})
	if err != nil {
		return err
	}
	return cause
}

func decodeReplyRequest(input string) (ReplyRequest, error) {
	if len(input) == 0 || len(input) > 16*1024 {
		return ReplyRequest{}, errors.New("frozen reply request has invalid size")
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewBufferString(input), 16*1024+1))
	decoder.DisallowUnknownFields()
	var req ReplyRequest
	if err := decoder.Decode(&req); err != nil {
		return ReplyRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ReplyRequest{}, errors.New("reply request must be one JSON document")
	}
	if req.Version != 1 || req.StepID == "" || req.RunID == "" || strings.TrimSpace(req.Text) == "" ||
		req.IdempotencyKey != req.StepID || (req.TriggerMessageID == "" && req.ChatID == "") {
		return ReplyRequest{}, errors.New("invalid frozen reply request")
	}
	return req, nil
}
