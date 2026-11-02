package memes

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/go-ego/gse"
)

const (
	// The version binds the pinned module, embedded dictionary, POS interpretation
	// and lexical filtering rules; it is independent of the exact-alias matcher.
	LinguisticObservationMethod = "gse_v1.3.1_embed_zh_s_segment_pos_candidates_v1"
	MaxLinguisticObservations   = 32
	MaxLinguisticTokenRunes     = 32
	maxLexicalSpanRunes         = 96
)

// LinguisticObservation describes dictionary segmentation, not a verified entity
// or a claim about the conversation. Text is always sliced from the original.
type LinguisticObservation struct {
	TurnID          string `json:"turn_id"`
	Text            string `json:"text"`
	StartRune       int    `json:"start_rune"`
	EndRune         int    `json:"end_rune"`
	POS             string `json:"pos"`
	Status          string `json:"status"`
	EntityCandidate string `json:"entity_candidate,omitempty"`
}

var linguisticAnalyzer struct {
	once      sync.Once
	segmenter gse.Segmenter
	err       error
}

func warmLinguisticAnalyzer() error {
	linguisticAnalyzer.once.Do(func() {
		// Segment performs read-only dictionary lookup after initialization. No
		// package globals, dictionary paths or model files are modified at runtime.
		linguisticAnalyzer.segmenter.SkipLog = true
		linguisticAnalyzer.segmenter.SkipSubSeg = true
		linguisticAnalyzer.err = linguisticAnalyzer.segmenter.LoadDictEmbed("zh_s")
	})
	return linguisticAnalyzer.err
}

func LinguisticDictionaryFingerprint() string {
	fp, _ := Fingerprint([]string{LinguisticObservationMethod, "dictionary_pos_candidate", "source_term_overlap"})
	return fp
}

func linguisticEntityCandidate(pos string) string {
	switch {
	case strings.HasPrefix(pos, "nr"):
		return "person"
	case strings.HasPrefix(pos, "ns"):
		return "place"
	case strings.HasPrefix(pos, "nt"):
		return "organization"
	default:
		return ""
	}
}

func linguisticTokens(turnID, text string) []LinguisticObservation {
	return linguisticTokensFromSegments(turnID, text, linguisticAnalyzer.segmenter.Segment([]byte(text)))
}

func linguisticTokensFromSegments(turnID, text string, segments []gse.Segment) []LinguisticObservation {
	// GSE returns byte offsets, including for lowercased ASCII dictionary keys.
	// Decode once and map only original UTF-8 boundaries back to rune offsets.
	boundaries := make(map[int]int, utf8.RuneCountInString(text)+1)
	count := 0
	for offset := range text {
		boundaries[offset] = count
		count++
	}
	boundaries[len(text)] = count
	out := []LinguisticObservation{}
	for _, segment := range segments {
		start, ok := boundaries[segment.Start()]
		end, endOK := boundaries[segment.End()]
		if !ok || !endOK || start >= end || segment.End() > len(text) {
			continue
		}
		original := text[segment.Start():segment.End()]
		if strings.IndexFunc(original, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
			continue
		}
		pos := segment.Token().Pos()
		out = append(out, LinguisticObservation{TurnID: turnID, Text: original, StartRune: start, EndRune: end, POS: pos, Status: "dictionary_pos_candidate", EntityCandidate: linguisticEntityCandidate(pos)})
	}
	return out
}

func appendLinguisticObservations(selection *KnowledgeSelection, tokens []LinguisticObservation) {
	for _, token := range tokens {
		// Prefer content words to particles/pronouns when the optional view is full.
		if !(strings.HasPrefix(token.POS, "n") || strings.HasPrefix(token.POS, "v") || strings.HasPrefix(token.POS, "a") || token.POS == "i" || token.POS == "l" || token.POS == "x") {
			selection.OmittedLinguistics++
			continue
		}
		if token.EndRune-token.StartRune > MaxLinguisticTokenRunes || len(selection.Linguistics) >= MaxLinguisticObservations {
			selection.OmittedLinguistics++
			continue
		}
		selection.Linguistics = append(selection.Linguistics, token)
	}
}

// Generic conversational words are not evidence for a new source association.
// Corpus-wide document-frequency filtering additionally removes common terms.
func lexicalContentTerm(token LinguisticObservation) (string, bool) {
	key := knowledgeNormalize(token.Text)
	if utf8.RuneCountInString(key) < 2 || !(strings.HasPrefix(token.POS, "n") || token.POS == "vn") || knowledgeGenericTerm(key) {
		return "", false
	}
	switch key {
	case "东西", "事情", "问题", "时间", "时候", "方面", "情况", "今天", "明天", "大家", "自己", "我们", "你们", "他们", "这个", "那个", "游戏", "来源", "解释", "内容", "名字", "名称", "人物", "地点", "历史", "玩家", "社区", "感觉", "意思", "世界", "生活", "工作", "朋友", "关系", "故事", "网友", "作者", "角色", "方式", "原因", "结果", "中国":
		return "", false
	}
	return key, true
}

type knowledgeLexicalPosting struct{ entryID string }

func (selector *KnowledgeSelector) buildLexicalIndex(snapshot KnowledgeSnapshot) error {
	selector.lexical = map[string][]knowledgeLexicalPosting{}
	budget := 0
	for _, entry := range snapshot.Entries {
		seen := map[string]bool{}
		// Only authoritative source title/body, never generated projection aliases.
		for _, text := range []string{entry.Title, entry.Description} {
			for _, token := range linguisticLexicalTokens(linguisticTokens("", text)) {
				key, ok := lexicalContentTerm(token)
				if !ok || seen[key] {
					continue
				}
				seen[key] = true
				budget += len(key) + len(entry.ID) + 32
				if budget > maxKnowledgeLexiconBytes {
					return errKnowledgeLexicalBudget
				}
				selector.lexical[key] = append(selector.lexical[key], knowledgeLexicalPosting{entryID: entry.ID})
			}
		}
	}
	// Keep each posting list small even for a very large frozen corpus. A single
	// document's generic language is still rejected by the content filter above.
	limit := min(64, max(3, len(snapshot.Entries)/10))
	for key, postings := range selector.lexical {
		if len(postings) > limit {
			delete(selector.lexical, key)
			continue
		}
		slices.SortFunc(postings, func(a, b knowledgeLexicalPosting) int { return strings.Compare(a.entryID, b.entryID) })
	}
	return nil
}

// Per-source state stores one recent occurrence per distinct content term.
// Its rune window bounds retention independently of input repetition and posting
// fanout. Once qualified, that source cannot create another candidate this turn.
type lexicalEvidence struct {
	recent  map[string]LinguisticObservation
	matched bool
}

func (selector *KnowledgeSelector) lexicalCandidates(turn Turn, turnIndex int, tokens []LinguisticObservation, pool *knowledgeCandidatePool, exactEntries map[string]bool) {
	evidence := map[string]*lexicalEvidence{}
	original := []rune(turn.Text)
	for _, token := range linguisticLexicalTokens(tokens) {
		key, ok := lexicalContentTerm(token)
		if !ok {
			continue
		}
		postings := selector.lexical[key]
		strong := token.EntityCandidate != "" && token.EndRune-token.StartRune >= 3 && len(postings) == 1
		for _, posting := range postings {
			if exactEntries[posting.entryID] {
				continue
			}
			state := evidence[posting.entryID]
			if state == nil {
				state = &lexicalEvidence{recent: map[string]LinguisticObservation{}}
				evidence[posting.entryID] = state
			}
			if state.matched {
				continue
			}
			start, qualifies := token.StartRune, strong
			for previousKey, previous := range state.recent {
				// Starts are monotonic, but compound/subword ends may overlap. Expire
				// by start so a later short subword can still use an earlier witness.
				if token.StartRune-previous.StartRune > maxLexicalSpanRunes {
					delete(state.recent, previousKey)
					continue
				}
				if token.EndRune-previous.StartRune > maxLexicalSpanRunes || previous.EndRune > token.StartRune || previousKey == key || strings.Contains(previousKey, key) || strings.Contains(key, previousKey) {
					continue
				}
				if !qualifies || previous.StartRune < start {
					start = previous.StartRune
					qualifies = true
				}
			}
			if !qualifies {
				// Replacing, rather than appending, retains the newest occurrence when
				// an older repeat expires before an independent term arrives.
				state.recent[key] = token
				continue
			}
			pool.add(knowledgeAnchorCandidate{anchor: KnowledgeAnchor{TurnID: turn.TurnID, Text: string(original[start:token.EndRune]), StartRune: start, EndRune: token.EndRune, EntryIDs: []string{posting.entryID}, MatchKind: "source_term_overlap", EntryMatches: []KnowledgeEntryMatch{{EntryID: posting.entryID, MatchKind: "source_term_overlap"}}}, turnIndex: turnIndex, priority: 10})
			state.matched = true
			state.recent = nil
		}
	}
}

func (c *GroundedContext) trimLinguisticsToBudget(limit int) {
	for len(c.Linguistics) > 0 {
		raw, _ := json.Marshal(c)
		if len(raw) <= limit {
			return
		}
		c.Linguistics = c.Linguistics[:len(c.Linguistics)-1]
		c.OmittedLinguistics++
	}
}

// Search-mode decomposition on individual nouns preserves compound recall
// without loading mutable dictionary state or treating one compound and its
// overlapping subword as two independent pieces of evidence.
func linguisticLexicalTokens(tokens []LinguisticObservation) []LinguisticObservation {
	out := append([]LinguisticObservation(nil), tokens...)
	for _, token := range tokens {
		if _, ok := lexicalContentTerm(token); !ok || token.EndRune-token.StartRune < 3 {
			continue
		}
		segments := linguisticAnalyzer.segmenter.ModeSegment([]byte(token.Text), true)
		for _, part := range linguisticTokensFromSegments(token.TurnID, token.Text, segments) {
			part.StartRune += token.StartRune
			part.EndRune += token.StartRune
			out = append(out, part)
		}
	}
	slices.SortFunc(out, func(a, b LinguisticObservation) int {
		if a.StartRune != b.StartRune {
			return a.StartRune - b.StartRune
		}
		return a.EndRune - b.EndRune
	})
	return slices.Compact(out)
}

// ValidateLinguisticMetadata accepts old scenes without this optional lane.
// Dictionary entity categories are inferred candidates and cannot be promoted.
func ValidateLinguisticMetadata(c *GroundedContext) bool {
	if c.LinguisticMethod == "" {
		return len(c.Linguistics) == 0 && c.LinguisticDictionaryFingerprint == "" && c.OmittedLinguistics == 0
	}
	if c.LinguisticMethod != LinguisticObservationMethod || c.LinguisticDictionaryFingerprint != LinguisticDictionaryFingerprint() || len(c.Linguistics) > MaxLinguisticObservations || c.OmittedLinguistics < 0 {
		return false
	}
	for _, token := range c.Linguistics {
		if token.TurnID == "" || token.StartRune < 0 || token.EndRune-token.StartRune != utf8.RuneCountInString(token.Text) || token.Text == "" || !utf8.ValidString(token.Text) || token.EndRune-token.StartRune > MaxLinguisticTokenRunes || token.POS == "" || len(token.POS) > 16 || token.Status != "dictionary_pos_candidate" || token.EntityCandidate != linguisticEntityCandidate(token.POS) {
			return false
		}
	}
	return true
}
