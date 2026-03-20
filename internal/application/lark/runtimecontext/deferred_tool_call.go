package runtimecontext

import (
	"context"
	"strings"
	"sync"
	"time"
)

type DeferredToolCall struct {
	PlaceholderOutput string    `json:"placeholder_output,omitempty"`
	ApprovalType      string    `json:"approval_type,omitempty"`
	ApprovalTitle     string    `json:"approval_title,omitempty"`
	ApprovalSummary   string    `json:"approval_summary,omitempty"`
	ApprovalExpiresAt time.Time `json:"approval_expires_at,omitempty"`
}

type DeferredToolCallCollector struct {
	mu    sync.Mutex
	queue []DeferredToolCall
}

type deferredToolCallCollectorKey struct{}

func NewDeferredToolCallCollector() *DeferredToolCallCollector {
	return &DeferredToolCallCollector{}
}

func WithDeferredToolCallCollector(ctx context.Context, collector *DeferredToolCallCollector) context.Context {
	if collector == nil {
		return ctx
	}
	return context.WithValue(ctx, deferredToolCallCollectorKey{}, collector)
}

func RecordDeferredToolCall(ctx context.Context, call DeferredToolCall) bool {
	collector := CollectorFromContext(ctx)
	if collector == nil {
		return false
	}
	if strings.TrimSpace(call.PlaceholderOutput) == "" {
		call.PlaceholderOutput = "已发起审批，等待确认后继续执行。"
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.queue = append(collector.queue, call)
	return true
}

func PopDeferredToolCall(ctx context.Context) (DeferredToolCall, bool) {
	collector := CollectorFromContext(ctx)
	if collector == nil {
		return DeferredToolCall{}, false
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.queue) == 0 {
		return DeferredToolCall{}, false
	}
	call := collector.queue[0]
	collector.queue = collector.queue[1:]
	return call, true
}

func CollectorFromContext(ctx context.Context) *DeferredToolCallCollector {
	if ctx == nil {
		return nil
	}
	collector, _ := ctx.Value(deferredToolCallCollectorKey{}).(*DeferredToolCallCollector)
	return collector
}
