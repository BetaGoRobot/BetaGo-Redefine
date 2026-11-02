package memestore

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func TestWithdrawalOverridesWarmGenerationAndRetrievedUses(t *testing.T) {
	s := fixture(t).ForScope("wiki")
	ctx := context.Background()
	m, records := knowledgeGeneration(t, s)
	snapshot, err := memes.CompileKnowledgeSnapshot(KnowledgeBinding("wiki", m), records)
	if err != nil {
		t.Fatal(err)
	}
	selector, err := memes.NewKnowledgeSelector(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	warmed, err := selector.Select(memes.Request{RequestID: "r", Dialogue: []memes.Turn{{TurnID: "t", Speaker: "human", Text: "海岛奇兵"}}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.CheckKnowledge(ctx, warmed)
	if err != nil || len(before.Entries) != 1 {
		t.Fatalf("initial visibility %v", err)
	}
	if err = s.WithdrawSource(ctx, records[0].Full.Sources[0]); err != nil {
		t.Fatal(err)
	}
	if err = s.WithdrawSource(ctx, records[0].Full.Sources[0]); err != nil {
		t.Fatal("withdrawal not idempotent", err)
	}
	after, err := s.CheckKnowledge(ctx, warmed)
	if err != nil || len(after.Entries) != 0 || len(after.Anchors) != 1 || len(after.Anchors[0].EntryIDs) != 0 {
		t.Fatalf("cached withdrawal ignored %+v %v", after, err)
	}
	cards, err := s.FilterUseCards(ctx, []memes.UseCard{records[0].Full})
	if err != nil || len(cards) != 0 {
		t.Fatal("withdrawn use survived")
	}
	cards, err = s.store.ForScope("other").FilterUseCards(ctx, []memes.UseCard{records[0].Full})
	if err != nil || len(cards) != 1 {
		t.Fatal("withdrawal crossed catalog scope")
	}
}

func TestCurrentProjectionRemovalOverridesCachedKnowledge(t *testing.T) {
	s := fixture(t).ForScope("wiki")
	ctx := context.Background()
	m, records := knowledgeGeneration(t, s)
	snapshot, err := memes.CompileKnowledgeSnapshot(KnowledgeBinding("wiki", m), records)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := memes.SelectKnowledge(memes.Request{RequestID: "r", Dialogue: []memes.Turn{{TurnID: "t", Speaker: "human", Text: "海岛奇兵"}}}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.ExecContext(ctx, `DELETE FROM `+s.store.table(string(IndexCard))+` WHERE scope_key=$1`, s.scope); err != nil {
		t.Fatal(err)
	}
	visible, err := s.CheckKnowledge(ctx, selection)
	if err != nil || len(visible.Entries) != 0 {
		t.Fatal("deleted source record survived warmed cache")
	}
}
