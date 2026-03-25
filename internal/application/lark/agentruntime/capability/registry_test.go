package capability

import (
	"context"
	"testing"
	"time"
)

type testCapability struct {
	meta Meta
}

func (c testCapability) Meta() Meta {
	return c.meta
}

func (c testCapability) Execute(context.Context, Request) (Result, error) {
	return Result{OutputText: c.meta.Name}, nil
}

func TestCapabilityRegistryRejectsDuplicateRegistration(t *testing.T) {
	registry := NewRegistry()
	capability := testCapability{
		meta: Meta{
			Name:             "search_history",
			Kind:             KindTool,
			SideEffectLevel:  SideEffectLevelNone,
			AllowedScopes:    []Scope{ScopeGroup},
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
	registry := NewRegistry()
	capability := testCapability{
		meta: Meta{
			Name:            "send_message",
			Kind:            KindTool,
			SideEffectLevel: SideEffectLevelChatWrite,
			AllowedScopes:   []Scope{ScopeGroup},
			DefaultTimeout:  10 * time.Second,
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if _, err := registry.Lookup("send_message", ScopeSchedule); err == nil {
		t.Fatalf("expected scope gate to reject schedule scope")
	}
	got, err := registry.Lookup("send_message", ScopeGroup)
	if err != nil {
		t.Fatalf("expected group scope to pass, got %v", err)
	}
	if got.Meta().Name != "send_message" {
		t.Fatalf("expected send_message capability, got %q", got.Meta().Name)
	}
}

func TestCapabilityRegistryListReturnsSortedMetadata(t *testing.T) {
	registry := NewRegistry()
	for _, capability := range []testCapability{
		{meta: Meta{Name: "word_count", Kind: KindCommand}},
		{meta: Meta{Name: "chat", Kind: KindCommand}},
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
