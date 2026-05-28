package llmusage

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	rows []UsageRecordRow
	err  error
}

func (f *fakeStore) CreateUsageRecord(_ context.Context, row *UsageRecordRow) error {
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, *row)
	return nil
}

func TestRecorderWritesOfflineRecordWithBuckets(t *testing.T) {
	store := &fakeStore{}
	recorder := NewRecorderWithStore(store)
	createdAt := time.Date(2026, 5, 28, 9, 10, 42, 0, time.UTC)

	err := recorder.Record(context.Background(), Record{
		Scope: Scope{
			ChatID:     " oc_chat ",
			ChatName:   " Chat Name ",
			OpenID:     " ou_user ",
			UserName:   " Alice ",
			SourceType: SourceTypeUser,
			Source:     "chat",
		},
		Provider:         "ark",
		Model:            "ep-test",
		Kind:             KindResponses,
		Status:           StatusSuccess,
		PromptTokens:     11,
		CompletionTokens: 7,
		TotalTokens:      18,
		ResponseID:       "resp_1",
		TraceID:          "trace_1",
		Error:            "",
		CreatedAt:        createdAt,
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if len(store.rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(store.rows))
	}
	row := store.rows[0]
	if row.BucketMinute != createdAt.Truncate(time.Minute) {
		t.Fatalf("BucketMinute = %s, want %s", row.BucketMinute, createdAt.Truncate(time.Minute))
	}
	if row.BucketHour != createdAt.Truncate(time.Hour) {
		t.Fatalf("BucketHour = %s, want %s", row.BucketHour, createdAt.Truncate(time.Hour))
	}
	if row.BucketDay != time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("BucketDay = %s, want 2026-05-28", row.BucketDay)
	}
	if row.ChatID != "oc_chat" || row.ChatName != "Chat Name" {
		t.Fatalf("chat fields = %q/%q", row.ChatID, row.ChatName)
	}
	if row.OpenID != "ou_user" || row.UserName != "Alice" {
		t.Fatalf("user fields = %q/%q", row.OpenID, row.UserName)
	}
	if row.PromptTokens != 11 || row.CompletionTokens != 7 || row.TotalTokens != 18 {
		t.Fatalf("tokens = %d/%d/%d", row.PromptTokens, row.CompletionTokens, row.TotalTokens)
	}
	if row.Provider != "ark" || row.Model != "ep-test" || row.Kind != string(KindResponses) || row.Status != string(StatusSuccess) {
		t.Fatalf("record identity fields = %+v", row)
	}
	if row.ResponseID != "resp_1" || row.TraceID != "trace_1" {
		t.Fatalf("trace fields = %q/%q", row.ResponseID, row.TraceID)
	}
}

func TestRecorderAllowsNilStore(t *testing.T) {
	recorder := NewRecorderWithStore(nil)
	if err := recorder.Record(context.Background(), Record{
		Scope:     Scope{SourceType: SourceTypeBackground, Source: "chunking"},
		Provider:  "ark",
		Model:     "ep-test",
		Kind:      KindEmbedding,
		Status:    StatusUsageMissing,
		CreatedAt: time.Date(2026, 5, 28, 9, 10, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Record() with nil store error = %v", err)
	}
}

func TestRecorderReturnsStoreError(t *testing.T) {
	store := &fakeStore{err: errors.New("insert failed")}
	recorder := NewRecorderWithStore(store)

	err := recorder.Record(context.Background(), Record{
		Scope:     Scope{SourceType: SourceTypeSystem, Source: "test"},
		Provider:  "ark",
		Model:     "ep-test",
		Kind:      KindResponses,
		Status:    StatusError,
		CreatedAt: time.Date(2026, 5, 28, 9, 10, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("Record() error is nil, want store error")
	}
}
