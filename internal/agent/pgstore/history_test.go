package pgstore_test

import (
	"context"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"testing"
	"time"
)

func TestHistoryScopeExactFiltersCurrentVersionAndKeyset(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	at := baseTime
	for _, x := range []struct {
		id, author string
		delta      time.Duration
	}{{"a", "alice", 0}, {"b", "alice", 1}, {"c", "bob", 2}, {"d", "alice", 2}, {"e", "alice", 3}} {
		e := event("history-"+x.id, 1)
		e.Message.Source.MessageID = x.id
		e.Message.Source.AuthorID = x.author
		e.Message.Source.OccurredAt = at.Add(x.delta)
		e.Message.UpdatedAt = e.Message.Source.OccurredAt
		e.OccurredAt = e.Message.UpdatedAt
		e.Message.ParentID = "actual-parent"
		accept(t, f, e)
	}
	edit := event("edit-b", 2)
	edit.Message.Source.MessageID = "b"
	edit.Message.Source.AuthorID = "alice"
	edit.Message.Source.OccurredAt = at.Add(time.Nanosecond)
	edit.Message.UpdatedAt = at.Add(time.Second)
	edit.OccurredAt = edit.Message.UpdatedAt
	edit.Message.Text = "edited current"
	accept(t, f, edit)
	q := domain.HistoryQuery{Scope: testScope(), Anchor: at.Add(2 * time.Second), After: at, Before: at.Add(3 * time.Nanosecond), AuthorID: "alice", Limit: 1}
	p, err := f.History(ctx, q)
	if err != nil || len(p.Messages) != 1 || p.Messages[0].Source.MessageID != "d" || !p.HasMore || p.Next == nil {
		t.Fatalf("precise first page: %+v %v", p, err)
	}
	q.Cursor = p.Next
	p, err = f.History(ctx, q)
	if err != nil || len(p.Messages) != 1 || p.Messages[0].Source.MessageID != "b" || p.Messages[0].Source.Version != 2 || p.Messages[0].Text != "edited current" || p.HasMore {
		t.Fatalf("precise next page: %+v %v", p, err)
	}
	q.Scope.ChatID = "foreign"
	q.Cursor = nil
	p, err = f.History(ctx, q)
	if err != nil || len(p.Messages) != 0 {
		t.Fatal("cross-group history")
	}
	q.Scope = testScope()
	q.Cursor = nil
	q.AuthorID = ""
	q.After = time.Time{}
	q.Before = time.Time{}
	q.Limit = 10
	del := event("delete-d", 2)
	del.Message.Source.MessageID = "d"
	del.Type = domain.EventDelete
	del.Message.Deleted = true
	accept(t, f, del)
	p, err = f.History(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Messages {
		if m.Deleted || m.Source.MessageID == "d" {
			t.Fatal("deleted evidence in history")
		}
	}
}
