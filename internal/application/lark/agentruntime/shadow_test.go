package agentruntime

import (
	"context"
	"testing"
	"time"
)

func TestShadowObserverMentionSelectsBBCommandBridge(t *testing.T) {
	registry := NewCapabilityRegistry()
	if err := registry.Register(NewCommandBridgeCapability(
		"bb",
		CapabilityMeta{
			Name:            "bb",
			Kind:            CapabilityKindCommand,
			SideEffectLevel: SideEffectLevelChatWrite,
			AllowedScopes:   []CapabilityScope{CapabilityScopeGroup, CapabilityScopeP2P},
			DefaultTimeout:  time.Minute,
		},
		func(context.Context, CommandInvocation, CapabilityRequest) (CapabilityResult, error) {
			return CapabilityResult{}, nil
		},
	)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	observer := NewShadowObserver(NewDefaultGroupPolicy(DefaultGroupPolicyConfig{}), registry, nil)
	observation := observer.Observe(context.Background(), ShadowObserveInput{
		Now:         time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC),
		ChatID:      "oc_chat",
		ChatType:    "group",
		Mentioned:   true,
		ActorOpenID: "ou_actor",
		InputText:   "@bot 帮我总结一下",
	})

	if !observation.EnterRuntime {
		t.Fatal("expected mention to enter runtime")
	}
	if observation.TriggerType != TriggerTypeMention {
		t.Fatalf("TriggerType = %q, want %q", observation.TriggerType, TriggerTypeMention)
	}
	if observation.Scope != CapabilityScopeGroup {
		t.Fatalf("Scope = %q, want %q", observation.Scope, CapabilityScopeGroup)
	}
	if len(observation.CandidateCapabilities) != 1 || observation.CandidateCapabilities[0] != "bb" {
		t.Fatalf("CandidateCapabilities = %#v, want [\"bb\"]", observation.CandidateCapabilities)
	}
}

func TestShadowObserverIneligibleMessageRecordsReason(t *testing.T) {
	observer := NewShadowObserver(NewDefaultGroupPolicy(DefaultGroupPolicyConfig{}), NewCapabilityRegistry(), nil)
	observation := observer.Observe(context.Background(), ShadowObserveInput{
		Now:         time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC),
		ChatID:      "oc_chat",
		ChatType:    "group",
		ActorOpenID: "ou_actor",
		InputText:   "普通群消息",
	})

	if observation.EnterRuntime {
		t.Fatal("expected ordinary group message to stay out of runtime")
	}
	if observation.Reason != "message_not_eligible" {
		t.Fatalf("Reason = %q, want %q", observation.Reason, "message_not_eligible")
	}
	if len(observation.CandidateCapabilities) != 0 {
		t.Fatalf("CandidateCapabilities = %#v, want empty", observation.CandidateCapabilities)
	}
}
