package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	errRunExecutionLivenessNotRenewable = errors.New("agent runtime run execution liveness not renewable")
	errRunExecutionWorkerMismatch       = errors.New("agent runtime run execution worker mismatch")
)

type replyLifecycleUpdate struct {
	status           RunStatus
	currentStepIndex int
	lastResponseID   string
	resultSummary    string
	updatedAt        time.Time
	finishedAt       *time.Time
}

func (c *RunCoordinator) appendCompletedCapabilityCalls(
	ctx context.Context,
	runID string,
	nextIndex int,
	calls []CompletedCapabilityCall,
	recordedAt time.Time,
) (int, error) {
	for _, call := range calls {
		step, err := newCompletedCapabilityStep(runID, nextIndex, call, recordedAt)
		if err != nil {
			return 0, err
		}
		if err := c.stepRepo.Append(ctx, step); err != nil {
			return 0, err
		}
		nextIndex++

		observeStep, err := newCompletedCapabilityObserveStep(runID, nextIndex, call, recordedAt)
		if err != nil {
			return 0, err
		}
		if err := c.stepRepo.Append(ctx, observeStep); err != nil {
			return 0, err
		}
		nextIndex++
	}
	return nextIndex, nil
}

func newReplyCompletionStep(runID string, index int, output replyCompletionOutput, completedAt time.Time) (*AgentStep, error) {
	replyOutput, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}

	replyExternalRef := strings.TrimSpace(output.ResponseCardID)
	if replyExternalRef == "" {
		replyExternalRef = strings.TrimSpace(output.ResponseMessageID)
	}
	return &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       runID,
		Index:       index,
		Kind:        StepKindReply,
		Status:      StepStatusCompleted,
		OutputJSON:  replyOutput,
		ExternalRef: replyExternalRef,
		CreatedAt:   completedAt,
		StartedAt:   &completedAt,
		FinishedAt:  &completedAt,
	}, nil
}

func (c *RunCoordinator) moveRunToRunning(ctx context.Context, run *AgentRun, startedAt time.Time) (*AgentRun, error) {
	return c.startRunExecution(ctx, run, "", startedAt, RunLeasePolicy{})
}

func (c *RunCoordinator) startRunExecution(
	ctx context.Context,
	run *AgentRun,
	workerID string,
	startedAt time.Time,
	policy RunLeasePolicy,
) (*AgentRun, error) {
	if run == nil {
		return nil, fmt.Errorf("agent runtime run is nil")
	}
	startedAt = normalizeObservedAt(startedAt)
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		if run.Status == RunStatusRunning {
			if run.StartedAt == nil {
				run.StartedAt = &startedAt
			}
			return run, nil
		}
		return c.updateRunReplyLifecycle(ctx, run, replyLifecycleUpdate{
			status:    RunStatusRunning,
			updatedAt: startedAt,
		})
	}

	policy = policy.Normalize()
	return c.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.Status = RunStatusRunning
		current.WaitingReason = WaitingReasonNone
		current.WaitingToken = ""
		current.ErrorText = ""
		current.UpdatedAt = startedAt
		if current.StartedAt == nil {
			current.StartedAt = &startedAt
		}
		applyRunExecutionLiveness(current, workerID, startedAt, policy)
		return nil
	})
}

func (c *RunCoordinator) refreshRunExecutionLiveness(
	ctx context.Context,
	runID, workerID string,
	observedAt time.Time,
	policy RunLeasePolicy,
) (*AgentRun, error) {
	if c == nil || c.runRepo == nil {
		return nil, nil
	}
	runID = strings.TrimSpace(runID)
	workerID = strings.TrimSpace(workerID)
	if runID == "" || workerID == "" {
		return nil, nil
	}

	run, err := c.runRepo.GetByID(ctx, runID)
	if err != nil || run == nil {
		return nil, err
	}
	switch run.Status {
	case RunStatusQueued, RunStatusRunning:
	default:
		return nil, nil
	}

	observedAt = normalizeObservedAt(observedAt)
	policy = policy.Normalize()
	updated, err := c.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		switch current.Status {
		case RunStatusQueued, RunStatusRunning:
		default:
			return errRunExecutionLivenessNotRenewable
		}
		if existingWorkerID := strings.TrimSpace(current.WorkerID); existingWorkerID != "" && existingWorkerID != workerID {
			return errRunExecutionWorkerMismatch
		}
		current.UpdatedAt = observedAt
		if current.StartedAt == nil {
			current.StartedAt = &observedAt
		}
		applyRunExecutionLiveness(current, workerID, observedAt, policy)
		return nil
	})
	if errors.Is(err, errRunExecutionLivenessNotRenewable) || errors.Is(err, errRunExecutionWorkerMismatch) {
		return nil, nil
	}
	return updated, err
}

func (c *RunCoordinator) updateRunReplyLifecycle(ctx context.Context, run *AgentRun, update replyLifecycleUpdate) (*AgentRun, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	if run == nil {
		return nil, fmt.Errorf("agent runtime run is nil")
	}
	return c.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.Status = update.status
		if update.currentStepIndex > 0 {
			current.CurrentStepIndex = update.currentStepIndex
		}
		if responseID := strings.TrimSpace(update.lastResponseID); responseID != "" {
			current.LastResponseID = responseID
		}
		if summary := strings.TrimSpace(update.resultSummary); summary != "" {
			current.ResultSummary = summary
		}
		current.WaitingReason = WaitingReasonNone
		current.WaitingToken = ""
		current.ErrorText = ""
		if update.status.IsTerminal() {
			clearRunExecutionLiveness(current)
		} else if update.status == RunStatusQueued {
			seedQueuedRunLiveness(current, update.updatedAt, DefaultRunLeasePolicy())
		}
		current.UpdatedAt = update.updatedAt
		current.FinishedAt = update.finishedAt
		if current.StartedAt == nil {
			current.StartedAt = &update.updatedAt
		}
		return nil
	})
}
