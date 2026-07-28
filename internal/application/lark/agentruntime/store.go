package agentruntime

import (
	"context"
	"encoding/json"
	"time"
)

type ProjectionDocument struct {
	IndexAlias string
	DocumentID string
	Payload    json.RawMessage
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

type ResolveInteractionRequest struct {
	RunID         string
	StepID        string
	InteractionID string
	Revision      int64
	TokenHash     string
	Action        string
	Outcome       string
	EventID       string
	SourceRef     string
	ResolvedAt    time.Time
	Projection    ProjectionDocument
}

type StepClaim struct {
	WorkerID string
	LeaseTTL time.Duration
	Now      time.Time
}

type Store interface {
	GetOrCreateSession(context.Context, *AgentSession) (*AgentSession, error)
	FindRunBySessionAndTriggerMessage(context.Context, string, string) (*AgentRun, error)
	CreateRun(context.Context, *AgentRun) error
	UpdateSessionActiveRun(context.Context, string, string, string, string) (*AgentSession, error)
	CreateStep(context.Context, *AgentStep) error
	FindActiveRun(context.Context, string) (*AgentRun, error)
	StartInteraction(context.Context, StartInteractionRequest) (*AgentRun, *AgentStep, error)
	ResolveInteraction(context.Context, ResolveInteractionRequest) (*AgentRun, *AgentStep, error)
	AppendEvent(context.Context, *AgentStep, ProjectionDocument) (*AgentStep, error)
	ClaimQueuedStep(context.Context, StepClaim) (*AgentStep, error)
	CompleteStep(context.Context, string, string) error
	RetryStep(context.Context, string, string, time.Time) error
	ReclaimStaleSteps(context.Context, time.Time, int) (int64, error)
}
