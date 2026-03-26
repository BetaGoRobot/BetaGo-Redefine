package shadow

import (
	"context"
	"testing"
	"time"

	capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"
)

func TestObserverMentionSelectsBBCommandBridge(t *testing.T) {
	registry := capdef.NewRegistry()
	if err := registry.Register(capdef.NewCommandBridgeCapability(
		"bb",
		capdef.Meta{
			Name:            "bb",
			Kind:            capdef.KindCommand,
			SideEffectLevel: capdef.SideEffectLevelChatWrite,
			AllowedScopes:   []capdef.Scope{capdef.ScopeGroup, capdef.ScopeP2P},
			DefaultTimeout:  time.Minute,
		},
		func(context.Context, capdef.CommandInvocation, capdef.Request) (capdef.Result, error) {
			return capdef.Result{}, nil
		},
	)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	observer := NewObserver(NewDefaultGroupPolicy(DefaultGroupPolicyConfig{}), registry, nil)
	observation := observer.Observe(context.Background(), ObserveInput{
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
	if observation.Scope != capdef.ScopeGroup {
		t.Fatalf("Scope = %q, want %q", observation.Scope, capdef.ScopeGroup)
	}
	if len(observation.CandidateCapabilities) != 1 || observation.CandidateCapabilities[0] != "bb" {
		t.Fatalf("CandidateCapabilities = %#v, want [\"bb\"]", observation.CandidateCapabilities)
	}
}

func TestObserverIneligibleMessageRecordsReason(t *testing.T) {
	observer := NewObserver(NewDefaultGroupPolicy(DefaultGroupPolicyConfig{}), capdef.NewRegistry(), nil)
	observation := observer.Observe(context.Background(), ObserveInput{
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
