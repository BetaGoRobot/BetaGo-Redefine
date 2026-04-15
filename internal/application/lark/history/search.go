package history

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"

	"github.com/bytedance/gg/gresult"
	"github.com/bytedance/sonic"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/tmc/langchaingo/schema"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"github.com/yanyiwu/gojieba"
	"go.uber.org/zap"
)

const (
	messageVectorFieldV2 = "message_v2"
	vectorDimensionV2    = 2048
)

// SearchResult 是我们最终返回给 LLM 的标准结果格式
type SearchResult struct {
	MessageID    string  `json:"message_id"`
	OpenID       string  `json:"user_id"`
	RawMessage   string  `json:"raw_message"`
	UserName     string  `json:"user_name"`
	ChatName     string  `json:"chat_name"`
	CreateTime   string  `json:"create_time"`
	CreateTimeV2 string  `json:"create_time_v2"`
	Mentions     string  `json:"mentions"`
	Score        float64 `json:"score"`
}

// HybridSearchRequest 定义了搜索的输入参数
type HybridSearchRequest struct {
	QueryText   []string `json:"query"`
	TopK        int      `json:"top_k"`
	OpenID      string   `json:"user_id,omitempty"`
	UserName    string   `json:"user_name,omitempty"`
	ChatID      string   `json:"chat_id,omitempty"`
	MessageType string   `json:"message_type,omitempty"`
	StartTime   string   `json:"start_time,omitempty"`
	EndTime     string   `json:"end_time,omitempty"`
	CutoffTime  string   `json:"cutoff_time,omitempty"` // RFC3339, messages before this time are excluded
}

type EmbeddingFunc func(ctx context.Context, text string) (vector []float32, tokenUsage model.Usage, err error)

// HybridSearch 执行混合搜索，同时查询 OpenSearch message_v2 和 Retriever contentVector 两个路径。
func HybridSearch(ctx context.Context, req HybridSearchRequest, embeddingFunc EmbeddingFunc) (searchResults []*SearchResult, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	logs.L().Ctx(ctx).Info("开始混合搜索", zap.String("req", utils.MustMarshalString(req)))
	if req.TopK <= 0 {
		req.TopK = 5
	}

	queryTerms := make([]string, 0)
	jieba := gojieba.NewJieba()
	defer jieba.Free()
	for _, query := range req.QueryText {
		if trimmed := strings.TrimSpace(query); trimmed != "" {
			queryTerms = append(queryTerms, jieba.Cut(trimmed, true)...)
		}
	}

	// 获取原始向量，并为 message_v2 / langchaingo 截断到 2048 维查询。
	queryVectors := make([][]float32, 0, len(req.QueryText))
	for _, query := range req.QueryText {
		if strings.TrimSpace(query) == "" {
			continue
		}
		var queryVec []float32
		queryVec, _, err = embeddingFunc(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("获取向量失败: %w", err)
		}
		queryVectors = append(queryVectors, queryVec)
	}

	queryVecListV2 := buildVectorQueryClauses(messageVectorFieldV2, truncateToDim(queryVectors, vectorDimensionV2), req.TopK)
	queryV2, err := buildHybridSearchQuery(req, queryTerms, queryVecListV2, time.Now())
	if err != nil {
		return nil, err
	}

	searchIndex := appconfig.GetLarkMsgIndex(ctx, req.ChatID, req.OpenID)
	queryText := strings.Join(req.QueryText, " ")

	var (
		osV2Results []*SearchResult
		retResults  []*SearchResult
		osV2Err     error
		retErr      error
		wg          sync.WaitGroup
	)

	// goroutine 1: OpenSearch message_v2 字段 (2048-dim)
	wg.Go(func() {
		res, err := opensearch.SearchData(ctx, searchIndex, queryV2)
		if err != nil {
			osV2Err = fmt.Errorf("message_v2 搜索失败: %w", err)
			return
		}
		osV2Results = parseSearchHits(ctx, res)
	})

	if len(queryText) > 0 {
		wg.Go(func() {
			docs, err := retriever.Cli().RecallDocs(ctx, req.ChatID, queryText, req.TopK)
			if err != nil {
				retErr = fmt.Errorf("retriever 召回失败: %w", err)
				return
			}
			retResults = make([]*SearchResult, 0, len(docs))
			for _, doc := range docs {
				retResults = append(retResults, documentToSearchResult(doc))
			}
		})
	}
	// goroutine 2: langchaingo (2048-dim, 截断在 CreateEmbedding 内部完成)

	wg.Wait()

	if osV2Err != nil {
		logs.L().Ctx(ctx).Warn("OpenSearch message_v2 查询失败", zap.Error(osV2Err))
	}
	if retErr != nil {
		logs.L().Ctx(ctx).Warn("Retriever 查询失败", zap.Error(retErr))
	}

	// 全部失败
	if osV2Err != nil && retErr != nil {
		return nil, fmt.Errorf("两路搜索全部失败: v2=%w, retriever=%w", osV2Err, retErr)
	}

	// 合并两路结果
	allResults := make([]*SearchResult, 0, len(osV2Results)+len(retResults))
	allResults = append(allResults, osV2Results...)
	allResults = append(allResults, retResults...)
	if len(allResults) == 0 {
		return nil, nil
	}
	return mergeSearchResults(req.TopK, osV2Results, retResults), nil
}

// truncateToDim 将向量截断到指定维度
func truncateToDim(vectors [][]float32, dim int) [][]float32 {
	result := make([][]float32, len(vectors))
	for i, v := range vectors {
		if len(v) > dim {
			result[i] = v[:dim]
		} else {
			result[i] = v
		}
	}
	return result
}

// parseSearchHits 解析 OpenSearch 搜索结果为 SearchResult 列表
func parseSearchHits(ctx context.Context, res *opensearchapi.SearchResp) []*SearchResult {
	results := make([]*SearchResult, 0, len(res.Hits.Hits))
	for _, hit := range res.Hits.Hits {
		result := &SearchResult{}
		if err := sonic.Unmarshal(hit.Source, &result); err != nil {
			logs.L().Ctx(ctx).Warn("解析 SearchResult 失败", zap.Error(err), zap.String("source", string(hit.Source)))
			continue
		}
		mentions := make([]*Mention, 0)
		if err := sonic.UnmarshalString(result.Mentions, &mentions); err != nil {
			logs.L().Ctx(ctx).Warn("解析 mentions 失败", zap.Error(err), zap.String("mentions", result.Mentions))
			continue
		}
		normalizeSearchResultActor(result)
		result.RawMessage = ReplaceMentionToName(result.RawMessage, mentions)
		results = append(results, result)
	}
	return results
}

func buildVectorQueryClauses(field string, queryVectors [][]float32, topK int) []map[string]any {
	queryVecList := make([]map[string]any, 0, len(queryVectors))
	for _, queryVec := range queryVectors {
		queryVecList = append(queryVecList, map[string]any{
			"knn": map[string]any{
				field: map[string]any{
					"vector": queryVec,
					"k":      topK,
					"boost":  2.0,
				},
			},
		})
	}
	return queryVecList
}

func parseTimeFormat(s, fmt string) gresult.R[time.Time] {
	return gresult.Of(time.Parse(fmt, s))
}

func buildHybridSearchQuery(req HybridSearchRequest, queryTerms []string, queryVecList []map[string]any, now time.Time) (map[string]any, error) {
	filters, err := buildHybridSearchFilters(req, now)
	if err != nil {
		return nil, err
	}

	shouldClauses := make([]map[string]any, 0, 2)
	if len(queryTerms) > 0 {
		shouldClauses = append(shouldClauses, map[string]any{
			"terms": map[string]any{"raw_message_jieba_array": queryTerms},
		})
	}
	if len(queryVecList) > 0 {
		shouldClauses = append(shouldClauses, map[string]any{
			"bool": map[string]any{"should": queryVecList},
		})
	}

	boolQuery := map[string]any{
		"must": filters,
	}
	if len(shouldClauses) > 0 {
		boolQuery["should"] = shouldClauses
		boolQuery["minimum_should_match"] = 1
	}

	return map[string]any{
		"size": req.TopK,
		"_source": []string{
			"message_id",
			"user_id",
			"raw_message",
			"user_name",
			"chat_name",
			"create_time",
			"create_time_v2",
			"mentions",
		},
		"query": map[string]any{
			"bool": boolQuery,
		},
	}, nil
}

func buildHybridSearchFilters(req HybridSearchRequest, now time.Time) ([]map[string]any, error) {
	chatID := strings.TrimSpace(req.ChatID)
	if chatID == "" {
		return nil, fmt.Errorf("chat_id is required for scoped history search")
	}
	if !hasHybridSearchSelector(req) {
		return nil, fmt.Errorf("at least one history selector is required")
	}

	filters := []map[string]any{
		{"term": map[string]any{"chat_id": chatID}},
	}
	if req.OpenID != "" {
		openID := strings.TrimSpace(req.OpenID)
		currentBot := botidentity.Current()
		if currentBot.BotOpenID != "" && openID == currentBot.BotOpenID {
			filters = append(filters, map[string]any{"terms": map[string]any{"user_id": []string{openID, "你"}}})
		} else {
			filters = append(filters, map[string]any{"term": map[string]any{"user_id": openID}})
		}
	}
	if trimmed := strings.TrimSpace(req.UserName); trimmed != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"user_name": trimmed}})
	}
	if trimmed := strings.TrimSpace(req.MessageType); trimmed != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"message_type": trimmed}})
	}

	if req.StartTime != "" {
		if parseStartTime := parseTimeFormat(req.StartTime, time.DateTime); !parseStartTime.IsErr() {
			filters = append(filters, map[string]any{"range": map[string]any{"create_time_v2": map[string]any{"gte": parseStartTime.Value().Format(time.RFC3339)}}})
		} else {
			filters = append(filters, map[string]any{"range": map[string]any{"create_time_v2": map[string]any{"gte": now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)}}})
		}
	}
	if req.EndTime != "" {
		if parseEndTime := parseTimeFormat(req.EndTime, time.DateTime); !parseEndTime.IsErr() {
			filters = append(filters, map[string]any{"range": map[string]any{"create_time_v2": map[string]any{"lte": parseEndTime.Value().Format(time.RFC3339)}}})
		} else {
			filters = append(filters, map[string]any{"range": map[string]any{"create_time_v2": map[string]any{"lte": now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)}}})
		}
	}
	// Apply cutoff time if set (history cutoff - messages before this time are excluded)
	if req.CutoffTime != "" {
		if parseCutoffTime := parseTimeFormat(req.CutoffTime, time.RFC3339); !parseCutoffTime.IsErr() {
			filters = append(filters, map[string]any{"range": map[string]any{"create_time_v2": map[string]any{"gte": parseCutoffTime.Value().Format(time.RFC3339)}}})
		} else if parseCutoffTimeAlt := parseTimeFormat(req.CutoffTime, time.DateTime); !parseCutoffTimeAlt.IsErr() {
			filters = append(filters, map[string]any{"range": map[string]any{"create_time_v2": map[string]any{"gte": parseCutoffTimeAlt.Value().Format(time.RFC3339)}}})
		}
	}
	return filters, nil
}

func hasHybridSearchSelector(req HybridSearchRequest) bool {
	for _, query := range req.QueryText {
		if strings.TrimSpace(query) != "" {
			return true
		}
	}
	return strings.TrimSpace(req.OpenID) != "" ||
		strings.TrimSpace(req.UserName) != "" ||
		strings.TrimSpace(req.MessageType) != "" ||
		strings.TrimSpace(req.StartTime) != "" ||
		strings.TrimSpace(req.EndTime) != ""
}

func normalizeSearchResultActor(result *SearchResult) {
	if result == nil {
		return
	}
	currentBot := botidentity.Current()
	if currentBot.BotOpenID == "" {
		return
	}
	if strings.TrimSpace(result.OpenID) == "你" {
		result.OpenID = currentBot.BotOpenID
	}
	if strings.TrimSpace(result.OpenID) == currentBot.BotOpenID {
		result.UserName = "你"
	}
}

// mergeSearchResults merges multiple result slices using round-robin interleaving,
// deduplicating by MessageID. Sources are ordered by recall priority.
func mergeSearchResults(topK int, sources ...[]*SearchResult) []*SearchResult {
	seen := make(map[string]bool)
	result := make([]*SearchResult, 0, topK)

	if len(sources) == 0 {
		return result
	}

	// pointers[i] tracks position in sources[i]
	pointers := make([]int, len(sources))
	active := true // true = advance to next source, false = try same source

	for len(result) < topK && active {
		active = false
		for srcIdx := range sources {
			if pointers[srcIdx] >= len(sources[srcIdx]) {
				continue
			}
			active = true
			item := sources[srcIdx][pointers[srcIdx]]
			pointers[srcIdx]++
			if item == nil || seen[item.MessageID] {
				continue
			}
			seen[item.MessageID] = true
			result = append(result, item)
			if len(result) >= topK {
				return result
			}
		}
	}

	return result
}

// documentToSearchResult converts a langchaingo schema.Document to a SearchResult.
func documentToSearchResult(doc schema.Document) *SearchResult {
	chatID, _ := doc.Metadata["chat_id"].(string)
	userID, _ := doc.Metadata["user_id"].(string)
	msgID, _ := doc.Metadata["msg_id"].(string)
	userName, _ := doc.Metadata["user_name"].(string)
	createTime, _ := doc.Metadata["create_time"].(string)

	return &SearchResult{
		MessageID:  msgID,
		OpenID:     userID,
		UserName:   userName,
		ChatName:   chatID,
		RawMessage: doc.PageContent,
		CreateTime: createTime,
	}
}
