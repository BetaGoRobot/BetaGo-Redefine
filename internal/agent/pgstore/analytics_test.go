package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type analyticsStore interface {
	MessageStatistics(context.Context, domain.AnalyticsQuery) (domain.MessageStatistics, error)
}

func TestStatisticsCountAuthorityScopeHalfOpenRangeAndDeletedMessages(t *testing.T) {
	f := setup(t)
	s, ok := any(f.Store).(analyticsStore)
	if !ok {
		t.Fatal("local statistics not migrated")
	}
	ctx := context.Background()
	for i, id := range []string{"first", "second", "upper-bound", "foreign", "deleted"} {
		e := event("statistics-"+id, 1)
		e.Message.Source.MessageID = id
		e.Message.Source.OccurredAt = baseTime.Add(time.Duration(i) * time.Second)
		e.Message.UpdatedAt = baseTime.Add(10 * time.Second)
		e.Message.Text = "hello hello 世界"
		if id == "foreign" {
			e.Message.Source.Scope.ChatID = "other"
		}
		if id == "deleted" {
			e.Message.Deleted = true
			e.Type = domain.EventDelete
		}
		accept(t, f, e)
	}
	q := domain.AnalyticsQuery{Scope: testScope(), Start: baseTime, End: baseTime.Add(2 * time.Second), Anchor: time.Now(), Limit: 10}
	out, err := s.MessageStatistics(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 || len(out.Authors) != 1 || out.Authors[0].Count != 2 || out.Coverage != "locally_captured_current_messages" || !out.WordsComplete {
		t.Fatalf("wrong count/coverage: %+v", out)
	}
	if len(out.Words) == 0 || out.Words[0].Value != "hello" || out.Words[0].Count != 4 {
		t.Fatalf("wrong tokenization: %+v", out.Words)
	}
	q.End = baseTime.Add(20 * time.Second)
	out, err = s.MessageStatistics(ctx, q)
	if err != nil || out.Total != 3 {
		t.Fatalf("foreign/deleted counted: %+v %v", out, err)
	}
	q.AuthorID = "absent"
	out, err = s.MessageStatistics(ctx, q)
	if err != nil || out.Total != 0 || len(out.Authors) != 0 {
		t.Fatalf("filter failed: %+v %v", out, err)
	}
}

func TestStatisticsRejectsUnboundedWindows(t *testing.T) {
	f := setup(t)
	s, ok := any(f.Store).(analyticsStore)
	if !ok {
		t.Fatal("local statistics not migrated")
	}
	q := domain.AnalyticsQuery{Scope: testScope(), Start: baseTime, End: baseTime.Add(32 * 24 * time.Hour), Anchor: time.Now(), Limit: 10}
	if _, err := s.MessageStatistics(context.Background(), q); err == nil {
		t.Fatal("accepted unbounded window")
	}
}

func TestStatisticsRespectsEffectiveHistoryBarrier(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for i, id := range []string{"hidden", "visible"} {
		e := event("statistics-"+id, 1)
		e.Message.Source.MessageID = id
		e.Message.Source.OccurredAt = baseTime.Add(time.Duration(i) * time.Hour)
		e.Message.UpdatedAt = e.Message.Source.OccurredAt
		e.Message.Text = "private text"
		accept(t, f, e)
	}
	since := baseTime.Add(30 * time.Minute)
	seedHistoryBarrier(t, f, since)
	out, err := f.MessageStatistics(ctx, domain.AnalyticsQuery{Scope: testScope(), Start: baseTime, End: baseTime.Add(2 * time.Hour), Anchor: time.Now(), Limit: 10})
	if err != nil || out.Total != 1 || !out.Start.Equal(since) || len(out.Sources) != 1 || out.Sources[0].MessageID != "visible" {
		t.Fatalf("hidden statistics escaped: %+v %v", out, err)
	}
}
