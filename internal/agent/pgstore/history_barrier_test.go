package pgstore_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func seedHistoryBarrier(t *testing.T, f fixture, since time.Time) {
	t.Helper()
	body, _ := json.Marshal(domain.CatalogEntry{Kind: domain.CatalogGroupConfig, Name: "history_since", Revision: 1, CatalogValue: domain.CatalogValue{Config: &domain.CatalogConfigValue{Value: since.Format(time.RFC3339Nano)}}})
	if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".catalog_entries(scope_key,kind,name,revision,creator,body,updated_at)VALUES($1,'group_config','history_since',1,'admin',$2,clock_timestamp())`, testScope().Key(), body); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveHistoryBarrierFiltersOriginalDiscussionAndMemoryProofs(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	old := event("barrier-old", 1)
	old.Message.Source.MessageID = "old"
	accept(t, f, old)
	current := event("barrier-current", 1)
	current.Message.Source.MessageID = "current"
	current.Message.Source.OccurredAt = baseTime.Add(time.Hour)
	current.Message.UpdatedAt = current.Message.Source.OccurredAt
	accept(t, f, current)
	seedHistoryBarrier(t, f, baseTime.Add(30*time.Minute))
	recent, err := f.Recent(ctx, testScope(), time.Now(), 10)
	if err != nil || len(recent) != 1 || recent[0].Source.MessageID != "current" {
		t.Fatalf("old prompt history escaped: %+v %v", recent, err)
	}
	page, err := f.History(ctx, domain.HistoryQuery{Scope: testScope(), Anchor: time.Now(), Limit: 10})
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("old history escaped: %+v %v", page, err)
	}
	for _, mode := range []domain.QueryMode{domain.QueryLiteral, domain.QueryDiscussion, domain.QueryMemory} {
		valid, err := f.ValidSources(ctx, domain.Query{Scope: testScope(), Anchor: time.Now(), Mode: mode, Limit: 1}, []domain.SourceRef{old.Message.Source})
		if err != nil || valid {
			t.Fatalf("old %s source admitted: %v %v", mode, valid, err)
		}
	}
	if _, err = f.Message(ctx, testScope(), "old"); err != nil {
		t.Fatal("visibility barrier deleted authoritative source")
	}
}
