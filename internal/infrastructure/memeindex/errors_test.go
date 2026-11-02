package memeindex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

func TestMalformedLaneIsFailedRatherThanSuccessfulEmpty(t *testing.T) {
	for _, body := range []string{`{"hits":{}}`, `{"hits":{"hits":null}}`, `{"hits":{"hits":[]},"timed_out":true}`, `{"hits":{"hits":[]},"_shards":{"failed":1}}`, `{"status":503,"error":{"reason":"private dialogue"}}`} {
		lane, _ := decodeLane(json.RawMessage(body), memes.SearchQuery{Field: memes.FieldKeys})
		if lane.Status != "error" || len(lane.Hits) > 0 || strings.Contains(lane.Error, "private") {
			t.Errorf("malformed lane reported success: %s -> %+v", body, lane)
		}
	}
}
func TestMalformedHitIsolatedAndFrozenMetadataRetained(t *testing.T) {
	record := fixture(t, "a")
	source := document{UseID: record.UseID, Text: record.Relation, Full: record.Full, Projection: record.Projection, Fingerprint: record.Fingerprint, RelationKinds: []string{"benefit_cost"}}
	raw, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_id": "bad", "_score": 8, "_source": map[string]any{"full": map[string]any{"use_id": 7}}}, map[string]any{"_id": documentID(record.UseID), "_score": 2, "_source": source}}}})
	result, warnings := decodeLane(raw, memes.SearchQuery{Field: memes.FieldRelation})
	if result.Status != "ok" || len(result.Hits) != 1 || len(warnings) != 1 {
		t.Fatalf("bad hit contaminated valid sibling: %+v %+v", result, warnings)
	}
	hit := result.Hits[0]
	if hit.Rank != 2 || hit.Fingerprint != record.Fingerprint || hit.CardFingerprint != record.Projection.CardFingerprint || len(hit.Projection.Relations) != 1 {
		t.Fatalf("evidence lost: %+v", hit)
	}
}
func TestHTTPSearchFailureSanitizedAndNeverSuccessfulEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		io.WriteString(w, `{"error":"password secret current dialogue"}`)
	}))
	defer server.Close()
	c, _ := New(Options{Endpoint: server.URL, Username: "private-user", Password: "private-secret", IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", Embedder: fixedEmbedder()})
	ret, _ := c.ForGeneration(manifest())
	snapshot, err := ret.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q", Field: memes.FieldRelation, Text: "private-current-dialogue"}}})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SuccessfulQueries != 0 || !snapshot.Degraded {
		t.Fatalf("failure became empty success: %+v", snapshot)
	}
	if len(snapshot.RawQueries) != 1 || snapshot.RawQueries[0].Status != "error" {
		t.Fatalf("failed raw query has no failure status: %+v", snapshot.RawQueries)
	}
	for _, q := range snapshot.Queries {
		for _, lane := range q.Channels {
			if lane.Status != "error" || strings.Contains(lane.Error, "secret") || strings.Contains(lane.Error, server.URL) || strings.Contains(lane.Error, "dialogue") {
				t.Fatalf("error leaked details: %+v", lane)
			}
		}
	}
}

type trackedReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackedReadCloser) Close() error { r.closed = true; return nil }
func TestHTTPResponsesAreLimitedClosedAndErrorsSanitized(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		content := "private body"
		if oversized {
			content = strings.Repeat("x", maxResponseBytes+1)
		}
		body := &trackedReadCloser{Reader: strings.NewReader(content)}
		hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 503, Header: make(http.Header), Body: body}, nil
		})}
		c, _ := New(Options{Endpoint: "http://private-host.invalid", IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", HTTPClient: hc})
		status, _, err := c.request(context.Background(), http.MethodGet, "/test", nil, "")
		if !body.closed || status != 503 {
			t.Fatal("body leaked")
		}
		if oversized && err != ErrResponse {
			t.Fatalf("oversized body was accepted: %v", err)
		}
	}
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("private-url/private-secret/private-dialogue")
	})}
	c, _ := New(Options{Endpoint: "http://private-host.invalid", IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", HTTPClient: hc})
	_, _, err := c.request(context.Background(), http.MethodGet, "/test", nil, "")
	if err != ErrTransport {
		t.Fatalf("transport error was not sanitized: %v", err)
	}
}
func TestHTTPRedirectCannotForwardCredentialsOrQuery(t *testing.T) {
	calls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "u" || password != "p" {
			t.Error("authentication missing")
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, _ := New(Options{Endpoint: source.URL, Username: "u", Password: "p", IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture"})
	status, _, err := c.request(context.Background(), http.MethodPost, "/_msearch", []byte("private dialogue"), "")
	if err != nil || status != 307 || calls != 0 {
		t.Fatalf("redirect forwarded data: status=%d calls=%d err=%v", status, calls, err)
	}
}

type failingLedger struct{}

func (failingLedger) Put(context.Context, memestore.Kind, string, any) (memestore.Record, error) {
	return memestore.Record{}, fmt.Errorf("private database credentials")
}
func TestPublicationLedgerFailurePreventsRemoteWrite(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
	defer server.Close()
	c, _ := New(Options{Endpoint: server.URL, IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", Embedder: fixedEmbedder(), Ledger: failingLedger{}})
	manifest, err := c.Publish(context.Background(), "g1", []memes.IndexRecord{fixture(t, "a")})
	if err != ErrLedger || manifest.Verified || requests != 0 {
		t.Fatalf("unrecorded remote write escaped: %+v %v requests=%d", manifest, err, requests)
	}
}

func TestMalformedHitWarningSurvivesFrozenReplay(t *testing.T) {
	record := fixture(t, "a")
	source := document{UseID: record.UseID, Text: record.Relation, Full: record.Full, Projection: record.Projection, Fingerprint: record.Fingerprint, RelationKinds: []string{"benefit_cost"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"responses": []any{map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_id": "bad", "_score": 7, "_source": map[string]any{"full": map[string]any{"use_id": 7}}}, map[string]any{"_id": documentID(record.UseID), "_score": 2, "_source": source}}}}, map[string]any{"hits": map[string]any{"hits": []any{}}}}})
	}))
	defer server.Close()
	c, _ := New(Options{Endpoint: server.URL, IndexPrefix: "test", Dimensions: 3, EmbeddingModel: "fixture", Embedder: fixedEmbedder()})
	ret, _ := c.ForGeneration(manifest())
	snapshot, err := ret.Search(context.Background(), memes.QueryPlan{Queries: []memes.SearchQuery{{ID: "q", Field: memes.FieldRelation, Text: record.Relation}}})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Degraded || len(snapshot.Candidates) != 1 {
		t.Fatalf("malformed hit handling lost valid sibling: %+v", snapshot)
	}
	replay, err := memes.ValidateRetrievalSnapshot(snapshot)
	if err != nil || !replay.Degraded || len(replay.Candidates) != 1 {
		t.Fatalf("malformed hit evidence cannot be replayed: %v", err)
	}
}
