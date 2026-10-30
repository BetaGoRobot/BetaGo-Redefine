package memes

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
)

const rrfK = 60.0

func rounded(v float64) float64 { return math.Round(v*1e12) / 1e12 }

// RankUses deduplicates each use's best score and gives equal scores competition ranks (1,1,3).
// Invalid scores are rejected by FuseRetrieval at its boundary; this helper omits them.
func RankUses(hits []RankedHit) []RankedHit {
	best := map[string]float64{}
	for _, h := range hits {
		if h.UseID == "" || math.IsNaN(h.Score) || math.IsInf(h.Score, 0) {
			continue
		}
		score, ok := best[h.UseID]
		if !ok || h.Score > score {
			best[h.UseID] = h.Score
		}
	}
	result := make([]RankedHit, 0, len(best))
	for id, score := range best {
		result = append(result, RankedHit{UseID: id, Score: score})
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := rounded(result[i].Score), rounded(result[j].Score)
		if a != b {
			return a > b
		}
		return result[i].UseID < result[j].UseID
	})
	rank := 0
	var previous float64
	for i := range result {
		score := rounded(result[i].Score)
		if i == 0 || score != previous {
			rank = i + 1
		}
		result[i].Rank = rank
		previous = score
	}
	return result
}
func capDiverseHits(hits []RankedHit, cards map[string]UseCard) []RankedHit {
	if len(hits) <= CandidateBudget {
		return hits
	}
	selected := map[string]bool{}
	families := map[string]bool{}
	for _, h := range hits {
		family := cards[h.UseID].FamilyID
		if !families[family] && len(selected) < CandidateBudget {
			selected[h.UseID], families[family] = true, true
		}
	}
	for _, h := range hits {
		if len(selected) >= CandidateBudget {
			break
		}
		selected[h.UseID] = true
	}
	var out []RankedHit
	for _, h := range hits {
		if selected[h.UseID] {
			out = append(out, h)
		}
	}
	return out
}
func sumVotes(lists [][]RankedHit, denominator float64) []RankedHit {
	votes := map[string][]float64{}
	for _, list := range lists {
		for _, h := range RankUses(list) {
			votes[h.UseID] = append(votes[h.UseID], 1/(rrfK+float64(h.Rank)))
		}
	}
	var hits []RankedHit
	for id, parts := range votes {
		sum, compensation := 0.0, 0.0
		for _, v := range parts {
			y := v - compensation
			t := sum + y
			compensation = (t - sum) - y
			sum = t
		}
		hits = append(hits, RankedHit{UseID: id, Score: sum / denominator})
	}
	return RankUses(hits)
}

func FuseRetrieval(plan QueryPlan, queries []QueryResult) (RetrievalSnapshot, error) {
	snap := RetrievalSnapshot{Plan: plan, Queries: []QueryResult{}, Fields: []FieldResult{}, Fused: []RankedHit{}, Candidates: []UseCard{}, TruncatedUseIDs: []string{}, Warnings: []Warning{}}
	// Keep exact raw responses, including quarantined cards, separate from derived rankings.
	rawQueries, err := json.Marshal(queries)
	if err != nil {
		return snap, fmt.Errorf("raw retrieval contract: %w", err)
	}
	if err = json.Unmarshal(rawQueries, &snap.RawQueries); err != nil {
		return snap, err
	}
	if len(plan.Queries) > MaxSearchQueries {
		return snap, errors.New("query count exceeds per-field budget")
	}
	planned := map[string]SearchQuery{}
	keys := map[string]string{}
	counts := map[Field]int{}
	for _, q := range plan.Queries {
		if q.ID == "" || q.Text == "" || FieldQueryBudget(q.Field) == 0 {
			return snap, errors.New("invalid planned query")
		}
		if _, exists := planned[q.ID]; exists {
			return snap, errors.New("duplicate query identifier")
		}
		counts[q.Field]++
		if counts[q.Field] > FieldQueryBudget(q.Field) {
			return snap, errors.New("per-field query budget exceeded")
		}
		planned[q.ID] = q
		key := string(q.Field) + "\x00" + q.Text
		if _, ok := keys[key]; !ok {
			keys[key] = q.ID
		}
	}
	cards := map[string]UseCard{}
	fingerprints := map[string]string{}
	conflict := map[string]bool{}
	kindByUse := map[string]map[string]bool{}
	warn := func(code, detail, id string) {
		snap.Degraded = true
		snap.Warnings = append(snap.Warnings, Warning{Code: code, Detail: detail, UseID: id})
	}
	seen := map[string]bool{}
	for _, q := range queries {
		expected, ok := planned[q.Query.ID]
		if !ok || q.Query.Field != expected.Field || q.Query.Text != expected.Text {
			return snap, errors.New("retrieval result does not match planned query")
		}
		key := string(expected.Field) + "\x00" + expected.Text
		if seen[key] {
			snap.Warnings = append(snap.Warnings, Warning{Code: "DUPLICATE_QUERY", Detail: expected.ID})
			continue
		}
		seen[key] = true
		q.Query = expected
		q.Hits = nil
		channelSeen := map[string]bool{}
		channels := make([]ChannelResult, 0, len(q.Channels))
		for _, channel := range q.Channels {
			for _, warning := range channel.Warnings {
				code := warning.Code
				if code == "" {
					code = "CHANNEL_WARNING"
				}
				warn(code, warning.Detail, warning.UseID)
			}
			if channel.Channel != "vector" && channel.Channel != "fulltext" {
				return snap, errors.New("unknown retrieval channel")
			}
			if expected.Field == FieldDescription && channel.Channel != "vector" {
				return snap, errors.New("description retrieval only supports a vector channel")
			}
			if channelSeen[channel.Channel] {
				return snap, errors.New("duplicate retrieval channel")
			}
			channelSeen[channel.Channel] = true
			if channel.Status != "ok" && channel.Status != "error" {
				return snap, errors.New("invalid channel status")
			}
			if channel.Status == "error" {
				channel.Hits = nil
				warn("CHANNEL_FAILED", channel.Error, "")
				channels = append(channels, channel)
				continue
			}
			valid := map[string]RawHit{}
			var scores []RankedHit
			for _, hit := range channel.Hits {
				if hit.UseID == "" || hit.UseID != hit.Full.UseID || math.IsNaN(hit.Score) || math.IsInf(hit.Score, 0) {
					warn("INVALID_HIT", "invalid identifier or score", hit.UseID)
					continue
				}
				if err := ValidateCard(hit.Full); err != nil {
					warn("INVALID_CARD", err.Error(), hit.UseID)
					continue
				}
				fp, err := Fingerprint(hit.Full)
				if err != nil {
					return snap, err
				}
				if (hit.CardFingerprint != "" && hit.CardFingerprint != fp) || (hit.Projection.CardFingerprint != "" && hit.Projection.CardFingerprint != fp) {
					warn("INVALID_CARD", "raw document projection/card fingerprint mismatch", hit.UseID)
					continue
				}
				if previous, exists := fingerprints[hit.UseID]; exists && fp != previous {
					conflict[hit.UseID] = true
					warn("CONFLICTING_FULL", "same use_id has conflicting card content", hit.UseID)
				} else {
					fingerprints[hit.UseID] = fp
					cards[hit.UseID] = hit.Full
				}
				if kindByUse[hit.UseID] == nil {
					kindByUse[hit.UseID] = map[string]bool{}
				}
				for _, kind := range hit.RelationKinds {
					kindByUse[hit.UseID][kind] = true
				}
				for _, rel := range hit.Projection.Relations {
					kindByUse[hit.UseID][rel.Kind] = true
				}
				if old, ok := valid[hit.UseID]; !ok || hit.Score > old.Score {
					valid[hit.UseID] = hit
				}
				scores = append(scores, RankedHit{UseID: hit.UseID, Score: hit.Score})
			}
			ranked := RankUses(scores)
			limit := CandidateBudget
			if expected.Field == FieldDescription {
				limit = DescriptionHitBudget
			}
			if len(ranked) > limit {
				warn("CHANNEL_TRUNCATED", fmt.Sprintf("%s exceeded five hits", channel.Channel), "")
			}
			channel.Hits = nil
			for _, rankedHit := range capDiverseHits(ranked, cards) {
				h := valid[rankedHit.UseID]
				h.Rank = rankedHit.Rank
				channel.Hits = append(channel.Hits, h)
			}
			channels = append(channels, channel)
		}
		q.Channels = channels
		snap.Queries = append(snap.Queries, q)
	}
	for _, q := range plan.Queries {
		key := string(q.Field) + "\x00" + q.Text
		if !seen[key] {
			warn("QUERY_FAILED", "missing query result: "+q.ID, "")
			snap.Queries = append(snap.Queries, QueryResult{Query: q, Status: "error", Error: "missing query result"})
			seen[key] = true
		}
	}
	fieldLists := map[Field][][]RankedHit{}
	for i := range snap.Queries {
		q := &snap.Queries[i]
		var channelLists [][]RankedHit
		success := 0
		for j := range q.Channels {
			channel := &q.Channels[j]
			if channel.Status != "ok" {
				continue
			}
			success++
			var list []RankedHit
			filtered := channel.Hits[:0]
			for _, h := range channel.Hits {
				if conflict[h.UseID] {
					continue
				}
				filtered = append(filtered, h)
				list = append(list, RankedHit{UseID: h.UseID, Score: h.Score})
			}
			channel.Hits = filtered
			channelLists = append(channelLists, RankUses(list))
		}
		if success == 0 {
			q.Status = "error"
			warn("QUERY_FAILED", q.Error, "")
			continue
		}
		q.Status = "ok"
		q.Hits = capDiverseHits(sumVotes(channelLists, 1), cards)
		snap.SuccessfulQueries++
		fieldLists[q.Query.Field] = append(fieldLists[q.Query.Field], q.Hits)
	}
	var fieldVotes [][]RankedHit
	for _, field := range []Field{FieldRelation, FieldKeys, FieldEmotion, FieldDescription} {
		lists := fieldLists[field]
		if len(lists) == 0 {
			continue
		}
		hits := capDiverseHits(sumVotes(lists, float64(len(lists))), cards)
		snap.Fields = append(snap.Fields, FieldResult{Field: field, SuccessfulQueries: len(lists), Hits: hits})
		fieldVotes = append(fieldVotes, hits)
	}
	all := sumVotes(fieldVotes, 1)
	protected := map[string]bool{}
	requestedKinds := map[string]bool{}
	for _, q := range plan.Queries {
		if q.Field == FieldRelation {
			for _, k := range q.RelationKinds {
				requestedKinds[k] = true
			}
		}
	}
	for _, field := range snap.Fields {
		quota := 1
		if field.Field == FieldRelation {
			quota = 2
		}
		used := 0
		for pos := 0; pos < len(field.Hits); {
			end := pos + 1
			for end < len(field.Hits) && field.Hits[end].Rank == field.Hits[pos].Rank {
				end++
			}
			var eligible []string
			for _, h := range field.Hits[pos:end] {
				if field.Field == FieldRelation {
					intersects := false
					for kind := range kindByUse[h.UseID] {
						if requestedKinds[kind] {
							intersects = true
						}
					}
					if !intersects {
						continue
					}
				}
				eligible = append(eligible, h.UseID)
			}
			if used+len(eligible) > quota {
				break
			}
			for _, id := range eligible {
				protected[id] = true
			}
			used += len(eligible)
			if used >= quota {
				break
			}
			pos = end
		}
	}
	selected := map[string]bool{}
	selectedFamilies := map[string]bool{}
	for _, h := range all {
		if protected[h.UseID] && len(selected) < CandidateBudget {
			snap.Fused = append(snap.Fused, h)
			selected[h.UseID] = true
			selectedFamilies[cards[h.UseID].FamilyID] = true
		}
	}
	for _, h := range all {
		if !selected[h.UseID] && !selectedFamilies[cards[h.UseID].FamilyID] && len(selected) < CandidateBudget {
			snap.Fused = append(snap.Fused, h)
			selected[h.UseID] = true
			selectedFamilies[cards[h.UseID].FamilyID] = true
		}
	}
	// Keep ordinary use-card recall when fewer distinct families are available.
	for _, h := range all {
		if !selected[h.UseID] && len(selected) < CandidateBudget {
			snap.Fused = append(snap.Fused, h)
			selected[h.UseID] = true
		}
	}
	sort.SliceStable(snap.Fused, func(i, j int) bool { return indexOf(all, snap.Fused[i].UseID) < indexOf(all, snap.Fused[j].UseID) })
	for _, h := range snap.Fused {
		snap.Candidates = append(snap.Candidates, cards[h.UseID])
	}
	for _, h := range all {
		if !selected[h.UseID] {
			snap.TruncatedUseIDs = append(snap.TruncatedUseIDs, h.UseID)
		}
	}
	snap.Fingerprint, err = RetrievalFingerprint(snap)
	if err != nil {
		return snap, err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return snap, err
	}
	var frozen RetrievalSnapshot
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return snap, err
	}
	return frozen, nil
}

// RetrievalFingerprint binds the candidate pool and all retrieval evidence.
func RetrievalFingerprint(s RetrievalSnapshot) (string, error) {
	s.Fingerprint = ""
	return Fingerprint(s)
}

// ValidateRetrievalSnapshot verifies both the content hash and the derivation from retained raw responses.
func ValidateRetrievalSnapshot(s RetrievalSnapshot) (RetrievalSnapshot, error) {
	if s.Fingerprint == "" {
		return RetrievalSnapshot{}, errors.New("retrieval snapshot fingerprint is required")
	}
	fp, err := RetrievalFingerprint(s)
	if err != nil || fp != s.Fingerprint {
		return RetrievalSnapshot{}, errors.New("retrieval snapshot fingerprint mismatch")
	}
	if len(s.Candidates) > CandidateBudget {
		return RetrievalSnapshot{}, errors.New("retrieval candidate budget exceeded")
	}
	raw := s.RawQueries
	if raw == nil {
		raw = s.Queries
	}
	derived, err := FuseRetrieval(s.Plan, raw)
	if err != nil {
		return RetrievalSnapshot{}, err
	}
	if derived.Fingerprint != s.Fingerprint {
		return RetrievalSnapshot{}, errors.New("retrieval snapshot does not match raw-query derivation")
	}
	return derived, nil
}
func indexOf(hits []RankedHit, id string) int {
	for i, h := range hits {
		if h.UseID == id {
			return i
		}
	}
	return len(hits)
}
