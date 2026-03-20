package runtimecontext

import (
	"context"
	"strings"
	"sync"
)

type CompatibleReplyRef struct {
	MessageID string `json:"message_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

type CompatibleReplyRecorder struct {
	mu   sync.Mutex
	last CompatibleReplyRef
}

type compatibleReplyRecorderKey struct{}

func NewCompatibleReplyRecorder() *CompatibleReplyRecorder {
	return &CompatibleReplyRecorder{}
}

func WithCompatibleReplyRecorder(ctx context.Context, recorder *CompatibleReplyRecorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, compatibleReplyRecorderKey{}, recorder)
}

func RecordCompatibleReplyRef(ctx context.Context, messageID, kind string) bool {
	recorder := CompatibleReplyRecorderFromContext(ctx)
	if recorder == nil {
		return false
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return false
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.last = CompatibleReplyRef{
		MessageID: messageID,
		Kind:      strings.TrimSpace(kind),
	}
	return true
}

func LatestCompatibleReplyRef(ctx context.Context) (CompatibleReplyRef, bool) {
	recorder := CompatibleReplyRecorderFromContext(ctx)
	if recorder == nil {
		return CompatibleReplyRef{}, false
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.last.MessageID == "" {
		return CompatibleReplyRef{}, false
	}
	return recorder.last, true
}

func CompatibleReplyRecorderFromContext(ctx context.Context) *CompatibleReplyRecorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(compatibleReplyRecorderKey{}).(*CompatibleReplyRecorder)
	return recorder
}
