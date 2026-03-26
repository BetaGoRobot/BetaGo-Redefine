package runtimecontext

import (
	"context"
	"strings"
	"sync"
)

type AgenticReplyTarget struct {
	MessageID string `json:"message_id,omitempty"`
	CardID    string `json:"card_id,omitempty"`
}

type AgenticReplyTargetState struct {
	mu     sync.RWMutex
	root   AgenticReplyTarget
	active AgenticReplyTarget
}

type agenticReplyTargetStateKey struct{}

func NewAgenticReplyTargetState() *AgenticReplyTargetState {
	return &AgenticReplyTargetState{}
}

func WithAgenticReplyTargetState(ctx context.Context, state *AgenticReplyTargetState) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, agenticReplyTargetStateKey{}, state)
}

func AgenticReplyTargetStateFromContext(ctx context.Context) *AgenticReplyTargetState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(agenticReplyTargetStateKey{}).(*AgenticReplyTargetState)
	return state
}

func SeedRootAgenticReplyTarget(ctx context.Context, messageID, cardID string) bool {
	state := AgenticReplyTargetStateFromContext(ctx)
	if state == nil {
		return false
	}
	messageID = strings.TrimSpace(messageID)
	cardID = strings.TrimSpace(cardID)
	if messageID == "" && cardID == "" {
		return false
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.root.MessageID == "" && state.root.CardID == "" {
		state.root = AgenticReplyTarget{MessageID: messageID, CardID: cardID}
	}
	if state.active.MessageID == "" && state.active.CardID == "" {
		state.active = AgenticReplyTarget{MessageID: messageID, CardID: cardID}
	}
	return true
}

func RecordActiveAgenticReplyTarget(ctx context.Context, messageID, cardID string) bool {
	state := AgenticReplyTargetStateFromContext(ctx)
	if state == nil {
		return false
	}
	messageID = strings.TrimSpace(messageID)
	cardID = strings.TrimSpace(cardID)
	if messageID == "" && cardID == "" {
		return false
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.root.MessageID == "" && state.root.CardID == "" {
		state.root = AgenticReplyTarget{MessageID: messageID, CardID: cardID}
	}
	state.active = AgenticReplyTarget{MessageID: messageID, CardID: cardID}
	return true
}

func RootAgenticReplyTarget(ctx context.Context) (AgenticReplyTarget, bool) {
	state := AgenticReplyTargetStateFromContext(ctx)
	if state == nil {
		return AgenticReplyTarget{}, false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.root.MessageID == "" && state.root.CardID == "" {
		return AgenticReplyTarget{}, false
	}
	return state.root, true
}

func ActiveAgenticReplyTarget(ctx context.Context) (AgenticReplyTarget, bool) {
	state := AgenticReplyTargetStateFromContext(ctx)
	if state == nil {
		return AgenticReplyTarget{}, false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.active.MessageID == "" && state.active.CardID == "" {
		return AgenticReplyTarget{}, false
	}
	return state.active, true
}
