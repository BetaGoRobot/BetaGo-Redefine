package memestore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

func fixture(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("MEME_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set MEME_TEST_POSTGRES_DSN for isolated PostgreSQL integration")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	if _, err = rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "meme_test_" + hex.EncodeToString(b[:])
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); db.Close() })
	s, err := New(db, schema)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestImmutableFactsAreIdempotentScopedAndCannotBeRewritten(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a, b := s.ForScope("chat-a"), s.ForScope("chat-b")
	first, err := a.Put(ctx, Source, "source", json.RawMessage(`{"title":"test","n":1}`))
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.Put(ctx, Source, "source", json.RawMessage(`{"n":1,"title":"test"}`))
	if err != nil || again.Fingerprint != first.Fingerprint {
		t.Fatalf("canonical idempotence: %v", err)
	}
	if _, err = a.Put(ctx, Source, "source", map[string]any{"title": "changed"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("rewrite accepted: %v", err)
	}
	if _, err = b.Get(ctx, Source, "source"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-scope fact: %v", err)
	}
	if _, err = b.Put(ctx, Source, "source", map[string]any{"title": "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Put(ctx, Kind("sources;drop"), "bad", nil); err == nil {
		t.Fatal("unvalidated table name accepted")
	}
}

func TestActiveGenerationRequiresVerifiedImmutableManifest(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := s.ForScope("public")
	if err := a.Activate(ctx, "missing"); err == nil {
		t.Fatal("nonexistent generation activated")
	}
	if _, err := a.Put(ctx, Generation, "unverified", map[string]any{"verified": false}); err != nil {
		t.Fatal(err)
	}
	if err := a.Activate(ctx, "unverified"); err == nil {
		t.Fatal("unverified generation activated")
	}
	manifest := GenerationManifest{Generation: "g1", Verified: true, CorpusHash: "corpus", ProjectionHash: "projection", Dimension: 3, RecordCount: 2, Indexes: []string{"meme_g1_relation", "meme_g1_emotion", "meme_g1_keys", "meme_g1_description"}}
	if _, err := a.Put(ctx, Generation, "g1", manifest); err != nil {
		t.Fatal(err)
	}
	if err := a.Activate(ctx, "g1"); err != nil {
		t.Fatal(err)
	}
	got, err := a.Active(ctx)
	if err != nil || got.Generation != "g1" {
		t.Fatalf("active: %+v %v", got, err)
	}
}

func TestSchemaAndScopeValidation(t *testing.T) {
	for _, name := range []string{"x.y", "x\";drop", "UPPER", ""} {
		if _, err := New(&sql.DB{}, name); err == nil {
			t.Errorf("schema accepted: %q", name)
		}
	}
}

func TestGenerationAllowsOpenSearchNamesBeyondSQLIdentifierRules(t *testing.T) {
	s := fixture(t).ForScope("wiki")
	manifest := GenerationManifest{Generation: "wiki-01", Verified: true, CorpusHash: "corpus", ProjectionHash: "projection", EmbeddingModel: "embedding", Dimension: 2048, RecordCount: 1, Indexes: []string{"meme-refs_wiki-01_relation", "meme-refs_wiki-01_emotion_chain", "meme-refs_wiki-01_keys", "meme-refs_wiki-01_description"}}
	if _, err := s.Put(context.Background(), Generation, manifest.Generation, manifest); err != nil {
		t.Fatal(err)
	}
	if err := s.Activate(context.Background(), manifest.Generation); err != nil {
		t.Fatalf("valid OpenSearch name rejected as SQL identifier: %v", err)
	}
}

func TestProjectionRecordKindAndBackendRoundTripKeepLegacyManifestDistinct(t *testing.T) {
	if !validKind(Kind("meme_projection_records")) {
		t.Fatal("projection records cannot use immutable fact storage")
	}
	var manifest GenerationManifest
	if err := json.Unmarshal([]byte(`{"generation":"milvus-generation","backend":"milvus"}`), &manifest); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil || !strings.Contains(string(raw), `"backend":"milvus"`) {
		t.Fatalf("Milvus provenance dropped from generation: %s %v", raw, err)
	}
	var legacy GenerationManifest
	if err = json.Unmarshal([]byte(`{"generation":"legacy-generation"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(legacy)
	if err != nil || strings.Contains(string(raw), `"backend"`) {
		t.Fatalf("legacy generation falsely marked with a backend: %s %v", raw, err)
	}
}

func TestImmutableProjectionRecordPreservesLongDescriptionAndScope(t *testing.T) {
	store := fixture(t)
	ctx := context.Background()
	full := map[string]any{"use_id": "wiki.long", "description": strings.Repeat("完整原文", 20000)}
	record := map[string]any{"use_id": "wiki.long", "full": full, "fingerprint": "projection-fingerprint"}
	_, fingerprint, err := canonical(record)
	if err != nil {
		t.Fatal(err)
	}
	scoped := store.ForScope("wiki")
	saved, err := scoped.Put(ctx, Kind("meme_projection_records"), fingerprint, record)
	if err != nil || len(saved.Body) <= 64*1024 || saved.Fingerprint != fingerprint {
		t.Fatalf("complete projection storage failed: bytes=%d %v", len(saved.Body), err)
	}
	restored, err := scoped.Get(ctx, Kind("meme_projection_records"), fingerprint)
	if err != nil || string(restored.Body) != string(saved.Body) {
		t.Fatalf("complete projection readback failed: %v", err)
	}
	if _, err = store.ForScope("other").Get(ctx, Kind("meme_projection_records"), fingerprint); !errors.Is(err, ErrNotFound) {
		t.Fatalf("projection crossed scopes: %v", err)
	}
	full["description"] = "changed"
	if _, err = scoped.Put(ctx, Kind("meme_projection_records"), fingerprint, record); !errors.Is(err, ErrConflict) {
		t.Fatalf("immutable projection rewritten: %v", err)
	}
}
