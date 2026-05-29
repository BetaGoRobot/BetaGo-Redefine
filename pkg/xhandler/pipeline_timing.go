package xhandler

import (
	"context"
	"time"
)

type pipelineStartedAtKey struct{}

func WithPipelineStartedAt(ctx context.Context, startedAt time.Time) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return context.WithValue(ctx, pipelineStartedAtKey{}, startedAt)
}

func PipelineStartedAt(ctx context.Context) (time.Time, bool) {
	if ctx == nil {
		return time.Time{}, false
	}
	startedAt, ok := ctx.Value(pipelineStartedAtKey{}).(time.Time)
	if !ok || startedAt.IsZero() {
		return time.Time{}, false
	}
	return startedAt, true
}

func PipelineElapsed(ctx context.Context) (time.Duration, bool) {
	startedAt, ok := PipelineStartedAt(ctx)
	if !ok {
		return 0, false
	}
	elapsed := max(time.Since(startedAt), 0)
	return elapsed, true
}

func PipelineElapsedString(ctx context.Context) string {
	elapsed, ok := PipelineElapsed(ctx)
	if !ok {
		return ""
	}
	return elapsed.Round(100 * time.Millisecond).String()
}
