package retrieval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func cloneEvidence(in []domain.Evidence) []domain.Evidence {
	out := append([]domain.Evidence(nil), in...)
	for i := range out {
		out[i].Sources = append([]domain.SourceRef(nil), in[i].Sources...)
	}
	return out
}

// RRF adds each ID once per branch. Stable first appearance breaks equal ranks.
func fuse(branches ...[]domain.Evidence) []domain.Evidence {
	out := []domain.Evidence{}
	positions := map[string]int{}
	for _, branch := range branches {
		seen := map[string]bool{}
		for rank, e := range branch {
			if e.ID == "" || seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			position, ok := positions[e.ID]
			if !ok {
				position = len(out)
				positions[e.ID] = position
				e.Score = 0
				out = append(out, e)
			}
			out[position].Score += 1 / float64(60+rank+1)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func acceptRanking(original, ranked []domain.Evidence) ([]domain.Evidence, bool) {
	if len(original) != len(ranked) {
		return nil, false
	}
	byID := make(map[string]domain.Evidence, len(original))
	for _, e := range original {
		if _, exists := byID[e.ID]; exists {
			return nil, false
		}
		byID[e.ID] = e
	}
	out := make([]domain.Evidence, 0, len(original))
	seen := map[string]bool{}
	for _, r := range ranked {
		e, ok := byID[r.ID]
		if !ok || seen[r.ID] || math.IsNaN(r.Score) || math.IsInf(r.Score, 0) {
			return nil, false
		}
		seen[r.ID] = true
		e.Score = r.Score
		out = append(out, e)
	}
	return out, true
}

func refKey(ref domain.SourceRef) string {
	b, _ := json.Marshal(ref)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func recentID(ref domain.SourceRef) string { return "recent_" + refKey(ref) }
func dedupeSources(in []domain.Evidence) []domain.Evidence {
	seen := map[string]bool{}
	out := make([]domain.Evidence, 0, len(in))
	for _, e := range in {
		keys := make([]string, len(e.Sources))
		for i, s := range e.Sources {
			keys[i] = refKey(s)
		}
		sort.Strings(keys)
		// Distinct facts may share every source. Only identical derived content
		// with the same complete provenance is a duplicate; raw recents retain
		// an empty DerivedText and can still deduplicate identical originals.
		b, _ := json.Marshal(struct {
			Sources []string
			Derived string
		}{keys, e.DerivedText})
		key := string(b)
		if !seen[key] {
			seen[key] = true
			out = append(out, e)
		}
	}
	return out
}
