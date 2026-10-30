package mememilvus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

type memoryRecords struct {
	mu     sync.Mutex
	lock   sync.Mutex
	values map[string]json.RawMessage
}

func (s *memoryRecords) Put(_ context.Context, k memestore.Kind, id string, v any) (memestore.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]json.RawMessage{}
	}
	b, _ := json.Marshal(v)
	key := string(k) + ":" + id
	if old, ok := s.values[key]; ok && !slices.Equal(old, b) {
		return memestore.Record{}, memestore.ErrConflict
	}
	s.values[key] = b
	return memestore.Record{ID: id, Body: slices.Clone(b)}, nil
}
func (s *memoryRecords) Get(_ context.Context, k memestore.Kind, id string) (memestore.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.values[string(k)+":"+id]
	if !ok {
		return memestore.Record{}, memestore.ErrNotFound
	}
	return memestore.Record{ID: id, Body: slices.Clone(b)}, nil
}
func (s *memoryRecords) WithPublicationLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	return fn(ctx)
}

type embedFunc func(context.Context, []string) ([][]float32, error)

func TestMilvus26RESTArrayReadback(t *testing.T) {
	for _, raw := range []string{`{"relation_kinds":["benefit_cost"]}`, `{"relation_kinds":{"Data":{"StringData":{"data":["benefit_cost"]}}}}`} {
		var d document
		if e := json.Unmarshal([]byte(raw), &d); e != nil || !slices.Equal([]string(d.RelationKinds), []string{"benefit_cost"}) {
			t.Fatalf("readback array: %v", e)
		}
	}
}

func (f embedFunc) Embed(c context.Context, s []string) ([][]float32, error) { return f(c, s) }
func fixture(t *testing.T, id, description string) memes.IndexRecord {
	t.Helper()
	card := memes.UseCard{UseID: id, FamilyID: "good_bad", Name: "好坏消息", Description: description, ReviewStatus: "pending", InputRelation: "同一变化带来收益和损失", Requirements: []memes.Requirement{{ID: "benefit", AnyOf: []string{"实际收益"}}}, ResponseAct: "指出好坏两面", Sources: []memes.SourceMetadata{{Kind: "constructed", Scope: "测试"}}}
	fp, _ := memes.Fingerprint(card)
	r, e := memes.CompileCard(card, memes.Projection{CardFingerprint: fp, Relations: []memes.Relation{{Kind: "benefit_cost", Pattern: "蒋介石在Steam获得收益并付出代价"}}, EmotionChain: []memes.EmotionChain{{Subject: "玩家", States: []string{"期待", "失望"}}}, Keys: memes.ProjectionKeys{Related: []string{"Steam", "蒋介石"}}})
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestOptionsAndCollectionNames(t *testing.T) {
	o := Options{Endpoint: "http://localhost:19530", IndexPrefix: "meme-test", Dimensions: 3, EmbeddingModel: "fixture"}
	c, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range c.indexes("g-1") {
		if strings.Contains(n, "-") || len(n) > 255 {
			t.Fatal(n)
		}
	}
	other, _ := New(Options{Endpoint: o.Endpoint, IndexPrefix: "meme_test", Dimensions: 3, EmbeddingModel: "fixture"})
	if slices.Equal(c.indexes("g-1"), other.indexes("g_1")) {
		t.Fatal("normalization collision")
	}
	_, e = c.Publish(context.Background(), "g1", []memes.IndexRecord{fixture(t, "one", "原文")})
	if !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

type fakeMilvus struct {
	mu                 sync.Mutex
	schemas            map[string]map[string]any
	docs               map[string]map[string]document
	inserts, mutations int
	failAfterInsert    bool
	dropInsert         bool
	badHit             bool
	lanes              []string
}

func (f *fakeMilvus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b map[string]json.RawMessage
	json.NewDecoder(r.Body).Decode(&b)
	var name string
	json.Unmarshal(b["collectionName"], &name)
	var data any = map[string]any{}
	path := strings.TrimPrefix(r.URL.Path, "/v2/vectordb/")
	switch path {
	case "collections/has":
		data = map[string]any{"has": f.schemas[name] != nil}
	case "collections/create":
		f.mutations++
		var schema map[string]any
		json.Unmarshal(b["schema"], &schema)
		var indexes []map[string]any
		json.Unmarshal(b["indexParams"], &indexes)
		f.schemas[name] = map[string]any{"schema": schema, "indexes": indexes}
		f.docs[name] = map[string]document{}
	case "collections/describe":
		s := f.schemas[name]
		schema, _ := s["schema"].(map[string]any)
		var fieldsOut []map[string]any
		for _, a := range schema["fields"].([]any) {
			field := a.(map[string]any)
			params := []any{}
			if p, ok := field["elementTypeParams"].(map[string]any); ok {
				for k, v := range p {
					str := fmt.Sprint(v)
					if _, ok := v.(map[string]any); ok {
						raw, _ := json.Marshal(v)
						str = string(raw)
					}
					params = append(params, map[string]any{"key": k, "value": str})
				}
			}
			fieldsOut = append(fieldsOut, map[string]any{"name": field["fieldName"], "type": field["dataType"], "primaryKey": field["isPrimary"], "params": params})
		}
		data = map[string]any{"collectionName": name, "fields": fieldsOut, "indexes": s["indexes"], "enableDynamicField": false, "autoId": false, "consistencyLevel": "Strong", "functions": schema["functions"]}
	case "collections/load", "collections/flush":
		f.mutations++
	case "entities/query":
		var outputs []string
		json.Unmarshal(b["outputFields"], &outputs)
		if slices.Contains(outputs, "count(*)") {
			data = []any{map[string]any{"count(*)": len(f.docs[name])}}
			break
		}
		var filter string
		json.Unmarshal(b["filter"], &filter)
		var ids []string
		idx := strings.Index(filter, "[")
		if idx >= 0 {
			json.Unmarshal([]byte(filter[idx:]), &ids)
		}
		rows := []document{}
		for _, id := range ids {
			if d, ok := f.docs[name][id]; ok {
				rows = append(rows, d)
			}
		}
		data = rows
	case "entities/insert":
		f.mutations++
		f.inserts++
		var docs []document
		json.Unmarshal(b["data"], &docs)
		if !f.dropInsert {
			for _, d := range docs {
				if _, ok := f.docs[name][d.UseID]; ok {
					t := map[string]any{"code": 99, "message": "duplicate insert"}
					json.NewEncoder(w).Encode(t)
					return
				}
				f.docs[name][d.UseID] = d
			}
		}
		if f.failAfterInsert || f.dropInsert {
			w.WriteHeader(503)
			return
		}
		data = map[string]any{"insertCount": len(docs)}
	case "entities/search":
		var field string
		json.Unmarshal(b["annsField"], &field)
		f.lanes = append(f.lanes, field)
		rows := []map[string]any{}
		if f.badHit {
			rows = append(rows, map[string]any{"use_id": "missing-record", "fingerprint": "not-present", "text": "bad fixture", "distance": 2, "relation_kinds": []string{}})
		}
		for _, d := range f.docs[name] {
			raw, _ := json.Marshal(d)
			var hit map[string]any
			json.Unmarshal(raw, &hit)
			hit["distance"] = float64(2)
			rows = append(rows, hit)
			if len(rows) == 5 {
				break
			}
		}
		data = rows
	default:
		w.WriteHeader(404)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
}
func newFake(t *testing.T) (*Client, *fakeMilvus, *memoryRecords, *int) {
	t.Helper()
	f := &fakeMilvus{schemas: map[string]map[string]any{}, docs: map[string]map[string]document{}}
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	store := &memoryRecords{}
	calls := new(int)
	c, e := New(Options{Endpoint: s.URL, IndexPrefix: "test", EmbeddingModel: "fixture", Dimensions: 3, Records: store, Embedder: embedFunc(func(_ context.Context, texts []string) ([][]float32, error) {
		*calls += len(texts)
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 2, 3}
		}
		return out, nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	return c, f, store, calls
}

func TestPublishResumeSealedAndSeparateSearchLanes(t *testing.T) {
	c, f, s, calls := newFake(t)
	records := []memes.IndexRecord{fixture(t, "a", "蒋介石使用Steam的原文解释"), fixture(t, "b", "蒋介石使用Steam的原文解释")}
	m, e := c.Publish(context.Background(), "g1", records)
	if e != nil {
		t.Fatal(e)
	}
	if !m.Verified || m.Backend != "milvus" || len(m.Indexes) != 4 {
		t.Fatal(m)
	}
	before, embedded := f.mutations, *calls
	if _, e = c.Publish(context.Background(), "g1", records); e != nil {
		t.Fatal(e)
	}
	if f.mutations != before || *calls != embedded {
		t.Fatal("sealed retry mutated or reembedded")
	}
	for _, r := range records {
		stored, e := s.Get(context.Background(), memestore.IndexCard, r.Fingerprint)
		if e != nil || !strings.Contains(string(stored.Body), "原文解释") {
			t.Fatal("full record absent", e)
		}
	}
	r, e := c.ForGeneration(m)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := r.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q1", Field: memes.FieldRelation, Text: "收益付出", RelationKinds: []string{"benefit_cost"}}, {ID: "q2", Field: memes.FieldDescription, Text: "Steam原话"}}})
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.Candidates) != 2 || snap.Degraded || len(snap.RawQueries) != 2 || len(snap.RawQueries[0].Channels) != 2 || len(snap.RawQueries[1].Channels) != 1 {
		t.Fatalf("bad snapshot %+v", snap)
	}
	if !slices.Equal(f.lanes, []string{"sparse", "vector", "vector"}) {
		t.Fatal(f.lanes)
	}
	if snap.RawQueries[0].Channels[0].Hits[0].Score != 2 || snap.RawQueries[0].Channels[1].Hits[0].Score != 1.0/3 {
		t.Fatal("score mapping")
	}
}

func TestUnknownInsertRecoveredWithoutDuplicate(t *testing.T) {
	c, f, _, calls := newFake(t)
	f.failAfterInsert = true
	records := []memes.IndexRecord{fixture(t, "a", "原文")}
	m, e := c.Publish(context.Background(), "g1", records)
	if e != nil || !m.Verified {
		t.Fatal(m, e)
	}
	inserts, embedded := f.inserts, *calls
	if _, e = c.Publish(context.Background(), "g1", records); e != nil || f.inserts != inserts || *calls != embedded {
		t.Fatal("recovery rewrote", e)
	}
}
func TestUnknownAbsentInsertNeverBlindlyRetried(t *testing.T) {
	c, f, _, _ := newFake(t)
	f.dropInsert = true
	records := []memes.IndexRecord{fixture(t, "a", "原文")}
	if _, e := c.Publish(context.Background(), "g1", records); !errors.Is(e, ErrUnknownOutcome) {
		t.Fatal(e)
	}
	inserts := f.inserts
	f.dropInsert = false
	if _, e := c.Publish(context.Background(), "g1", records); !errors.Is(e, ErrUnknownOutcome) || f.inserts != inserts {
		t.Fatal("blind retry", e)
	}
}

func TestPublicationConflictAndSealedMissingDocumentNeverWrite(t *testing.T) {
	c, f, _, _ := newFake(t)
	records := []memes.IndexRecord{fixture(t, "a", "原文")}
	m, e := c.Publish(context.Background(), "g1", records)
	if e != nil {
		t.Fatal(e)
	}
	before := f.mutations
	if _, e = c.Publish(context.Background(), "g1", []memes.IndexRecord{fixture(t, "a", "变动原文")}); !errors.Is(e, ErrConflict) {
		t.Fatalf("changed corpus: %v", e)
	}
	delete(f.docs[m.Indexes[0]], "a")
	c.options.Embedder = nil
	if _, e = c.Publish(context.Background(), "g1", records); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if f.mutations != before {
		t.Fatal("sealed generation was mutated")
	}
}

func TestCheckpoint64ResumesAfterEmbeddingFailure(t *testing.T) {
	c, f, _, _ := newFake(t)
	records := make([]memes.IndexRecord, 70)
	for i := range records {
		r := fixture(t, fmt.Sprintf("use%03d", i), "相同原解释")
		r.Projection.Relations[0].Pattern += fmt.Sprint(i)
		var e error
		records[i], e = memes.CompileCard(r.Full, r.Projection)
		if e != nil {
			t.Fatal(e)
		}
	}
	var sizes []int
	failed := false
	c.options.Embedder = embedFunc(func(_ context.Context, ss []string) ([][]float32, error) {
		sizes = append(sizes, len(ss))
		if len(sizes) == 2 && !failed {
			failed = true
			return nil, ErrEmbedding
		}
		out := make([][]float32, len(ss))
		for i := range out {
			out[i] = []float32{1, 2, 3}
		}
		return out, nil
	})
	if _, e := c.Publish(context.Background(), "g1", records); !errors.Is(e, ErrEmbedding) {
		t.Fatal(e)
	}
	if f.inserts != 1 || !slices.Equal(sizes, []int{64, 6}) {
		t.Fatal("first checkpoint", f.inserts, sizes)
	}
	if _, e := c.Publish(context.Background(), "g1", records); e != nil {
		t.Fatal(e)
	}
	if sizes[2] != 6 {
		t.Fatal("reembedded completed 64", sizes)
	}
}

func TestConcurrentPublishUsesGenerationLock(t *testing.T) {
	c, f, _, _ := newFake(t)
	records := []memes.IndexRecord{fixture(t, "a", "解释")}
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, e := c.Publish(context.Background(), "g1", records); errs <- e }()
	}
	for i := 0; i < 2; i++ {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	if f.inserts != 4 {
		t.Fatal("duplicate insert", f.inserts)
	}
}

func TestEmbeddingFailurePreservesSuccessfulEmptyBM25(t *testing.T) {
	c, f, _, _ := newFake(t)
	m, e := c.Publish(context.Background(), "g1", []memes.IndexRecord{fixture(t, "a", "解释")})
	if e != nil {
		t.Fatal(e)
	}
	c.options.Embedder = nil
	f.docs[m.Indexes[2]] = map[string]document{}
	r, _ := c.ForGeneration(m)
	snap, e := r.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q1", Field: memes.FieldKeys, Text: "Steam"}, {ID: "q2", Field: memes.FieldDescription, Text: "原话"}}})
	if e != nil {
		t.Fatal(e)
	}
	if !snap.Degraded || snap.SuccessfulQueries != 1 || len(snap.Candidates) != 0 || snap.RawQueries[0].Channels[0].Status != "ok" || len(snap.RawQueries[1].Channels) != 1 {
		t.Fatalf("lane failure semantics %+v", snap)
	}
}
func TestBadHitQuarantinedWithoutDiscardingLane(t *testing.T) {
	c, f, _, _ := newFake(t)
	m, e := c.Publish(context.Background(), "g1", []memes.IndexRecord{fixture(t, "a", "解释"), fixture(t, "b", "解释")})
	if e != nil {
		t.Fatal(e)
	}
	f.badHit = true
	r, _ := c.ForGeneration(m)
	snap, e := r.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q", Field: memes.FieldKeys, Text: "Steam"}}})
	if e != nil {
		t.Fatal(e)
	}
	if !snap.Degraded || len(snap.Candidates) != 2 {
		t.Fatalf("lost valid cards %+v", snap)
	}
	for _, lane := range snap.RawQueries[0].Channels {
		if lane.Status != "ok" || len(lane.Hits) != 2 || len(lane.Warnings) != 1 || lane.Warnings[0].Code != "INVALID_INDEX_HIT" || lane.Hits[0].Rank != 2 || lane.Hits[1].Rank != 3 {
			t.Fatalf("bad quarantine/ranks %+v", lane)
		}
	}
}

func TestLongDescriptionAllBytesMeanL2AndCache(t *testing.T) {
	c, f, _, calls := newFake(t)
	text := strings.Repeat("解释Steam蒋介石。", 6000)
	var chunks []string
	c.options.Embedder = embedFunc(func(_ context.Context, ss []string) ([][]float32, error) {
		out := make([][]float32, len(ss))
		for i, s := range ss {
			if !utf8.ValidString(s) || len(s) > 32768 {
				t.Fatal("bad embedding chunk")
			}
			chunks = append(chunks, s)
			out[i] = []float32{3, 4, 0}
		}
		*calls += len(ss)
		return out, nil
	})
	records := []memes.IndexRecord{fixture(t, "a", text), fixture(t, "b", text)}
	m, e := c.Publish(context.Background(), "g1", records)
	if e != nil {
		t.Fatal(e)
	}
	var originalChunks []string
	for _, s := range chunks {
		if strings.HasPrefix(s, "解释") || len(s) > 1000 {
			originalChunks = append(originalChunks, s)
		}
	}
	if strings.Join(originalChunks, "") != text {
		t.Fatal("original description lost bytes")
	}
	for _, d := range f.docs[m.Indexes[3]] {
		if len(d.Text) > 100 || len(d.Vector) != 3 || d.Vector[0] < .5999 || d.Vector[0] > .6001 || d.Vector[1] < .7999 || d.Vector[1] > .8001 {
			t.Fatal("bad long description", len(d.Text), d.Vector)
		}
	}
}

func TestMilvus26RealPublishSearch(t *testing.T) {
	endpoint := os.Getenv("MEME_TEST_MILVUS_URL")
	if endpoint == "" {
		t.Skip("set MEME_TEST_MILVUS_URL for isolated Milvus 2.6")
	}
	store := &memoryRecords{}
	trace := &traceTransport{base: http.DefaultTransport}
	t.Cleanup(func() {
		if t.Failed() {
			for _, line := range trace.responses {
				t.Log(line)
			}
		}
	})
	c, e := New(Options{Endpoint: endpoint, HTTPClient: &http.Client{Transport: trace}, Username: os.Getenv("MEME_TEST_MILVUS_USERNAME"), Password: os.Getenv("MEME_TEST_MILVUS_PASSWORD"), IndexPrefix: fmt.Sprintf("meme_test_%d", time.Now().UnixNano()), EmbeddingModel: "fixture", Dimensions: 3, Records: store, Embedder: embedFunc(func(_ context.Context, ss []string) ([][]float32, error) {
		out := make([][]float32, len(ss))
		for i := range out {
			out[i] = []float32{1, 2, 3}
		}
		return out, nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		for _, n := range c.indexes("g1") {
			_ = c.request(context.Background(), "collections/drop", map[string]any{"collectionName": n}, nil)
		}
	})
	records := []memes.IndexRecord{fixture(t, "a", strings.Repeat("蒋介石Steam解释", 10000)), fixture(t, "b", "另一解释")}
	m, e := c.Publish(context.Background(), "g1", records)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Publish(context.Background(), "g1", records); e != nil {
		t.Fatal("sealed retry", e)
	}
	r, e := c.ForGeneration(m)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := r.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q1", Field: memes.FieldKeys, Text: "蒋介石 Steam"}, {ID: "q2", Field: memes.FieldDescription, Text: "原话Steam"}}})
	if e != nil || len(snap.Candidates) != 2 || snap.Degraded {
		t.Fatalf("real retrieval candidates=%d degraded=%v err=%v", len(snap.Candidates), snap.Degraded, e)
	}
}

type traceTransport struct {
	base      http.RoundTripper
	responses []string
}

func (x *traceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, e := x.base.RoundTrip(r)
	if e == nil {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(b))
		x.responses = append(x.responses, r.URL.Path+" "+string(b))
	}
	return resp, e
}
