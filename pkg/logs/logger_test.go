package logs

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestContextualLoggerIncludesTraceAndSpanIDs(t *testing.T) {
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{17, 18, 19, 20, 21, 22, 23, 24}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	var output bytes.Buffer
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	stdout := zap.New(zapcore.NewCore(encoder, zapcore.AddSync(&output), zap.InfoLevel))
	NewContextualLogger(stdout, zap.NewNop()).Ctx(ctx).Error("request failed")

	got := output.String()
	for _, want := range []string{
		`"trace_id":"` + traceID.String() + `"`,
		`"span_id":"` + spanID.String() + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("log output %q does not contain %q", got, want)
		}
	}
}
