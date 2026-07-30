package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
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
		return []*history.SearchResult{{
			MessageID: "om_1", OpenID: "ou_bot", RawMessage: "hello",
			CreateTimeUnixMillis: 9999999999999,
		}}, nil
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_test_chat"}
	const endTime = "2026-07-29T15:00:00.123Z"
	err := SearchHistory.Handle(context.Background(), nil, meta, HistorySearchArgs{
		Keywords:    "机器人",
		OpenID:      "ou_test",
		UserName:    "Alice",
		MessageType: "text",
		TopK:        8,
		EndTime:     endTime,
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
	if captured.MessageIndexOnly || captured.CausalEndMillis != 0 || captured.EndTime != endTime {
		t.Fatalf("default history request changed = %#v", captured)
	}
	if result, ok := meta.GetExtra("search_result"); !ok || !strings.Contains(result, "om_1") || !strings.Contains(result, `"user_id":"ou_bot"`) {
		t.Fatalf("search_result extra missing expected payload: %q", result)
	}
}

func TestCandidateShadowSearchHistoryUsesStrictCausalMessageIndexAndFiltersResults(t *testing.T) {
	useWorkspaceConfigPath(t)
	old := historyHybridSearchFn
	defer func() {
		historyHybridSearchFn = old
	}()

	anchor := time.Date(2026, 7, 29, 15, 0, 0, 123000000, time.UTC)
	var captured history.HybridSearchRequest
	historyHybridSearchFn = func(
		ctx context.Context,
		req history.HybridSearchRequest,
		embeddingFunc history.EmbeddingFunc,
	) ([]*history.SearchResult, error) {
		captured = req
		return []*history.SearchResult{
			{
				MessageID: "om_exact_future", RawMessage: "future exact",
				CreateTimeUnixMillis: anchor.UnixMilli() + 1,
				CreateTimeV2:         anchor.Add(time.Millisecond).Format(time.RFC3339Nano),
			},
			{
				MessageID: "om_legacy_same_second", RawMessage: "future ambiguous legacy",
				CreateTimeV2: anchor.Truncate(time.Second).Format(time.RFC3339),
			},
			{
				MessageID: "om_exact_past", RawMessage: "past exact",
				CreateTimeUnixMillis: anchor.UnixMilli() - 1,
				CreateTimeV2:         anchor.Add(-time.Millisecond).Format(time.RFC3339Nano),
			},
			{
				MessageID: "om_legacy_past", RawMessage: "past legacy",
				CreateTimeV2: anchor.Add(-time.Second).Truncate(time.Second).Format(time.RFC3339),
			},
		}, nil
	}

	registry, err := BuildCandidateShadowRegistry(
		conversationeval.NewObservationCache(),
		nil,
		"oc_test_chat",
		"ou_actor",
		anchor,
	)
	if err != nil {
		t.Fatalf("BuildCandidateShadowRegistry() error = %v", err)
	}
	observation, err := registry.Invoke(
		context.Background(),
		"episode-1",
		"search_history",
		json.RawMessage(`{"keywords":"机器人","end_time":"2026-07-30T00:00:00Z"}`),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if !captured.MessageIndexOnly ||
		captured.CausalEndMillis != anchor.UnixMilli() ||
		captured.EndTime != anchor.Format(time.RFC3339Nano) {
		t.Fatalf("candidate history request = %#v", captured)
	}
	if strings.Contains(observation.Output, "om_exact_future") ||
		strings.Contains(observation.Output, "om_legacy_same_second") {
		t.Fatalf("candidate output leaked post-anchor result: %s", observation.Output)
	}
	for _, messageID := range []string{"om_exact_past", "om_legacy_past"} {
		if !strings.Contains(observation.Output, messageID) {
			t.Fatalf("candidate output missing causal result %q: %s", messageID, observation.Output)
		}
	}
	if !strings.Contains(
		string(observation.CanonicalArguments),
		anchor.Format(time.RFC3339Nano),
	) {
		t.Fatalf("effective trace arguments = %s", observation.CanonicalArguments)
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
