package retrieval

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func matches(q domain.Query, ref domain.SourceRef, m domain.Message) bool {
	return ref.Validate() == nil && ref.Scope == q.Scope && m.Source.Scope == q.Scope && !m.Deleted && m.Source.MessageID == ref.MessageID && m.Source.Version == ref.Version && m.Source.AuthorID == ref.AuthorID && m.Source.OccurredAt.Equal(ref.OccurredAt) && !ref.OccurredAt.After(q.Anchor) && !m.UpdatedAt.IsZero() && !m.UpdatedAt.Before(ref.OccurredAt) && !m.UpdatedAt.After(q.Anchor) && (q.Mode != domain.QueryLiteral || q.AuthorID == "" || ref.AuthorID == q.AuthorID)
}

func (s *Service) verify(ctx context.Context, q domain.Query, in []domain.Evidence, literal literalVerification) ([]domain.Evidence, error) {
	out := make([]domain.Evidence, 0, len(in))
	total := 0
	for _, candidate := range in {
		if q.Mode == domain.QueryLiteral || candidate.Channel == "recent" {
			candidate.DerivedText = ""
		}
		if !utf8.ValidString(candidate.DerivedText) || len(candidate.DerivedText) > domain.MaxPayloadBytes {
			continue
		}
		if q.Mode == domain.QueryLiteral && len(candidate.Sources) != 1 {
			continue
		}
		if candidate.ID == "" || len(candidate.ID) > 1024 || len(candidate.Sources) == 0 || len(candidate.Sources) > 256 || math.IsNaN(candidate.Score) || math.IsInf(candidate.Score, 0) {
			continue
		}
		texts := make([]string, 0, len(candidate.Sources))
		valid := true
		authorMatch := q.AuthorID == ""
		size := 0
		for _, ref := range candidate.Sources {
			if ref.Validate() != nil || ref.Scope != q.Scope || ref.OccurredAt.After(q.Anchor) {
				valid = false
				break
			}
			m, err := s.sources.Message(ctx, q.Scope, ref.MessageID)
			if errors.Is(err, domain.ErrNotFound) {
				valid = false
				break
			}
			if err != nil {
				return nil, sourceError(ctx)
			}
			if !matches(q, ref, m) || !utf8.ValidString(m.Text) || len(m.Text) > domain.MaxPayloadBytes {
				valid = false
				break
			}
			size += len(m.Text) + 1
			if size > domain.MaxPayloadBytes {
				valid = false
				break
			}
			texts = append(texts, m.Text)
			if ref.AuthorID == q.AuthorID {
				authorMatch = true
			}
		}
		if !valid || !authorMatch {
			continue
		}
		// Semantic author filters select an aggregate containing ANY matching
		// author. Prove ALL dependencies with their exact original identities;
		// the generic PG port's author filter intentionally means ALL sources.
		proofQuery := q
		if q.Mode != domain.QueryLiteral {
			proofQuery.AuthorID = ""
		}
		proof, err := s.sources.ValidSources(ctx, proofQuery, candidate.Sources)
		if err != nil {
			return nil, sourceError(ctx)
		}
		if !proof {
			continue
		}
		candidate.Text = strings.Join(texts, "\n")
		if q.Mode == domain.QueryLiteral && !literal.matches(candidate.Text, candidate) {
			continue
		}
		combinedSize := len(candidate.Text) + len(candidate.DerivedText)
		if total+combinedSize > 8<<20 {
			continue
		}
		total += combinedSize
		candidate.Sources = append([]domain.SourceRef(nil), candidate.Sources...)
		out = append(out, candidate)
	}
	return out, nil
}
