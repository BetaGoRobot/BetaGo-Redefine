package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/tmc/langchaingo/schema"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
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

func TestBuildHybridSearchFiltersPreservesRFC3339NanoEnd(t *testing.T) {
	const end = "2026-07-29T15:00:00.123456789+08:00"
	filters, err := buildHybridSearchFilters(
		HybridSearchRequest{
			QueryText: []string{"机器人"},
			ChatID:    "oc_test_chat",
			EndTime:   end,
		},
		time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("buildHybridSearchFilters() error = %v", err)
	}
	if got, ok := rangeFilterValue(filters, "create_time_v2", "lte"); !ok || got != end {
		t.Fatalf("end range = %#v, %v; want %q, true", got, ok, end)
	}
}

func TestMessageIndexOnlyPushesScopedFiltersIntoKNNWithoutChangingDefaultShape(t *testing.T) {
	const (
		cutoff = "2026-07-01T00:00:00+08:00"
		end    = "2026-07-29T15:00:00.123+08:00"
	)
	vectorClauses := []map[string]any{
		{
			"knn": map[string]any{
				"message_v2": map[string]any{
					"vector": []float32{0.1, 0.2},
					"k":      10,
					"boost":  2.0,
				},
			},
		},
		{
			"knn": map[string]any{
				"message_v2": map[string]any{
					"vector": []float32{0.3, 0.4},
					"k":      10,
					"boost":  2.0,
				},
			},
		},
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	messageOnly, err := buildHybridSearchQuery(
		HybridSearchRequest{
			QueryText:        []string{"机器人"},
			TopK:             10,
			ChatID:           "oc_test_chat",
			CutoffTime:       cutoff,
			EndTime:          end,
			MessageIndexOnly: true,
		},
		[]string{"机器人"},
		vectorClauses,
		now,
	)
	if err != nil {
		t.Fatalf("message-index-only query error = %v", err)
	}
	allFilters := allKNNMustFilters(messageOnly)
	if len(allFilters) != len(vectorClauses) {
		t.Fatalf("filtered knn clauses = %d, want %d: %#v", len(allFilters), len(vectorClauses), messageOnly)
	}
	for index, knnFilters := range allFilters {
		if !containsTermFilter(knnFilters, "chat_id", "oc_test_chat") {
			t.Fatalf("knn[%d] filters missing chat scope: %#v", index, knnFilters)
		}
		if got, ok := rangeFilterValue(knnFilters, "create_time_v2", "gte"); !ok || got != cutoff {
			t.Fatalf("knn[%d] cutoff = %#v, %v; want %q, true", index, got, ok, cutoff)
		}
		if got, ok := rangeFilterValue(knnFilters, "create_time_v2", "lte"); !ok || got != end {
			t.Fatalf("knn[%d] end = %#v, %v; want %q, true", index, got, ok, end)
		}
	}

	defaultQuery, err := buildHybridSearchQuery(
		HybridSearchRequest{
			QueryText:  []string{"机器人"},
			TopK:       10,
			ChatID:     "oc_test_chat",
			CutoffTime: cutoff,
			EndTime:    end,
		},
		[]string{"机器人"},
		vectorClauses,
		now,
	)
	if err != nil {
		t.Fatalf("default query error = %v", err)
	}
	if _, ok := knnMustFilters(defaultQuery); ok {
		t.Fatalf("default query unexpectedly changed knn shape: %#v", defaultQuery)
	}
}

func TestMessageIndexOnlyUsesExactMillisWithConservativeLegacyFallback(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 123000000, time.FixedZone("UTC+8", 8*60*60))
	req := HybridSearchRequest{
		QueryText:        []string{"机器人"},
		TopK:             10,
		ChatID:           "oc_test_chat",
		EndTime:          anchor.Format(time.RFC3339Nano),
		CausalEndMillis:  anchor.UnixMilli(),
		MessageIndexOnly: true,
	}
	query, err := buildHybridSearchQuery(
		req,
		[]string{"机器人"},
		buildVectorQueryClauses(messageVectorFieldV2, [][]float32{{0.1, 0.2}}, 10),
		anchor.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("buildHybridSearchQuery() error = %v", err)
	}
	if sourceFields, ok := query["_source"].([]string); !ok ||
		!containsString(sourceFields, "create_time_unix_millis") {
		t.Fatalf("_source = %#v, want exact millis field", query["_source"])
	}

	outerFilters := queryMustFilters(t, query)
	assertExactAndLegacyCausalFilter(t, outerFilters, anchor)
	knnFilters, ok := knnMustFilters(query)
	if !ok {
		t.Fatalf("message-index-only query has no knn filter: %#v", query)
	}
	assertExactAndLegacyCausalFilter(t, knnFilters, anchor)
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

func TestHybridSearchMessageIndexOnlySkipsRetrieverAndReturnsMessageResults(t *testing.T) {
	useHistorySearchConfigPath(t)
	oldSearch := hybridSearchMessageIndexFn
	oldRecall := hybridSearchRecallDocsFn
	defer func() {
		hybridSearchMessageIndexFn = oldSearch
		hybridSearchRecallDocsFn = oldRecall
	}()

	want := []*SearchResult{
		{MessageID: "om_1", RawMessage: "before"},
		{MessageID: "om_2", RawMessage: "at anchor"},
	}
	hybridSearchMessageIndexFn = func(context.Context, string, map[string]any) ([]*SearchResult, error) {
		return want, nil
	}
	retrieverCalled := false
	hybridSearchRecallDocsFn = func(context.Context, string, string, int, string, string) ([]schema.Document, error) {
		retrieverCalled = true
		return []schema.Document{{PageContent: "must not merge"}}, nil
	}

	got, err := HybridSearch(
		context.Background(),
		HybridSearchRequest{
			QueryText:        []string{"机器人"},
			TopK:             2,
			ChatID:           "oc_test_chat",
			MessageIndexOnly: true,
		},
		func(context.Context, string) ([]float32, model.Usage, error) {
			return []float32{0.1, 0.2}, model.Usage{}, nil
		},
	)
	if err != nil {
		t.Fatalf("HybridSearch() error = %v", err)
	}
	if retrieverCalled {
		t.Fatal("message-index-only search called retriever")
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("HybridSearch() = %#v, want direct message-index results %#v", got, want)
	}
}

func TestHybridSearchDefaultStillMergesRetrieverResults(t *testing.T) {
	useHistorySearchConfigPath(t)
	oldSearch := hybridSearchMessageIndexFn
	oldRecall := hybridSearchRecallDocsFn
	defer func() {
		hybridSearchMessageIndexFn = oldSearch
		hybridSearchRecallDocsFn = oldRecall
	}()

	hybridSearchMessageIndexFn = func(context.Context, string, map[string]any) ([]*SearchResult, error) {
		return []*SearchResult{{MessageID: "om_index"}}, nil
	}
	hybridSearchRecallDocsFn = func(context.Context, string, string, int, string, string) ([]schema.Document, error) {
		return []schema.Document{{
			PageContent: "retriever",
			Metadata:    map[string]any{"msg_id": "om_retriever"},
		}}, nil
	}

	got, err := HybridSearch(
		context.Background(),
		HybridSearchRequest{
			QueryText: []string{"机器人"},
			TopK:      2,
			ChatID:    "oc_test_chat",
		},
		func(context.Context, string) ([]float32, model.Usage, error) {
			return []float32{0.1, 0.2}, model.Usage{}, nil
		},
	)
	if err != nil {
		t.Fatalf("HybridSearch() error = %v", err)
	}
	if len(got) != 2 || got[0].MessageID != "om_index" || got[1].MessageID != "om_retriever" {
		t.Fatalf("HybridSearch() = %#v, want default merged index/retriever results", got)
	}
}

func TestParseSearchHitsPreservesOpenSearchScore(t *testing.T) {
	got := parseSearchHits(context.Background(), &opensearchapi.SearchResp{
		Hits: opensearchapi.SearchHits{
			Hits: []opensearchapi.SearchHit{{
				Score:  0.875,
				Source: json.RawMessage(`{"message_id":"om_1","raw_message":"hello","mentions":"[]","create_time_unix_millis":1785301200123}`),
			}},
		},
	})
	if len(got) != 1 {
		t.Fatalf("parseSearchHits() len = %d, want 1", len(got))
	}
	if got[0].Score != 0.875 {
		t.Fatalf("score = %v, want 0.875", got[0].Score)
	}
	if got[0].CreateTimeUnixMillis != 1785301200123 {
		t.Fatalf("create_time_unix_millis = %d, want 1785301200123", got[0].CreateTimeUnixMillis)
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
	return slices.Contains(values, target)
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

func rangeFilterValue(filters []map[string]any, field, operator string) (any, bool) {
	for _, filter := range filters {
		ranges, ok := filter["range"].(map[string]any)
		if !ok {
			continue
		}
		fieldRange, ok := ranges[field].(map[string]any)
		if !ok {
			continue
		}
		value, ok := fieldRange[operator]
		if ok {
			return value, true
		}
	}
	return nil, false
}

func knnMustFilters(query map[string]any) ([]map[string]any, bool) {
	all := allKNNMustFilters(query)
	if len(all) == 0 {
		return nil, false
	}
	return all[0], true
}

func allKNNMustFilters(query map[string]any) [][]map[string]any {
	result := make([][]map[string]any, 0)
	boolQuery, ok := query["query"].(map[string]any)["bool"].(map[string]any)
	if !ok {
		return result
	}
	should, ok := boolQuery["should"].([]map[string]any)
	if !ok {
		return result
	}
	for _, clause := range should {
		nested, ok := clause["bool"].(map[string]any)
		if !ok {
			continue
		}
		vectorShould, ok := nested["should"].([]map[string]any)
		if !ok {
			continue
		}
		for _, vectorClause := range vectorShould {
			knn, ok := vectorClause["knn"].(map[string]any)
			if !ok {
				continue
			}
			field, ok := knn[messageVectorFieldV2].(map[string]any)
			if !ok {
				continue
			}
			filter, ok := field["filter"].(map[string]any)
			if !ok {
				continue
			}
			filterBool, ok := filter["bool"].(map[string]any)
			if !ok {
				continue
			}
			must, ok := filterBool["must"].([]map[string]any)
			if ok {
				result = append(result, must)
			}
		}
	}
	return result
}

func queryMustFilters(t *testing.T, query map[string]any) []map[string]any {
	t.Helper()
	boolQuery, ok := query["query"].(map[string]any)["bool"].(map[string]any)
	if !ok {
		t.Fatalf("query bool missing: %#v", query)
	}
	must, ok := boolQuery["must"].([]map[string]any)
	if !ok {
		t.Fatalf("query must missing: %#v", boolQuery)
	}
	return must
}

func assertExactAndLegacyCausalFilter(t *testing.T, filters []map[string]any, anchor time.Time) {
	t.Helper()
	for _, filter := range filters {
		filterBool, ok := filter["bool"].(map[string]any)
		if !ok {
			continue
		}
		should, ok := filterBool["should"].([]map[string]any)
		if !ok || filterBool["minimum_should_match"] != 1 || len(should) != 2 {
			continue
		}
		if got, ok := rangeFilterValue(should, "create_time_unix_millis", "lte"); !ok || got != anchor.UnixMilli() {
			t.Fatalf("exact causal range = %#v, %v; want %d, true", got, ok, anchor.UnixMilli())
		}
		legacyBool, ok := should[1]["bool"].(map[string]any)
		if !ok {
			t.Fatalf("legacy causal branch = %#v, want bool", should[1])
		}
		mustNot, ok := legacyBool["must_not"].([]map[string]any)
		if !ok || !containsExistsFilter(mustNot, "create_time_unix_millis") {
			t.Fatalf("legacy branch must_not = %#v, want missing exact millis", legacyBool["must_not"])
		}
		must, ok := legacyBool["must"].([]map[string]any)
		if !ok {
			t.Fatalf("legacy branch must = %#v", legacyBool["must"])
		}
		wantSecond := anchor.Truncate(time.Second).Format(time.RFC3339)
		if got, ok := rangeFilterValue(must, "create_time_v2", "lt"); !ok || got != wantSecond {
			t.Fatalf("legacy causal range = %#v, %v; want %q, true", got, ok, wantSecond)
		}
		return
	}
	t.Fatalf("causal fallback filter missing: %#v", filters)
}

func containsExistsFilter(filters []map[string]any, field string) bool {
	for _, filter := range filters {
		exists, ok := filter["exists"].(map[string]any)
		if ok && exists["field"] == field {
			return true
		}
	}
	return false
}
