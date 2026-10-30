package ark_dal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	appotel "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type embeddingTestTransport func(*http.Request) (*http.Response, error)

func (f embeddingTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func embeddingObservationFixture(t *testing.T, status int, body string) (*observer.ObservedLogs, *tracetest.SpanRecorder, *arkUsageStore) {
	t.Helper()
	httpClient := &http.Client{Transport: embeddingTestTransport(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	client := arkruntime.NewClientWithApiKey("test-only", arkruntime.WithBaseUrl("http://fixture.invalid"), arkruntime.WithHTTPClient(httpClient), arkruntime.WithRetryTimes(0))
	oldRuntime, oldLogger, oldTracer, oldRecorder := embeddingRuntimeClientFn, embeddingLoggerFn, appotel.OtelTracer, llmusage.DefaultRecorder()
	core, observed := observer.New(zap.DebugLevel)
	embeddingLoggerFn = func() *logs.ContextualLogger { return logs.NewContextualLogger(zap.New(core), zap.NewNop()) }
	embeddingRuntimeClientFn = func() (*arkruntime.Client, *config.ArkConfig, error) {
		return client, &config.ArkConfig{EmbeddingModel: "fixture-embedding"}, nil
	}
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	appotel.OtelTracer = provider.Tracer("embedding-test")
	store := &arkUsageStore{}
	llmusage.SetDefaultRecorder(llmusage.NewRecorderWithStore(store))
	t.Cleanup(func() {
		embeddingRuntimeClientFn = oldRuntime
		embeddingLoggerFn = oldLogger
		appotel.OtelTracer = oldTracer
		llmusage.SetDefaultRecorder(oldRecorder)
		_ = provider.Shutdown(context.Background())
	})
	return observed, spans, store
}

func TestEmbeddingTextPrivateOmitsPayloadFromLogsSpansAndUsageErrors(t *testing.T) {
	const marker = "PRIVATE_EMBEDDING_QUERY_MARKER"
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "InvalidParameter", "message": "provider echoed " + marker}})
	observed, spans, store := embeddingObservationFixture(t, 400, string(body))
	_, _, err := EmbeddingTextPrivate(context.Background(), marker, llmusage.Scope{Source: "meme_reply_embedding"})
	if err == nil {
		t.Fatal("provider failure missing")
	}
	if len(observed.All()) != 1 || len(spans.Ended()) != 1 || len(store.rows) != 1 {
		t.Fatal("observability or usage disappeared")
	}
	row := store.rows[0]
	if row.Model != "fixture-embedding" || row.Status != string(llmusage.StatusError) || !strings.Contains(row.Error, "400") || !strings.Contains(row.Error, "InvalidParameter") {
		t.Fatalf("safe model/status/code missing: model=%s status=%s diagnostic=%s", row.Model, row.Status, row.Error)
	}
	serializedLogs, _ := json.Marshal(observed.All())
	span := spans.Ended()[0]
	for _, data := range []string{string(serializedLogs), fmt.Sprint(span.Attributes(), span.Events(), span.Status()), row.Error} {
		if strings.Contains(data, marker) {
			t.Fatal("private query leaked through observability")
		}
	}
	for _, attr := range span.Attributes() {
		if strings.HasPrefix(string(attr.Key), "input.") {
			t.Fatal("private path recorded input preview metadata")
		}
	}
}

func TestEmbeddingTextLegacyKeepsExistingPayloadObservability(t *testing.T) {
	const marker = "LEGACY_EMBEDDING_MARKER"
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "InvalidParameter", "message": "provider echoed " + marker}})
	observed, spans, store := embeddingObservationFixture(t, 400, string(body))
	if _, _, err := EmbeddingText(context.Background(), marker, llmusage.Scope{}); err == nil {
		t.Fatal("provider failure missing")
	}
	serializedLogs, _ := json.Marshal(observed.All())
	span := spans.Ended()[0]
	for _, data := range []string{string(serializedLogs), fmt.Sprint(span.Attributes()), store.rows[0].Error} {
		if !strings.Contains(data, marker) {
			t.Fatal("legacy observability behavior changed")
		}
	}
}

func TestEmbeddingTextPrivateKeepsVectorsModelAndActualUsage(t *testing.T) {
	observed, spans, store := embeddingObservationFixture(t, 200, `{"data":{"embedding":[0.1,0.2]},"usage":{"prompt_tokens":3,"total_tokens":3}}`)
	vector, usage, err := EmbeddingTextPrivate(context.Background(), "success query", llmusage.Scope{Source: "meme_reply_embedding"})
	if err != nil || len(vector) != 2 || usage.PromptTokens != 3 || usage.TotalTokens != 3 {
		t.Fatal("private path changed vector/usage")
	}
	if len(observed.All()) != 0 || len(spans.Ended()) != 1 || len(store.rows) != 1 || store.rows[0].Model != "fixture-embedding" || store.rows[0].PromptTokens != 3 {
		t.Fatal("private success lost metadata")
	}
}
