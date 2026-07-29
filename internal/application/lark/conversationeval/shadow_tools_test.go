package conversationeval

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestObservationCacheReplaysControlOutputByCanonicalArguments(t *testing.T) {
	cache := NewObservationCache()
	err := cache.RecordControl(context.Background(), "episode-1", ToolTrace{
		Name:      "finance_market_data_get",
		Arguments: json.RawMessage(`{"symbols":["AAPL"],"market":"US"}`),
		Output:    `{"price":210}`,
	})
	if err != nil {
		t.Fatalf("RecordControl() error = %v", err)
	}

	got, ok, err := cache.Replay(
		context.Background(),
		"episode-1",
		"finance_market_data_get",
		json.RawMessage(`{"market":"US","symbols":["AAPL"]}`),
	)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if !ok {
		t.Fatal("Replay() missed canonical-equivalent arguments")
	}
	if got.Output != `{"price":210}` ||
		got.SourceLane != LaneControl ||
		!got.ReplayedFromControl ||
		got.ObservedAt.IsZero() ||
		string(got.CanonicalArguments) != `{"market":"US","symbols":["AAPL"]}` {
		t.Fatalf("replayed observation = %#v", got)
	}

	if _, ok, err := cache.Replay(
		context.Background(),
		"episode-2",
		"finance_market_data_get",
		json.RawMessage(`{"market":"US","symbols":["AAPL"]}`),
	); err != nil || ok {
		t.Fatalf("different episode replay = ok %v err %v, want miss", ok, err)
	}
	if _, ok, err := cache.Replay(
		context.Background(),
		"episode-1",
		"finance_news_get",
		json.RawMessage(`{"market":"US","symbols":["AAPL"]}`),
	); err != nil || ok {
		t.Fatalf("different tool replay = ok %v err %v, want miss", ok, err)
	}
}

func TestObservationCacheRejectsUnknownToolsAndInvalidArguments(t *testing.T) {
	cache := NewObservationCache()
	if err := cache.RecordControl(context.Background(), "episode-1", ToolTrace{
		Name: "unknown_tool", Arguments: json.RawMessage(`{}`), Output: "unsafe",
	}); err == nil {
		t.Fatal("RecordControl() accepted unknown tool defaulting to none")
	}
	if err := cache.RecordControl(context.Background(), "episode-1", ToolTrace{
		Name: "research_read_url", Arguments: json.RawMessage(`{"url":"https://example.com"}`), Output: "outside candidate allowlist",
	}); err == nil {
		t.Fatal("RecordControl() accepted explicit-none tool outside candidate allowlist")
	}
	if err := cache.RecordControl(context.Background(), "episode-1", ToolTrace{
		Name: "finance_news_get", Arguments: json.RawMessage(`{bad`), Output: "invalid",
	}); err == nil {
		t.Fatal("RecordControl() accepted invalid arguments")
	}
}

func TestObservationCacheCanonicalArgumentsPreserveLargeIntegerIdentity(t *testing.T) {
	cache := NewObservationCache()
	if err := cache.RecordControl(context.Background(), "episode-1", ToolTrace{
		Name: "finance_news_get", Arguments: json.RawMessage(`{"cursor":9007199254740992}`), Output: "first",
	}); err != nil {
		t.Fatalf("RecordControl() error = %v", err)
	}
	if _, ok, err := cache.Replay(
		context.Background(),
		"episode-1",
		"finance_news_get",
		json.RawMessage(`{"cursor":9007199254740993}`),
	); err != nil || ok {
		t.Fatalf("large integer replay = ok %v err %v, want distinct key miss", ok, err)
	}
}

func TestObservationCacheRecordsCompletedCandidateTracesFromControlSnapshot(t *testing.T) {
	cache := NewObservationCache()
	err := cache.RecordControlSnapshot(context.Background(), "episode-1", CaptureSnapshot{
		Output: &Output{CapabilityCalls: []ToolTrace{
			{
				Name: "finance_news_get", Arguments: json.RawMessage(`{"symbol":"AAPL"}`),
				Output: "cached",
			},
			{
				Name: "finance_market_data_get", Arguments: json.RawMessage(`{"symbol":"AAPL"}`),
				Output: "pending", Pending: true,
			},
			{
				Name: "send_message", Arguments: json.RawMessage(`{"content":"no"}`),
				Output: "unsafe",
			},
		}},
	})
	if err != nil {
		t.Fatalf("RecordControlSnapshot() error = %v", err)
	}
	got, ok, err := cache.Replay(
		context.Background(),
		"episode-1",
		"finance_news_get",
		json.RawMessage(`{"symbol":"AAPL"}`),
	)
	if err != nil || !ok || got.Output != "cached" {
		t.Fatalf("completed control replay = %#v, ok %v, err %v", got, ok, err)
	}
	if _, ok, err := cache.Replay(
		context.Background(),
		"episode-1",
		"finance_market_data_get",
		json.RawMessage(`{"symbol":"AAPL"}`),
	); err != nil || ok {
		t.Fatalf("pending control replay = ok %v err %v, want miss", ok, err)
	}
}

func TestShadowToolRegistryReplaysControlBeforeCallingCandidateTool(t *testing.T) {
	cache := NewObservationCache()
	if err := cache.RecordControl(context.Background(), "episode-1", ToolTrace{
		Name: "finance_news_get", Arguments: json.RawMessage(`{"symbol":"AAPL"}`),
		Output: "control-output",
	}); err != nil {
		t.Fatalf("RecordControl() error = %v", err)
	}
	registry := NewShadowToolRegistry(cache)
	calls := 0
	if err := registry.Register("finance_news_get", func(context.Context, json.RawMessage) (string, error) {
		calls++
		return "candidate-output", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := registry.Invoke(
		context.Background(),
		"episode-1",
		"finance_news_get",
		json.RawMessage(`{"symbol":"AAPL"}`),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("candidate tool calls = %d, want zero on replay", calls)
	}
	if got.Output != "control-output" || !got.ReplayedFromControl || got.SourceLane != LaneControl {
		t.Fatalf("replayed result = %#v", got)
	}
}

func TestShadowToolRegistryMarksFreshCandidateObservationTime(t *testing.T) {
	registry := NewShadowToolRegistry(NewObservationCache())
	if err := registry.Register("finance_news_get", func(context.Context, json.RawMessage) (string, error) {
		return "candidate-output", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := registry.Invoke(
		context.Background(),
		"episode-1",
		"finance_news_get",
		json.RawMessage(`{"symbol":"AAPL"}`),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got.SourceLane != LaneCandidate || got.ReplayedFromControl || got.ObservedAt.IsZero() {
		t.Fatalf("fresh candidate observation = %#v", got)
	}
}

func TestShadowToolRegistryRecordsAnchorClampedSearchHistoryArguments(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 123, time.UTC)
	registry := NewAnchoredShadowToolRegistry(NewObservationCache(), anchor)
	var invokedArguments json.RawMessage
	if err := registry.Register("search_history", func(_ context.Context, arguments json.RawMessage) (string, error) {
		invokedArguments = append(json.RawMessage(nil), arguments...)
		return "candidate-output", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := registry.Invoke(
		context.Background(),
		"episode-1",
		"search_history",
		json.RawMessage(`{"keywords":"callback","end_time":"2026-07-30T00:00:00Z"}`),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	wantEndTime := anchor.Format(time.RFC3339Nano)
	for name, arguments := range map[string]json.RawMessage{
		"handler":     invokedArguments,
		"observation": got.CanonicalArguments,
	} {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(arguments, &object); err != nil {
			t.Fatalf("%s arguments = %s: %v", name, arguments, err)
		}
		var endTime string
		if err := json.Unmarshal(object["end_time"], &endTime); err != nil {
			t.Fatalf("%s end_time = %s: %v", name, object["end_time"], err)
		}
		if endTime != wantEndTime {
			t.Fatalf("%s end_time = %q, want %q", name, endTime, wantEndTime)
		}
	}
}

func TestShadowToolRegistryRejectsOutsideRegistryWithoutFallback(t *testing.T) {
	registry := NewShadowToolRegistry(NewObservationCache())
	calls := 0
	fake := func(context.Context, json.RawMessage) (string, error) {
		calls++
		return "unsafe", nil
	}
	for _, name := range []string{"send_message", "config_set", "research_read_url", "unknown_tool"} {
		if err := registry.Register(name, fake); err == nil {
			t.Fatalf("Register(%q) accepted unsafe or outside-allowlist tool", name)
		}
		if _, err := registry.Invoke(context.Background(), "episode-1", name, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("Invoke(%q) used a fallback outside shadow registry", name)
		}
	}
	if _, err := registry.Invoke(
		context.Background(),
		"episode-1",
		"finance_news_get",
		json.RawMessage(`{}`),
	); err == nil {
		t.Fatal("Invoke() fell back for allowlisted but unregistered tool")
	}
	if calls != 0 {
		t.Fatalf("unsafe fake calls = %d, want zero", calls)
	}
}

func TestShadowToolRegistryKeepsRecentActiveMembersReplayOnlyOnCacheMiss(t *testing.T) {
	cache := NewObservationCache()
	registry := NewShadowToolRegistry(cache)
	calls := 0
	if err := registry.Register("get_recent_active_members", func(context.Context, json.RawMessage) (string, error) {
		calls++
		return "future message preview", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if _, err := registry.Invoke(
		context.Background(),
		"episode-1",
		"get_recent_active_members",
		json.RawMessage(`{"top_k":5}`),
	); err == nil {
		t.Fatal("Invoke() allowed replay-only recent member lookup on cache miss")
	}
	if calls != 0 {
		t.Fatalf("replay-only handler calls = %d, want zero", calls)
	}
	if err := cache.RecordControl(context.Background(), "episode-1", ToolTrace{
		Name: "get_recent_active_members", Arguments: json.RawMessage(`{"top_k":5}`),
		Output: "control members",
	}); err != nil {
		t.Fatalf("RecordControl() error = %v", err)
	}
	got, err := registry.Invoke(
		context.Background(),
		"episode-1",
		"get_recent_active_members",
		json.RawMessage(`{"top_k":5}`),
	)
	if err != nil {
		t.Fatalf("Invoke() replay error = %v", err)
	}
	if got.Output != "control members" || !got.ReplayedFromControl || calls != 0 {
		t.Fatalf("replay-only control observation = %#v, handler calls %d", got, calls)
	}
}
