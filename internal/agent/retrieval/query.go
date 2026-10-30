package retrieval

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type literalClause struct {
	text   string
	phrase bool
}

// This optional port keeps engine analysis out of public evidence fields. Only
// the normal ALL hits may be candidates; the additional count is query metadata.
type literalAnalyzer interface {
	LiteralAnalyzed(context.Context, domain.Query) ([]domain.Evidence, int, error)
}

type literalVerification struct {
	clauses    []literalClause
	indexed    map[string]string
	queryWords int
}

func literalCandidateKey(e domain.Evidence) string {
	if len(e.Sources) != 1 {
		return ""
	}
	return e.ID + ":" + refKey(e.Sources[0])
}

func (v literalVerification) hasUnquoted(cjkOnly bool) bool {
	for _, c := range v.clauses {
		if !c.phrase && (!cjkOnly || strings.ContainsFunc(c.text, han)) {
			return true
		}
	}
	return false
}

func (v literalVerification) matches(original string, e domain.Evidence) bool {
	indexed, ok := v.indexed[literalCandidateKey(e)]
	engineWords := e.Channel == "literal" && ok && indexed == original && v.queryWords > 0 && v.queryWords < 10
	normalized := strings.Join(strings.Fields(strings.ToLower(original)), " ")
	for _, clause := range v.clauses {
		if clause.phrase {
			if !strings.Contains(original, clause.text) {
				return false
			}
		} else if strings.ContainsFunc(clause.text, han) {
			// Delegate segmentation to the actual index. Never approximate
			// Chinese terms using runs, character sets, or raw substrings.
			if !engineWords {
				return false
			}
		} else if !containsComplete(normalized, clause.text) {
			return false
		}
	}
	return true
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) })
}

func parseLiteral(text string) ([]literalClause, error) {
	var clauses []literalClause
	var segment strings.Builder
	quoted := false
	flush := func() {
		s := segment.String()
		segment.Reset()
		if quoted {
			if len(words(s)) > 0 {
				clauses = append(clauses, literalClause{s, true})
			}
		} else {
			for _, word := range words(s) {
				clauses = append(clauses, literalClause{word, false})
			}
		}
	}
	for _, r := range text {
		if r == '"' {
			flush()
			quoted = !quoted
		} else {
			segment.WriteRune(r)
		}
	}
	if quoted {
		return nil, fmt.Errorf("%w: unclosed literal phrase", domain.ErrInvalid)
	}
	flush()
	if len(clauses) == 0 {
		return nil, fmt.Errorf("%w: query requires words", domain.ErrInvalid)
	}
	return clauses, nil
}

// Ten deduplicated terms and 128 runes bound keyword and embedding input.
// Uniform sampling retains the tail of long questions deterministically.
func shortExpression(text string) string {
	tokens := words(text)
	unique := make([]string, 0, 10)
	seen := map[string]bool{}
	for _, token := range tokens {
		if !seen[token] {
			seen[token] = true
			unique = append(unique, token)
		}
	}
	chosen := unique
	if len(unique) > 10 {
		chosen = make([]string, 10)
		for i := range chosen {
			chosen[i] = unique[i*(len(unique)-1)/9]
		}
	}
	out := []rune(strings.Join(chosen, " "))
	if len(out) > 128 {
		out = out[:128]
	}
	return string(out)
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) }
func han(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}
func containsComplete(text, term string) bool {
	first, _ := utf8.DecodeRuneInString(term)
	last, _ := utf8.DecodeLastRuneInString(term)
	for offset := 0; offset <= len(text)-len(term); {
		next := strings.Index(text[offset:], term)
		if next < 0 {
			return false
		}
		start := offset + next
		end := start + len(term)
		left := start == 0 || han(first)
		if !left {
			r, _ := utf8.DecodeLastRuneInString(text[:start])
			left = !wordRune(r) || han(r)
		}
		right := end == len(text) || han(last)
		if !right {
			r, _ := utf8.DecodeRuneInString(text[end:])
			right = !wordRune(r) || han(r)
		}
		if left && right {
			return true
		}
		offset = start + len(term)
	}
	return false
}
