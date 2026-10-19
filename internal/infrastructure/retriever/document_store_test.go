package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	opensearchgo "github.com/opensearch-project/opensearch-go"
	"github.com/opensearch-project/opensearch-go/opensearchapi"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/vectorstores"
)

type documentTestEmbedder struct{}

func (documentTestEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = []float32{0.1, 0.2}
	}
	return vectors, nil
}

func (documentTestEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	return []float32{0.1, 0.2}, nil
}

func testDocumentStore(t *testing.T, handler http.HandlerFunc) (*documentStore, *atomic.Int64) {
	t.Helper()
	opened := new(atomic.Int64)
	server := httptest.NewUnstartedServer(handler)
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	client, err := opensearchgo.NewClient(opensearchgo.Config{
		Addresses:    []string{server.URL},
		Transport:    transport,
		DisableRetry: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := newDocumentStore(client, documentTestEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	return store, opened
}

func TestDocumentStoreReusesConnectionsAndPreservesDocumentFormat(t *testing.T) {
	var creates, writes atomic.Int64
	store, opened := testDocumentStore(t, func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			io.WriteString(w, `{"version":{"number":"1.3.0","distribution":"opensearch"}}`)
			return
		}
		if r.URL.Path == "/messages" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			dim := body["mappings"].(map[string]any)["properties"].(map[string]any)["contentVector"].(map[string]any)["dimension"]
			if dim != float64(vectorDimension) {
				t.Errorf("vector dimension = %v, want %d", dim, vectorDimension)
			}
			if creates.Add(1) > 1 {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":{"type":"resource_already_exists_exception"},"status":400}`)
				return
			}
			io.WriteString(w, `{"acknowledged":true}`)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/messages/_doc/") {
			t.Errorf("unexpected write path: %s", r.URL.Path)
		}
		var doc struct {
			Content string         `json:"content"`
			Vector  []float32      `json:"contentVector"`
			Meta    map[string]any `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			t.Error(err)
		}
		if doc.Content != "test document" || len(doc.Vector) != 2 || doc.Vector[0] != 0.1 || doc.Meta["msg_id"] != "message-1" {
			t.Errorf("document format changed: %+v", doc)
		}
		writes.Add(1)
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"result":"created"}`)
	})
	for i := 0; i < 10; i++ {
		if _, err := store.CreateIndex(context.Background(), "messages", mutateIndexMap); err != nil {
			t.Fatal(err)
		}
		ids, err := store.AddDocuments(context.Background(), []schema.Document{{PageContent: "test document", Metadata: map[string]any{"msg_id": "message-1"}}}, vectorstores.WithNameSpace("messages"))
		if err != nil || len(ids) != 1 || ids[0] == "" {
			t.Fatalf("AddDocuments(): ids=%v, err=%v", ids, err)
		}
	}
	if creates.Load() != 10 || writes.Load() != 10 {
		t.Fatalf("requests: creates=%d writes=%d", creates.Load(), writes.Load())
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("10 sequential writes opened %d connections, want 1 reused connection", got)
	}
}

func TestDocumentStoreRejectsHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			store, opened := testDocumentStore(t, func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/" {
					io.WriteString(w, `{"version":{"number":"1.3.0","distribution":"opensearch"}}`)
					return
				}
				w.WriteHeader(status)
				io.WriteString(w, `{"error":{"type":"synthetic_failure"}}`)
			})
			for i := 0; i < 3; i++ {
				if _, err := store.CreateIndex(context.Background(), "messages", mutateIndexMap); err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) {
					t.Fatalf("CreateIndex() error = %v, want HTTP %d", err, status)
				}
				ids, err := store.AddDocuments(context.Background(), []schema.Document{{PageContent: "test"}}, vectorstores.WithNameSpace("messages"))
				if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) || len(ids) != 0 {
					t.Fatalf("rejected write reported ids=%v err=%v, want HTTP %d", ids, err, status)
				}
			}
			if got := opened.Load(); got != 1 {
				t.Fatalf("failed responses leaked connections: opened %d", got)
			}
		})
	}
}

type trackedDocumentBody struct {
	*strings.Reader
	readErr error
	closed  bool
}

func (b *trackedDocumentBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.Reader.Read(p)
}

func (b *trackedDocumentBody) Close() error {
	b.closed = true
	return nil
}

func TestDocumentResponsesAreClosedOnSuccessAndFailure(t *testing.T) {
	requestFailure := errors.New("synthetic transport failure")
	for _, test := range []struct {
		name       string
		status     int
		body       string
		readErr    error
		requestErr error
		wantErr    bool
	}{
		{name: "success", status: 201, body: `{"result":"created"}`},
		{name: "HTTP failure", status: 403, body: `{"error":"forbidden"}`, wantErr: true},
		{name: "read failure", status: 200, readErr: io.ErrUnexpectedEOF, wantErr: true},
		{name: "transport failure with response", status: 503, body: `{"error":"unavailable"}`, requestErr: requestFailure, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedDocumentBody{Reader: strings.NewReader(test.body), readErr: test.readErr}
			err := consumeDocumentResponse(&opensearchapi.Response{StatusCode: test.status, Body: body}, test.requestErr, false)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error=%t", err, test.wantErr)
			}
			if !body.closed {
				t.Fatal("response body was not closed")
			}
			if test.readErr == nil && body.Len() != 0 {
				t.Fatal("response body was not fully consumed")
			}
			if test.readErr != nil && !errors.Is(err, test.readErr) {
				t.Fatalf("read failure was lost: %v", err)
			}
			if test.requestErr != nil && !errors.Is(err, test.requestErr) {
				t.Fatalf("transport failure was lost: %v", err)
			}
		})
	}
}

func TestAlreadyExistingIndexIsNotIgnoredForDocumentWrites(t *testing.T) {
	body := &trackedDocumentBody{Reader: strings.NewReader(`{"error":{"type":"resource_already_exists_exception"},"status":400}`)}
	err := consumeDocumentResponse(&opensearchapi.Response{StatusCode: 400, Body: body}, nil, false)
	if err == nil || !body.closed {
		t.Fatalf("document write error=%v, body closed=%t", err, body.closed)
	}
}
