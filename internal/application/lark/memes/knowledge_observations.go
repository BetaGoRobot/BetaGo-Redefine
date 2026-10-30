package memes

import (
	"container/heap"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	MaxKnowledgeAnchors    = 6
	MaxKnowledgeCandidates = 64
)

// KnowledgeAnchor is an exact slice of an original turn, never normalized text.
type KnowledgeAnchor struct {
	ID        string   `json:"id"`
	TurnID    string   `json:"turn_id"`
	Text      string   `json:"text"`
	StartRune int      `json:"start_rune"`
	EndRune   int      `json:"end_rune"`
	EntryIDs  []string `json:"entry_ids"`
	// MatchKind summarizes the strongest match. EntryMatches preserves the
	// actual method for each entry and must be used for entry-level provenance.
	MatchKind    string                `json:"match_kind"`
	EntryMatches []KnowledgeEntryMatch `json:"entry_matches"`
}

type KnowledgeEntryMatch struct {
	EntryID   string `json:"entry_id"`
	MatchKind string `json:"match_kind"`
}

type KnowledgeSelection struct {
	Binding            KnowledgeBinding  `json:"binding"`
	Anchors            []KnowledgeAnchor `json:"anchors"`
	Entries            []KnowledgeEntry  `json:"entries"`
	OmittedAnchorCount int               `json:"omitted_anchor_count"`
}

// SelectKnowledge is the convenience API for callers without a reusable
// snapshot. Runtime callers should prepare NewKnowledgeSelector once instead.
func SelectKnowledge(req Request, snapshot KnowledgeSnapshot) (KnowledgeSelection, error) {
	selector, err := NewKnowledgeSelector(snapshot)
	if err != nil {
		return KnowledgeSelection{Binding: snapshot.Binding, Anchors: []KnowledgeAnchor{}, Entries: []KnowledgeEntry{}}, err
	}
	return selector.Select(req)
}

type knowledgeTerm struct {
	entryID  string
	original string
	basis    string
}

type knowledgeAnchorCandidate struct {
	anchor    KnowledgeAnchor
	turnIndex int
	priority  int
}

type knowledgeCandidatePool struct {
	candidates knowledgeCandidateHeap
	total      int
}

func (pool *knowledgeCandidatePool) add(candidate knowledgeAnchorCandidate) {
	pool.total++
	if len(pool.candidates) < MaxKnowledgeCandidates {
		heap.Push(&pool.candidates, candidate)
		return
	}
	if compareKnowledgeAnchors(candidate, pool.candidates[0]) < 0 {
		pool.candidates[0] = candidate
		heap.Fix(&pool.candidates, 0)
	}
}

// The root is the worst retained candidate, so a better streamed match can
// replace it without retaining an unbounded collection of original spans.
type knowledgeCandidateHeap []knowledgeAnchorCandidate

func (h knowledgeCandidateHeap) Len() int { return len(h) }
func (h knowledgeCandidateHeap) Less(i, j int) bool {
	return compareKnowledgeAnchors(h[i], h[j]) > 0
}
func (h knowledgeCandidateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *knowledgeCandidateHeap) Push(value any) {
	*h = append(*h, value.(knowledgeAnchorCandidate))
}
func (h *knowledgeCandidateHeap) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	(*h)[last] = knowledgeAnchorCandidate{}
	*h = (*h)[:last]
	return value
}

func knowledgeMatchKind(term knowledgeTerm, quote string) (string, int) {
	normalized := quote != term.original
	if term.basis == KnowledgeAliasSourceTitle {
		if normalized {
			return "source_title_normalized", 1
		}
		return "source_title_exact", 0
	}
	if normalized {
		return "inferred_card_name_normalized", 3
	}
	return "inferred_card_name_exact", 2
}

func compareKnowledgeAnchors(a, b knowledgeAnchorCandidate) int {
	// Newer turns always come first. Within the same turn, the priority
	// demotes generic dates/numbers, then ranks source provenance and exact
	// matches; length, original position and IDs resolve the remaining ties.
	if a.turnIndex != b.turnIndex {
		return b.turnIndex - a.turnIndex
	}
	if a.priority != b.priority {
		return a.priority - b.priority
	}
	if sizeA, sizeB := a.anchor.EndRune-a.anchor.StartRune, b.anchor.EndRune-b.anchor.StartRune; sizeA != sizeB {
		return sizeB - sizeA
	}
	if a.anchor.StartRune != b.anchor.StartRune {
		return a.anchor.StartRune - b.anchor.StartRune
	}
	if c := strings.Compare(a.anchor.Text, b.anchor.Text); c != 0 {
		return c
	}
	return slices.Compare(a.anchor.EntryIDs, b.anchor.EntryIDs)
}

// Each boundary connects a normalized byte offset to an original rune offset.
// Whole normalization segments are mapped atomically: neither half of a
// ligature nor the base of a combining sequence can become an invented span.
func knowledgeNormalizedView(text string) (string, map[int]int) {
	var normalized strings.Builder
	boundaries := map[int]int{0: 0}
	startRune := 0
	for start := 0; start < len(text); {
		width := norm.NFKC.NextBoundaryInString(text[start:], true)
		if width <= 0 {
			width = len(text) - start
		}
		part := text[start : start+width]
		boundaries[normalized.Len()] = startRune
		normalized.WriteString(knowledgeNormalize(part))
		startRune += utf8.RuneCountInString(part)
		boundaries[normalized.Len()] = startRune
		start += width
	}
	return normalized.String(), boundaries
}

// Lowercasing may create a new composable sequence (for example J plus caron).
// Normalize again so dictionary keys and mapped input segments share one form.
func knowledgeNormalize(text string) string {
	return norm.NFKC.String(strings.ToLower(norm.NFKC.String(text)))
}

func knowledgeWordBoundaries(view string, start, end int, term string) bool {
	first, _ := utf8.DecodeRuneInString(term)
	last, _ := utf8.DecodeLastRuneInString(term)
	if start > 0 && knowledgeLatinWordRune(first) {
		previous, _ := utf8.DecodeLastRuneInString(view[:start])
		if knowledgeLatinWordRune(previous) {
			return false
		}
	}
	if end < len(view) && knowledgeLatinWordRune(last) {
		next, _ := utf8.DecodeRuneInString(view[end:])
		if knowledgeLatinWordRune(next) {
			return false
		}
	}
	return true
}

// A single Han character needs explicit boundaries in the original utterance.
// Longer short names retain ordinary substring matching inside Chinese text.
func knowledgeSingleHanBoundaries(original []rune, start, end int, term string) bool {
	first, width := utf8.DecodeRuneInString(term)
	if width != len(term) || !unicode.Is(unicode.Han, first) {
		return true
	}
	boundary := func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	}
	if start > 0 && !boundary(original[start-1]) {
		return false
	}
	return end == len(original) || boundary(original[end])
}

func knowledgeLatinWordRune(r rune) bool {
	return unicode.In(r, unicode.Latin) || unicode.IsDigit(r) || r == '_'
}

func knowledgeGenericTerm(text string) bool {
	for _, r := range text {
		if !unicode.IsDigit(r) && !strings.ContainsRune("一二三四五六七八九十百千万亿两零〇年月日号周年天世纪-/. ", r) {
			return false
		}
	}
	return true
}

func knowledgeSortedMapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
