package agentcard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

var (
	ErrCardValidationFailed = errors.New("agent card validation failed")
	ErrCardPolicyDenied     = errors.New("agent card policy denied")
	ErrCardCompileFailed    = errors.New("agent card compilation failed")
	ErrCardConflict         = errors.New("agent card interaction conflicts with persisted state")
	ErrCardNotFound         = errors.New("agent card surface not found")
)

type SurfaceStatus string

const (
	SurfaceStatusDraft      SurfaceStatus = "draft"
	SurfaceStatusSent       SurfaceStatus = "sent"
	SurfaceStatusSubmitted  SurfaceStatus = "submitted"
	SurfaceStatusProcessing SurfaceStatus = "processing"
	SurfaceStatusResolved   SurfaceStatus = "resolved"
	SurfaceStatusCancelled  SurfaceStatus = "cancelled"
	SurfaceStatusExpired    SurfaceStatus = "expired"
	SurfaceStatusFailed     SurfaceStatus = "failed"
)

type PatchStatus string

const (
	PatchStatusIdle    PatchStatus = "idle"
	PatchStatusPending PatchStatus = "pending"
	PatchStatusRunning PatchStatus = "running"
	PatchStatusFailed  PatchStatus = "failed"
)

type CardSurface struct {
	ID                   string
	RunID                string
	WaitStepID           string
	InteractionID        string
	ChatID               string
	ReplyToMessageID     string
	MessageID            string
	SpecVersion          string
	SpecJSON             string
	CompiledJSONRedacted string
	Status               SurfaceStatus
	Revision             int64
	ExpectedActorOpenID  string
	InteractionKind      string
	ExpiresAt            time.Time
	SubmittedAt          time.Time
	ProcessingAt         time.Time
	ResolvedAt           time.Time
	CancelledAt          time.Time
	FailedAt             time.Time
	LastActionID         string
	LastSourceRef        string
	PatchStatus          PatchStatus
	PatchAttemptCount    int32
	NextPatchAt          time.Time
	PatchWorkerID        string
	PatchLeaseExpiresAt  time.Time
	LastError            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ActorPolicyMode string

const (
	ActorPolicyOwner     ActorPolicyMode = "owner"
	ActorPolicyAnyMember ActorPolicyMode = "any_member"
)

type ActorPolicy struct {
	Mode   ActorPolicyMode `json:"mode"`
	OpenID string          `json:"open_id,omitempty"`
}

type TrustedActionDescriptor struct {
	ActionID        string          `json:"action_id"`
	Mode            ActionMode      `json:"mode"`
	Intent          string          `json:"intent"`
	ContinueAgent   bool            `json:"continue_agent"`
	CapabilityName  string          `json:"capability_name,omitempty"`
	CapabilityInput json.RawMessage `json:"capability_input,omitempty"`
}

type TrustedWaitInput struct {
	Version        int                       `json:"version"`
	ComposeKey     string                    `json:"compose_key"`
	SpecDigest     string                    `json:"spec_digest"`
	ActorPolicy    ActorPolicy               `json:"actor_policy"`
	ActionBindings []TrustedActionDescriptor `json:"action_bindings"`
}

type BeginCardInteractionRequest struct {
	SurfaceID            string
	RunID                string
	StepID               string
	InteractionID        string
	IdempotencyKey       string
	ExpectedRunRevision  int64
	Revision             int64
	TokenHash            string
	InteractionKind      string
	ExpiresAt            time.Time
	ExpectedActorOpenID  string
	ChatID               string
	ReplyToMessageID     string
	SpecVersion          string
	SpecJSON             string
	CompiledJSONRedacted string
	TrustedInput         json.RawMessage
	Projection           agentruntime.ProjectionDocument
}

func (r BeginCardInteractionRequest) Surface() *CardSurface {
	return &CardSurface{
		ID: r.SurfaceID, RunID: r.RunID, WaitStepID: r.StepID,
		InteractionID: r.InteractionID, ChatID: r.ChatID,
		ReplyToMessageID: r.ReplyToMessageID, SpecVersion: r.SpecVersion,
		SpecJSON: r.SpecJSON, CompiledJSONRedacted: r.CompiledJSONRedacted,
		Status: SurfaceStatusDraft, Revision: r.Revision,
		ExpectedActorOpenID: r.ExpectedActorOpenID,
		InteractionKind:     r.InteractionKind, ExpiresAt: r.ExpiresAt,
		PatchStatus: PatchStatusIdle,
	}
}

func (r BeginCardInteractionRequest) Validate() error {
	for name, value := range map[string]string{
		"surface_id": r.SurfaceID, "run_id": r.RunID, "step_id": r.StepID,
		"interaction_id": r.InteractionID, "idempotency_key": r.IdempotencyKey,
		"interaction_kind": r.InteractionKind, "chat_id": r.ChatID,
		"spec_version": r.SpecVersion,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if r.ExpectedRunRevision < 0 || r.Revision != r.ExpectedRunRevision+1 {
		return fmt.Errorf("revision must immediately follow expected run revision")
	}
	tokenHash, err := hex.DecodeString(r.TokenHash)
	if err != nil || len(tokenHash) != sha256.Size {
		return fmt.Errorf("token hash must be a SHA-256 hex digest")
	}
	if r.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	for name, document := range map[string][]byte{
		"spec_json":              []byte(r.SpecJSON),
		"compiled_json_redacted": []byte(r.CompiledJSONRedacted),
		"trusted_input":          r.TrustedInput,
	} {
		if !json.Valid(document) {
			return fmt.Errorf("%s must be valid JSON", name)
		}
	}
	return r.Projection.Validate()
}

type MarkSurfaceSentRequest struct {
	SurfaceID        string
	ExpectedRevision int64
	MessageID        string
	SourceRef        string
	SentAt           time.Time
}

type MarkSurfaceSendFailedRequest struct {
	SurfaceID        string
	ExpectedRevision int64
	SourceRef        string
	ErrorCode        string
	FailedAt         time.Time
}

type MarkSurfaceSendUncertainRequest struct {
	SurfaceID        string
	ExpectedRevision int64
	SourceRef        string
	ErrorCode        string
	ObservedAt       time.Time
}

type GetSurfaceRequest struct {
	RunID         string
	InteractionID string
}

type ClaimActionRequest struct {
	RunID                string
	StepID               string
	InteractionID        string
	ExpectedRevision     int64
	ActionID             string
	ActorOpenID          string
	MessageID            string
	ChatID               string
	PresentedToken       string
	InteractionKind      string
	ContinueAgent        bool
	SourceRef            string
	EventID              string
	FormValues           map[string]any
	InputName            string
	InputValue           string
	SelectedOption       string
	SelectedOptions      []string
	Checked              bool
	CompiledJSONRedacted string
	DesiredStatus        SurfaceStatus
	ClaimedAt            time.Time
}

type ActionClaim struct {
	Surface    *CardSurface
	Descriptor TrustedActionDescriptor
	Outcome    json.RawMessage
	Replay     bool
}

type TransitionSurfaceRequest struct {
	SurfaceID            string
	ExpectedRevision     int64
	From                 SurfaceStatus
	To                   SurfaceStatus
	CompiledJSONRedacted string
	ActionID             string
	ActorOpenID          string
	SourceRef            string
	OccurredAt           time.Time
}

type ClaimPatchRequest struct {
	SurfaceID        string
	ExpectedRevision int64
	WorkerID         string
	LeaseTTL         time.Duration
	Now              time.Time
}

type CompletePatchRequest struct {
	SurfaceID        string
	ExpectedRevision int64
	WorkerID         string
	AttemptCount     int32
	CompletedAt      time.Time
}

type RetryPatchRequest struct {
	SurfaceID        string
	ExpectedRevision int64
	WorkerID         string
	AttemptCount     int32
	ErrorCode        string
	FailedAt         time.Time
	RetryAt          time.Time
}

// InteractionStore is the minimum atomic persistence port required by Binder.
type InteractionStore interface {
	BeginCardInteraction(context.Context, BeginCardInteractionRequest) (*CardSurface, error)
}

// Store is the complete surface lifecycle contract. Task-specific services may
// depend on narrower interfaces so persistence features can roll out safely.
type Store interface {
	InteractionStore
	MarkSurfaceSent(context.Context, MarkSurfaceSentRequest) (*CardSurface, error)
	MarkSurfaceSendFailed(context.Context, MarkSurfaceSendFailedRequest) (*CardSurface, error)
	MarkSurfaceSendUncertain(context.Context, MarkSurfaceSendUncertainRequest) (*CardSurface, error)
	GetByInteraction(context.Context, GetSurfaceRequest) (*CardSurface, error)
	ClaimAction(context.Context, ClaimActionRequest) (*ActionClaim, error)
	TransitionSurface(context.Context, TransitionSurfaceRequest) (*CardSurface, error)
	ClaimPatch(context.Context, ClaimPatchRequest) (*CardSurface, error)
	CompletePatch(context.Context, CompletePatchRequest) error
	RetryPatch(context.Context, RetryPatchRequest) error
}
