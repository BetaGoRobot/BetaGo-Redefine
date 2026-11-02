package memeruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

type knowledgeCatalogStub struct {
	catalogStub
	reads   int
	missing bool
}

func (c *knowledgeCatalogStub) CheckKnowledge(_ context.Context, k memes.KnowledgeSelection) (memes.KnowledgeSelection, error) {
	return k, nil
}
func (c *knowledgeCatalogStub) FilterUseCards(_ context.Context, k []memes.UseCard) ([]memes.UseCard, error) {
	return k, nil
}

func (c *knowledgeCatalogStub) Knowledge(_ context.Context, m memestore.GenerationManifest) (memes.KnowledgeSnapshot, error) {
	c.reads++
	if c.missing {
		return memes.KnowledgeSnapshot{}, memestore.ErrNotFound
	}
	card := memes.UseCard{UseID: "sea", FamilyID: "sea", Name: "海岛奇兵", Description: "影射退守与反攻", ReviewStatus: "pending", InputRelation: "退守但仍喊反攻", Requirements: []memes.Requirement{}, ResponseAct: "讽刺", Sources: []memes.SourceMetadata{{Kind: "wiki", Scope: "wiki", Title: "海岛奇兵"}}}
	fp, _ := memes.Fingerprint(card)
	record, e := memes.CompileCard(card, memes.Projection{CardFingerprint: fp, Relations: []memes.Relation{{Kind: "contrast", Pattern: "退守与反攻的反差"}}})
	if e != nil {
		return memes.KnowledgeSnapshot{}, e
	}
	return memes.CompileKnowledgeSnapshot(memestore.KnowledgeBinding("wiki", m), []memes.IndexRecord{record})
}
func TestKnowledgeModeRetainsPriorsIfModelFactoryFailsAndCachesByGeneration(t *testing.T) {
	c := &knowledgeCatalogStub{catalogStub: catalogStub{generation: "g1"}}
	service, e := New(Options{Catalog: c, Knowledge: c, KnowledgeFirst: true, Index: &indexStub{}, BotID: "bot", Model: func(context.Context, string) (memes.Completion, error) {
		return nil, errors.New("temporarily unavailable")
	}})
	if e != nil {
		t.Fatal(e)
	}
	dialogue := []memes.Turn{{TurnID: "m", Speaker: "human", Text: "海岛奇兵"}}
	for i := 0; i < 2; i++ {
		r := service.References(context.Background(), "chat", "m", dialogue)
		if r.ContextV2 == nil || len(r.ContextV2.PriorKnowledge) != 1 {
			t.Fatalf("priors lost: %+v", r)
		}
	}
	if c.reads != 1 {
		t.Fatalf("immutable snapshot reloaded %d times", c.reads)
	}
	c.generation = "g2"
	r := service.References(context.Background(), "chat", "m", dialogue)
	if c.reads != 2 || r.ContextV2 == nil || r.ContextV2.Binding.Generation != "g2" {
		t.Fatal("stale generation reused")
	}
}

func TestKnowledgeModeFallsBackExplicitlyWhenSidecarMissing(t *testing.T) {
	c := &knowledgeCatalogStub{catalogStub: catalogStub{generation: "g1"}, missing: true}
	model := &queryModel{}
	service, e := New(Options{Catalog: c, Knowledge: c, KnowledgeFirst: true, Index: &indexStub{}, BotID: "bot", Model: func(context.Context, string) (memes.Completion, error) { return model, nil }})
	if e != nil {
		t.Fatal(e)
	}
	r := service.References(context.Background(), "chat", "m", []memes.Turn{{TurnID: "m", Speaker: "human", Text: "认真问游戏"}})
	if r.ContextV2 != nil || model.calls != 1 || !r.Degraded {
		t.Fatalf("fallback unavailable %+v", r)
	}
	found := false
	for _, w := range r.Warnings {
		found = found || w.Code == "KNOWLEDGE_UNAVAILABLE"
	}
	if !found {
		t.Fatal("silent fallback")
	}
}

func TestRestoredMemeEnvironmentHonorsCurrentAllowedChats(t *testing.T) {
	c := &knowledgeCatalogStub{catalogStub: catalogStub{generation: "g"}}
	service, err := New(Options{Catalog: c, Knowledge: c, KnowledgeFirst: true, Index: &indexStub{}, BotID: "bot", AllowedChats: []string{"allowed"}, Model: func(context.Context, string) (memes.Completion, error) {
		t.Fatal("restore must not call model/planner")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	view, cards, err := service.Revalidate(context.Background(), "removed-chat", &memes.GroundedContext{Version: 2}, []memes.UseCard{{UseID: "cached"}})
	if err == nil || view != nil || len(cards) != 0 {
		t.Fatal("saved meme context bypassed current chat restriction")
	}
}
