package conversationeval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrCandidateTaskNotFound  = errors.New("evaluation candidate task not found")
	ErrCandidateTaskLeaseLost = errors.New("evaluation candidate task lease lost")
)

type CandidateTaskStatus string

const (
	CandidateTaskQueued    CandidateTaskStatus = "queued"
	CandidateTaskRunning   CandidateTaskStatus = "running"
	CandidateTaskCompleted CandidateTaskStatus = "completed"
)

type CandidateTaskLease struct {
	Task           CandidateTask
	Status         CandidateTaskStatus
	AttemptCount   int32
	WorkerID       string
	LeaseExpiresAt time.Time
}

type CandidateTaskClaim struct {
	WorkerID string
	LeaseTTL time.Duration
	Now      time.Time
}

func (c CandidateTaskClaim) Validate() error {
	if err := validateID("candidate worker_id", c.WorkerID); err != nil {
		return err
	}
	if c.LeaseTTL <= 0 || c.Now.IsZero() {
		return contractError("candidate claim requires positive lease_ttl and now")
	}
	return nil
}

type CompleteCandidateTaskRequest struct {
	TaskID       string
	WorkerID     string
	AttemptCount int32
	FinishedAt   time.Time
}

func (r CompleteCandidateTaskRequest) Validate() error {
	if err := validateCandidateLeaseIdentity(r.TaskID, r.WorkerID, r.AttemptCount); err != nil {
		return err
	}
	if r.FinishedAt.IsZero() {
		return contractError("candidate task finished_at must not be zero")
	}
	return nil
}

type RetryCandidateTaskRequest struct {
	TaskID       string
	WorkerID     string
	AttemptCount int32
	ErrorText    string
	FailedAt     time.Time
	RetryAt      time.Time
}

func (r RetryCandidateTaskRequest) Validate() error {
	if err := validateCandidateLeaseIdentity(r.TaskID, r.WorkerID, r.AttemptCount); err != nil {
		return err
	}
	if strings.TrimSpace(r.ErrorText) == "" || r.FailedAt.IsZero() || r.RetryAt.IsZero() {
		return contractError("candidate retry requires error_text, failed_at, and retry_at")
	}
	if len(r.ErrorText) > 4096 {
		return contractError("candidate retry error_text exceeds 4096 bytes")
	}
	return nil
}

type CandidateTaskQueue interface {
	CandidateSubmitter
	ClaimCandidate(context.Context, CandidateTaskClaim) (*CandidateTaskLease, error)
	CompleteCandidateTask(context.Context, CompleteCandidateTaskRequest) error
	RetryCandidateTask(context.Context, RetryCandidateTaskRequest) error
}

func (t CandidateTask) Validate() error {
	for name, value := range map[string]string{
		"candidate task id": t.ID, "candidate output_id": t.OutputID,
	} {
		if err := validateID(name, value); err != nil {
			return err
		}
	}
	if err := t.Cohort.Validate(); err != nil {
		return fmt.Errorf("candidate task cohort: %w", err)
	}
	if err := t.Episode.Validate(); err != nil {
		return fmt.Errorf("candidate task episode: %w", err)
	}
	if err := t.Message.Validate(); err != nil {
		return fmt.Errorf("candidate task message: %w", err)
	}
	if t.Episode.CohortID != t.Cohort.ID ||
		t.Episode.AnchorEventID != t.Message.EventID ||
		t.Episode.AnchorMessageID != t.Message.MessageID ||
		t.Episode.ChatID != t.Message.ChatID {
		return contractError("candidate task entity anchors do not match")
	}
	if !t.ContextSnapshot.AnchorAt.Equal(t.Episode.AnchorAt) ||
		t.ContextSnapshot.AnchorEventID != t.Episode.AnchorEventID {
		return contractError("candidate task context anchor does not match episode")
	}
	if err := t.ContextSnapshot.Validate(); err != nil {
		return err
	}
	if t.CreatedAt.IsZero() {
		return contractError("candidate task created_at must not be zero")
	}
	return nil
}

func validateCandidateLeaseIdentity(taskID, workerID string, attemptCount int32) error {
	if err := validateID("candidate task_id", taskID); err != nil {
		return err
	}
	if err := validateID("candidate worker_id", workerID); err != nil {
		return err
	}
	if attemptCount <= 0 {
		return contractError("candidate attempt_count must be positive")
	}
	return nil
}
