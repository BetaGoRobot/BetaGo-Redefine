package meili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func options(endpoint string) Options {
	return Options{Endpoint: endpoint, APIKey: "private-secret", MessagesIndex: "test_messages", ContextsIndex: "test_contexts", Timeout: time.Second, PollInterval: time.Millisecond, MaxResponseBytes: 1 << 20, VectorDimensions: 3, EmbeddingModel: "provider/model@v1"}
}

func newClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	c, err := New(options(endpoint))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func scope() domain.Scope {
	return domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}
}
func message() domain.Message {
	at := time.Date(2026, 10, 22, 10, 0, 0, 123456789, time.UTC)
	return domain.Message{Source: domain.SourceRef{Scope: scope(), MessageID: "msg/1", AuthorID: "author", Version: 3, OccurredAt: at}, EventID: "evt", Text: "方案已经通过 预算 审批", UpdatedAt: at.Add(time.Nanosecond), Raw: json.RawMessage(`{"secret":"never-index"}`)}
}
func query() domain.Query {
	return domain.Query{Scope: scope(), Anchor: message().UpdatedAt.Add(time.Hour), Text: `"方案已经通过" 预算`, Mode: domain.QueryLiteral, Limit: 10}
}
func derived() domain.Context {
	m := message()
	return domain.Context{ID: "ctx/1", Scope: scope(), Version: 4, Text: "预算审批讨论", Sources: []domain.SourceRef{m.Source}, CreatedAt: m.UpdatedAt, UpdatedAt: m.UpdatedAt.Add(time.Second)}
}
func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	defer r.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
	}
	return body
}
func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func TestOptionsValidation(t *testing.T) {
	for _, mutate := range []func(*Options){
		func(o *Options) { o.Endpoint = "file:///tmp/x" }, func(o *Options) { o.Endpoint = "https://user:secret@example.test" },
		func(o *Options) { o.APIKey = "" }, func(o *Options) { o.MessagesIndex = "../bad" }, func(o *Options) { o.ContextsIndex = o.MessagesIndex },
		func(o *Options) { o.VectorDimensions = -1 }, func(o *Options) { o.EmbeddingModel = "" }, func(o *Options) { o.Timeout = -1 },
		func(o *Options) { o.MaxResponseBytes = -1 }, func(o *Options) { o.PollInterval = -1 },
		func(o *Options) { o.MaxRequestBytes = -1 }, func(o *Options) { o.MaxRequestBytes = (1 << 30) + 1 },
	} {
		o := options("http://example.test")
		mutate(&o)
		if _, err := New(o); err == nil {
			t.Fatalf("accepted invalid options: %#v", o)
		}
	}
}

func TestInitializeWaitsForSettingsAndIsIdempotent(t *testing.T) {
	created := map[string]bool{}
	tasks := map[int]string{}
	polls := map[int]int{}
	settings := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-secret" {
			t.Error("missing authentication")
		}
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/tasks/"):
			var id int
			_, _ = fmt.Sscanf(r.URL.Path, "/tasks/%d", &id)
			polls[id]++
			status := "processing"
			if polls[id] > 1 {
				status = "succeeded"
			}
			writeJSON(w, map[string]any{"uid": id, "status": status})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/indexes/"):
			idx := strings.TrimPrefix(r.URL.Path, "/indexes/")
			if !created[idx] {
				w.WriteHeader(404)
				writeJSON(w, map[string]any{"code": "index_not_found"})
				return
			}
			writeJSON(w, map[string]any{"uid": idx, "primaryKey": "id"})
		case r.Method == "POST" && r.URL.Path == "/indexes":
			body := decodeBody(t, r)
			if body["primaryKey"] != "id" {
				t.Error(body)
			}
			idx := body["uid"].(string)
			if created[idx] {
				t.Error("duplicate creation")
			}
			created[idx] = true
			id := len(tasks)
			tasks[id] = "create"
			writeJSON(w, map[string]any{"taskUid": id})
		case r.Method == "PATCH" && strings.HasSuffix(r.URL.Path, "/settings"):
			idx := strings.Split(r.URL.Path, "/")[2]
			settings[idx] = decodeBody(t, r)
			id := len(tasks)
			tasks[id] = "settings"
			writeJSON(w, map[string]any{"taskUid": id})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 4 {
		t.Fatalf("tasks = %d", len(tasks))
	}
	for id := range tasks {
		if polls[id] < 2 {
			t.Fatalf("did not await task %d", id)
		}
	}
	ms := settings["test_messages"]
	if !reflect.DeepEqual(ms["stopWords"], []any{}) {
		t.Fatal("literal analysis requires explicitly empty stop words")
	}
	if !reflect.DeepEqual(ms["searchableAttributes"], []any{"text"}) || ms["prefixSearch"] != "disabled" || ms["typoTolerance"].(map[string]any)["enabled"] != false {
		t.Fatalf("literal settings: %#v", ms)
	}
	for _, field := range []string{"tenant_id", "app_id", "chat_id", "visibility", "scope_hash", "author_id", "author_hash", "occurred_sec", "occurred_nano", "updated_sec", "updated_nano"} {
		if !contains(ms["filterableAttributes"].([]any), field) {
			t.Errorf("not filterable: %s", field)
		}
	}
	cs := settings["test_contexts"]
	e := cs["embedders"].(map[string]any)["external"].(map[string]any)
	if e["source"] != "userProvided" || e["dimensions"] != float64(3) {
		t.Fatalf("embedder: %#v", e)
	}
	if !contains(cs["filterableAttributes"].([]any), "embedding_model") {
		t.Fatal("model identity not filterable")
	}
	for _, field := range []string{"scope_hash", "author_hashes"} {
		if !contains(cs["filterableAttributes"].([]any), field) {
			t.Errorf("context exact identity not filterable: %s", field)
		}
	}
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("repeat initialization: %v", err)
	}
	if len(created) != 2 {
		t.Fatal(created)
	}
}
func contains(v []any, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}

func TestAsyncTaskFailuresAndCancellation(t *testing.T) {
	for _, status := range []string{"failed", "canceled", "unknown", "processing"} {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/tasks/") {
					writeJSON(w, map[string]any{"uid": 1, "status": status, "error": map[string]string{"message": "private-secret body http://credentials"}})
				} else {
					writeJSON(w, map[string]any{"taskUid": 1})
				}
			}))
			defer srv.Close()
			o := options(srv.URL)
			o.Timeout = 25 * time.Millisecond
			c, err := New(o)
			if err != nil {
				t.Fatal(err)
			}
			err = c.UpsertMessage(context.Background(), message())
			if err == nil {
				t.Fatal("task was not successful")
			}
			for _, secret := range []string{srv.URL, "private-secret", "credentials"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("unsafe error", err)
				}
			}
			if status == "processing" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline: %v", err)
			}
		})
	}
	c := newClient(t, "http://example.test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.DeleteMessage(ctx, scope(), "id"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type endlessBody struct{ read int64 }

func (b *endlessBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.read += int64(len(p))
	return len(p), nil
}
func (*endlessBody) Close() error { return nil }

func TestResponseCapAndRedactedTransportErrors(t *testing.T) {
	b := &endlessBody{}
	o := options("http://example.test")
	o.MaxResponseBytes = 32
	o.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: b, Header: make(http.Header)}, nil
	})}
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Literal(context.Background(), query()); err == nil {
		t.Fatal("unbounded response accepted")
	}
	if b.read > 33 {
		t.Fatalf("read %d bytes before enforcing cap", b.read)
	}
	o.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("http://private-secret.example.test/request?credential=private-secret")
	})}
	c, _ = New(o)
	_, err = c.Literal(context.Background(), query())
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("transport error: %v", err)
	}
}

func TestLiteralRequestsAndProvenance(t *testing.T) {
	m := message()
	var indexed map[string]any
	var gotFilter string
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch {
		case strings.HasPrefix(r.URL.Path, "/tasks/"):
			writeJSON(w, map[string]any{"status": "succeeded"})
		case r.Method == "POST" && r.URL.Path == "/indexes/test_messages/documents":
			var docs []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&docs); err != nil {
				t.Error(err)
			}
			indexed = docs[0]
			writeJSON(w, map[string]any{"taskUid": 1})
		case r.URL.Path == "/indexes/test_messages/search":
			body := decodeBody(t, r)
			if body["q"] != query().Text || body["matchingStrategy"] != "all" {
				t.Errorf("literal request: %#v", body)
			}
			if _, ok := body["vector"]; ok {
				t.Error("literal contains vector")
			}
			if _, ok := body["hybrid"]; ok {
				t.Error("literal contains hybrid")
			}
			gotFilter = body["filter"].(string)
			writeJSON(w, map[string]any{"hits": []any{indexed}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	if err := c.UpsertMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(indexed)
	if strings.Contains(string(encoded), "never-index") || strings.Contains(string(encoded), "Raw") {
		t.Fatal("raw payload indexed")
	}
	q := query()
	q.AuthorID = `a\" OR chat_id = "other"`
	hits, err := c.Literal(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately returned author mismatch must be rejected after decoding as well.
	if len(hits) != 0 {
		t.Fatal("untrusted author mismatch accepted")
	}
	quoted, _ := json.Marshal(q.AuthorID)
	if !strings.Contains(gotFilter, "author_id = "+string(quoted)) {
		t.Fatalf("unescaped filter: %s", gotFilter)
	}
	for _, part := range []string{`tenant_id = "tenant"`, `app_id = "app"`, `chat_id = "chat"`, `visibility = "group"`, "occurred_sec", "occurred_nano", "updated_sec", "updated_nano"} {
		if !strings.Contains(gotFilter, part) {
			t.Errorf("missing filter %s in %s", part, gotFilter)
		}
	}
	hits, err = c.Literal(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Text != m.Text || !reflect.DeepEqual(hits[0].Sources, []domain.SourceRef{m.Source}) || hits[0].Channel != "literal" {
		t.Fatalf("provenance %#v %v", hits, err)
	}
	if calls.Load() != 4 {
		t.Fatalf("unexpected model/API calls: %d", calls.Load())
	}
}

func TestDeleteIDsAreScopedAndTombstonesAllowUnknownAuthor(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			paths = append(paths, r.URL.Path)
			writeJSON(w, map[string]any{"taskUid": 1})
		} else {
			writeJSON(w, map[string]any{"status": "succeeded"})
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	m := message()
	m.Deleted = true
	m.Source.AuthorID = ""
	if err := c.UpsertMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*domain.Scope){func(s *domain.Scope) { s.TenantID = "other" }, func(s *domain.Scope) { s.AppID = "other" }, func(s *domain.Scope) { s.ChatID = "other" }, func(s *domain.Scope) { s.Visibility = "other" }} {
		s := scope()
		change(&s)
		if err := c.DeleteMessage(context.Background(), s, m.Source.MessageID); err != nil {
			t.Fatal(err)
		}
	}
	unique := map[string]bool{}
	for _, p := range paths {
		unique[p] = true
	}
	if len(paths) != 5 || len(unique) != 5 || strings.Contains(paths[0], "msg/1") {
		t.Fatalf("unsafe IDs: %#v", paths)
	}
}

func TestContextVectorValidationAndSearchModes(t *testing.T) {
	var indexed map[string]any
	var searches []map[string]any
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch {
		case strings.HasPrefix(r.URL.Path, "/tasks/"):
			writeJSON(w, map[string]any{"status": "succeeded"})
		case strings.HasSuffix(r.URL.Path, "/documents"):
			var docs []map[string]any
			_ = json.NewDecoder(r.Body).Decode(&docs)
			indexed = docs[0]
			writeJSON(w, map[string]any{"taskUid": 1})
		case strings.HasSuffix(r.URL.Path, "/search"):
			body := decodeBody(t, r)
			searches = append(searches, body)
			indexed["_rankingScore"] = 0.75
			writeJSON(w, map[string]any{"hits": []any{indexed}})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	for _, v := range [][]float32{nil, {1, 2}, {1, 2, float32(math.NaN())}, {1, 2, float32(math.Inf(1))}} {
		if err := c.UpsertContext(context.Background(), derived(), v); err == nil {
			t.Fatal("invalid vector accepted")
		}
		if _, err := c.Vector(context.Background(), query(), v); err == nil {
			t.Fatal("invalid search vector accepted")
		}
	}
	if requests != 0 {
		t.Fatalf("invalid vectors sent %d requests", requests)
	}
	if err := c.UpsertContext(context.Background(), derived(), []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if indexed["version"] != "4" || indexed["embedding_model"] == "" {
		t.Fatalf("context metadata %#v", indexed)
	}
	q := query()
	q.Mode = domain.QueryDiscussion
	kw, err := c.Keyword(context.Background(), q)
	if err != nil || len(kw) != 1 || kw[0].Channel != "keyword" || kw[0].Score != .75 {
		t.Fatalf("keyword %#v %v", kw, err)
	}
	vec, err := c.Vector(context.Background(), q, []float32{1, 0, 0})
	if err != nil || len(vec) != 1 || vec[0].Channel != "vector" {
		t.Fatalf("vector %#v %v", vec, err)
	}
	if _, ok := searches[0]["hybrid"]; ok {
		t.Fatal("keyword must be independent")
	}
	if _, ok := searches[0]["vector"]; ok {
		t.Fatal("keyword must be independent")
	}
	if searches[1]["q"] != "" || searches[1]["hybrid"].(map[string]any)["semanticRatio"] != float64(1) || searches[1]["hybrid"].(map[string]any)["embedder"] != "external" {
		t.Fatalf("pure vector request %#v", searches[1])
	}
	if !strings.Contains(searches[0]["filter"].(string), "embedding_model = ") || !strings.Contains(searches[1]["filter"].(string), "embedding_model = ") {
		t.Fatal("model fingerprint not constrained")
	}
	if !reflect.DeepEqual(vec[0].Sources, derived().Sources) {
		t.Fatal("sources lost")
	}
}

func TestInvalidAndForeignHitsAreDiscarded(t *testing.T) {
	var documents []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/tasks/"):
			writeJSON(w, map[string]any{"status": "succeeded"})
		case strings.HasSuffix(r.URL.Path, "/documents"):
			if err := json.NewDecoder(r.Body).Decode(&documents); err != nil {
				t.Error(err)
			}
			writeJSON(w, map[string]any{"taskUid": 1})
		default:
			original, _ := json.Marshal(documents[0])
			var hits []any
			for _, mutate := range []func(map[string]any){func(d map[string]any) { d["chat_id"] = "foreign" }, func(d map[string]any) { d["id"] = "corrupt" }, func(d map[string]any) { delete(d, "source") }, func(d map[string]any) { d["updated_sec"] = float64(query().Anchor.Unix() + 1) }, func(d map[string]any) { d["text"] = 25 }, func(d map[string]any) { d["_rankingScore"] = "bad" }} {
				var d map[string]any
				_ = json.Unmarshal(original, &d)
				mutate(d)
				hits = append(hits, d)
			}
			writeJSON(w, map[string]any{"hits": hits})
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	if err := c.UpsertMessage(context.Background(), message()); err != nil {
		t.Fatal(err)
	}
	hits, err := c.Literal(context.Background(), query())
	if err != nil || len(hits) != 0 {
		t.Fatalf("unsafe hits %#v %v", hits, err)
	}
}

func TestErrorsDoNotRetainUntrustedResponseCodes(t *testing.T) {
	for _, task := range []bool{false, true} {
		t.Run(fmt.Sprint(task), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !task {
					w.WriteHeader(http.StatusBadRequest)
					writeJSON(w, map[string]string{"code": "private-secret-response-code"})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/tasks/") {
					writeJSON(w, map[string]any{"status": "failed", "error": map[string]string{"code": "private-secret-task-code"}})
				} else {
					writeJSON(w, map[string]int{"taskUid": 1})
				}
			}))
			defer srv.Close()
			err := newClient(t, srv.URL).UpsertMessage(context.Background(), message())
			if err == nil || strings.Contains(fmt.Sprintf("%#v", err), "private-secret") {
				t.Fatalf("response data retained inside error: %#v", err)
			}
		})
	}
}

func TestInitializeRecoversOnlyVerifiedConcurrentIndexCreation(t *testing.T) {
	for _, primaryKey := range []string{"id", "wrong"} {
		t.Run(primaryKey, func(t *testing.T) {
			var gets int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/indexes/"):
					gets++
					if gets%2 == 1 {
						w.WriteHeader(404)
						writeJSON(w, map[string]string{"code": "index_not_found"})
					} else {
						writeJSON(w, map[string]string{"primaryKey": primaryKey})
					}
				case r.Method == "POST" && r.URL.Path == "/indexes":
					writeJSON(w, map[string]int{"taskUid": 1})
				case strings.HasPrefix(r.URL.Path, "/tasks/"):
					if r.URL.Path == "/tasks/1" {
						writeJSON(w, map[string]any{"status": "failed", "error": map[string]string{"code": "index_already_exists"}})
					} else {
						writeJSON(w, map[string]string{"status": "succeeded"})
					}
				case r.Method == "PATCH":
					writeJSON(w, map[string]int{"taskUid": 2})
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
			}))
			defer srv.Close()
			err := newClient(t, srv.URL).Initialize(context.Background())
			if (err == nil) != (primaryKey == "id") {
				t.Fatalf("primary key %q: %v", primaryKey, err)
			}
		})
	}
}

func TestRedirectsDoNotForwardCredentials(t *testing.T) {
	var reached atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		writeJSON(w, map[string]any{"hits": []any{}})
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if _, err := newClient(t, srv.URL).Literal(context.Background(), query()); err == nil {
		t.Fatal("redirect accepted")
	}
	if reached.Load() != 0 {
		t.Fatal("credentials could have reached redirected endpoint")
	}
}

func TestContextCandidatesRequireCompleteCurrentMetadata(t *testing.T) {
	var indexed map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/tasks/"):
			writeJSON(w, map[string]string{"status": "succeeded"})
		case strings.HasSuffix(r.URL.Path, "/documents"):
			var docs []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&docs); err != nil {
				t.Error(err)
			}
			indexed = docs[0]
			writeJSON(w, map[string]int{"taskUid": 1})
		case strings.HasSuffix(r.URL.Path, "/search"):
			original, _ := json.Marshal(indexed)
			var hits []any
			for _, mutate := range []func(map[string]any){
				func(d map[string]any) { delete(d, "version") }, func(d map[string]any) { d["version"] = "0" }, func(d map[string]any) { delete(d, "sources") },
				func(d map[string]any) { delete(d, "embedding_model") }, func(d map[string]any) { d["embedding_model"] = "other-model" },
				func(d map[string]any) { d["invalidated"] = true }, func(d map[string]any) { delete(d, "invalidated") },
				func(d map[string]any) { d["sources"].([]any)[0].(map[string]any)["version"] = "rounded-invalid" },
				func(d map[string]any) {
					d["sources"].([]any)[0].(map[string]any)["scope"].(map[string]any)["tenant_id"] = "foreign"
				},
				func(d map[string]any) {
					d["sources"].([]any)[0].(map[string]any)["occurred_at"] = query().Anchor.Add(time.Nanosecond).Format(time.RFC3339Nano)
				},
			} {
				var d map[string]any
				_ = json.Unmarshal(original, &d)
				mutate(d)
				hits = append(hits, d)
			}
			writeJSON(w, map[string]any{"hits": hits})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	if err := c.UpsertContext(context.Background(), derived(), []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	hits, err := c.Vector(context.Background(), query(), []float32{1, 0, 0})
	if err != nil || len(hits) != 0 {
		t.Fatalf("untrusted contexts %#v %v", hits, err)
	}
}

func TestDefaultRequestCapRejectsOversizedPayloadBeforeHTTP(t *testing.T) {
	privatePayload := "private-payload-do-not-return" + strings.Repeat("x", 8<<20)
	for _, operation := range []string{"message", "context", "literal", "keyword"} {
		t.Run(operation, func(t *testing.T) {
			var calls atomic.Int64
			o := options("http://example.test")
			o.HTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				body := `{"taskUid":1}`
				if strings.HasPrefix(r.URL.Path, "/tasks/") {
					body = `{"status":"succeeded"}`
				} else if strings.HasSuffix(r.URL.Path, "/search") {
					body = `{"hits":[]}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			c, err := New(o)
			if err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "message":
				m := message()
				m.Text = privatePayload
				err = c.UpsertMessage(context.Background(), m)
			case "context":
				d := derived()
				d.Text = privatePayload
				err = c.UpsertContext(context.Background(), d, []float32{1, 0, 0})
			case "literal":
				q := query()
				q.Text = privatePayload
				_, err = c.Literal(context.Background(), q)
			case "keyword":
				q := query()
				q.Text = privatePayload
				_, err = c.Keyword(context.Background(), q)
			}
			if err == nil {
				t.Error("oversized request accepted")
			}
			if calls.Load() != 0 {
				t.Errorf("oversized request made %d outbound HTTP calls", calls.Load())
			}
			if strings.Contains(fmt.Sprintf("%#v", err), "private-payload") {
				t.Fatalf("request payload retained inside error: %#v", err)
			}
		})
	}
}

func TestRequestCapIncludesJSONEscapingAndAllowsExactBoundary(t *testing.T) {
	body := map[string]any{"text": strings.Repeat(`<private-payload>"`, 32)}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{int64(len(encoded)) - 1, int64(len(encoded))} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var calls atomic.Int64
			o := options("http://example.test")
			o.MaxRequestBytes = limit
			o.HTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				got, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if !reflect.DeepEqual(got, encoded) {
					t.Error("encoded request changed")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			})}
			c, err := New(o)
			if err != nil {
				t.Fatal(err)
			}
			err = c.request(context.Background(), http.MethodPost, "/request", body, nil)
			if limit < int64(len(encoded)) {
				if !errors.Is(err, domain.ErrInvalid) || calls.Load() != 0 {
					t.Fatalf("escaped request cap: calls=%d err=%v", calls.Load(), err)
				}
			} else if err != nil || calls.Load() != 1 {
				t.Fatalf("exact boundary: calls=%d err=%v", calls.Load(), err)
			}
			if err != nil && strings.Contains(fmt.Sprintf("%#v", err), "private-payload") {
				t.Fatal("error contains request payload")
			}
		})
	}
}

func TestRequestPreflightIncludesAnonymousDTOFieldsBeforeEncoding(t *testing.T) {
	m := message()
	d := derived()
	oversized := strings.Repeat("x", 4097)
	for _, body := range []any{
		[]messageDTO{{metadata: meta(m.Source.Scope, m.Source.MessageID, oversized, m.Source.OccurredAt, m.UpdatedAt), MessageID: m.Source.MessageID, AuthorID: m.Source.AuthorID, Source: source(m.Source)}},
		[]contextDTO{{metadata: meta(d.Scope, d.ID, oversized, d.Sources[0].OccurredAt, d.UpdatedAt), ContextID: d.ID, Version: "1", Sources: []sourceDTO{source(d.Sources[0])}, CreatedAt: d.CreatedAt}},
	} {
		t.Run(fmt.Sprintf("%T", body), func(t *testing.T) {
			budget := int64(4096)
			if requestInputFits(reflect.ValueOf(body), &budget) {
				t.Fatal("oversized embedded Text reached JSON encoder instead of preflight rejection")
			}
		})
	}
}

var _ io.Reader = (*endlessBody)(nil)
