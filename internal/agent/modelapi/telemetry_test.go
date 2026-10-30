package modelapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestModelTelemetryIncludesIdentityPayloadUsageAndProviderRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "provider-request-123")
		io.WriteString(w, `{"id":"response-456","model":"doubao-real-model","choices":[{"message":{"role":"assistant","content":"这是模型的明文回复"},"finish_reason":"stop"}],"usage":{"prompt_tokens":123,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":100}}}`)
	}))
	defer server.Close()
	oldLog, oldTrace := slog.Default(), otel.GetTracerProvider()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	defer func() {
		slog.SetDefault(oldLog)
		otel.SetTracerProvider(oldTrace)
		provider.Shutdown(context.Background())
	}()
	client, err := NewChat(Endpoint{BaseURL: server.URL, Model: "ep-configured-id", Protocol: "ark", APIKey: "credential-canary"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "这是完整的用户输入"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ep-configured-id", "doubao-real-model", "response-456", "provider-request-123", "这是完整的用户输入", "这是模型的明文回复", "cached_tokens"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("logs missing %q", want)
		}
	}
	if strings.Contains(output.String(), "credential-canary") {
		t.Error("API key disclosed")
	}
	found := map[string]bool{}
	for _, span := range recorder.Ended() {
		if span.Name() != "model.request" {
			continue
		}
		for _, a := range span.Attributes() {
			found[string(a.Key)] = true
		}
	}
	for _, key := range []string{"gen_ai.request.model", "gen_ai.response.model", "gen_ai.response.id", "provider.request_id", "gen_ai.usage.input_tokens", "gen_ai.usage.output_tokens"} {
		if !found[key] {
			t.Errorf("trace missing %s", key)
		}
	}
}

func TestRejectedModelKeepsHTTPClassificationForOversizedErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "rejected-request")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, strings.Repeat("provider overloaded ", 50))
	}))
	defer server.Close()
	endpoint, err := normalize(Endpoint{BaseURL: server.URL, Model: "model", MaxResponseBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	var out any
	err = request(context.Background(), endpoint, "/chat/completions", map[string]string{"model": "model"}, &out)
	_, code := telemetry.Classify(err)
	if code != "provider_http_rejected" {
		t.Fatalf("logging changed provider rejection to %s", code)
	}
}
