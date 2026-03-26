package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

func TestHistorySearchHandlerInjectsChatIDScope(t *testing.T) {
	old := historyHybridSearchFn
	defer func() {
		historyHybridSearchFn = old
	}()

	var captured history.HybridSearchRequest
	historyHybridSearchFn = func(ctx context.Context, req history.HybridSearchRequest, embeddingFunc history.EmbeddingFunc) ([]*history.SearchResult, error) {
		captured = req
		return []*history.SearchResult{{MessageID: "om_1", OpenID: "ou_bot", RawMessage: "hello"}}, nil
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_test_chat"}
	err := SearchHistory.Handle(context.Background(), nil, meta, HistorySearchArgs{
		Keywords:    "机器人",
		OpenID:      "ou_test",
		UserName:    "Alice",
		MessageType: "text",
		TopK:        8,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if captured.ChatID != "oc_test_chat" {
		t.Fatalf("captured chat_id = %q, want %q", captured.ChatID, "oc_test_chat")
	}
	if captured.UserName != "Alice" {
		t.Fatalf("captured user_name = %q, want %q", captured.UserName, "Alice")
	}
	if captured.MessageType != "text" {
		t.Fatalf("captured message_type = %q, want %q", captured.MessageType, "text")
	}
	if result, ok := meta.GetExtra("search_result"); !ok || !strings.Contains(result, "om_1") || !strings.Contains(result, `"user_id":"ou_bot"`) {
		t.Fatalf("search_result extra missing expected payload: %q", result)
	}
}

func TestHistorySearchHandlerRejectsEmptyChatID(t *testing.T) {
	old := historyHybridSearchFn
	defer func() {
		historyHybridSearchFn = old
	}()

	historyHybridSearchFn = func(ctx context.Context, req history.HybridSearchRequest, embeddingFunc history.EmbeddingFunc) ([]*history.SearchResult, error) {
		t.Fatal("history search should not be called when chat scope is empty")
		return nil, nil
	}

	err := SearchHistory.Handle(context.Background(), nil, &xhandler.BaseMetaData{}, HistorySearchArgs{
		Keywords: "机器人",
	})
	if err == nil {
		t.Fatal("expected empty chat_id error")
	}
	if !strings.Contains(err.Error(), "chat_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}
