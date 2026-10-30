package memes

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

var relationVocabulary = map[string]bool{"goal_means": true, "contrast": true, "before_after": true, "benefit_cost": true, "attribution": true, "role_pattern": true, "promise_credibility": true, "opportunity_action": true}
var emotionVocabulary = map[string]bool{"joy": true, "happy": true, "sadness": true, "anger": true, "fear": true, "surprise": true, "disgust": true, "neutral": true, "anticipation": true, "trust": true, "confusion": true, "relief": true, "frustration": true, "embarrassment": true, "disappointment": true, "pride": true, "hope": true, "regret": true, "anxiety": true, "confidence": true, "doubt": true, "mocking": true, "approval": true, "resistance": true, "acceptance": true, "开心": true, "难过": true, "愤怒": true, "害怕": true, "惊讶": true, "困惑": true, "释然": true, "失望": true, "尴尬": true, "期待": true, "怀疑": true, "抵触": true, "接受": true}

func RenderRelations(relations []Relation) string {
	parts := make([]string, 0, len(relations))
	for _, r := range relations {
		parts = append(parts, r.Kind+": "+strings.TrimSpace(r.Pattern))
	}
	return strings.Join(parts, "\n")
}
func RenderEmotionChains(chains []EmotionChain) string {
	parts := make([]string, 0, len(chains))
	for _, c := range chains {
		parts = append(parts, c.Subject+": "+strings.Join(c.States, " → "))
	}
	return strings.Join(parts, "\n")
}
func RenderKeys(keys []string) string { return strings.Join(keys, " | ") }
func validRelation(r Relation) bool {
	return relationVocabulary[r.Kind] && strings.TrimSpace(r.Pattern) != "" && len([]rune(r.Pattern)) <= 1024
}
func validEmotion(c EmotionChain) bool {
	if strings.TrimSpace(c.Subject) == "" || len(c.States) == 0 || len(c.States) > 8 {
		return false
	}
	for _, s := range c.States {
		if !emotionVocabulary[s] {
			return false
		}
	}
	return true
}

func CompileCard(card UseCard, p Projection) (IndexRecord, error) {
	if err := ValidateCard(card); err != nil {
		return IndexRecord{}, err
	}
	fp, err := Fingerprint(card)
	if err != nil {
		return IndexRecord{}, err
	}
	if p.CardFingerprint != fp {
		return IndexRecord{}, errors.New("projection fingerprint does not match card")
	}
	if len(p.Relations) == 0 {
		return IndexRecord{}, errors.New("card projection requires an input relation")
	}
	for _, r := range p.Relations {
		if !validRelation(r) || len(r.Evidence) > 0 {
			return IndexRecord{}, errors.New("invalid card relation projection")
		}
	}
	for _, c := range p.EmotionChain {
		if !validEmotion(c) || len(c.Evidence) > 0 {
			return IndexRecord{}, errors.New("invalid card emotion projection")
		}
	}
	var keys []string
	for _, group := range p.Keys.RequiredAliasGroups {
		if len(group) == 0 {
			return IndexRecord{}, errors.New("empty required alias group")
		}
		for _, key := range group {
			if strings.TrimSpace(key) == "" {
				return IndexRecord{}, errors.New("empty projection key")
			}
		}
		keys = append(keys, strings.Join(group, " / "))
	}
	for _, key := range p.Keys.Related {
		if strings.TrimSpace(key) == "" {
			return IndexRecord{}, errors.New("empty related key")
		}
		keys = append(keys, key)
	}
	index := IndexRecord{UseID: card.UseID, Relation: RenderRelations(p.Relations), EmotionChain: RenderEmotionChains(p.EmotionChain), Keys: RenderKeys(keys), Full: card, Projection: p}
	index.Fingerprint, err = Fingerprint(index)
	if err != nil {
		return IndexRecord{}, err
	}
	raw, err := json.Marshal(index)
	if err != nil {
		return IndexRecord{}, err
	}
	var frozen IndexRecord
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return IndexRecord{}, err
	}
	return frozen, nil
}

func CompileQueryPlan(a QueryAnalysis, r Request, s []SourceFragment) (QueryPlan, error) {
	plan := QueryPlan{Analysis: QueryAnalysis{Relations: []Relation{}, EmotionChain: []EmotionChain{}, Keys: []Key{}}, Queries: []SearchQuery{}, Warnings: []Warning{}, Discarded: []SearchQuery{}}
	if len(a.Relations) > 32 || len(a.EmotionChain) > 32 || len(a.Keys) > 32 {
		return plan, errors.New("query analysis exceeds element limit")
	}
	counts := map[Field]int{}
	seen := map[string]bool{}
	turnPositions := map[string]int{}
	fragmentPositions := map[int]int{}
	for i, turn := range r.Dialogue {
		turnPositions[turn.TurnID] = i
	}
	for _, fragment := range s {
		fragmentPositions[fragment.SourceID] = turnPositions[fragment.TurnID]
	}
	type pendingQuery struct {
		query   SearchQuery
		recency int
	}
	var pending []pendingQuery
	recency := func(evidence []Evidence) int {
		latest := -1
		for _, ref := range evidence {
			if position, ok := fragmentPositions[ref.SourceID]; ok && position > latest {
				latest = position
			}
		}
		return latest
	}
	enqueue := func(q SearchQuery, evidence []Evidence) {
		pending = append(pending, pendingQuery{query: q, recency: recency(evidence)})
	}
	add := func(q SearchQuery) {
		key := string(q.Field) + "\x00" + q.Text
		if seen[key] {
			plan.Warnings = append(plan.Warnings, Warning{Code: "DUPLICATE_QUERY", Detail: q.Text})
			plan.Discarded = append(plan.Discarded, q)
			return
		}
		seen[key] = true
		if counts[q.Field] >= FieldQueryBudget(q.Field) {
			plan.Warnings = append(plan.Warnings, Warning{Code: "QUERY_BUDGET", Detail: q.Text})
			plan.Discarded = append(plan.Discarded, q)
			return
		}
		counts[q.Field]++
		q.ID = fmt.Sprintf("%s_%d", q.Field, counts[q.Field])
		plan.Queries = append(plan.Queries, q)
	}
	for i, rel := range a.Relations {
		if !validRelation(rel) {
			return plan, errors.New("invalid core relation type or pattern")
		}
		if err := ValidateEvidence(rel.Evidence, s, r.Dialogue, false); err != nil {
			return plan, fmt.Errorf("core relation evidence: %w", err)
		}
		plan.Analysis.Relations = append(plan.Analysis.Relations, rel)
		enqueue(SearchQuery{Field: FieldRelation, Text: RenderRelations([]Relation{rel}), RelationKinds: []string{rel.Kind}, ElementIndices: []int{i}}, rel.Evidence)
	}
	fragments := map[int]SourceFragment{}
	turns := map[string]Turn{}
	for _, f := range s {
		fragments[f.SourceID] = f
	}
	for _, t := range r.Dialogue {
		turns[t.TurnID] = t
	}
	for i, chain := range a.EmotionChain {
		err := ValidateEvidence(chain.Evidence, s, r.Dialogue, false)
		if err == nil && !validEmotion(chain) {
			err = errors.New("invalid emotion label or subject")
		}
		if err == nil {
			for _, ref := range chain.Evidence {
				fragment := fragments[ref.SourceID]
				if turns[fragment.TurnID].Speaker != chain.Subject && !strings.Contains(fragment.Quote, chain.Subject) {
					err = errors.New("emotion subject has no explicit linkage to cited speaker or text")
					break
				}
			}
		}
		if err != nil {
			plan.Warnings = append(plan.Warnings, Warning{Code: "INVALID_OPTIONAL_EMOTION", Detail: err.Error()})
			continue
		}
		plan.Analysis.EmotionChain = append(plan.Analysis.EmotionChain, chain)
		enqueue(SearchQuery{Field: FieldEmotion, Text: RenderEmotionChains([]EmotionChain{chain}), ElementIndices: []int{i}}, chain.Evidence)
	}
	hasCurrentKey := false
	for i, key := range a.Keys {
		err := ValidateEvidence(key.Evidence, s, r.Dialogue, false)
		if err == nil && strings.TrimSpace(key.Text) == "" {
			err = errors.New("empty key")
		}
		if err == nil {
			for _, ref := range key.Evidence {
				if !strings.Contains(fragments[ref.SourceID].Quote, key.Text) {
					err = errors.New("key is not literal in cited evidence")
					break
				}
			}
		}
		if err != nil {
			plan.Warnings = append(plan.Warnings, Warning{Code: "INVALID_OPTIONAL_KEY", Detail: err.Error()})
			continue
		}
		plan.Analysis.Keys = append(plan.Analysis.Keys, key)
		hasCurrentKey = hasCurrentKey || recency(key.Evidence) == len(r.Dialogue)-1
		enqueue(SearchQuery{Field: FieldKeys, Text: RenderKeys([]string{key.Text}), ElementIndices: []int{i}}, key.Evidence)
	}
	if text := originalDialogueQuery(r.Dialogue); text != "" {
		if !hasCurrentKey {
			// Preserve a literal current-turn search even when the model returns
			// no usable keys. This is original query text, not invented analysis
			// or repaired evidence, and it consumes the existing keys budget.
			pending = append(pending, pendingQuery{query: SearchQuery{Field: FieldKeys, Text: text, ElementIndices: []int{}}, recency: len(r.Dialogue)})
		}
		pending = append(pending, pendingQuery{query: SearchQuery{Field: FieldDescription, Text: text, ElementIndices: []int{}}, recency: len(r.Dialogue) - 1})
	}
	// Validate all supplied evidence before budgeting, then prefer facts tied
	// to newer turns rather than the model's arbitrary array order.
	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].query.Field != pending[j].query.Field {
			return fieldOrder(pending[i].query.Field) < fieldOrder(pending[j].query.Field)
		}
		return pending[i].recency > pending[j].recency
	})
	for _, candidate := range pending {
		add(candidate.query)
	}
	// Preserve the dimension ordering independently of map iteration.
	sort.SliceStable(plan.Queries, func(i, j int) bool { return fieldOrder(plan.Queries[i].Field) < fieldOrder(plan.Queries[j].Field) })
	raw, err := json.Marshal(plan)
	if err != nil {
		return plan, err
	}
	var frozen QueryPlan
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return plan, err
	}
	return frozen, nil
}
func fieldOrder(f Field) int {
	switch f {
	case FieldRelation:
		return 0
	case FieldKeys:
		return 1
	case FieldEmotion:
		return 2
	case FieldDescription:
		return 3
	}
	return 4
}

// Anchor the original-description lane to the latest whole utterance. Earlier
// turns remain available to the query model for causal and referential analysis,
// but cannot numerically overwhelm a newly introduced topic in this vector.
func originalDialogueQuery(dialogue []Turn) string {
	if len(dialogue) == 0 {
		return ""
	}
	text := dialogue[len(dialogue)-1].Text
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || len(text) > MaxInputBytes || utf8.RuneCountInString(text) > MaxInputRunes {
		return ""
	}
	return text
}
