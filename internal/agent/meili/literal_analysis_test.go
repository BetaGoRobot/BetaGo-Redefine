package meili

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLiteralAnalysisUsesRelaxedQueryOnlyForWordCount(t *testing.T) {
	m := message()
	d := messageDTO{metadata: meta(m.Source.Scope, m.Source.MessageID, m.Text, m.Source.OccurredAt, m.UpdatedAt), MessageID: m.Source.MessageID, AuthorID: m.Source.AuthorID, AuthorHash: authorHash(m.Source.Scope, m.Source.AuthorID), Source: source(m.Source)}
	var primary map[string]any
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := decodeBody(t, r)
		if body["matchingStrategy"] == "all" {
			primary = body
			writeJSON(w, map[string]any{"hits": []any{d}})
			return
		}
		if body["matchingStrategy"] != "last" || body["q"] != primary["q"] || body["filter"] != primary["filter"] || body["limit"] != float64(1) || body["showRankingScoreDetails"] != true || !reflect.DeepEqual(body["attributesToRetrieve"], []any{"id"}) {
			t.Errorf("analysis count escaped original query/filter/budget: %#v", body)
		}
		writeJSON(w, map[string]any{"hits": []any{map[string]any{"id": "relaxed-only", "text": "never evidence", "_rankingScoreDetails": map[string]any{"words": map[string]any{"matchingWords": 1, "maxMatchingWords": 9}}}}})
	}))
	defer srv.Close()
	q := query()
	q.AuthorID = m.Source.AuthorID
	hits, count, err := newClient(t, srv.URL).LiteralAnalyzed(context.Background(), q)
	if err != nil || count != 9 || len(hits) != 1 || hits[0].Text != m.Text || hits[0].Sources[0] != m.Source || calls != 2 {
		t.Fatalf("literal analysis lost primary evidence/count: count=%d hits=%+v calls=%d error=%v", count, hits, calls, err)
	}
}

func TestLiteralAnalysisRejectsMissingOrMalformedWordCounts(t *testing.T) {
	m := message()
	d := messageDTO{metadata: meta(m.Source.Scope, m.Source.MessageID, m.Text, m.Source.OccurredAt, m.UpdatedAt), MessageID: m.Source.MessageID, AuthorID: m.Source.AuthorID, Source: source(m.Source)}
	for _, words := range []any{nil, map[string]any{}, map[string]any{"matchingWords": 0, "maxMatchingWords": 3}, map[string]any{"matchingWords": 3, "maxMatchingWords": 2}, map[string]any{"matchingWords": 1, "maxMatchingWords": 0}, map[string]any{"matchingWords": 1, "maxMatchingWords": "9"}} {
		t.Run("malformed", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := decodeBody(t, r)
				if body["matchingStrategy"] == "all" {
					writeJSON(w, map[string]any{"hits": []any{d}})
				} else {
					writeJSON(w, map[string]any{"hits": []any{map[string]any{"_rankingScoreDetails": map[string]any{"words": words}}}})
				}
			}))
			defer srv.Close()
			if _, _, err := newClient(t, srv.URL).LiteralAnalyzed(context.Background(), query()); err == nil {
				t.Fatal("unverifiable analyzed count accepted")
			}
		})
	}
}
