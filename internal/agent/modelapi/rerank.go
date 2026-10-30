package modelapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type Reranker struct{ endpoint Endpoint }

func NewReranker(e Endpoint) (*Reranker, error) {
	e, err := normalize(e)
	if err != nil {
		return nil, err
	}
	if e.Protocol == "" {
		e.Protocol = "cohere"
	}
	if e.Protocol != "cohere" && e.Protocol != "json_chat" {
		return nil, fmt.Errorf("%w: rerank protocol", domain.ErrInvalid)
	}
	return &Reranker{e}, nil
}

type rankResult struct {
	Index int     `json:"index"`
	Score float64 `json:"relevance_score"`
}

func (r *rankResult) UnmarshalJSON(b []byte) error {
	var wire struct {
		Index *int     `json:"index"`
		Score *float64 `json:"relevance_score"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	if wire.Index == nil || wire.Score == nil {
		return ErrProtocol
	}
	r.Index = *wire.Index
	r.Score = *wire.Score
	return nil
}

type rankResponse struct {
	Results []rankResult `json:"results"`
}

func (r *Reranker) Rerank(ctx context.Context, query string, items []domain.Evidence) ([]domain.Evidence, error) {
	if len(items) == 0 {
		return []domain.Evidence{}, nil
	}
	if len(items) > 256 || len(query) > 65536 {
		return nil, fmt.Errorf("%w: rerank budget", domain.ErrInvalid)
	}
	docs := make([]string, len(items))
	budget := int64(len(query))
	for i, d := range items {
		budget += int64(len(d.Text) + len(d.DerivedText))
		if budget > r.endpoint.MaxRequestBytes/6 {
			return nil, fmt.Errorf("%w: rerank budget", domain.ErrInvalid)
		}
		docs[i] = "Canonical original:\n" + d.Text + "\nDerived evidence:\n" + d.DerivedText
	}
	var response rankResponse
	if r.endpoint.Protocol == "cohere" {
		if err := request(ctx, r.endpoint, "/rerank", map[string]any{"model": r.endpoint.Model, "query": query, "documents": docs, "top_n": len(docs)}, &response); err != nil {
			return nil, err
		}
	} else {
		c := &Chat{r.endpoint}
		payload, _ := json.Marshal(map[string]any{"query": query, "documents": docs})
		out, err := c.Generate(ctx, ChatRequest{Messages: []ChatMessage{{Role: "system", Content: "Rank every document for the query. Return JSON only: {\"results\":[{\"index\":0,\"relevance_score\":0.5}]}. Each zero-based document index exactly once; finite scores between zero and one."}, {Role: "user", Content: string(payload)}}, ResponseFormat: json.RawMessage(`{"type":"json_object"}`)})
		if err != nil {
			return nil, err
		}
		text, ok := out.Message.Content.(string)
		if !ok || json.Unmarshal([]byte(text), &response) != nil {
			return nil, fmt.Errorf("%w: rerank JSON", ErrProtocol)
		}
	}
	if len(response.Results) != len(items) {
		return nil, fmt.Errorf("%w: rerank result count", ErrProtocol)
	}
	out := make([]domain.Evidence, len(items))
	seen := make([]bool, len(items))
	for i, d := range response.Results {
		if d.Index < 0 || d.Index >= len(items) || seen[d.Index] || math.IsNaN(d.Score) || math.IsInf(d.Score, 0) || d.Score < 0 || d.Score > 1 {
			return nil, fmt.Errorf("%w: rerank index or score", ErrProtocol)
		}
		seen[d.Index] = true
		out[i] = items[d.Index]
		out[i].Score = d.Score
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}
