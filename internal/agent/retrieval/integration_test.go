package retrieval_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/hindsight"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/meili"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
	_ "github.com/lib/pq"
)

// This test joins real isolated PostgreSQL + Meilisearch clients. Embeddings
// and ranking use the deterministic fixture model; Hindsight uses a pinned HTTP
// fixture joined to real PG source proof. No semantic-quality assertion.
func TestRealPGAndMeiliJoinRejectsStaleEvidenceAndCoversIndexGap(t *testing.T) {
	dsn, endpoint, key := os.Getenv("AGENT_TEST_POSTGRES_DSN"), os.Getenv("AGENT_TEST_MEILI_URL"), os.Getenv("AGENT_TEST_MEILI_API_KEY")
	if dsn == "" || endpoint == "" || key == "" {
		t.Skip("isolated PostgreSQL and Meilisearch required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal("isolated PG configuration unavailable")
	}
	defer db.Close()
	if db.PingContext(ctx) != nil {
		t.Fatal("isolated PG unavailable")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	prefix := "retrieval_test_" + hex.EncodeToString(random[:])
	store, err := pgstore.New(db, prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.Exec(`DROP SCHEMA IF EXISTS "` + prefix + `" CASCADE`); err != nil {
			t.Error("isolated PG cleanup failed")
		}
	}()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal("isolated PG migration failed")
	}
	o := meili.Options{Endpoint: endpoint, APIKey: key, MessagesIndex: prefix + "_messages", ContextsIndex: prefix + "_contexts", EmbeddingModel: "deterministic-fixture-v1", VectorDimensions: 2, Timeout: 15 * time.Second, PollInterval: 5 * time.Millisecond}
	idx, err := meili.New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = idx.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer deleteIndexes(t, endpoint, key, o.MessagesIndex, o.ContextsIndex)
	accept := func(m domain.Message, id string) {
		t.Helper()
		m.EventID = id
		kind := domain.EventMessage
		if m.Deleted {
			kind = domain.EventDelete
		}
		if _, err := store.Accept(ctx, domain.Event{ID: id, Type: kind, Message: m, OccurredAt: m.UpdatedAt}); err != nil {
			t.Fatal("isolated PG accept failed")
		}
	}
	fresh := message("fresh", "预算 审批 原始记录")
	stale := message("stale", "预算 审批 旧版本")
	deleted := message("deleted", "预算 审批 删除记录")
	future := message("future", "预算 审批 未来")
	future.Source.OccurredAt = anchor.Add(time.Nanosecond)
	future.UpdatedAt = future.Source.OccurredAt
	cross := message("cross", "预算 审批 其他范围")
	cross.Source.Scope.Visibility = "private"
	for _, m := range []domain.Message{fresh, stale, deleted, future, cross} {
		accept(m, "event_"+m.Source.MessageID)
		if err := idx.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	oldStale := stale
	stale.Source.Version++
	stale.UpdatedAt = anchor.Add(-time.Millisecond)
	stale.Text = "已修改为其他内容"
	accept(stale, "edit_stale")
	deleted.Source.Version++
	deleted.Deleted = true
	deleted.UpdatedAt = anchor.Add(-time.Millisecond)
	accept(deleted, "delete_message")
	gap := message("gap", "预算 审批 异步索引空窗")
	gap.Source.OccurredAt = anchor.Add(-time.Millisecond)
	gap.UpdatedAt = gap.Source.OccurredAt
	accept(gap, "index_gap")
	mdl := &model{}
	service, err := retrieval.New(retrieval.Options{Sources: store, Search: idx, Model: mdl, RecentLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Quoted raw terms have a complete PG fallback without pretending to
	// reproduce the engine's Chinese all-term tokenizer in recent messages.
	q := query(domain.QueryLiteral, `"预算" "审批"`)
	r, err := service.Retrieve(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range r.Evidence {
		for _, ref := range e.Sources {
			got[ref.MessageID] = true
		}
	}
	if len(got) != 2 || !got["fresh"] || !got["gap"] || r.Degraded || mdl.embeds+mdl.reranks != 0 {
		t.Fatalf("real joined literal proof failed: ids=%v degraded=%v", got, r.Degraded)
	}
	goodContext := domain.Context{ID: "good_context", Scope: scope, Version: 1, Text: "预算 审批 讨论摘要", Sources: []domain.SourceRef{fresh.Source}, CreatedAt: fresh.Source.OccurredAt, UpdatedAt: fresh.UpdatedAt}
	if err := store.PutContext(ctx, goodContext); err != nil {
		t.Fatal("isolated context persist failed")
	}
	if err := idx.UpsertContext(ctx, goodContext, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	badContext := goodContext
	badContext.ID = "stale_context"
	badContext.Sources = []domain.SourceRef{fresh.Source, oldStale.Source}
	if err := idx.UpsertContext(ctx, badContext, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	q.Mode = domain.QueryDiscussion
	r, err = service.Retrieve(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Degraded || mdl.embeds != 1 || mdl.reranks != 1 {
		t.Fatalf("real separated semantic branches failed: %+v", r.Failures)
	}
	for _, e := range r.Evidence {
		for _, ref := range e.Sources {
			if ref.MessageID == "stale" {
				t.Fatal("partially valid discussion escaped current PG proof")
			}
		}
	}
	// Author inclusion is ANY for a discussion, while the generic PG proof
	// retains its ALL-author contract. Bob's media source must remain a full
	// dependency even when querying Alice's contributions.
	bob := message("bob_media", "")
	bob.Source.AuthorID = "bob"
	bob.Source.OccurredAt = anchor.Add(-2 * time.Minute)
	bob.Media = []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "fixture-image", Sequence: 0}}
	accept(bob, "mixed_bob_media")
	mixed := domain.Context{ID: "mixed_context", Scope: scope, Version: 1, Text: "预算 审批 OCR extracted media and discussion rationale", Sources: []domain.SourceRef{fresh.Source, bob.Source}, CreatedAt: bob.Source.OccurredAt, UpdatedAt: fresh.UpdatedAt}
	if err := store.PutContext(ctx, mixed); err != nil {
		t.Fatal("mixed context persistence failed")
	}
	if err := idx.UpsertContext(ctx, mixed, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	q.AuthorID = "alice"
	if all, err := store.ValidSources(ctx, q, mixed.Sources); err != nil || all {
		t.Fatal("generic PG ALL-author proof contract changed")
	}
	r, err = service.Retrieve(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	foundMixed := false
	for _, e := range r.Evidence {
		if e.DerivedText == mixed.Text {
			foundMixed = reflect.DeepEqual(e.Sources, mixed.Sources) && e.Text == fresh.Text+"\n"
		}
	}
	if !foundMixed {
		t.Error("real Meili ANY-author context lost derived text or complete mixed-author provenance")
	}
	mem := mixedAuthorMemoryFixture(t, mixed)
	memoryService, err := retrieval.New(retrieval.Options{Sources: store, Memory: mem, RecentLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	memoryQuery := q
	memoryQuery.Mode = domain.QueryMemory
	r, err = memoryService.Retrieve(ctx, memoryQuery)
	if err != nil {
		t.Fatal(err)
	}
	foundObservation := false
	for _, e := range r.Evidence {
		if e.ID == "mixed_observation" {
			foundObservation = e.DerivedText == "Consolidated mixed-author memory" && e.Text == fresh.Text+"\n" && reflect.DeepEqual(e.Sources, mixed.Sources)
		}
	}
	if !foundObservation {
		t.Error("HTTP memory observation did not survive complete real PG mixed-author proof")
	}
	bob.Source.Version++
	bob.UpdatedAt = anchor
	bob.Deleted = true
	accept(bob, "delete_mixed_bob")
	r, err = memoryService.Retrieve(ctx, memoryQuery)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Evidence {
		if e.ID == "mixed_observation" {
			t.Fatal("memory accepted a matching Alice reference after its Bob dependency was deleted")
		}
	}
	q.AuthorID = ""
	// A source edited while the reranker runs is rejected by the final reload.
	mdl.rewrite = func(in []domain.Evidence) []domain.Evidence {
		fresh.Source.Version++
		fresh.UpdatedAt = anchor
		fresh.Text = "改写后的原文"
		accept(fresh, "edit_during_rank")
		return in
	}
	r, err = service.Retrieve(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Evidence {
		for _, ref := range e.Sources {
			if ref.MessageID == "fresh" {
				t.Fatal("source changed during model call survived")
			}
		}
	}
}

// The fixture speaks pinned 0.10.2 HTTP retain/recall shapes; its extraction is
// deterministic synthetic data. Current source validity comes from real PG.
func mixedAuthorMemoryFixture(t *testing.T, document domain.Context) *hindsight.Client {
	t.Helper()
	var metadata map[string]string
	bank := "/v1/default/banks/" + hindsight.BankID(document.Scope)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-memory-key" {
			t.Error("fixture memory authorization missing")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == bank:
			_ = json.NewEncoder(w).Encode(map[string]any{"bank_id": hindsight.BankID(document.Scope)})
		case r.Method == http.MethodPost && r.URL.Path == bank+"/memories":
			var body struct {
				Items []struct{ Metadata map[string]string }
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Items) != 1 {
				t.Error("fixture memory retain invalid")
				http.Error(w, "invalid", 400)
				return
			}
			metadata = body.Items[0].Metadata
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "bank_id": hindsight.BankID(document.Scope), "items_count": 1, "async": false})
		case r.Method == http.MethodPost && r.URL.Path == bank+"/memories/recall":
			tags := []string{document.Scope.Key()}
			observation := map[string]any{"id": "mixed_observation", "type": "observation", "text": "Consolidated mixed-author memory", "source_fact_ids": []string{"mixed_fact"}, "tags": tags}
			fact := map[string]any{"id": "mixed_fact", "type": "world", "text": "Mixed source fact", "document_id": hindsight.DocumentID(document.Scope, document.ID), "metadata": metadata, "tags": tags}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{observation}, "source_facts": map[string]any{"mixed_fact": fact}, "source_facts_truncated": false})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := hindsight.New(hindsight.Options{URL: srv.URL, APIKey: "fixture-memory-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Retain(context.Background(), document); err != nil {
		t.Fatal(err)
	}
	return c
}

// Only fresh per-test indexes are removed. No existing fixture index is read,
// modified, or deleted. A separate timeout permits cleanup after test expiry.
func deleteIndexes(t *testing.T, endpoint, key string, indexes ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request := func(method, path string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, endpoint+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(io.LimitReader(resp.Body, 4096))
	}
	for _, index := range indexes {
		body, err := request("DELETE", "/indexes/"+index)
		if err != nil {
			t.Error("isolated index cleanup failed")
			continue
		}
		var task struct {
			TaskUID json.Number `json:"taskUid"`
		}
		if json.Unmarshal(body, &task) != nil || task.TaskUID == "" {
			t.Error("isolated cleanup task unavailable")
			continue
		}
		for {
			body, err = request("GET", "/tasks/"+task.TaskUID.String())
			if err != nil {
				t.Error("isolated cleanup wait failed")
				break
			}
			var status struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(body, &status) != nil || status.Status == "failed" {
				t.Error("isolated cleanup task failed")
				break
			}
			if status.Status == "succeeded" {
				break
			}
			select {
			case <-ctx.Done():
				t.Error("isolated cleanup deadline")
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
}
