package mememilvus

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

func validatePlan(p memes.QueryPlan) error {
	if len(p.Queries) > memes.MaxSearchQueries {
		return ErrInvalid
	}
	ids := map[string]bool{}
	counts := map[memes.Field]int{}
	for _, q := range p.Queries {
		counts[q.Field]++
		if q.ID == "" || ids[q.ID] || strings.TrimSpace(q.Text) == "" || len(q.Text) > 32768 || !utf8.ValidString(q.Text) || counts[q.Field] > memes.FieldQueryBudget(q.Field) {
			return ErrInvalid
		}
		ids[q.ID] = true
	}
	return nil
}
func (r *retriever) Search(ctx context.Context, p memes.QueryPlan) (memes.RetrievalSnapshot, error) {
	if e := validatePlan(p); e != nil {
		return memes.RetrievalSnapshot{}, e
	}
	if len(p.Queries) == 0 {
		return memes.FuseRetrieval(p, nil)
	}
	texts := make([]string, len(p.Queries))
	for i, q := range p.Queries {
		texts[i] = q.Text
	}
	vectors, embedErr := r.client.embed(ctx, texts)
	raw := make([]memes.QueryResult, len(p.Queries))
	for i, q := range p.Queries {
		qr := memes.QueryResult{Query: q, Status: "error"}
		if q.Field != memes.FieldDescription {
			qr.Channels = append(qr.Channels, r.searchLane(ctx, q, "fulltext", nil))
		}
		if embedErr != nil {
			qr.Channels = append(qr.Channels, memes.ChannelResult{Channel: "vector", Status: "error", Error: ErrEmbedding.Error()})
		} else {
			qr.Channels = append(qr.Channels, r.searchLane(ctx, q, "vector", vectors[i]))
		}
		for _, ch := range qr.Channels {
			if ch.Status == "ok" {
				qr.Status = "ok"
				break
			}
		}
		if qr.Status == "error" {
			qr.Error = "all retrieval channels failed"
		}
		raw[i] = qr
	}
	return memes.FuseRetrieval(p, raw)
}
func (r *retriever) searchLane(ctx context.Context, q memes.SearchQuery, channel string, vector []float32) memes.ChannelResult {
	result := memes.ChannelResult{Channel: channel, Status: "error"}
	fi := slices.Index(fields, q.Field)
	if fi < 0 {
		result.Error = ErrInvalid.Error()
		return result
	}
	limit := memes.CandidateBudget
	if q.Field == memes.FieldDescription {
		limit = memes.DescriptionHitBudget
	}
	body := map[string]any{"collectionName": r.manifest.Indexes[fi], "limit": limit, "outputFields": []string{"use_id", "fingerprint", "text", "relation_kinds"}, "consistencyLevel": "Strong"}
	if channel == "fulltext" {
		body["annsField"] = "sparse"
		body["data"] = []string{q.Text}
		body["searchParams"] = map[string]any{"metricType": "BM25", "params": map[string]any{}}
	} else {
		body["annsField"] = "vector"
		body["data"] = [][]float32{vector}
		body["searchParams"] = map[string]any{"metricType": "L2", "params": map[string]any{"ef": 64}}
	}
	if q.Field == memes.FieldRelation && len(q.RelationKinds) > 0 {
		kinds, _ := json.Marshal(q.RelationKinds)
		body["filter"] = "ARRAY_CONTAINS_ANY(relation_kinds, " + string(kinds) + ")"
	}
	var rows []json.RawMessage
	if e := r.client.request(ctx, "entities/search", body, &rows); e != nil {
		result.Error = e.Error()
		return result
	}
	if len(rows) > limit {
		result.Error = ErrResponse.Error()
		return result
	}
	seen := map[string]bool{}
	quarantine := func(id, detail string) {
		result.Warnings = append(result.Warnings, memes.Warning{Code: "INVALID_INDEX_HIT", Detail: detail, UseID: id})
	}
	for position, encoded := range rows {
		var row struct {
			document
			Distance *float64 `json:"distance"`
		}
		if json.Unmarshal(encoded, &row) != nil {
			quarantine("", "invalid Milvus hit JSON")
			continue
		}
		if row.UseID == "" || row.Fingerprint == "" || row.Distance == nil || math.IsNaN(*row.Distance) || math.IsInf(*row.Distance, 0) || *row.Distance < 0 || seen[row.UseID] {
			quarantine(row.UseID, "invalid identifier, distance or duplicate hit")
			continue
		}
		seen[row.UseID] = true
		stored, e := r.client.options.Records.Get(ctx, memestore.IndexCard, row.Fingerprint)
		if e != nil {
			quarantine(row.UseID, "full record unavailable")
			continue
		}
		var record memes.IndexRecord
		if json.Unmarshal(stored.Body, &record) != nil {
			quarantine(row.UseID, "invalid full record JSON")
			continue
		}
		compiled, e := memes.CompileCard(record.Full, record.Projection)
		if e != nil || record.UseID != row.UseID || record.Fingerprint != row.Fingerprint || compiled.Fingerprint != row.Fingerprint {
			quarantine(row.UseID, "full record fingerprint mismatch")
			continue
		}
		expected := documentFor(compiled, q.Field)
		if expected.Text != row.Text || !slices.Equal(expected.RelationKinds, row.RelationKinds) {
			quarantine(row.UseID, "projection text or relation kinds mismatch")
			continue
		}
		score := *row.Distance
		if channel == "vector" {
			score = 1 / (1 + score)
		}
		result.Hits = append(result.Hits, memes.RawHit{DocumentID: row.UseID, UseID: row.UseID, Score: score, Rank: position + 1, Full: compiled.Full, Projection: compiled.Projection, Fingerprint: compiled.Fingerprint, CardFingerprint: compiled.Projection.CardFingerprint, RelationKinds: expected.RelationKinds})
	}
	result.Status = "ok"
	return result
}
