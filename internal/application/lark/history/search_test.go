package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
)

func TestBuildHybridSearchQueryRequiresChatID(t *testing.T) {
	_, err := buildHybridSearchQuery(
		HybridSearchRequest{QueryText: []string{"机器人"}, TopK: 3},
		[]string{"机器人"},
		nil,
		time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC),
	)
	if err == nil {
		t.Fatal("expected chat_id requirement error")
	}
}

func TestBuildHybridSearchQueryIncludesMetadataFilters(t *testing.T) {
	query, err := buildHybridSearchQuery(
		HybridSearchRequest{
			QueryText:   []string{"机器人"},
			TopK:        7,
			ChatID:      "oc_test_chat",
			OpenID:      "ou_test_user",
			UserName:    "Alice",
			MessageType: "text",
			StartTime:   "2026-03-20 08:00:00",
			EndTime:     "2026-03-21 08:00:00",
		},
		[]string{"机器人"},
		[]map[string]any{
			{
				"knn": map[string]any{
					"message_v2": map[string]any{"vector": []float32{0.1, 0.2}, "k": 7, "boost": 2.0},
				},
			},
		},
		time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("buildHybridSearchQuery() error = %v", err)
	}

	if got := query["size"]; got != 7 {
		t.Fatalf("size = %v, want 7", got)
	}
	sourceFields, ok := query["_source"].([]string)
	if !ok {
		t.Fatalf("_source = %#v, want []string", query["_source"])
	}
	if !containsString(sourceFields, "user_id") {
		t.Fatalf("_source = %#v, want contain user_id", sourceFields)
	}

	boolQuery, ok := query["query"].(map[string]any)["bool"].(map[string]any)
	if !ok {
		t.Fatalf("bool query missing: %+v", query["query"])
	}
	mustClauses, ok := boolQuery["must"].([]map[string]any)
	if !ok {
		t.Fatalf("must clauses missing: %+v", boolQuery["must"])
	}
	if !containsTermFilter(mustClauses, "chat_id", "oc_test_chat") {
		t.Fatalf("must clauses missing chat_id filter: %+v", mustClauses)
	}
	if !containsTermFilter(mustClauses, "user_id", "ou_test_user") {
		t.Fatalf("must clauses missing user_id filter: %+v", mustClauses)
	}
	if !containsTermFilter(mustClauses, "user_name", "Alice") {
		t.Fatalf("must clauses missing user_name filter: %+v", mustClauses)
	}
	if !containsTermFilter(mustClauses, "message_type", "text") {
		t.Fatalf("must clauses missing message_type filter: %+v", mustClauses)
	}
	if !containsRangeFilter(mustClauses, "create_time_v2", "gte") {
		t.Fatalf("must clauses missing gte create_time_v2 range: %+v", mustClauses)
	}
	if !containsRangeFilter(mustClauses, "create_time_v2", "lte") {
		t.Fatalf("must clauses missing lte create_time_v2 range: %+v", mustClauses)
	}

	shouldClauses, ok := boolQuery["should"].([]map[string]any)
	if !ok {
		t.Fatalf("should clauses missing: %+v", boolQuery["should"])
	}
	if len(shouldClauses) != 2 {
		t.Fatalf("should clauses = %d, want 2", len(shouldClauses))
	}
}

func TestBuildVectorQueryClausesUsesV2MessageField(t *testing.T) {
	clauses := buildVectorQueryClauses(messageVectorFieldV2, [][]float32{{0.1, 0.2}}, 7)
	if len(clauses) != 1 {
		t.Fatalf("len(clauses) = %d, want 1", len(clauses))
	}
	knn, ok := clauses[0]["knn"].(map[string]any)
	if !ok {
		t.Fatalf("clause = %#v, want knn map", clauses[0])
	}
	if _, ok := knn["message_v2"]; !ok {
		t.Fatalf("knn fields = %#v, want message_v2", knn)
	}
}

func TestBuildVectorQueryClausesV2FieldConfirmed(t *testing.T) {
	// Verify that message_v2 is used and legacy message field is absent
	clauses := buildVectorQueryClauses(messageVectorFieldV2, [][]float32{{0.1, 0.2}, {0.3, 0.4}}, 5)
	if len(clauses) != 2 {
		t.Fatalf("len(clauses) = %d, want 2", len(clauses))
	}
	for i, clause := range clauses {
		knn, ok := clause["knn"].(map[string]any)
		if !ok {
			t.Fatalf("clause[%d] = %#v, want knn map", i, clause)
		}
		if _, ok := knn["message_v2"]; !ok {
			t.Fatalf("clause[%d] knn fields = %#v, want message_v2", i, knn)
		}
		if _, ok := knn["message"]; ok {
			t.Fatalf("clause[%d] knn fields = %#v, want no legacy message field", i, knn)
		}
	}
}

func TestMergeSearchResultsRoundRobinDedupsMessageIDAcrossV2AndRetriever(t *testing.T) {
	got := mergeSearchResults(3,
		[]*SearchResult{
			{MessageID: "v2-1"},
			{MessageID: "same"},
		},
		[]*SearchResult{
			{MessageID: "retriever-1"},
			{MessageID: "same"},
			{MessageID: "retriever-2"},
		},
	)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	want := []string{"v2-1", "retriever-1", "same"}
	for i, item := range got {
		if item.MessageID != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, item.MessageID, want[i])
		}
	}
}

func TestReplaceMentionToNameRestoresMentionAndMarksBotSelf(t *testing.T) {
	useHistorySearchConfigPath(t)
	selfOpenID := botidentity.Current().BotOpenID

	got := ReplaceMentionToName("提醒 <atuser></atuser> 看下", []*Mention{
		{
			Key:  "<atuser></atuser>",
			Name: "旧机器人昵称",
			ID: struct {
				LegacyUserID string `json:"user_id"`
				OpenID       string `json:"open_id"`
				UnionID      string `json:"union_id"`
			}{
				OpenID: selfOpenID,
			},
		},
	})
	if got != "提醒 @你 看下" {
		t.Fatalf("ReplaceMentionToName() = %q, want %q", got, "提醒 @你 看下")
	}
}

func useHistorySearchConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsTermFilter(filters []map[string]any, field, value string) bool {
	for _, filter := range filters {
		term, ok := filter["term"].(map[string]any)
		if !ok {
			continue
		}
		if got, ok := term[field]; ok && got == value {
			return true
		}
	}
	return false
}

func containsRangeFilter(filters []map[string]any, field, operator string) bool {
	for _, filter := range filters {
		ranges, ok := filter["range"].(map[string]any)
		if !ok {
			continue
		}
		fieldRange, ok := ranges[field].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := fieldRange[operator]; ok {
			return true
		}
	}
	return false
}
