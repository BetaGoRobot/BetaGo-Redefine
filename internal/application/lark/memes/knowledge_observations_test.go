package memes

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func knowledgeTestRequest(text string) Request {
	return Request{RequestID: "knowledge-test", Dialogue: []Turn{{TurnID: "current", Speaker: "alice", Text: text}}}
}

func TestKnowledgeObservationsKeepBothH03EntitiesAndSeriousGameWording(t *testing.T) {
	s := knowledgeTestSnapshot(t,
		knowledgeTestRecord(t, "hoi", "钢铁雄心", "钢铁雄心", "钢铁雄心是策略游戏；社区用语也可能承担历史影射。", ProjectionKeys{}),
		knowledgeTestRecord(t, "bb", "海岛奇兵", "海岛奇兵", "海岛奇兵是游戏；某些社区语境还有历史影射用法。", ProjectionKeys{}))
	for _, text := range []string{"三年，从钢铁雄心玩成海岛奇兵", "认真问游戏机制：钢铁雄心的补给线和海岛奇兵的升级怎么选？不要讲历史影射。"} {
		got, err := SelectKnowledge(knowledgeTestRequest(text), s)
		if err != nil || len(got.Anchors) != 2 || len(got.Entries) != 2 || got.OmittedAnchorCount != 0 {
			t.Fatalf("literal observations lost a named entity: %+v %v", got, err)
		}
		for _, anchor := range got.Anchors {
			if anchor.MatchKind != "source_title_exact" || string([]rune(text)[anchor.StartRune:anchor.EndRune]) != anchor.Text {
				t.Fatalf("anchor rewrote or interpreted original words: %+v", anchor)
			}
		}
	}
}

func TestKnowledgeObservationsPreserveSameNameAmbiguity(t *testing.T) {
	s := knowledgeTestSnapshot(t,
		knowledgeTestRecord(t, "game", "微操", "微操", "游戏中对单位的精细控制。", ProjectionKeys{}),
		knowledgeTestRecord(t, "meme", "微操", "微操", "社区中讽刺不恰当干预的用法。", ProjectionKeys{}))
	got, err := SelectKnowledge(knowledgeTestRequest("认真讨论微操，不要替我确定是哪种意思。"), s)
	if err != nil || len(got.Anchors) != 1 || len(got.Anchors[0].EntryIDs) != 2 || len(got.Entries) != 2 {
		t.Fatalf("short term or ambiguous senses were discarded: %+v %v", got, err)
	}
}

func TestKnowledgeObservationsUnicodeNormalizationHasOriginalRuneSpans(t *testing.T) {
	s := knowledgeTestSnapshot(t,
		knowledgeTestRecord(t, "cafe", "Café", "Café", "词条一", ProjectionKeys{}),
		knowledgeTestRecord(t, "file", "file", "file", "词条二", ProjectionKeys{}),
		knowledgeTestRecord(t, "punct", "A-B", "A-B", "词条三", ProjectionKeys{}))
	text := "👩‍💻说：“Ｃａｆｅ\u0301”、ﬁle，还有（Ａ－Ｂ）！"
	got, err := SelectKnowledge(knowledgeTestRequest(text), s)
	if err != nil || len(got.Anchors) != 3 {
		t.Fatalf("normalized dictionary failed: %+v %v", got, err)
	}
	want := map[string]bool{"Ｃａｆｅ\u0301": true, "ﬁle": true, "Ａ－Ｂ": true}
	for _, anchor := range got.Anchors {
		if !want[anchor.Text] || string([]rune(text)[anchor.StartRune:anchor.EndRune]) != anchor.Text || anchor.MatchKind != "source_title_normalized" {
			t.Fatalf("normalization corrupted original offsets or punctuation: %+v", anchor)
		}
		delete(want, anchor.Text)
	}
	if len(want) != 0 {
		t.Fatal(want)
	}
	partial := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "partial", "f", "f", "不得匹配兼容字形的一部分", ProjectionKeys{}))
	got, err = SelectKnowledge(knowledgeTestRequest("ﬁ"), partial)
	if err != nil || len(got.Anchors) != 0 {
		t.Fatalf("partial normalization expansion became an original quote: %+v %v", got, err)
	}
}

func TestKnowledgeObservationsDictionaryOrderCannotChangeSelection(t *testing.T) {
	s := knowledgeTestSnapshot(t,
		knowledgeTestRecord(t, "a", "微操", "微操", "来源甲", ProjectionKeys{}),
		knowledgeTestRecord(t, "b", "微操", "微操", "来源乙", ProjectionKeys{}),
		knowledgeTestRecord(t, "c", "海岛奇兵", "海岛奇兵", "来源丙", ProjectionKeys{}))
	r := knowledgeTestRequest("海岛奇兵与微操")
	a, err := SelectKnowledge(r, s)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(s.Entries)-1; i < j; i, j = i+1, j-1 {
		s.Entries[i], s.Entries[j] = s.Entries[j], s.Entries[i]
	}
	knowledgeTestReseal(t, &s)
	b, err := SelectKnowledge(r, s)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("dictionary order changed selected entries or ambiguous IDs: %+v %+v %v", a, b, err)
	}
	a.Entries[0].Sources[0].Title = "mutated"
	if s.Entries[0].Sources[0].Title == "mutated" || s.Entries[1].Sources[0].Title == "mutated" {
		t.Fatal("selection aliases immutable dictionary")
	}
}

func TestKnowledgeObservationsBudgetFavorsCurrentTurnThenRecency(t *testing.T) {
	var records []IndexRecord
	for i, title := range []string{"旧词甲", "旧词乙", "旧词丙", "旧词丁", "旧词戊", "旧词己", "新词甲", "新词乙"} {
		records = append(records, knowledgeTestRecord(t, fmt.Sprintf("term_%d", i), title, title, "来源解释", ProjectionKeys{}))
	}
	s := knowledgeTestSnapshot(t, records...)
	r := Request{RequestID: "budget", Dialogue: []Turn{
		{TurnID: "oldest", Speaker: "alice", Text: "旧词甲、旧词乙、旧词丙、旧词丁"},
		{TurnID: "recent", Speaker: "bob", Text: "旧词戊、旧词己"},
		{TurnID: "current", Speaker: "alice", Text: "新词甲、新词乙"},
	}}
	got, err := SelectKnowledge(r, s)
	if err != nil || len(got.Anchors) != 6 || got.OmittedAnchorCount != 2 {
		t.Fatalf("anchor cap or explicit omission failed: %+v %v", got, err)
	}
	for i, turnID := range []string{"current", "current", "recent", "recent", "oldest", "oldest"} {
		if got.Anchors[i].TurnID != turnID {
			t.Fatalf("current/recency priority failed: %+v", got.Anchors)
		}
	}
}

func TestKnowledgeObservationsRejectInvalidRequestAndAllowNoMatches(t *testing.T) {
	s := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "a", "微操", "微操", "来源解释", ProjectionKeys{}))
	if _, err := SelectKnowledge(Request{}, s); err == nil {
		t.Fatal("invalid original request accepted")
	}
	got, err := SelectKnowledge(knowledgeTestRequest("认真讨论另一个主题。"), s)
	if err != nil || len(got.Anchors) != 0 || len(got.Entries) != 0 || got.Binding != s.Binding {
		t.Fatalf("empty knowledge result lost its valid binding: %+v %v", got, err)
	}
}

func TestKnowledgeObservationsDatesCannotCrowdOutNamedSources(t *testing.T) {
	var records []IndexRecord
	for i, title := range []string{"一年", "二年", "三年", "四年", "五年", "六年", "七年", "钢铁雄心", "海岛奇兵"} {
		records = append(records, knowledgeTestRecord(t, fmt.Sprintf("date_%d", i), title, title, "来源解释", ProjectionKeys{}))
	}
	s := knowledgeTestSnapshot(t, records...)
	got, err := SelectKnowledge(knowledgeTestRequest("一年二年三年四年五年六年七年，从钢铁雄心玩成海岛奇兵"), s)
	if err != nil || len(got.Anchors) != 6 || got.OmittedAnchorCount != 3 || got.Anchors[0].Text != "钢铁雄心" || got.Anchors[1].Text != "海岛奇兵" {
		t.Fatalf("generic dates occupied high-specificity title budget: %+v %v", got, err)
	}
}

func TestKnowledgeObservationsConcurrentSelectionsDoNotMutateSnapshot(t *testing.T) {
	s := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "a", "微操", "微操", "来源原文", ProjectionKeys{}))
	const workers = 8
	results := make(chan error, workers)
	for range workers {
		go func() {
			for range 10 {
				got, err := SelectKnowledge(knowledgeTestRequest("微操"), s)
				if err != nil {
					results <- err
					return
				}
				got.Entries[0].Aliases[0].Text = "changed"
				got.Entries[0].UseIDs[0] = "changed"
			}
			results <- nil
		}()
	}
	for range workers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateKnowledgeSnapshot(s, s.Binding); err != nil {
		t.Fatal("concurrent selection mutated immutable snapshot:", err)
	}
}

func TestKnowledgeObservationsCurrentInferredNamePrecedesOldExactTitles(t *testing.T) {
	var records []IndexRecord
	for i, title := range []string{"旧词甲", "旧词乙", "旧词丙", "旧词丁", "旧词戊", "旧词己"} {
		records = append(records, knowledgeTestRecord(t, fmt.Sprintf("old_%d", i), title, title, "旧来源解释", ProjectionKeys{}))
	}
	records = append(records,
		knowledgeTestRecord(t, "current", "当前来源标题", "当前卡名", "当前来源解释", ProjectionKeys{}),
		knowledgeTestRecord(t, "date", "一年", "一年", "日期解释", ProjectionKeys{}))
	s := knowledgeTestSnapshot(t, records...)
	r := Request{RequestID: "current-first", Dialogue: []Turn{
		{TurnID: "old", Speaker: "alice", Text: "旧词甲、旧词乙、旧词丙、旧词丁、旧词戊、旧词己"},
		{TurnID: "current", Speaker: "bob", Text: "一年，当前卡名"},
	}}
	got, err := SelectKnowledge(r, s)
	if err != nil || len(got.Anchors) != 6 || got.OmittedAnchorCount != 2 {
		t.Fatalf("unexpected capped selection: %+v %v", got, err)
	}
	// Recency wins across turns; specificity and provenance rank within a turn.
	if got.Anchors[0].TurnID != "current" || got.Anchors[0].Text != "当前卡名" || got.Anchors[0].MatchKind != "inferred_card_name_exact" || got.Anchors[1].Text != "一年" {
		t.Fatalf("old exact titles displaced current observations, or generic date displaced current named alias: %+v", got.Anchors)
	}
}

func TestKnowledgeObservationsSameSpanKeepsEachEntryMatchProvenance(t *testing.T) {
	s := knowledgeTestSnapshot(t,
		knowledgeTestRecord(t, "title", "微操", "微操", "原始词条解释", ProjectionKeys{}),
		knowledgeTestRecord(t, "inference", "另一个来源", "微操", "不同来源自动生成的卡名", ProjectionKeys{}))
	got, err := SelectKnowledge(knowledgeTestRequest("微操"), s)
	if err != nil || len(got.Anchors) != 1 || len(got.Entries) != 2 {
		t.Fatalf("same-span ambiguity was lost: %+v %v", got, err)
	}
	anchor := got.Anchors[0]
	if anchor.MatchKind != "source_title_exact" || len(anchor.EntryMatches) != 2 {
		t.Fatalf("missing per-entry match provenance: %+v", anchor)
	}
	want := map[string]string{}
	for _, entry := range got.Entries {
		if entry.Title == "微操" {
			want[entry.ID] = "source_title_exact"
		} else {
			want[entry.ID] = "inferred_card_name_exact"
		}
	}
	var ids []string
	for _, match := range anchor.EntryMatches {
		if want[match.EntryID] != match.MatchKind {
			t.Fatalf("strong source-title method was attributed to inferred entry: %+v", anchor.EntryMatches)
		}
		ids = append(ids, match.EntryID)
		delete(want, match.EntryID)
	}
	if len(want) != 0 || !reflect.DeepEqual(ids, anchor.EntryIDs) {
		t.Fatalf("entry references and per-entry provenance disagree: %+v", anchor)
	}
}

func TestKnowledgeCandidatePoolNeverRetainsMoreThan64(t *testing.T) {
	pool := knowledgeCandidatePool{}
	const total = 512
	for i := range total {
		pool.add(knowledgeAnchorCandidate{turnIndex: i, anchor: KnowledgeAnchor{Text: "微操", StartRune: i, EndRune: i + 2}})
		if len(pool.candidates) > 64 {
			t.Fatalf("retained %d candidates after %d matches", len(pool.candidates), i+1)
		}
	}
	if pool.total != total || len(pool.candidates) != 64 {
		t.Fatalf("candidate count or retention cap mismatch: total=%d retained=%d", pool.total, len(pool.candidates))
	}
	slices.SortFunc(pool.candidates, compareKnowledgeAnchors)
	for i, candidate := range pool.candidates {
		if candidate.turnIndex != total-i-1 {
			t.Fatalf("bounded pool dropped higher-ranked candidate: position=%d turn=%d", i, candidate.turnIndex)
		}
	}
}

func TestKnowledgeObservationsOmissionsCountCandidatesBeyond64(t *testing.T) {
	s := knowledgeTestSnapshot(t,
		knowledgeTestRecord(t, "title", "微操", "微操", "原始词条解释", ProjectionKeys{}),
		knowledgeTestRecord(t, "inference", "另一个来源", "微操", "自动生成卡名", ProjectionKeys{}))
	const total = 80
	got, err := SelectKnowledge(knowledgeTestRequest(strings.Repeat("微操，", total)), s)
	if err != nil || len(got.Anchors) != 6 || got.OmittedAnchorCount != total-6 {
		t.Fatalf("matches discarded by the 64-candidate working set were silently omitted: %+v %v", got, err)
	}
	for i, anchor := range got.Anchors {
		if anchor.StartRune != i*3 || len(anchor.EntryMatches) != 2 || len(anchor.EntryIDs) != 2 {
			t.Fatalf("same-span entries were double-counted or stable rank changed: %+v", anchor)
		}
	}
}

func TestKnowledgeObservationsSingleHanRequiresOriginalWordBoundaries(t *testing.T) {
	s := knowledgeTestSnapshot(t,
		knowledgeTestRecord(t, "give", "给", "给", "原始单字词条", ProjectionKeys{}),
		knowledgeTestRecord(t, "laugh", "笑", "笑", "笑的来源解释", ProjectionKeys{}),
		knowledgeTestRecord(t, "laugh_ambiguous", "笑", "笑", "笑的另一来源解释", ProjectionKeys{}),
		knowledgeTestRecord(t, "meme", "梗", "梗", "梗的来源解释", ProjectionKeys{}),
		knowledgeTestRecord(t, "grass", "草", "草", "草的来源解释", ProjectionKeys{}),
		knowledgeTestRecord(t, "inferred", "卡名推断来源", "草", "不同来源的卡名推断", ProjectionKeys{}),
		knowledgeTestRecord(t, "micro", "微操", "微操", "两字短词来源解释", ProjectionKeys{}))
	selector, err := NewKnowledgeSelector(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		text string
		want []string
	}{
		{text: "给我讲个地狱笑话"},
		{text: "不要玩梗"},
		{text: "苏联笑话"},
		{text: "微笑草地"},
		{text: "A笑B"},
		{text: "1笑2"},
		{text: "Ａ笑Ｂ"},
		{text: "笑a"},
		{text: "a笑"},
		{text: "笑话，草稿"},
		{text: "笑", want: []string{"笑"}},
		{text: "草", want: []string{"草"}},
		{text: " 笑\t", want: []string{"笑"}},
		{text: "“笑”", want: []string{"笑"}},
		{text: "「草」", want: []string{"草"}},
		{text: "。笑！", want: []string{"笑"}},
		{text: "😂笑🙂", want: []string{"笑"}},
		{text: "Ⓐ笑Ⓑ", want: []string{"笑"}},
		{text: "👩‍💻草🚀", want: []string{"草"}},
		{text: "这个微操实在离谱", want: []string{"微操"}},
	} {
		t.Run(test.text, func(t *testing.T) {
			req := knowledgeTestRequest(test.text)
			before := knowledgeTestClone(t, req)
			got, err := selector.Select(req)
			if err != nil {
				t.Fatal(err)
			}
			var texts []string
			for _, anchor := range got.Anchors {
				texts = append(texts, anchor.Text)
				if string([]rune(test.text)[anchor.StartRune:anchor.EndRune]) != anchor.Text {
					t.Fatal("boundary filter changed original rune spans")
				}
				if (anchor.Text == "笑" || anchor.Text == "草") && len(anchor.EntryIDs) != 2 {
					t.Fatalf("standalone or quoted single-character ambiguity was removed: %+v", anchor)
				}
			}
			if !reflect.DeepEqual(texts, test.want) {
				t.Fatalf("single-Han boundary mismatch: got %v want %v", texts, test.want)
			}
			if !reflect.DeepEqual(req, before) {
				t.Fatal("boundary filter mutated original planner request")
			}
		})
	}
	if err := ValidateKnowledgeSnapshot(s, s.Binding); err != nil {
		t.Fatal("boundary filtering changed the frozen source fingerprint:", err)
	}
}
