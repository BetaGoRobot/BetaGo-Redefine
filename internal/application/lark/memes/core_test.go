package memes

import (
	"strings"
	"testing"
)

func testRequest(t *testing.T) (Request, []SourceFragment) {
	t.Helper()
	r, s, err := ValidateRequest(Request{RequestID: "r1", Dialogue: []Turn{{TurnID: "t1", Speaker: "alice", Text: "宽带升级后网速快了，但公网 IPv4 地址没了。"}}})
	if err != nil {
		t.Fatal(err)
	}
	return r, s
}
func testCard(id string) UseCard {
	return UseCard{UseID: id, FamilyID: "two_sides", Name: "好坏消息", ReviewStatus: "pending", InputRelation: "同一变化带来收益和损失", Requirements: []Requirement{{ID: "benefit", AnyOf: []string{"实际收益"}}, {ID: "cost", AnyOf: []string{"实际损失"}}}, ResponseAct: "指出好坏两面", Roles: map[string]Role{"change": {Kind: "event", Meaning: "同一变化"}}, Stance: Stance{Voice: "bot_comment", ReplyTarget: "conversation", EvaluationTarget: "change"}, Template: "好消息是{benefit}，坏消息是{cost}。", Anchors: []string{"好消息是", "坏消息是"}, Slots: map[string]Slot{"benefit": {Meaning: "收益", ValueSource: "context", BindTo: "change", MaxChars: 24, Form: "text"}, "cost": {Meaning: "损失", ValueSource: "context", BindTo: "change", MaxChars: 24, Form: "text"}}, Sources: []SourceMetadata{{Kind: "constructed", Scope: "结构示例"}}}
}
func fixedCard(id string) UseCard {
	c := testCard(id)
	c.Template = "还有这种操作？"
	c.Anchors = []string{"这种操作"}
	c.Slots = map[string]Slot{}
	return c
}
func refs(id int) []Evidence { return []Evidence{{SourceID: id, State: Asserted}} }
func TestRequestAndEvidencePreserveOriginal(t *testing.T) {
	r, s := testRequest(t)
	if r.MaxChars != 64 || len(s) == 0 {
		t.Fatal("request defaults/fragments missing")
	}
	for _, f := range s {
		if string([]rune(r.Dialogue[0].Text)[f.StartRune:f.EndRune]) != f.Quote {
			t.Fatal("fragment is not an exact original slice")
		}
	}
	if err := ValidateEvidence(refs(0), s, r.Dialogue, false); err != nil {
		t.Fatal(err)
	}
	s[0].Quote = "模型改写"
	if ValidateEvidence(refs(0), s, r.Dialogue, false) == nil {
		t.Fatal("rewritten evidence accepted")
	}
	for _, r := range []Request{{RequestID: "x", Dialogue: []Turn{{TurnID: "x", Speaker: "a", Text: strings.Repeat("文", 8193)}}}, {RequestID: "x", MaxChars: 121, Dialogue: []Turn{{TurnID: "x", Speaker: "a", Text: "x"}}}} {
		if _, _, err := ValidateRequest(r); err == nil {
			t.Fatal("oversize accepted")
		}
	}
}
func TestStrictJSONRejectsDuplicateUnknownAndTrailing(t *testing.T) {
	for _, raw := range []string{`{"relations":[],"relations":[],"emotion_chain":[],"keys":[]}`, `{"relations":[],"emotion_chain":[],"keys":[],"answer":"leak"}`, `{"relations":[],"emotion_chain":[],"keys":[]} {}`} {
		var q QueryAnalysis
		if DecodeStrict([]byte(raw), &q) == nil {
			t.Fatal("invalid JSON accepted:", raw)
		}
	}
}
func TestQueryOptionalWarningsAndCoreEvidenceFailure(t *testing.T) {
	r, s := testRequest(t)
	q := QueryAnalysis{Relations: []Relation{{Kind: "benefit_cost", Pattern: "同一变化使A收益并失去B", Evidence: refs(0)}}, EmotionChain: []EmotionChain{{Subject: "alice", States: []string{"happy"}, Evidence: refs(999)}}, Keys: []Key{{Text: "光纤", Evidence: refs(0)}}}
	p, err := CompileQueryPlan(q, r, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Queries) != 2 || p.Queries[1].Field != FieldDescription || len(p.Warnings) != 2 {
		t.Fatalf("optional fields not isolated: %+v", p)
	}
	q.Relations[0].Evidence = refs(999)
	if _, err := CompileQueryPlan(q, r, s); err == nil {
		t.Fatal("invalid core relation accepted")
	}
}
func TestCardProjectionFingerprintAndEncodingContract(t *testing.T) {
	c := fixedCard("fixed")
	if err := ValidateCard(c); err != nil {
		t.Fatal(err)
	}
	fp, _ := Fingerprint(c)
	p := Projection{CardFingerprint: fp, Relations: []Relation{{Kind: "benefit_cost", Pattern: "同一变化使A收益并失去B"}}}
	idx, err := CompileCard(c, p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(idx.Relation, c.Template) {
		t.Fatal("answer leaked into projection")
	}
	c.Name = "changed"
	if _, err := CompileCard(c, p); err == nil {
		t.Fatal("stale projection accepted")
	}
	c.Template = "{value!s}"
	if err := ValidateCard(c); err != nil {
		t.Fatal("optional template hint unexpectedly validated as a renderer contract", err)
	}

}
func TestFusionTiesDedupEmptyDenominatorAndConflicts(t *testing.T) {
	h := RankUses([]RankedHit{{UseID: "c", Score: 0.5}, {UseID: "b", Score: 1}, {UseID: "a", Score: 1}, {UseID: "a", Score: 0.1}})
	if len(h) != 3 || h[0].Rank != 1 || h[1].Rank != 1 || h[2].Rank != 3 {
		t.Fatalf("wrong competition ranks: %+v", h)
	}
	plan := QueryPlan{Queries: []SearchQuery{{ID: "q1", Field: FieldRelation, Text: "relation"}, {ID: "q2", Field: FieldRelation, Text: "empty"}}}
	a := fixedCard("a")
	hit := RawHit{UseID: "a", Score: 1, Full: a}
	q1 := QueryResult{Query: plan.Queries[0], Status: "ok", Channels: []ChannelResult{{Channel: "vector", Status: "ok", Hits: []RawHit{hit}}}}
	q2 := QueryResult{Query: plan.Queries[1], Status: "ok", Channels: []ChannelResult{{Channel: "vector", Status: "ok"}}}
	snap, err := FuseRetrieval(plan, []QueryResult{q1, q1, q2})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates) != 1 || snap.SuccessfulQueries != 2 {
		t.Fatalf("dedup/empty failed %+v", snap)
	}
	if snap.Fields[0].Hits[0].Score != 1.0/61/2 {
		t.Fatalf("empty query missing from denominator %v", snap.Fields)
	}
	b := a
	b.Name = "conflict"
	q2.Channels[0].Hits = []RawHit{{UseID: "a", Score: 2, Full: b}}
	snap, err = FuseRetrieval(plan, []QueryResult{q1, q2})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates) != 0 || !snap.Degraded {
		t.Fatal("conflicting full not quarantined")
	}
}
func TestFusionPreservesRawConflictsAndIsIdempotent(t *testing.T) {
	plan := QueryPlan{Queries: []SearchQuery{{ID: "q", Field: FieldRelation, Text: "relation"}}}
	a := fixedCard("a")
	b := a
	b.Name = "changed"
	raw := []QueryResult{{Query: plan.Queries[0], Channels: []ChannelResult{{Channel: "vector", Status: "ok", Hits: []RawHit{{UseID: "a", Score: 1, Full: a}}}, {Channel: "fulltext", Status: "ok", Hits: []RawHit{{UseID: "a", Score: 1, Full: b}}}}}}
	got, err := FuseRetrieval(plan, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.RawQueries) != 1 || len(got.RawQueries[0].Channels[0].Hits) != 1 || len(got.RawQueries[0].Channels[1].Hits) != 1 {
		t.Fatal("raw conflicting full lost")
	}
	twice, err := FuseRetrieval(plan, got.RawQueries)
	if err != nil || len(twice.Candidates) != 0 || !twice.Degraded {
		t.Fatal("repeated fusion lost conflict", err)
	}
	raw[0].Channels[0].Hits[0].Full.Name = "mutated"
	if got.RawQueries[0].Channels[0].Hits[0].Full.Name == "mutated" {
		t.Fatal("raw snapshot aliased caller data")
	}
}

func TestFusionProtectionDoesNotSplitTieAtQuota(t *testing.T) {
	plan := QueryPlan{Queries: []SearchQuery{{ID: "r", Field: FieldRelation, Text: "r", RelationKinds: []string{"contrast"}}, {ID: "k", Field: FieldKeys, Text: "k"}, {ID: "e", Field: FieldEmotion, Text: "e"}}}
	hit := func(id string, score float64) RawHit {
		return RawHit{UseID: id, Score: score, Full: fixedCard(id), RelationKinds: []string{"contrast"}}
	}
	queries := []QueryResult{{Query: plan.Queries[0], Channels: []ChannelResult{{Channel: "vector", Status: "ok", Hits: []RawHit{hit("r", 10), hit("a", 9), hit("b", 9)}}}}, {Query: plan.Queries[1], Channels: []ChannelResult{{Channel: "vector", Status: "ok", Hits: []RawHit{hit("k", 10), hit("g", 9), hit("h", 8), hit("c", 7), hit("d", 6)}}}}, {Query: plan.Queries[2], Channels: []ChannelResult{{Channel: "vector", Status: "ok", Hits: []RawHit{hit("e", 10), hit("g", 9), hit("h", 8), hit("c", 7), hit("d", 6)}}}}}
	got, err := FuseRetrieval(plan, queries)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range got.Candidates {
		ids[c.UseID] = true
	}
	for _, id := range []string{"r", "k", "e", "g", "h"} {
		if !ids[id] {
			t.Fatalf("quota tie biased final pool %+v", ids)
		}
	}
}

func TestQueryKeywordLiteralAndEmotionSubjects(t *testing.T) {
	r := Request{RequestID: "r", Dialogue: []Turn{{TurnID: "1", Speaker: "alice", Text: "我很开心，光纤装好了。"}, {TurnID: "2", Speaker: "bob", Text: "我有些失望。"}}}
	r, s, err := ValidateRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	a := QueryAnalysis{Keys: []Key{{Text: "光纤", Evidence: refs(0)}, {Text: "fiber", Evidence: refs(0)}}, EmotionChain: []EmotionChain{{Subject: "alice", States: []string{"joy", "disappointment"}, Evidence: []Evidence{{SourceID: 0, State: Asserted}, {SourceID: 1, State: Asserted}}}}}
	got, err := CompileQueryPlan(a, r, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Queries) != 2 || got.Queries[0].Text != "光纤" || got.Queries[1].Field != FieldDescription || len(got.Warnings) != 2 {
		t.Fatalf("query fabricated lexical/subject bridge %+v", got)
	}
}

func TestRequestFragmentBudgetDoesNotLoseAnyOriginalText(t *testing.T) {
	text := strings.Repeat("不可以。", 200)
	r, s, err := ValidateRequest(Request{RequestID: "r", Dialogue: []Turn{{TurnID: "t", Speaker: "a", Text: text}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s) > 128 {
		t.Fatal("fragment cap exceeded")
	}
	var rebuilt string
	for _, f := range s {
		rebuilt += f.Quote
	}
	if rebuilt != r.Dialogue[0].Text {
		t.Fatal("fragment budget silently lost original text")
	}
	r.Dialogue[0].Speaker = " "
	if _, _, err := ValidateRequest(r); err == nil {
		t.Fatal("blank participant accepted")
	}
}

func TestFusionFailedQueriesExcludedButSuccessfulEmptyCounts(t *testing.T) {
	plan := QueryPlan{Queries: []SearchQuery{{ID: "a", Field: FieldKeys, Text: "first"}, {ID: "b", Field: FieldKeys, Text: "second"}}}
	one := QueryResult{Query: plan.Queries[0], Channels: []ChannelResult{{Channel: "vector", Status: "ok", Hits: []RawHit{{UseID: "a", Score: 1, Full: fixedCard("a")}}}}}
	second := QueryResult{Query: plan.Queries[1], Channels: []ChannelResult{{Channel: "vector", Status: "error", Error: "failure"}}}
	got, err := FuseRetrieval(plan, []QueryResult{one, second})
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields[0].Hits[0].Score != 1.0/61 || got.SuccessfulQueries != 1 || !got.Degraded {
		t.Fatalf("failed query affected denominator %+v", got)
	}
	second.Channels[0].Status = "ok"
	got, err = FuseRetrieval(plan, []QueryResult{one, second})
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields[0].Hits[0].Score != 1.0/61/2 || got.SuccessfulQueries != 2 {
		t.Fatal("successful empty absent from denominator")
	}
}

func TestEmotionCanDescribeExplicitThirdParty(t *testing.T) {
	r, s, err := ValidateRequest(Request{RequestID: "r", Dialogue: []Turn{{TurnID: "t", Speaker: "alice", Text: "Bob先很困惑，后来松了口气。"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := CompileQueryPlan(QueryAnalysis{EmotionChain: []EmotionChain{{Subject: "Bob", States: []string{"confusion", "relief"}, Evidence: refs(0)}}}, r, s)
	if err != nil || len(got.Queries) != 2 || got.Queries[1].Field != FieldDescription || len(got.Warnings) != 0 {
		t.Fatalf("third party incorrectly equated with speaker: %+v %v", got, err)
	}
}
