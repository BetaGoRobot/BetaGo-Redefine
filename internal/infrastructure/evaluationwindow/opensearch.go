package evaluationwindow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

type OpenSearchPreWindowSource struct{}

var _ conversationeval.PreWindowSource = OpenSearchPreWindowSource{}

var searchMessages = func(
	ctx context.Context,
	index string,
	query map[string]any,
) (*opensearchapi.SearchResp, error) {
	return opensearch.SearchData(ctx, index, query)
}

func (OpenSearchPreWindowSource) MessagesBefore(
	ctx context.Context,
	chatID string,
	anchorAt time.Time,
	limit int,
) ([]conversationeval.WindowMessage, error) {
	if strings.TrimSpace(chatID) == "" || anchorAt.IsZero() || limit <= 0 {
		return nil, fmt.Errorf(
			"%w: pre-window query requires chat_id, anchor_at, and positive limit",
			conversationeval.ErrInvalidContract,
		)
	}
	query := preWindowQuery(chatID, anchorAt, limit)
	index := appconfig.GetLarkMsgIndex(ctx, chatID, "")
	response, err := searchMessages(ctx, index, query)
	if err != nil {
		return nil, fmt.Errorf("search evaluation pre-window: %w", err)
	}
	messages := make([]conversationeval.WindowMessage, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		var document xmodel.MessageIndex
		if err := json.Unmarshal(hit.Source, &document); err != nil {
			continue
		}
		occurredAt, ok := messageOccurredAt(document)
		if !ok || !occurredAt.Before(anchorAt) || document.MessageID == "" {
			continue
		}
		messages = append(messages, conversationeval.WindowMessage{
			EventID: document.MessageID, MessageID: document.MessageID,
			ChatID: document.ChatID, TopicID: document.ThreadID,
			SenderOpenID: document.OpenID, ReplyToMessageID: document.ParentID,
			Content: document.RawMessage, OccurredAt: occurredAt,
			Position: conversationeval.WindowPositionPre,
		})
	}
	sort.SliceStable(messages, func(i, j int) bool {
		if messages[i].OccurredAt.Equal(messages[j].OccurredAt) {
			return messages[i].EventID < messages[j].EventID
		}
		return messages[i].OccurredAt.Before(messages[j].OccurredAt)
	})
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	for index := range messages {
		messages[index].Sequence = index
	}
	return messages, nil
}

func preWindowQuery(chatID string, anchorAt time.Time, limit int) map[string]any {
	legacyCutoff := anchorAt.Truncate(time.Second).Add(-time.Nanosecond)
	return map[string]any{
		"size": limit,
		"_source": []string{
			"message_id", "chat_id", "thread_id", "parent_id", "user_id",
			"raw_message", "create_time_v2", "create_time_unix_millis",
		},
		"sort": []any{
			map[string]any{"create_time_v2": map[string]any{"order": "desc"}},
			map[string]any{"message_id.keyword": map[string]any{"order": "desc"}},
		},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"chat_id": chatID}},
					map[string]any{"bool": map[string]any{
						"minimum_should_match": 1,
						"should": []any{
							map[string]any{"range": map[string]any{
								"create_time_unix_millis": map[string]any{"lt": anchorAt.UnixMilli()},
							}},
							map[string]any{"bool": map[string]any{
								"must_not": []any{
									map[string]any{"exists": map[string]any{"field": "create_time_unix_millis"}},
								},
								"filter": []any{
									map[string]any{"range": map[string]any{
										"create_time_v2": map[string]any{"lte": legacyCutoff.Format(time.RFC3339Nano)},
									}},
								},
							}},
						},
					}},
				},
			},
		},
	}
}

func messageOccurredAt(document xmodel.MessageIndex) (time.Time, bool) {
	if document.CreateTimeUnixMillis > 0 {
		return time.UnixMilli(document.CreateTimeUnixMillis), true
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		if parsed, err := time.ParseInLocation(layout, document.CreateTimeV2, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
