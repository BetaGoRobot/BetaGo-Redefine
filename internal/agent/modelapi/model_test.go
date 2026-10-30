package modelapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestArkEmbeddingFanoutPreservesOrderAndDimension(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings/multimodal" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("protocol/auth")
		}
		var b struct {
			Input      []struct{ Type, Text string }
			Dimensions int
		}
		json.NewDecoder(r.Body).Decode(&b)
		if len(b.Input) != 1 || b.Dimensions != 2 {
			t.Error("Ark inputs must be separate calls")
		}
		calls.Add(1)
		if b.Input[0].Text == "first" {
			time.Sleep(10 * time.Millisecond)
			io.WriteString(w, `{"data":{"embedding":[1,0]}}`)
		} else {
			io.WriteString(w, `{"data":{"embedding":[0,1]}}`)
		}
	}))
	defer s.Close()
	e, err := NewEmbedder(EmbeddingConfig{Endpoint: Endpoint{BaseURL: s.URL, APIKey: "secret", Model: "ep", Protocol: "ark_multimodal"}, Dimensions: 2, Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Embed(context.Background(), []string{"first", "second"})
	if err != nil || len(v) != 2 || v[0][0] != 1 || v[1][1] != 1 || calls.Load() != 2 {
		t.Fatalf("order/count %v %v", v, err)
	}
}
func TestStandardEmbeddingRejectsInvalidCountIndexAndDimension(t *testing.T) {
	for _, body := range []string{`{"data":[{"index":0,"embedding":[1,0]}]}`, `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`, `{"data":[{"index":0,"embedding":[1]},{"index":1,"embedding":[0,1]}]}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		e, _ := NewEmbedder(EmbeddingConfig{Endpoint: Endpoint{BaseURL: s.URL, Model: "m", Protocol: "openai"}, Dimensions: 2})
		if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil {
			t.Errorf("accepted invalid response %s", body)
		}
		s.Close()
	}
}
func TestJSONRerankUsesDerivedAndCanonicalTextAndValidatesIndices(t *testing.T) {
	body := `{"choices":[{"message":{"content":"{\"results\":[{\"index\":1,\"relevance_score\":0.9},{\"index\":0,\"relevance_score\":0.1}]}"}}]}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "image transcript") || !strings.Contains(string(b), "canonical original") {
			t.Error("lost canonical or derived text")
		}
		io.WriteString(w, body)
	}))
	defer s.Close()
	r, _ := NewReranker(Endpoint{BaseURL: s.URL, Model: "rank", Protocol: "json_chat"})
	items := []domain.Evidence{{ID: "a", Text: "canonical original", DerivedText: "image transcript"}, {ID: "b", Text: "other"}}
	out, err := r.Rerank(context.Background(), "query", items)
	if err != nil || out[0].ID != "b" {
		t.Fatalf("rank %v %v", out, err)
	}
	body = `{"choices":[{"message":{"content":"{\"results\":[{\"index\":0,\"relevance_score\":1},{\"index\":0,\"relevance_score\":0}]}"}}]}`
	if _, err = r.Rerank(context.Background(), "query", items); err == nil {
		t.Fatal("duplicate index accepted")
	}
}
func TestHTTPBoundedAndCredentialSafe(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, "secret https://bad?api_key=secret")
	}))
	defer s.Close()
	e, _ := NewEmbedder(EmbeddingConfig{Endpoint: Endpoint{BaseURL: s.URL, APIKey: "secret", Model: "m"}, Dimensions: 2})
	_, err := e.Embed(context.Background(), []string{"a"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = e.Embed(ctx, []string{"a"}); err == nil {
		t.Fatal("cancellation ignored")
	}
}
func TestGatewayAuthenticationLimitsAndBase64(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"index":0,"embedding":[1,0]}]}`)
	}))
	defer upstream.Close()
	e, _ := NewEmbedder(EmbeddingConfig{Endpoint: Endpoint{BaseURL: upstream.URL, Model: "m"}, Dimensions: 2})
	h, err := NewGateway(GatewayConfig{APIKey: "gateway-secret", EmbeddingModel: "alias", RerankModel: "rank", Dimensions: 2, MaxBodyBytes: 256}, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(body, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(body))
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := invoke(`{"model":"alias","input":"a"}`, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := invoke(strings.Repeat("a", 300), "Bearer gateway-secret"); w.Code != 413 {
		t.Fatal(w.Code)
	}
	w := invoke(`{"model":"alias","input":["a"],"dimensions":2,"encoding_format":"base64"}`, "Bearer gateway-secret")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var b struct{ Data []struct{ Embedding string } }
	json.Unmarshal(w.Body.Bytes(), &b)
	raw, err := base64.StdEncoding.DecodeString(b.Data[0].Embedding)
	if err != nil || len(raw) != 8 {
		t.Fatal("base64 compatibility")
	}
}
func TestCredentialNotForwardedAcrossRedirectEvenWithInjectedClient(t *testing.T) {
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
		io.WriteString(w, `{"data":[{"index":0,"embedding":[1,0]}]}`)
	}))
	defer other.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", other.URL)
		w.WriteHeader(307)
	}))
	defer s.Close()
	e, _ := NewEmbedder(EmbeddingConfig{Endpoint: Endpoint{BaseURL: s.URL, APIKey: "secret", Model: "m", HTTPClient: s.Client()}, Dimensions: 2})
	if _, err := e.Embed(context.Background(), []string{"a"}); err == nil || leaked {
		t.Fatalf("redirect accepted or leaked credentials %v %v", err, leaked)
	}
}
func TestRerankRejectsMissingIndexOrScore(t *testing.T) {
	for _, body := range []string{`{"results":[{"relevance_score":1}]}`, `{"results":[{"index":0}]}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		r, _ := NewReranker(Endpoint{BaseURL: s.URL, Model: "m"})
		if _, err := r.Rerank(context.Background(), "q", []domain.Evidence{{ID: "a", Text: "t"}}); err == nil {
			t.Error("missing index or score accepted")
		}
		s.Close()
	}
}
func TestChatToolCallsRoundTripAndMultimodalProtocol(t *testing.T) {
	var seen int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		var b map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&b)
		if seen == 1 {
			if !strings.Contains(string(b["messages"]), "tool_call_id") || !strings.Contains(string(b["tools"]), "parameters") || !strings.Contains(string(b["thinking"]), "disabled") {
				t.Error("lost typed tool protocol")
			}
			io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"search","arguments":"{}"}}]}}]}`)
		} else {
			if !strings.Contains(string(b["messages"]), "input_audio") || !strings.Contains(string(b["messages"]), "wav") {
				t.Error("audio protocol")
			}
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"transcribed"}}]}`)
		}
	}))
	defer s.Close()
	c, _ := NewChat(Endpoint{BaseURL: s.URL, Model: "m"})
	out, err := c.Generate(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "tool", ToolCallID: "original", Content: "result"}}, Tools: []ToolDefinition{{Type: "function", Function: FunctionDefinition{Name: "search", Parameters: json.RawMessage(`{"type":"object"}`)}}}})
	if err != nil || len(out.Message.ToolCalls) != 1 || out.Message.ToolCalls[0].Function.Name != "search" {
		t.Fatalf("tools %v %v", out, err)
	}
	if text, err := c.Transcribe(context.Background(), []byte("synthetic wav")); err != nil || text != "transcribed" {
		t.Fatal(err)
	}
}
func TestEmbeddingConcurrencyBoundAcrossBatches(t *testing.T) {
	var active, maximum atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, `{"data":{"embedding":[1,0]}}`)
	}))
	defer s.Close()
	e, _ := NewEmbedder(EmbeddingConfig{Endpoint: Endpoint{BaseURL: s.URL, Model: "m", Protocol: "ark_multimodal"}, Dimensions: 2, Concurrency: 2})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := e.Embed(context.Background(), []string{"a", "b"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if maximum.Load() > 2 {
		t.Fatalf("global concurrency %d exceeds 2", maximum.Load())
	}
}

type heldEmbedding struct{ started, release chan struct{} }

func (h heldEmbedding) Embed(ctx context.Context, _ []string) ([][]float32, error) {
	close(h.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.release:
		return [][]float32{{1, 0}}, nil
	}
}
func TestGatewayBoundsActiveRequests(t *testing.T) {
	e := heldEmbedding{make(chan struct{}), make(chan struct{})}
	h, err := NewGateway(GatewayConfig{APIKey: "key", EmbeddingModel: "embed", RerankModel: "rank", Dimensions: 2, MaxConcurrent: 1}, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	invoke := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(`{"model":"embed","input":"a"}`))
		r.Header.Set("Authorization", "Bearer key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- invoke() }()
	<-e.started
	w := invoke()
	close(e.release)
	first := <-done
	if w.Code != 429 || first.Code != 200 {
		t.Fatalf("active request budget %d %d", w.Code, first.Code)
	}
}

type responseTransport struct {
	body   io.ReadCloser
	length int64
}

func (r responseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: r.length, Body: r.body}, nil
}

type endlessBody struct{ n int }

func (b *endlessBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.n += len(p)
	return len(p), nil
}
func (b *endlessBody) Close() error { return nil }
func TestModelResponseLimitBeforeFullConsumption(t *testing.T) {
	for _, length := range []int64{1000, -1} {
		body := &endlessBody{}
		e, _ := NewEmbedder(EmbeddingConfig{Endpoint: Endpoint{BaseURL: "https://invalid.example", Model: "m", MaxResponseBytes: 64, HTTPClient: &http.Client{Transport: responseTransport{body, length}}}, Dimensions: 2})
		if _, err := e.Embed(context.Background(), []string{"a"}); err == nil {
			t.Fatal("unbounded body accepted")
		}
		if body.n > 65 || length > 64 && body.n != 0 {
			t.Fatalf("read %d bytes for length %d", body.n, length)
		}
	}
}
func TestStandardEmbeddingRequiresExplicitIndex(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"data":[{"embedding":[1,0]}]}`) }))
	defer s.Close()
	e, _ := NewEmbedder(EmbeddingConfig{Endpoint: Endpoint{BaseURL: s.URL, Model: "m"}, Dimensions: 2})
	if _, err := e.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("missing index accepted")
	}
}
