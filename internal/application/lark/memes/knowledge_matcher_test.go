package memes

import (
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

func TestKnowledgeSelectorReusableSelectionMatchesConvenienceWrapper(t *testing.T) {
	s := knowledgeTestSnapshot(t,
		knowledgeTestRecord(t, "hoi", "钢铁雄心", "当前卡名", "来源甲", ProjectionKeys{}),
		knowledgeTestRecord(t, "ambiguous", "钢铁雄心", "钢铁雄心", "来源乙", ProjectionKeys{}),
		knowledgeTestRecord(t, "suffix", "雄心", "雄心", "来源丙", ProjectionKeys{}),
		knowledgeTestRecord(t, "short", "微操", "微操", "来源丁", ProjectionKeys{}),
		knowledgeTestRecord(t, "unicode", "Café", "Café", "来源戊", ProjectionKeys{}))
	selector, err := NewKnowledgeSelector(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"钢铁雄心和微操", "👩‍💻说：Ｃａｆｅ\u0301，当前卡名", strings.Repeat("微操，", 80), "没有命中"} {
		req := knowledgeTestRequest(text)
		want, err := SelectKnowledge(req, s)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			got, err := selector.Select(req)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("prepared selection differs from convenience API: %+v %+v %v", got, want, err)
			}
		}
	}
}

func TestKnowledgeSelectorCaseNormalizationAfterCanonicalComposition(t *testing.T) {
	s := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "j", "ǰ", "ǰ", "lowercase j with caron", ProjectionKeys{}))
	got, err := SelectKnowledge(knowledgeTestRequest("说：J\u030c！"), s)
	if err != nil || len(got.Anchors) != 1 {
		t.Fatalf("canonically equivalent Latin case variant not found: %+v %v", got, err)
	}
	anchor := got.Anchors[0]
	if anchor.Text != "J\u030c" || anchor.StartRune != 2 || anchor.EndRune != 4 || anchor.MatchKind != "source_title_normalized" {
		t.Fatalf("canonical casing changed the original quote or offsets: %+v", anchor)
	}
}

func TestKnowledgeSelectorAhoCorasickMatchesLiteralOracle(t *testing.T) {
	var records []IndexRecord
	for i, title := range []string{"he", "she", "his", "hers", "微", "微操", "操", "钢铁雄心", "雄心", "心", "abab", "bab", "ab", "b"} {
		records = append(records, knowledgeTestRecord(t, fmt.Sprintf("pattern_%d", i), title, title, "原始词条", ProjectionKeys{}))
	}
	selector, err := NewKnowledgeSelector(knowledgeTestSnapshot(t, records...))
	if err != nil {
		t.Fatal(err)
	}
	if len(selector.patterns) != len(records) {
		t.Fatalf("matcher did not prepare all %d patterns: %d", len(records), len(selector.patterns))
	}
	text := "ushers his hers she abababa 微操，钢铁雄心"
	var got, want []string
	selector.scan(text, func(patternIndex, endByte int) {
		pattern := selector.patterns[patternIndex].text
		got = append(got, fmt.Sprintf("%d:%d:%s", endByte-len(pattern), endByte, pattern))
	})
	for _, pattern := range selector.patterns {
		for start := 0; start < len(text); start++ {
			if strings.HasPrefix(text[start:], pattern.text) {
				want = append(want, fmt.Sprintf("%d:%d:%s", start, start+len(pattern.text), pattern.text))
			}
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failure links lost or duplicated overlapping/suffix matches:\ngot %v\nwant %v", got, want)
	}
}

func TestKnowledgeSelectorDetachesSnapshotAndConcurrentResults(t *testing.T) {
	s := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "short", "微操", "微操", "来源原文", ProjectionKeys{}))
	selector, err := NewKnowledgeSelector(s)
	if err != nil {
		t.Fatal(err)
	}
	req := knowledgeTestRequest("微操")
	want, err := selector.Select(req)
	if err != nil || len(want.Entries) != 1 {
		t.Fatalf("selector is not ready: %+v %v", want, err)
	}
	s.Binding.Generation = "changed"
	s.Entries[0].ID = "changed"
	s.Entries[0].Sources[0].Title = "changed"
	s.Entries[0].Aliases[0].Text = "changed"
	s.Entries[0].Description = "changed"
	const workers = 4
	results := make(chan error, workers)
	for range workers {
		go func() {
			for range 20 {
				got, err := selector.Select(req)
				if err != nil || !reflect.DeepEqual(got, want) {
					results <- fmt.Errorf("prepared snapshot or returned data mutated: %v", err)
					return
				}
				got.Entries[0].Sources[0].Title = "changed"
				got.Entries[0].Aliases[0].Text = "changed"
				got.Anchors[0].EntryMatches[0].MatchKind = "changed"
				got.Anchors[0].EntryIDs[0] = "changed"
			}
			results <- nil
		}()
	}
	for range workers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func TestKnowledgeSelectorRejectsInvalidSnapshotAndUninitializedSelector(t *testing.T) {
	s := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "a", "微操", "微操", "来源解释", ProjectionKeys{}))
	s.Entries[0].Description = "tampered"
	if _, err := NewKnowledgeSelector(s); err == nil {
		t.Fatal("selector prepared a snapshot with invalid source content")
	}
	var selector *KnowledgeSelector
	if _, err := selector.Select(knowledgeTestRequest("微操")); err == nil {
		t.Fatal("nil selector accepted")
	}
	selector = &KnowledgeSelector{}
	if _, err := selector.Select(knowledgeTestRequest("微操")); err == nil {
		t.Fatal("uninitialized selector accepted")
	}
}

func TestKnowledgeSelectorRejectsOversizedLexiconBeforeFingerprintWork(t *testing.T) {
	s := knowledgeTestSnapshot(t, knowledgeTestRecord(t, "a", "微操", "微操", "来源解释", ProjectionKeys{}))
	s.Entries[0].Aliases[0].Text = strings.Repeat("a", 32*1024*1024)
	if _, err := NewKnowledgeSelector(s); err == nil || !strings.Contains(err.Error(), "lexicon exceeds") {
		t.Fatalf("oversized lexicon reached content fingerprint validation: %v", err)
	}
}

func TestKnowledgeSelectorRejectsOversizedTrieBeforeStateAllocation(t *testing.T) {
	// This path fits the lexicon-byte limit but exceeds the state budget.
	// Preflight must reject it without allocating any trie nodes or edges.
	if _, err := knowledgeMatcherStateCount([]string{strings.Repeat("a", 2*1024*1024+1)}); err == nil || !strings.Contains(err.Error(), "matcher exceeds") {
		t.Fatalf("oversized automaton passed allocation preflight: %v", err)
	}
}

// The corpus stays outside Git. Set BETAGO_KNOWLEDGE_BENCH_SNAPSHOT to a
// published/dry-run snapshot path to exercise the real dictionary and budget.
func TestKnowledgeSelectorFullCorpusConcurrentLatency(t *testing.T) {
	path := os.Getenv("BETAGO_KNOWLEDGE_BENCH_SNAPSHOT")
	if path == "" {
		t.Skip("set BETAGO_KNOWLEDGE_BENCH_SNAPSHOT for full-corpus performance verification")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot KnowledgeSnapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	stopSampling := make(chan struct{})
	peaks := make(chan runtime.MemStats)
	go func() {
		peak := before
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			var sample runtime.MemStats
			runtime.ReadMemStats(&sample)
			peak.HeapAlloc = max(peak.HeapAlloc, sample.HeapAlloc)
			peak.HeapSys = max(peak.HeapSys, sample.HeapSys)
			select {
			case <-stopSampling:
				peaks <- peak
				return
			case <-ticker.C:
			}
		}
	}()
	start := time.Now()
	selector, err := NewKnowledgeSelector(snapshot)
	cold := time.Since(start)
	close(stopSampling)
	peak := <-peaks
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	runtime.GC()
	var retained runtime.MemStats
	runtime.ReadMemStats(&retained)
	runtime.GC()
	var settled runtime.MemStats
	runtime.ReadMemStats(&settled)
	runtime.KeepAlive(snapshot)
	indexedAliases := 0
	for _, pattern := range selector.patterns {
		indexedAliases += len(pattern.matches)
	}
	t.Logf("cold prepare=%v allocated=%d bytes retained_heap_delta=%d bytes entries=%d patterns=%d indexed_aliases=%d states=%d fingerprint=%s", cold, after.TotalAlloc-before.TotalAlloc, int64(retained.HeapAlloc)-int64(before.HeapAlloc), len(snapshot.Entries), len(selector.patterns), indexedAliases, len(selector.nodes), snapshot.Fingerprint)
	t.Logf("cold memory baseline_heap=%d baseline_heap_sys=%d peak_heap=%d peak_heap_sys=%d peak_heap_delta=%d peak_heap_sys_delta=%d retained_heap=%d retained_heap_sys=%d GOMAXPROCS=%d", before.HeapAlloc, before.HeapSys, peak.HeapAlloc, peak.HeapSys, peak.HeapAlloc-before.HeapAlloc, peak.HeapSys-before.HeapSys, retained.HeapAlloc, retained.HeapSys, runtime.GOMAXPROCS(0))
	t.Logf("cold settled_heap=%d settled_heap_delta=%d bytes after_second_gc=true", settled.HeapAlloc, int64(settled.HeapAlloc)-int64(before.HeapAlloc))
	for _, multi := range []bool{false, true} {
		req := knowledgeTestRequest("三年，从钢铁雄心玩成海岛奇兵")
		if multi {
			req.Dialogue = nil
			for i := range 32 {
				req.Dialogue = append(req.Dialogue, Turn{TurnID: fmt.Sprint(i), Speaker: "alice", Text: strings.Repeat("微操，", 85)})
			}
		}
		if _, _, err = ValidateRequest(req); err != nil {
			t.Fatal("performance request is outside the input contract:", err)
		}
		if _, err = selector.Select(req); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var wg sync.WaitGroup
		const workers, iterations = 4, 32
		latencies := make(chan time.Duration, workers*iterations)
		errors := make(chan error, workers*iterations)
		for range workers {
			wg.Go(func() {
				for range iterations {
					start := time.Now()
					_, err := selector.Select(req)
					latencies <- time.Since(start)
					if err != nil {
						errors <- err
					}
				}
			})
		}
		wg.Wait()
		close(latencies)
		close(errors)
		for err := range errors {
			t.Fatal(err)
		}
		var durations []time.Duration
		for latency := range latencies {
			durations = append(durations, latency)
		}
		slices.Sort(durations)
		p95 := durations[(95*len(durations)+99)/100-1]
		runes := 0
		for _, turn := range req.Dialogue {
			runes += utf8.RuneCountInString(turn.Text)
		}
		t.Logf("hot concurrency=%d samples=%d turns=%d runes=%d p50=%v p95=%v max=%v", workers, len(durations), len(req.Dialogue), runes, durations[len(durations)/2], p95, durations[len(durations)-1])
		if p95 >= 500*time.Millisecond {
			t.Fatalf("hot selector exceeded 500ms phase deadline: p95=%v", p95)
		}
	}
}
