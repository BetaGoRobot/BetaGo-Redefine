package retrieval_test

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
)

type analyzedSearch struct {
	*search
	count       int
	analysisErr error
}

func (s analyzedSearch) LiteralAnalyzed(ctx context.Context, q domain.Query) ([]domain.Evidence, int, error) {
	items, err := s.Literal(ctx, q)
	if s.analysisErr != nil {
		err = s.analysisErr
	}
	return items, s.count, err
}

func TestChineseEngineWordProofRequiresExactIndexedOriginalAndCompleteCount(t *testing.T) {
	for _, kind := range []string{"current", "mismatched_text", "missing_count", "ten_words", "recent", "quoted"} {
		t.Run(kind, func(t *testing.T) {
			raw := message("a", "预算需要两人审批")
			candidate := evidence("a", raw)
			candidate.Text = raw.Text
			idx := analyzedSearch{search: &search{literal: []domain.Evidence{candidate}}, count: 2}
			pg := &source{messages: map[string]domain.Message{"a": raw}}
			q := query(domain.QueryLiteral, "预算审批")
			switch kind {
			case "mismatched_text":
				idx.literal[0].Text = "预算审批"
			case "missing_count":
				idx.count = 0
			case "ten_words":
				idx.count = 10
			case "recent":
				idx.literal = nil
				pg.recent = []domain.Message{raw}
			case "quoted":
				q.Text = `"预算审批"`
			}
			service, err := retrieval.New(retrieval.Options{Sources: pg, Search: idx})
			if err != nil {
				t.Fatal(err)
			}
			r, err := service.Retrieve(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == "current" {
				want = 1
			}
			if len(r.Evidence) != want {
				t.Fatalf("invalid engine proof acceptance for %s: %+v", kind, r)
			}
			if (kind == "missing_count" || kind == "ten_words" || kind == "recent") && !r.Degraded {
				t.Fatal("missing tokenizer proof silently treated as complete")
			}
		})
	}
}
