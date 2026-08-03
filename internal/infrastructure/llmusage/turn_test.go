package llmusage

import (
	"testing"
	"time"
)

func TestTurnAccumulatorSumsResponseLegsAndRecordsToolsOnce(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	turn := NewTurnAccumulator(TurnOptions{
		Scope: Scope{
			SourceType: SourceTypeUser, Source: "chat",
			BusinessScene: SceneConversation, BusinessOperation: OperationChatReply,
		},
		Provider: "ark", Model: "model", Kind: KindResponsesStream, CreatedAt: createdAt,
	})
	turn.AddUsage("resp-plan", Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12})
	turn.AddToolCall(ToolCall{Name: "search_history", Status: ToolStatusSuccess, Duration: 40 * time.Millisecond})
	turn.AddUsage("resp-final", Usage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25})

	got := turn.Record(StatusSuccess, "")
	if got.PromptTokens != 30 || got.CompletionTokens != 7 || got.TotalTokens != 37 {
		t.Fatalf("turn tokens = %d/%d/%d", got.PromptTokens, got.CompletionTokens, got.TotalTokens)
	}
	if got.ResponseID != "resp-final" {
		t.Fatalf("ResponseID = %q, want final response", got.ResponseID)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "search_history" {
		t.Fatalf("tool calls = %+v", got.ToolCalls)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %s, want %s", got.CreatedAt, createdAt)
	}
}

func TestTurnAccumulatorDeduplicatesResponseCompletedReplay(t *testing.T) {
	turn := NewTurnAccumulator(TurnOptions{Scope: Scope{Source: "chat"}, Provider: "ark", Model: "model", Kind: KindResponsesStream})
	usage := Usage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}
	turn.AddUsage("resp-1", usage)
	turn.AddUsage("resp-1", usage)
	got := turn.Record(StatusSuccess, "")
	if got.TotalTokens != 6 {
		t.Fatalf("TotalTokens = %d, want deduplicated 6", got.TotalTokens)
	}
}

func TestTurnAccumulatorReturnsDefensiveToolCopies(t *testing.T) {
	turn := NewTurnAccumulator(TurnOptions{Scope: Scope{Source: "chat"}})
	turn.AddToolCall(ToolCall{Name: "tool", Status: ToolStatusSuccess})
	first := turn.Record(StatusSuccess, "")
	first.ToolCalls[0].Name = "mutated"
	second := turn.Record(StatusSuccess, "")
	if second.ToolCalls[0].Name != "tool" {
		t.Fatalf("stored tool call mutated through Record(): %+v", second.ToolCalls)
	}
}
