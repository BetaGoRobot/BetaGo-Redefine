package ops

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"go.uber.org/zap"
)

type intentContextConfig interface {
	IntentContextReadEnabled() bool
	IntentContextHistoryLimit() int
	IntentContextProfileLimit() int
}

type IntentAnalyzeInputBuildOptions struct {
	ContextEnabled *bool
	HistoryLimit   *int
	ProfileLimit   *int
}

type IntentAnalyzeInputPreview struct {
	Input          string
	ContextEnabled bool
	HistoryLimit   int
	ProfileLimit   int
	HistoryLines   []string
	ProfileLines   []string
}

var (
	intentSearchStatusFn        = opensearch.Status
	intentHistoryLoader         = defaultIntentHistoryLoader
	intentProfileLoader         = defaultIntentProfileLoader
	intentContextConfigAccessor = func(ctx context.Context, chatID, openID string) intentContextConfig {
		return appconfig.NewAccessor(ctx, chatID, openID)
	}
)

func buildIntentAnalyzeInput(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, currentText string) string {
	return BuildIntentAnalyzeInputPreview(ctx, event, meta, currentText, IntentAnalyzeInputBuildOptions{}).Input
}

func BuildIntentAnalyzeInputPreview(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	meta *xhandler.BaseMetaData,
	currentText string,
	options IntentAnalyzeInputBuildOptions,
) IntentAnalyzeInputPreview {
	currentText = strings.TrimSpace(currentText)
	if currentText == "" {
		return IntentAnalyzeInputPreview{}
	}
	preview := IntentAnalyzeInputPreview{Input: currentText}

	chatID := strings.TrimSpace(messageChatID(event, meta))
	openID := strings.TrimSpace(messageOpenID(event, meta))
	if chatID == "" {
		return preview
	}
	cfg := intentContextConfigAccessor(ctx, chatID, openID)
	if cfg != nil {
		preview.ContextEnabled = cfg.IntentContextReadEnabled()
		preview.HistoryLimit = cfg.IntentContextHistoryLimit()
		preview.ProfileLimit = cfg.IntentContextProfileLimit()
	}
	if options.ContextEnabled != nil {
		preview.ContextEnabled = *options.ContextEnabled
	}
	if options.HistoryLimit != nil {
		preview.HistoryLimit = *options.HistoryLimit
	}
	if options.ProfileLimit != nil {
		preview.ProfileLimit = *options.ProfileLimit
	}
	if preview.HistoryLimit < 0 {
		preview.HistoryLimit = 0
	}
	if preview.ProfileLimit < 0 {
		preview.ProfileLimit = 0
	}
	if !preview.ContextEnabled {
		return preview
	}
	if preview.HistoryLimit == 0 && preview.ProfileLimit == 0 {
		return preview
	}

	enabled, _ := intentSearchStatusFn()
	if !enabled {
		return preview
	}

	historyLines, err := intentHistoryLoader(ctx, chatID, currentText, preview.HistoryLimit)
	if err != nil {
		logs.L().Ctx(ctx).Warn("intent context history load failed", zap.Error(err))
		historyLines = nil
	}
	profileLines, err := intentProfileLoader(ctx, chatID, openID, preview.ProfileLimit)
	if err != nil {
		logs.L().Ctx(ctx).Warn("intent profile load failed", zap.Error(err))
		profileLines = nil
	}
	preview.HistoryLines = dedupNonEmpty(historyLines)
	preview.ProfileLines = dedupNonEmpty(profileLines)
	if len(preview.HistoryLines) == 0 && len(preview.ProfileLines) == 0 {
		return preview
	}

	var builder strings.Builder
	builder.WriteString("当前消息:\n")
	builder.WriteString(currentText)
	if len(preview.HistoryLines) > 0 {
		builder.WriteString("\n\n最近上下文(新到旧):\n")
		builder.WriteString(strings.Join(preview.HistoryLines, "\n"))
	}
	if len(preview.ProfileLines) > 0 {
		builder.WriteString("\n\n用户画像线索:\n")
		builder.WriteString(strings.Join(preview.ProfileLines, "\n"))
	}
	preview.Input = builder.String()
	return preview
}

func defaultIntentHistoryLoader(ctx context.Context, chatID, currentText string, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	msgList, err := history.New(ctx).
		Query(osquery.Bool().Must(osquery.Term("chat_id", chatID))).
		Source("raw_message", "mentions", "create_time", "user_id", "chat_id", "user_name", "message_type").
		Size(uint64(limit*2)).
		Sort("create_time", "desc").
		GetMsg()
	if err != nil {
		return nil, err
	}

	lines := make([]string, 0, len(msgList))
	currentText = strings.TrimSpace(currentText)
	for _, line := range msgList.ToLines() {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if currentText != "" && strings.Contains(trimmed, currentText) {
			continue
		}
		lines = append(lines, trimmed)
		if len(lines) >= limit {
			break
		}
	}
	return dedupNonEmpty(lines), nil
}

func defaultIntentProfileLoader(ctx context.Context, chatID, openID string, limit int) ([]string, error) {
	chatID = strings.TrimSpace(chatID)
	openID = strings.TrimSpace(openID)
	if chatID == "" || openID == "" || limit <= 0 {
		return nil, nil
	}

	accessor := appconfig.NewAccessor(ctx, chatID, openID)
	if accessor == nil {
		return nil, nil
	}
	index := strings.TrimSpace(accessor.LarkUserProfileIndex())
	if index == "" {
		return nil, nil
	}

	query := map[string]any{
		"size": limit * 4,
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
								map[string]any{"term": map[string]any{"user_id.keyword": openID}},
								map[string]any{"term": map[string]any{"user_id": openID}},
								map[string]any{"term": map[string]any{"open_id.keyword": openID}},
								map[string]any{"term": map[string]any{"open_id": openID}},
								map[string]any{"term": map[string]any{"target_open_id.keyword": openID}},
								map[string]any{"term": map[string]any{"target_open_id": openID}},
								map[string]any{"term": map[string]any{"chat_user_id.keyword": openID}},
								map[string]any{"term": map[string]any{"chat_user_id": openID}},
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

	resp, err := opensearch.SearchData(ctx, index, query)
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Hits.Hits) == 0 {
		return nil, nil
	}

	lines := make([]string, 0, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		line := intentProfileLineFromSearchHit(hit)
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= limit {
			break
		}
	}
	return dedupNonEmpty(lines), nil
}

func intentProfileLineFromSearchHit(hit opensearchapi.SearchHit) string {
	if len(hit.Source) == 0 {
		return ""
	}
	doc := map[string]any{}
	if err := sonic.Unmarshal(hit.Source, &doc); err != nil {
		return ""
	}
	return intentProfileLineFromDoc(doc)
}

func intentProfileLineFromDoc(doc map[string]any) string {
	if len(doc) == 0 {
		return ""
	}
	state := strings.ToLower(intentFirstString(doc, "state", "status"))
	switch state {
	case "deleted", "inactive", "rejected", "invalid":
		return ""
	}
	facet := intentFirstString(doc, "facet", "category", "type", "label")
	value := intentFirstString(doc, "canonical_value", "value", "summary", "description", "content", "text")
	if value == "" {
		return ""
	}
	confidence := intentFirstFloat(doc, "confidence", "score", "weight")
	if confidence > 0 && confidence < 0.30 {
		return ""
	}
	if facet == "" {
		return "画像线索: " + value
	}
	return fmt.Sprintf("画像线索: %s=%s", facet, value)
}

func intentFirstString(doc map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := doc[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func intentFirstFloat(doc map[string]any, keys ...string) float64 {
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

func dedupNonEmpty(lines []string) []string {
	seen := make(map[string]struct{}, len(lines))
	deduped := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		deduped = append(deduped, trimmed)
	}
	return deduped
}
