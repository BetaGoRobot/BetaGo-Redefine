package memes

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func knowledgeTestBinding() KnowledgeBinding {
	return KnowledgeBinding{Scope: "public", Generation: "generation-1", CorpusHash: "corpus-1", ProjectionHash: "projection-1"}
}

func knowledgeTestRecord(t *testing.T, id, title, name, description string, keys ProjectionKeys) IndexRecord {
	t.Helper()
	c := fixedCard(id)
	c.Name, c.Description = name, description
	c.Sources = []SourceMetadata{{Kind: "wiki", Scope: "wiki:" + title, URL: "https://example.invalid/wiki/" + title, Title: title}}
	fp, err := Fingerprint(c)
	if err != nil {
		t.Fatal(err)
	}
	r, err := CompileCard(c, Projection{CardFingerprint: fp, Relations: []Relation{{Kind: "contrast", Pattern: "同一对象的不同解释"}}, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func knowledgeTestSnapshot(t *testing.T, records ...IndexRecord) KnowledgeSnapshot {
	t.Helper()
	s, err := CompileKnowledgeSnapshot(knowledgeTestBinding(), records)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func knowledgeTestReseal(t *testing.T, s *KnowledgeSnapshot) {
	t.Helper()
	s.Fingerprint = ""
	var err error
	s.Fingerprint, err = Fingerprint(*s)
	if err != nil {
		t.Fatal(err)
	}
}

func knowledgeTestClone[T any](t *testing.T, v T) T {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestKnowledgeSnapshotDeduplicatesSourceAndRetainsEveryRecord(t *testing.T) {
	a := knowledgeTestRecord(t, "hoi.a", "钢铁雄心", "钢铁雄心", strings.Repeat("完整原始释义。", 250), ProjectionKeys{})
	b := knowledgeTestRecord(t, "hoi.b", "钢铁雄心", "历史游戏", a.Full.Description, ProjectionKeys{})
	b.Full.FamilyID = "another_family"
	b.Projection.CardFingerprint, _ = Fingerprint(b.Full)
	b, _ = CompileCard(b.Full, b.Projection)
	s := knowledgeTestSnapshot(t, a, b, a)
	if len(s.Entries) != 1 {
		t.Fatalf("same source description occupied multiple entries: %+v", s.Entries)
	}
	e := s.Entries[0]
	if e.Description != a.Full.Description || !reflect.DeepEqual(e.UseIDs, []string{"hoi.a", "hoi.b"}) || len(e.FamilyIDs) != 2 || len(e.RecordFingerprints) != 2 {
		t.Fatalf("lost full description or source record references: %+v", e)
	}
	if e.Binding != s.Binding || e.Senses[0].SourceKind != "wiki_explanation" || e.Senses[0].Status != "source_claim" {
		t.Fatalf("source explanation was not kept as a bound source claim: %+v", e)
	}
	a.Full.Sources[0].Title = "mutated"
	if s.Entries[0].Sources[0].Title != "钢铁雄心" {
		t.Fatal("snapshot aliases mutable record sources")
	}
	if err := ValidateKnowledgeSnapshot(s, knowledgeTestBinding()); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeSnapshotAliasProvenanceDoesNotPromoteGeneratedKeys(t *testing.T) {
	r := knowledgeTestRecord(t, "hoi", "钢铁雄心", "三年", "游戏及社区历史影射的来源解释。", ProjectionKeys{RequiredAliasGroups: [][]string{{"二战", "海岛奇兵"}}, Related: []string{"1949年"}})
	s := knowledgeTestSnapshot(t, r)
	want := map[string]string{"钢铁雄心": "source_title", "三年": "inferred_card_name", "二战": "inferred_projection_alias", "海岛奇兵": "inferred_projection_alias", "1949年": "inferred_projection_related"}
	for _, a := range s.Entries[0].Aliases {
		if want[a.Text] != a.Basis {
			t.Fatalf("alias lacks its evidence basis: %+v", a)
		}
		delete(want, a.Text)
	}
	if len(want) != 0 {
		t.Fatalf("missing alias candidates: %+v", want)
	}
	got, err := SelectKnowledge(knowledgeTestRequest("海岛奇兵、二战、1949年"), s)
	if err != nil || len(got.Anchors) != 0 {
		t.Fatalf("generated associations silently became literal source aliases: %+v %v", got, err)
	}
	got, err = SelectKnowledge(knowledgeTestRequest("三年，从钢铁雄心说起"), s)
	if err != nil || len(got.Anchors) != 2 || got.Anchors[0].Text != "钢铁雄心" || got.Anchors[1].MatchKind != "inferred_card_name_exact" {
		t.Fatalf("generic inferred card name outranked exact named source: %+v %v", got, err)
	}
}

func TestKnowledgeSnapshotFingerprintIgnoresInputRecordOrder(t *testing.T) {
	a := knowledgeTestRecord(t, "a", "钢铁雄心", "钢铁雄心", "来源解释甲", ProjectionKeys{RequiredAliasGroups: [][]string{{"甲", "乙"}}, Related: []string{"丙", "丁"}})
	b := knowledgeTestRecord(t, "b", "海岛奇兵", "海岛奇兵", "来源解释乙", ProjectionKeys{})
	first := knowledgeTestSnapshot(t, a, b)
	second := knowledgeTestSnapshot(t, b, a)
	if first.Fingerprint == "" || first.Fingerprint != second.Fingerprint || !reflect.DeepEqual(first, second) {
		t.Fatal("snapshot fingerprint or contents depend on record traversal")
	}
}

func TestKnowledgeSnapshotRejectsNativeRecordAndBindingTampering(t *testing.T) {
	r := knowledgeTestRecord(t, "a", "微操", "微操", "来源原文", ProjectionKeys{})
	for _, mutate := range []func(*IndexRecord){
		func(r *IndexRecord) { r.Fingerprint = "forged" },
		func(r *IndexRecord) { r.Full.Description = "changed" },
		func(r *IndexRecord) { r.Keys = "forged projection" },
		func(r *IndexRecord) { r.UseID = "other" },
	} {
		bad := knowledgeTestClone(t, r)
		mutate(&bad)
		if _, err := CompileKnowledgeSnapshot(knowledgeTestBinding(), []IndexRecord{bad}); err == nil {
			t.Fatal("tampered native record accepted")
		}
	}
	s := knowledgeTestSnapshot(t, r)
	for _, field := range []string{"scope", "generation", "corpus", "projection"} {
		t.Run(field, func(t *testing.T) {
			bad := knowledgeTestClone(t, s)
			switch field {
			case "scope":
				bad.Entries[0].Binding.Scope = "private:other"
			case "generation":
				bad.Entries[0].Binding.Generation = "old"
			case "corpus":
				bad.Entries[0].Binding.CorpusHash = "old"
			case "projection":
				bad.Entries[0].Binding.ProjectionHash = "old"
			}
			knowledgeTestReseal(t, &bad)
			if ValidateKnowledgeSnapshot(bad, s.Binding) == nil {
				t.Fatal("entry binding mismatch accepted after resealing snapshot")
			}
			if _, err := SelectKnowledge(knowledgeTestRequest("微操"), bad); err == nil {
				t.Fatal("scanner accepted cross-binding entry")
			}
		})
	}
	bad := knowledgeTestClone(t, s)
	bad.Entries[0].Description = "changed"
	if ValidateKnowledgeSnapshot(bad, s.Binding) == nil {
		t.Fatal("snapshot content mutation accepted")
	}
	if ValidateKnowledgeSnapshot(s, KnowledgeBinding{Scope: "other"}) == nil {
		t.Fatal("caller binding mismatch accepted")
	}
}

func TestKnowledgeSnapshotRejectsInvalidBindingAndPreservesSeparateSources(t *testing.T) {
	for _, binding := range []KnowledgeBinding{{}, {Scope: "public", Generation: "g", CorpusHash: "c"}, {Scope: "public", Generation: "g", CorpusHash: "c", ProjectionHash: string([]byte{255})}} {
		if _, err := CompileKnowledgeSnapshot(binding, nil); err == nil {
			t.Fatal("incomplete or invalid binding accepted")
		}
	}
	empty := knowledgeTestSnapshot(t)
	if empty.Entries == nil || ValidateKnowledgeSnapshot(empty, empty.Binding) != nil {
		t.Fatal("valid empty corpus is not a frozen, explicit snapshot")
	}
	r := knowledgeTestRecord(t, "a", "微操", "微操", "来源原文", ProjectionKeys{})
	r.Full.Sources = append(r.Full.Sources, SourceMetadata{Kind: "constructed", Scope: "constructed", Title: "构造义项"})
	r.Projection.CardFingerprint, _ = Fingerprint(r.Full)
	r, err := CompileCard(r.Full, r.Projection)
	if err != nil {
		t.Fatal(err)
	}
	s := knowledgeTestSnapshot(t, r)
	if len(s.Entries) != 2 {
		t.Fatal("distinct sources were merged without provenance")
	}
	for _, entry := range s.Entries {
		if entry.Sources[0].Kind == "constructed" && (entry.Senses[0].SourceKind != "inferred_annotation" || entry.Senses[0].Status != "inferred") {
			t.Fatal("constructed annotation was upgraded to an external source claim")
		}
	}
	for _, mutate := range []func(*KnowledgeSnapshot){
		func(s *KnowledgeSnapshot) { s.SchemaVersion++ },
		func(s *KnowledgeSnapshot) { s.Entries = nil },
		func(s *KnowledgeSnapshot) { s.Entries[0].Senses[0].Status = "verified" },
		func(s *KnowledgeSnapshot) { s.Entries[0].Aliases[0].Basis = "proven_alias" },
		func(s *KnowledgeSnapshot) { s.Entries = append(s.Entries, s.Entries[0]) },
	} {
		bad := knowledgeTestClone(t, s)
		mutate(&bad)
		knowledgeTestReseal(t, &bad)
		if ValidateKnowledgeSnapshot(bad, bad.Binding) == nil {
			t.Fatal("resealed invalid knowledge contract accepted")
		}
	}
}
