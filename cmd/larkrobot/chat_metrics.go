package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/interfaces/webui"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
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

// chatActivityHourOfWeek 聚合某群在 since 之后按"周内小时"维度的发言量。
//
// 实现走 OpenSearch date_histogram，按小时桶 + UTC+8 时区聚合，再在客户端
// 把每个时间点映射成 (周几, 小时)。返回数据维度固定 7×24=168 个桶，
// 缺失桶会以 count=0 显式补齐，便于前端直接渲染热力图无需补 0。
//
// dow 取值 0..6，0 表示周一，与前端展示顺序一致。
func chatActivityHourOfWeek(ctx context.Context, chatID string, since time.Time) (*webui.ChatActivity, error) {
	req := map[string]any{
		"size": 0,
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
		"aggs": map[string]any{
			"per_hour": map[string]any{
				"date_histogram": map[string]any{
					"field":          "create_time_v2",
					"fixed_interval": "1h",
					"time_zone":      "+08:00",
					"min_doc_count":  1,
				},
			},
		},
	}
	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, chatID, ""), req)
	if err != nil {
		return nil, err
	}
	// 7×24 网格预初始化，确保前端拿到稳定的 168 桶（无数据则为 0）。
	grid := make([][]int64, 7)
	for i := range grid {
		grid[i] = make([]int64, 24)
	}
	var total int64
	if resp != nil && len(resp.Aggregations) > 0 {
		var aggs struct {
			PerHour struct {
				Buckets []struct {
					KeyAsString string `json:"key_as_string"`
					Key         int64  `json:"key"`
					DocCount    int64  `json:"doc_count"`
				} `json:"buckets"`
			} `json:"per_hour"`
		}
		if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
			return nil, err
		}
		loc := utils.UTC8Loc()
		for _, b := range aggs.PerHour.Buckets {
			// OpenSearch 的 date_histogram 即使设置了 time_zone，bucket key
			// 仍然是 UTC epoch ms；这里用 UTC+8 计算 (周几, 小时)。
			t := time.UnixMilli(b.Key).In(loc)
			dow := int(t.Weekday()) // Sunday=0..Saturday=6
			// 转换为 Monday=0..Sunday=6，与前端列序一致。
			dow = (dow + 6) % 7
			hour := t.Hour()
			grid[dow][hour] += b.DocCount
			total += b.DocCount
		}
	}
	out := make([]webui.HourOfWeekBucket, 0, 7*24)
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			out = append(out, webui.HourOfWeekBucket{
				DayOfWeek: d,
				Hour:      h,
				Count:     grid[d][h],
			})
		}
	}
	return &webui.ChatActivity{
		Total:      total,
		HourOfWeek: out,
	}, nil
}

// chatKeywordsToken 聚合某群在 since 之后的 Top 关键词。
//
// 复用项目内既有 word cloud 范式：先 nested 进 raw_message_jieba_tag，
// 再 filter 词性（实词 + 长度>1 防"的/了"灰尘），最后 terms 取 Top topN。
//
// 返回的 doc_count 是命中文档（消息）数；同一条消息中重复词只计 1 次，
// 这样高频小水群里的一个梗也不会把数据洗到只剩一个词。
func chatKeywordsToken(ctx context.Context, chatID string, since time.Time, topN int) (*webui.ChatKeywords, error) {
	if topN <= 0 {
		topN = 80
	}
	tagsIncluded := []string{
		"n", "nr", "ns", "nt", "nz",
		"v", "vd", "vn",
		"a", "ad", "an",
		"i", "l",
	}
	req := map[string]any{
		"size": 0,
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
		"aggs": map[string]any{
			"jieba_tag": map[string]any{
				"nested": map[string]any{
					"path": "raw_message_jieba_tag",
				},
				"aggs": map[string]any{
					"filtered": map[string]any{
						"filter": map[string]any{
							"bool": map[string]any{
								"must": []map[string]any{
									{"terms": map[string]any{"raw_message_jieba_tag.tag": tagsIncluded}},
									{"script": map[string]any{
										"script": map[string]any{
											"source": "doc['raw_message_jieba_tag.word'].value.length() > 1",
											"lang":   "painless",
										},
									}},
								},
							},
						},
						"aggs": map[string]any{
							"words": map[string]any{
								"terms": map[string]any{
									"field": "raw_message_jieba_tag.word",
									"size":  topN,
								},
							},
						},
					},
				},
			},
		},
	}
	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, chatID, ""), req)
	if err != nil {
		return nil, err
	}
	out := &webui.ChatKeywords{Items: []webui.KeywordCount{}}
	if resp == nil || len(resp.Aggregations) == 0 {
		return out, nil
	}
	var aggs struct {
		JiebaTag struct {
			Filtered struct {
				Words struct {
					Buckets []struct {
						Key      string `json:"key"`
						DocCount int64  `json:"doc_count"`
					} `json:"buckets"`
				} `json:"words"`
			} `json:"filtered"`
		} `json:"jieba_tag"`
	}
	if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
		return nil, err
	}
	for _, b := range aggs.JiebaTag.Filtered.Words.Buckets {
		word := strings.TrimSpace(b.Key)
		if word == "" {
			continue
		}
		out.Items = append(out.Items, webui.KeywordCount{Word: word, Count: b.DocCount})
	}
	return out, nil
}

// chatCommandsTop 聚合某群在 since 之后被调用的 Top 命令分布。
//
// 索引中 main_command 字段在 is_command=true 时由 messages/recording 写入；
// 这里硬过滤 is_command=true，再 terms agg main_command（缺省词条转 unknown）。
// 总数走同一个 query 的 hits.total，避免再发一次 count。
func chatCommandsTop(ctx context.Context, chatID string, since time.Time, topN int) (*webui.ChatCommands, error) {
	if topN <= 0 {
		topN = 20
	}
	req := map[string]any{
		"size":             0,
		"track_total_hits": true,
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"chat_id": chatID}},
					{"term": map[string]any{"is_command": true}},
					{"range": map[string]any{
						"create_time_v2": map[string]any{
							"gte": since.Format(time.RFC3339),
						},
					}},
				},
			},
		},
		"aggs": map[string]any{
			"commands": map[string]any{
				"terms": map[string]any{
					"field":         "main_command",
					"size":          topN,
					"missing":       "unknown",
					"min_doc_count": 1,
				},
			},
		},
	}
	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, chatID, ""), req)
	if err != nil {
		return nil, err
	}
	out := &webui.ChatCommands{Items: []webui.CommandCount{}}
	if resp == nil {
		return out, nil
	}
	out.Total = int64(resp.Hits.Total.Value)
	if len(resp.Aggregations) == 0 {
		return out, nil
	}
	var aggs struct {
		Commands struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int64  `json:"doc_count"`
			} `json:"buckets"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
		return nil, err
	}
	for _, b := range aggs.Commands.Buckets {
		cmd := strings.TrimSpace(b.Key)
		if cmd == "" {
			cmd = "unknown"
		}
		out.Items = append(out.Items, webui.CommandCount{Command: cmd, Count: b.DocCount})
	}
	return out, nil
}
