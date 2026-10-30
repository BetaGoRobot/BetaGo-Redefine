// Fixtures match the pinned Hindsight 0.10.2 http.py models and endpoints in
// /tmp/agent-clean-docs/hindsight-pinned-http.py, not a memory-quality benchmark.
package hindsight_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/hindsight"
)

var scope = domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}
var at = time.Date(2026, 10, 22, 12, 0, 0, 987654321, time.UTC)

func contextDocument() domain.Context {
	return domain.Context{ID: "discussion", Scope: scope, Version: 9007199254740995, Text: "verified discussion", CreatedAt: at.Add(-time.Minute), UpdatedAt: at, Sources: []domain.SourceRef{{Scope: scope, MessageID: "m1", Version: 9007199254740993, AuthorID: "alice", OccurredAt: at.Add(-time.Minute)}, {Scope: scope, MessageID: "m2", Version: 9007199254740994, AuthorID: "bob", OccurredAt: at.Add(-time.Second)}}}
}
func client(t *testing.T, url string, opts ...hindsight.Options) *hindsight.Client {
	t.Helper()
	o := hindsight.Options{URL: url, APIKey: "fixture-secret"}
	if len(opts) > 0 {
		o = opts[0]
		o.URL = url
		o.APIKey = "fixture-secret"
	}
	c, err := hindsight.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestRetainUsesPinnedSynchronousProtocolExactMetadataAndStableIDs(t *testing.T) {
	doc := contextDocument()
	var calls []string
	var metadata map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("missing authorization")
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut {
			respond(w, map[string]any{"bank_id": hindsight.BankID(scope)})
			return
		}
		var body struct {
			Async *bool `json:"async"`
			Items []struct {
				Content, Timestamp string
				DocumentID         string `json:"document_id"`
				Metadata           map[string]string
				Tags               []string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Async == nil || *body.Async || len(body.Items) != 1 {
			t.Error("retain must synchronously wait for one item")
		}
		item := body.Items[0]
		metadata = item.Metadata
		if item.DocumentID != hindsight.DocumentID(scope, doc.ID) || item.Timestamp != doc.UpdatedAt.Format(time.RFC3339Nano) || item.Content != doc.Text || !reflect.DeepEqual(item.Tags, []string{scope.Key()}) {
			t.Error("retention identity, timestamp, or tag mismatch")
		}
		respond(w, map[string]any{"success": true, "bank_id": hindsight.BankID(scope), "items_count": 1, "async": false})
	}))
	defer srv.Close()
	c := client(t, srv.URL)
	if err := c.Retain(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	want := []string{"PUT /v1/default/banks/" + hindsight.BankID(scope), "POST /v1/default/banks/" + hindsight.BankID(scope) + "/memories"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("wrong pinned endpoints: %v", calls)
	}
	raw, err := json.Marshal(metadata)
	if err != nil || !strings.Contains(string(raw), "9007199254740993") || !strings.Contains(string(raw), "9007199254740994") || !strings.Contains(string(raw), "987654321") {
		t.Fatal("exact versions/timestamps lost")
	}
	variants := []domain.Scope{scope, scope, scope, scope}
	variants[0].TenantID = "other"
	variants[1].AppID = "other"
	variants[2].ChatID = "other"
	variants[3].Visibility = "other"
	for _, s := range variants {
		if hindsight.BankID(s) == hindsight.BankID(scope) || hindsight.DocumentID(s, doc.ID) == hindsight.DocumentID(scope, doc.ID) {
			t.Fatal("scope isolation collision")
		}
	}
	if hindsight.DocumentID(scope, doc.ID) != hindsight.DocumentID(scope, doc.ID) {
		t.Fatal("unstable replacement document ID")
	}
}

func TestRetainRequiresCompleteAcknowledgement(t *testing.T) {
	for _, kind := range []string{"false", "wrongbank", "wrongcount", "async", "missingasync", "missingstatus"} {
		t.Run(kind, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					respond(w, map[string]any{"bank_id": hindsight.BankID(scope)})
					return
				}
				ack := map[string]any{"success": true, "bank_id": hindsight.BankID(scope), "items_count": 1, "async": false}
				switch kind {
				case "false":
					ack["success"] = false
				case "wrongbank":
					ack["bank_id"] = "other"
				case "wrongcount":
					ack["items_count"] = 0
				case "async":
					ack["async"] = true
				case "missingasync":
					delete(ack, "async")
				case "missingstatus":
					delete(ack, "success")
				}
				respond(w, ack)
			}))
			defer srv.Close()
			if err := client(t, srv.URL).Retain(context.Background(), contextDocument()); err == nil {
				t.Fatal("incomplete acknowledgement accepted")
			}
		})
	}
}

func TestDeleteDocumentIsIdempotentAndUsesExplicitDocumentID(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/default/banks/"+hindsight.BankID(scope)+"/documents/"+hindsight.DocumentID(scope, "discussion") {
			t.Error("wrong delete endpoint")
		}
		n++
		if n > 1 {
			http.Error(w, "Document not found", 404)
			return
		}
		respond(w, map[string]any{"success": true, "document_id": hindsight.DocumentID(scope, "discussion"), "memory_units_deleted": 3})
	}))
	defer srv.Close()
	c := client(t, srv.URL)
	for range 2 {
		if err := c.DeleteDocument(context.Background(), scope, hindsight.DocumentID(scope, "discussion")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecallExpandsAllSourceFactsAndRejectsUnverifiableObservations(t *testing.T) {
	for _, kind := range []string{"complete", "author-match", "author-missing", "author-match-missing-dependency", "missing", "truncated", "cycle", "unsourced", "unknown", "crossscope", "future", "badmetadata", "badversion", "wrongdoc", "wrongtag"} {
		t.Run(kind, func(t *testing.T) {
			doc := contextDocument()
			var metadata map[string]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPut:
					respond(w, map[string]any{"bank_id": hindsight.BankID(scope)})
				case strings.HasSuffix(r.URL.Path, "/memories"):
					var body struct {
						Items []struct{ Metadata map[string]string }
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					metadata = body.Items[0].Metadata
					respond(w, map[string]any{"success": true, "bank_id": hindsight.BankID(scope), "items_count": 1, "async": false})
				case strings.HasSuffix(r.URL.Path, "/memories/recall"):
					var request map[string]json.RawMessage
					_ = json.NewDecoder(r.Body).Decode(&request)
					var tags []string
					var match, stamp string
					_ = json.Unmarshal(request["tags"], &tags)
					_ = json.Unmarshal(request["tags_match"], &match)
					_ = json.Unmarshal(request["query_timestamp"], &stamp)
					var include map[string]json.RawMessage
					_ = json.Unmarshal(request["include"], &include)
					if !reflect.DeepEqual(tags, []string{scope.Key()}) || match != "all_strict" || stamp != at.Format(time.RFC3339Nano) || string(include["entities"]) != "null" || include["source_facts"] == nil || include["chunks"] == nil {
						t.Error("recall must request strict scoped dependencies with bounded budgets")
					}
					leaf := func(id string) map[string]any {
						return map[string]any{"id": id, "text": "raw fact", "type": "world", "metadata": metadata, "document_id": hindsight.DocumentID(scope, doc.ID), "tags": []string{scope.Key()}, "chunk_id": "chunk"}
					}
					one, two := leaf("f1"), leaf("f2")
					two["type"] = "experience"
					obs := map[string]any{"id": "o1", "text": "aggregate memory", "type": "observation", "source_fact_ids": []string{"f1", "f2"}, "tags": []string{scope.Key()}}
					facts := map[string]any{"f1": one, "f2": two}
					response := map[string]any{"results": []any{obs}, "source_facts": facts, "source_facts_truncated": false, "chunks": map[string]any{"chunk": map[string]any{"id": "chunk", "text": doc.Text, "chunk_index": 0}}}
					switch kind {
					case "missing", "author-match-missing-dependency":
						delete(facts, "f2")
					case "truncated":
						response["source_facts_truncated"] = true
					case "cycle":
						two["type"] = "observation"
						two["source_fact_ids"] = []string{"o1"}
						facts["o1"] = obs
					case "unsourced":
						two["metadata"] = nil
					case "unknown":
						two["type"] = "mental_model"
					case "crossscope":
						for k, v := range metadata {
							metadata[k] = strings.ReplaceAll(v, `"ChatID":"chat"`, `"ChatID":"other"`)
						}
					case "future":
						for k, v := range metadata {
							metadata[k] = strings.ReplaceAll(v, at.Format(time.RFC3339Nano), at.Add(time.Nanosecond).Format(time.RFC3339Nano))
						}
					case "badmetadata":
						two["metadata"] = map[string]string{"agent_provenance": "not json"}
					case "badversion":
						for k, v := range metadata {
							metadata[k] = strings.ReplaceAll(v, `"9007199254740993"`, `9007199254740993`)
						}
					case "wrongdoc":
						two["document_id"] = "unknown"
					case "wrongtag":
						two["tags"] = []string{"other"}
					}
					respond(w, response)
				default:
					t.Error("wrong recall endpoint")
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			c := client(t, srv.URL)
			if err := c.Retain(context.Background(), doc); err != nil {
				t.Fatal(err)
			}
			q := domain.Query{Scope: scope, Anchor: at, Text: "decision", Mode: domain.QueryMemory, Limit: 10}
			if strings.HasPrefix(kind, "author-match") {
				q.AuthorID = "alice"
			} else if kind == "author-missing" {
				q.AuthorID = "carol"
			}
			items, err := c.Recall(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "complete" || kind == "author-match" {
				if len(items) != 1 || !reflect.DeepEqual(items[0].Sources, doc.Sources) {
					t.Fatalf("lost complete exact dependency set: %+v", items)
				}
				if items[0].DerivedText != "aggregate memory" {
					t.Fatal("recall producer did not identify derived memory content")
				}
			} else if len(items) != 0 {
				t.Fatalf("unverifiable observation accepted (%s): %+v", kind, items)
			}
		})
	}
}

func TestHTTPBoundsCancellationAndSafeErrors(t *testing.T) {
	t.Run("redirect", func(t *testing.T) {
		var leaked atomic.Bool
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
		defer target.Close()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
		defer srv.Close()
		err := client(t, srv.URL).EnsureBank(context.Background(), scope)
		if err == nil || leaked.Load() {
			t.Fatal("followed authenticated redirect")
		}
	})
	t.Run("responsecap", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 1024)) }))
		defer srv.Close()
		if err := client(t, srv.URL, hindsight.Options{MaxResponseBytes: 128}).EnsureBank(context.Background(), scope); err == nil {
			t.Fatal("unbounded response")
		}
	})
	t.Run("requestcap", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			respond(w, map[string]any{"bank_id": hindsight.BankID(scope)})
		}))
		defer srv.Close()
		doc := contextDocument()
		doc.Text = strings.Repeat("x", 1024)
		if err := client(t, srv.URL, hindsight.Options{MaxRequestBytes: 128}).Retain(context.Background(), doc); err == nil || calls.Load() != 0 {
			t.Fatal("oversized input reached service")
		}
	})
	t.Run("cancel", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case <-r.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
		}))
		defer srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := client(t, srv.URL).EnsureBank(ctx, scope)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("caller cancellation not preserved: %v", err)
		}
	})
	t.Run("safeerror", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "fixture-secret retained private content", 500)
		}))
		defer srv.Close()
		err := client(t, srv.URL).EnsureBank(context.Background(), scope)
		if err == nil || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "private") {
			t.Fatal("upstream secret reflected")
		}
	})
}
