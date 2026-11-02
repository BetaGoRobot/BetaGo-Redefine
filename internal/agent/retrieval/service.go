// Package retrieval verifies search and memory candidates against current facts.
package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

type Search interface {
	Literal(context.Context, domain.Query) ([]domain.Evidence, error)
	Keyword(context.Context, domain.Query) ([]domain.Evidence, error)
	Vector(context.Context, domain.Query, []float32) ([]domain.Evidence, error)
}
type Memory interface {
	Recall(context.Context, domain.Query) ([]domain.Evidence, error)
}
type Options struct {
	Sources     domain.SourceReader
	Search      Search
	Memory      Memory
	Model       domain.Model
	RecentLimit int
}
type Failure struct{ Channel, Reason string }
type Result struct {
	Evidence []domain.Evidence
	Degraded bool
	Failures []Failure
	// RecentWindow is the bounded PG window, never exhaustive search. An
	// unavailable index may contain older matches outside this window.
	RecentWindow int
}
type Service struct {
	sources     domain.SourceReader
	search      Search
	memory      Memory
	model       domain.Model
	recentLimit int
}

func New(o Options) (*Service, error) {
	if o.Sources == nil || o.RecentLimit < 0 || o.RecentLimit > 1000 {
		return nil, fmt.Errorf("%w: retrieval sources or recent limit", domain.ErrInvalid)
	}
	if o.RecentLimit == 0 {
		o.RecentLimit = 64
	}
	return &Service{sources: o.Sources, search: o.Search, memory: o.Memory, model: o.Model, recentLimit: o.RecentLimit}, nil
}

func (r *Result) fail(channel, reason string) {
	r.Degraded = true
	r.Failures = append(r.Failures, Failure{Channel: channel, Reason: reason})
}

// Retrieve keeps authority filters out of model output. Upstream failures carry
// fixed classifications because provider errors can contain credentials/text.
func (s *Service) Retrieve(ctx context.Context, q domain.Query) (out Result, retErr error) {
	ctx, span := telemetry.Start(ctx, "agent.retrieval", attribute.String("retrieval.mode", string(q.Mode)))
	defer func() { telemetry.End(ctx, span, retErr) }()
	telemetry.Payload(ctx, "agent.retrieval.query", "retrieval_query", q)
	if err := q.Validate(); err != nil {
		return Result{}, err
	}
	if len(q.Text) > 65536 || !utf8.ValidString(q.Text) {
		return Result{}, fmt.Errorf("%w: retrieval query text", domain.ErrInvalid)
	}
	var clauses []literalClause
	if q.Mode == domain.QueryLiteral {
		if strings.TrimSpace(q.Text) != "" || q.AuthorID == "" {
			var err error
			clauses, err = parseLiteral(q.Text)
			if err != nil {
				return Result{}, err
			}
		}
	} else if len(words(q.Text)) == 0 {
		return Result{}, fmt.Errorf("%w: semantic query requires words", domain.ErrInvalid)
	}
	result := Result{RecentWindow: s.recentLimit}
	literal := literalVerification{clauses: clauses, indexed: map[string]string{}}
	recent, err := s.sources.Recent(ctx, q.Scope, q.Anchor, s.recentLimit)
	if err != nil {
		return Result{}, sourceError(ctx)
	}
	if len(recent) > s.recentLimit {
		recent = recent[:s.recentLimit]
	}
	var candidates []domain.Evidence
	// Search/model/memory calls are optional. Share one budget across them,
	// while PG recent context and both authority checks retain the parent.
	externalCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	collect := func(ch string, items []domain.Evidence, err error) []domain.Evidence {
		if err != nil {
			telemetry.Warn(ctx, "agent.retrieval.channel_failed", "channel", ch, "error", err)
			result.fail(ch, "unavailable")
			return nil
		}
		if len(items) > 1000 {
			items = items[:1000]
			result.fail(ch, "candidate_limit")
		}
		telemetry.Info(ctx, "agent.retrieval.channel", "channel", ch, "candidate_count", len(items))
		telemetry.Payload(ctx, "agent.retrieval.candidates", "candidates", items)
		out := cloneEvidence(items)
		for i := range out {
			out[i].Channel = ch
			if ch == "literal" {
				out[i].DerivedText = ""
				literal.indexed[literalCandidateKey(out[i])] = out[i].Text
			} else {
				// Meili contexts still use the candidate Text field. Normalize
				// once here; later proofs must never recapture canonical PG text.
				if ch != "memory" || out[i].DerivedText == "" {
					out[i].DerivedText = out[i].Text
				}
				out[i].Text = ""
			}
		}
		return out
	}
	switch q.Mode {
	case domain.QueryLiteral:
		if s.search == nil {
			result.fail("literal", "unavailable")
		} else {
			var items []domain.Evidence
			if analyzed, ok := s.search.(literalAnalyzer); ok && literal.hasUnquoted(false) {
				items, literal.queryWords, err = analyzed.LiteralAnalyzed(externalCtx, q)
			} else {
				items, err = s.search.Literal(externalCtx, q)
			}
			candidates = collect("literal", items, err)
			if err == nil && len(items) > 0 {
				if literal.queryWords >= 10 && literal.hasUnquoted(false) {
					result.fail("literal", "query_word_limit")
				} else if literal.queryWords <= 0 && literal.hasUnquoted(true) {
					result.fail("literal", "literal_tokenization_unavailable")
				}
			}
		}
	case domain.QueryDiscussion:
		short := q
		short.Text = shortExpression(q.Text)
		var keyword, vector []domain.Evidence
		if s.search == nil {
			result.fail("keyword", "unavailable")
			result.fail("vector", "unavailable")
		} else {
			items, err := s.search.Keyword(externalCtx, short)
			keyword = collect("keyword", items, err)
		}
		if s.model == nil {
			result.fail("embedding", "unavailable")
			if s.search != nil {
				result.fail("vector", "unavailable")
			}
		} else if s.search != nil {
			embedded, err := s.model.Embed(externalCtx, []string{short.Text})
			if err != nil || !validEmbedding(embedded) {
				result.fail("embedding", "unavailable")
				result.fail("vector", "unavailable")
			} else {
				items, err := s.search.Vector(externalCtx, short, embedded[0])
				vector = collect("vector", items, err)
			}
		}
		candidates = fuse(keyword, vector)
	case domain.QueryMemory:
		short := q
		short.Text = shortExpression(q.Text)
		if s.memory == nil {
			result.fail("memory", "unavailable")
		} else {
			items, err := s.memory.Recall(externalCtx, short)
			candidates = collect("memory", items, err)
		}
	}
	if q.Mode == domain.QueryLiteral && literal.hasUnquoted(true) && len(recent) > 0 {
		// A direct PG window has no engine tokenization proof. Semantic recent
		// context and exact quoted literals remain available independently.
		result.fail("recent", "literal_tokenization_unavailable")
		recent = nil
	}
	for _, m := range recent {
		candidates = append(candidates, domain.Evidence{ID: recentID(m.Source), Channel: "recent", Sources: []domain.SourceRef{m.Source}})
	}
	candidates, err = s.verify(ctx, q, candidates, literal)
	if err != nil {
		return Result{}, err
	}
	candidates = dedupeSources(candidates)
	if q.Mode == domain.QueryDiscussion && len(candidates) > 0 {
		if s.model == nil {
			result.fail("rerank", "unavailable")
		} else {
			ranked, rankErr := s.model.Rerank(externalCtx, q.Text, cloneEvidence(candidates))
			if rankErr != nil {
				result.fail("rerank", "unavailable")
			} else if accepted, ok := acceptRanking(candidates, ranked); ok {
				candidates = accepted
			} else {
				result.fail("rerank", "invalid_order")
			}
		}
	}
	// Reload after ranking and prove complete dependency sets again: model
	// latency must not leave an unchecked edit/deletion window.
	candidates, err = s.verify(ctx, q, candidates, literal)
	if err != nil {
		return Result{}, err
	}
	if len(candidates) > q.Limit {
		candidates = candidates[:q.Limit]
	}
	result.Evidence = candidates
	telemetry.Info(ctx, "agent.retrieval.summary", "result_count", len(candidates), "degraded", result.Degraded)
	telemetry.Payload(ctx, "agent.retrieval.results", "retrieval_result", result)
	return result, nil
}

func validEmbedding(v [][]float32) bool {
	if len(v) != 1 || len(v[0]) == 0 || len(v[0]) > 65536 {
		return false
	}
	for _, x := range v[0] {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
	}
	return true
}

var errSourcesUnavailable = errors.New("retrieval authoritative source unavailable")

func sourceError(ctx context.Context) error {
	if ctx.Err() != nil {
		return fmt.Errorf("retrieval source canceled: %w", ctx.Err())
	}
	return errSourcesUnavailable
}
