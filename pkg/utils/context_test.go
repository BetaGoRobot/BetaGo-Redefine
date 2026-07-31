package utils

import (
	"context"
	"testing"
)

func TestDurableContextPreservesValuesWithoutCancellation(t *testing.T) {
	type contextKey string
	const key contextKey = "trace"

	parent, cancel := context.WithCancel(context.WithValue(
		context.Background(),
		key,
		"trace-value",
	))
	cancel()

	ctx := DurableContext(parent)
	if err := ctx.Err(); err != nil {
		t.Fatalf("DurableContext() inherited cancellation: %v", err)
	}
	if got := ctx.Value(key); got != "trace-value" {
		t.Fatalf("DurableContext() value = %v, want trace-value", got)
	}
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("DurableContext() inherited a deadline")
	}
}
