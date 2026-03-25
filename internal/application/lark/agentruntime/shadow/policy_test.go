package shadow

import (
	"testing"
	"time"
)

func TestDefaultGroupPolicyMentionStartsNewRunWithoutSupersedingActiveRun(t *testing.T) {
	policy := NewDefaultGroupPolicy(DefaultGroupPolicyConfig{
		FollowUpWindow: 2 * time.Minute,
	})
	now := time.Unix(1710000000, 0).UTC()
	active := &ActiveRunSnapshot{
		ID:           "run_active",
		ActorOpenID:  "ou_old",
		Status:       "running",
		LastActiveAt: now.Add(-30 * time.Second),
	}

	decision := policy.Decide(MessageSignal{
		Now:         now,
		ChatType:    "group",
		Mentioned:   true,
		ActorOpenID: "ou_new",
	}, active)

	if !decision.EnterRuntime {
		t.Fatalf("expected mention to enter runtime, got %#v", decision)
	}
	if decision.TriggerType != TriggerTypeMention {
		t.Fatalf("expected mention trigger, got %q", decision.TriggerType)
	}
	if decision.SupersedeRunID != "" {
		t.Fatalf("expected mention not to supersede active run, got %#v", decision)
	}
}

func TestDefaultGroupPolicyAllowsFollowUpWithinWindow(t *testing.T) {
	policy := NewDefaultGroupPolicy(DefaultGroupPolicyConfig{
		FollowUpWindow: 2 * time.Minute,
	})
	now := time.Unix(1710000000, 0).UTC()
	active := &ActiveRunSnapshot{
		ID:           "run_active",
		ActorOpenID:  "ou_actor",
		Status:       "waiting_callback",
		LastActiveAt: now.Add(-45 * time.Second),
	}

	decision := policy.Decide(MessageSignal{
		Now:         now,
		ChatType:    "group",
		ActorOpenID: "ou_actor",
	}, active)

	if !decision.EnterRuntime || decision.AttachToRunID != "run_active" {
		t.Fatalf("expected follow-up to attach to active run, got %#v", decision)
	}
	if decision.TriggerType != TriggerTypeFollowUp {
		t.Fatalf("expected follow_up trigger, got %q", decision.TriggerType)
	}
}

func TestDefaultGroupPolicyRejectsFollowUpOutsideWindow(t *testing.T) {
	policy := NewDefaultGroupPolicy(DefaultGroupPolicyConfig{
		FollowUpWindow: 2 * time.Minute,
	})
	now := time.Unix(1710000000, 0).UTC()
	active := &ActiveRunSnapshot{
		ID:           "run_active",
		ActorOpenID:  "ou_actor",
		Status:       "running",
		LastActiveAt: now.Add(-10 * time.Minute),
	}

	decision := policy.Decide(MessageSignal{
		Now:         now,
		ChatType:    "group",
		ActorOpenID: "ou_actor",
	}, active)

	if decision.EnterRuntime {
		t.Fatalf("expected stale follow-up to be rejected, got %#v", decision)
	}
}

func TestDefaultGroupPolicyReplyToBotStartsRunWithoutActiveRun(t *testing.T) {
	policy := NewDefaultGroupPolicy(DefaultGroupPolicyConfig{
		FollowUpWindow: 2 * time.Minute,
	})
	now := time.Unix(1710000000, 0).UTC()

	decision := policy.Decide(MessageSignal{
		Now:         now,
		ChatType:    "group",
		ReplyToBot:  true,
		ActorOpenID: "ou_actor",
	}, nil)

	if !decision.EnterRuntime {
		t.Fatalf("expected reply-to-bot to enter runtime, got %#v", decision)
	}
	if decision.TriggerType != TriggerTypeReplyToBot {
		t.Fatalf("expected reply_to_bot trigger, got %q", decision.TriggerType)
	}
}

func TestDefaultGroupPolicyCommandBridgeStartsNewRunWithoutSupersedingActiveRun(t *testing.T) {
	policy := NewDefaultGroupPolicy(DefaultGroupPolicyConfig{
		FollowUpWindow: 2 * time.Minute,
	})
	now := time.Unix(1710000000, 0).UTC()
	active := &ActiveRunSnapshot{
		ID:           "run_active",
		ActorOpenID:  "ou_other",
		Status:       "running",
		LastActiveAt: now.Add(-30 * time.Second),
	}

	decision := policy.Decide(MessageSignal{
		Now:         now,
		ChatType:    "group",
		IsCommand:   true,
		CommandName: "bb",
		ActorOpenID: "ou_actor",
	}, active)

	if !decision.EnterRuntime {
		t.Fatalf("expected /bb to enter runtime, got %#v", decision)
	}
	if decision.TriggerType != TriggerTypeCommandBridge {
		t.Fatalf("expected command_bridge trigger, got %q", decision.TriggerType)
	}
	if decision.SupersedeRunID != "" {
		t.Fatalf("expected /bb not to supersede active run, got %#v", decision)
	}
}
