package retrieval_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestSemanticEvidenceKeepsDerivedContentSeparateFromOriginal(t *testing.T) {
	for _, mode := range []domain.QueryMode{domain.QueryDiscussion, domain.QueryMemory} {
		t.Run(string(mode), func(t *testing.T) {
			raw := message("media", "")
			derived := "OCR: the launch date is Friday"
			if mode == domain.QueryMemory {
				raw.Text = "canonical original"
				derived = "Consolidated decision and its rationale"
			}
			candidate := evidence("context", raw)
			candidate.Text = derived
			pg := &source{messages: map[string]domain.Message{"media": raw}}
			idx := &search{keyword: []domain.Evidence{candidate}}
			mem := &memory{items: []domain.Evidence{candidate}}
			mdl := &model{}
			result, err := makeService(t, pg, idx, mem, mdl).Retrieve(context.Background(), query(mode, "launch decision"))
			if err != nil || len(result.Evidence) != 1 {
				t.Fatalf("valid derived evidence missing: %+v %v", result, err)
			}
			got := result.Evidence[0]
			if got.Text != raw.Text || got.DerivedText != derived || !reflect.DeepEqual(got.Sources, candidate.Sources) {
				t.Fatalf("original and derived were not kept distinct: %+v", got)
			}
			if mode == domain.QueryDiscussion && (len(mdl.input) != 1 || mdl.input[0].DerivedText != derived || mdl.input[0].Text != raw.Text) {
				t.Fatal("reranker cannot see both validated content fields")
			}
		})
	}
}

func TestRerankerCannotRewriteDerivedContent(t *testing.T) {
	raw := message("a", "canonical")
	candidate := evidence("context", raw)
	candidate.Text = "validated OCR"
	pg := &source{messages: map[string]domain.Message{"a": raw}}
	idx := &search{keyword: []domain.Evidence{candidate}}
	mdl := &model{rewrite: func(in []domain.Evidence) []domain.Evidence {
		in[0].Text = "rewritten original"
		in[0].DerivedText = "rewritten derived"
		in[0].Sources[0].AuthorID = "intruder"
		return in
	}}
	result, err := makeService(t, pg, idx, &memory{}, mdl).Retrieve(context.Background(), query(domain.QueryDiscussion, "OCR"))
	if err != nil || len(result.Evidence) != 1 || result.Evidence[0].Text != raw.Text || result.Evidence[0].DerivedText != candidate.Text || result.Evidence[0].Sources[0].AuthorID != raw.Source.AuthorID {
		t.Fatalf("model changed content/provenance: %+v %v", result, err)
	}
}

func TestLiteralAndRecentNeverClaimDerivedText(t *testing.T) {
	raw := message("a", "canonical")
	candidate := evidence("indexed", raw)
	candidate.Text = "untrusted raw index"
	candidate.DerivedText = "injected derived"
	pg := &source{messages: map[string]domain.Message{"a": raw}}
	idx := &search{literal: []domain.Evidence{candidate}}
	result, err := makeService(t, pg, idx, &memory{}, &model{}).Retrieve(context.Background(), query(domain.QueryLiteral, "canonical"))
	if err != nil || len(result.Evidence) != 1 || result.Evidence[0].Text != raw.Text || result.Evidence[0].DerivedText != "" {
		t.Fatalf("raw index text promoted to derived: %+v %v", result, err)
	}
	pg.recent = []domain.Message{raw}
	result, err = makeService(t, pg, &search{}, &memory{}, &model{}).Retrieve(context.Background(), query(domain.QueryDiscussion, "canonical"))
	if err != nil || len(result.Evidence) != 1 || result.Evidence[0].DerivedText != "" {
		t.Fatalf("recent original relabeled as derived: %+v %v", result, err)
	}
}

func TestDistinctMemoriesSharingCompleteSourcesAreNotCollapsed(t *testing.T) {
	raw := message("a", "canonical")
	one, two := evidence("one", raw), evidence("two", raw)
	one.Text = "Decision"
	two.Text = "Reason for the decision"
	pg := &source{messages: map[string]domain.Message{"a": raw}}
	result, err := makeService(t, pg, &search{}, &memory{items: []domain.Evidence{one, two}}, &model{}).Retrieve(context.Background(), query(domain.QueryMemory, "decision"))
	if err != nil || len(result.Evidence) != 2 || result.Evidence[0].DerivedText == result.Evidence[1].DerivedText {
		t.Fatalf("distinct facts collapsed by shared provenance: %+v %v", result, err)
	}
}

func TestDerivedTextIsUTF8AndByteBoundedIncludingCombinedResults(t *testing.T) {
	raw := message("a", "canonical")
	pg := &source{messages: map[string]domain.Message{"a": raw}}
	for _, text := range []string{string([]byte{0xff}), strings.Repeat("x", domain.MaxPayloadBytes+1)} {
		candidate := evidence("invalid", raw)
		candidate.Text = text
		result, err := makeService(t, pg, &search{keyword: []domain.Evidence{candidate}}, &memory{}, &model{}).Retrieve(context.Background(), query(domain.QueryDiscussion, "x"))
		if err != nil || len(result.Evidence) != 0 {
			t.Fatal("invalid or oversized derived text accepted")
		}
	}
	var candidates []domain.Evidence
	for i := range 9 {
		id := string(rune('a' + i))
		m := message(id, "canonical")
		pg.messages[id] = m
		e := evidence(id, m)
		e.Text = strings.Repeat("x", domain.MaxPayloadBytes)
		candidates = append(candidates, e)
	}
	result, err := makeService(t, pg, &search{keyword: candidates}, &memory{}, &model{}).Retrieve(context.Background(), query(domain.QueryDiscussion, "x"))
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, e := range result.Evidence {
		total += len(e.Text) + len(e.DerivedText)
	}
	if len(result.Evidence) == 0 || len(result.Evidence) == len(candidates) || total > 8<<20 {
		t.Fatalf("combined evidence budget not enforced: count=%d bytes=%d", len(result.Evidence), total)
	}
}

func TestSemanticAuthorMatchesAnySourceButProvesEveryDependency(t *testing.T) {
	for _, mode := range []domain.QueryMode{domain.QueryDiscussion, domain.QueryMemory} {
		t.Run(string(mode), func(t *testing.T) {
			alice, bob := message("alice", "Alice original"), message("bob", "Bob original")
			bob.Source.AuthorID = "bob"
			candidate := evidence("mixed", alice, bob)
			for _, author := range []string{"alice", "bob", "carol"} {
				t.Run(author, func(t *testing.T) {
					pg := &source{messages: map[string]domain.Message{"alice": alice, "bob": bob}}
					q := query(mode, "discussion")
					q.AuthorID = author
					r, err := makeService(t, pg, &search{keyword: []domain.Evidence{candidate}}, &memory{items: []domain.Evidence{candidate}}, &model{}).Retrieve(context.Background(), q)
					if err != nil {
						t.Fatal(err)
					}
					want := 1
					if author == "carol" {
						want = 0
					}
					if len(r.Evidence) != want {
						t.Fatalf("ANY-author inclusion failed: author=%s got=%d", author, len(r.Evidence))
					}
					if want == 1 && !reflect.DeepEqual(r.Evidence[0].Sources, candidate.Sources) {
						t.Fatal("nonmatching author's dependency was removed")
					}
				})
			}
			for _, invalid := range []string{"stale", "deleted", "future", "scope", "author"} {
				t.Run(invalid, func(t *testing.T) {
					changed := bob
					switch invalid {
					case "stale":
						changed.Source.Version++
					case "deleted":
						changed.Deleted = true
					case "future":
						changed.UpdatedAt = anchor.Add(time.Nanosecond)
					case "scope":
						changed.Source.Scope.AppID = "other"
					case "author":
						changed.Source.AuthorID = "mallory"
					}
					pg := &source{messages: map[string]domain.Message{"alice": alice, "bob": changed}}
					q := query(mode, "discussion")
					q.AuthorID = "alice"
					r, err := makeService(t, pg, &search{keyword: []domain.Evidence{candidate}}, &memory{items: []domain.Evidence{candidate}}, &model{}).Retrieve(context.Background(), q)
					if err != nil || len(r.Evidence) != 0 {
						t.Fatalf("nonmatching-author invalid dependency survived: %s %+v %v", invalid, r, err)
					}
				})
			}
		})
	}
}
