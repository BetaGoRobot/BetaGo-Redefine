package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestWithoutPayloadSuppressesMemeBodiesAndPreservesNormalTelemetry(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	ctx := WithoutPayload(context.Background())
	Payload(ctx, "meme.query.body", "body", "private raw dialogue")
	Info(ctx, "meme.query.metadata", "count", 5)
	Payload(context.Background(), "normal.body", "body", "normal diagnostic")
	if strings.Contains(logs.String(), "private raw dialogue") || !strings.Contains(logs.String(), "meme.query.metadata") || !strings.Contains(logs.String(), "normal diagnostic") {
		t.Fatal("payload suppression changed normal diagnostics or exposed meme dialogue")
	}
}
