package memestore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func knowledgeRecord(t *testing.T, id string) memes.IndexRecord {
	t.Helper()
	card := memes.UseCard{UseID: id, FamilyID: id, Name: "海岛奇兵", Description: "词条解释：也用于影射退守台湾后仍宣称反攻。", ReviewStatus: "pending", InputRelation: "处境收缩仍坚持反攻", Requirements: []memes.Requirement{{ID: "retreat", AnyOf: []string{"退守"}}}, ResponseAct: "调侃处境", Sources: []memes.SourceMetadata{{Kind: "wiki", Scope: "wiki", Title: "海岛奇兵", URL: "https://example.org/island"}}}
	fp, _ := memes.Fingerprint(card)
	record, err := memes.CompileCard(card, memes.Projection{CardFingerprint: fp, Relations: []memes.Relation{{Kind: "contrast", Pattern: "退守与反攻宣言形成反差"}}})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestKnowledgePublicationSizePreflightMatchesEncodedChunksAndRejectsAggregate(t *testing.T) {
	entries := []memes.KnowledgeEntry{{ID: "a", Description: strings.Repeat("x", 300000)}, {ID: "b", Description: strings.Repeat("y", 300000)}}
	actual := 0
	for _, entry := range entries {
		raw, err := json.Marshal([]memes.KnowledgeEntry{entry})
		if err != nil {
			t.Fatal(err)
		}
		actual += len(raw)
	}
	if err := knowledgeEncodedSize(entries, actual); err != nil {
		t.Fatal("readable exact boundary rejected", err)
	}
	if err := knowledgeEncodedSize(entries, actual-1); err == nil {
		t.Fatal("aggregate larger than reader budget accepted")
	}
}

func knowledgeGeneration(t *testing.T, s *Scoped) (GenerationManifest, []memes.IndexRecord) {
	t.Helper()
	ctx := context.Background()
	records := []memes.IndexRecord{knowledgeRecord(t, "sea")}
	cards := []memes.UseCard{records[0].Full}
	corpus, _ := memes.Fingerprint(cards)
	projection, _ := memes.Fingerprint(records)
	m := GenerationManifest{Generation: "knowledge-test", Backend: "milvus", Verified: true, CorpusHash: corpus, ProjectionHash: projection, RecordCount: 1, Dimension: 3, EmbeddingModel: "test", Indexes: []string{"kr", "ke", "kk", "kd"}}
	if _, err := s.Put(ctx, Generation, m.Generation, m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, IndexCard, records[0].Fingerprint, records[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, Publication, m.Generation+":milvus:verified", m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, Publication, m.Generation+":milvus:insert:kr:batch:receipt", []map[string]string{{"use_id": records[0].UseID, "fingerprint": records[0].Fingerprint}}); err != nil {
		t.Fatal(err)
	}
	return m, records
}

func TestKnowledgeUsesSealedMembershipAndImmutableBoundChunks(t *testing.T) {
	s := fixture(t).ForScope("wiki")
	ctx := context.Background()
	m, records := knowledgeGeneration(t, s)
	// A historical projection in the same scope must not enter the snapshot.
	old := knowledgeRecord(t, "historical")
	if _, err := s.Put(ctx, IndexCard, old.Fingerprint, old); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GenerationRecords(ctx, m)
	if err != nil || len(loaded) != 1 || loaded[0].UseID != records[0].UseID {
		t.Fatalf("membership: %v %v", loaded, err)
	}
	binding := KnowledgeBinding("wiki", m)
	snapshot, err := memes.CompileKnowledgeSnapshot(binding, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PublishKnowledge(ctx, m, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = s.PublishKnowledge(ctx, m, snapshot); err != nil {
		t.Fatalf("idempotence: %v", err)
	}
	got, err := s.Knowledge(ctx, m)
	if err != nil || got.Fingerprint != snapshot.Fingerprint {
		t.Fatalf("readback: %v", err)
	}
	selection, err := memes.SelectKnowledge(memes.Request{RequestID: "restore", Dialogue: []memes.Turn{{TurnID: "t", Speaker: "human", Text: "海岛奇兵"}}}, got)
	if err != nil {
		t.Fatal(err)
	}
	view := memes.GroundedKnowledgeContext(selection)
	view.BindKnowledgeSnapshot(got.Fingerprint)
	if err = s.ValidateKnowledgeEnvironment(ctx, view); err != nil {
		t.Fatal("valid restored binding rejected", err)
	}
	view.PriorKnowledge[0].EntryFingerprint = "forged"
	if err = s.ValidateKnowledgeEnvironment(ctx, view); err == nil {
		t.Fatal("self-reported entry fingerprint trusted")
	}
	view.PriorKnowledge[0].EntryFingerprint, _ = memes.Fingerprint(selection.Entries[0])
	view.Binding.ProjectionHash = "other"
	if err = s.ValidateKnowledgeEnvironment(ctx, view); err == nil {
		t.Fatal("restored generation mismatch trusted")
	}
	wrong := m
	wrong.ProjectionHash = "another-corpus"
	if _, err = s.Knowledge(ctx, wrong); !errors.Is(err, ErrNotFound) {
		t.Fatalf("different binding loaded: %v", err)
	}
	if _, err = s.store.ForScope("other").Knowledge(ctx, m); !errors.Is(err, ErrNotFound) {
		t.Fatalf("different scope loaded: %v", err)
	}
	// Honest hashing of a modified row must still fail snapshot provenance checks.
	var manifest knowledgeManifest
	row, err := s.Get(ctx, Knowledge, knowledgeID(binding))
	if err != nil || json.Unmarshal(row.Body, &manifest) != nil {
		t.Fatal(err)
	}
	if len(manifest.Chunks) == 0 {
		t.Fatal("unsealed chunk list")
	}
	_, err = s.store.db.ExecContext(ctx, `DELETE FROM `+s.store.table(string(Knowledge))+` WHERE scope_key=$1 AND id=$2`, s.scope, manifest.Chunks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Knowledge(ctx, m); err == nil {
		t.Fatal("missing chunk accepted")
	}
}

func TestKnowledgeRejectsIncorrectGenerationHashesBeforePublication(t *testing.T) {
	s := fixture(t).ForScope("wiki")
	m, records := knowledgeGeneration(t, s)
	wrong := m
	wrong.RecordCount++
	if _, err := s.GenerationRecords(context.Background(), wrong); err == nil {
		t.Fatal("wrong count accepted")
	}
	wrong = m
	wrong.CorpusHash = "wrong"
	if _, err := s.GenerationRecords(context.Background(), wrong); err == nil {
		t.Fatal("wrong manifest accepted")
	}
	snapshot, err := memes.CompileKnowledgeSnapshot(KnowledgeBinding("wiki", m), records)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Binding.Scope = "other"
	if err = s.PublishKnowledge(context.Background(), m, snapshot); err == nil {
		t.Fatal("cross scope snapshot published")
	}
}
