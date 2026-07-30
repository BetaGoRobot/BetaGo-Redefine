package evaluationindex

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
)

func TestStoreUpsertsExactEpisodeDocument(t *testing.T) {
	backend := &indexBackendFake{}
	owner, index := evaluationIndexTestTenant(t)
	store, err := NewStoreWithBackend(owner, index, backend)
	if err != nil {
		t.Fatalf("NewStoreWithBackend() error = %v", err)
	}
	snapshot := evaluationSnapshotFixture()
	snapshot.normalize()

	if err := store.Upsert(context.Background(), snapshot); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if backend.upsertIndex != index ||
		backend.upsertID != snapshot.TenantID+":"+snapshot.EpisodeID {
		t.Fatalf(
			"upsert target = %q/%q",
			backend.upsertIndex,
			backend.upsertID,
		)
	}
	got, ok := backend.upsertData.(EvaluationSnapshot)
	if !ok || !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("upsert document = %#v", backend.upsertData)
	}
}

func TestStoreSearchBuildsTimeChatCohortQualityFilters(t *testing.T) {
	backend := &indexBackendFake{}
	owner, index := evaluationIndexTestTenant(t)
	store, err := NewStoreWithBackend(owner, index, backend)
	if err != nil {
		t.Fatalf("NewStoreWithBackend() error = %v", err)
	}
	from := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	needsReview := true
	backend.documents = []json.RawMessage{mustJSON(t, evaluationSnapshotFixture())}

	got, err := store.Search(context.Background(), EpisodeFilter{
		CohortID: "cohort-1", ChatID: "chat-1", From: from, To: to,
		Disagreement: "reply", FeedbackType: "correction", NeedsReview: &needsReview,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 || got[0].EpisodeID != "episode-1" {
		t.Fatalf("Search() = %#v", got)
	}
	if backend.searchIndex != index {
		t.Fatalf("search index = %q", backend.searchIndex)
	}
	encoded, err := json.Marshal(backend.searchQuery)
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}
	var query struct {
		Size  int                            `json:"size"`
		Sort  []map[string]map[string]string `json:"sort"`
		Query struct {
			Bool struct {
				Filter []map[string]any `json:"filter"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(encoded, &query); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if query.Size != DefaultSearchSize || len(query.Sort) != 2 ||
		len(query.Query.Bool.Filter) != 7 {
		t.Fatalf("query = %s", encoded)
	}
	assertTermFilter(t, query.Query.Bool.Filter, "tenant_id", owner.ID)
	assertTermFilter(t, query.Query.Bool.Filter, "cohort_id", "cohort-1")
	assertTermFilter(t, query.Query.Bool.Filter, "chat_id", "chat-1")
	assertTermFilter(t, query.Query.Bool.Filter, "disagreements", "reply")
	assertTermFilter(t, query.Query.Bool.Filter, "feedback_types", "correction")
	assertTermFilter(t, query.Query.Bool.Filter, "needs_review", true)
	assertRangeFilter(t, query.Query.Bool.Filter, "anchor_at", from, to)
}

func TestStoreRejectsInvalidSnapshotAndFilter(t *testing.T) {
	owner, index := evaluationIndexTestTenant(t)
	store, err := NewStoreWithBackend(owner, index, &indexBackendFake{})
	if err != nil {
		t.Fatalf("NewStoreWithBackend() error = %v", err)
	}
	snapshot := evaluationSnapshotFixture()
	snapshot.EpisodeID = ""
	if err := store.Upsert(context.Background(), snapshot); err == nil {
		t.Fatal("Upsert(invalid snapshot) error = nil")
	}
	if _, err := store.Search(context.Background(), EpisodeFilter{
		From: time.Now(), To: time.Now().Add(-time.Hour),
	}); err == nil {
		t.Fatal("Search(inverted range) error = nil")
	}
	if _, err := NewStoreWithBackend(owner, index, nil); err == nil {
		t.Fatal("NewStoreWithBackend(nil backend) error = nil")
	}
	other, _ := tenant.New("app-other", "bot-other")
	otherSnapshot := evaluationSnapshotFixture()
	otherSnapshot.TenantID = other.ID
	otherSnapshot.AppID = other.AppID
	otherSnapshot.BotOpenID = other.BotOpenID
	if err := store.Upsert(context.Background(), otherSnapshot); err == nil {
		t.Fatal("Upsert(other tenant snapshot) error = nil")
	}
	if _, err := NewStoreWithBackend(
		owner,
		DefaultIndexAlias,
		&indexBackendFake{},
	); err == nil {
		t.Fatal("NewStoreWithBackend(shared alias) error = nil")
	}
}

func evaluationIndexTestTenant(t *testing.T) (tenant.Tenant, string) {
	t.Helper()
	owner, err := tenant.New("app-1", "bot-1")
	if err != nil {
		t.Fatal(err)
	}
	index, err := owner.IndexAlias(DefaultIndexAlias)
	if err != nil {
		t.Fatal(err)
	}
	return owner, index
}

type indexBackendFake struct {
	upsertIndex string
	upsertID    string
	upsertData  any
	searchIndex string
	searchQuery map[string]any
	documents   []json.RawMessage
}

func (f *indexBackendFake) Upsert(
	_ context.Context,
	index string,
	id string,
	data any,
) error {
	f.upsertIndex = index
	f.upsertID = id
	f.upsertData = data
	return nil
}

func (f *indexBackendFake) Search(
	_ context.Context,
	index string,
	query map[string]any,
) ([]json.RawMessage, error) {
	f.searchIndex = index
	f.searchQuery = query
	return append([]json.RawMessage(nil), f.documents...), nil
}

func evaluationSnapshotFixture() EvaluationSnapshot {
	anchor := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	postEnd := anchor.Add(10 * time.Minute)
	owner, _ := tenant.New("app-1", "bot-1")
	return EvaluationSnapshot{
		SchemaVersion: "conversation_evaluation.v1",
		TenantID:      owner.ID, AppID: owner.AppID, BotOpenID: owner.BotOpenID,
		EpisodeID: "episode-1", CohortID: "cohort-1", ChatID: "chat-1",
		RunID: "run-1", AnchorEventID: "event-1", AnchorMessageID: "message-1",
		TopicID: "topic-1", Status: "judged", ServingLane: "control",
		AnchorAt: anchor, PostWindowEnd: &postEnd,
		LateFeedbackUntil: anchor.Add(24 * time.Hour),
		Disagreements:     []string{"reply"}, FeedbackTypes: []string{"correction"},
		NeedsReview: true,
		PreMessages: []MessageSnapshot{{
			MessageID: "message-pre", Content: "before", OccurredAt: anchor.Add(-time.Minute),
		}},
		AnchorMessage: MessageSnapshot{
			MessageID: "message-1", Content: "anchor", OccurredAt: anchor,
		},
		PostMessages: []MessageSnapshot{{
			MessageID: "message-post", Content: "after", OccurredAt: anchor.Add(time.Minute),
		}},
		Control: LaneSnapshot{
			JoinDecision: "join", TopicRelation: "related", ReplyText: "reply A",
			ContextText: []string{"context A"},
		},
		Candidate: LaneSnapshot{
			JoinDecision: "skip", TopicRelation: "unrelated",
			ContextText: []string{"context B"},
		},
		LatestJudgments: []JudgmentSnapshot{{
			Source: "conversation_evaluation_judge", Version: 1,
			Winner: "control", Rationale: "A is better", Confidence: 90,
			NeedsReview: true, CreatedAt: postEnd.Add(time.Minute),
		}},
		FullSnapshot: json.RawMessage(`{"episode":{"id":"episode-1"}}`),
		UpdatedAt:    postEnd.Add(time.Minute),
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}

func assertTermFilter(
	t *testing.T,
	filters []map[string]any,
	field string,
	want any,
) {
	t.Helper()
	for _, filter := range filters {
		term, ok := filter["term"].(map[string]any)
		if ok && reflect.DeepEqual(term[field], want) {
			return
		}
	}
	t.Fatalf("term filter %q=%#v missing from %#v", field, want, filters)
}

func assertRangeFilter(
	t *testing.T,
	filters []map[string]any,
	field string,
	from time.Time,
	to time.Time,
) {
	t.Helper()
	for _, filter := range filters {
		ranges, ok := filter["range"].(map[string]any)
		if !ok {
			continue
		}
		value, ok := ranges[field].(map[string]any)
		if ok && value["gte"] == from.Format(time.RFC3339Nano) &&
			value["lt"] == to.Format(time.RFC3339Nano) {
			return
		}
	}
	t.Fatalf("range filter %q missing from %#v", field, filters)
}
