package main

import (
	"context"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
)

func countRecentChatMessages(ctx context.Context, chatID string, since time.Time) (int, error) {
	req := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"chat_id": chatID}},
					{"range": map[string]any{
						"create_time_v2": map[string]any{
							"gte": since.Format(time.RFC3339),
						},
					}},
				},
			},
		},
		"size":             0,
		"track_total_hits": true,
	}
	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, chatID, ""), req)
	if err != nil {
		return 0, err
	}
	if resp == nil {
		return 0, nil
	}
	return resp.Hits.Total.Value, nil
}
