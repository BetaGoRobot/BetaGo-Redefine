package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/tmc/langchaingo/schema"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

func TestTopicRecallTimeoutPreservesAnswerContext(t *testing.T) {
	oldRecall, oldHybrid := topicRecallDocsFn, topicHybridSearchFn
	t.Cleanup(func() { topicRecallDocsFn, topicHybridSearchFn = oldRecall, oldHybrid })
	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "capture"}[capture], func(t *testing.T) {
			parent := context.Background()
			wait := func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 3100*time.Millisecond {
					return errors.New("optional recall has no short deadline")
				}
				<-ctx.Done()
				return ctx.Err()
			}
			topicRecallDocsFn = func(ctx context.Context, _, _ string, _ int, _, _ string) ([]schema.Document, error) {
				return nil, wait(ctx)
			}
			topicHybridSearchFn = func(ctx context.Context, _ history.HybridSearchRequest, _ history.EmbeddingFunc) ([]*history.SearchResult, error) {
				return nil, wait(ctx)
			}
			started := time.Now()
			docs, err := recallTopicDocsForMode(parent, "chat", "你怎么看", 10, "", time.Now(), capture, nil)
			if !errors.Is(err, context.DeadlineExceeded) || len(docs) != 0 {
				t.Fatalf("stalled recall did not time out: docs=%v err=%v", docs, err)
			}
			if elapsed := time.Since(started); elapsed > 4*time.Second {
				t.Fatalf("optional recall blocked answer preparation for %v", elapsed)
			}
			if parent.Err() != nil {
				t.Fatalf("optional recall canceled the context needed for the answer: %v", parent.Err())
			}
		})
	}
}

func TestTopicRecallRespectsShorterParentDeadline(t *testing.T) {
	old := topicRecallDocsFn
	t.Cleanup(func() { topicRecallDocsFn = old })
	parent, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	want, _ := parent.Deadline()
	topicRecallDocsFn = func(ctx context.Context, _, _ string, _ int, _, _ string) ([]schema.Document, error) {
		if got, ok := ctx.Deadline(); !ok || got.After(want) {
			t.Fatalf("recall extended the parent deadline: got %v want %v", got, want)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err := recallTopicDocsForMode(parent, "chat", "query", 10, "", time.Now(), false, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("parent cancellation lost: %v", err)
	}
}

func TestRecallTopicDocsForModePreservesLegacyRetrieverCall(t *testing.T) {
	oldRecall := topicRecallDocsFn
	oldHybrid := topicHybridSearchFn
	defer func() {
		topicRecallDocsFn = oldRecall
		topicHybridSearchFn = oldHybrid
	}()

	var gotSuffix, gotQuery, gotStart, gotEnd string
	var gotK int
	wantDocs := []schema.Document{{PageContent: "legacy"}}
	topicRecallDocsFn = func(_ context.Context, suffix, query string, k int, start, end string) ([]schema.Document, error) {
		gotSuffix, gotQuery, gotK, gotStart, gotEnd = suffix, query, k, start, end
		return wantDocs, nil
	}
	topicHybridSearchFn = func(context.Context, history.HybridSearchRequest, history.EmbeddingFunc) ([]*history.SearchResult, error) {
		t.Fatal("legacy recall called HybridSearch")
		return nil, nil
	}

	got, err := recallTopicDocsForMode(
		context.Background(),
		"oc_chat",
		"query",
		10,
		"2026-07-01 00:00:00",
		time.Date(2026, 7, 29, 15, 0, 0, 123000000, time.FixedZone("UTC+8", 8*60*60)),
		false,
		nil,
	)
	if err != nil {
		t.Fatalf("recallTopicDocsForMode() error = %v", err)
	}
	if len(got) != 1 || got[0].PageContent != wantDocs[0].PageContent {
		t.Fatalf("docs = %#v, want %#v", got, wantDocs)
	}
	if gotSuffix != "oc_chat" || gotQuery != "query" || gotK != 10 ||
		gotStart != "2026-07-01 00:00:00" || gotEnd != "" {
		t.Fatalf(
			"legacy call = suffix %q query %q k %d start %q end %q",
			gotSuffix, gotQuery, gotK, gotStart, gotEnd,
		)
	}
}

func TestRecallTopicDocsForModeCaptureUsesCausalMessageIndexResults(t *testing.T) {
	oldRecall := topicRecallDocsFn
	oldHybrid := topicHybridSearchFn
	defer func() {
		topicRecallDocsFn = oldRecall
		topicHybridSearchFn = oldHybrid
	}()

	anchor := time.Date(2026, 7, 29, 15, 0, 0, 123000000, time.FixedZone("UTC+8", 8*60*60))
	topicRecallDocsFn = func(context.Context, string, string, int, string, string) ([]schema.Document, error) {
		t.Fatal("capture recall called legacy Retriever")
		return nil, nil
	}
	var gotReq history.HybridSearchRequest
	var gotEmbedding history.EmbeddingFunc
	topicHybridSearchFn = func(
		_ context.Context,
		req history.HybridSearchRequest,
		embedding history.EmbeddingFunc,
	) ([]*history.SearchResult, error) {
		gotReq, gotEmbedding = req, embedding
		return []*history.SearchResult{
			{
				MessageID: "om_exact", RawMessage: "exact page content",
				CreateTime:           "2026-07-29 15:00:00",
				CreateTimeV2:         "2026-07-29T15:00:00+08:00",
				CreateTimeUnixMillis: anchor.UnixMilli(),
				Score:                0.75,
			},
			{
				MessageID: "om_future", RawMessage: "future page content",
				CreateTime:           "2026-07-29 15:00:01",
				CreateTimeV2:         "2026-07-29T15:00:00+08:00",
				CreateTimeUnixMillis: anchor.UnixMilli() + 1,
				Score:                0.99,
			},
			{
				MessageID: "om_old_same_second", RawMessage: "old same-second page content",
				CreateTime:   "2026-07-29 15:00:00",
				CreateTimeV2: "2026-07-29T15:00:00+08:00",
				Score:        0.8,
			},
			{
				MessageID: "om_old_past_second", RawMessage: "old past page content",
				CreateTime:   "2026-07-29 14:59:59",
				CreateTimeV2: "2026-07-29T14:59:59+08:00",
				Score:        0.5,
			},
		}, nil
	}
	embedding := func(context.Context, string) ([]float32, model.Usage, error) {
		return []float32{0.1}, model.Usage{}, nil
	}

	got, err := recallTopicDocsForMode(
		context.Background(),
		"oc_chat",
		"query",
		10,
		"2026-07-01 00:00:00",
		anchor,
		true,
		embedding,
	)
	if err != nil {
		t.Fatalf("recallTopicDocsForMode() error = %v", err)
	}
	if !gotReq.MessageIndexOnly || gotReq.ChatID != "oc_chat" ||
		len(gotReq.QueryText) != 1 || gotReq.QueryText[0] != "query" ||
		gotReq.TopK != 10 || gotReq.CutoffTime != "2026-07-01 00:00:00" ||
		gotReq.EndTime != anchor.Format(time.RFC3339Nano) ||
		gotReq.CausalEndMillis != anchor.UnixMilli() {
		t.Fatalf("capture request = %#v", gotReq)
	}
	if gotEmbedding == nil {
		t.Fatal("capture request did not forward embedding function")
	}
	if len(got) != 2 {
		t.Fatalf("capture docs = %#v, want exact and old prior-second results", got)
	}
	if got[0].PageContent != "exact page content" || got[0].Score != 0.75 {
		t.Fatalf("capture doc content/score = %#v", got[0])
	}
	if got[0].Metadata["msg_id"] != "om_exact" ||
		got[0].Metadata["create_time"] != "2026-07-29 15:00:00" ||
		got[0].Metadata["create_time_v2"] != "2026-07-29T15:00:00+08:00" ||
		got[0].Metadata["create_time_unix_millis"] != anchor.UnixMilli() {
		t.Fatalf("capture doc metadata = %#v", got[0].Metadata)
	}
	if got[1].PageContent != "old past page content" ||
		got[1].Metadata["msg_id"] != "om_old_past_second" {
		t.Fatalf("legacy fallback doc = %#v", got[1])
	}
}
