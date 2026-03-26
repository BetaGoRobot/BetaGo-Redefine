package capability

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Kind names a capability runtime type.
type Kind string

const (
	KindCommand    Kind = "command"
	KindTool       Kind = "tool"
	KindCardAction Kind = "card_action"
	KindSchedule   Kind = "schedule"
	KindInternal   Kind = "internal"
)

// SideEffectLevel names a capability runtime type.
type SideEffectLevel string

const (
	SideEffectLevelNone          SideEffectLevel = "none"
	SideEffectLevelChatWrite     SideEffectLevel = "chat_write"
	SideEffectLevelExternalWrite SideEffectLevel = "external_write"
	SideEffectLevelAdminWrite    SideEffectLevel = "admin_write"
)

// Scope names a capability runtime type.
type Scope string

const (
	ScopeP2P      Scope = "p2p"
	ScopeGroup    Scope = "group"
	ScopeSchedule Scope = "schedule"
	ScopeCallback Scope = "callback"
)

// Meta carries capability runtime state.
type Meta struct {
	Name                  string          `json:"name"`
	Kind                  Kind            `json:"kind"`
	Description           string          `json:"description,omitempty"`
	SideEffectLevel       SideEffectLevel `json:"side_effect_level"`
	RequiresApproval      bool            `json:"requires_approval"`
	AllowCompatibleOutput bool            `json:"allow_compatible_output"`
	SupportsStreaming     bool            `json:"supports_streaming"`
	SupportsAsync         bool            `json:"supports_async"`
	SupportsSchedule      bool            `json:"supports_schedule"`
	Idempotent            bool            `json:"idempotent"`
	DefaultTimeout        time.Duration   `json:"default_timeout"`
	AllowedScopes         []Scope         `json:"allowed_scopes,omitempty"`
}

// AllowsScope implements capability runtime behavior.
func (m Meta) AllowsScope(scope Scope) bool {
	if len(m.AllowedScopes) == 0 {
		return true
	}
	for _, allowed := range m.AllowedScopes {
		if allowed == scope {
			return true
		}
	}
	return false
}

// Request carries capability runtime state.
type Request struct {
	SessionID   string `json:"session_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	StepID      string `json:"step_id,omitempty"`
	Scope       Scope  `json:"scope,omitempty"`
	ChatID      string `json:"chat_id,omitempty"`
	ActorOpenID string `json:"actor_open_id,omitempty"`
	InputText   string `json:"input_text,omitempty"`
	PayloadJSON []byte `json:"payload_json,omitempty"`
}

// Result carries capability runtime state.
type Result struct {
	OutputText               string `json:"output_text,omitempty"`
	OutputJSON               []byte `json:"output_json,omitempty"`
	ExternalRef              string `json:"external_ref,omitempty"`
	CompatibleReplyMessageID string `json:"compatible_reply_message_id,omitempty"`
	CompatibleReplyKind      string `json:"compatible_reply_kind,omitempty"`
	Async                    bool   `json:"async"`
}

// ApprovalSpec carries capability runtime state.
type ApprovalSpec struct {
	Type              string    `json:"type,omitempty"`
	Title             string    `json:"title,omitempty"`
	Summary           string    `json:"summary,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitempty"`
	ReservationStepID string    `json:"reservation_step_id,omitempty"`
	ReservationToken  string    `json:"reservation_token,omitempty"`
}

// ContinuationInput carries capability runtime state.
type ContinuationInput struct {
	PreviousResponseID string `json:"previous_response_id,omitempty"`
}

// CallInput carries capability runtime state.
type CallInput struct {
	Request      Request            `json:"request"`
	Approval     *ApprovalSpec      `json:"approval,omitempty"`
	Continuation *ContinuationInput `json:"continuation,omitempty"`
	QueueTail    []QueuedCall       `json:"queue_tail,omitempty"`
}

// CompletedCall carries capability runtime state.
type CompletedCall struct {
	CallID             string `json:"call_id,omitempty"`
	CapabilityName     string `json:"capability_name,omitempty"`
	Arguments          string `json:"arguments,omitempty"`
	Output             string `json:"output,omitempty"`
	PreviousResponseID string `json:"previous_response_id,omitempty"`
}

// QueuedCall carries capability runtime state.
type QueuedCall struct {
	CallID         string    `json:"call_id,omitempty"`
	CapabilityName string    `json:"capability_name,omitempty"`
	Input          CallInput `json:"input"`
}

// PlanPending carries capability runtime state.
type PlanPending struct {
	CallID         string `json:"call_id,omitempty"`
	CapabilityName string `json:"capability_name,omitempty"`
	Arguments      string `json:"arguments,omitempty"`
}

// Capability defines a capability runtime contract.
type Capability interface {
	Meta() Meta
	Execute(context.Context, Request) (Result, error)
}

// ResolveResultSummary implements capability runtime behavior.
func ResolveResultSummary(capabilityName string, result Result) string {
	if text := strings.TrimSpace(result.OutputText); text != "" {
		return text
	}
	if raw := strings.TrimSpace(string(result.OutputJSON)); raw != "" {
		return raw
	}
	if name := strings.TrimSpace(capabilityName); name != "" {
		return fmt.Sprintf("capability %s executed", name)
	}
	return "capability executed"
}
