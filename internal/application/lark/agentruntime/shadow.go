package agentruntime

import (
	"context"
	"strings"
	"time"
)

type ShadowObserveInput struct {
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

type ShadowObservation struct {
	PolicyDecision
	Scope                 CapabilityScope `json:"scope,omitempty"`
	CandidateCapabilities []string        `json:"candidate_capabilities,omitempty"`
}

type ActiveRunProvider func(context.Context, string) *ActiveRunSnapshot

type ShadowObserver struct {
	policy         GroupPolicy
	registry       *CapabilityRegistry
	activeProvider ActiveRunProvider
}

func NewShadowObserver(policy GroupPolicy, registry *CapabilityRegistry, activeProvider ActiveRunProvider) *ShadowObserver {
	if policy == nil {
		policy = NewDefaultGroupPolicy(DefaultGroupPolicyConfig{})
	}
	if registry == nil {
		registry = NewCapabilityRegistry()
	}
	return &ShadowObserver{
		policy:         policy,
		registry:       registry,
		activeProvider: activeProvider,
	}
}

func (o *ShadowObserver) Observe(ctx context.Context, input ShadowObserveInput) ShadowObservation {
	if input.Now.IsZero() {
		input.Now = time.Now()
	}

	var active *ActiveRunSnapshot
	if o.activeProvider != nil {
		active = o.activeProvider(ctx, strings.TrimSpace(input.ChatID))
	}

	scope := capabilityScopeFromChatType(input.ChatType)
	decision := o.policy.Decide(MessageSignal{
		Now:         input.Now,
		ChatType:    input.ChatType,
		Mentioned:   input.Mentioned,
		ReplyToBot:  input.ReplyToBot,
		IsCommand:   input.IsCommand,
		CommandName: input.CommandName,
		ActorOpenID: input.ActorOpenID,
	}, active)

	observation := ShadowObservation{
		PolicyDecision: decision,
		Scope:          scope,
	}
	if decision.EnterRuntime {
		observation.CandidateCapabilities = o.candidateCapabilities(scope)
	}
	return observation
}

func capabilityScopeFromChatType(chatType string) CapabilityScope {
	if strings.EqualFold(strings.TrimSpace(chatType), "p2p") {
		return CapabilityScopeP2P
	}
	return CapabilityScopeGroup
}

func (o *ShadowObserver) candidateCapabilities(scope CapabilityScope) []string {
	if o.registry == nil {
		return nil
	}
	if _, err := o.registry.Lookup("bb", scope); err == nil {
		return []string{"bb"}
	}
	return nil
}
