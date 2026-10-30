package memes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type referenceModel struct {
	outputs  []string
	requests []CompletionRequest
	delay    time.Duration
}

func (m *referenceModel) Complete(ctx context.Context, request CompletionRequest) (CompletionResponse, error) {
	m.requests = append(m.requests, request)
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return CompletionResponse{}, ctx.Err()
		}
	}
	if len(m.outputs) == 0 {
		return CompletionResponse{}, errors.New("unexpected model call")
	}
	raw := m.outputs[0]
	m.outputs = m.outputs[1:]
	return CompletionResponse{JSON: json.RawMessage(raw), Metadata: CallMetadata{Model: "test-model", InputTokens: 12, OutputTokens: 8}}, nil
}

type referenceRetriever struct {
	calls    int
	fail     bool
	empty    bool
	degraded bool
	card     UseCard
}

func (r *referenceRetriever) Search(ctx context.Context, plan QueryPlan) (RetrievalSnapshot, error) {
	r.calls++
	if r.fail {
		return RetrievalSnapshot{}, errors.New("remote unavailable")
	}
	queries := make([]QueryResult, 0, len(plan.Queries))
	for _, q := range plan.Queries {
		channel := ChannelResult{Channel: "vector", Status: "ok"}
		if !r.empty {
			channel.Hits = []RawHit{{UseID: r.card.UseID, Full: r.card, Score: 100, RelationKinds: []string{"benefit_cost"}}}
		}
		channels := []ChannelResult{channel}
		if r.degraded && q.Field != FieldDescription {
			channels = append(channels, ChannelResult{Channel: "fulltext", Status: "error", Error: "fulltext unavailable"})
		}
		queries = append(queries, QueryResult{Query: q, Channels: channels})
	}
	return FuseRetrieval(plan, queries)
}
func referenceDeps() (*referenceModel, *referenceRetriever) {
	return &referenceModel{outputs: []string{`{"relations":[{"kind":"benefit_cost","pattern":"同一变化使A获得收益并承受损失","evidence":[{"source_id":0,"state":"asserted"}]}],"emotion_chain":[],"keys":[]}`}}, &referenceRetriever{card: testCard("two_sides.change")}
}
func TestReferenceServiceOnlyQueriesAndReturnsSourceCards(t *testing.T) {
	request, _ := testRequest(t)
	model, retriever := referenceDeps()
	service, err := NewReferenceService(ReferenceDependencies{Model: model, Retriever: retriever}, ReferenceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := service.Retrieve(context.Background(), request)
	if result.Status != ReferenceStatusReady || len(result.Cards) != 1 || len(model.requests) != 1 || retriever.calls != 1 {
		t.Fatalf("reference flow failed %+v", result)
	}
	if result.Cards[0].ReviewStatus != "pending" {
		t.Fatal("pending source card was gated or silently approved")
	}
	if model.requests[0].Stage != StageQuery || strings.Contains(string(model.requests[0].InputJSON), "score") {
		t.Fatal("query call included retrieval ranking")
	}
	if result.Metadata.SemanticCalls != 1 || result.Metadata.RepairCalls != 0 || len(result.Sources) == 0 || result.Retrieval.Fingerprint == "" {
		t.Fatal("missing retrieval evidence/call diagnostics")
	}
}
func TestReferenceServiceHonestEmptyOriginalOnlyAndFailure(t *testing.T) {
	for _, kind := range []string{"empty", "original_only", "failed", "degraded"} {
		t.Run(kind, func(t *testing.T) {
			request, _ := testRequest(t)
			model, retriever := referenceDeps()
			status, reason := ReferenceStatusEmpty, ReasonNoCandidates
			switch kind {
			case "empty":
				retriever.empty = true
			case "original_only":
				model.outputs = []string{`{"relations":[],"emotion_chain":[],"keys":[]}`}
				status, reason = ReferenceStatusReady, ""
			case "failed":
				retriever.fail = true
				status, reason = ReferenceStatusError, ReasonRetrievalFailed
			case "degraded":
				retriever.degraded = true
				status, reason = ReferenceStatusReady, ""
			}
			service, _ := NewReferenceService(ReferenceDependencies{model, retriever}, ReferenceOptions{})
			got := service.Retrieve(context.Background(), request)
			if got.Status != status || got.Reason != reason {
				t.Fatalf("%+v", got)
			}
			if kind == "degraded" && (!got.Degraded || len(got.Warnings) == 0) {
				t.Fatal("partial failure hidden")
			}
			if kind == "original_only" && (retriever.calls != 1 || len(got.Plan.Queries) != 1 || got.Plan.Queries[0].Field != FieldDescription) {
				t.Fatal("empty semantic analysis lost original-dialogue retrieval")
			}
		})
	}
}
func TestReferenceServiceAllowsOneStructureRepairAndNoSemanticRepair(t *testing.T) {
	request, _ := testRequest(t)
	model, retriever := referenceDeps()
	model.outputs = append([]string{`{"unknown":1}`}, model.outputs...)
	service, _ := NewReferenceService(ReferenceDependencies{model, retriever}, ReferenceOptions{})
	got := service.Retrieve(context.Background(), request)
	if got.Status != ReferenceStatusReady || len(model.requests) != 2 || !model.requests[1].Repair || got.Metadata.RepairCalls != 1 {
		t.Fatalf("one generic repair failed %+v", got)
	}
	model, retriever = referenceDeps()
	model.outputs = []string{`{}`, `{}`, `{"relations":[],"emotion_chain":[],"keys":[]}`}
	service, _ = NewReferenceService(ReferenceDependencies{model, retriever}, ReferenceOptions{})
	got = service.Retrieve(context.Background(), request)
	if got.Status != ReferenceStatusError || got.Reason != ReasonContractInvalid || len(model.requests) != 2 || retriever.calls != 0 {
		t.Fatal("structure repair budget bypassed")
	}
	model, retriever = referenceDeps()
	model.outputs = []string{`{"relations":[{"kind":"contrast","pattern":"A与B形成对比","evidence":[{"source_id":999,"state":"asserted"}]}],"emotion_chain":[],"keys":[]}`}
	service, _ = NewReferenceService(ReferenceDependencies{model, retriever}, ReferenceOptions{})
	got = service.Retrieve(context.Background(), request)
	if got.Status != ReferenceStatusError || got.Reason != ReasonContractInvalid || len(model.requests) != 1 || retriever.calls != 0 {
		t.Fatal("bad core evidence repaired as if it were formatting")
	}
}
func TestReferenceServiceTimeoutAndOptions(t *testing.T) {
	request, _ := testRequest(t)
	model, retriever := referenceDeps()
	for _, opts := range []ReferenceOptions{{RequestTimeout: -time.Second}, {RequestTimeout: 121 * time.Second}, {StageTimeout: 61 * time.Second}, {RequestTimeout: time.Second, StageTimeout: 2 * time.Second}} {
		if _, err := NewReferenceService(ReferenceDependencies{model, retriever}, opts); err == nil {
			t.Fatal("bad timeout silently accepted")
		}
	}
	model.delay = time.Second
	service, _ := NewReferenceService(ReferenceDependencies{model, retriever}, ReferenceOptions{RequestTimeout: 30 * time.Millisecond, StageTimeout: 10 * time.Millisecond})
	got := service.Retrieve(context.Background(), request)
	if got.Status != ReferenceStatusError || got.Reason != ReasonTimeout || retriever.calls != 0 || len(model.requests) != 1 {
		t.Fatal("timeout called later stages", got)
	}
	if got.Metadata.RequestTimeoutMS != 30 || got.Metadata.StageTimeoutMS != 10 {
		t.Fatal("effective timing missing")
	}
}
func TestWikiEncodingBindsOriginalSourceAndMarksConditionsPending(t *testing.T) {
	card := testCard("two_sides.change")
	card.ReviewStatus = "approved"
	card.Description = "model rewritten source"
	encoded := EncodedWiki{Cards: []EncodedUse{{Full: card, Projection: Projection{Relations: []Relation{{Kind: "benefit_cost", Pattern: "同一变化带来收益和损失"}}}}}}
	source := WikiSource{ID: "wiki-1", Title: "好消息坏消息", URL: "https://example.com/wiki/1", Description: "原始Wiki定义和用法，保留未改写。"}
	records, err := CompileWikiEncoding(source, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Full.Description != source.Description || records[0].Full.ReviewStatus != "pending" || records[0].Full.Sources[0].URL != source.URL {
		t.Fatal("source was replaced or inferred conditions marked approved")
	}
	fp, _ := Fingerprint(records[0].Full)
	if records[0].Projection.CardFingerprint != fp {
		t.Fatal("source binding failed to regenerate projection fingerprint")
	}
	if SystemPrompt(StageEncode) == "" || !json.Valid(StageSchema(StageEncode)) {
		t.Fatal("missing offline encoder stage contract")
	}
}
func TestFingerprintSurvivesJSONBNormalization(t *testing.T) {
	a := json.RawMessage(`{"b":1e2,"a":2}`)
	b := json.RawMessage(`{"a":2,"b":100.00}`)
	fa, _ := Fingerprint(a)
	fb, _ := Fingerprint(b)
	if fa != fb {
		t.Fatal("JSONB formatting changed retrieval fingerprint")
	}
}
