package otel

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type stubSpan struct {
	trace.Span
	recorded []error
	code     codes.Code
	desc     string
}

func (s *stubSpan) RecordError(err error, _ ...trace.EventOption) {
	s.recorded = append(s.recorded, err)
}

func (s *stubSpan) SetStatus(code codes.Code, description string) {
	s.code = code
	s.desc = description
}

func TestRecordErrorSetsErrorStatus(t *testing.T) {
	span := &stubSpan{}
	expected := errors.New("boom")

	RecordError(span, expected)

	if len(span.recorded) != 1 || span.recorded[0] != expected {
		t.Fatalf("expected error to be recorded once")
	}
	if span.code != codes.Error {
		t.Fatalf("expected codes.Error, got %v", span.code)
	}
	if span.desc != expected.Error() {
		t.Fatalf("expected description %q, got %q", expected.Error(), span.desc)
	}
}

func TestRecordErrorPtrHandlesNil(t *testing.T) {
	span := &stubSpan{}

	RecordErrorPtr(span, nil)

	if len(span.recorded) != 0 {
		t.Fatalf("expected no recorded errors")
	}
}

func TestPreviewString(t *testing.T) {
	if got := PreviewString("  short  ", 10); got != "short" {
		t.Fatalf("expected trimmed short value, got %q", got)
	}

	got := PreviewString("abcdefghijklmnopqrstuvwxyz", 8)
	if got != "abcde..." {
		t.Fatalf("expected truncated preview, got %q", got)
	}

	if got := PreviewString("abcdef", 3); got != "abc" {
		t.Fatalf("expected hard truncate for tiny limit, got %q", got)
	}

	if got := PreviewString("abcdef", 0); got != "" {
		t.Fatalf("expected empty preview for non-positive limit, got %q", got)
	}
}

func TestStartEntryCreatesNewRootTrace(t *testing.T) {
	restore := installOTelTestTracer(t)
	defer restore()

	parentCtx, parentSpan := StartNamed(context.Background(), "parent")
	defer parentSpan.End()

	entryCtx, entrySpan := StartEntry(parentCtx, "entry")
	defer entrySpan.End()

	parentTraceID := parentSpan.SpanContext().TraceID()
	entryTraceID := entrySpan.SpanContext().TraceID()
	if !entryTraceID.IsValid() {
		t.Fatalf("expected entry trace ID to be valid")
	}
	if entryTraceID == parentTraceID {
		t.Fatalf("expected entry trace to ignore parent trace %s", parentTraceID)
	}
	if got := trace.SpanContextFromContext(entryCtx).TraceID(); got != entryTraceID {
		t.Fatalf("expected entry context trace ID %s, got %s", entryTraceID, got)
	}
}

func TestDetachSpanPreservesContextState(t *testing.T) {
	restore := installOTelTestTracer(t)
	defer restore()

	type contextKey string
	const key contextKey = "test"

	baseCtx, cancel := context.WithTimeout(context.WithValue(context.Background(), key, "value"), time.Second)
	defer cancel()

	parentCtx, parentSpan := StartNamed(baseCtx, "parent")
	defer parentSpan.End()

	detachedCtx := DetachSpan(parentCtx)
	if got := trace.SpanContextFromContext(detachedCtx); got.IsValid() {
		t.Fatalf("expected detached context to have no active span, got %s", got.TraceID())
	}
	if got := detachedCtx.Value(key); got != "value" {
		t.Fatalf("expected detached context value to be preserved, got %#v", got)
	}

	parentDeadline, parentOK := parentCtx.Deadline()
	detachedDeadline, detachedOK := detachedCtx.Deadline()
	if parentOK != detachedOK || !parentDeadline.Equal(detachedDeadline) {
		t.Fatalf("expected detached context deadline to match parent, got parent=%v/%v detached=%v/%v", parentDeadline, parentOK, detachedDeadline, detachedOK)
	}

	cancel()
	select {
	case <-detachedCtx.Done():
	case <-time.After(time.Second):
		t.Fatalf("expected detached context cancellation to propagate")
	}
}

func installOTelTestTracer(t *testing.T) func() {
	t.Helper()

	prevTracer := OtelTracer
	prevProvider := tracerProvider
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	tracerProvider = tp
	OtelTracer = tp.Tracer("otel-test")

	return func() {
		tracerProvider = prevProvider
		OtelTracer = prevTracer
		_ = tp.Shutdown(context.Background())
	}
}
