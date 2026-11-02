package modelapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNormalizedClientUsesEndpointBudgetWithoutMutatingCaller(t *testing.T) {
	caller := &http.Client{}
	e, err := normalize(Endpoint{BaseURL: "https://example.invalid", Model: "fixture", Timeout: 75 * time.Millisecond, HTTPClient: caller})
	if err != nil {
		t.Fatal(err)
	}
	if e.HTTPClient.Timeout != e.Timeout || caller.Timeout != 0 || caller.Transport != nil {
		t.Fatalf("missing bounded client or mutated caller: client=%v caller=%v", e.HTTPClient.Timeout, caller.Timeout)
	}
}

func TestResponseBodyDeadlineRetainsCancellationClassification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	e, err := normalize(Endpoint{BaseURL: server.URL, Model: "fixture", Timeout: 60 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var output any
	err = request(context.Background(), e, "/chat/completions", map[string]string{"input": "synthetic"}, &output)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body timeout lost deadline classification: %v", err)
	}
}
