package conversationeval

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
)

type Capture interface {
	RecordIntent(context.Context, any)
	RecordContext(context.Context, ContextSnapshot, []ExcludedContextItem)
	RecordToolPlan(context.Context, any)
	RecordOutput(context.Context, Output)
	RecordDelivery(context.Context, string)
}

type OutputDecision string

const (
	OutputDecisionReply OutputDecision = "reply"
	OutputDecisionSkip  OutputDecision = "skip"
)

type ToolOutputSource string

const (
	ToolOutputSourceCapability ToolOutputSource = "capability"
	ToolOutputSourcePlanner    ToolOutputSource = "planner"
)

type ToolTrace struct {
	CallID       string           `json:"call_id,omitempty"`
	Name         string           `json:"name"`
	Arguments    json.RawMessage  `json:"arguments,omitempty"`
	Output       string           `json:"output,omitempty"`
	OutputSource ToolOutputSource `json:"output_source"`
	Pending      bool             `json:"pending,omitempty"`
}

type TokenUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	Records          int   `json:"records"`
}

type References struct {
	Web     string `json:"web,omitempty"`
	History string `json:"history,omitempty"`
}

type Output struct {
	Decision        OutputDecision `json:"decision"`
	Reply           string         `json:"reply,omitempty"`
	Thought         string         `json:"thought,omitempty"`
	References      References     `json:"references,omitempty"`
	CapabilityCalls []ToolTrace    `json:"capability_calls,omitempty"`
	Latency         time.Duration  `json:"-"`
	TokenUsage      *TokenUsage    `json:"token_usage,omitempty"`
}

func (o Output) MarshalJSON() ([]byte, error) {
	type wireOutput struct {
		Decision        OutputDecision `json:"decision"`
		Reply           string         `json:"reply,omitempty"`
		Thought         string         `json:"thought,omitempty"`
		References      References     `json:"references,omitempty"`
		CapabilityCalls []ToolTrace    `json:"capability_calls,omitempty"`
		LatencyMS       int64          `json:"latency_ms"`
		TokenUsage      *TokenUsage    `json:"token_usage,omitempty"`
	}
	return json.Marshal(wireOutput{
		Decision: o.Decision, Reply: o.Reply, Thought: o.Thought,
		References: o.References, CapabilityCalls: o.CapabilityCalls,
		LatencyMS: o.Latency.Milliseconds(), TokenUsage: o.TokenUsage,
	})
}

func (o *Output) UnmarshalJSON(data []byte) error {
	type wireOutput struct {
		Decision        OutputDecision `json:"decision"`
		Reply           string         `json:"reply,omitempty"`
		Thought         string         `json:"thought,omitempty"`
		References      References     `json:"references,omitempty"`
		CapabilityCalls []ToolTrace    `json:"capability_calls,omitempty"`
		LatencyMS       int64          `json:"latency_ms"`
		TokenUsage      *TokenUsage    `json:"token_usage,omitempty"`
	}
	var wire wireOutput
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*o = Output{
		Decision: wire.Decision, Reply: wire.Reply, Thought: wire.Thought,
		References: wire.References, CapabilityCalls: wire.CapabilityCalls,
		Latency:    time.Duration(wire.LatencyMS) * time.Millisecond,
		TokenUsage: wire.TokenUsage,
	}
	return nil
}

type CaptureSnapshot struct {
	IntentJSON        json.RawMessage       `json:"intent,omitempty"`
	Context           *ContextSnapshot      `json:"context,omitempty"`
	ExcludedContext   []ExcludedContextItem `json:"excluded_context,omitempty"`
	ToolPlans         []json.RawMessage     `json:"tool_plans,omitempty"`
	Output            *Output               `json:"output,omitempty"`
	DeliveryMessageID string                `json:"delivery_message_id,omitempty"`
}

type CaptureRecorder struct {
	mu                sync.RWMutex
	intentJSON        json.RawMessage
	context           *ContextSnapshot
	excludedContext   []ExcludedContextItem
	toolPlans         []json.RawMessage
	output            *Output
	deliveryMessageID string
	usage             llmusage.Collector
}

func NewCaptureRecorder() *CaptureRecorder {
	return &CaptureRecorder{}
}

func (r *CaptureRecorder) RecordIntent(_ context.Context, value any) {
	encoded, ok := encodeCaptureValue(value)
	if r == nil || !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.intentJSON = encoded
}

func (r *CaptureRecorder) RecordContext(
	_ context.Context,
	snapshot ContextSnapshot,
	excluded []ExcludedContextItem,
) {
	if r == nil {
		return
	}
	contextCopy := cloneCaptureValue(snapshot)
	excludedCopy := cloneCaptureValue(excluded)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.context = &contextCopy
	r.excludedContext = excludedCopy
}

func (r *CaptureRecorder) RecordToolPlan(_ context.Context, value any) {
	encoded, ok := encodeCaptureValue(value)
	if r == nil || !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolPlans = append(r.toolPlans, encoded)
}

func (r *CaptureRecorder) RecordOutput(_ context.Context, output Output) {
	if r == nil {
		return
	}
	totals := r.usage.Totals()
	if totals.Records > 0 {
		output.TokenUsage = &TokenUsage{
			PromptTokens:     totals.PromptTokens,
			CompletionTokens: totals.CompletionTokens,
			TotalTokens:      totals.TotalTokens,
			Records:          totals.Records,
		}
	}
	outputCopy := cloneCaptureValue(output)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.output = &outputCopy
}

func (r *CaptureRecorder) ObserveUsage(ctx context.Context, record llmusage.Record) {
	if r == nil {
		return
	}
	r.usage.ObserveUsage(ctx, record)
}

func (r *CaptureRecorder) RecordDelivery(_ context.Context, messageID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveryMessageID = messageID
}

func (r *CaptureRecorder) Snapshot() CaptureSnapshot {
	if r == nil {
		return CaptureSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value := CaptureSnapshot{
		IntentJSON:        append(json.RawMessage(nil), r.intentJSON...),
		ExcludedContext:   cloneCaptureValue(r.excludedContext),
		ToolPlans:         make([]json.RawMessage, len(r.toolPlans)),
		DeliveryMessageID: r.deliveryMessageID,
	}
	if r.context != nil {
		contextCopy := cloneCaptureValue(*r.context)
		value.Context = &contextCopy
	}
	for index := range r.toolPlans {
		value.ToolPlans[index] = append(json.RawMessage(nil), r.toolPlans[index]...)
	}
	if r.output != nil {
		outputCopy := cloneCaptureValue(*r.output)
		value.Output = &outputCopy
	}
	return value
}

type captureContextKey struct{}

func WithCapture(ctx context.Context, capture Capture) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if capture == nil {
		capture = noopCapture{}
	}
	ctx = context.WithValue(ctx, captureContextKey{}, capture)
	if observer, ok := capture.(llmusage.Observer); ok {
		ctx = llmusage.WithObserver(ctx, observer)
	}
	return ctx
}

func FromContext(ctx context.Context) Capture {
	if ctx != nil {
		if capture, ok := ctx.Value(captureContextKey{}).(Capture); ok && capture != nil {
			return capture
		}
	}
	return noopCapture{}
}

type noopCapture struct{}

func (noopCapture) RecordIntent(context.Context, any)                                     {}
func (noopCapture) RecordContext(context.Context, ContextSnapshot, []ExcludedContextItem) {}
func (noopCapture) RecordToolPlan(context.Context, any)                                   {}
func (noopCapture) RecordOutput(context.Context, Output)                                  {}
func (noopCapture) RecordDelivery(context.Context, string)                                {}

func encodeCaptureValue(value any) (json.RawMessage, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(encoded), true
}

func cloneCaptureValue[T any](value T) T {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned T
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return value
	}
	return cloned
}
