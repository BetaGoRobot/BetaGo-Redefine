package memes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLinguisticObservationsWithoutKnownTitlePreserveOriginalUnicode(t *testing.T) {
	selector, err := NewKnowledgeSelector(knowledgeTestSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	text := "👩‍💻ＡBC北京的计算机网络，张三正在研究火箭技术。"
	got, err := selector.Select(knowledgeTestRequest(text))
	if err != nil {
		t.Fatal(err)
	}
	view := GroundedKnowledgeContext(got)
	if view.LinguisticMethod != LinguisticObservationMethod || view.LinguisticDictionaryFingerprint != LinguisticDictionaryFingerprint() || len(view.Linguistics) == 0 {
		t.Fatalf("general segmentation missing without known titles: %+v", view)
	}
	runes := []rune(text)
	seenPOS, seenEntity := false, false
	for _, token := range view.Linguistics {
		if token.StartRune < 0 || token.EndRune > len(runes) || string(runes[token.StartRune:token.EndRune]) != token.Text || token.TurnID != "current" || token.Status != "dictionary_pos_candidate" {
			t.Fatalf("invented Unicode token or promoted candidate: %+v", token)
		}
		if token.POS != "" {
			seenPOS = true
		}
		if token.EntityCandidate != "" {
			seenEntity = true
		}
	}
	if !seenPOS || !seenEntity {
		t.Fatalf("dictionary POS/entity candidates missing: %+v", view.Linguistics)
	}
}

func TestKnowledgeLexicalRecallUsesSourceTermsWithoutTitleHit(t *testing.T) {
	s := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "rocket", "完全不同的词条标题", "另一个卡名", "火箭发动机使用液体燃料。", ProjectionKeys{}))
	got, err := SelectKnowledge(knowledgeTestRequest("这个发动机使用的燃料有点离谱"), s)
	if err != nil || len(got.Anchors) != 1 || len(got.Entries) != 1 {
		t.Fatalf("unknown title did not recall source-term association: %+v %v", got, err)
	}
	a := got.Anchors[0]
	if a.MatchKind != "source_term_overlap" || len(a.EntryMatches) != 1 || a.EntryMatches[0].MatchKind != "source_term_overlap" || a.Text != "发动机使用的燃料" {
		t.Fatalf("lexical candidate misrepresented as title match: %+v", a)
	}
	if got.Entries[0].Description != s.Entries[0].Description || got.Entries[0].Senses[0].Status != "source_claim" {
		t.Fatal("source claim rewritten")
	}
}

func TestKnowledgeLexicalRecallSuppressesCommonWordsAndSingleTerm(t *testing.T) {
	s := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "rocket", "完全不同标题", "完全不同卡名", "游戏中的人物讨论发动机和燃料的问题。", ProjectionKeys{}))
	for _, text := range []string{"今天游戏有什么问题", "发动机坏了", "人物喜欢这个游戏", "发动机发动机发动机"} {
		got, err := SelectKnowledge(knowledgeTestRequest(text), s)
		if err != nil || len(got.Anchors) != 0 {
			t.Fatalf("weak/common overlap recalled knowledge for %q: %+v %v", text, got, err)
		}
	}
}

func TestKnowledgeLexicalRecallOnlyFillsUnusedExactAnchorCapacity(t *testing.T) {
	records := []IndexRecord{knowledgeTestRecord(t, "lex", "完全不同标题", "完全不同卡名", "发动机和燃料。", ProjectionKeys{})}
	for i := range 6 {
		title := fmt.Sprintf("精确词条%d", i)
		records = append(records, knowledgeTestRecord(t, fmt.Sprintf("exact%d", i), title, title, "来源解释", ProjectionKeys{}))
	}
	s := knowledgeTestSnapshot(t, records...)
	req := Request{RequestID: "exact-first", Dialogue: []Turn{{TurnID: "old", Speaker: "a", Text: "精确词条0 精确词条1 精确词条2 精确词条3 精确词条4 精确词条5"}, {TurnID: "current", Speaker: "b", Text: "发动机使用燃料"}}}
	got, err := SelectKnowledge(req, s)
	if err != nil || len(got.Anchors) != 6 {
		t.Fatalf("unexpected anchors: %+v %v", got, err)
	}
	for _, a := range got.Anchors {
		if a.MatchKind == "source_term_overlap" {
			t.Fatal("lexical association displaced exact anchor")
		}
	}
}

func TestLinguisticContextBoundedAndConcurrentReuse(t *testing.T) {
	s := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "lex", "不同词条标题", "不同卡片名字", "发动机和燃料。", ProjectionKeys{}))
	selector, err := NewKnowledgeSelector(s)
	if err != nil {
		t.Fatal(err)
	}
	req := knowledgeTestRequest(strings.Repeat("发动机使用燃料，北京研究火箭技术。", 300))
	expected, err := selector.Select(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(GroundedKnowledgeContext(expected))
	var view map[string]json.RawMessage
	_ = json.Unmarshal(raw, &view)
	var tokens []json.RawMessage
	_ = json.Unmarshal(view["linguistics"], &tokens)
	if len(tokens) == 0 || len(tokens) > 32 || len(raw) > 16*1024 {
		t.Fatalf("unbounded or empty linguistic view: tokens=%d bytes=%d", len(tokens), len(raw))
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				got, e := selector.Select(req)
				if e != nil || !reflect.DeepEqual(got, expected) {
					t.Errorf("concurrent result changed: %v", e)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err = ValidateKnowledgeSnapshot(s, s.Binding); err != nil {
		t.Fatal(err)
	}
}

func TestLinguisticAndLexicalObservationsSurviveVisibilityFiltering(t *testing.T) {
	snapshot := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "rocket", "不同的来源标题", "不同的卡名", "火箭发动机使用液体燃料。", ProjectionKeys{}))
	selected, err := SelectKnowledge(knowledgeTestRequest("发动机使用燃料"), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	filtered := FilterKnowledgeSelection(selected, map[string]bool{})
	if len(filtered.Entries) != 0 || len(filtered.Anchors) != 1 || len(filtered.Anchors[0].EntryIDs) != 0 || len(filtered.Anchors[0].EntryMatches) != 0 {
		t.Fatalf("withdrawn lexical source escaped policy: %+v", filtered)
	}
	if !reflect.DeepEqual(filtered.Linguistics, selected.Linguistics) {
		t.Fatal("visibility policy dropped independent original-text linguistic candidates")
	}
	c := GroundedKnowledgeContext(selected)
	RestrictGroundedContext(c, filtered)
	if len(c.PriorKnowledge) != 0 || len(c.Linguistics) == 0 {
		t.Fatalf("withdrawal lost observations or retained hidden source: %+v", c)
	}
}

func TestLinguisticPlannerReceivesCandidatesBeforeInterpretation(t *testing.T) {
	snapshot := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "rocket", "不同的来源标题", "不同的卡名", "火箭发动机使用液体燃料。", ProjectionKeys{}))
	model := &groundedCompletion{}
	service, err := NewGroundedReferenceService(ReferenceDependencies{Model: model, Retriever: groundedRetriever{}}, ReferenceOptions{RequestTimeout: time.Second, StageTimeout: time.Second}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	result := service.Retrieve(context.Background(), knowledgeTestRequest("发动机使用燃料"))
	input := string(model.input.InputJSON)
	if !strings.Contains(input, `"linguistic_method":"gse_v1.3.1_embed_zh_s_segment_pos_candidates_v1"`) || !strings.Contains(input, `"status":"dictionary_pos_candidate"`) || !strings.Contains(input, `"match_kind":"source_term_overlap"`) || result.ContextV2 == nil || len(result.ContextV2.Linguistics) == 0 {
		t.Fatalf("planner lacked prior linguistic/lexical observations: %s result=%+v", input, result)
	}
}

func TestKnowledgeLexicalRareEntityAndCommonCorpusTerms(t *testing.T) {
	snapshot := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "entity", "不同的来源标题", "不同的卡名", "阿根廷的足球。", ProjectionKeys{}))
	got, err := SelectKnowledge(knowledgeTestRequest("阿根廷怎么了"), snapshot)
	if err != nil || len(got.Anchors) != 1 || got.Anchors[0].MatchKind != "source_term_overlap" {
		t.Fatalf("rare dictionary entity candidate not recalled: %+v %v", got, err)
	}
	var records []IndexRecord
	for i := range 5 {
		records = append(records, knowledgeTestRecord(t, fmt.Sprintf("common%d", i), fmt.Sprintf("不同标题%d", i), fmt.Sprintf("不同卡名%d", i), "发动机使用燃料。", ProjectionKeys{}))
	}
	snapshot = knowledgeTestSnapshot(t, records...)
	got, err = SelectKnowledge(knowledgeTestRequest("发动机使用燃料"), snapshot)
	if err != nil || len(got.Anchors) != 0 {
		t.Fatalf("corpus-common terms became association: %+v %v", got, err)
	}
}

func TestLinguisticCapPrefersCurrentContentAndOmitsOverlongToken(t *testing.T) {
	selector, err := NewKnowledgeSelector(knowledgeTestSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	req := Request{RequestID: "cap", Dialogue: []Turn{{TurnID: "old", Speaker: "a", Text: strings.Repeat("发动机和燃料", 200)}, {TurnID: "current", Speaker: "b", Text: strings.Repeat("的", 100) + "北京计算机网络 " + strings.Repeat("A", 100)}}}
	got, err := selector.Select(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Linguistics) == 0 || got.Linguistics[0].TurnID != "current" {
		t.Fatal("current content lost to older observations")
	}
	for _, token := range got.Linguistics {
		if token.Text == "的" || len([]rune(token.Text)) > 32 || token.Status != "dictionary_pos_candidate" {
			t.Fatalf("function word or oversized token crowded useful view: %+v", token)
		}
	}
	if got.OmittedLinguistics == 0 {
		t.Fatal("omissions were not recorded")
	}
}

// Unlike the exact-title stress case, this request forces lexical processing at
// the request rune limit while retaining no exact title/alias matches.
func TestKnowledgeLexicalFullCorpusLatency(t *testing.T) {
	path := os.Getenv("BETAGO_KNOWLEDGE_BENCH_SNAPSHOT")
	if path == "" {
		t.Skip("set BETAGO_KNOWLEDGE_BENCH_SNAPSHOT for full corpus")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot KnowledgeSnapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	selector, err := NewKnowledgeSelector(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(selector.lexical))
	for key := range selector.lexical {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var terms []string
	for _, key := range keys {
		if utf8.RuneCountInString(key) < 2 {
			continue
		}
		got, e := selector.Select(knowledgeTestRequest(key))
		if e != nil {
			t.Fatal(e)
		}
		exact := false
		for _, anchor := range got.Anchors {
			if anchor.MatchKind != "source_term_overlap" {
				exact = true
			}
		}
		if !exact {
			terms = append(terms, key)
		}
		if len(terms) == 32 {
			break
		}
	}
	if len(terms) < 2 {
		t.Fatal("corpus lacks lexical-only test terms")
	}
	phrase := strings.Join(terms, "，") + "。"
	runes := []rune(strings.Repeat(phrase, MaxInputRunes/utf8.RuneCountInString(phrase)+1))
	req := knowledgeTestRequest(string(runes[:MaxInputRunes]))
	got, err := selector.Select(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, anchor := range got.Anchors {
		if anchor.MatchKind != "source_term_overlap" {
			t.Fatalf("stress fixture unexpectedly hit exact title: %+v", anchor)
		}
	}
	var wg sync.WaitGroup
	durations := make(chan time.Duration, 32)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 8 {
				started := time.Now()
				_, e := selector.Select(req)
				durations <- time.Since(started)
				if e != nil {
					t.Error(e)
				}
			}
		}()
	}
	wg.Wait()
	close(durations)
	var latencies []time.Duration
	for elapsed := range durations {
		latencies = append(latencies, elapsed)
	}
	slices.Sort(latencies)
	t.Logf("lexical-only terms=%d runes=%d concurrency=4 samples=%d p95=%v max=%v", len(terms), MaxInputRunes, len(latencies), latencies[len(latencies)*95/100], latencies[len(latencies)-1])
	if latencies[len(latencies)-1] > 500*time.Millisecond {
		t.Fatal("lexical-only request exceeded knowledge budget")
	}
}

func TestKnowledgeCandidatePoolShares64SlotsAcrossMatchKinds(t *testing.T) {
	pool := knowledgeCandidatePool{}
	pool.add(knowledgeAnchorCandidate{turnIndex: 0, anchor: KnowledgeAnchor{Text: "旧的精确标题", MatchKind: "source_title_exact"}})
	for i := range 100 {
		pool.add(knowledgeAnchorCandidate{turnIndex: i + 1, anchor: KnowledgeAnchor{Text: "新的词项关联", MatchKind: "source_term_overlap"}})
	}
	if len(pool.candidates) != 64 || pool.total != 101 {
		t.Fatalf("combined working set lost its bound: retained=%d total=%d", len(pool.candidates), pool.total)
	}
	slices.SortFunc(pool.candidates, compareKnowledgeAnchors)
	if pool.candidates[0].anchor.MatchKind != "source_title_exact" {
		t.Fatal("new lexical candidates displaced older exact evidence")
	}
}

func TestKnowledgeLexicalRepeatedPostingNounBoundedConcurrentRequests(t *testing.T) {
	var records []IndexRecord
	for i := range 640 {
		description := "无关"
		if i < 64 {
			description = "发动机"
		}
		records = append(records, knowledgeTestRecord(t, fmt.Sprintf("load_%d", i), fmt.Sprintf("占位词条%d", i), fmt.Sprintf("占位卡片%d", i), description, ProjectionKeys{}))
	}
	selector, err := NewKnowledgeSelector(knowledgeTestSnapshot(t, records...))
	if err != nil {
		t.Fatal(err)
	}
	if len(selector.lexical["发动机"]) != 64 {
		t.Fatal("regression fixture does not exercise maximum posting fanout")
	}
	req := knowledgeTestRequest(strings.Repeat("发动机", MaxInputRunes/3))
	// Allocation is the stable regression gate, including under -race. Measure
	// the production wall-time budget only in an opt-in native performance run.
	enforceTiming := os.Getenv("BETAGO_KNOWLEDGE_BENCH_TIMING") == "1"
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			got, e := selector.Select(req)
			elapsed := time.Since(started)
			if e != nil || len(got.Anchors) != 0 {
				t.Errorf("repeated noun became independent evidence: %+v %v", got.Anchors, e)
			}
			t.Logf("repeated noun select=%v", elapsed)
			if enforceTiming && elapsed > 500*time.Millisecond {
				t.Errorf("valid repeated-noun request exceeded500ms: %v", elapsed)
			}
		}()
	}
	wg.Wait()
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("four-request TotalAlloc=%d bytes", allocated)
	if allocated > 64<<20 {
		t.Fatalf("repeated noun expanded occurrence-by-posting evidence: allocated=%d bytes", allocated)
	}
}

func TestKnowledgeLexicalRecentRepeatRetainsIndependentWindowEvidence(t *testing.T) {
	selector, err := NewKnowledgeSelector(knowledgeTestSnapshot(t, knowledgeTestRecord(t, "window", "不同的来源标题", "不同的卡名", "发动机和燃料。", ProjectionKeys{})))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		text string
		want bool
	}{
		{text: "发动机" + strings.Repeat("的", 100) + "发动机使用燃料", want: true},
		{text: "发动机" + strings.Repeat("的", 100) + "燃料", want: false},
		{text: strings.Repeat("发动机", 100), want: false},
	} {
		got, e := selector.Select(knowledgeTestRequest(test.text))
		if e != nil {
			t.Fatal(e)
		}
		if (len(got.Anchors) > 0) != test.want {
			t.Fatalf("recent evidence mismatch: want=%v anchors=%+v", test.want, got.Anchors)
		}
		for _, anchor := range got.Anchors {
			if anchor.StartRune < 100 || anchor.EndRune-anchor.StartRune > maxLexicalSpanRunes || anchor.Text != string([]rune(test.text)[anchor.StartRune:anchor.EndRune]) {
				t.Fatalf("stale or invented witness span: %+v", anchor)
			}
		}
	}
}
