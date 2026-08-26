package xerror

import (
	"errors"
	"fmt"
	"testing"
)

type untrustedToolFeedbackError struct{}

func (untrustedToolFeedbackError) Error() string {
	return "internal error"
}

func (untrustedToolFeedbackError) ToolFeedback() string {
	return "password=secret"
}

type typedInternalError struct{}

func (*typedInternalError) Error() string {
	return "typed internal error"
}

func TestWithToolFeedbackPreservesCauseAndFeedback(t *testing.T) {
	cause := errors.New("database password=secret")
	err := WithToolFeedback(cause, "缺少 message 或 tool_name")

	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(%v, cause) = false, want true", err)
	}

	feedback, ok := ToolFeedback(err)
	if !ok {
		t.Fatal("ToolFeedback() ok = false, want true")
	}
	if feedback != "缺少 message 或 tool_name" {
		t.Fatalf("ToolFeedback() feedback = %q, want %q", feedback, "缺少 message 或 tool_name")
	}
}

func TestToolFeedbackRejectsPlainInternalError(t *testing.T) {
	err := errors.New("database password=secret")

	feedback, ok := ToolFeedback(err)
	if ok {
		t.Fatalf("ToolFeedback() ok = true, want false (feedback %q)", feedback)
	}
	if feedback != "" {
		t.Fatalf("ToolFeedback() feedback = %q, want empty", feedback)
	}
}

func TestToolFeedbackRejectsUntrustedProvider(t *testing.T) {
	err := untrustedToolFeedbackError{}

	feedback, ok := ToolFeedback(err)
	if ok {
		t.Fatalf("ToolFeedback() ok = true, want false (feedback %q)", feedback)
	}
	if feedback != "" {
		t.Fatalf("ToolFeedback() feedback = %q, want empty", feedback)
	}
}

func TestToolFeedbackFindsTrustedFeedbackThroughWrapping(t *testing.T) {
	wrapped := WithToolFeedback(errors.New("internal"), "缺少 message")
	err := fmt.Errorf("context: %w", wrapped)

	feedback, ok := ToolFeedback(err)
	if !ok {
		t.Fatal("ToolFeedback() ok = false, want true")
	}
	if feedback != "缺少 message" {
		t.Fatalf("ToolFeedback() feedback = %q, want %q", feedback, "缺少 message")
	}
}

func TestWithToolFeedbackPreservesTypedCause(t *testing.T) {
	cause := &typedInternalError{}
	err := WithToolFeedback(cause, "safe feedback")

	var target *typedInternalError
	if !errors.As(err, &target) {
		t.Fatal("errors.As() = false, want true")
	}
	if target != cause {
		t.Fatalf("errors.As() target = %p, want original cause %p", target, cause)
	}
}

func TestWithToolFeedbackNilInputReturnsNil(t *testing.T) {
	if err := WithToolFeedback(nil, "safe feedback"); err != nil {
		t.Fatalf("WithToolFeedback(nil, feedback) = %v, want nil", err)
	}
}

func TestToolFeedbackNilInputReturnsNoFeedback(t *testing.T) {
	feedback, ok := ToolFeedback(nil)
	if ok {
		t.Fatalf("ToolFeedback(nil) ok = true, want false (feedback %q)", feedback)
	}
	if feedback != "" {
		t.Fatalf("ToolFeedback(nil) feedback = %q, want empty", feedback)
	}
}

func TestWithToolFeedbackTrimsFeedback(t *testing.T) {
	err := WithToolFeedback(errors.New("internal"), " \t 缺少 message 或 tool_name \n")

	feedback, ok := ToolFeedback(err)
	if !ok {
		t.Fatal("ToolFeedback() ok = false, want true")
	}
	if feedback != "缺少 message 或 tool_name" {
		t.Fatalf("ToolFeedback() feedback = %q, want %q", feedback, "缺少 message 或 tool_name")
	}
}

func TestWithToolFeedbackRejectsEmptyFeedback(t *testing.T) {
	cause := errors.New("database password=secret")
	err := WithToolFeedback(cause, " \t\n")

	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(%v, cause) = false, want true", err)
	}

	feedback, ok := ToolFeedback(err)
	if ok {
		t.Fatalf("ToolFeedback() ok = true, want false (feedback %q)", feedback)
	}
	if feedback != "" {
		t.Fatalf("ToolFeedback() feedback = %q, want empty", feedback)
	}
}
