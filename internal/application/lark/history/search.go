package history

import (
	"context"
	"fmt"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/bytedance/gg/gresult"
	"github.com/bytedance/sonic"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"github.com/yanyiwu/gojieba"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
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
}

type EmbeddingFunc func(ctx context.Context, text string) (vector []float32, tokenUsage model.Usage, err error)

// HybridSearch 执行混合搜索
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

	queryVecList := make([]map[string]any, 0, len(req.QueryText))
	for _, query := range req.QueryText {
		if strings.TrimSpace(query) == "" {
			continue
		}
		var queryVec []float32
		queryVec, _, err = embeddingFunc(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("获取向量失败: %w", err)
		}

		queryVecList = append(queryVecList, map[string]any{
			"knn": map[string]interface{}{
				"message": map[string]interface{}{
					"vector": queryVec,
					"k":      req.TopK, // KNN 召回 K 个最近邻
					"boost":  2.0,      // 向量分数权重 (示例值，请调优)
				},
			},
		})
	}

	query, err := buildHybridSearchQuery(req, queryTerms, queryVecList, time.Now())
	if err != nil {
		return nil, err
	}
	span.SetAttributes(attribute.Key("query").String(utils.MustMarshalString(query)))
	res, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, req.ChatID, req.OpenID), query)
	if err != nil {
		return nil, fmt.Errorf("搜索请求失败: %w", err)
	}

	resultList := make([]*SearchResult, 0, len(res.Hits.Hits))
	for _, hit := range res.Hits.Hits {
		result := &SearchResult{}
		if err = sonic.Unmarshal(hit.Source, &result); err != nil {
			logs.L().Ctx(ctx).Warn("解析 SearchResult 失败", zap.Error(err), zap.String("source", string(hit.Source)))
			continue
		}
		mentions := make([]*Mention, 0)
		if err = sonic.UnmarshalString(result.Mentions, &mentions); err != nil {
			logs.L().Ctx(ctx).Warn("解析 mentions 失败", zap.Error(err), zap.String("mentions", result.Mentions))
			continue
		}
		normalizeSearchResultActor(result)
		result.RawMessage = ReplaceMentionToName(result.RawMessage, mentions)
		resultList = append(resultList, result)
	}

	return resultList, nil
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
