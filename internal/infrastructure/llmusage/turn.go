package llmusage

import (
	"strings"
	"sync"
	"time"
)

type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

type TurnOptions struct {
	Scope     Scope
	Provider  string
	Model     string
	Kind      Kind
	CreatedAt time.Time
}

type TurnAccumulator struct {
	mu sync.Mutex

	options       TurnOptions
	seenResponses map[string]struct{}
	responseID    string
	usage         Usage
	toolCalls     []ToolCall
}

func NewTurnAccumulator(options TurnOptions) *TurnAccumulator {
	if options.CreatedAt.IsZero() {
		options.CreatedAt = time.Now()
	}
	return &TurnAccumulator{
		options: options, seenResponses: make(map[string]struct{}),
	}
}

func (a *TurnAccumulator) AddUsage(responseID string, usage Usage) bool {
	if a == nil {
		return false
	}
	responseID = strings.TrimSpace(responseID)
	a.mu.Lock()
	defer a.mu.Unlock()
	if responseID != "" {
		if _, exists := a.seenResponses[responseID]; exists {
			return false
		}
		a.seenResponses[responseID] = struct{}{}
		a.responseID = responseID
	}
	a.usage.PromptTokens += max(usage.PromptTokens, 0)
	a.usage.CompletionTokens += max(usage.CompletionTokens, 0)
	a.usage.TotalTokens += max(usage.TotalTokens, 0)
	return true
}

func (a *TurnAccumulator) AddToolCall(call ToolCall) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolCalls = append(a.toolCalls, call)
}

func (a *TurnAccumulator) Record(status Status, errorText string) Record {
	if a == nil {
		return Record{Status: status, Error: strings.TrimSpace(errorText)}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return Record{
		Scope: a.options.Scope, Provider: a.options.Provider,
		Model: a.options.Model, Kind: a.options.Kind, Status: status,
		PromptTokens: a.usage.PromptTokens, CompletionTokens: a.usage.CompletionTokens,
		TotalTokens: a.usage.TotalTokens, ResponseID: a.responseID,
		Error: strings.TrimSpace(errorText), CreatedAt: a.options.CreatedAt,
		ToolCalls: append([]ToolCall(nil), a.toolCalls...),
	}
}
