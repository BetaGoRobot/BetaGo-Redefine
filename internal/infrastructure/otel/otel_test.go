package otel

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel/codes"
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
