package meili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
func anchorFilter(prefix string, at time.Time) string {
	return fmt.Sprintf("(%s_sec < %d OR (%s_sec = %d AND %s_nano <= %d))", prefix, at.Unix(), prefix, at.Unix(), prefix, at.Nanosecond())
}

func (c *Client) filter(q domain.Query, contexts bool) string {
	// Raw string equality in Meili case-folds. Lowercase hex hashes enforce the
	// exact provider identities before its candidate limit is applied.
	parts := []string{"scope_hash = " + quote(q.Scope.Key()), "tenant_id = " + quote(q.Scope.TenantID), "app_id = " + quote(q.Scope.AppID), "chat_id = " + quote(q.Scope.ChatID), "visibility = " + quote(q.Scope.Visibility), anchorFilter("occurred", q.Anchor), anchorFilter("updated", q.Anchor)}
	if q.AuthorID != "" {
		key := "author_id"
		hashKey := "author_hash"
		if contexts {
			key = "author_ids"
			hashKey = "author_hashes"
		}
		parts = append(parts, key+" = "+quote(q.AuthorID), hashKey+" = "+quote(authorHash(q.Scope, q.AuthorID)))
	}
	if contexts {
		parts = append(parts, "invalidated = false", "embedding_model = "+quote(c.model))
	}
	return strings.Join(parts, " AND ")
}

// Literal preserves caller phrase syntax and requires all terms. It only reads
// the messages index, with no vector, model, or reranker operations.
func (c *Client) Literal(ctx context.Context, q domain.Query) ([]domain.Evidence, error) {
	return c.search(ctx, q, "literal", nil)
}

// LiteralAnalyzed preserves the ALL search as the only evidence source. A
// separate LAST query exposes the engine's effective word count, which ALL's
// optimized ranking omits. Its relaxed hits never contribute text or sources.
// Count 10 is ambiguous: Meili may have truncated a longer query at ten words.
func (c *Client) LiteralAnalyzed(ctx context.Context, q domain.Query) ([]domain.Evidence, int, error) {
	ctx, cancel := c.operation(ctx)
	defer cancel()
	hits, err := c.Literal(ctx, q)
	if err != nil || len(hits) == 0 || strings.TrimSpace(q.Text) == "" {
		return hits, 0, err
	}
	body := map[string]any{"q": q.Text, "filter": c.filter(q, false), "limit": 1, "matchingStrategy": "last", "showRankingScoreDetails": true, "attributesToRetrieve": []string{"id"}}
	var result struct {
		Hits []struct {
			Details struct {
				Words *struct {
					Matching *int `json:"matchingWords"`
					Maximum  *int `json:"maxMatchingWords"`
				} `json:"words"`
			} `json:"_rankingScoreDetails"`
		} `json:"hits"`
	}
	if err := c.request(ctx, http.MethodPost, "/indexes/"+c.messages+"/search", body, &result); err != nil {
		return nil, 0, err
	}
	if len(result.Hits) != 1 {
		return nil, 0, errors.New("meili literal analysis missing word count")
	}
	words := result.Hits[0].Details.Words
	if words == nil || words.Matching == nil || words.Maximum == nil || *words.Matching <= 0 || *words.Maximum <= 0 || *words.Maximum > 65536 || *words.Matching > *words.Maximum {
		return nil, 0, errors.New("meili literal analysis invalid word count")
	}
	return hits, *words.Maximum, nil
}

// Keyword returns context keyword candidates independently of vector search.
func (c *Client) Keyword(ctx context.Context, q domain.Query) ([]domain.Evidence, error) {
	return c.search(ctx, q, "keyword", nil)
}

// Vector is a pure external-vector search; the query text does not contribute.
func (c *Client) Vector(ctx context.Context, q domain.Query, vector []float32) ([]domain.Evidence, error) {
	if err := c.validateVector(vector); err != nil {
		return nil, err
	}
	return c.search(ctx, q, "vector", vector)
}

func (c *Client) search(ctx context.Context, q domain.Query, channel string, vector []float32) ([]domain.Evidence, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	contexts := channel != "literal"
	index := c.messages
	if contexts {
		index = c.contexts
	}
	body := map[string]any{"q": q.Text, "filter": c.filter(q, contexts), "limit": q.Limit, "matchingStrategy": "all", "showRankingScore": true}
	if channel == "vector" {
		body["q"] = ""
		body["vector"] = vector
		body["hybrid"] = map[string]any{"embedder": "external", "semanticRatio": 1}
	}
	ctx, cancel := c.operation(ctx)
	defer cancel()
	var result struct {
		Hits []json.RawMessage `json:"hits"`
	}
	if err := c.request(ctx, http.MethodPost, "/indexes/"+index+"/search", body, &result); err != nil {
		return nil, err
	}
	hits := make([]domain.Evidence, 0, len(result.Hits))
	for _, raw := range result.Hits {
		var e domain.Evidence
		var ok bool
		if contexts {
			e, ok = c.contextHit(raw, q)
		} else {
			e, ok = messageHit(raw, q)
		}
		if ok {
			e.Channel = channel
			hits = append(hits, e)
		}
		if len(hits) == q.Limit {
			break
		}
	}
	return hits, nil
}

func (m metadata) valid(q domain.Query, id string) bool {
	return m.SchemaVersion == 1 && m.ScopeHash == q.Scope.Key() && m.scopeDTO.domain() == q.Scope && m.ID == documentID(q.Scope, id) && !m.UpdatedAt.IsZero() && !m.UpdatedAt.After(q.Anchor) && m.UpdatedSec == m.UpdatedAt.Unix() && m.UpdatedNano == m.UpdatedAt.Nanosecond() && m.OccurredNano >= 0 && m.OccurredNano < 1e9 && (m.Score == nil || (!math.IsNaN(*m.Score) && !math.IsInf(*m.Score, 0)))
}
func (m metadata) score() float64 {
	if m.Score == nil {
		return 0
	}
	return *m.Score
}

func messageHit(raw json.RawMessage, q domain.Query) (domain.Evidence, bool) {
	var d messageDTO
	if json.Unmarshal(raw, &d) != nil {
		return domain.Evidence{}, false
	}
	s, err := d.Source.domain()
	if err != nil || !d.valid(q, d.MessageID) || s.Scope != q.Scope || d.MessageID != s.MessageID || d.AuthorID != s.AuthorID || (q.AuthorID != "" && q.AuthorID != s.AuthorID) || s.OccurredAt.After(q.Anchor) || d.UpdatedAt.Before(s.OccurredAt) || d.OccurredSec != s.OccurredAt.Unix() || d.OccurredNano != s.OccurredAt.Nanosecond() {
		return domain.Evidence{}, false
	}
	return domain.Evidence{ID: d.ID, Text: d.Text, Sources: []domain.SourceRef{s}, Score: d.score()}, true
}

func (c *Client) contextHit(raw json.RawMessage, q domain.Query) (domain.Evidence, bool) {
	var d contextDTO
	if json.Unmarshal(raw, &d) != nil {
		return domain.Evidence{}, false
	}
	v, err := strconv.ParseInt(d.Version, 10, 64)
	if err != nil || v <= 0 || !d.valid(q, d.ContextID) || d.EmbeddingModel != c.model || d.Invalidated == nil || *d.Invalidated || len(d.Sources) == 0 {
		return domain.Evidence{}, false
	}
	sources := make([]domain.SourceRef, len(d.Sources))
	authorMatch := q.AuthorID == ""
	var latest time.Time
	for i, ref := range d.Sources {
		s, err := ref.domain()
		if err != nil || s.Scope != q.Scope || s.OccurredAt.After(q.Anchor) || s.OccurredAt.After(d.UpdatedAt) {
			return domain.Evidence{}, false
		}
		sources[i] = s
		if s.AuthorID == q.AuthorID {
			authorMatch = true
		}
		if s.OccurredAt.After(latest) {
			latest = s.OccurredAt
		}
	}
	if !authorMatch || d.OccurredSec != latest.Unix() || d.OccurredNano != latest.Nanosecond() {
		return domain.Evidence{}, false
	}
	context := domain.Context{ID: d.ContextID, Scope: q.Scope, Version: v, Text: d.Text, Sources: sources, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
	if context.Validate() != nil {
		return domain.Evidence{}, false
	}
	return domain.Evidence{ID: d.ID, Text: d.Text, Sources: sources, Score: d.score()}, true
}
