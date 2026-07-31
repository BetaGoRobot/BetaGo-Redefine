package agenticrollout

import (
	"context"
	"errors"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
)

type Capability string

const (
	ConversationRuntime  Capability = "conversation_runtime"
	CallbackContinuation Capability = "callback_continuation"
	ParallelEvaluation   Capability = "parallel_evaluation"
	AgentCard            Capability = "agent_card"
)

var orderedCapabilities = []Capability{
	ConversationRuntime,
	CallbackContinuation,
	ParallelEvaluation,
	AgentCard,
}

type OverrideState string

const (
	OverrideInherit  OverrideState = "inherit"
	OverrideEnabled  OverrideState = "enabled"
	OverrideDisabled OverrideState = "disabled"
)

type Source string

const (
	SourceDefault      Source = "default"
	SourceTOML         Source = "toml"
	SourceGlobalConfig Source = "global_config"
	SourceChatOverride Source = "chat_override"
)

type CapabilityState struct {
	Key       Capability    `json:"key"`
	Label     string        `json:"label"`
	Override  OverrideState `json:"override"`
	Baseline  bool          `json:"baseline"`
	Effective bool          `json:"effective"`
	Source    Source        `json:"source"`
	Available bool          `json:"available"`
	Reason    string        `json:"reason,omitempty"`
}

type ChatState struct {
	ChatID       string            `json:"chat_id"`
	Revision     string            `json:"revision"`
	Capabilities []CapabilityState `json:"capabilities"`
}

func (s ChatState) Capability(key Capability) CapabilityState {
	for _, state := range s.Capabilities {
		if state.Key == key {
			return state
		}
	}
	return CapabilityState{Key: key}
}

type ChangeSet map[Capability]OverrideState

type BatchRequest struct {
	ChatIDs           []string          `json:"chat_ids"`
	ExpectedRevisions map[string]string `json:"expected_revisions"`
	Changes           ChangeSet         `json:"changes"`
	DryRun            bool              `json:"dry_run"`
}

type BatchItem struct {
	ChatID string    `json:"chat_id"`
	Before ChatState `json:"before"`
	After  ChatState `json:"after"`
}

type BatchResult struct {
	DryRun bool        `json:"dry_run"`
	Items  []BatchItem `json:"items"`
}

type StaticPolicies struct {
	EvaluationAvailable        bool
	EvaluationAllows           func(string) bool
	AgentCardAvailable         bool
	AgentCardAllows            func(string) bool
	AgentCardUnavailableReason string
}

type ConfigStore interface {
	GetScopedBoolOverride(
		ctx context.Context,
		key appconfig.ConfigKey,
		scope appconfig.ConfigScope,
		chatID string,
		openID string,
	) (bool, bool)
	ApplyConfigMutations(
		ctx context.Context,
		mutations []appconfig.ConfigMutation,
		guard func() error,
	) error
}

var (
	ErrInvalidRequest = errors.New("invalid rollout request")
	ErrStaleRevision  = errors.New("stale rollout revision")
	ErrUnavailable    = errors.New("rollout capability unavailable")
	ErrPersistence    = errors.New("rollout persistence unavailable")
)
