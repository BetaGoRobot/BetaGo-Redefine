package memes

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDescriptionQueryUsesOriginalDialogueWithEmptyAnalysis(t *testing.T) {
	request, fragments, err := ValidateRequest(Request{RequestID: "description", Dialogue: []Turn{{TurnID: "1", Speaker: "a", Text: "先别拆门。"}, {TurnID: "2", Speaker: "b", Text: "我只是说可能把钥匙弄丢了。"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := CompileQueryPlan(QueryAnalysis{}, request, fragments)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Queries) != 1 || plan.Queries[0].Field != FieldDescription || plan.Queries[0].Text != "先别拆门。\n我只是说可能把钥匙弄丢了。" || len(plan.Queries[0].ElementIndices) != 0 {
		t.Fatalf("original dialogue query missing or rewritten: %+v", plan)
	}
	empty, err := CompileQueryPlan(QueryAnalysis{}, Request{}, nil)
	if err != nil || len(empty.Queries) != 0 {
		t.Fatal("description query invented original text", empty, err)
	}
}

func TestDescriptionQueryKeepsWholeRecentTurnsWithinInputBudgets(t *testing.T) {
	request, fragments, err := ValidateRequest(Request{RequestID: "description", Dialogue: []Turn{{TurnID: "1", Speaker: "a", Text: strings.Repeat("字", MaxInputRunes-1)}, {TurnID: "2", Speaker: "b", Text: "否"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := CompileQueryPlan(QueryAnalysis{}, request, fragments)
	if err != nil {
		t.Fatal(err)
	}
	q := plan.Queries[0]
	if q.Text != "否" || len(q.Text) > MaxInputBytes || utf8.RuneCountInString(q.Text) > MaxInputRunes {
		t.Fatalf("query split an original turn or exceeded budget: %d bytes, %d runes", len(q.Text), utf8.RuneCountInString(q.Text))
	}
}

func TestDescriptionOnlyVectorRetrievalReturnsBoundedReferences(t *testing.T) {
	request, _ := testRequest(t)
	model, retriever := referenceDeps()
	model.outputs = []string{`{"relations":[],"emotion_chain":[],"keys":[]}`}
	service, err := NewReferenceService(ReferenceDependencies{model, retriever}, ReferenceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := service.Retrieve(context.Background(), request)
	if result.Status != ReferenceStatusReady || len(result.Cards) != 1 || len(result.Plan.Queries) != 1 || result.Plan.Queries[0].Field != FieldDescription || result.Degraded || result.Metadata.SemanticCalls != 1 {
		t.Fatalf("description-only reference retrieval failed: %+v", result)
	}
	if len(result.Retrieval.Fields) != 1 || result.Retrieval.Fields[0].Field != FieldDescription || len(result.Retrieval.RawQueries[0].Channels) != 1 {
		t.Fatal("description vector fabricated another retrieval lane", result.Retrieval)
	}
}

func TestDescriptionQueryBudgetAndFinalPoolBudget(t *testing.T) {
	plan := QueryPlan{Queries: []SearchQuery{{ID: "d1", Field: FieldDescription, Text: "original"}}}
	hits := []RawHit{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		hits = append(hits, RawHit{UseID: id, Full: fixedCard(id), Score: 1})
	}
	result := QueryResult{Query: plan.Queries[0], Channels: []ChannelResult{{Channel: "vector", Status: "ok", Hits: hits}}}
	snapshot, err := FuseRetrieval(plan, []QueryResult{result})
	if err != nil || len(snapshot.Candidates) != CandidateBudget {
		t.Fatalf("description pool budget failed: %+v %v", snapshot, err)
	}
	if _, err = ValidateRetrievalSnapshot(snapshot); err != nil {
		t.Fatal("description snapshot derivation failed", err)
	}
	plan.Queries = append(plan.Queries, SearchQuery{ID: "d2", Field: FieldDescription, Text: "another"})
	if _, err = FuseRetrieval(plan, nil); err == nil {
		t.Fatal("description query budget was enlarged")
	}
}

func TestSevenQueryBudgetIncludesOneDescriptionQuery(t *testing.T) {
	plan := QueryPlan{}
	for _, field := range []Field{FieldRelation, FieldEmotion, FieldKeys} {
		for _, suffix := range []string{"a", "b"} {
			plan.Queries = append(plan.Queries, SearchQuery{ID: string(field) + suffix, Field: field, Text: suffix})
		}
	}
	plan.Queries = append(plan.Queries, SearchQuery{ID: "description", Field: FieldDescription, Text: "original"})
	results := make([]QueryResult, 0, len(plan.Queries))
	for _, q := range plan.Queries {
		results = append(results, QueryResult{Query: q, Channels: []ChannelResult{{Channel: "vector", Status: "ok"}}})
	}
	snapshot, err := FuseRetrieval(plan, results)
	if err != nil || snapshot.SuccessfulQueries != 7 || snapshot.Degraded {
		t.Fatalf("valid seven-query plan rejected: %+v %v", snapshot, err)
	}
	plan.Queries = append(plan.Queries, SearchQuery{ID: "extra", Field: FieldKeys, Text: "extra"})
	if _, err = FuseRetrieval(plan, nil); err == nil {
		t.Fatal("total query budget was enlarged")
	}
}

func TestDescriptionVectorFusesWithSemanticFieldsWithoutChangingFiveCardBudget(t *testing.T) {
	plan := QueryPlan{Queries: []SearchQuery{
		{ID: "r", Field: FieldRelation, Text: "contrast", RelationKinds: []string{"contrast"}},
		{ID: "k", Field: FieldKeys, Text: "literal"},
		{ID: "e", Field: FieldEmotion, Text: "joy"},
		{ID: "d", Field: FieldDescription, Text: "original dialogue"},
	}}
	results := []QueryResult{}
	for i, q := range plan.Queries {
		ids := [][]string{{"a", "b"}, {"c"}, {"d"}, {"e"}}[i]
		hits := []RawHit{}
		for rank, id := range ids {
			hits = append(hits, RawHit{UseID: id, Full: fixedCard(id), Score: float64(10 - rank), RelationKinds: []string{"contrast"}})
		}
		results = append(results, QueryResult{Query: q, Channels: []ChannelResult{{Channel: "vector", Status: "ok", Hits: hits}}})
	}
	snapshot, err := FuseRetrieval(plan, results)
	if err != nil || snapshot.Degraded || len(snapshot.Candidates) != CandidateBudget || len(snapshot.Fields) != 4 {
		t.Fatalf("four-field fusion failed: %+v %v", snapshot, err)
	}
	results[3].Channels = append(results[3].Channels, ChannelResult{Channel: "fulltext", Status: "ok"})
	if _, err = FuseRetrieval(plan, results); err == nil {
		t.Fatal("description search accepted a non-vector retrieval lane")
	}
}
