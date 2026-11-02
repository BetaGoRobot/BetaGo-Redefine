package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func historical(id string, version int64) domain.Event {
	e := event(id, version)
	e.Message.Raw = json.RawMessage(`{"kind":"backfill","sender_type":"user","addressed":false}`)
	return e
}
func historicalAccept(t *testing.T, f fixture, e domain.Event) (pgstore.AcceptResult, error) {
	t.Helper()
	p, ok := any(f.Store).(interface {
		AcceptHistorical(context.Context, domain.Event) (pgstore.AcceptResult, error)
	})
	if !ok {
		t.Fatal("historical acceptance API is missing")
	}
	return p.AcceptHistorical(context.Background(), e)
}
func TestHistoricalAcceptOnlyIndexesAndPreservesOnlineFacts(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := historical("historical", 1)
	r, err := historicalAccept(t, f, e)
	if err != nil || !r.Current || len(r.JobIDs) != 1 {
		t.Fatalf("historical accept: %+v %v", r, err)
	}
	j, err := f.Job(ctx, r.JobIDs[0])
	if err != nil || j.Kind != domain.JobIndexMessage || j.Priority >= 1000 {
		t.Fatalf("historical jobs: %+v %v", j, err)
	}
	duplicate := e
	duplicate.ID = "other-representation"
	duplicate.Message.EventID = duplicate.ID
	duplicate.Message.Raw = json.RawMessage(`{"kind":"backfill","sender_type":"user","provider":{"different":"audit"}}`)
	r, err = historicalAccept(t, f, duplicate)
	if err != nil || !r.Duplicate || len(r.JobIDs) != 0 {
		t.Fatalf("semantic duplicate: %+v %v", r, err)
	}
	current, _ := f.Message(ctx, testScope(), "message")
	if current.EventID != e.ID {
		t.Fatal("duplicate overwrote known current")
	}
	for _, change := range []func(*domain.Event){func(e *domain.Event) { e.Message.Text = "changed" }, func(e *domain.Event) { e.Message.ParentID = "parent" }, func(e *domain.Event) { e.Message.Source.AuthorID = "other" }, func(e *domain.Event) { e.Message.UpdatedAt = e.Message.UpdatedAt.Add(1) }} {
		bad := duplicate.Clone()
		bad.ID += "-conflict"
		bad.Message.EventID = bad.ID
		change(&bad)
		if _, err = historicalAccept(t, f, bad); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("same-version conflict escaped: %v", err)
		}
	}
	bad := e.Clone()
	bad.Message.Raw = json.RawMessage(`{"kind":"backfill","sender_type":"app"}`)
	if _, err = historicalAccept(t, f, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bot accepted: %v", err)
	}
}
func TestHistoricalAcceptStaleDeletionAndInvalidation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	live := event("online", 5)
	accept(t, f, live)
	duplicate := historical("backfill-same-version", 5)
	r, err := historicalAccept(t, f, duplicate)
	if err != nil || !r.Duplicate || len(r.JobIDs) != 0 {
		t.Fatalf("online semantic duplicate: %+v %v", r, err)
	}
	c := domain.Context{ID: "derived", Scope: testScope(), Version: 1, Text: "derived", Sources: []domain.SourceRef{live.Message.Source}, CreatedAt: baseTime, UpdatedAt: live.Message.UpdatedAt}
	if err = f.PutContext(ctx, c); err != nil {
		t.Fatal(err)
	}
	newer := historical("backfill-newer", 6)
	newer.Message.Text = "new canonical"
	r, err = historicalAccept(t, f, newer)
	if err != nil || !r.Current || len(r.JobIDs) != 2 {
		t.Fatalf("new version: %+v %v", r, err)
	}
	got, _ := f.Context(ctx, testScope(), c.ID)
	if !got.Invalidated {
		t.Fatal("historical update did not invalidate context")
	}
	stale := historical("backfill-stale", 2)
	r, err = historicalAccept(t, f, stale)
	if err != nil || r.Current || len(r.JobIDs) != 0 {
		t.Fatalf("stale scheduled work: %+v %v", r, err)
	}
	deletion := event("deleted", 7)
	deletion.Type = domain.EventDelete
	deletion.Message.Deleted = true
	accept(t, f, deletion)
	later := historical("must-not-resurrect", 8)
	r, err = historicalAccept(t, f, later)
	if err != nil || r.Current || len(r.JobIDs) != 0 {
		t.Fatalf("resurrection: %+v %v", r, err)
	}
	gotMessage, _ := f.Message(ctx, testScope(), "message")
	if !gotMessage.Deleted {
		t.Fatal("deletion lost")
	}
}

func historicalOpaque(t *testing.T, id, kind, content, representation string) domain.Event {
	t.Helper()
	e := historical(id, 1)
	e.Message.Text = ""
	var provider any
	switch representation {
	case "archive":
		raw, _ := json.Marshal(map[string]any{"event": map[string]any{"message": map[string]any{"message_type": kind, "content": content}}})
		provider = map[string]any{"source": "opensearch_archive", "row": map[string]any{"raw_body": string(raw)}}
	case "inbound":
		provider = map[string]any{"message": map[string]any{"message_type": kind, "content": content}}
	case "refresh":
		provider = map[string]any{"msg_type": kind, "body": map[string]any{"content": content}}
	default:
		t.Fatal("invalid opaque fixture representation")
	}
	raw, err := json.Marshal(map[string]any{"kind": "backfill", "sender_type": "user", "message_type": kind, "provider": provider})
	if err != nil {
		t.Fatal(err)
	}
	e.Message.Raw = raw
	return e
}
func TestHistoricalOpaqueFactsIgnoreEnvelopesButRetainOriginalContent(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	original := historicalOpaque(t, "opaque-archive", "merge_forward", `{"n":9007199254740993,"items":["雪🙂"]}`, "archive")
	if _, err := historicalAccept(t, f, original); err != nil {
		t.Fatal(err)
	}
	for _, representation := range []string{"inbound", "refresh"} {
		same := historicalOpaque(t, "opaque-"+representation, "merge_forward", `{ "items": ["雪🙂"], "n": 9007199254740993 }`, representation)
		result, err := historicalAccept(t, f, same)
		if err != nil || !result.Duplicate || len(result.JobIDs) != 0 {
			t.Fatalf("equivalent opaque audit envelope conflicted: %+v %v", result, err)
		}
	}
	for _, different := range []domain.Event{
		historicalOpaque(t, "changed-number", "merge_forward", `{"items":["雪🙂"],"n":9007199254740992}`, "refresh"),
		historicalOpaque(t, "changed-type", "interactive", `{"n":9007199254740993,"items":["雪🙂"]}`, "inbound"),
	} {
		if _, err := historicalAccept(t, f, different); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("different opaque original was not a version conflict: %v", err)
		}
	}
	got, err := f.Message(ctx, testScope(), original.Message.Source.MessageID)
	if err != nil || string(got.Raw) != string(original.Message.Raw) {
		t.Fatal("opaque duplicate or conflict replaced known original")
	}
}
func TestHistoricalOpaqueLiteralContentIsImmutable(t *testing.T) {
	f := setup(t)
	first := historicalOpaque(t, "literal-first", "merge_forward", "plain opaque🙂", "archive")
	if _, err := historicalAccept(t, f, first); err != nil {
		t.Fatal(err)
	}
	same := historicalOpaque(t, "literal-equivalent", "merge_forward", "plain opaque🙂", "refresh")
	result, err := historicalAccept(t, f, same)
	if err != nil || !result.Duplicate || len(result.JobIDs) != 0 {
		t.Fatalf("same opaque literal conflicted: %+v %v", result, err)
	}
	changed := historicalOpaque(t, "literal-changed", "merge_forward", "different opaque🙂", "inbound")
	if _, err = historicalAccept(t, f, changed); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("different opaque literal accepted: %v", err)
	}
}
