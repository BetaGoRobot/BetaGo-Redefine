package agentruntime

import (
	"strings"
	"time"
)

type MessageSignal struct {
	Now         time.Time `json:"now"`
	ChatType    string    `json:"chat_type,omitempty"`
	Mentioned   bool      `json:"mentioned"`
	ReplyToBot  bool      `json:"reply_to_bot"`
	IsCommand   bool      `json:"is_command"`
	CommandName string    `json:"command_name,omitempty"`
	ActorOpenID string    `json:"actor_open_id,omitempty"`
}

type ActiveRunSnapshot struct {
	ID           string    `json:"id"`
	ActorOpenID  string    `json:"actor_open_id,omitempty"`
	Status       RunStatus `json:"status"`
	LastActiveAt time.Time `json:"last_active_at"`
}

type TriggerDecision struct {
	EnterRuntime bool        `json:"enter_runtime"`
	TriggerType  TriggerType `json:"trigger_type,omitempty"`
	Reason       string      `json:"reason,omitempty"`
}

type OwnershipDecision struct {
	AttachToRunID   string `json:"attach_to_run_id,omitempty"`
	SupersedeRunID  string `json:"supersede_run_id,omitempty"`
	OwnershipReason string `json:"ownership_reason,omitempty"`
}

type PolicyDecision struct {
	EnterRuntime   bool        `json:"enter_runtime"`
	TriggerType    TriggerType `json:"trigger_type,omitempty"`
	AttachToRunID  string      `json:"attach_to_run_id,omitempty"`
	SupersedeRunID string      `json:"supersede_run_id,omitempty"`
	Reason         string      `json:"reason,omitempty"`
}

type TriggerPolicy interface {
	EvaluateTrigger(signal MessageSignal, active *ActiveRunSnapshot) TriggerDecision
}

type OwnershipPolicy interface {
	EvaluateOwnership(signal MessageSignal, active *ActiveRunSnapshot, trigger TriggerDecision) OwnershipDecision
}

type GroupPolicy interface {
	Decide(signal MessageSignal, active *ActiveRunSnapshot) PolicyDecision
}

type DefaultGroupPolicyConfig struct {
	FollowUpWindow time.Duration `json:"follow_up_window"`
}

type DefaultGroupPolicy struct {
	cfg DefaultGroupPolicyConfig
}

func NewDefaultGroupPolicy(cfg DefaultGroupPolicyConfig) *DefaultGroupPolicy {
	if cfg.FollowUpWindow <= 0 {
		cfg.FollowUpWindow = 2 * time.Minute
	}
	return &DefaultGroupPolicy{cfg: cfg}
}

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
	case TriggerTypeMention, TriggerTypeCommandBridge:
		return OwnershipDecision{
			SupersedeRunID:  active.ID,
			OwnershipReason: "supersede_active_run",
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
	if active == nil || active.ID == "" {
		return false
	}
	return !active.Status.IsTerminal()
}
