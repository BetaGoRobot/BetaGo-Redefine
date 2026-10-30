package memesource

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

// Only these explicit literal constraints may consume the existing structure
// repair call. CompileWikiEncoding remains the final authority for all cards.
type encodingFormatError struct{ diagnostic string }

func (e *encodingFormatError) Error() string { return e.diagnostic }

func formatError(path, constraint string, args ...any) error {
	return &encodingFormatError{path + ": " + fmt.Sprintf(constraint, args...)}
}

var conditionIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

func canonicalRequirements(requirements []memes.Requirement) []memes.Requirement {
	if requirements == nil {
		return nil
	}
	result := append([]memes.Requirement{}, requirements...)
	counts := map[string]int{}
	for _, requirement := range result {
		counts[requirement.ID]++
	}
	reserved := map[string]bool{}
	for _, requirement := range result {
		if conditionIdentifier.MatchString(requirement.ID) && counts[requirement.ID] == 1 {
			reserved[requirement.ID] = true
		}
	}
	for i := range result {
		if conditionIdentifier.MatchString(result[i].ID) && counts[result[i].ID] == 1 {
			continue
		}
		for ordinal := i + 1; ; ordinal++ {
			candidate := fmt.Sprintf("condition_%d", ordinal)
			if !reserved[candidate] {
				result[i].ID = candidate
				reserved[candidate] = true
				break
			}
		}
	}
	return result
}

// A missing or wrongly typed field can be a structural draft. An explicitly
// empty core string still lacks semantics, even alongside another schema error.
func explicitEmptyCore(raw []byte) error {
	if len(raw) > 1024*1024 {
		return nil
	}
	var draft struct {
		Cards []json.RawMessage `json:"cards"`
	}
	if json.Unmarshal(raw, &draft) != nil {
		return nil // The normal strict decoder owns malformed JSON diagnostics.
	}
	for i, rawCard := range draft.Cards {
		var card struct {
			Full json.RawMessage `json:"full"`
		}
		var full map[string]json.RawMessage
		if json.Unmarshal(rawCard, &card) != nil || json.Unmarshal(card.Full, &full) != nil {
			continue // One malformed card must not hide another card's semantics.
		}
		for _, field := range []string{"name", "input_relation", "response_act"} {
			var value any
			if json.Unmarshal(full[field], &value) != nil {
				continue
			}
			if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
				return fmt.Errorf("card missing semantic field: %s at $.cards[%d].full.%s", field, i, field)
			}
		}
	}
	return nil
}

// Scan every card before returning a repairable error: a formatting issue in
// one card must never hide absent core semantics or evidence in another card.
// When decoding failed, absent fields remain structural; explicit evidence and
// the authoritative source can still be rejected without a repair attempt.
func encodingSemanticGuard(source Source, encoded memes.EncodedWiki, decoded bool) error {
	wiki := source.WikiSource()
	parsed, err := url.Parse(wiki.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(wiki.ID) == "" || strings.TrimSpace(wiki.Title) == "" || strings.TrimSpace(wiki.Description) == "" {
		return fmt.Errorf("Wiki source requires id, title, absolute HTTP URL and original description")
	}
	if len(wiki.Description) > 128*1024 {
		return fmt.Errorf("Wiki source description exceeds 128KiB")
	}
	for i, use := range encoded.Cards {
		if decoded {
			for _, field := range []struct{ name, value string }{{"name", use.Full.Name}, {"input_relation", use.Full.InputRelation}, {"response_act", use.Full.ResponseAct}} {
				if strings.TrimSpace(field.value) == "" {
					return fmt.Errorf("card missing semantic field: %s at $.cards[%d].full.%s", field.name, i, field.name)
				}
			}
		}
		for j, relation := range use.Projection.Relations {
			if len(relation.Evidence) > 0 {
				return fmt.Errorf("$.cards[%d].projection.relations[%d].evidence: offline projection must not contain evidence", i, j)
			}
		}
		for j, chain := range use.Projection.EmotionChain {
			if len(chain.Evidence) > 0 {
				return fmt.Errorf("$.cards[%d].projection.emotion_chain[%d].evidence: offline projection must not contain evidence", i, j)
			}
		}
	}
	return nil
}

const relationFormatValues = "goal_means contrast before_after benefit_cost attribution role_pattern promise_credibility opportunity_action"
const emotionFormatValues = "joy happy sadness anger fear surprise disgust neutral anticipation trust confusion relief frustration embarrassment disappointment pride hope regret anxiety confidence doubt mocking approval resistance acceptance 开心 难过 愤怒 害怕 惊讶 困惑 释然 失望 尴尬 期待 怀疑 抵触 接受"

// Values in diagnostics are fixed vocabulary, never rejected model text.
func encodingFormatCheck(encoded memes.EncodedWiki) error {
	if len(encoded.Cards) < 1 || len(encoded.Cards) > 8 {
		return formatError("$.cards", "expected 1..8 uses, got %d", len(encoded.Cards))
	}
	for i, use := range encoded.Cards {
		full := fmt.Sprintf("$.cards[%d].full", i)
		for j, requirement := range use.Full.Requirements {
			path := fmt.Sprintf("%s.requirements[%d].any_of", full, j)
			if len(requirement.AnyOf) == 0 {
				return formatError(path, "expected at least 1 alternative, got 0")
			}
			for k, branch := range requirement.AnyOf {
				if strings.TrimSpace(branch) == "" {
					return formatError(fmt.Sprintf("%s[%d]", path, k), "must be nonempty")
				}
			}
		}
		projection := fmt.Sprintf("$.cards[%d].projection", i)
		if len(use.Projection.Relations) == 0 {
			return formatError(projection+".relations", "expected at least 1 relation, got 0")
		}
		for j, relation := range use.Projection.Relations {
			path := fmt.Sprintf("%s.relations[%d]", projection, j)
			if !slices.Contains(strings.Fields(relationFormatValues), relation.Kind) {
				return formatError(path+".kind", "outside allowed values (%s)", relationFormatValues)
			}
			if strings.TrimSpace(relation.Pattern) == "" {
				return formatError(path+".pattern", "must be nonempty")
			}
			if count := utf8.RuneCountInString(relation.Pattern); count > 1024 {
				return formatError(path+".pattern", "expected at most 1024 runes, got %d", count)
			}
		}
		for j, chain := range use.Projection.EmotionChain {
			path := fmt.Sprintf("%s.emotion_chain[%d]", projection, j)
			if strings.TrimSpace(chain.Subject) == "" {
				return formatError(path+".subject", "must be nonempty")
			}
			if len(chain.States) < 1 || len(chain.States) > 8 {
				return formatError(path+".states", "expected 1..8 states, got %d", len(chain.States))
			}
			for k, state := range chain.States {
				if !slices.Contains(strings.Fields(emotionFormatValues), state) {
					return formatError(fmt.Sprintf("%s.states[%d]", path, k), "outside allowed values (%s)", emotionFormatValues)
				}
			}
		}
		for j, group := range use.Projection.Keys.RequiredAliasGroups {
			path := fmt.Sprintf("%s.keys.required_alias_groups[%d]", projection, j)
			if len(group) == 0 {
				return formatError(path, "expected at least 1 alias, got 0")
			}
			for k, alias := range group {
				if strings.TrimSpace(alias) == "" {
					return formatError(fmt.Sprintf("%s[%d]", path, k), "must be nonempty")
				}
			}
		}
		for j, key := range use.Projection.Keys.Related {
			if strings.TrimSpace(key) == "" {
				return formatError(fmt.Sprintf("%s.keys.related[%d]", projection, j), "must be nonempty")
			}
		}
	}
	return nil
}
