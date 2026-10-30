package memeruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

type catalogStub struct {
	generation string
	err        error
}

func (c *catalogStub) Active(context.Context) (memestore.GenerationManifest, error) {
	return memestore.GenerationManifest{Generation: c.generation, CorpusHash: "corpus", ProjectionHash: "projection", Verified: true}, c.err
}

type indexStub struct{ generations []string }

func (i *indexStub) ForGeneration(m memestore.GenerationManifest) (memes.Retriever, error) {
	i.generations = append(i.generations, m.Generation)
	return emptyRetriever{}, nil
}

type emptyRetriever struct{}

func (emptyRetriever) Search(_ context.Context, p memes.QueryPlan) (memes.RetrievalSnapshot, error) {
	results := []memes.QueryResult{}
	for _, q := range p.Queries {
		channels := []memes.ChannelResult{{Channel: "vector", Status: "ok", Hits: []memes.RawHit{}}}
		if q.Field != memes.FieldDescription {
			channels = append([]memes.ChannelResult{{Channel: "fulltext", Status: "ok", Hits: []memes.RawHit{}}}, channels...)
		}
		results = append(results, memes.QueryResult{Query: q, Status: "ok", Channels: channels})
	}
	return memes.FuseRetrieval(p, results)
}

type queryModel struct{ calls int }

func (m *queryModel) Complete(context.Context, memes.CompletionRequest) (memes.CompletionResponse, error) {
	m.calls++
	return memes.CompletionResponse{JSON: json.RawMessage(`{"relations":[{"kind":"contrast","pattern":"同一事物的预期和现实形成反差","evidence":[{"source_id":0,"state":"asserted"}]}],"emotion_chain":[],"keys":[]}`)}, nil
}

func TestRuntimeBindsHostScopeAndFreezesOneGenerationPerRequest(t *testing.T) {
	catalog, index, model := &catalogStub{generation: "first"}, &indexStub{}, &queryModel{}
	service, err := New(Options{Catalog: catalog, Index: index, Model: func(context.Context, string) (memes.Completion, error) { return model, nil }, BotID: "bot", AllowedChats: []string{"chat"}})
	if err != nil {
		t.Fatal(err)
	}
	dialogue := []memes.Turn{{TurnID: "m", Speaker: "human", Text: "说好休假，结果还在加班。"}}
	result := service.References(context.Background(), "chat", "m", dialogue)
	if result.Status != memes.ReferenceStatusEmpty || model.calls != 1 || len(index.generations) != 1 || index.generations[0] != "first" {
		t.Fatalf("lookup: %+v calls=%d", result, model.calls)
	}
	catalog.generation = "second"
	service.References(context.Background(), "chat", "m", dialogue)
	if len(index.generations) != 2 || index.generations[1] != "second" {
		t.Fatalf("new run reused stale generation: %+v", index.generations)
	}
	denied := service.References(context.Background(), "other-chat", "m", dialogue)
	if denied.Status != memes.ReferenceStatusError || model.calls != 2 {
		t.Fatal("unauthorized host scope reached model")
	}
}
func TestMissingCatalogIsHonestAndDoesNotInvokeModel(t *testing.T) {
	calls := 0
	service, err := New(Options{Catalog: &catalogStub{err: errors.New("missing")}, Index: &indexStub{}, Model: func(context.Context, string) (memes.Completion, error) { calls++; return nil, nil }, BotID: "bot"})
	if err != nil {
		t.Fatal(err)
	}
	result := service.References(context.Background(), "chat", "m", []memes.Turn{{TurnID: "m", Speaker: "human", Text: "原话"}})
	if result.Status != memes.ReferenceStatusError || calls != 0 {
		t.Fatalf("missing catalog fabricated reference: %+v calls=%d", result, calls)
	}
}
