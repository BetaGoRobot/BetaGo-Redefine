package shadow

import (
	"strings"
	"time"
)

// DefaultGroupPolicyConfig carries shadow observation state.
type DefaultGroupPolicyConfig struct {
	FollowUpWindow time.Duration `json:"follow_up_window"`
}

// DefaultGroupPolicy carries shadow observation state.
type DefaultGroupPolicy struct {
	cfg DefaultGroupPolicyConfig
}

// NewDefaultGroupPolicy implements shadow observation behavior.
func NewDefaultGroupPolicy(cfg DefaultGroupPolicyConfig) *DefaultGroupPolicy {
	if cfg.FollowUpWindow <= 0 {
		cfg.FollowUpWindow = 2 * time.Minute
	}
	return &DefaultGroupPolicy{cfg: cfg}
}

// Decide implements shadow observation behavior.
func (p *DefaultGroupPolicy) Decide(signal MessageSignal, active *ActiveRunSnapshot) PolicyDecision {
	trigger := p.EvaluateTrigger(signal, active)
	if !trigger.EnterRuntime {
		return PolicyDecision{
			EnterRuntime: false,
			Reason:       trigger.Reason,
		}
	}

	ownership := p.EvaluateOwnership(signal, active, trigger)
	reason := trigger.Reason
	if ownership.OwnershipReason != "" {
		reason = ownership.OwnershipReason
	}

	return PolicyDecision{
		EnterRuntime:   true,
		TriggerType:    trigger.TriggerType,
		AttachToRunID:  ownership.AttachToRunID,
		SupersedeRunID: ownership.SupersedeRunID,
		Reason:         reason,
	}
}

// EvaluateTrigger implements shadow observation behavior.
func (p *DefaultGroupPolicy) EvaluateTrigger(signal MessageSignal, active *ActiveRunSnapshot) TriggerDecision {
	if strings.EqualFold(strings.TrimSpace(signal.ChatType), "p2p") {
		return TriggerDecision{
			EnterRuntime: true,
			TriggerType:  TriggerTypeP2P,
			Reason:       "p2p_message",
		}
	}

	if signal.IsCommand && strings.EqualFold(strings.TrimSpace(signal.CommandName), "bb") {
		return TriggerDecision{
			EnterRuntime: true,
			TriggerType:  TriggerTypeCommandBridge,
			Reason:       "command_bridge",
		}
	}

	if signal.Mentioned {
		return TriggerDecision{
			EnterRuntime: true,
			TriggerType:  TriggerTypeMention,
			Reason:       "explicit_mention",
		}
	}

	if signal.ReplyToBot {
		return TriggerDecision{
			EnterRuntime: true,
			TriggerType:  TriggerTypeReplyToBot,
			Reason:       "reply_to_bot",
		}
	}

	if p.canFollowUp(signal, active) {
		return TriggerDecision{
			EnterRuntime: true,
			TriggerType:  TriggerTypeFollowUp,
			Reason:       "follow_up_window",
		}
	}

	return TriggerDecision{
		EnterRuntime: false,
		Reason:       "message_not_eligible",
	}
}

// EvaluateOwnership implements shadow observation behavior.
func (p *DefaultGroupPolicy) EvaluateOwnership(signal MessageSignal, active *ActiveRunSnapshot, trigger TriggerDecision) OwnershipDecision {
	if !trigger.EnterRuntime || !isActiveRunSnapshot(active) {
		return OwnershipDecision{}
	}

	switch trigger.TriggerType {
	case TriggerTypeFollowUp:
		return OwnershipDecision{
			AttachToRunID:   active.ID,
			OwnershipReason: "attach_follow_up",
		}
	case TriggerTypeReplyToBot:
		if p.isWithinFollowUpWindow(signal.Now, active.LastActiveAt) {
			return OwnershipDecision{
				AttachToRunID:   active.ID,
				OwnershipReason: "attach_reply_to_bot",
			}
		}
	}

	return OwnershipDecision{}
}

func (p *DefaultGroupPolicy) canFollowUp(signal MessageSignal, active *ActiveRunSnapshot) bool {
	if !isActiveRunSnapshot(active) {
		return false
	}
	if strings.TrimSpace(signal.ActorOpenID) == "" || signal.ActorOpenID != active.ActorOpenID {
		return false
	}
	if signal.Mentioned || signal.ReplyToBot || signal.IsCommand {
		return false
	}
	return p.isWithinFollowUpWindow(signal.Now, active.LastActiveAt)
}

func (p *DefaultGroupPolicy) isWithinFollowUpWindow(now, lastActiveAt time.Time) bool {
	if now.IsZero() || lastActiveAt.IsZero() {
		return false
	}
	return !now.Before(lastActiveAt) && now.Sub(lastActiveAt) <= p.cfg.FollowUpWindow
}

func isActiveRunSnapshot(active *ActiveRunSnapshot) bool {
	if active == nil || strings.TrimSpace(active.ID) == "" {
		return false
	}
	switch strings.TrimSpace(active.Status) {
	case "completed", "failed", "cancelled":
		return false
	default:
		return true
	}
}
