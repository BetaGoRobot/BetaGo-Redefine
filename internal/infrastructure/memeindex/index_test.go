package memeindex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

type embedFunc func(context.Context, []string) ([][]float32, error)

func (f embedFunc) Embed(ctx context.Context, input []string) ([][]float32, error) {
	return f(ctx, input)
}
func fixture(t *testing.T, id string) memes.IndexRecord {
	t.Helper()
	c := memes.UseCard{UseID: id, FamilyID: "two_sides", Name: "好坏消息", ReviewStatus: "pending", InputRelation: "同一变化带来收益和损失", Requirements: []memes.Requirement{{ID: "benefit", AnyOf: []string{"实际收益"}}}, ResponseAct: "指出好坏两面", Roles: map[string]memes.Role{"change": {Kind: "event", Meaning: "同一变化"}}, Stance: memes.Stance{Voice: "bot_comment", ReplyTarget: "conversation", EvaluationTarget: "change"}, Template: "还有这种操作？", Anchors: []string{"这种操作"}, Slots: map[string]memes.Slot{}, Sources: []memes.SourceMetadata{{Kind: "constructed", Scope: "结构示例"}}}
	fp, _ := memes.Fingerprint(c)
	r, err := memes.CompileCard(c, memes.Projection{CardFingerprint: fp, Relations: []memes.Relation{{Kind: "benefit_cost", Pattern: "同一变化使A收益并失去B"}}, Keys: memes.ProjectionKeys{Related: []string{"宽带"}}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func manifest() memestore.GenerationManifest {
	return memestore.GenerationManifest{Generation: "g1", Verified: true, CorpusHash: "c", ProjectionHash: "p", EmbeddingModel: "fixture", Dimension: 3, RecordCount: 1, Indexes: []string{"test_g1_relation", "test_g1_emotion_chain", "test_g1_keys", "test_g1_description"}}
}
func TestSearchKeepsSeparateBoundedLanes(t *testing.T) {
	r := fixture(t, "a")
	var requests int
	var embedded []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.URL.Path != "/_msearch" {
			t.Errorf("unexpected path %s", req.URL.Path)
		}
		raw, _ := io.ReadAll(req.Body)
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) != 4 {
			t.Errorf("want two lane subsearches: %s", raw)
		}
		for i := 1; i < len(lines); i += 2 {
			var body map[string]any
			json.Unmarshal([]byte(lines[i]), &body)
			if body["size"] != float64(5) {
				t.Error("unbounded lane")
			}
			query := body["query"].(map[string]any)
			if len(query) != 1 {
				t.Error("mixed query lane")
			}
		}
		source := map[string]any{"use_id": r.UseID, "text": r.Relation, "full": r.Full, "projection": r.Projection, "fingerprint": r.Fingerprint, "relation_kinds": []string{"benefit_cost"}}
		json.NewEncoder(w).Encode(map[string]any{"responses": []any{map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_id": documentID("a"), "_score": 2.0, "_source": source}}}}, map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_id": documentID("a"), "_score": 0.8, "_source": source}}}}}})
	}))
	defer server.Close()
	c, err := New(Options{Endpoint: server.URL, IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", Embedder: embedFunc(func(_ context.Context, x []string) ([][]float32, error) {
		embedded = append(embedded, x...)
		return [][]float32{{1, 0, 0}}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	retriever, err := c.ForGeneration(manifest())
	if err != nil {
		t.Fatal(err)
	}
	snap, err := retriever.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q1", Field: memes.FieldRelation, Text: r.Relation}}})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(embedded) != 1 || len(snap.Candidates) != 1 || len(snap.Queries) != 1 || len(snap.Queries[0].Channels) != 2 {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
	if len(snap.RawQueries) != 1 || snap.RawQueries[0].Status != "ok" {
		t.Fatalf("raw query missing typed status: %+v", snap.RawQueries)
	}
	if snap.Queries[0].Channels[0].Hits[0].Score != 2 || snap.Queries[0].Channels[1].Hits[0].Score != .8 {
		t.Fatal("raw scores not preserved")
	}
}
func TestSearchMissingEmbeddingStillReturnsKeywordAndFailure(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		raw, _ := io.ReadAll(r.Body)
		if len(strings.Split(strings.TrimSpace(string(raw)), "\n")) != 2 {
			t.Error("missing embed should send only one lane")
		}
		io.WriteString(w, `{"responses":[{"hits":{"hits":[]}}]}`)
	}))
	defer server.Close()
	c, err := New(Options{Endpoint: server.URL, IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	ret, err := c.ForGeneration(manifest())
	if err != nil {
		t.Fatal(err)
	}
	snap, err := ret.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q", Field: memes.FieldKeys, Text: "宽带"}}})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || snap.SuccessfulQueries != 1 || !snap.Degraded || snap.Queries[0].Channels[1].Status != "error" {
		t.Fatalf("missing embedding hidden %+v", snap)
	}
}
func TestSearchRejectsExcessBudgetBeforeIO(t *testing.T) {
	var calls int
	c, err := New(Options{Endpoint: "http://127.0.0.1:1", IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", Embedder: embedFunc(func(context.Context, []string) ([][]float32, error) { calls++; return nil, nil })})
	if err != nil {
		t.Fatal(err)
	}
	ret, _ := c.ForGeneration(manifest())
	_, err = ret.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "a", Field: memes.FieldKeys, Text: "a"}, {ID: "b", Field: memes.FieldKeys, Text: "b"}, {ID: "c", Field: memes.FieldKeys, Text: "c"}}})
	if err == nil || calls != 0 {
		t.Fatal("budget escaped validation")
	}
}
func TestPublishValidatesBeforeRemoteWrites(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); calls++; mu.Unlock(); w.WriteHeader(500) }))
	defer server.Close()
	c, _ := New(Options{Endpoint: server.URL, IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture"})
	record := fixture(t, "x")
	record.Full.Template = "changed"
	_, err := c.Publish(context.Background(), "g1", []memes.IndexRecord{record})
	if err == nil || calls != 0 {
		t.Fatal("stale projection caused remote writes")
	}
}

func TestSearchSevenLogicalQueriesEmbedOnceAndDoNotWidenEmptyResults(t *testing.T) {
	requests, embedCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "_description") {
			if len(strings.Split(strings.TrimSpace(string(raw)), "\n")) != 2 {
				t.Error("description query did not have exactly one lane")
			}
			io.WriteString(w, `{"responses":[{"hits":{"hits":[]}}]}`)
			return
		}
		if len(strings.Split(strings.TrimSpace(string(raw)), "\n")) != 4 {
			t.Error("logical query did not have exactly two lanes")
		}
		io.WriteString(w, `{"responses":[{"hits":{"hits":[]}},{"hits":{"hits":[]}}]}`)
	}))
	defer server.Close()
	e := embedFunc(func(_ context.Context, texts []string) ([][]float32, error) {
		embedCalls++
		if len(texts) != 7 {
			t.Fatalf("batch has %d queries", len(texts))
		}
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 2, 3}
		}
		return out, nil
	})
	c, _ := New(Options{Endpoint: server.URL, IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", Embedder: e})
	ret, _ := c.ForGeneration(manifest())
	plan := memes.QueryPlan{}
	for _, field := range fields {
		for _, s := range []string{"first", "second"}[:memes.FieldQueryBudget(field)] {
			plan.Queries = append(plan.Queries, memes.SearchQuery{ID: string(field) + s, Field: field, Text: s})
		}
	}
	snapshot, err := ret.Search(context.Background(), plan)
	if err != nil || requests != 7 || embedCalls != 1 || snapshot.SuccessfulQueries != 7 || len(snapshot.Candidates) != 0 || snapshot.Degraded {
		t.Fatalf("unexpected widening or empty handling: %+v %v, requests=%d embeds=%d", snapshot, err, requests, embedCalls)
	}
}
