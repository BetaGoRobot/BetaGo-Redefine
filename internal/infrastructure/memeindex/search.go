package memeindex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

type document struct {
	UseID         string           `json:"use_id"`
	Text          string           `json:"text"`
	Full          memes.UseCard    `json:"full"`
	Projection    memes.Projection `json:"projection"`
	Fingerprint   string           `json:"fingerprint"`
	RelationKinds []string         `json:"relation_kinds"`
	Vector        []float32        `json:"vector,omitempty"`
}
type searchResponse struct {
	Error    json.RawMessage `json:"error"`
	Status   int             `json:"status"`
	TimedOut bool            `json:"timed_out"`
	Shards   struct {
		Failed int `json:"failed"`
	} `json:"_shards"`
	Hits *struct {
		Hits []json.RawMessage `json:"hits"`
	} `json:"hits"`
}

func validatePlan(plan memes.QueryPlan) error {
	if len(plan.Queries) > memes.MaxSearchQueries {
		return ErrInvalid
	}
	ids := map[string]bool{}
	counts := map[memes.Field]int{}
	for _, q := range plan.Queries {
		if q.ID == "" || ids[q.ID] || strings.TrimSpace(q.Text) == "" || len(q.Text) > 32<<10 || memes.FieldQueryBudget(q.Field) == 0 {
			return ErrInvalid
		}
		ids[q.ID] = true
		counts[q.Field]++
		if counts[q.Field] > memes.FieldQueryBudget(q.Field) {
			return ErrInvalid
		}
	}
	return nil
}
func (r *retriever) Search(ctx context.Context, plan memes.QueryPlan) (memes.RetrievalSnapshot, error) {
	if err := validatePlan(plan); err != nil {
		return memes.RetrievalSnapshot{}, err
	}
	if len(plan.Queries) == 0 {
		return memes.FuseRetrieval(plan, nil)
	}
	texts := make([]string, len(plan.Queries))
	for i, q := range plan.Queries {
		texts[i] = q.Text
	}
	vectors, embeddingErr := r.client.embed(ctx, texts)
	results := make([]memes.QueryResult, len(plan.Queries))
	for i, q := range plan.Queries {
		result := memes.QueryResult{Query: q, Status: "error"}
		if q.Field != memes.FieldDescription {
			result.Channels = append(result.Channels, memes.ChannelResult{Channel: "fulltext", Status: "error", Error: "fulltext search failed"})
		}
		result.Channels = append(result.Channels, memes.ChannelResult{Channel: "vector", Status: "error", Error: "vector search failed"})
		vectorLane := len(result.Channels) - 1
		var lines bytes.Buffer
		enc := json.NewEncoder(&lines)
		index := ""
		for j, f := range fields {
			if q.Field == f {
				index = r.manifest.Indexes[j]
			}
		}
		appendLane := func(query any) {
			enc.Encode(map[string]any{"index": index})
			enc.Encode(map[string]any{"size": memes.CandidateBudget, "_source": []string{"use_id", "text", "full", "projection", "fingerprint", "relation_kinds"}, "query": query})
		}
		if q.Field != memes.FieldDescription {
			appendLane(map[string]any{"match": map[string]any{"text": map[string]any{"query": q.Text}}})
		}
		if embeddingErr == nil {
			appendLane(map[string]any{"knn": map[string]any{"vector": map[string]any{"vector": vectors[i], "k": memes.CandidateBudget}}})
		} else {
			result.Channels[vectorLane].Error = ErrEmbedding.Error()
			if q.Field == memes.FieldDescription {
				result.Error = ErrEmbedding.Error()
				results[i] = result
				continue
			}
		}
		status, raw, err := r.client.request(ctx, http.MethodPost, "/_msearch", lines.Bytes(), "application/x-ndjson")
		if err != nil || !successful(status) {
			message := "meme search request failed"
			if err != nil {
				message = err.Error()
			} else {
				message = (&HTTPError{Status: status}).Error()
			}
			for lane := range result.Channels {
				if lane != vectorLane || embeddingErr == nil {
					result.Channels[lane].Error = message
				}
			}
			result.Error = message
			results[i] = result
			continue
		}
		var response struct {
			Responses []json.RawMessage `json:"responses"`
		}
		want := len(result.Channels)
		if embeddingErr != nil {
			want--
		}
		if err := json.Unmarshal(raw, &response); err != nil || len(response.Responses) != want {
			result.Error = ErrResponse.Error()
			for lane := range result.Channels {
				if lane != vectorLane || embeddingErr == nil {
					result.Channels[lane].Error = ErrResponse.Error()
				}
			}
			results[i] = result
			continue
		}
		for lane, body := range response.Responses {
			channel, issues := decodeLane(body, q)
			channel.Channel = result.Channels[lane].Channel
			channel.Warnings = issues
			result.Channels[lane] = channel
		}
		for _, channel := range result.Channels {
			if channel.Status == "ok" {
				result.Status = "ok"
				break
			}
		}
		if result.Status == "error" {
			result.Error = "meme search lanes failed"
		}
		results[i] = result
	}
	return memes.FuseRetrieval(plan, results)
}
func decodeLane(raw json.RawMessage, q memes.SearchQuery) (memes.ChannelResult, []memes.Warning) {
	result := memes.ChannelResult{Status: "error", Error: ErrResponse.Error()}
	var response searchResponse
	if err := json.Unmarshal(raw, &response); err != nil || response.Hits == nil || response.Hits.Hits == nil || len(response.Error) > 0 && string(response.Error) != "null" || response.Status >= 400 || response.TimedOut || response.Shards.Failed > 0 {
		return result, nil
	}
	result.Status = "ok"
	result.Error = ""
	result.Hits = []memes.RawHit{}
	var warnings []memes.Warning
	for i, rawHit := range response.Hits.Hits {
		if i >= memes.CandidateBudget {
			warnings = append(warnings, memes.Warning{Code: "CHANNEL_TRUNCATED", Detail: "server exceeded five hits"})
			break
		}
		var hit struct {
			ID     string   `json:"_id"`
			Score  *float64 `json:"_score"`
			Source document `json:"_source"`
		}
		if err := json.Unmarshal(rawHit, &hit); err != nil || hit.Score == nil || hit.ID != documentID(hit.Source.UseID) || !validDocument(hit.Source, q.Field) {
			warnings = append(warnings, memes.Warning{Code: "INVALID_INDEX_HIT", Detail: fmt.Sprintf("invalid hit at rank %d", i+1)})
			continue
		}
		result.Hits = append(result.Hits, memes.RawHit{DocumentID: hit.ID, UseID: hit.Source.UseID, Score: *hit.Score, Rank: i + 1, Full: hit.Source.Full, Projection: hit.Source.Projection, Fingerprint: hit.Source.Fingerprint, CardFingerprint: hit.Source.Projection.CardFingerprint, RelationKinds: hit.Source.RelationKinds})
	}
	return result, warnings
}
func validDocument(d document, field memes.Field) bool {
	compiled, err := memes.CompileCard(d.Full, d.Projection)
	if err != nil || compiled.Fingerprint != d.Fingerprint || compiled.UseID != d.UseID || d.UseID == "" {
		return false
	}
	return d.Text == projectionText(compiled, field) && slices.Equal(d.RelationKinds, relationKinds(compiled))
}
func projectionText(r memes.IndexRecord, field memes.Field) string {
	switch field {
	case memes.FieldRelation:
		return r.Relation
	case memes.FieldEmotion:
		return r.EmotionChain
	case memes.FieldKeys:
		return r.Keys
	case memes.FieldDescription:
		return r.Full.Description
	}
	return ""
}
