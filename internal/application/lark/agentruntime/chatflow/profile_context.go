package chatflow

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/bytedance/sonic"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"go.uber.org/zap"
)

type chatflowProfileContextRequest struct {
	ChatID       string
	TargetOpenID string
	UserRequest  string
	HistoryLines []string
	ContextLines []string
	ReplyScoped  bool
}

type chatflowProfileConfigAccessor interface {
	ChatflowProfileReadEnabled() bool
	ChatflowProfileSnippetLimit() int
}

var (
	chatflowProfileAccessorBuilder = func(ctx context.Context, chatID, openID string) chatflowProfileConfigAccessor {
		return appconfig.NewAccessor(ctx, chatID, openID)
	}
	chatflowProfileContextLoader = defaultChatflowProfileContextLoader
	chatflowProfileSearch        = opensearch.SearchData
	chatflowProfileIndexResolver = func(ctx context.Context, chatID, openID string) string {
		accessor := appconfig.NewAccessor(ctx, chatID, openID)
		if accessor == nil {
			return ""
		}
		return accessor.LarkUserProfileIndex()
	}
)

func resolveChatflowProfileContextLines(ctx context.Context, chatID, openID string, req chatflowProfileContextRequest) []string {
	accessor := chatflowProfileAccessorBuilder(ctx, strings.TrimSpace(chatID), strings.TrimSpace(openID))
	if accessor == nil || !accessor.ChatflowProfileReadEnabled() {
		return nil
	}
	lines, err := chatflowProfileContextLoader(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Warn("chatflow profile context load failed", zap.Error(err))
		return nil
	}
	lines = trimNonEmptyLines(lines)
	if len(lines) == 0 {
		return nil
	}

	limit := accessor.ChatflowProfileSnippetLimit()
	if limit <= 0 {
		limit = 3
	}
	// Thread reply already has high-confidence local context; keep profile injection conservative.
	if req.ReplyScoped && limit > 1 {
		limit = 1
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return lines
}

func defaultChatflowProfileContextLoader(ctx context.Context, req chatflowProfileContextRequest) ([]string, error) {
	chatID := strings.TrimSpace(req.ChatID)
	targetOpenID := strings.TrimSpace(req.TargetOpenID)
	if chatID == "" || targetOpenID == "" {
		return nil, nil
	}

	index := strings.TrimSpace(chatflowProfileIndexResolver(ctx, chatID, targetOpenID))
	if index == "" {
		return nil, nil
	}

	query := map[string]any{
		"size": 20,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{
						"bool": map[string]any{
							"should": []any{
								map[string]any{"term": map[string]any{"chat_id.keyword": chatID}},
								map[string]any{"term": map[string]any{"chat_id": chatID}},
								map[string]any{"term": map[string]any{"scope_chat_id.keyword": chatID}},
								map[string]any{"term": map[string]any{"scope_chat_id": chatID}},
							},
							"minimum_should_match": 1,
						},
					},
					map[string]any{
						"bool": map[string]any{
							"should": []any{
								map[string]any{"term": map[string]any{"user_id.keyword": targetOpenID}},
								map[string]any{"term": map[string]any{"user_id": targetOpenID}},
								map[string]any{"term": map[string]any{"open_id.keyword": targetOpenID}},
								map[string]any{"term": map[string]any{"open_id": targetOpenID}},
								map[string]any{"term": map[string]any{"target_open_id.keyword": targetOpenID}},
								map[string]any{"term": map[string]any{"target_open_id": targetOpenID}},
								map[string]any{"term": map[string]any{"chat_user_id.keyword": targetOpenID}},
								map[string]any{"term": map[string]any{"chat_user_id": targetOpenID}},
							},
							"minimum_should_match": 1,
						},
					},
				},
			},
		},
		"sort": []any{
			map[string]any{"updated_at_unix": map[string]any{"order": "desc"}},
			map[string]any{"updated_at": map[string]any{"order": "desc"}},
			map[string]any{"timestamp_v2": map[string]any{"order": "desc"}},
		},
		"_source": []string{
			"facet",
			"category",
			"type",
			"canonical_value",
			"value",
			"summary",
			"description",
			"content",
			"text",
			"confidence",
			"score",
			"weight",
			"state",
			"status",
		},
	}

	resp, err := chatflowProfileSearch(ctx, index, query)
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Hits.Hits) == 0 {
		return nil, nil
	}

	lines := make([]string, 0, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		docLine := profileContextLineFromSearchHit(hit)
		if strings.TrimSpace(docLine) == "" {
			continue
		}
		lines = append(lines, docLine)
	}
	return trimNonEmptyLines(lines), nil
}

func profileContextLineFromSearchHit(hit opensearchapi.SearchHit) string {
	if len(hit.Source) == 0 {
		return ""
	}
	doc := map[string]any{}
	if err := sonic.Unmarshal(hit.Source, &doc); err != nil {
		return ""
	}
	return profileContextLineFromDoc(doc)
}

func profileContextLineFromDoc(doc map[string]any) string {
	if len(doc) == 0 {
		return ""
	}
	state := strings.ToLower(mapStringFirstNonEmpty(doc, "state", "status"))
	switch state {
	case "deleted", "inactive", "rejected", "invalid":
		return ""
	}

	facet := mapStringFirstNonEmpty(doc, "facet", "category", "type", "label")
	value := mapStringFirstNonEmpty(doc, "canonical_value", "value", "summary", "description", "content", "text")
	if value == "" {
		return ""
	}
	confidence := mapFloatFirstNonZero(doc, "confidence", "score", "weight")
	if confidence > 0 && confidence < 0.30 {
		return ""
	}

	if facet == "" {
		if confidence > 0 {
			return fmt.Sprintf("画像线索: %s (%.2f)", value, confidence)
		}
		return "画像线索: " + value
	}
	if confidence > 0 {
		return fmt.Sprintf("画像线索: %s=%s (%.2f)", facet, value, confidence)
	}
	return fmt.Sprintf("画像线索: %s=%s", facet, value)
}

func mapStringFirstNonEmpty(doc map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := doc[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func mapFloatFirstNonZero(doc map[string]any, keys ...string) float64 {
	for _, key := range keys {
		value, ok := doc[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			if typed != 0 {
				return typed
			}
		case float32:
			if typed != 0 {
				return float64(typed)
			}
		case int:
			if typed != 0 {
				return float64(typed)
			}
		case int64:
			if typed != 0 {
				return float64(typed)
			}
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil && parsed != 0 {
				return parsed
			}
		}
	}
	return 0
}
