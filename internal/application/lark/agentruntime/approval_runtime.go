package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	approvaldef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/approval"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

func (c *RunCoordinator) moveRunToWaitingApproval(
	ctx context.Context,
	run *AgentRun,
	revision int64,
	token string,
	requestedAt time.Time,
) (*AgentRun, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	if run == nil {
		return nil, fmt.Errorf("approval run is nil")
	}
	return c.runRepo.UpdateStatus(ctx, run.ID, revision, func(current *AgentRun) error {
		if current.Status != RunStatusRunning {
			return fmt.Errorf("%w: run status=%s", ErrApprovalStateConflict, current.Status)
		}
		current.Status = RunStatusWaitingApproval
		current.WaitingReason = WaitingReasonApproval
		current.WaitingToken = token
		current.CurrentStepIndex++
		current.UpdatedAt = requestedAt
		clearRunExecutionLiveness(current)
		return nil
	})
}

func newCompletedApprovalStep(
	runID, stepID string,
	index int,
	token string,
	request ApprovalRequest,
	requestedAt time.Time,
) (*AgentStep, error) {
	stepInput, err := request.EncodeStepState()
	if err != nil {
		return nil, err
	}
	return &AgentStep{
		ID:          stepID,
		RunID:       runID,
		Index:       index,
		Kind:        StepKindApprovalRequest,
		Status:      StepStatusCompleted,
		InputJSON:   stepInput,
		ExternalRef: token,
		CreatedAt:   requestedAt,
		StartedAt:   &requestedAt,
		FinishedAt:  &requestedAt,
	}, nil
}

// ReserveApproval implements agent runtime behavior.
func (c *RunCoordinator) ReserveApproval(ctx context.Context, input RequestApprovalInput) (*ApprovalRequest, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	if c.runtimeStore == nil {
		return nil, fmt.Errorf("approval reservation store unavailable")
	}

	requestedAt := normalizeObservedAt(input.RequestedAt)
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	}
	if err := input.Validate(requestedAt); err != nil {
		return nil, err
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, err
	}
	if run == nil || run.Status.IsTerminal() {
		return nil, fmt.Errorf("%w: run %s unavailable for reservation", ErrApprovalStateConflict, input.RunID)
	}

	reservation := ApprovalReservation{
		RunID:          strings.TrimSpace(input.RunID),
		StepID:         newRuntimeID("step"),
		Token:          newRuntimeID("approval"),
		ApprovalType:   strings.TrimSpace(input.ApprovalType),
		Title:          strings.TrimSpace(input.Title),
		Summary:        strings.TrimSpace(input.Summary),
		CapabilityName: strings.TrimSpace(input.CapabilityName),
		PayloadJSON:    append([]byte(nil), input.PayloadJSON...),
		RequestedAt:    requestedAt,
		ExpiresAt:      input.ExpiresAt.UTC(),
	}
	if err := reservation.Validate(requestedAt); err != nil {
		return nil, err
	}
	raw, err := reservation.Encode()
	if err != nil {
		return nil, err
	}
	if err := c.runtimeStore.SaveApprovalReservation(ctx, reservation.StepID, reservation.Token, raw, reservation.TTL(requestedAt)); err != nil {
		return nil, err
	}

	request := reservation.ApprovalRequest(run.Revision)
	if err := request.Validate(requestedAt); err != nil {
		return nil, err
	}
	return &request, nil
}

// ActivateReservedApproval implements agent runtime behavior.
func (c *RunCoordinator) ActivateReservedApproval(ctx context.Context, input ActivateReservedApprovalInput) (*ApprovalRequest, *ApprovalReservationDecision, error) {
	if c == nil {
		return nil, nil, fmt.Errorf("run coordinator is nil")
	}
	if c.runtimeStore == nil {
		return nil, nil, fmt.Errorf("approval reservation store unavailable")
	}

	requestedAt := normalizeObservedAt(input.RequestedAt)
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	}

	raw, err := c.runtimeStore.LoadApprovalReservation(ctx, strings.TrimSpace(input.StepID), strings.TrimSpace(input.Token))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) == 0 {
		return nil, nil, ErrApprovalReservationNotFound
	}

	reservation, err := approvaldef.DecodeApprovalReservation(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("decode approval reservation: %w", err)
	}
	if err := reservation.Validate(requestedAt); err != nil {
		return nil, nil, err
	}
	if inputRunID := strings.TrimSpace(input.RunID); inputRunID != "" && reservation.RunID != inputRunID {
		return nil, nil, fmt.Errorf("%w: reservation run_id=%s input run_id=%s", ErrApprovalStateConflict, reservation.RunID, inputRunID)
	}

	run, err := c.runRepo.GetByID(ctx, reservation.RunID)
	if err != nil {
		return nil, nil, err
	}
	if run == nil {
		return nil, nil, fmt.Errorf("%w: reserved run missing", ErrApprovalStateConflict)
	}

	updated := run
	switch {
	case run.Status == RunStatusRunning:
		updated, err = c.moveRunToWaitingApproval(ctx, run, run.Revision, reservation.Token, requestedAt)
		if err != nil {
			return nil, nil, err
		}
	case run.Status == RunStatusWaitingApproval &&
		run.WaitingReason == WaitingReasonApproval &&
		run.WaitingToken == reservation.Token:
	default:
		return nil, nil, fmt.Errorf("%w: run status=%s", ErrApprovalStateConflict, run.Status)
	}

	request := reservation.ApprovalRequest(updated.Revision)
	if err := c.appendReservedApprovalStepIfMissing(ctx, updated, reservation, request, requestedAt); err != nil {
		return nil, nil, err
	}
	if _, err := c.runtimeStore.ConsumeApprovalReservation(ctx, reservation.StepID, reservation.Token); err != nil && !errors.Is(err, ErrApprovalReservationNotFound) {
		logs.L().Ctx(ctx).Warn("consume approval reservation after activation failed",
			zap.String("run_id", reservation.RunID),
			zap.String("step_id", reservation.StepID),
			zap.String("token", reservation.Token),
			zap.Error(err),
		)
	}

	var decision *ApprovalReservationDecision
	if reservation.Decision != nil {
		normalized := approvaldef.NewApprovalReservationDecision(reservation.Decision.Outcome, reservation.Decision.ActorOpenID, reservation.Decision.OccurredAt)
		decision = &normalized
	}
	return &request, decision, nil
}

func (c *RunCoordinator) appendReservedApprovalStepIfMissing(
	ctx context.Context,
	run *AgentRun,
	reservation ApprovalReservation,
	request ApprovalRequest,
	requestedAt time.Time,
) error {
	if c == nil {
		return fmt.Errorf("run coordinator is nil")
	}
	if c.stepRepo == nil {
		return fmt.Errorf("approval step repository unavailable")
	}
	if run == nil {
		return fmt.Errorf("approval run is nil")
	}

	steps, err := c.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step == nil || strings.TrimSpace(step.ID) != reservation.StepID {
			continue
		}
		if step.Kind != StepKindApprovalRequest {
			return fmt.Errorf("%w: approval step kind=%s", ErrApprovalStateConflict, step.Kind)
		}
		return nil
	}

	step, err := newCompletedApprovalStep(run.ID, reservation.StepID, run.CurrentStepIndex, reservation.Token, request, requestedAt)
	if err != nil {
		return err
	}
	return c.stepRepo.Append(ctx, step)
}
