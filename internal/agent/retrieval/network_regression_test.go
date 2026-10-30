package retrieval_test

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
)

type stalledRetrieval struct {
	*search
	*model
	stage  string
	budget time.Duration
}

type contextCheckedSource struct{ *source }

func (s contextCheckedSource) Message(ctx context.Context, scope domain.Scope, id string) (domain.Message, error) {
	if ctx.Err() != nil {
		return domain.Message{}, ctx.Err()
	}
	return s.source.Message(ctx, scope, id)
}
func (s contextCheckedSource) ValidSources(ctx context.Context, q domain.Query, refs []domain.SourceRef) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return s.source.ValidSources(ctx, q, refs)
}

func (s *stalledRetrieval) wait(ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		s.budget = time.Until(deadline)
	}
	select {
	case <-ctx.Done():
	case <-time.After(4 * time.Second):
	}
}
func (s *stalledRetrieval) Literal(ctx context.Context, q domain.Query) ([]domain.Evidence, error) {
	s.wait(ctx)
	return nil, context.DeadlineExceeded
}
func (s *stalledRetrieval) Recall(ctx context.Context, q domain.Query) ([]domain.Evidence, error) {
	s.wait(ctx)
	return nil, context.DeadlineExceeded
}
func (s *stalledRetrieval) Embed(ctx context.Context, text []string) ([][]float32, error) {
	if s.stage == "embedding" {
		s.wait(ctx)
		return nil, context.DeadlineExceeded
	}
	return s.model.Embed(ctx, text)
}
func (s *stalledRetrieval) Rerank(ctx context.Context, q string, items []domain.Evidence) ([]domain.Evidence, error) {
	if s.stage == "rerank" {
		s.wait(ctx)
		return nil, context.DeadlineExceeded
	}
	// Even after an embedding timeout, ranking must share the consumed budget.
	if s.stage == "embedding" && ctx.Err() == nil {
		s.budget = 0
	}
	return s.model.Rerank(ctx, q, items)
}

func TestExternalTimeoutKeepsValidatedRecentContextAndParentUsable(t *testing.T) {
	for _, stage := range []string{"literal", "memory", "embedding", "rerank"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			fresh := message("fresh", "exact original")
			deleted := message("deleted", "exact deleted")
			deleted.Deleted = true
			cross := message("cross", "exact cross")
			cross.Source.Scope.ChatID = "another"
			pg := &source{messages: map[string]domain.Message{"fresh": fresh, "deleted": deleted, "cross": cross}, recent: []domain.Message{fresh, deleted, cross}}
			stalled := &stalledRetrieval{search: &search{}, model: &model{}, stage: stage}
			svc, err := retrieval.New(retrieval.Options{Sources: contextCheckedSource{pg}, Search: stalled, Memory: stalled, Model: stalled})
			if err != nil {
				t.Fatal(err)
			}
			mode, text := domain.QueryDiscussion, "exact"
			if stage == "literal" {
				mode, text = domain.QueryLiteral, `"exact"`
			}
			if stage == "memory" {
				mode = domain.QueryMemory
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := time.Now()
			got, err := svc.Retrieve(ctx, query(mode, text))
			if err != nil || !got.Degraded || len(got.Evidence) != 1 || got.Evidence[0].Text != fresh.Text || pg.proofs < 2 || ctx.Err() != nil {
				t.Fatalf("timeout lost verified fallback or parent: %+v err=%v proofs=%d parent=%v", got, err, pg.proofs, ctx.Err())
			}
			if elapsed := time.Since(started); elapsed > 3700*time.Millisecond || stalled.budget <= 0 || stalled.budget > 3*time.Second {
				t.Fatalf("external work lacks shared three-second budget: elapsed=%v budget=%v", elapsed, stalled.budget)
			}
		})
	}
}
