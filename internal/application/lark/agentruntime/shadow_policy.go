package agentruntime

import (
	"context"
	"time"

	shadowdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/shadow"
)

// MessageSignal captures the user-visible properties of one incoming message for
// shadow observation at the root package boundary.
type MessageSignal struct {
	Now         time.Time `json:"now"`
	ChatType    string    `json:"chat_type,omitempty"`
	Mentioned   bool      `json:"mentioned"`
	ReplyToBot  bool      `json:"reply_to_bot"`
	IsCommand   bool      `json:"is_command"`
	CommandName string    `json:"command_name,omitempty"`
	ActorOpenID string    `json:"actor_open_id,omitempty"`
}

// ActiveRunSnapshot summarizes the currently active run for one actor/chat pair
// so shadow policy can decide whether a new message should attach or follow up.
type ActiveRunSnapshot struct {
	ID           string    `json:"id"`
	ActorOpenID  string    `json:"actor_open_id,omitempty"`
	Status       RunStatus `json:"status"`
	LastActiveAt time.Time `json:"last_active_at"`
}

// TriggerDecision records whether the observed message should enter the runtime
// and, if so, which trigger classification applies.
type TriggerDecision struct {
	EnterRuntime bool        `json:"enter_runtime"`
	TriggerType  TriggerType `json:"trigger_type,omitempty"`
	Reason       string      `json:"reason,omitempty"`
}

// OwnershipDecision records whether a new trigger should attach to or supersede
// an existing active run.
type OwnershipDecision struct {
	AttachToRunID   string `json:"attach_to_run_id,omitempty"`
	SupersedeRunID  string `json:"supersede_run_id,omitempty"`
	OwnershipReason string `json:"ownership_reason,omitempty"`
}

// PolicyDecision is the combined output of trigger evaluation and ownership evaluation.
type PolicyDecision struct {
	EnterRuntime   bool        `json:"enter_runtime"`
	TriggerType    TriggerType `json:"trigger_type,omitempty"`
	AttachToRunID  string      `json:"attach_to_run_id,omitempty"`
	SupersedeRunID string      `json:"supersede_run_id,omitempty"`
	Reason         string      `json:"reason,omitempty"`
}

// ShadowObserveInput aliases the low-level shadow observe input on the root package surface.
type ShadowObserveInput = shadowdef.ObserveInput

// ActiveRunProvider loads the active-run snapshot used during shadow observation.
type ActiveRunProvider func(context.Context, string, string) *ActiveRunSnapshot

// TriggerPolicy decides whether an observed message is eligible to enter the runtime.
type TriggerPolicy interface {
	EvaluateTrigger(signal MessageSignal, active *ActiveRunSnapshot) TriggerDecision
}

// OwnershipPolicy decides how a newly eligible message relates to an existing active run.
type OwnershipPolicy interface {
	EvaluateOwnership(signal MessageSignal, active *ActiveRunSnapshot, trigger TriggerDecision) OwnershipDecision
}

// GroupPolicy combines trigger and ownership decisions into one policy result.
type GroupPolicy interface {
	Decide(signal MessageSignal, active *ActiveRunSnapshot) PolicyDecision
}

// DefaultGroupPolicyConfig configures the default follow-up heuristics used by
// the root-package shadow observer.
type DefaultGroupPolicyConfig struct {
	FollowUpWindow time.Duration `json:"follow_up_window"`
}

// DefaultGroupPolicy adapts the shadow subpackage default policy onto the root-package types.
type DefaultGroupPolicy struct {
	inner *shadowdef.DefaultGroupPolicy
}

// ShadowObservation is the root-package view of a shadow observation result.
type ShadowObservation struct {
	PolicyDecision
	Scope                 CapabilityScope `json:"scope,omitempty"`
	CandidateCapabilities []string        `json:"candidate_capabilities,omitempty"`
}

// ShadowObserver adapts the shadow subpackage observer onto the root-package API.
type ShadowObserver struct {
	inner *shadowdef.Observer
}

// NewDefaultGroupPolicy constructs the default shadow policy used to decide
// whether messages should enter the runtime.
func NewDefaultGroupPolicy(cfg DefaultGroupPolicyConfig) *DefaultGroupPolicy {
	return &DefaultGroupPolicy{
		inner: shadowdef.NewDefaultGroupPolicy(shadowdef.DefaultGroupPolicyConfig{
			FollowUpWindow: cfg.FollowUpWindow,
		}),
	}
}

// Decide evaluates the full policy result for one observed message.
func (p *DefaultGroupPolicy) Decide(signal MessageSignal, active *ActiveRunSnapshot) PolicyDecision {
	decision := p.inner.Decide(toShadowMessageSignal(signal), toShadowActiveRun(active))
	return fromShadowPolicyDecision(decision)
}

// EvaluateTrigger evaluates only the trigger portion of the shadow policy.
func (p *DefaultGroupPolicy) EvaluateTrigger(signal MessageSignal, active *ActiveRunSnapshot) TriggerDecision {
	decision := p.inner.EvaluateTrigger(toShadowMessageSignal(signal), toShadowActiveRun(active))
	return fromShadowTriggerDecision(decision)
}

// EvaluateOwnership evaluates only the ownership portion of the shadow policy.
func (p *DefaultGroupPolicy) EvaluateOwnership(signal MessageSignal, active *ActiveRunSnapshot, trigger TriggerDecision) OwnershipDecision {
	decision := p.inner.EvaluateOwnership(toShadowMessageSignal(signal), toShadowActiveRun(active), shadowdef.TriggerDecision{
		EnterRuntime: trigger.EnterRuntime,
		TriggerType:  shadowdef.TriggerType(trigger.TriggerType),
		Reason:       trigger.Reason,
	})
	return OwnershipDecision{
		AttachToRunID:   decision.AttachToRunID,
		SupersedeRunID:  decision.SupersedeRunID,
		OwnershipReason: decision.OwnershipReason,
	}
}

// NewShadowObserver constructs the root-package shadow observer wrapper.
func NewShadowObserver(policy GroupPolicy, registry *CapabilityRegistry, activeProvider ActiveRunProvider) *ShadowObserver {
	var shadowPolicy shadowdef.GroupPolicy
	switch typed := policy.(type) {
	case nil:
		shadowPolicy = nil
	case *DefaultGroupPolicy:
		shadowPolicy = typed.inner
	default:
		shadowPolicy = shadowdef.NewDefaultGroupPolicy(shadowdef.DefaultGroupPolicyConfig{})
	}

	var shadowProvider shadowdef.ActiveRunProvider
	if activeProvider != nil {
		shadowProvider = func(ctx context.Context, chatID, actorOpenID string) *shadowdef.ActiveRunSnapshot {
			return toShadowActiveRun(activeProvider(ctx, chatID, actorOpenID))
		}
	}

	return &ShadowObserver{
		inner: shadowdef.NewObserver(shadowPolicy, registry, shadowProvider),
	}
}

// Observe runs shadow observation for one message and returns the root-package
// observation value used by routing code.
func (o *ShadowObserver) Observe(ctx context.Context, input ShadowObserveInput) ShadowObservation {
	if o == nil || o.inner == nil {
		return ShadowObservation{}
	}
	observation := o.inner.Observe(ctx, input)
	return ShadowObservation{
		PolicyDecision:        fromShadowPolicyDecision(observation.PolicyDecision),
		Scope:                 CapabilityScope(observation.Scope),
		CandidateCapabilities: append([]string(nil), observation.CandidateCapabilities...),
	}
}

func toShadowMessageSignal(signal MessageSignal) shadowdef.MessageSignal {
	return shadowdef.MessageSignal{
		Now:         signal.Now,
		ChatType:    signal.ChatType,
		Mentioned:   signal.Mentioned,
		ReplyToBot:  signal.ReplyToBot,
		IsCommand:   signal.IsCommand,
		CommandName: signal.CommandName,
		ActorOpenID: signal.ActorOpenID,
	}
}

func toShadowActiveRun(active *ActiveRunSnapshot) *shadowdef.ActiveRunSnapshot {
	if active == nil {
		return nil
	}
	return &shadowdef.ActiveRunSnapshot{
		ID:           active.ID,
		ActorOpenID:  active.ActorOpenID,
		Status:       string(active.Status),
		LastActiveAt: active.LastActiveAt,
	}
}

func fromShadowTriggerDecision(decision shadowdef.TriggerDecision) TriggerDecision {
	return TriggerDecision{
		EnterRuntime: decision.EnterRuntime,
		TriggerType:  TriggerType(decision.TriggerType),
		Reason:       decision.Reason,
	}
}

func fromShadowPolicyDecision(decision shadowdef.PolicyDecision) PolicyDecision {
	return PolicyDecision{
		EnterRuntime:   decision.EnterRuntime,
		TriggerType:    TriggerType(decision.TriggerType),
		AttachToRunID:  decision.AttachToRunID,
		SupersedeRunID: decision.SupersedeRunID,
		Reason:         decision.Reason,
	}
}
