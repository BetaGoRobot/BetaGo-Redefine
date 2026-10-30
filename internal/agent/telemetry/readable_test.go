package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestBusinessPayloadIsReadableAndNestedCredentialsAreRedacted(t *testing.T) {
	old := slog.Default()
	defer slog.SetDefault(old)
	var output bytes.Buffer
	slog.SetDefault(slog.New(&bridge{stdout: slog.NewJSONHandler(&output, nil)}))
	Info(context.Background(), "agent.tool.result", "tool", "example", "content", `{"text":"北京今天下雨","nested":{"access_token":"credential-canary","password":"password-canary"}}`, "prompt", strings.Repeat("可读上下文", 200))
	got := output.String()
	for _, want := range []string{"北京今天下雨", "可读上下文", `\"text\"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing business content %q", want)
		}
	}
	for _, secret := range []string{"credential-canary", "password-canary", "[oversize]"} {
		if strings.Contains(got, secret) {
			t.Errorf("unexpected %q", secret)
		}
	}
}

func TestBridgeAddsLevelAndNamespaceAliases(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	oldLog, oldTrace, oldMeter := slog.Default(), otel.GetTracerProvider(), otel.GetMeterProvider()
	defer func() { slog.SetDefault(oldLog); otel.SetTracerProvider(oldTrace); otel.SetMeterProvider(oldMeter) }()
	var output bytes.Buffer
	stop, err := initialize(context.Background(), "agent", &output)
	if err != nil {
		t.Fatal(err)
	}
	defer stop(context.Background())
	Warn(context.Background(), "agent.tool.unknown", "tool", "example")
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"level": "WARN", "severity": "warn", "namespace": "betago", "service.namespace": "betago", "service_namespace": "betago", "service_name": "betago-agent-next-agent"} {
		if got[key] != want {
			t.Errorf("%s = %v, want %s", key, got[key], want)
		}
	}
}

func TestBusinessDecisionIsVisibleInTrace(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() { otel.SetTracerProvider(old); provider.Shutdown(context.Background()) }()
	ctx, span := Start(context.Background(), "agent.turn")
	Info(ctx, "agent.turn.decision", "choice", "reply", "bubble_count", 1)
	End(ctx, span, nil)
	s := recorder.Ended()[0]
	found := map[string]bool{}
	for _, a := range s.Attributes() {
		found[string(a.Key)] = true
	}
	for _, key := range []string{"operation.description", "service.namespace", "logs.url", "choice", "operation.outcome"} {
		if !found[key] {
			t.Errorf("trace missing %s", key)
		}
	}
	if len(s.Events()) == 0 {
		t.Error("trace has no readable business events")
	}
}

func TestCredentialsAreRedactedWithoutHidingBusinessText(t *testing.T) {
	for _, in := range []string{
		"Authorization: Basic basic-canary\n业务正文",
		"Cookie: session=cookie-canary; id=123\n业务正文",
		"token=token-canary&query=业务正文",
		`{"luckin_personal_token":"token-canary","text":"业务正文","url":"https://example.test/?access_token=url-canary"}`,
	} {
		got := sanitizeText(in)
		for _, secret := range []string{"basic-canary", "cookie-canary", "token-canary", "url-canary"} {
			if strings.Contains(got, secret) {
				t.Errorf("credential disclosed: %s", secret)
			}
		}
		if !strings.Contains(got, "业务正文") {
			t.Error("business text removed")
		}
		if again := sanitizeText(got); again != got {
			t.Error("sanitation must be idempotent")
		}
	}
}

func TestPayloadChunksReassembleExactSanitizedUTF8(t *testing.T) {
	old := slog.Default()
	defer slog.SetDefault(old)
	var output bytes.Buffer
	slog.SetDefault(slog.New(&bridge{stdout: slog.NewJSONHandler(&output, nil)}))
	input := strings.Repeat("中文🙂password=[redacted]\n", 4000) + `{"token":"credential-canary","text":"结尾"}`
	want := sanitizeText(input)
	Payload(context.Background(), "agent.model.request.content", "request_body", input)
	var reconstructed strings.Builder
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var row struct {
			RequestBody  string `json:"request_body"`
			Bytes        int    `json:"payload_bytes"`
			Index, Count int
			ChunkIndex   int    `json:"chunk_index"`
			ChunkCount   int    `json:"chunk_count"`
			SHA          string `json:"payload_sha256"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		count++
		if row.ChunkIndex != count || row.Bytes != len(want) || row.SHA != fmt.Sprintf("%x", sha256.Sum256([]byte(want))) || !utf8.ValidString(row.RequestBody) {
			t.Fatal("invalid chunk metadata/UTF-8")
		}
		reconstructed.WriteString(row.RequestBody)
	}
	if count < 2 || reconstructed.String() != want {
		t.Fatal("exported payload differs from sanitized source")
	}
}

func TestLogLinkUsesVMUIHashRouterAndExactTraceFilter(t *testing.T) {
	u, err := url.Parse(LogsURL("trace-123", "span-456"))
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.Parse(u.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Query().Get("query") != `trace_id:="trace-123" span_id:="span-456"` {
		t.Fatal("log link lost query in VMUI route")
	}
}
