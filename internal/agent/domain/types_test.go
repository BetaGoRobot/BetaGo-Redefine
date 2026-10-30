package domain_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func scope() domain.Scope {
	return domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}
}

func TestScopeValidationAndUnambiguousStableKey(t *testing.T) {
	s := scope()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Key() != scope().Key() || len(s.Key()) != 64 {
		t.Fatal("key is not stable SHA256")
	}
	for _, bad := range []domain.Scope{{}, {TenantID: "tenant", AppID: "app", ChatID: "chat"}, {TenantID: " tenant", AppID: "app", ChatID: "chat", Visibility: "group"}, {TenantID: "t\x00", AppID: "app", ChatID: "chat", Visibility: "group"}} {
		if !errors.Is(bad.Validate(), domain.ErrInvalid) {
			t.Fatalf("accepted incomplete scope: %#v", bad)
		}
	}
	a, b := s, s
	a.TenantID = "a:b"
	a.AppID = "c"
	b.TenantID = "a"
	b.AppID = "b:c"
	if a.Key() == b.Key() {
		t.Fatal("delimiter collision")
	}
	for i := 0; i < 4; i++ {
		changed := s
		switch i {
		case 0:
			changed.TenantID += "x"
		case 1:
			changed.AppID += "x"
		case 2:
			changed.ChatID += "x"
		case 3:
			changed.Visibility += "x"
		}
		if changed.Key() == s.Key() {
			t.Fatal("scope component absent from key")
		}
	}
}

func TestEventValidationAndImmutableCopy(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)
	e := domain.Event{ID: "event", Type: domain.EventMessage, OccurredAt: now, Message: domain.Message{Source: domain.SourceRef{Scope: scope(), MessageID: "message", Version: 1, AuthorID: "author", OccurredAt: now}, EventID: "event", Text: "原话", UpdatedAt: now, Raw: json.RawMessage(`{ "audit": true }`), Media: []domain.Media{{Type: domain.MediaImage, ResourceID: "resource", MIME: "image/png", Sequence: 0}}}}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	c := e.Clone()
	e.Message.Raw[2] = 'x'
	e.Message.Media[0].ResourceID = "changed"
	if string(c.Message.Raw) != `{ "audit": true }` || c.Message.Media[0].ResourceID != "resource" {
		t.Fatal("mutable audit/media shared")
	}
	for _, mutate := range []func(*domain.Event){func(e *domain.Event) { e.ID = "" }, func(e *domain.Event) { e.Message.Source.Scope.ChatID = "" }, func(e *domain.Event) { e.Message.Source.Version = 0 }, func(e *domain.Event) { e.Message.EventID = "other" }, func(e *domain.Event) { e.Message.UpdatedAt = now.Add(-time.Second) }, func(e *domain.Event) { e.Type = "unknown" }, func(e *domain.Event) { e.Message.Raw = []byte(`invalid`) }} {
		bad := c.Clone()
		mutate(&bad)
		if !errors.Is(bad.Validate(), domain.ErrInvalid) {
			t.Fatalf("accepted invalid event: %#v", bad)
		}
	}
}

func TestQueryAndContextRequireCompleteSources(t *testing.T) {
	now := time.Now().UTC()
	q := domain.Query{Scope: scope(), Anchor: now, Text: "query", Mode: domain.QueryLiteral, Limit: 10}
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	q.Mode = "invalid"
	if !errors.Is(q.Validate(), domain.ErrInvalid) {
		t.Fatal("unknown query mode accepted")
	}
	c := domain.Context{ID: "context", Scope: scope(), Version: 1, Text: "discussion", Sources: []domain.SourceRef{{Scope: scope(), MessageID: "m", Version: 1, AuthorID: "a", OccurredAt: now}}, CreatedAt: now, UpdatedAt: now}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Sources[0].Scope.ChatID = "other"
	if !errors.Is(c.Validate(), domain.ErrInvalid) {
		t.Fatal("cross-scope context accepted")
	}
	c.Sources = nil
	if !errors.Is(c.Validate(), domain.ErrInvalid) {
		t.Fatal("unsourced context accepted")
	}
}

func TestDeleteMayPrecedeOriginalAndHaveUnresolvedAuthor(t *testing.T) {
	now := time.Now().UTC()
	e := domain.Event{ID: "delete", Type: domain.EventDelete, OccurredAt: now, Message: domain.Message{EventID: "delete", Source: domain.SourceRef{Scope: scope(), MessageID: "not_seen", Version: 2}, UpdatedAt: now, Deleted: true}}
	if err := e.Validate(); err != nil {
		t.Fatalf("delete cannot depend on previously received original: %v", err)
	}
	if e.Message.Source.Validate() == nil {
		t.Fatal("unresolved tombstone became evidence")
	}
}

func TestScopeRejectsInvalidUTF8ToPreventHashAliasing(t *testing.T) {
	s := scope()
	s.TenantID = string([]byte{0xff})
	if !errors.Is(s.Validate(), domain.ErrInvalid) {
		t.Fatal("invalid UTF8 would be replaced when encoded into the scope hash")
	}
}
