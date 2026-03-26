package shadow

import (
	"context"
	"strings"
	"time"

	capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"
)

// Observer carries shadow observation state.
type Observer struct {
	policy         GroupPolicy
	registry       *capdef.Registry
	activeProvider ActiveRunProvider
}

// NewObserver implements shadow observation behavior.
func NewObserver(policy GroupPolicy, registry *capdef.Registry, activeProvider ActiveRunProvider) *Observer {
	if policy == nil {
		policy = NewDefaultGroupPolicy(DefaultGroupPolicyConfig{})
	}
	if registry == nil {
		registry = capdef.NewRegistry()
	}
	return &Observer{
		policy:         policy,
		registry:       registry,
		activeProvider: activeProvider,
	}
}

// Observe implements shadow observation behavior.
func (o *Observer) Observe(ctx context.Context, input ObserveInput) Observation {
	if input.Now.IsZero() {
		input.Now = time.Now()
	}

	var active *ActiveRunSnapshot
	if o.activeProvider != nil {
		active = o.activeProvider(ctx, strings.TrimSpace(input.ChatID), strings.TrimSpace(input.ActorOpenID))
	}

	scope := ScopeFromChatType(input.ChatType)
	decision := o.policy.Decide(MessageSignal{
		Now:         input.Now,
		ChatType:    input.ChatType,
		Mentioned:   input.Mentioned,
		ReplyToBot:  input.ReplyToBot,
		IsCommand:   input.IsCommand,
		CommandName: input.CommandName,
		ActorOpenID: input.ActorOpenID,
	}, active)

	observation := Observation{
		PolicyDecision: decision,
		Scope:          scope,
	}
	if decision.EnterRuntime {
		observation.CandidateCapabilities = o.candidateCapabilities(scope)
	}
	return observation
}

func (o *Observer) candidateCapabilities(scope capdef.Scope) []string {
	if o.registry == nil {
		return nil
	}
	if _, err := o.registry.Lookup("bb", scope); err == nil {
		return []string{"bb"}
	}
	return nil
}
