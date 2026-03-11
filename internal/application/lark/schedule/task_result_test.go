package schedule

import (
	"context"
	"strings"
	"testing"
)

func TestTaskResultRefRoundTrip(t *testing.T) {
	objectKey := "task/task-1/20260311T120000.000000000Z.txt"
	ref := buildTaskResultRef(objectKey)
	if !strings.HasPrefix(ref, scheduleTaskResultPrefix) {
		t.Fatalf("unexpected ref: %s", ref)
	}
	got, ok := parseTaskResultRef(ref)
	if !ok {
		t.Fatal("expected ref to be parsed")
	}
	if got != objectKey {
		t.Fatalf("unexpected object key: got %s want %s", got, objectKey)
	}
}

func TestBuildTaskResultLineIgnoresLegacyValue(t *testing.T) {
	if got := buildTaskResultLine(context.Background(), "https://legacy.short/url"); got != "" {
		t.Fatalf("expected legacy value to be ignored, got %q", got)
	}
}
