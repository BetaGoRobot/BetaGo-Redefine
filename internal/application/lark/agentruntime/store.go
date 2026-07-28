package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrLeaseLost                = errors.New("agent step lease lost")
	ErrActiveRunConflict        = errors.New("session already has a different active run")
	ErrInteractionConflict      = errors.New("interaction state conflicts with the request")
	ErrInteractionExpired       = errors.New("interaction has expired")
	ErrInteractionTokenMismatch = errors.New("interaction token does not match")
	ErrTerminalRun              = errors.New("agent run is terminal")
)

type ProjectionDocument struct {
	IndexAlias string
	DocumentID string
	Payload    json.RawMessage
}

func (d ProjectionDocument) Validate() error {
	if err := validateCanonical("index_alias", d.IndexAlias); err != nil {
		return err
	}
	if err := validateCanonical("document_id", d.DocumentID); err != nil {
		return err
	}
	return validateJSONDocument("projection payload", d.Payload)
}

type StartInteractionRequest struct {
	RunID           string
	StepID          string
	InteractionID   string
	Revision        int64
	TokenHash       string
	InteractionKind string
	ExpiresAt       time.Time
	Projection      ProjectionDocument
}

func (r StartInteractionRequest) Validate() error {
	if err := validateCanonical("run_id", r.RunID); err != nil {
		return err
	}
	if err := validateCanonical("step_id", r.StepID); err != nil {
		return err
	}
	if err := validateCanonical("interaction_id", r.InteractionID); err != nil {
		return err
	}
	if err := validateCanonical("interaction_kind", r.InteractionKind); err != nil {
		return err
	}
	if r.Revision <= 0 {
		return invalidRuntimeContract("revision must be positive")
	}
	tokenHash, err := hex.DecodeString(r.TokenHash)
	if err != nil || len(tokenHash) != sha256.Size {
		return invalidRuntimeContract("token_hash must be a SHA-256 hex digest")
	}
	if r.ExpiresAt.IsZero() {
		return invalidRuntimeContract("expires_at is required")
	}
	return r.Projection.Validate()
}

type ResolveInteractionRequest struct {
	RunID          string
	StepID         string
	InteractionID  string
	Revision       int64
	PresentedToken string
	Action         string
	Outcome        json.RawMessage
	EventID        string
	SourceRef      string
	ResolvedAt     time.Time
	Projection     ProjectionDocument
}

func (r ResolveInteractionRequest) Validate() error {
	if err := validateCanonical("run_id", r.RunID); err != nil {
		return err
	}
	if err := validateCanonical("step_id", r.StepID); err != nil {
		return err
	}
	if err := validateCanonical("interaction_id", r.InteractionID); err != nil {
		return err
	}
	if err := validateCanonical("presented_token", r.PresentedToken); err != nil {
		return err
	}
	if err := validateCanonical("action", r.Action); err != nil {
		return err
	}
	if r.Revision <= 0 {
		return invalidRuntimeContract("revision must be positive")
	}
	if err := validateJSONDocument("outcome", r.Outcome); err != nil {
		return err
	}
	if r.EventID == "" && r.SourceRef == "" {
		return invalidRuntimeContract("event_id or source_ref is required")
	}
	if r.EventID != "" {
		if err := validateCanonical("event_id", r.EventID); err != nil {
			return err
		}
	}
	if r.SourceRef != "" {
		if err := validateCanonical("source_ref", r.SourceRef); err != nil {
			return err
		}
	}
	if r.ResolvedAt.IsZero() {
		return invalidRuntimeContract("resolved_at is required")
	}
	return r.Projection.Validate()
}

type StepClaim struct {
	WorkerID string
	LeaseTTL time.Duration
	Now      time.Time
}

func (c StepClaim) Validate() error {
	if err := validateCanonical("worker_id", c.WorkerID); err != nil {
		return err
	}
	if c.LeaseTTL <= 0 {
		return invalidRuntimeContract("lease_ttl must be positive")
	}
	if c.Now.IsZero() {
		return invalidRuntimeContract("now is required")
	}
	return nil
}

type CompleteStepRequest struct {
	StepID       string
	WorkerID     string
	AttemptCount int32
	Output       json.RawMessage
	FinishedAt   time.Time
}

func (r CompleteStepRequest) Validate() error {
	if err := validateCanonical("step_id", r.StepID); err != nil {
		return err
	}
	if err := validateCanonical("worker_id", r.WorkerID); err != nil {
		return err
	}
	if r.AttemptCount <= 0 {
		return invalidRuntimeContract("attempt_count must be positive")
	}
	if err := validateJSONDocument("output", r.Output); err != nil {
		return err
	}
	if r.FinishedAt.IsZero() {
		return invalidRuntimeContract("finished_at is required")
	}
	return nil
}

type RetryStepRequest struct {
	StepID       string
	WorkerID     string
	AttemptCount int32
	ErrorText    string
	RetryAt      time.Time
}

func (r RetryStepRequest) Validate() error {
	if err := validateCanonical("step_id", r.StepID); err != nil {
		return err
	}
	if err := validateCanonical("worker_id", r.WorkerID); err != nil {
		return err
	}
	if r.AttemptCount <= 0 {
		return invalidRuntimeContract("attempt_count must be positive")
	}
	if strings.TrimSpace(r.ErrorText) == "" {
		return invalidRuntimeContract("error_text is required")
	}
	if r.RetryAt.IsZero() {
		return invalidRuntimeContract("retry_at is required")
	}
	return nil
}

type ReclaimStaleStepsRequest struct {
	Now   time.Time
	Limit int
}

func (r ReclaimStaleStepsRequest) Validate() error {
	if r.Now.IsZero() {
		return invalidRuntimeContract("now is required")
	}
	if r.Limit <= 0 {
		return invalidRuntimeContract("limit must be positive")
	}
	return nil
}

type CoordinatorStore interface {
	GetOrCreateSession(context.Context, *AgentSession) (*AgentSession, error)
	FindRunBySessionAndTriggerMessage(context.Context, string, string) (*AgentRun, error)
	CreateRun(context.Context, *AgentRun) error
	UpdateSessionActiveRun(context.Context, string, string, string, string) (*AgentSession, error)
	CreateStep(context.Context, *AgentStep) error
}

type InteractionStore interface {
	FindActiveRun(context.Context, string) (*AgentRun, error)
	StartInteraction(context.Context, StartInteractionRequest) (*AgentRun, *AgentStep, error)
	ResolveInteraction(context.Context, ResolveInteractionRequest) (*AgentRun, *AgentStep, error)
}

type QueueStore interface {
	AppendEvent(context.Context, *AgentStep, ProjectionDocument) (*AgentStep, error)
	ClaimQueuedStep(context.Context, StepClaim) (*AgentStep, error)
	CompleteStep(context.Context, CompleteStepRequest) error
	RetryStep(context.Context, RetryStepRequest) error
	ReclaimStaleSteps(context.Context, ReclaimStaleStepsRequest) (int64, error)
}

type Store interface {
	CoordinatorStore
	InteractionStore
	QueueStore
}

func validateJSONDocument(field string, document json.RawMessage) error {
	if len(document) == 0 || !json.Valid(document) {
		return invalidRuntimeContract(field + " must be valid JSON")
	}
	return nil
}
