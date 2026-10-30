package memeindex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

type ledger struct {
	mu                sync.Mutex
	values            map[string]string
	intents, receipts int
}

func (l *ledger) Put(_ context.Context, _ memestore.Kind, id string, v any) (memestore.Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.values == nil {
		l.values = map[string]string{}
	}
	raw, _ := json.Marshal(v)
	if old, ok := l.values[id]; ok && old != string(raw) {
		return memestore.Record{}, fmt.Errorf("conflicting ledger body")
	}
	l.values[id] = string(raw)
	if strings.HasSuffix(id, ":intent") {
		l.intents++
	}
	if strings.HasSuffix(id, ":receipt") {
		l.receipts++
	}
	return memestore.Record{ID: id}, nil
}
func fixedEmbedder() Embedder {
	return embedFunc(func(_ context.Context, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i, s := range texts {
			out[i] = []float32{float32(len([]rune(s))), 1, 2}
		}
		return out, nil
	})
}
func TestOpenSearch215PublishAndSearchImmutableGeneration(t *testing.T) {
	endpoint := os.Getenv("MEME_TEST_OPENSEARCH_URL")
	if endpoint == "" {
		t.Skip("set MEME_TEST_OPENSEARCH_URL for isolated OpenSearch 2.15")
	}
	prefix := fmt.Sprintf("meme_test_%d", time.Now().UnixNano())
	l := &ledger{}
	c, err := New(Options{Endpoint: endpoint, IndexPrefix: prefix, Dimensions: 3, EmbeddingModel: "deterministic-fixture-v1", Embedder: fixedEmbedder(), Ledger: l})
	if err != nil {
		t.Fatal(err)
	}
	records := []memes.IndexRecord{fixture(t, "a.case"), fixture(t, "b")}
	// Only this test's freshly generated namespace can be removed.
	t.Cleanup(func() {
		for _, index := range c.indexes("g1") {
			c.request(context.Background(), http.MethodDelete, "/"+index, nil, "")
		}
	})
	manifest, err := c.Publish(context.Background(), "g1", records)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Verified || manifest.RecordCount != 2 || len(manifest.Indexes) != 4 || manifest.CorpusHash == "" || manifest.ProjectionHash == "" {
		t.Fatalf("bad manifest %+v", manifest)
	}
	for i, index := range manifest.Indexes {
		status, raw, err := c.request(context.Background(), http.MethodGet, "/"+index+"/_mapping", nil, "")
		if err != nil || status != 200 {
			t.Fatalf("mapping: %d %s %v", status, raw, err)
		}
		var mapping map[string]struct {
			Mappings struct {
				Meta       map[string]any            `json:"_meta"`
				Properties map[string]map[string]any `json:"properties"`
			} `json:"mappings"`
		}
		if err := json.Unmarshal(raw, &mapping); err != nil {
			t.Fatal(err)
		}
		settingsStatus, settingsRaw, settingsErr := c.request(context.Background(), http.MethodGet, "/"+index+"/_settings", nil, "")
		if settingsErr != nil || settingsStatus != 200 {
			t.Fatal("settings unavailable")
		}
		var settings map[string]struct {
			Settings struct {
				Index struct {
					Blocks map[string]string `json:"blocks"`
				} `json:"index"`
			} `json:"settings"`
		}
		if json.Unmarshal(settingsRaw, &settings) != nil || settings[index].Settings.Index.Blocks["write"] != "true" {
			t.Fatalf("generation is still writable: %s", settingsRaw)
		}
		props := mapping[index].Mappings.Properties
		if props["full"]["enabled"] != false || props["projection"]["enabled"] != false || props["text"]["analyzer"] != "cjk" || props["vector"]["dimension"] != float64(3) {
			t.Fatalf("unsafe mapping: %s", raw)
		}
		if mapping[index].Mappings.Meta["projection_hash"] != manifest.ProjectionHash {
			t.Fatal("missing hash binding")
		}
		status, raw, err = c.request(context.Background(), http.MethodGet, "/"+index+"/_count", nil, "")
		if err != nil || status != 200 {
			t.Fatal("count failed")
		}
		var count struct {
			Count int `json:"count"`
		}
		json.Unmarshal(raw, &count)
		expected := 2
		if fields[i] == memes.FieldEmotion || fields[i] == memes.FieldDescription {
			expected = 0
		}
		if count.Count != expected {
			t.Fatalf("index %s has %d docs, want %d", index, count.Count, expected)
		}
	}
	ret, err := c.ForGeneration(manifest)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := ret.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q1", Field: memes.FieldRelation, Text: records[0].Relation, RelationKinds: []string{"benefit_cost"}}, {ID: "q2", Field: memes.FieldKeys, Text: "宽带"}}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Degraded || len(snap.Candidates) != 2 || snap.SuccessfulQueries != 2 {
		t.Fatalf("unexpected search %+v", snap)
	}
	for _, q := range snap.Queries {
		if len(q.Channels) != 2 || q.Channels[0].Status != "ok" || q.Channels[1].Status != "ok" {
			t.Fatal("real lane failed")
		}
	}
	before := len(l.values)
	again, err := c.Publish(context.Background(), "g1", records)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(manifest)
	b, _ := json.Marshal(again)
	if string(a) != string(b) || len(l.values) != before {
		t.Fatal("same publication was not idempotent")
	}
	other := fixture(t, "a.case")
	other.Full.Name = "different card"
	fp, _ := memes.Fingerprint(other.Full)
	other.Projection.CardFingerprint = fp
	other, err = memes.CompileCard(other.Full, other.Projection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Publish(context.Background(), "g1", []memes.IndexRecord{other, records[1]}); err == nil {
		t.Fatal("generation overwritten with conflicting body")
	}
	if l.intents == 0 || l.receipts == 0 {
		t.Fatal("publication missing durable intents/receipts")
	}
}

func TestOpenSearch215HighDimensionFaissVectors(t *testing.T) {
	endpoint := os.Getenv("MEME_TEST_OPENSEARCH_URL")
	if endpoint == "" {
		t.Skip("set MEME_TEST_OPENSEARCH_URL for isolated OpenSearch 2.15")
	}
	prefix := fmt.Sprintf("meme_test_faiss_%d", time.Now().UnixNano())
	vectors := embedFunc(func(_ context.Context, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range texts {
			out[i] = make([]float32, 2048)
			out[i][0] = 1
			out[i][2047] = float32(len(texts[i]))
		}
		return out, nil
	})
	c, err := New(Options{Endpoint: endpoint, IndexPrefix: prefix, Dimensions: 2048, EmbeddingModel: "fixture-2048", Embedder: vectors})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, index := range c.indexes("g1") {
			c.request(context.Background(), http.MethodDelete, "/"+index, nil, "")
		}
	})
	record := descriptionFixture(t, "high_dimension", "完整的原始说明，使用独立2048维向量。")
	manifest, err := c.Publish(context.Background(), "g1", []memes.IndexRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	status, raw, err := c.request(context.Background(), http.MethodGet, "/"+manifest.Indexes[0]+"/_mapping", nil, "")
	if status != 200 || err != nil {
		t.Fatal("mapping unavailable")
	}
	var mapping map[string]struct {
		Mappings struct {
			Meta       map[string]any `json:"_meta"`
			Properties map[string]struct {
				Method map[string]any `json:"method"`
			} `json:"properties"`
		} `json:"mappings"`
	}
	json.Unmarshal(raw, &mapping)
	if mapping[manifest.Indexes[0]].Mappings.Meta["vector_engine"] != "faiss" || mapping[manifest.Indexes[0]].Mappings.Properties["vector"].Method["engine"] != "faiss" {
		t.Fatalf("2048-dimensional generation did not freeze faiss engine: %s", raw)
	}
	ret, _ := c.ForGeneration(manifest)
	snapshot, err := ret.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q", Field: memes.FieldRelation, Text: record.Relation}, {ID: "description_1", Field: memes.FieldDescription, Text: record.Full.Description}}})
	if err != nil || snapshot.Degraded || len(snapshot.Candidates) != 1 {
		t.Fatalf("2048 vector search failed: %+v %v", snapshot, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOpenSearch215UnknownCommittedWriteIsReadBeforeRetry(t *testing.T) {
	endpoint := os.Getenv("MEME_TEST_OPENSEARCH_URL")
	if endpoint == "" {
		t.Skip("set MEME_TEST_OPENSEARCH_URL for isolated OpenSearch 2.15")
	}
	prefix := fmt.Sprintf("meme_test_unknown_%d", time.Now().UnixNano())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	dropped := false
	creates := map[string]int{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		isCreate := r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/_create/")
		if isCreate {
			creates[r.URL.Path]++
		}
		response, err := transport.RoundTrip(r)
		if err == nil && isCreate && !dropped && response.StatusCode == 201 {
			dropped = true
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			return nil, fmt.Errorf("lost receipt with private-secret and private-dialogue")
		}
		return response, err
	})}
	c, err := New(Options{Endpoint: endpoint, HTTPClient: client, IndexPrefix: prefix, Dimensions: 3, EmbeddingModel: "fixture", Embedder: fixedEmbedder(), Ledger: &ledger{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, index := range c.indexes("g1") {
			c.request(context.Background(), http.MethodDelete, "/"+index, nil, "")
		}
	})
	result, err := c.Publish(context.Background(), "g1", []memes.IndexRecord{fixture(t, "unknown_receipt")})
	if err != nil || !result.Verified || !dropped {
		t.Fatalf("committed write recovery failed: %+v %v", result, err)
	}
	for path, attempts := range creates {
		if attempts != 1 {
			t.Fatalf("unknown outcome blindly retried %s %d times", path, attempts)
		}
	}
}
