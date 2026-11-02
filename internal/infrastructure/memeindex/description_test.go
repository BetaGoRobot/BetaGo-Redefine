package memeindex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func descriptionFixture(t *testing.T, id, text string) memes.IndexRecord {
	t.Helper()
	r := fixture(t, id)
	r.Full.Description = text
	r.Projection.CardFingerprint, _ = memes.Fingerprint(r.Full)
	r, err := memes.CompileCard(r.Full, r.Projection)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDescriptionProjectionPreservesOriginalAndSeparateFields(t *testing.T) {
	r := descriptionFixture(t, "original", "  原始解释\n尾部没有被改写。  ")
	if got := projectionText(r, memes.FieldDescription); got != r.Full.Description {
		t.Fatalf("description lost original bytes: %q", got)
	}
	for _, field := range []memes.Field{memes.FieldRelation, memes.FieldEmotion, memes.FieldKeys} {
		if strings.Contains(projectionText(r, field), "原始解释") {
			t.Fatal("original description contaminated semantic projections")
		}
	}
}

func TestSearchDescriptionSendsOnlyVectorAndRecordsOnlyVectorChannel(t *testing.T) {
	record := descriptionFixture(t, "description_hit", "这段原文只在第四索引出现")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		raw, _ := io.ReadAll(r.Body)
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) != 2 || !strings.Contains(lines[0], "test_g1_description") || strings.Contains(string(raw), `"match"`) || !strings.Contains(string(raw), `"knn"`) {
			t.Errorf("description sent a non-vector lane: %s", raw)
		}
		doc := document{UseID: record.UseID, Text: record.Full.Description, Full: record.Full, Projection: record.Projection, Fingerprint: record.Fingerprint, RelationKinds: relationKinds(record)}
		json.NewEncoder(w).Encode(map[string]any{"responses": []any{map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_id": documentID(record.UseID), "_score": 1, "_source": doc}}}}}})
	}))
	defer server.Close()
	c, _ := New(Options{Endpoint: server.URL, IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", Embedder: fixedEmbedder()})
	m := manifest()
	ret, err := c.ForGeneration(m)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ret.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "description_1", Field: memes.FieldDescription, Text: "这段原话"}}})
	if err != nil || snapshot.Degraded || requests != 1 || len(snapshot.Candidates) != 1 {
		t.Fatalf("description search: %+v %v requests=%d", snapshot, err, requests)
	}
	for _, result := range []memes.QueryResult{snapshot.RawQueries[0], snapshot.Queries[0]} {
		if len(result.Channels) != 1 || result.Channels[0].Channel != "vector" || result.Channels[0].Status != "ok" {
			t.Fatalf("description fabricated a fulltext channel: %+v", result)
		}
	}
}

func TestDescriptionEmbeddingFailureDoesNotSendFulltextFallback(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
	defer server.Close()
	c, _ := New(Options{Endpoint: server.URL, IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture"})
	m := manifest()
	ret, err := c.ForGeneration(m)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ret.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "description_1", Field: memes.FieldDescription, Text: "原话"}}})
	if err != nil || requests != 0 || !snapshot.Degraded || snapshot.SuccessfulQueries != 0 || len(snapshot.RawQueries) != 1 {
		t.Fatalf("embedding failure leaked a fulltext request: %+v %v requests=%d", snapshot, err, requests)
	}
	channels := snapshot.RawQueries[0].Channels
	if len(channels) != 1 || channels[0].Channel != "vector" || channels[0].Status != "error" || channels[0].Error != ErrEmbedding.Error() {
		t.Fatalf("wrong failure evidence: %+v", channels)
	}
}

func TestDescriptionQueryBudgetAndFourIndexManifest(t *testing.T) {
	var plan memes.QueryPlan
	for _, f := range []memes.Field{memes.FieldRelation, memes.FieldEmotion, memes.FieldKeys} {
		for i := range 2 {
			plan.Queries = append(plan.Queries, memes.SearchQuery{ID: fmt.Sprintf("%s_%d", f, i), Field: f, Text: "text"})
		}
	}
	plan.Queries = append(plan.Queries, memes.SearchQuery{ID: "description_1", Field: memes.FieldDescription, Text: "原话"})
	if err := validatePlan(plan); err != nil {
		t.Fatal("seven bounded queries rejected", err)
	}
	plan.Queries = append(plan.Queries, memes.SearchQuery{ID: "description_2", Field: memes.FieldDescription, Text: "原话"})
	if err := validatePlan(plan); err == nil {
		t.Fatal("description budget exceeded")
	}
	c, _ := New(Options{Endpoint: "http://127.0.0.1:1", IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture"})
	old := manifest()
	old.Indexes = old.Indexes[:3]
	if _, err := c.ForGeneration(old); err == nil {
		t.Fatal("old three-index manifest accepted")
	}
	m := manifest()
	if _, err := c.ForGeneration(m); err != nil {
		t.Fatal("four-index manifest rejected", err)
	}
}

func TestShortDescriptionUsesOriginalProviderVectorOnce(t *testing.T) {
	text := " 原话\n完整保留 "
	calls := 0
	c, _ := New(Options{Endpoint: "http://127.0.0.1:1", IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", Embedder: embedFunc(func(_ context.Context, texts []string) ([][]float32, error) {
		calls++
		if !slices.Equal(texts, []string{text}) {
			t.Fatalf("short description rewritten or duplicated: %q", texts)
		}
		return [][]float32{{3, 4, 0}}, nil
	})})
	cache := map[[32]byte][]float32{}
	for range 2 {
		vectors, err := c.embedPublicationBatch(context.Background(), "g1", []publicationText{{text: text, description: true}, {text: text, description: true}}, cache)
		if err != nil || !slices.Equal(vectors[0], []float32{3, 4, 0}) || !slices.Equal(vectors[1], []float32{3, 4, 0}) {
			t.Fatalf("short vector changed: %v %v", vectors, err)
		}
	}
	if calls != 1 {
		t.Fatalf("publication-local description cache missed: calls=%d", calls)
	}
}

func TestLongDescriptionRejectsInvalidOrCancellingVectors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		vector []float32
	}{
		{"zero_mean", []float32{0, 0, 0}},
		{"nan", []float32{float32(math.NaN()), 0, 0}},
		{"infinite", []float32{float32(math.Inf(1)), 0, 0}},
		{"wrong_dimension", []float32{1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := New(Options{Endpoint: "http://127.0.0.1:1", IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", Embedder: embedFunc(func(_ context.Context, input []string) ([][]float32, error) {
				out := make([][]float32, len(input))
				for i := range out {
					out[i] = tc.vector
				}
				return out, nil
			})})
			text := strings.Repeat("x", (32<<10)+1)
			if vectors, err := c.embedLongDescription(context.Background(), "g1", text, sha256.Sum256([]byte(text))); !errors.Is(err, ErrEmbedding) || vectors != nil {
				t.Fatalf("invalid averaged vector accepted: %v %v", vectors, err)
			}
		})
	}
}

func TestOpenSearch215DescriptionOnlyOriginalVectorAndLongTail(t *testing.T) {
	endpoint := os.Getenv("MEME_TEST_OPENSEARCH_URL")
	if endpoint == "" {
		t.Skip("set MEME_TEST_OPENSEARCH_URL for isolated OpenSearch 2.15")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// The first chunk ends inside this run of multibyte runes. Tail must remain intact.
	text := strings.Repeat("原", 11000) + "只有后半段才有的尾部标识"
	records := []memes.IndexRecord{descriptionFixture(t, "original_a", text), descriptionFixture(t, "original_b", text)}
	var chunks []string
	querying := false
	embedder := embedFunc(func(_ context.Context, texts []string) ([][]float32, error) {
		vectors := make([][]float32, len(texts))
		for i, s := range texts {
			if !utf8.ValidString(s) || len(s) > 32<<10 {
				t.Fatalf("invalid/oversized embedding input: bytes=%d", len(s))
			}
			if querying {
				vectors[i] = []float32{float32(1 / math.Sqrt2), float32(1 / math.Sqrt2), 0}
				continue
			}
			vectors[i] = []float32{1, 0, 0}
			if strings.Contains(s, "原") || strings.Contains(s, "尾部标识") {
				chunks = append(chunks, s)
				if strings.Contains(s, "尾部标识") {
					vectors[i] = []float32{0, 1, 0}
				}
			}
		}
		return vectors, nil
	})
	var descriptionSearches int
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/_msearch" {
			raw, _ := io.ReadAll(r.Body)
			r.Body.Close()
			r.Body = io.NopCloser(strings.NewReader(string(raw)))
			if strings.Contains(string(raw), "_description") {
				descriptionSearches++
				if strings.Contains(string(raw), `"match"`) || len(strings.Split(strings.TrimSpace(string(raw)), "\n")) != 2 {
					t.Error("original description sent fulltext")
				}
			}
		}
		return transport.RoundTrip(r)
	})}
	l := &ledger{}
	c, err := New(Options{Endpoint: endpoint, HTTPClient: client, IndexPrefix: fmt.Sprintf("meme_description_%d", time.Now().UnixNano()), Dimensions: 3, EmbeddingModel: "description-fixture", Embedder: embedder, Ledger: l})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, index := range c.indexes("g1") {
			c.request(context.Background(), http.MethodDelete, "/"+index, nil, "")
		}
	})
	m, err := c.Publish(ctx, "g1", records)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Indexes) != 4 || !m.Verified {
		t.Fatalf("four-index manifest missing: %+v", m)
	}
	if len(chunks) != 2 || strings.Join(chunks, "") != text {
		t.Fatalf("original tail truncated or duplicate descriptions re-embedded: chunks=%d", len(chunks))
	}
	chunkReceipts := 0
	for id, raw := range l.values {
		var event publicationEvent
		if json.Unmarshal([]byte(raw), &event) == nil && event.Operation == "embed_description" && strings.HasSuffix(id, ":receipt") {
			chunkReceipts++
			if event.ChunkCount != 2 || event.EmbeddingPolicy != "utf8_32768_mean_l2_v1" || len(event.TextHash) != 64 {
				t.Fatalf("long description lost aggregation evidence: %+v", event)
			}
		}
	}
	if chunkReceipts != 1 {
		t.Fatalf("long description embedding receipts=%d", chunkReceipts)
	}
	descriptionIndex := m.Indexes[3]
	for _, r := range records {
		doc, found, err := c.getDocument(ctx, descriptionIndex, r.UseID)
		if err != nil || !found || doc.Text != text || !validVector(doc.Vector, 3) || math.Abs(float64(doc.Vector[0])-1/math.Sqrt2) > 1e-6 || math.Abs(float64(doc.Vector[1])-1/math.Sqrt2) > 1e-6 || doc.Vector[2] != 0 {
			t.Fatalf("tail did not contribute to normalized mean: %+v found=%t err=%v", doc.Vector, found, err)
		}
		for i, f := range []memes.Field{memes.FieldRelation, memes.FieldEmotion, memes.FieldKeys} {
			doc, found, err := c.getDocument(ctx, m.Indexes[i], r.UseID)
			if err != nil || found && (doc.Text != projectionText(r, f) || strings.Contains(doc.Text, "尾部标识")) {
				t.Fatalf("description contaminated %s", f)
			}
		}
	}
	status, raw, err := c.request(ctx, http.MethodGet, "/"+descriptionIndex+"/_mapping", nil, "")
	if status != 200 || err != nil || !strings.Contains(string(raw), "utf8_32768_mean_l2_v1") || !strings.Contains(string(raw), "meme_projection_v2") {
		t.Fatalf("description embedding strategy not recorded: status=%d err=%v", status, err)
	}
	querying = true
	ret, err := c.ForGeneration(m)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ret.Search(ctx, memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "description_1", Field: memes.FieldDescription, Text: "从原话找描述尾部"}}})
	if err != nil || snapshot.Degraded || len(snapshot.Candidates) != 2 || descriptionSearches != 1 || len(snapshot.RawQueries[0].Channels) != 1 {
		t.Fatalf("real description-only query failed: %+v %v", snapshot, err)
	}
	ids := []string{snapshot.Candidates[0].UseID, snapshot.Candidates[1].UseID}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"original_a", "original_b"}) {
		t.Fatalf("unexpected original hits: %v", ids)
	}
}
