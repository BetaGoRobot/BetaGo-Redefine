package memes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func groundedFixture(t *testing.T) (Request, []SourceFragment, KnowledgeSelection) {
	t.Helper()
	r, s, e := ValidateRequest(Request{RequestID: "h03", Dialogue: []Turn{{TurnID: "current", Speaker: "human", Text: "三年从钢铁雄心玩成海岛奇兵，损一句"}}})
	if e != nil {
		t.Fatal(e)
	}
	return r, s, KnowledgeSelection{Binding: KnowledgeBinding{Scope: "wiki", Generation: "g", CorpusHash: "c", ProjectionHash: "p"}, Anchors: []KnowledgeAnchor{{ID: "iron", TurnID: "current", Text: "钢铁雄心", StartRune: 3, EndRune: 7, EntryIDs: []string{"k1"}}, {ID: "sea", TurnID: "current", Text: "海岛奇兵", StartRune: 9, EndRune: 13, EntryIDs: []string{"k2"}}}, Entries: []KnowledgeEntry{{ID: "k1", Title: "钢铁雄心"}, {ID: "k2", Title: "海岛奇兵", Description: "源词条将其用于影射退守台湾后仍宣称反攻。"}}}
}

func TestGroundedQueriesResolveLiteralFromFrozenReferences(t *testing.T) {
	r, s, k := groundedFixture(t)
	for i := range k.Entries {
		k.Entries[i].Binding = k.Binding
	}
	h := GroundedHypothesis{ID: "h", Meaning: "可能是退守后的历史影射", CommunicationAct: "invited_teasing", SourceIDs: []int{s[0].SourceID}, AnchorIDs: []string{"sea"}, KnowledgeIDs: []string{"k2"}}
	a := GroundedAnalysis{Hypotheses: []GroundedHypothesis{h}, Queries: []GroundedQuery{{Origin: "literal", AnchorIDs: []string{"sea"}, Field: FieldKeys}, {Origin: "literal", AnchorIDs: []string{"sea"}, Text: "模型自报替代原文", Field: FieldKeys}, {Origin: "hypothesis_probe", HypothesisID: "h", SourceIDs: []int{s[0].SourceID}, Text: "败退后仍宣称反攻", Field: FieldRelation}}}
	plan, valid, warnings := CompileGroundedPlan(a, r, s, k)
	if len(valid) != 1 || len(plan.Queries) != 2 || plan.Queries[0].Text != "海岛奇兵" || len(warnings) != 1 {
		t.Fatalf("plan=%+v hypotheses=%+v warnings=%+v", plan, valid, warnings)
	}
	if valid[0].ID == h.ID || !strings.HasPrefix(valid[0].ID, "hypothesis_") {
		t.Fatal("model identifier exposed instead of opaque server reference")
	}
	bad := GroundedAnalysis{Queries: []GroundedQuery{{Origin: "knowledge_link", SourceIDs: []int{s[0].SourceID}, KnowledgeIDs: []string{"invented"}, Text: "反攻大陆", Field: FieldKeys}}}
	badPlan, _, badWarnings := CompileGroundedPlan(bad, r, s, k)
	if len(badPlan.Queries) != 0 || len(badWarnings) != 1 {
		t.Fatal("invented knowledge accepted")
	}
	if len(k.Anchors) != 2 || k.Anchors[0].Text != "钢铁雄心" {
		t.Fatal("query rejection modified prior anchors")
	}
}

func TestGroundedModelSchemaRejectsUnknownNullAndExcessivePlans(t *testing.T) {
	for _, raw := range []string{`{"hypotheses":[],"queries":[],"stop_reason":"","literal_source":"trust me"}`, `{"hypotheses":null,"queries":[],"stop_reason":""}`} {
		if _, err := decodeGroundedAnalysis([]byte(raw)); err == nil {
			t.Fatal("invalid schema accepted", raw)
		}
	}
	r, s, k := groundedFixture(t)
	queries := []GroundedQuery{}
	for i := 0; i < 8; i++ {
		queries = append(queries, GroundedQuery{Origin: "literal", AnchorIDs: []string{"sea"}, Field: FieldKeys})
	}
	p, _, w := CompileGroundedPlan(GroundedAnalysis{Queries: queries}, r, s, k)
	if len(p.Queries) > 3 || len(w) == 0 {
		t.Fatalf("budget unbounded %+v", p)
	}
}

func TestGroundedDecoderRejectsMalformedSiblingIndependentlyAndRetainsAudit(t *testing.T) {
	r, s, k := groundedFixture(t)
	for i := range k.Entries {
		k.Entries[i].Binding = k.Binding
	}
	a, err := decodeGroundedAnalysis([]byte(`{"hypotheses":[],"queries":[{"origin":"not_literal","source_ids":[],"anchor_ids":["sea"],"knowledge_ids":[],"hypothesis_id":"","field":"keys","text":"forged"},{"origin":"literal","source_ids":[],"anchor_ids":["sea"],"knowledge_ids":[],"hypothesis_id":"","field":"keys","text":""}],"stop_reason":"sufficient_knowledge"}`))
	if err != nil {
		t.Fatal(err)
	}
	p, _, warnings := CompileGroundedPlan(a, r, s, k)
	if len(p.Queries) != 1 || p.Queries[0].Text != "海岛奇兵" || len(warnings) != 1 || p.Grounded == nil || p.Grounded.StopReason != "sufficient_knowledge" || len(p.Grounded.RejectedInputs) != 1 || len(p.Grounded.Decisions) != 1 || p.Grounded.Decisions[0].Index != 1 || p.Grounded.Decisions[0].Proposal.Origin != "literal" {
		t.Fatalf("sibling/audit lost: %+v %+v", p, warnings)
	}
}

type groundedCompletion struct {
	input CompletionRequest
	fail  bool
}

func (m *groundedCompletion) Complete(_ context.Context, r CompletionRequest) (CompletionResponse, error) {
	m.input = r
	if m.fail {
		return CompletionResponse{}, errors.New("model unavailable")
	}
	return CompletionResponse{JSON: json.RawMessage(`{"hypotheses":[],"queries":[],"stop_reason":"serious_question"}`)}, nil
}

type groundedRetriever struct{}

func (groundedRetriever) Search(context.Context, QueryPlan) (RetrievalSnapshot, error) {
	return RetrievalSnapshot{}, errors.New("must not search")
}

func TestGroundedPlannerSeesKnowledgeAndKeepsItOnFailure(t *testing.T) {
	card := fixedCard("island")
	card.Name = "海岛奇兵"
	card.Description = "源词条影射退守台湾与反攻大陆。"
	card.Sources = []SourceMetadata{{Kind: "wiki", Scope: "wiki", Title: "海岛奇兵", URL: "https://example.org/sea"}}
	fp, _ := Fingerprint(card)
	record, e := CompileCard(card, Projection{CardFingerprint: fp, Relations: []Relation{{Kind: "contrast", Pattern: "退守与反攻的反差"}}})
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := CompileKnowledgeSnapshot(KnowledgeBinding{Scope: "wiki", Generation: "g", CorpusHash: "c", ProjectionHash: "p"}, []IndexRecord{record})
	if e != nil {
		t.Fatal(e)
	}
	for _, fail := range []bool{false, true} {
		model := &groundedCompletion{fail: fail}
		service, e := NewGroundedReferenceService(ReferenceDependencies{Model: model, Retriever: groundedRetriever{}}, ReferenceOptions{RequestTimeout: time.Second, StageTimeout: time.Second}, snapshot)
		if e != nil {
			t.Fatal(e)
		}
		result := service.Retrieve(context.Background(), Request{RequestID: "h", Dialogue: []Turn{{TurnID: "t", Speaker: "human", Text: "海岛奇兵雷达怎么升级？认真问游戏"}}})
		if result.ContextV2 == nil || len(result.ContextV2.Observations) != 1 || len(result.ContextV2.PriorKnowledge) != 1 {
			t.Fatalf("lost priors: %+v", result)
		}
		if !strings.Contains(string(model.input.InputJSON), "反攻大陆") {
			t.Fatal("planner called before providing source knowledge")
		}
		if result.Metadata.SemanticCalls != 1 {
			t.Fatal("unbounded planning calls")
		}
	}
}
