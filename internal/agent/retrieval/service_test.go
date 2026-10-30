package retrieval_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
)

var scope = domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}
var anchor = time.Date(2026, 10, 22, 12, 0, 0, 987654321, time.UTC)

func message(id, text string) domain.Message {
	return domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: id, Version: 9007199254740993, AuthorID: "alice", OccurredAt: anchor.Add(-time.Minute)}, Text: text, UpdatedAt: anchor.Add(-time.Second)}
}
func evidence(id string, messages ...domain.Message) domain.Evidence {
	e := domain.Evidence{ID: id, Text: "untrusted derived text"}
	for _, m := range messages {
		e.Sources = append(e.Sources, m.Source)
	}
	return e
}

type source struct {
	messages map[string]domain.Message
	recent   []domain.Message
	err      error
	proofs   int
}

func (s *source) Message(_ context.Context, _ domain.Scope, id string) (domain.Message, error) {
	if s.err != nil {
		return domain.Message{}, s.err
	}
	m, ok := s.messages[id]
	if !ok {
		return m, domain.ErrNotFound
	}
	return m.Clone(), nil
}
func (s *source) Recent(context.Context, domain.Scope, time.Time, int) ([]domain.Message, error) {
	return s.recent, s.err
}
func (s *source) ValidSources(_ context.Context, q domain.Query, refs []domain.SourceRef) (bool, error) {
	s.proofs++
	if s.err != nil {
		return false, s.err
	}
	for _, ref := range refs {
		m, ok := s.messages[ref.MessageID]
		if !ok || m.Deleted || m.Source != ref || ref.Scope != q.Scope || ref.OccurredAt.After(q.Anchor) || m.UpdatedAt.After(q.Anchor) || (q.AuthorID != "" && q.AuthorID != ref.AuthorID) {
			return false, nil
		}
	}
	return len(refs) > 0, nil
}

type search struct {
	literal, keyword, vector []domain.Evidence
	calls                    []string
	queries                  []domain.Query
	fail                     map[string]error
}

func (s *search) get(ch string, q domain.Query, items []domain.Evidence) ([]domain.Evidence, error) {
	s.calls = append(s.calls, ch)
	s.queries = append(s.queries, q)
	return items, s.fail[ch]
}
func (s *search) Literal(_ context.Context, q domain.Query) ([]domain.Evidence, error) {
	return s.get("literal", q, s.literal)
}
func (s *search) Keyword(_ context.Context, q domain.Query) ([]domain.Evidence, error) {
	return s.get("keyword", q, s.keyword)
}
func (s *search) Vector(_ context.Context, q domain.Query, _ []float32) ([]domain.Evidence, error) {
	return s.get("vector", q, s.vector)
}

type memory struct {
	items []domain.Evidence
	calls int
	err   error
}

func (m *memory) Recall(context.Context, domain.Query) ([]domain.Evidence, error) {
	m.calls++
	return m.items, m.err
}

type model struct {
	embeds, reranks       int
	question              string
	input                 []domain.Evidence
	embedError, errorRank error
	rewrite               func([]domain.Evidence) []domain.Evidence
	embedded              []string
}

func (m *model) Embed(_ context.Context, text []string) ([][]float32, error) {
	m.embeds++
	m.embedded = append([]string(nil), text...)
	return [][]float32{{1, 0}}, m.embedError
}
func (m *model) Rerank(_ context.Context, q string, in []domain.Evidence) ([]domain.Evidence, error) {
	m.reranks++
	m.question = q
	m.input = append([]domain.Evidence(nil), in...)
	if m.rewrite != nil {
		return m.rewrite(in), m.errorRank
	}
	return in, m.errorRank
}
func query(mode domain.QueryMode, text string) domain.Query {
	return domain.Query{Scope: scope, Anchor: anchor, Mode: mode, Text: text, Limit: 20}
}
func makeService(t *testing.T, pg *source, idx *search, mem *memory, mdl *model) *retrieval.Service {
	t.Helper()
	s, err := retrieval.New(retrieval.Options{Sources: pg, Search: idx, Memory: mem, Model: mdl, RecentLimit: 32})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMixedCandidatesRequireEveryCurrentSourceAndReturnPGOriginal(t *testing.T) {
	fresh := message("fresh", "exact original")
	stale := message("stale", "old")
	deleted := message("deleted", "deleted")
	future := message("future", "future")
	cross := message("cross", "cross")
	missing := message("missing", "absent")
	wrongAuthor := message("author", "author")
	wrongTime := message("time", "time")
	old := stale
	stale.Source.Version++
	deleted.Deleted = true
	future.UpdatedAt = anchor.Add(time.Nanosecond)
	cross.Source.Scope.AppID = "other"
	wrongAuthor.Source.AuthorID = "mallory"
	oldTime := wrongTime
	wrongTime.Source.OccurredAt = wrongTime.Source.OccurredAt.Add(time.Nanosecond)
	pg := &source{messages: map[string]domain.Message{"fresh": fresh, "stale": stale, "deleted": deleted, "future": future, "cross": cross, "author": wrongAuthor, "time": wrongTime}}
	idx := &search{literal: []domain.Evidence{evidence("good", fresh), evidence("stale", old), evidence("deleted", deleted), evidence("future", future), evidence("cross", cross), evidence("missing", missing), evidence("partial", fresh, old), evidence("time", oldTime), {ID: "unsourced", Text: "exact"}}}
	result, err := makeService(t, pg, idx, &memory{}, &model{}).Retrieve(context.Background(), query(domain.QueryLiteral, "exact"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].ID != "good" || result.Evidence[0].Text != fresh.Text {
		t.Fatalf("invalid evidence survived or original not reloaded: %+v", result)
	}
	if pg.proofs == 0 {
		t.Fatal("no authoritative full-set proof")
	}
}

func TestExplicitModesKeepLiteralFreeOfModelsAndDiscussionUsesSeparateFusion(t *testing.T) {
	a, b, c := message("a", "alpha"), message("b", "beta"), message("c", "gamma")
	for _, mode := range []domain.QueryMode{domain.QueryLiteral, domain.QueryDiscussion, domain.QueryMemory} {
		t.Run(string(mode), func(t *testing.T) {
			pg := &source{messages: map[string]domain.Message{"a": a, "b": b, "c": c}}
			idx := &search{literal: []domain.Evidence{evidence("a", a)}, keyword: []domain.Evidence{evidence("a", a), evidence("b", b)}, vector: []domain.Evidence{evidence("b", b), evidence("c", c)}}
			mem := &memory{items: []domain.Evidence{evidence("memory", a, b)}}
			mdl := &model{}
			q := query(mode, "alpha")
			result, err := makeService(t, pg, idx, mem, mdl).Retrieve(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case domain.QueryLiteral:
				if !reflect.DeepEqual(idx.calls, []string{"literal"}) || mdl.embeds+mdl.reranks+mem.calls != 0 {
					t.Fatal("literal invoked semantic path")
				}
			case domain.QueryDiscussion:
				if !reflect.DeepEqual(idx.calls, []string{"keyword", "vector"}) || mdl.embeds != 1 || mdl.reranks != 1 || mem.calls != 0 {
					t.Fatalf("discussion routing: %v", idx.calls)
				}
				if len(result.Evidence) != 3 || result.Evidence[0].ID != "b" {
					t.Fatalf("unstable RRF/dedupe: %+v", result)
				}
			case domain.QueryMemory:
				if len(idx.calls) != 0 || mem.calls != 1 || mdl.embeds+mdl.reranks != 0 {
					t.Fatal("memory routing")
				}
				if result.Evidence[0].Text != "alpha\nbeta" {
					t.Fatalf("memory did not return originals: %+v", result)
				}
			}
		})
	}
}

func TestLiteralChecksCompleteLongPhraseAndTokenBoundaries(t *testing.T) {
	phrase := "one two three four five six seven eight nine ten eleven twelve"
	partial := message("partial", "one two three four five six seven eight nine ten eleven X")
	full := message("full", phrase)
	prefix := message("prefix", "alphabet")
	pg := &source{messages: map[string]domain.Message{"partial": partial, "full": full, "prefix": prefix}}
	idx := &search{literal: []domain.Evidence{evidence("partial", partial), evidence("full", full)}}
	s := makeService(t, pg, idx, &memory{}, &model{})
	r, err := s.Retrieve(context.Background(), query(domain.QueryLiteral, `"`+phrase+`"`))
	if err != nil || len(r.Evidence) != 1 || r.Evidence[0].ID != "full" {
		t.Fatalf("partial quote accepted: %+v %v", r, err)
	}
	idx.literal = []domain.Evidence{evidence("prefix", prefix)}
	r, err = s.Retrieve(context.Background(), query(domain.QueryLiteral, "alpha"))
	if err != nil || len(r.Evidence) != 0 {
		t.Fatal("prefix accepted as a complete token")
	}
	if _, err = s.Retrieve(context.Background(), query(domain.QueryLiteral, `"unclosed`)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("malformed phrase accepted")
	}
}

func TestLiteralCannotAssembleAPhraseAcrossDifferentMessages(t *testing.T) {
	a, b := message("a", "alpha"), message("b", "beta")
	pg := &source{messages: map[string]domain.Message{"a": a, "b": b}}
	idx := &search{literal: []domain.Evidence{evidence("joined", a, b)}}
	r, err := makeService(t, pg, idx, &memory{}, &model{}).Retrieve(context.Background(), query(domain.QueryLiteral, `"alpha beta"`))
	if err != nil || len(r.Evidence) != 0 {
		t.Fatal("literal phrase assembled from different messages")
	}
}

func TestQuotedLiteralPreservesExactRawCaseAndWhitespace(t *testing.T) {
	for _, tc := range []struct {
		name, text, query string
		want              int
	}{
		{"exact", "prefix alpha beta suffix", `"alpha beta"`, 1},
		{"newline_mismatch", "alpha\nbeta", `"alpha beta"`, 0},
		{"tab_mismatch", "alpha\tbeta", `"alpha beta"`, 0},
		{"extra_space_mismatch", "alpha  beta", `"alpha beta"`, 0},
		{"source_case_mismatch", "Alpha Beta", `"alpha beta"`, 0},
		{"query_case_mismatch", "alpha beta", `"Alpha Beta"`, 0},
		{"exact_extra_spaces", "alpha  beta", `"alpha  beta"`, 1},
		{"exact_newline", "alpha\nbeta", "\"alpha\nbeta\"", 1},
		{"mixed_term_phrase", "alpha beta gamma", `GAMMA "alpha beta"`, 1},
		{"mixed_rejects_changed_phrase", "Alpha beta gamma", `GAMMA "alpha beta"`, 0},
		{"mixed_rejects_missing_term", "alpha beta delta", `GAMMA "alpha beta"`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := message("a", tc.text)
			pg := &source{messages: map[string]domain.Message{"a": m}}
			idx := &search{literal: []domain.Evidence{evidence("a", m)}}
			r, err := makeService(t, pg, idx, &memory{}, &model{}).Retrieve(context.Background(), query(domain.QueryLiteral, tc.query))
			if err != nil || len(r.Evidence) != tc.want {
				t.Fatalf("quoted literal raw-text match: got %d, want %d, error %v", len(r.Evidence), tc.want, err)
			}
		})
	}
}

func TestLongQuestionUsesBoundedDeterministicExpressionAndOriginalRerank(t *testing.T) {
	m := message("a", "trusted")
	pg := &source{messages: map[string]domain.Message{"a": m}}
	idx := &search{keyword: []domain.Evidence{evidence("a", m)}}
	mdl := &model{}
	s := makeService(t, pg, idx, &memory{}, mdl)
	text := strings.Repeat("long semantic question about a discussion and its decision ", 100)
	q := query(domain.QueryDiscussion, text)
	if _, err := s.Retrieve(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if mdl.question != text || len(mdl.embedded) != 1 || len([]rune(mdl.embedded[0])) > 128 || len(strings.Fields(idx.queries[0].Text)) > 10 || idx.queries[0].Text != mdl.embedded[0] {
		t.Fatal("retrieval expression or rerank question incorrect")
	}
	for _, seen := range idx.queries {
		if seen.Scope != q.Scope || seen.AuthorID != q.AuthorID || seen.Anchor != q.Anchor {
			t.Fatal("server-owned filters altered")
		}
	}
}

func TestRecentPGCoversIndexGapAndIsNotAnExhaustiveFallback(t *testing.T) {
	m := message("gap", "search target")
	pg := &source{messages: map[string]domain.Message{"gap": m}, recent: []domain.Message{m}}
	idx := &search{fail: map[string]error{"literal": errors.New("secret document")}}
	mdl := &model{}
	r, err := makeService(t, pg, idx, &memory{}, mdl).Retrieve(context.Background(), query(domain.QueryLiteral, "target"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Evidence) != 1 || r.Evidence[0].Channel != "recent" || !r.Degraded || len(r.Failures) != 1 || r.Failures[0].Channel != "literal" || r.RecentWindow != 32 {
		t.Fatalf("index gap/degradation lost: %+v", r)
	}
	if strings.Contains(r.Failures[0].Reason, "secret") {
		t.Fatal("upstream error leaked")
	}
}

func TestChannelFailuresAreExplicitAndPGErrorsFailClosed(t *testing.T) {
	m := message("a", "source")
	for _, branch := range []string{"keyword", "vector", "embedding", "rerank", "memory"} {
		t.Run(branch, func(t *testing.T) {
			pg := &source{messages: map[string]domain.Message{"a": m}}
			idx := &search{keyword: []domain.Evidence{evidence("a", m)}, vector: []domain.Evidence{evidence("a", m)}, fail: map[string]error{}}
			mdl := &model{}
			mem := &memory{}
			q := query(domain.QueryDiscussion, "source")
			boom := errors.New("private")
			switch branch {
			case "keyword", "vector":
				idx.fail[branch] = boom
			case "embedding":
				mdl.embedError = boom
			case "rerank":
				mdl.errorRank = boom
			case "memory":
				mem.err = boom
				q.Mode = domain.QueryMemory
			}
			r, err := makeService(t, pg, idx, mem, mdl).Retrieve(context.Background(), q)
			if err != nil || !r.Degraded || len(r.Failures) == 0 {
				t.Fatalf("unreported channel failure: %+v %v", r, err)
			}
		})
	}
	pg := &source{err: errors.New("private DB detail")}
	r, err := makeService(t, pg, &search{}, &memory{}, &model{}).Retrieve(context.Background(), query(domain.QueryLiteral, "x"))
	if err == nil || len(r.Evidence) != 0 || strings.Contains(err.Error(), "private") {
		t.Fatal("PG failure did not fail closed safely")
	}
}

func TestRerankerCannotInjectOrRewriteEvidence(t *testing.T) {
	for _, kind := range []string{"rewrite", "unknown", "duplicate", "nan"} {
		t.Run(kind, func(t *testing.T) {
			a, b := message("a", "alpha"), message("b", "beta")
			pg := &source{messages: map[string]domain.Message{"a": a, "b": b}}
			idx := &search{keyword: []domain.Evidence{evidence("a", a), evidence("b", b)}}
			mdl := &model{rewrite: func(in []domain.Evidence) []domain.Evidence {
				switch kind {
				case "rewrite":
					in[0].Text = "injected"
					in[0].Sources[0].Version = 1
				case "unknown":
					in[0].ID = "invented"
				case "duplicate":
					in[1].ID = in[0].ID
				case "nan":
					in[0].Score = math.NaN()
				}
				return in
			}}
			r, err := makeService(t, pg, idx, &memory{}, mdl).Retrieve(context.Background(), query(domain.QueryDiscussion, "question"))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Evidence) != 2 || r.Evidence[0].Text != "alpha" || r.Evidence[0].Sources[0].Version != a.Source.Version {
				t.Fatalf("model altered proof: %+v", r)
			}
			if kind != "rewrite" && !r.Degraded {
				t.Fatal("malformed ranking not degraded")
			}
		})
	}
}

func TestAuthorOnlyLiteralAndUnbalancedSemanticQuotes(t *testing.T) {
	m := message("a", "source original")
	pg := &source{messages: map[string]domain.Message{"a": m}}
	idx := &search{literal: []domain.Evidence{evidence("a", m)}, keyword: []domain.Evidence{evidence("a", m)}}
	s := makeService(t, pg, idx, &memory{}, &model{})
	q := query(domain.QueryLiteral, "")
	q.AuthorID = "alice"
	r, err := s.Retrieve(context.Background(), q)
	if err != nil || len(r.Evidence) != 1 {
		t.Fatalf("author-only search rejected: %v", err)
	}
	q.AuthorID = "mallory"
	r, err = s.Retrieve(context.Background(), q)
	if err != nil || len(r.Evidence) != 0 {
		t.Fatal("author filter not proved against originals")
	}
	q = query(domain.QueryDiscussion, `explain why someone wrote "an incomplete quote`)
	if _, err = s.Retrieve(context.Background(), q); err != nil {
		t.Fatalf("semantic text treated as literal syntax: %v", err)
	}
}

type failingSources struct {
	*source
	at string
}

func (s failingSources) Message(ctx context.Context, scope domain.Scope, id string) (domain.Message, error) {
	if s.at == "message" {
		return domain.Message{}, errors.New("private database detail")
	}
	return s.source.Message(ctx, scope, id)
}
func (s failingSources) ValidSources(ctx context.Context, q domain.Query, refs []domain.SourceRef) (bool, error) {
	if s.at == "proof" {
		return false, errors.New("private database detail")
	}
	return s.source.ValidSources(ctx, q, refs)
}
func TestEachAuthoritativeReadErrorFailsClosed(t *testing.T) {
	m := message("a", "alpha")
	for _, stage := range []string{"message", "proof"} {
		t.Run(stage, func(t *testing.T) {
			pg := failingSources{source: &source{messages: map[string]domain.Message{"a": m}}, at: stage}
			s, err := retrieval.New(retrieval.Options{Sources: pg, Search: &search{literal: []domain.Evidence{evidence("a", m)}}})
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Retrieve(context.Background(), query(domain.QueryLiteral, "alpha"))
			if err == nil || len(r.Evidence) != 0 || strings.Contains(err.Error(), "private") {
				t.Fatal("authoritative read did not fail closed safely")
			}
		})
	}
}

func TestMemoryRejectsWholeObservationWhenOnePGDependencyInvalid(t *testing.T) {
	for _, kind := range []string{"stale", "deleted", "future", "missing", "crossscope"} {
		t.Run(kind, func(t *testing.T) {
			fresh, other := message("fresh", "trusted"), message("other", "dependency")
			refs := evidence("observation", fresh, other)
			pg := &source{messages: map[string]domain.Message{"fresh": fresh, "other": other}}
			switch kind {
			case "stale":
				other.Source.Version++
				pg.messages["other"] = other
			case "deleted":
				other.Deleted = true
				pg.messages["other"] = other
			case "future":
				other.UpdatedAt = anchor.Add(time.Nanosecond)
				pg.messages["other"] = other
			case "missing":
				delete(pg.messages, "other")
			case "crossscope":
				refs.Sources[1].Scope.TenantID = "other"
			}
			memory := &memory{items: []domain.Evidence{refs}}
			r, err := makeService(t, pg, &search{}, memory, &model{}).Retrieve(context.Background(), query(domain.QueryMemory, "decision"))
			if err != nil || len(r.Evidence) != 0 {
				t.Fatalf("partially valid observation survived (%s): %+v %v", kind, r, err)
			}
		})
	}
}
