package agentruntime

import (
	"context"
	"errors"
	"strings"
	"time"
)

const disabledContinuationReason = "callback continuation disabled"

type SuppressReplyDeliveryRequest struct {
	StepID       string
	WorkerID     string
	AttemptCount int32
	Reason       string
	FinishedAt   time.Time
}

func (r SuppressReplyDeliveryRequest) Validate() error {
	if err := validateCanonical("step_id", r.StepID); err != nil {
		return err
	}
	if err := validateCanonical("worker_id", r.WorkerID); err != nil {
		return err
	}
	if err := validateCanonical("reason", r.Reason); err != nil {
		return err
	}
	if r.AttemptCount <= 0 {
		return invalidRuntimeContract("attempt_count must be positive")
	}
	if r.FinishedAt.IsZero() {
		return invalidRuntimeContract("finished_at is required")
	}
	return nil
}

type DisabledContinuationStore interface {
	ContinuationStore
	SuppressReplyDelivery(context.Context, SuppressReplyDeliveryRequest) error
}

type DisabledContinuationProcessorConfig struct {
	WorkerID string
	LeaseTTL time.Duration
	Now      func() time.Time
}

type DisabledContinuationProcessor struct {
	store  DisabledContinuationStore
	config DisabledContinuationProcessorConfig
}

func NewDisabledContinuationProcessor(
	store DisabledContinuationStore,
	config DisabledContinuationProcessorConfig,
) *DisabledContinuationProcessor {
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &DisabledContinuationProcessor{store: store, config: config}
}

func (p *DisabledContinuationProcessor) ProcessRun(ctx context.Context, runID string) error {
	if p == nil || isNilRuntimeDependency(p.store) ||
		strings.TrimSpace(runID) == "" ||
		strings.TrimSpace(p.config.WorkerID) == "" ||
		p.config.LeaseTTL <= 0 ||
		p.config.Now == nil {
		return errors.New("disabled continuation processor is not configured")
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
		lease := StepLease{
			StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
			LeaseTTL: p.config.LeaseTTL, Now: p.config.Now(),
		}
		if err := p.store.ValidateContinuationLease(ctx, lease); err != nil {
			return err
		}
		switch step.Kind {
		case StepKindDecide:
			_, err = p.store.PersistDecision(ctx, PersistDecisionRequest{
				StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
				Decision: TurnDecision{
					Decision: TurnDecisionObserveOnly,
					Reason:   disabledContinuationReason,
				},
				FinishedAt: p.config.Now(),
			})
		case StepKindReply:
			err = p.store.SuppressReplyDelivery(ctx, SuppressReplyDeliveryRequest{
				StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
				Reason: disabledContinuationReason, FinishedAt: p.config.Now(),
			})
		default:
			err = ErrInvalidRuntimeContract
		}
		if err != nil {
			return err
		}
	}
	return errors.New("disabled continuation stage limit exceeded")
}
