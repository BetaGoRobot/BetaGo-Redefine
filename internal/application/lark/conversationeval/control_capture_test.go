package conversationeval

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
)

func TestCaptureFromContextIsNilSafeNoOp(t *testing.T) {
	capture := FromContext(context.Background())
	capture.RecordIntent(context.Background(), map[string]any{"need_reply": true})
	capture.RecordContext(context.Background(), ContextSnapshot{}, nil)
	capture.RecordToolPlan(context.Background(), ToolTrace{Name: "search_history"})
	capture.RecordOutput(context.Background(), Output{Decision: OutputDecisionSkip})
	capture.RecordDelivery(context.Background(), "message_ignored")

	ctx := WithCapture(context.Background(), nil)
	FromContext(ctx).RecordDelivery(ctx, "message_ignored")
}

func TestCaptureEnabledDistinguishesRecorderFromNoOp(t *testing.T) {
	if CaptureEnabled(context.Background()) {
		t.Fatal("background context unexpectedly has capture enabled")
	}
	if CaptureEnabled(WithCapture(context.Background(), nil)) {
		t.Fatal("nil capture unexpectedly enabled")
	}
	if !CaptureEnabled(WithCapture(context.Background(), NewCaptureRecorder())) {
		t.Fatal("recorder capture is not enabled")
	}
}

func TestRecorderCapturesConcurrentArtifactsAndReturnsDeepSnapshot(t *testing.T) {
	recorder := NewCaptureRecorder()
	ctx := WithCapture(context.Background(), recorder)
	anchor := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	snapshot := ContextSnapshot{
		SchemaVersion: SchemaVersion,
		AnchorEventID: "anchor_1",
		AnchorAt:      anchor,
		Messages: []ContextItem{{
			ID: "message_1", Source: ContextSourceHistory, SourceID: "message_1",
			Kind: ContextKindMessage, Content: "hello", ContentHash: ContentSHA256("hello"),
			Rank: 0, TokenCount: EstimateTokens("hello"), Selected: true,
			OccurredAt: anchor.Add(-time.Minute), Metadata: json.RawMessage(`{"thread_id":"thread_1"}`),
		}},
		SystemPrompt:  "system",
		UserPrompt:    "user",
		TokenEstimate: EstimateTokens("hello"),
		TokenBudget:   100,
	}
	recorder.RecordContext(ctx, snapshot, nil)
	recorder.RecordIntent(ctx, map[string]any{"need_reply": true, "reason": "direct"})

	const calls = 64
	var group sync.WaitGroup
	for range calls {
		group.Add(1)
		go func() {
			defer group.Done()
			recorder.RecordToolPlan(ctx, ToolTrace{
				CallID: "call", Name: "search_history", Arguments: json.RawMessage(`{"query":"hello"}`),
				Output: "result", OutputSource: ToolOutputSourceCapability,
			})
		}()
	}
	group.Wait()
	recorder.RecordOutput(ctx, Output{
		Decision: OutputDecisionReply, Reply: "world", Thought: "answer",
		References: References{History: "message_1"},
		Latency:    250 * time.Millisecond,
		TokenUsage: &TokenUsage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, Records: 1},
	})
	recorder.RecordDelivery(ctx, "om_delivery")

	got := recorder.Snapshot()
	if len(got.ToolPlans) != calls {
		t.Fatalf("tool plan count = %d, want %d", len(got.ToolPlans), calls)
	}
	if got.Context == nil || got.Context.Messages[0].Content != "hello" {
		t.Fatalf("captured context = %#v", got.Context)
	}
	if got.Output == nil || got.Output.Decision != OutputDecisionReply ||
		got.Output.TokenUsage == nil || got.Output.TokenUsage.TotalTokens != 14 {
		t.Fatalf("captured output = %#v", got.Output)
	}
	if got.DeliveryMessageID != "om_delivery" {
		t.Fatalf("delivery message ID = %q", got.DeliveryMessageID)
	}

	got.Context.Messages[0].Content = "mutated"
	got.ToolPlans[0][0] = '{'
	second := recorder.Snapshot()
	if second.Context.Messages[0].Content != "hello" || !json.Valid(second.ToolPlans[0]) {
		t.Fatalf("Snapshot() leaked mutable state: %#v", second)
	}
}

func TestCaptureShapesMarshalDeterministically(t *testing.T) {
	value := Output{
		Decision: OutputDecisionReply,
		Reply:    "hello",
		Thought:  "direct",
		References: References{
			Web:     "https://example.com",
			History: "message_1",
		},
		CapabilityCalls: []ToolTrace{{
			CallID: "call_1", Name: "search_history",
			Arguments: json.RawMessage(`{"query":"hello"}`),
			Output:    "found", OutputSource: ToolOutputSourceCapability,
		}},
		Latency: 1500 * time.Millisecond,
		TokenUsage: &TokenUsage{
			PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25, Records: 1,
		},
	}
	first, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("first marshal: %v", err)
	}
	second, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("second marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("JSON is not deterministic:\n%s\n%s", first, second)
	}
	const want = `{"decision":"reply","reply":"hello","thought":"direct","references":{"web":"https://example.com","history":"message_1"},"capability_calls":[{"call_id":"call_1","name":"search_history","arguments":{"query":"hello"},"output":"found","output_source":"capability"}],"latency_ms":1500,"token_usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25,"records":1}}`
	if string(first) != want {
		t.Fatalf("marshaled Output = %s\nwant = %s", first, want)
	}
}

func TestContextHelpersUseStableHashAndTokenEstimate(t *testing.T) {
	if got := ContentSHA256("hello"); got != "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("ContentSHA256() = %q", got)
	}
	if got := EstimateTokens("你好abc"); got != 2 {
		t.Fatalf("EstimateTokens() = %d, want deterministic ceil(runes/4)=2", got)
	}
}

func TestCaptureRecorderAggregatesUsageAcrossRequestStages(t *testing.T) {
	recorder := NewCaptureRecorder()
	ctx := WithCapture(context.Background(), recorder)

	if err := llmusage.RecordUsage(ctx, llmusage.Record{
		PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7,
	}); err != nil {
		t.Fatalf("RecordUsage(intent) error = %v", err)
	}
	if err := llmusage.RecordUsage(ctx, llmusage.Record{
		PromptTokens: 11, CompletionTokens: 3, TotalTokens: 14,
	}); err != nil {
		t.Fatalf("RecordUsage(chat) error = %v", err)
	}
	recorder.RecordOutput(ctx, Output{Decision: OutputDecisionReply, Reply: "ok"})

	output := recorder.Snapshot().Output
	if output == nil || output.TokenUsage == nil {
		t.Fatal("captured output token usage is nil")
	}
	if got := *output.TokenUsage; got.PromptTokens != 16 ||
		got.CompletionTokens != 5 || got.TotalTokens != 21 || got.Records != 2 {
		t.Fatalf("captured token usage = %+v", got)
	}
}

func TestCaptureRecorderCloneFailureDoesNotRetainMutableInput(t *testing.T) {
	recorder := NewCaptureRecorder()
	messages := []ContextItem{{
		ID: "message_1", Source: ContextSourceHistory, SourceID: "message_1",
		Kind: ContextKindMessage, Content: "original", ContentHash: ContentSHA256("original"),
		Selected: true, OccurredAt: time.UnixMilli(1), Metadata: json.RawMessage(`{`),
	}}
	recorder.RecordContext(context.Background(), ContextSnapshot{Messages: messages}, nil)
	messages[0].Content = "mutated"

	snapshot := recorder.Snapshot()
	if snapshot.Context == nil {
		t.Fatal("Snapshot().Context is nil")
	}
	if len(snapshot.Context.Messages) > 0 && snapshot.Context.Messages[0].Content == "mutated" {
		t.Fatal("clone failure retained caller-owned mutable slice")
	}
}
