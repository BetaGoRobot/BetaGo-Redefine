package xerror

import (
	"errors"
	"testing"
)

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
