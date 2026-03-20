package agentruntime

import (
	"context"
	"testing"
	"time"
)

type testCapability struct {
	meta CapabilityMeta
}

func (c testCapability) Meta() CapabilityMeta {
	return c.meta
}

func (c testCapability) Execute(context.Context, CapabilityRequest) (CapabilityResult, error) {
	return CapabilityResult{OutputText: c.meta.Name}, nil
}

func TestCapabilityRegistryRejectsDuplicateRegistration(t *testing.T) {
	registry := NewCapabilityRegistry()
	capability := testCapability{
		meta: CapabilityMeta{
			Name:             "search_history",
			Kind:             CapabilityKindTool,
			SideEffectLevel:  SideEffectLevelNone,
			AllowedScopes:    []CapabilityScope{CapabilityScopeGroup},
			DefaultTimeout:   5 * time.Second,
			SupportsAsync:    false,
			RequiresApproval: false,
		},
	}

	if err := registry.Register(capability); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register(capability); err == nil {
		t.Fatalf("expected duplicate capability registration to fail")
	}
}

func TestCapabilityRegistryLookupHonorsAllowedScope(t *testing.T) {
	registry := NewCapabilityRegistry()
	capability := testCapability{
		meta: CapabilityMeta{
			Name:            "send_message",
			Kind:            CapabilityKindTool,
			SideEffectLevel: SideEffectLevelChatWrite,
			AllowedScopes:   []CapabilityScope{CapabilityScopeGroup},
			DefaultTimeout:  10 * time.Second,
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if _, err := registry.Lookup("send_message", CapabilityScopeSchedule); err == nil {
		t.Fatalf("expected scope gate to reject schedule scope")
	}
	got, err := registry.Lookup("send_message", CapabilityScopeGroup)
	if err != nil {
		t.Fatalf("expected group scope to pass, got %v", err)
	}
	if got.Meta().Name != "send_message" {
		t.Fatalf("expected send_message capability, got %q", got.Meta().Name)
	}
}

func TestCapabilityRegistryListReturnsSortedMetadata(t *testing.T) {
	registry := NewCapabilityRegistry()
	for _, capability := range []testCapability{
		{meta: CapabilityMeta{Name: "word_count", Kind: CapabilityKindCommand}},
		{meta: CapabilityMeta{Name: "chat", Kind: CapabilityKindCommand}},
	} {
		if err := registry.Register(capability); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
	}

	list := registry.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(list))
	}
	if list[0].Name != "chat" || list[1].Name != "word_count" {
		t.Fatalf("expected sorted capability names, got %#v", list)
	}
}
