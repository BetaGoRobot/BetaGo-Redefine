package memesource

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

type memoryStore struct {
	records map[string]memestore.Record
	active  string
	order   []string
}

func newMemoryStore() *memoryStore { return &memoryStore{records: map[string]memestore.Record{}} }
func (s *memoryStore) Put(_ context.Context, k memestore.Kind, id string, v any) (memestore.Record, error) {
	raw, _ := json.Marshal(v)
	fp, _ := memes.Fingerprint(v)
	key := string(k) + ":" + id
	if old, ok := s.records[key]; ok && old.Fingerprint != fp {
		return memestore.Record{}, memestore.ErrConflict
	}
	r := memestore.Record{ID: id, Body: raw, Fingerprint: fp}
	s.records[key] = r
	s.order = append(s.order, string(k))
	return r, nil
}
func (s *memoryStore) Get(_ context.Context, k memestore.Kind, id string) (memestore.Record, error) {
	r, ok := s.records[string(k)+":"+id]
	if !ok {
		return r, memestore.ErrNotFound
	}
	return r, nil
}
func (s *memoryStore) Activate(_ context.Context, id string) error {
	s.active = id
	s.order = append(s.order, "activate")
	return nil
}

type fakeCompletion struct {
	calls   int
	request memes.CompletionRequest
	fail    bool
}

type completionFunc func(context.Context, memes.CompletionRequest) (memes.CompletionResponse, error)

func (f completionFunc) Complete(ctx context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
	return f(ctx, in)
}

func (m *fakeCompletion) Complete(_ context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
	m.calls++
	m.request = in
	if m.fail {
		return memes.CompletionResponse{}, errors.New("provider failed")
	}
	encoded := memes.EncodedWiki{Cards: []memes.EncodedUse{{Full: memes.UseCard{
		Requirements: []memes.Requirement{}, Roles: map[string]memes.Role{}, Slots: map[string]memes.Slot{}, Examples: memes.Examples{Positive: []memes.Example{}, NearMiss: []memes.Example{}},
		UseID: "one", FamilyID: "family", Name: "测试词条", ReviewStatus: "approved", Description: "模型改写的描述", InputRelation: "结果与预期形成反差", ResponseAct: "感叹反差", Template: "测试固定表达", Anchors: []string{"测试固定表达"},
		Stance: memes.Stance{Voice: "bot_comment", ReplyTarget: "conversation", EvaluationTarget: "conversation"}, Sources: []memes.SourceMetadata{{Kind: "fabricated", Scope: "approved", URL: "https://wrong.invalid"}},
	}, Projection: memes.Projection{CardFingerprint: "model-cannot-compute", Relations: []memes.Relation{{Kind: "contrast", Pattern: "结果与预期形成反差"}}, EmotionChain: []memes.EmotionChain{}, Keys: memes.ProjectionKeys{RequiredAliasGroups: [][]string{}, Related: []string{"结果"}}}}}}
	raw, _ := json.Marshal(encoded)
	return memes.CompletionResponse{JSON: raw, Metadata: memes.CallMetadata{Model: "model", DurationMS: 12}}, nil
}

type fakePublisher struct {
	calls    int
	records  []memes.IndexRecord
	store    *memoryStore
	verified bool
}

func (p *fakePublisher) Publish(_ context.Context, generation string, records []memes.IndexRecord) (memestore.GenerationManifest, error) {
	p.calls++
	p.records = records
	p.store.order = append(p.store.order, "publish_readback")
	return memestore.GenerationManifest{Generation: generation, Verified: p.verified, CorpusHash: "corpus", ProjectionHash: "projection", EmbeddingModel: "embedding", Dimension: 2, RecordCount: len(records), Indexes: []string{"relation", "emotion", "keys", "description"}}, nil
}
func testSource() Source {
	s := Source{ID: "wiki:fixture", URL: "https://cnmeme.wiki/api/v1/public/memes/fixture", SchemaVersion: 1, Item: WikiItem{Slug: "fixture", Title: "测试词条", ContentHTML: "<p>原始条件与边界。</p>"}, Description: "原始条件与边界。", RawCatalogItem: json.RawMessage(`{"slug":"fixture"}`), RawDetail: json.RawMessage(`{"schema_version":1,"item":{"slug":"fixture","title":"测试词条","content_html":"<p>原始条件与边界。</p>"}}`)}
	s.Revision, _ = memes.Fingerprint(s)
	return s
}

func TestSyncEncodesExactProvenanceAndReusesImmutableRevision(t *testing.T) {
	store := newMemoryStore()
	model := &fakeCompletion{}
	publisher := &fakePublisher{store: store, verified: true}
	catalog := Catalog{Sources: []Source{testSource()}, Complete: true}
	deps := SyncDependencies{Store: store, Model: model, Publisher: publisher}
	options := SyncOptions{ModelID: "model", Generation: "fixture1"}
	report, err := Sync(context.Background(), catalog, deps, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Active || report.CardCount != 1 || report.EncodedSources != 1 || len(report.Calls) != 1 || store.active != "fixture1" {
		t.Fatalf("report: %+v", report)
	}
	if model.request.Stage != memes.StageEncode || !strings.Contains(model.request.SystemPrompt, memes.SystemPrompt(memes.StageEncode)) || !strings.Contains(model.request.SystemPrompt, "projection.relations至少") || !json.Valid(model.request.Schema) || !strings.Contains(string(model.request.InputJSON), "原始条件与边界") {
		t.Fatalf("wrong encoding request: %+v", model.request)
	}
	full := publisher.records[0].Full
	if full.Description != catalog.Sources[0].Description || full.ReviewStatus != "pending" || full.Sources[0].URL != catalog.Sources[0].URL || full.Sources[0].Kind != "wiki" || !strings.HasPrefix(full.UseID, "wiki_") {
		t.Fatalf("unbound provenance: %+v", full)
	}
	if _, err := memes.CompileCard(full, publisher.records[0].Projection); err != nil {
		t.Fatal(err)
	}
	if strings.Join(store.order, ",") != "meme_sources,meme_card_versions,publish_readback,meme_index_generations,activate" {
		t.Fatalf("activation before verified publication: %v", store.order)
	}
	report, err = Sync(context.Background(), catalog, deps, options)
	if err != nil || model.calls != 1 || report.ReusedSources != 1 || report.EncodedSources != 0 {
		t.Fatalf("resume made duplicate model call: %+v %v calls=%d", report, err, model.calls)
	}
	options.ModelID = "changed-model"
	_, err = Sync(context.Background(), catalog, deps, options)
	if err != nil || model.calls != 2 {
		t.Fatalf("changed encoding config reused previous card: %v calls=%d", err, model.calls)
	}
}

func TestSyncSourceOrEncodingFailurePreventsPublication(t *testing.T) {
	for _, stage := range []string{"detail", "encode"} {
		t.Run(stage, func(t *testing.T) {
			store := newMemoryStore()
			model := &fakeCompletion{fail: stage == "encode"}
			publisher := &fakePublisher{store: store, verified: true}
			catalog := Catalog{Sources: []Source{testSource()}, Complete: true}
			if stage == "detail" {
				catalog.Errors = []SourceError{{SourceID: "wiki:missing", Stage: "detail", Error: "Wiki HTTP status 404"}}
			}
			report, err := Sync(context.Background(), catalog, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model", Generation: "failure"})
			if !errors.Is(err, ErrIncomplete) || len(report.Errors) != 1 || report.Active || publisher.calls != 0 || store.active != "" {
				t.Fatalf("partial import activated: %+v %v", report, err)
			}
			if len(store.records) == 0 {
				t.Fatal("raw source lost on encode failure")
			}
			if stage == "encode" && (model.calls != 1 || len(report.Calls) != 1) {
				t.Fatal("transport failure received a structure repair retry")
			}
		})
	}
}

func TestSyncRejectsUnverifiedPublicationAndCorruptSource(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "unverified", true: "source revision"}[corrupt], func(t *testing.T) {
			store := newMemoryStore()
			model := &fakeCompletion{}
			publisher := &fakePublisher{store: store}
			source := testSource()
			if corrupt {
				source.Description = "tampered"
			}
			report, err := Sync(context.Background(), Catalog{Sources: []Source{source}}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
			if err == nil || store.active != "" || report.Active {
				t.Fatalf("activated invalid data: %+v %v", report, err)
			}
			if corrupt && model.calls != 0 {
				t.Fatal("encoded a corrupt source revision")
			}
		})
	}
}

func TestSyncDerivesLocalIDsWithoutRepairingMissingSemanticFields(t *testing.T) {
	store := newMemoryStore()
	model := &fakeCompletion{}
	publisher := &fakePublisher{store: store, verified: true}
	one, two := testSource(), testSource()
	two.ID = "wiki:second"
	two.Item.Slug = "second"
	two.Revision = ""
	two.Revision, _ = memes.Fingerprint(two)
	report, err := Sync(context.Background(), Catalog{Sources: []Source{one, two}}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
	if err != nil || report.CardCount != 2 || publisher.records[0].UseID == publisher.records[1].UseID {
		t.Fatalf("colliding local model IDs: %+v %v", report, err)
	}
	store = newMemoryStore()
	publisher = &fakePublisher{store: store, verified: true}
	invalid := completionFunc(func(ctx context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
		response, err := model.Complete(ctx, in)
		var encoded memes.EncodedWiki
		json.Unmarshal(response.JSON, &encoded)
		encoded.Cards[0].Full.InputRelation = ""
		response.JSON, _ = json.Marshal(encoded)
		return response, err
	})
	report, err = Sync(context.Background(), Catalog{Sources: []Source{one}}, SyncDependencies{Store: store, Model: invalid, Publisher: publisher}, SyncOptions{ModelID: "model"})
	if !errors.Is(err, ErrIncomplete) || len(report.Errors) != 1 || publisher.calls != 0 || len(report.Calls) != 1 {
		t.Fatalf("program identity silently repaired missing semantic fields: %+v %v", report, err)
	}
}

func TestSyncRepairsMissingStructureOnceWithExactOriginalSource(t *testing.T) {
	store := newMemoryStore()
	publisher := &fakePublisher{store: store, verified: true}
	fixture := &fakeCompletion{}
	valid, _ := fixture.Complete(context.Background(), memes.CompletionRequest{})
	var object map[string]any
	if err := json.Unmarshal(valid.JSON, &object); err != nil {
		t.Fatal(err)
	}
	full := object["cards"].([]any)[0].(map[string]any)["full"].(map[string]any)
	delete(full, "requirements")
	invalid, _ := json.Marshal(object)
	var requests []memes.CompletionRequest
	model := completionFunc(func(_ context.Context, request memes.CompletionRequest) (memes.CompletionResponse, error) {
		requests = append(requests, request)
		if len(requests) == 1 {
			return memes.CompletionResponse{JSON: invalid, Metadata: memes.CallMetadata{DurationMS: 11}}, nil
		}
		return memes.CompletionResponse{JSON: valid.JSON, Metadata: memes.CallMetadata{DurationMS: 17}}, nil
	})
	catalog := Catalog{Sources: []Source{testSource()}, Complete: true}
	report, err := Sync(context.Background(), catalog, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
	if err != nil || !report.Active || len(requests) != 2 || len(report.Calls) != 2 || report.Calls[0].DurationMS != 11 || report.Calls[1].DurationMS != 17 {
		t.Fatalf("structure repair failed: report=%+v error=%v calls=%d", report, err, len(requests))
	}
	if requests[0].Repair || !requests[1].Repair || string(requests[0].Schema) != string(requests[1].Schema) || requests[1].Stage != memes.StageEncode {
		t.Fatal("repair changed stage or schema")
	}
	var repair struct {
		OriginalInput  json.RawMessage `json:"original_input"`
		InvalidOutput  string          `json:"invalid_output"`
		StructureError string          `json:"structure_error"`
	}
	if err := json.Unmarshal(requests[1].InputJSON, &repair); err != nil {
		t.Fatal(err)
	}
	if string(repair.OriginalInput) != string(requests[0].InputJSON) || repair.InvalidOutput != string(invalid) || !strings.Contains(repair.StructureError, "$.cards[0].full.requirements") {
		t.Fatalf("repair lost exact source or field path: %+v", repair)
	}
	if publisher.records[0].Full.Description != catalog.Sources[0].Description || strings.Join(store.order, ",") != "meme_sources,meme_card_versions,publish_readback,meme_index_generations,activate" {
		t.Fatal("repair changed authoritative source or persisted a failed draft")
	}
	report, err = Sync(context.Background(), catalog, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
	if err != nil || len(requests) != 2 || report.ReusedSources != 1 {
		t.Fatalf("successful repair was not cached: %+v %v", report, err)
	}
}

func TestSyncStopsAfterOneStructureRepairAndReportsSafeFieldPath(t *testing.T) {
	for _, test := range []struct {
		name       string
		invalid    json.RawMessage
		diagnostic string
	}{
		{"missing", json.RawMessage(`{"cards":[{"full":{"use_id":"one"},"projection":{}}]}`), "$.cards[0].full.family_id"},
		{"unknown", json.RawMessage(`{"private-source-output-secret":1}`), "unknown JSON field"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			publisher := &fakePublisher{store: store, verified: true}
			calls := 0
			model := completionFunc(func(_ context.Context, request memes.CompletionRequest) (memes.CompletionResponse, error) {
				calls++
				return memes.CompletionResponse{JSON: test.invalid}, nil
			})
			report, err := Sync(context.Background(), Catalog{Sources: []Source{testSource()}}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
			if !errors.Is(err, ErrIncomplete) || calls != 2 || len(report.Calls) != 2 || len(report.Errors) != 1 || publisher.calls != 0 || store.active != "" {
				t.Fatalf("unbounded or active invalid repair: %+v %v calls=%d", report, err, calls)
			}
			if !strings.Contains(report.Errors[0].Error, test.diagnostic) || strings.Contains(report.Errors[0].Error, "private-source-output-secret") {
				t.Fatalf("unsafe or unhelpful structure diagnostic: %q", report.Errors[0].Error)
			}
			for key := range store.records {
				if strings.HasPrefix(key, string(memestore.Card)+":") {
					t.Fatal("persisted invalid repair as a good card cache")
				}
			}
		})
	}
}
