package memes

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	maxKnowledgeLexiconBytes  = 32 << 20
	maxKnowledgeMatcherStates = 2 << 20
)

// KnowledgeSelector owns an immutable, generation-bound dictionary prepared
// once for concurrent requests. Its source data is detached during construction.
type KnowledgeSelector struct {
	binding  KnowledgeBinding
	entries  map[string]KnowledgeEntry
	patterns []knowledgePattern
	nodes    []knowledgeMatcherNode
	edges    []knowledgeMatcherEdge
}

type knowledgePattern struct {
	text    string
	matches []knowledgeTerm
	generic bool
}

type knowledgeMatcherNode struct {
	edgeStart int32
	failure   int32
	output    int32
	terminal  int32
	edgeCount uint16
}

type knowledgeMatcherEdge struct {
	next  int32
	value byte
}

type knowledgeBuildEdge struct {
	next    int32
	sibling int32
	value   byte
}

func NewKnowledgeSelector(snapshot KnowledgeSnapshot) (*KnowledgeSelector, error) {
	// Bound dictionary input before canonical fingerprint work. Description
	// bodies are additionally bounded by the authoritative PG reader.
	metadataBytes := 0
	for _, entry := range snapshot.Entries {
		metadataBytes += len(entry.ID) + len(entry.Title) + 32
		if metadataBytes > maxKnowledgeLexiconBytes {
			return nil, errors.New("knowledge lexicon exceeds 32MiB metadata budget")
		}
		for _, alias := range entry.Aliases {
			metadataBytes += len(alias.Text) + len(alias.Basis) + 32
			if metadataBytes > maxKnowledgeLexiconBytes {
				return nil, errors.New("knowledge lexicon exceeds 32MiB metadata budget")
			}
		}
	}
	if err := ValidateKnowledgeSnapshot(snapshot, snapshot.Binding); err != nil {
		return nil, err
	}
	selector := &KnowledgeSelector{binding: snapshot.Binding, entries: make(map[string]KnowledgeEntry, len(snapshot.Entries))}
	terms := map[string][]knowledgeTerm{}
	normalizedBytes := 0
	for _, entry := range snapshot.Entries {
		selector.entries[entry.ID] = cloneKnowledgeEntry(entry)
		for _, alias := range entry.Aliases {
			if alias.Basis != KnowledgeAliasSourceTitle && alias.Basis != KnowledgeAliasInferredCardName {
				continue
			}
			key := knowledgeNormalize(alias.Text)
			if strings.TrimSpace(key) != "" {
				if _, exists := terms[key]; !exists {
					if len(key) > maxKnowledgeLexiconBytes-normalizedBytes {
						return nil, errors.New("knowledge lexicon exceeds normalized dictionary budget")
					}
					normalizedBytes += len(key)
				}
				terms[key] = append(terms[key], knowledgeTerm{entryID: entry.ID, original: alias.Text, basis: alias.Basis})
			}
		}
	}
	keys := make([]string, 0, len(terms))
	for key := range terms {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	stateCount, err := knowledgeMatcherStateCount(keys)
	if err != nil {
		return nil, err
	}
	selector.nodes = make([]knowledgeMatcherNode, 1, stateCount)
	selector.nodes[0] = knowledgeMatcherNode{edgeStart: -1, terminal: -1, output: -1}
	buildEdges := make([]knowledgeBuildEdge, 0, stateCount-1)
	path := []int32{0}
	previous := ""
	for _, key := range keys {
		matches := terms[key]
		slices.SortFunc(matches, func(a, b knowledgeTerm) int {
			if c := strings.Compare(a.entryID, b.entryID); c != 0 {
				return c
			}
			if c := strings.Compare(a.original, b.original); c != 0 {
				return c
			}
			return strings.Compare(a.basis, b.basis)
		})
		pattern := len(selector.patterns)
		selector.patterns = append(selector.patterns, knowledgePattern{text: key, matches: slices.Compact(matches), generic: knowledgeGenericTerm(key)})
		// Sorted patterns share exactly the common prefix with the previous
		// path, so construction needs no per-state maps or edge searches.
		common := knowledgeCommonPrefix(previous, key)
		path = path[:common+1]
		state := path[common]
		for i := common; i < len(key); i++ {
			next := int32(len(selector.nodes))
			buildEdges = append(buildEdges, knowledgeBuildEdge{next: next, sibling: selector.nodes[state].edgeStart, value: key[i]})
			selector.nodes[state].edgeStart = int32(len(buildEdges) - 1)
			selector.nodes[state].edgeCount++
			selector.nodes = append(selector.nodes, knowledgeMatcherNode{edgeStart: -1, terminal: -1, output: -1})
			path = append(path, next)
			state = next
		}
		selector.nodes[state].terminal = int32(pattern)
		previous = key
	}
	selector.edges = make([]knowledgeMatcherEdge, 0, len(buildEdges))
	for i := range selector.nodes {
		start := len(selector.edges)
		for edge := selector.nodes[i].edgeStart; edge >= 0; edge = buildEdges[edge].sibling {
			selector.edges = append(selector.edges, knowledgeMatcherEdge{next: buildEdges[edge].next, value: buildEdges[edge].value})
		}
		// Child edges were prepended in lexical order; reverse their chain
		// into compact sorted adjacency for binary-search transitions.
		slices.Reverse(selector.edges[start:])
		selector.nodes[i].edgeStart = int32(start)
	}
	selector.buildFailureLinks()
	return selector, nil
}

func (selector *KnowledgeSelector) Select(req Request) (KnowledgeSelection, error) {
	selection := KnowledgeSelection{Anchors: []KnowledgeAnchor{}, Entries: []KnowledgeEntry{}}
	if selector == nil || len(selector.nodes) == 0 || selector.entries == nil {
		return selection, errors.New("knowledge selector is not initialized")
	}
	selection.Binding = selector.binding
	if _, _, err := ValidateRequest(req); err != nil {
		return selection, err
	}
	pool := knowledgeCandidatePool{candidates: make(knowledgeCandidateHeap, 0, MaxKnowledgeCandidates)}
	for turnIndex, turn := range req.Dialogue {
		view, boundaries := knowledgeNormalizedView(turn.Text)
		original := []rune(turn.Text)
		selector.scan(view, func(patternIndex, endByte int) {
			pattern := selector.patterns[patternIndex]
			startByte := endByte - len(pattern.text)
			start, startOK := boundaries[startByte]
			end, endOK := boundaries[endByte]
			if !startOK || !endOK || start >= end || !knowledgeWordBoundaries(view, startByte, endByte, pattern.text) || !knowledgeSingleHanBoundaries(original, start, end, pattern.text) {
				return
			}
			// Unique normalized patterns and whole normalization boundaries
			// make this one original span, regardless of how many entries match.
			pool.add(pattern.candidate(turn.TurnID, string(original[start:end]), start, end, turnIndex))
		})
	}
	candidates := pool.candidates
	slices.SortFunc(candidates, compareKnowledgeAnchors)
	if len(candidates) > MaxKnowledgeAnchors {
		candidates = candidates[:MaxKnowledgeAnchors]
	}
	selection.OmittedAnchorCount = pool.total - len(candidates)
	selectedEntries := map[string]bool{}
	for i, candidate := range candidates {
		anchor := candidate.anchor
		anchor.ID = fmt.Sprintf("anchor_%d", i+1)
		selection.Anchors = append(selection.Anchors, anchor)
		for _, entryID := range anchor.EntryIDs {
			selectedEntries[entryID] = true
		}
	}
	for _, entryID := range knowledgeSortedMapKeys(selectedEntries) {
		selection.Entries = append(selection.Entries, cloneKnowledgeEntry(selector.entries[entryID]))
	}
	return selection, nil
}

func (selector *KnowledgeSelector) buildFailureLinks() {
	queue := make([]int32, 0, len(selector.nodes))
	for _, edge := range selector.childEdges(0) {
		queue = append(queue, edge.next)
	}
	for head := 0; head < len(queue); head++ {
		state := queue[head]
		for _, edge := range selector.childEdges(state) {
			value, child := edge.value, edge.next
			failure := selector.nodes[state].failure
			for failure != 0 {
				if _, exists := selector.transition(failure, value); exists {
					break
				}
				failure = selector.nodes[failure].failure
			}
			if next, exists := selector.transition(failure, value); exists {
				selector.nodes[child].failure = next
			}
			ancestor := selector.nodes[child].failure
			if selector.nodes[ancestor].terminal >= 0 {
				selector.nodes[child].output = ancestor
			} else {
				selector.nodes[child].output = selector.nodes[ancestor].output
			}
			queue = append(queue, child)
		}
	}
}

// Aho-Corasick visits all overlapping and suffix patterns in a single pass.
// Failure/output links reference shared nodes rather than copying output lists.
func (selector *KnowledgeSelector) scan(text string, visit func(patternIndex, endByte int)) {
	state := int32(0)
	for i := range len(text) {
		value := text[i]
		next, exists := selector.transition(state, value)
		for !exists && state != 0 {
			state = selector.nodes[state].failure
			next, exists = selector.transition(state, value)
		}
		if exists {
			state = next
		} else {
			state = 0
		}
		if pattern := selector.nodes[state].terminal; pattern >= 0 {
			visit(int(pattern), i+1)
		}
		for output := selector.nodes[state].output; output >= 0; output = selector.nodes[output].output {
			visit(int(selector.nodes[output].terminal), i+1)
		}
	}
}

func knowledgeMatcherStateCount(keys []string) (int, error) {
	count := 1
	previous := ""
	for _, key := range keys {
		needed := len(key) - knowledgeCommonPrefix(previous, key)
		if needed > maxKnowledgeMatcherStates-count {
			return 0, errors.New("knowledge matcher exceeds state budget")
		}
		count += needed
		previous = key
	}
	return count, nil
}

func knowledgeCommonPrefix(a, b string) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}

func (selector *KnowledgeSelector) childEdges(state int32) []knowledgeMatcherEdge {
	node := selector.nodes[state]
	return selector.edges[node.edgeStart : node.edgeStart+int32(node.edgeCount)]
}

func (selector *KnowledgeSelector) transition(state int32, value byte) (int32, bool) {
	edges := selector.childEdges(state)
	low, high := 0, len(edges)
	for low < high {
		middle := low + (high-low)/2
		if edges[middle].value < value {
			low = middle + 1
		} else {
			high = middle
		}
	}
	if low < len(edges) && edges[low].value == value {
		return edges[low].next, true
	}
	return 0, false
}

func (pattern knowledgePattern) candidate(turnID, quote string, start, end, turnIndex int) knowledgeAnchorCandidate {
	candidate := knowledgeAnchorCandidate{anchor: KnowledgeAnchor{TurnID: turnID, Text: quote, StartRune: start, EndRune: end, EntryIDs: []string{}, EntryMatches: []KnowledgeEntryMatch{}}, turnIndex: turnIndex, priority: 100}
	// Constructor sorting groups aliases by entry. Keep only the best method
	// for each entry without rebuilding a map at every occurrence of a word.
	for _, match := range pattern.matches {
		kind, priority := knowledgeMatchKind(match, quote)
		last := len(candidate.anchor.EntryMatches) - 1
		if last >= 0 && candidate.anchor.EntryMatches[last].EntryID == match.entryID {
			previous := candidate.anchor.EntryMatches[last].MatchKind
			if knowledgeMatchPriority(kind) < knowledgeMatchPriority(previous) {
				candidate.anchor.EntryMatches[last].MatchKind = kind
			}
		} else {
			candidate.anchor.EntryIDs = append(candidate.anchor.EntryIDs, match.entryID)
			candidate.anchor.EntryMatches = append(candidate.anchor.EntryMatches, KnowledgeEntryMatch{EntryID: match.entryID, MatchKind: kind})
		}
		if pattern.generic {
			priority += 4
		}
		if priority < candidate.priority || (priority == candidate.priority && kind < candidate.anchor.MatchKind) {
			candidate.anchor.MatchKind, candidate.priority = kind, priority
		}
	}
	return candidate
}

func knowledgeMatchPriority(kind string) int {
	switch kind {
	case "source_title_exact":
		return 0
	case "source_title_normalized":
		return 1
	case "inferred_card_name_exact":
		return 2
	default:
		return 3
	}
}
