package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func discussionDefinition(t *testing.T, f *autoEffectFixture, name string) atoms.Definition {
	t.Helper()
	r, err := atoms.New(atoms.Options{Sources: f.store, Tasks: f.store})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range r.Definitions() {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("missing %s", name)
	return atoms.Definition{}
}
func putDiscussion(t *testing.T, f *autoEffectFixture, id, text string, ref domain.SourceRef) {
	t.Helper()
	now := time.Now().UTC()
	if err := f.store.PutContext(context.Background(), domain.Context{ID: id, Scope: f.task.Scope, Version: 1, Text: text, Sources: []domain.SourceRef{ref}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}
func TestDiscussionMigrationAdvancesCursorAcrossUnverifiableProjection(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	ctx := context.Background()
	putDiscussion(t, f, "a", "old context", f.task.Root)
	m, err := f.store.Message(ctx, f.task.Scope, f.task.Root.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	m.Source.Version++
	m.EventID = "discussion-source-edit"
	m.UpdatedAt = time.Now().UTC()
	m.Text = "edited"
	if _, err = f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: m.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".contexts SET invalidated=false WHERE context_id='a'`); err != nil {
		t.Fatal(err)
	}
	putDiscussion(t, f, "b", "current context", m.Source)
	d := discussionDefinition(t, f, "discussion_list")
	raw, err := d.Run(ctx, atoms.Invocation{Task: domain.Task{Scope: f.task.Scope}}, `{"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Available bool `json:"available"`
		Data      struct {
			Next string `json:"next_after_id"`
			More bool   `json:"has_more"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(raw), &out) != nil || !out.Available || out.Data.Next != "a" || !out.Data.More {
		t.Fatalf("stale page cannot advance: %s", raw)
	}
}
func TestDiscussionMigrationReturnsUsableBoundedPageAndDetail(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	for i := 0; i < 10; i++ {
		putDiscussion(t, f, fmt.Sprintf("c%02d", i), strings.Repeat("中", 8192), f.task.Root)
	}
	for name, args := range map[string]string{"discussion_list": `{"limit":20}`, "discussion_get": `{"id":"c00"}`} {
		d := discussionDefinition(t, f, name)
		raw, err := d.Run(context.Background(), atoms.Invocation{Task: domain.Task{Scope: f.task.Scope}}, args)
		var out struct {
			Available bool `json:"available"`
		}
		if err != nil || len(raw) > 64<<10 || json.Unmarshal([]byte(raw), &out) != nil || !out.Available {
			t.Fatalf("%s unusable: bytes=%d %v", name, len(raw), err)
		}
	}
}
