package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/tmc/langchaingo/schema"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

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
				MessageID: "om_past", RawMessage: "safe page content",
				CreateTime:   "2026-07-29 15:00:00",
				CreateTimeV2: "2026-07-29T15:00:00+08:00",
				Score:        0.75,
			},
			{
				MessageID: "om_future", RawMessage: "future page content",
				CreateTime:   "2026-07-29 15:00:01",
				CreateTimeV2: "2026-07-29T15:00:00.124+08:00",
				Score:        0.99,
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
		gotReq.EndTime != anchor.Format(time.RFC3339Nano) {
		t.Fatalf("capture request = %#v", gotReq)
	}
	if gotEmbedding == nil {
		t.Fatal("capture request did not forward embedding function")
	}
	if len(got) != 1 {
		t.Fatalf("capture docs = %#v, want only causal result", got)
	}
	if got[0].PageContent != "safe page content" || got[0].Score != 0.75 {
		t.Fatalf("capture doc content/score = %#v", got[0])
	}
	if got[0].Metadata["msg_id"] != "om_past" ||
		got[0].Metadata["create_time"] != "2026-07-29 15:00:00" ||
		got[0].Metadata["create_time_v2"] != "2026-07-29T15:00:00+08:00" {
		t.Fatalf("capture doc metadata = %#v", got[0].Metadata)
	}
}
