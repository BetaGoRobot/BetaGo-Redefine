package agentruntime

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type CapabilityKind string

const (
	CapabilityKindCommand    CapabilityKind = "command"
	CapabilityKindTool       CapabilityKind = "tool"
	CapabilityKindCardAction CapabilityKind = "card_action"
	CapabilityKindSchedule   CapabilityKind = "schedule"
	CapabilityKindInternal   CapabilityKind = "internal"
)

type SideEffectLevel string

const (
	SideEffectLevelNone          SideEffectLevel = "none"
	SideEffectLevelChatWrite     SideEffectLevel = "chat_write"
	SideEffectLevelExternalWrite SideEffectLevel = "external_write"
	SideEffectLevelAdminWrite    SideEffectLevel = "admin_write"
)

type CapabilityScope string

const (
	CapabilityScopeP2P      CapabilityScope = "p2p"
	CapabilityScopeGroup    CapabilityScope = "group"
	CapabilityScopeSchedule CapabilityScope = "schedule"
	CapabilityScopeCallback CapabilityScope = "callback"
)

type CapabilityMeta struct {
	Name                  string            `json:"name"`
	Kind                  CapabilityKind    `json:"kind"`
	Description           string            `json:"description,omitempty"`
	SideEffectLevel       SideEffectLevel   `json:"side_effect_level"`
	RequiresApproval      bool              `json:"requires_approval"`
	AllowCompatibleOutput bool              `json:"allow_compatible_output"`
	SupportsStreaming     bool              `json:"supports_streaming"`
	SupportsAsync         bool              `json:"supports_async"`
	SupportsSchedule      bool              `json:"supports_schedule"`
	Idempotent            bool              `json:"idempotent"`
	DefaultTimeout        time.Duration     `json:"default_timeout"`
	AllowedScopes         []CapabilityScope `json:"allowed_scopes,omitempty"`
}

func (m CapabilityMeta) AllowsScope(scope CapabilityScope) bool {
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

type CapabilityRequest struct {
	SessionID   string          `json:"session_id,omitempty"`
	RunID       string          `json:"run_id,omitempty"`
	StepID      string          `json:"step_id,omitempty"`
	Scope       CapabilityScope `json:"scope,omitempty"`
	ChatID      string          `json:"chat_id,omitempty"`
	ActorOpenID string          `json:"actor_open_id,omitempty"`
	InputText   string          `json:"input_text,omitempty"`
	PayloadJSON []byte          `json:"payload_json,omitempty"`
}

type CapabilityResult struct {
	OutputText               string `json:"output_text,omitempty"`
	OutputJSON               []byte `json:"output_json,omitempty"`
	ExternalRef              string `json:"external_ref,omitempty"`
	CompatibleReplyMessageID string `json:"compatible_reply_message_id,omitempty"`
	CompatibleReplyKind      string `json:"compatible_reply_kind,omitempty"`
	Async                    bool   `json:"async"`
}

type Capability interface {
	Meta() CapabilityMeta
	Execute(ctx context.Context, req CapabilityRequest) (CapabilityResult, error)
}

type CapabilityRegistry struct {
	mu           sync.RWMutex
	capabilities map[string]Capability
}

func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{
		capabilities: make(map[string]Capability),
	}
}

func (r *CapabilityRegistry) Register(capability Capability) error {
	if capability == nil {
		return fmt.Errorf("capability is nil")
	}
	meta := capability.Meta()
	if meta.Name == "" {
		return fmt.Errorf("capability name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.capabilities[meta.Name]; exists {
		return fmt.Errorf("capability already registered: %s", meta.Name)
	}
	r.capabilities[meta.Name] = capability
	return nil
}

func (r *CapabilityRegistry) Get(name string) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	capability, ok := r.capabilities[name]
	return capability, ok
}

func (r *CapabilityRegistry) Lookup(name string, scope CapabilityScope) (Capability, error) {
	capability, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("capability not found: %s", name)
	}
	if !capability.Meta().AllowsScope(scope) {
		return nil, fmt.Errorf("capability %s does not allow scope %s", name, scope)
	}
	return capability, nil
}

func (r *CapabilityRegistry) List() []CapabilityMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()

	metas := make([]CapabilityMeta, 0, len(r.capabilities))
	for _, capability := range r.capabilities {
		metas = append(metas, capability.Meta())
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].Name < metas[j].Name
	})
	return metas
}
