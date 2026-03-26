package shadow

import (
	"context"
	"strings"
	"time"

	capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"
)

// TriggerType names a shadow observation type.
type TriggerType string

const (
	TriggerTypeMention       TriggerType = "mention"
	TriggerTypeReplyToBot    TriggerType = "reply_to_bot"
	TriggerTypeCommandBridge TriggerType = "command_bridge"
	TriggerTypeFollowUp      TriggerType = "follow_up"
	TriggerTypeP2P           TriggerType = "p2p"
)

// MessageSignal carries shadow observation state.
type MessageSignal struct {
	Now         time.Time `json:"now"`
	ChatType    string    `json:"chat_type,omitempty"`
	Mentioned   bool      `json:"mentioned"`
	ReplyToBot  bool      `json:"reply_to_bot"`
	IsCommand   bool      `json:"is_command"`
	CommandName string    `json:"command_name,omitempty"`
	ActorOpenID string    `json:"actor_open_id,omitempty"`
}

// ActiveRunSnapshot carries shadow observation state.
type ActiveRunSnapshot struct {
	ID           string    `json:"id"`
	ActorOpenID  string    `json:"actor_open_id,omitempty"`
	Status       string    `json:"status"`
	LastActiveAt time.Time `json:"last_active_at"`
}

// TriggerDecision carries shadow observation state.
type TriggerDecision struct {
	EnterRuntime bool        `json:"enter_runtime"`
	TriggerType  TriggerType `json:"trigger_type,omitempty"`
	Reason       string      `json:"reason,omitempty"`
}

// OwnershipDecision carries shadow observation state.
type OwnershipDecision struct {
	AttachToRunID   string `json:"attach_to_run_id,omitempty"`
	SupersedeRunID  string `json:"supersede_run_id,omitempty"`
	OwnershipReason string `json:"ownership_reason,omitempty"`
}

// PolicyDecision carries shadow observation state.
type PolicyDecision struct {
	EnterRuntime   bool        `json:"enter_runtime"`
	TriggerType    TriggerType `json:"trigger_type,omitempty"`
	AttachToRunID  string      `json:"attach_to_run_id,omitempty"`
	SupersedeRunID string      `json:"supersede_run_id,omitempty"`
	Reason         string      `json:"reason,omitempty"`
}

// ObserveInput carries shadow observation state.
type ObserveInput struct {
	Now         time.Time `json:"now"`
	ChatID      string    `json:"chat_id,omitempty"`
	ChatType    string    `json:"chat_type,omitempty"`
	Mentioned   bool      `json:"mentioned"`
	ReplyToBot  bool      `json:"reply_to_bot"`
	IsCommand   bool      `json:"is_command"`
	CommandName string    `json:"command_name,omitempty"`
	ActorOpenID string    `json:"actor_open_id,omitempty"`
	InputText   string    `json:"input_text,omitempty"`
}

// Observation carries shadow observation state.
type Observation struct {
	PolicyDecision
	Scope                 capdef.Scope `json:"scope,omitempty"`
	CandidateCapabilities []string     `json:"candidate_capabilities,omitempty"`
}

// ActiveRunProvider names a shadow observation type.
type ActiveRunProvider func(context.Context, string, string) *ActiveRunSnapshot

// TriggerPolicy defines a shadow observation contract.
type TriggerPolicy interface {
	EvaluateTrigger(signal MessageSignal, active *ActiveRunSnapshot) TriggerDecision
}

// OwnershipPolicy defines a shadow observation contract.
type OwnershipPolicy interface {
	EvaluateOwnership(signal MessageSignal, active *ActiveRunSnapshot, trigger TriggerDecision) OwnershipDecision
}

// GroupPolicy defines a shadow observation contract.
type GroupPolicy interface {
	Decide(signal MessageSignal, active *ActiveRunSnapshot) PolicyDecision
}

// ScopeFromChatType implements shadow observation behavior.
func ScopeFromChatType(chatType string) capdef.Scope {
	if strings.EqualFold(strings.TrimSpace(chatType), "p2p") {
		return capdef.ScopeP2P
	}
	return capdef.ScopeGroup
}
