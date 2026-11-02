package hindsight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
)

type recallFact struct {
	ID            string            `json:"id"`
	Text          string            `json:"text"`
	Type          string            `json:"type"`
	DocumentID    string            `json:"document_id"`
	Metadata      map[string]string `json:"metadata"`
	Tags          []string          `json:"tags"`
	SourceFactIDs []string          `json:"source_fact_ids"`
}
type recallResponse struct {
	Results              *[]recallFact         `json:"results"`
	SourceFacts          map[string]recallFact `json:"source_facts"`
	SourceFactsTruncated bool                  `json:"source_facts_truncated"`
}
type tokenBudget struct {
	MaxTokens int `json:"max_tokens"`
}
type sourceBudget struct {
	MaxTokens         int `json:"max_tokens"`
	MaxPerObservation int `json:"max_tokens_per_observation"`
}
type recallInclude struct {
	Entities    any          `json:"entities"`
	Chunks      tokenBudget  `json:"chunks"`
	SourceFacts sourceBudget `json:"source_facts"`
}
type recallRequest struct {
	Query          string        `json:"query"`
	Types          []string      `json:"types"`
	Budget         string        `json:"budget"`
	MaxTokens      int           `json:"max_tokens"`
	QueryTimestamp string        `json:"query_timestamp"`
	Tags           []string      `json:"tags"`
	TagsMatch      string        `json:"tags_match"`
	Include        recallInclude `json:"include"`
}

// Recall expands every observation edge, including nested dependencies, before
// returning candidates. query_timestamp aids interpretation; PG still enforces
// the strict anchor on each current source and its latest mutation time.
func (c *Client) Recall(ctx context.Context, q domain.Query) ([]domain.Evidence, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(q.Text)) == 0 || len(q.Text) > 65536 || int64(len(q.Text)) > c.maxRequest {
		return nil, domain.ErrInvalid
	}
	request := recallRequest{Query: q.Text, Types: []string{"world", "experience", "observation"}, Budget: "mid", MaxTokens: 4096, QueryTimestamp: q.Anchor.Format(time.RFC3339Nano), Tags: []string{q.Scope.Key()}, TagsMatch: "all_strict", Include: recallInclude{Chunks: tokenBudget{4096}, SourceFacts: sourceBudget{8192, 4096}}}
	data, err := c.encode(request)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var response recallResponse
	if err := c.request(ctx, http.MethodPost, bankPath(q.Scope)+"/memories/recall", data, &response); err != nil {
		return nil, err
	}
	telemetry.Payload(ctx, "agent.memory.response", "memory_response", response)
	if response.Results == nil || len(*response.Results) > 1000 || len(response.SourceFacts) > 4096 {
		return nil, errors.New("hindsight recall response invalid")
	}
	out := make([]domain.Evidence, 0, q.Limit)
	seen := map[string]bool{}
	for _, fact := range *response.Results {
		if seen[fact.ID] {
			continue
		}
		refs, ok := expand(fact, response, q)
		if !ok {
			continue
		}
		seen[fact.ID] = true
		out = append(out, domain.Evidence{ID: fact.ID, DerivedText: fact.Text, Channel: "memory", Sources: refs})
		if len(out) == q.Limit {
			break
		}
	}
	return out, nil
}

func hasTag(tags []string, scope domain.Scope) bool {
	for _, tag := range tags {
		if tag == scope.Key() {
			return true
		}
	}
	return false
}

func expand(root recallFact, response recallResponse, q domain.Query) ([]domain.SourceRef, bool) {
	var refs []domain.SourceRef
	path := map[string]bool{}
	seen := map[string]domain.SourceRef{}
	remaining := 4096
	var visit func(recallFact, int) bool
	visit = func(fact recallFact, depth int) bool {
		remaining--
		if remaining < 0 || depth > 32 || fact.ID == "" || len(fact.ID) > 1024 || path[fact.ID] || !hasTag(fact.Tags, q.Scope) {
			return false
		}
		path[fact.ID] = true
		defer delete(path, fact.ID)
		if fact.Type == "observation" {
			if response.SourceFactsTruncated || len(fact.SourceFactIDs) == 0 || len(fact.SourceFactIDs) > 4096 {
				return false
			}
			for _, id := range fact.SourceFactIDs {
				dependency, ok := response.SourceFacts[id]
				if !ok || dependency.ID != id || !visit(dependency, depth+1) {
					return false
				}
			}
			return true
		}
		if (fact.Type != "world" && fact.Type != "experience") || len(fact.SourceFactIDs) != 0 {
			return false
		}
		raw := fact.Metadata[provenanceKey]
		if len(raw) == 0 || len(raw) > 1<<20 {
			return false
		}
		var p provenance
		decoder := json.NewDecoder(bytes.NewBufferString(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&p) != nil {
			return false
		}
		if decoder.Decode(new(any)) != io.EOF {
			return false
		}
		version, err := strconv.ParseInt(p.ContextVersion, 10, 64)
		if err != nil || version <= 0 || strconv.FormatInt(version, 10) != p.ContextVersion || p.SchemaVersion != 1 || p.Scope != q.Scope || p.ContextID == "" || fact.DocumentID != DocumentID(q.Scope, p.ContextID) || len(p.Sources) == 0 || len(p.Sources) > 256 || p.CreatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) || p.UpdatedAt.After(q.Anchor) {
			return false
		}
		for _, source := range p.Sources {
			version, err := strconv.ParseInt(source.Version, 10, 64)
			if err != nil || strconv.FormatInt(version, 10) != source.Version {
				return false
			}
			ref := domain.SourceRef{Scope: source.Scope, MessageID: source.MessageID, Version: version, AuthorID: source.AuthorID, OccurredAt: source.OccurredAt}
			if ref.Validate() != nil || ref.Scope != q.Scope || ref.OccurredAt.After(q.Anchor) || ref.OccurredAt.After(p.UpdatedAt) {
				return false
			}
			key := ref.MessageID
			if prior, ok := seen[key]; ok {
				if prior.Version != ref.Version || prior.AuthorID != ref.AuthorID || !prior.OccurredAt.Equal(ref.OccurredAt) {
					return false
				}
			} else {
				if len(refs) >= 256 {
					return false
				}
				seen[key] = ref
				refs = append(refs, ref)
			}
		}
		return true
	}
	if !visit(root, 0) || len(refs) == 0 {
		return nil, false
	}
	if q.AuthorID != "" {
		authorMatch := false
		for _, ref := range refs {
			if ref.AuthorID == q.AuthorID {
				authorMatch = true
				break
			}
		}
		if !authorMatch {
			return nil, false
		}
	}
	return refs, true
}
