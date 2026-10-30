package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	api "go.opentelemetry.io/otel/trace"
)

func TestBusinessLogTraceAndAsyncParentPreserveFacts(t *testing.T) {
	export := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(export))
	oldProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(oldProvider); provider.Shutdown(context.Background()) })
	var output bytes.Buffer
	oldLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLog) })
	ctx, span := Start(Context(context.Background(), "message_id", "message-1", "job_id", 1), "agent.ingress")
	original := json.RawMessage(`{"version":9007199254740993,"task_id":"task-1"}`)
	payload := InjectJobPayload(ctx, original)
	if string(original) != `{"version":9007199254740993,"task_id":"task-1"}` || !strings.Contains(string(payload), "9007199254740993") {
		t.Fatal("tracing mutated source facts or rounded a version")
	}
	jobctx, child := Start(ExtractJobPayload(context.Background(), payload), "agent.job.agent_turn")
	if child.SpanContext().TraceID() != span.SpanContext().TraceID() || child.SpanContext().SpanID() == span.SpanContext().SpanID() {
		t.Fatal("durable queue lost causal trace")
	}
	err := Wrap("model.request.validate", "model_context_budget", domain.ErrInvalid)
	err = Wrap("engine.run", "operation_failed", err)
	Error(jobctx, "agent.model.failed", err, "json_request_bytes", 140000, "limit_bytes", 131072, "authorization", "private-secret", "content", "readable-business-message")
	End(jobctx, child, err)
	End(ctx, span, nil)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("diagnostic lost error identity")
	}
	text := output.String()
	for _, wanted := range []string{"trace_id", span.SpanContext().TraceID().String(), "model.request.validate", "model_context_budget", "140000", "message-1"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("missing useful diagnostic %s", wanted)
		}
	}
	if !strings.Contains(text, "readable-business-message") {
		t.Fatal("business content missing")
	}
	for _, secret := range []string{"private-secret"} {
		if strings.Contains(text, secret) {
			t.Fatal("business telemetry disclosed payload/credential")
		}
	}
	spans := export.GetSpans()
	if len(spans) != 2 || spans[0].Parent.SpanID() != span.SpanContext().SpanID() {
		t.Fatal("exported trace lost parent")
	}
}

func TestInvalidQueueMetadataNeverCreatesRemoteParent(t *testing.T) {
	ctx := ExtractJobPayload(context.Background(), json.RawMessage(`{"_otel":{"traceparent":"bad","authorization":"secret"}}`))
	if api.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("invalid remote carrier accepted")
	}
	plain := json.RawMessage(`{"task_id":"t"}`)
	if string(InjectJobPayload(context.Background(), plain)) != string(plain) {
		t.Fatal("inactive tracing changed payload")
	}
}

func TestExpectedDeferralIsNotAFailedOperation(t *testing.T) {
	export := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(export))
	oldProvider, oldLog := otel.GetTracerProvider(), slog.Default()
	otel.SetTracerProvider(provider)
	defer func() {
		otel.SetTracerProvider(oldProvider)
		slog.SetDefault(oldLog)
		provider.Shutdown(context.Background())
	}()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	for _, err := range []error{domain.ErrTaskBusy, domain.ErrDeliveryPending, Wrap("turn.prompt", "media_not_ready", errors.New("pending media"))} {
		ctx, span := Start(context.Background(), "agent.expected_wait")
		End(ctx, span, err)
	}
	if strings.Contains(output.String(), `"level":"ERROR"`) {
		t.Fatal("normal pending state emitted a false business failure")
	}
	for _, span := range export.GetSpans() {
		if span.Status.Code == codes.Error {
			t.Fatal("normal pending state marked trace failed")
		}
	}
}
