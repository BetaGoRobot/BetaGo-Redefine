package main

import (
	"context"
	"encoding/json"
	"strings"
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

// recentChatIDs 聚合当前 bot 在 since 之后产生过消息的全部 chat_id。
//
// lark_msg_index 是进程级配置，每个 bot 进程独占一个 index，所以这里聚合到的
// chat_id 集合天然限定在「当前 bot」范围内。WebUI 用它做 mergeMissingChats
// 的白名单，避免共享 token 表把另一个 bot 的会话也吐进列表。
//
// 桶上限 10000 与现有 chat 规模匹配；若日后接近上限再考虑 composite 分页。
func recentChatIDs(ctx context.Context, since time.Time) (map[string]struct{}, error) {
	req := map[string]any{
		"size": 0,
		"query": map[string]any{
			"range": map[string]any{
				"create_time_v2": map[string]any{
					"gte": since.Format(time.RFC3339),
				},
			},
		},
		"aggs": map[string]any{
			"chat_ids": map[string]any{
				"terms": map[string]any{
					"field": "chat_id",
					"size":  10000,
				},
			},
		},
	}
	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, "", ""), req)
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Aggregations) == 0 {
		return map[string]struct{}{}, nil
	}
	var aggs struct {
		ChatIDs struct {
			Buckets []struct {
				Key string `json:"key"`
			} `json:"buckets"`
		} `json:"chat_ids"`
	}
	if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(aggs.ChatIDs.Buckets))
	for _, b := range aggs.ChatIDs.Buckets {
		id := strings.TrimSpace(b.Key)
		if id == "" {
			continue
		}
		out[id] = struct{}{}
	}
	return out, nil
}
