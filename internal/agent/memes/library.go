// Package memes retrieves optional public references from a frozen local corpus.
// It never reads conversation history, the network, or a model.
package memes

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

//go:embed library.json
var embeddedLibrary []byte

const (
	maxCandidates   = 8
	maxDisplayBytes = 6 << 10
)

// Candidate is untrusted reference material, not an instruction or a decision
// to participate. Conditions remain attached when a record provides them.
type Candidate struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Meaning    string   `json:"meaning"`
	Examples   []string `json:"examples,omitempty"`
	SourceKind string   `json:"source_kind"`
	SourceURL  string   `json:"source_url,omitempty"`
	WhenToUse  []string `json:"when_to_use,omitempty"`
}

type Metadata struct {
	Count  int
	SHA256 string
}

type indexedRecord struct {
	record       Candidate
	title, body  map[string]bool
	retrievalAll [][]string
}

type corpusRecord struct {
	Candidate
	RetrievalAll [][]string `json:"retrieval_all,omitempty"`
}

// Library keeps all backing storage private; Retrieve returns bounded copies.
type Library struct {
	records   []indexedRecord
	frequency map[string]int
	metadata  Metadata
}

var defaultLibrary = sync.OnceValues(func() (*Library, error) { return load(embeddedLibrary) })

func Default() (*Library, error) { return defaultLibrary() }

func load(raw []byte) (*Library, error) {
	var records []corpusRecord
	if json.Unmarshal(raw, &records) != nil || len(records) == 0 {
		return nil, errors.New("invalid meme corpus")
	}
	sum := sha256.Sum256(raw)
	library := &Library{frequency: map[string]int{}, metadata: Metadata{Count: len(records), SHA256: hex.EncodeToString(sum[:])}}
	seen := map[string]bool{}
	for _, record := range records {
		if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Title) == "" || strings.TrimSpace(record.Meaning) == "" || strings.TrimSpace(record.SourceKind) == "" || seen[record.ID] {
			return nil, errors.New("invalid meme corpus record")
		}
		if record.SourceURL != "" {
			u, err := url.Parse(record.SourceURL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
				return nil, errors.New("invalid meme corpus provenance")
			}
		}
		for _, group := range record.RetrievalAll {
			if len(group) == 0 {
				return nil, errors.New("invalid meme corpus conditions")
			}
			for i, alias := range group {
				alias = strings.ToLower(strings.TrimSpace(alias))
				if utf8.RuneCountInString(alias) < 2 {
					return nil, errors.New("invalid meme corpus condition alias")
				}
				group[i] = alias
			}
		}
		seen[record.ID] = true
		title := terms(record.Title)
		body := terms(record.Title + " " + record.Meaning + " " + strings.Join(record.WhenToUse, " "))
		for term := range body {
			library.frequency[term]++
		}
		library.records = append(library.records, indexedRecord{record: record.Candidate, title: title, body: body, retrievalAll: record.RetrievalAll})
	}
	return library, nil
}

func (l *Library) Metadata() Metadata { return l.metadata }

// Retrieve ranks only lexical evidence in the actual utterance. Ordinary
// records require multiple informative title matches covering at least half
// the title; description overlap alone is not a reason to surface a joke.
// Explicit applicability conditions can admit curated contextual references.
func (l *Library) Retrieve(utterance string) []Candidate {
	utterance = strings.ToLower(clip(utterance, 4096))
	query := terms(utterance)
	if len(query) == 0 {
		return nil
	}
	type match struct {
		index int
		score int
	}
	var ranked []match
	for i, item := range l.records {
		conditionsMet := true
		for _, group := range item.retrievalAll {
			matched := false
			for _, alias := range group {
				if strings.Contains(utterance, alias) {
					matched = true
					break
				}
			}
			if !matched {
				conditionsMet = false
				break
			}
		}
		if !conditionsMet {
			continue
		}
		informativeTitleTerms := 0
		for term := range item.title {
			if frequency := l.frequency[term]; frequency > 0 && frequency <= len(l.records)/8 {
				informativeTitleTerms++
			}
		}
		score, titleHits := 0, 0
		for term := range query {
			frequency := l.frequency[term]
			if frequency == 0 || frequency > len(l.records)/8 {
				continue
			}
			// Integer weights make addition independent of Go map iteration order.
			weight := int(100 * math.Log2(1+float64(len(l.records))/float64(frequency)))
			if item.title[term] {
				titleHits++
				score += 6 * weight
			} else if item.body[term] {
				score += weight
			}
		}
		if len(item.retrievalAll) > 0 || (titleHits >= 2 && titleHits*2 >= informativeTitleTerms) {
			ranked = append(ranked, match{index: i, score: score})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return l.records[ranked[i].index].record.ID < l.records[ranked[j].index].record.ID
	})
	var selected []Candidate
	for _, match := range ranked {
		candidate := display(l.records[match.index].record)
		next := append(selected, candidate)
		raw, _ := json.Marshal(next)
		if len(raw) > maxDisplayBytes {
			continue
		}
		selected = next
		if len(selected) == maxCandidates {
			break
		}
	}
	return selected
}

func terms(text string) map[string]bool {
	result := map[string]bool{}
	var previous rune
	var word strings.Builder
	flush := func() {
		if word.Len() >= 2 {
			result[word.String()] = true
		}
		word.Reset()
	}
	for _, r := range strings.ToLower(text) {
		if unicode.Is(unicode.Han, r) {
			flush()
			if previous != 0 {
				result[string([]rune{previous, r})] = true
			}
			previous = r
		} else {
			previous = 0
			if r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
				word.WriteRune(r)
			} else {
				flush()
			}
		}
	}
	flush()
	return result
}

func display(record Candidate) Candidate {
	result := Candidate{ID: clip(record.ID, 192), Title: clip(record.Title, 192), Meaning: clip(record.Meaning, 900), SourceKind: clip(record.SourceKind, 64), SourceURL: clip(record.SourceURL, 512)}
	if len(record.Examples) > 0 {
		result.Examples = []string{clip(record.Examples[0], 240)}
	}
	for _, condition := range record.WhenToUse {
		if len(result.WhenToUse) == 4 {
			break
		}
		result.WhenToUse = append(result.WhenToUse, clip(condition, 192))
	}
	return result
}

func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
