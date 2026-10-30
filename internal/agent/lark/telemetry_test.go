package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestIngressReceiptAndPersistenceFailureIncludeBusinessContents(t *testing.T) {
	oldLog, oldProvider := slog.Default(), otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	otel.SetTracerProvider(provider)
	defer func() {
		slog.SetDefault(oldLog)
		otel.SetTracerProvider(oldProvider)
		provider.Shutdown(context.Background())
	}()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	validTrace := false
	ingress, err := NewIngress(IngressConfig{Normalize: NormalizeConfig{AppID: "app", BotOpenID: "bot"}, AppSecret: "credential-canary", Persist: func(ctx context.Context, e domain.Event) error {
		validTrace = trace.SpanContextFromContext(ctx).IsValid()
		return errors.New("credential-canary")
	}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	scope := domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}
	meta, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", MessageType: "text", Addressed: true})
	e := domain.Event{ID: "event-logging", Type: domain.EventMessage, OccurredAt: at, Message: domain.Message{EventID: "event-logging", Source: domain.SourceRef{Scope: scope, MessageID: "message-logging", Version: 1, AuthorID: "human", OccurredAt: at}, UpdatedAt: at, Text: "content-canary", Raw: meta}}
	if ingress.persist(context.Background(), e, nil) == nil {
		t.Fatal("persistence failure disappeared")
	}
	logs := output.String()
	for _, wanted := range []string{"agent.message.received", "message-logging", "\"addressed\":true", "ingress.persist", "persistence_failed", "trace_id"} {
		if !strings.Contains(logs, wanted) {
			t.Fatalf("missing ingress diagnostic %s", wanted)
		}
	}
	if !validTrace || strings.Contains(logs, "credential-canary") || !strings.Contains(logs, "content-canary") {
		t.Fatal("missing trace or unsafe ingress diagnostics")
	}
}

func TestBusinessCodeOnSuccessfulHTTPIsObservableWithoutProviderMessage(t *testing.T) {
	old := slog.Default()
	defer slog.SetDefault(old)
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	if communityAPIError(context.Background(), nil, 200, 230002) == nil {
		t.Fatal("provider rejection disappeared")
	}
	logs := output.String()
	for _, wanted := range []string{"lark.api.failed", "230002", "http_status", "provider_rejected"} {
		if !strings.Contains(logs, wanted) {
			t.Fatalf("missing provider diagnostic %s", wanted)
		}
	}
}
