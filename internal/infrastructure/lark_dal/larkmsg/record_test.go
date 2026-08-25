package larkmsg

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"go.opentelemetry.io/otel/trace"
)

func TestStartRecordingSpanDetachesCallerCancellation(t *testing.T) {
	type contextKey string
	const key contextKey = "recording-value"

	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{17, 18, 19, 20, 21, 22, 23, 24}
	parentSpanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
	parent := trace.ContextWithSpanContext(
		context.WithValue(context.Background(), key, "preserved"),
		parentSpanContext,
	)
	parent, cancel := context.WithCancel(parent)
	cancel()

	ctx, span := startRecordingSpan(parent)
	defer span.End()
	if err := ctx.Err(); err != nil {
		t.Fatalf("recording context inherited caller cancellation: %v", err)
	}
	if got := ctx.Value(key); got != "preserved" {
		t.Fatalf("recording context value = %v, want preserved", got)
	}
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("recording context inherited caller deadline")
	}
	if got := trace.SpanContextFromContext(ctx).TraceID(); got != traceID {
		t.Fatalf("recording trace ID = %s, want %s", got, traceID)
	}
}

func useLarkMsgConfigPath(t *testing.T) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.toml")
	data := []byte("[lark_config]\nbot_open_id = \"ou_configured_bot\"\n")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
	infraConfig.LoadFile(configPath)
}

func TestResolveRecordedBotIdentityPreservesSenderOpenID(t *testing.T) {
	useLarkMsgConfigPath(t)

	openID, userName := resolveRecordedBotIdentity("ou_custom_bot")
	if openID != "ou_custom_bot" {
		t.Fatalf("openID = %q, want %q", openID, "ou_custom_bot")
	}
	if userName != "你" {
		t.Fatalf("userName = %q, want %q", userName, "你")
	}
}

func TestResolveRecordedBotIdentityFallsBackToConfiguredBotOpenID(t *testing.T) {
	useLarkMsgConfigPath(t)

	openID, userName := resolveRecordedBotIdentity("")
	want := infraConfig.Get().LarkConfig.BotOpenID
	if openID != want {
		t.Fatalf("openID = %q, want %q", openID, want)
	}
	if userName != "你" {
		t.Fatalf("userName = %q, want %q", userName, "你")
	}
}
