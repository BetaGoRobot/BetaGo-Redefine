package main

import (
	"context"
	"encoding/json"
	"sort"
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
// 索引中 main_command 字段在 is_command=true 时由 messages/recording 写入。
// 新索引优先走 main_command.keyword terms agg；老索引若没有 keyword 子字段，
// 则回退到搜索命令消息样本并在 Go 侧按 _source.main_command 计数，避免要求
// 在线上索引上开启 text fielddata。
func chatCommandsTop(ctx context.Context, chatID string, since time.Time, topN int) (*webui.ChatCommands, error) {
	if topN <= 0 {
		topN = 20
	}
	baseQuery := map[string]any{
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
	}
	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, chatID, ""),
		withCommandTermsAgg(baseQuery, "main_command.keyword", topN))
	if err != nil {
		if !isCommandAggFieldError(err) {
			return nil, err
		}
		return chatCommandsTopFromSource(ctx, chatID, since, topN)
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

func withCommandTermsAgg(baseQuery map[string]any, field string, topN int) map[string]any {
	req := make(map[string]any, len(baseQuery)+1)
	for k, v := range baseQuery {
		req[k] = v
	}
	req["aggs"] = map[string]any{
		"commands": map[string]any{
			"terms": map[string]any{
				"field":         field,
				"size":          topN,
				"missing":       "unknown",
				"min_doc_count": 1,
			},
		},
	}
	return req
}

func chatCommandsTopFromSource(ctx context.Context, chatID string, since time.Time, topN int) (*webui.ChatCommands, error) {
	req := map[string]any{
		"size":             1000,
		"track_total_hits": true,
		"_source":          []string{"main_command"},
		"sort": []map[string]any{
			{"create_time_v2": map[string]any{"order": "desc"}},
		},
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
	if len(resp.Hits.Hits) == 0 {
		return out, nil
	}
	type hitSource struct {
		MainCommand string `json:"main_command"`
	}
	counts := make(map[string]int64)
	for _, hit := range resp.Hits.Hits {
		var src hitSource
		if err := json.Unmarshal(hit.Source, &src); err != nil {
			continue
		}
		cmd := strings.TrimSpace(src.MainCommand)
		if cmd == "" {
			cmd = "unknown"
		}
		counts[cmd]++
	}
	items := make([]webui.CommandCount, 0, len(counts))
	for cmd, count := range counts {
		items = append(items, webui.CommandCount{Command: cmd, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Command < items[j].Command
	})
	if len(items) > topN {
		items = items[:topN]
	}
	out.Items = items
	return out, nil
}

func isCommandAggFieldError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "main_command") &&
		(strings.Contains(msg, "fielddata") ||
			strings.Contains(msg, "Text fields are not optimised") ||
			strings.Contains(msg, "keyword"))
}

// chatTopSenders 聚合某群在 since 之后按发言数排序的 Top 用户。
//
// 走 OpenSearch terms agg user_id（keyword），桶内子聚合 top_hits(size=1) 取一条
// 样本，从中读 user_name 用于展示；user_name 缺失时回退 OpenID。Total 用 hits.total
// 直接拿，避免再发一次 count。
func chatTopSenders(ctx context.Context, chatID string, since time.Time, topN int) (*webui.ChatTopSenders, error) {
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
					{"range": map[string]any{
						"create_time_v2": map[string]any{
							"gte": since.Format(time.RFC3339),
						},
					}},
				},
			},
		},
		"aggs": map[string]any{
			"senders": map[string]any{
				"terms": map[string]any{
					"field":         "user_id",
					"size":          topN,
					"min_doc_count": 1,
				},
				"aggs": map[string]any{
					"sample": map[string]any{
						"top_hits": map[string]any{
							"size":    1,
							"_source": map[string]any{"includes": []string{"user_name"}},
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
	out := &webui.ChatTopSenders{Items: []webui.SenderRank{}}
	if resp == nil {
		return out, nil
	}
	out.Total = int64(resp.Hits.Total.Value)
	if len(resp.Aggregations) == 0 {
		return out, nil
	}
	var aggs struct {
		Senders struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int64  `json:"doc_count"`
				Sample   struct {
					Hits struct {
						Hits []struct {
							Source struct {
								UserName string `json:"user_name"`
							} `json:"_source"`
						} `json:"hits"`
					} `json:"hits"`
				} `json:"sample"`
			} `json:"buckets"`
		} `json:"senders"`
	}
	if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
		return nil, err
	}
	for _, b := range aggs.Senders.Buckets {
		openID := strings.TrimSpace(b.Key)
		if openID == "" {
			continue
		}
		userName := ""
		if len(b.Sample.Hits.Hits) > 0 {
			userName = strings.TrimSpace(b.Sample.Hits.Hits[0].Source.UserName)
		}
		if userName == "" {
			userName = openID
		}
		out.Items = append(out.Items, webui.SenderRank{
			OpenID:   openID,
			UserName: userName,
			Count:    b.DocCount,
		})
	}
	return out, nil
}

// chatMessageKinds 聚合某群在 since 之后按 message_type 维度的消息数分布。
//
// message_type 在 OpenSearch 索引里是 keyword（仓库内有现成的 term 过滤先例），
// 直接 terms agg；空字符串桶折叠成 "unknown"。Total 走 hits.total，避免再发一次
// count，只用一次请求。
func chatMessageKinds(ctx context.Context, chatID string, since time.Time) (*webui.ChatMessageKinds, error) {
	req := map[string]any{
		"size":             0,
		"track_total_hits": true,
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
			"kinds": map[string]any{
				"terms": map[string]any{
					"field":         "message_type",
					"size":          50,
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
	out := &webui.ChatMessageKinds{Items: []webui.MessageKindCount{}}
	if resp == nil {
		return out, nil
	}
	out.Total = int64(resp.Hits.Total.Value)
	if len(resp.Aggregations) == 0 {
		return out, nil
	}
	var aggs struct {
		Kinds struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int64  `json:"doc_count"`
			} `json:"buckets"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
		return nil, err
	}
	for _, b := range aggs.Kinds.Buckets {
		kind := strings.TrimSpace(b.Key)
		if kind == "" {
			kind = "unknown"
		}
		out.Items = append(out.Items, webui.MessageKindCount{Kind: kind, Count: b.DocCount})
	}
	return out, nil
}

// chatCommandTrend 聚合某群在 since 之后按"日"维度的总消息数与命令调用数。
//
// 走 OpenSearch date_histogram(1d, UTC+8) 拿总数，再嵌套 filter agg(is_command=true)
// 拿命令数，单次请求即可。bucket key 是 UTC epoch ms，按 UTC+8 折成 YYYY-MM-DD。
//
// 注意 min_doc_count: 0 不开：空白日子省掉，前端按返回数组顺序连点画线即可。
func chatCommandTrend(ctx context.Context, chatID string, since time.Time) (*webui.ChatCommandTrend, error) {
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
			"per_day": map[string]any{
				"date_histogram": map[string]any{
					"field":          "create_time_v2",
					"calendar_interval": "1d",
					"time_zone":      "+08:00",
					"min_doc_count":  1,
				},
				"aggs": map[string]any{
					"commands": map[string]any{
						"filter": map[string]any{
							"term": map[string]any{"is_command": true},
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
	out := &webui.ChatCommandTrend{Days: []string{}, Total: []int64{}, Commands: []int64{}}
	if resp == nil || len(resp.Aggregations) == 0 {
		return out, nil
	}
	var aggs struct {
		PerDay struct {
			Buckets []struct {
				Key      int64 `json:"key"`
				DocCount int64 `json:"doc_count"`
				Commands struct {
					DocCount int64 `json:"doc_count"`
				} `json:"commands"`
			} `json:"buckets"`
		} `json:"per_day"`
	}
	if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
		return nil, err
	}
	loc := utils.UTC8Loc()
	for _, b := range aggs.PerDay.Buckets {
		day := time.UnixMilli(b.Key).In(loc).Format("2006-01-02")
		out.Days = append(out.Days, day)
		out.Total = append(out.Total, b.DocCount)
		out.Commands = append(out.Commands, b.Commands.DocCount)
	}
	return out, nil
}

// chatTopMentions 取样 + 客户端聚合"被 @ 最多的用户"。
//
// mentions 字段是 JSON 字符串塞在 text 列里，OpenSearch 无法直接 agg；
// 这里 search 拉一批 mentions 非空消息（exists + 长度 > 2 简单过滤"[]"/""），
// 解析每条 JSON 再按 open_id 聚合。同一条消息内重复 @ 同一人按多次计入。
//
// truncated = 命中文档总数 > sampleSize 时为 true，前端可提示"样本截断"。
func chatTopMentions(ctx context.Context, chatID string, since time.Time, sampleSize int, topN int) (*webui.ChatTopMentions, error) {
	if sampleSize <= 0 {
		sampleSize = 500
	}
	if topN <= 0 {
		topN = 20
	}
	req := map[string]any{
		"size":             sampleSize,
		"track_total_hits": true,
		"_source":          map[string]any{"includes": []string{"mentions"}},
		"sort": []map[string]any{
			{"create_time_v2": map[string]any{"order": "desc"}},
		},
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"chat_id": chatID}},
					{"range": map[string]any{
						"create_time_v2": map[string]any{
							"gte": since.Format(time.RFC3339),
						},
					}},
					{"exists": map[string]any{"field": "mentions"}},
				},
				// 过滤掉空 JSON ("" / "[]" / "null") 的消息，避免占样本份额。
				"must_not": []map[string]any{
					{"terms": map[string]any{"mentions.keyword": []string{"", "[]", "null"}}},
				},
			},
		},
	}
	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, chatID, ""), req)
	if err != nil {
		return nil, err
	}
	out := &webui.ChatTopMentions{Items: []webui.MentionRank{}}
	if resp == nil {
		return out, nil
	}
	type mentionEntry struct {
		Key string `json:"key"`
		ID  struct {
			OpenID string `json:"open_id"`
		} `json:"id"`
		Name string `json:"name"`
	}
	type hitSource struct {
		Mentions string `json:"mentions"`
	}
	counts := make(map[string]int64)
	names := make(map[string]string)
	out.Sampled = int64(len(resp.Hits.Hits))
	out.Truncated = int64(resp.Hits.Total.Value) > out.Sampled
	for _, hit := range resp.Hits.Hits {
		var src hitSource
		if err := json.Unmarshal(hit.Source, &src); err != nil {
			continue
		}
		raw := strings.TrimSpace(src.Mentions)
		if raw == "" || raw == "[]" || raw == "null" {
			continue
		}
		var entries []mentionEntry
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			continue
		}
		for _, m := range entries {
			openID := strings.TrimSpace(m.ID.OpenID)
			if openID == "" {
				continue
			}
			counts[openID]++
			if names[openID] == "" {
				if name := strings.TrimSpace(m.Name); name != "" {
					names[openID] = name
				}
			}
		}
	}
	ranks := make([]webui.MentionRank, 0, len(counts))
	for openID, c := range counts {
		name := names[openID]
		if name == "" {
			name = openID
		}
		ranks = append(ranks, webui.MentionRank{OpenID: openID, UserName: name, Count: c})
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].Count != ranks[j].Count {
			return ranks[i].Count > ranks[j].Count
		}
		return ranks[i].OpenID < ranks[j].OpenID
	})
	if len(ranks) > topN {
		ranks = ranks[:topN]
	}
	out.Items = ranks
	return out, nil
}

// chatTopicTrend 聚合"按天 × 词性大类"的词频趋势。
//
// OpenSearch 嵌套结构：
//   1. date_histogram(1d, +08:00) 按天分桶；
//   2. nested 进 jieba_tag；
//   3. filter 词长 > 1（剔除"的/了/是"灰尘）；
//   4. filters 把细分词性折叠成 4 大类：名词 / 动词 / 形容词 / 其它实词。
//
// 桶值为 nested 命中数（同一条消息里同词重复算多次），符合"词性活跃度"语义。
// 返回结构与前端 stack area 直接对齐：days[] 与每个 series.values[] 同长。
func chatTopicTrend(ctx context.Context, chatID string, since time.Time) (*webui.ChatTopicTrend, error) {
	type tagGroup struct {
		Key  string
		Tags []string
	}
	groups := []tagGroup{
		{Key: "noun", Tags: []string{"n", "nr", "ns", "nt", "nz"}},
		{Key: "verb", Tags: []string{"v", "vd", "vn"}},
		{Key: "adj", Tags: []string{"a", "ad", "an"}},
		{Key: "other", Tags: []string{"i", "l"}},
	}
	groupLabels := map[string]string{
		"noun":  "名词",
		"verb":  "动词",
		"adj":   "形容词",
		"other": "其它实词",
	}
	filtersMap := make(map[string]any, len(groups))
	for _, g := range groups {
		filtersMap[g.Key] = map[string]any{
			"terms": map[string]any{"raw_message_jieba_tag.tag": g.Tags},
		}
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
			"per_day": map[string]any{
				"date_histogram": map[string]any{
					"field":             "create_time_v2",
					"calendar_interval": "1d",
					"time_zone":         "+08:00",
					"min_doc_count":     1,
				},
				"aggs": map[string]any{
					"jieba": map[string]any{
						"nested": map[string]any{
							"path": "raw_message_jieba_tag",
						},
						"aggs": map[string]any{
							"long_word": map[string]any{
								"filter": map[string]any{
									"script": map[string]any{
										"script": map[string]any{
											"source": "doc['raw_message_jieba_tag.word'].value.length() > 1",
											"lang":   "painless",
										},
									},
								},
								"aggs": map[string]any{
									"by_tag": map[string]any{
										"filters": map[string]any{
											"filters": filtersMap,
										},
									},
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
	out := &webui.ChatTopicTrend{Days: []string{}, Series: []webui.TopicTrendSeries{}}
	if resp == nil || len(resp.Aggregations) == 0 {
		return out, nil
	}
	var aggs struct {
		PerDay struct {
			Buckets []struct {
				Key   int64 `json:"key"`
				Jieba struct {
					LongWord struct {
						ByTag struct {
							Buckets map[string]struct {
								DocCount int64 `json:"doc_count"`
							} `json:"buckets"`
						} `json:"by_tag"`
					} `json:"long_word"`
				} `json:"jieba"`
			} `json:"buckets"`
		} `json:"per_day"`
	}
	if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
		return nil, err
	}
	loc := utils.UTC8Loc()
	values := make(map[string][]int64, len(groups))
	for _, g := range groups {
		values[g.Key] = make([]int64, 0, len(aggs.PerDay.Buckets))
	}
	for _, b := range aggs.PerDay.Buckets {
		out.Days = append(out.Days, time.UnixMilli(b.Key).In(loc).Format("2006-01-02"))
		buckets := b.Jieba.LongWord.ByTag.Buckets
		for _, g := range groups {
			values[g.Key] = append(values[g.Key], buckets[g.Key].DocCount)
		}
	}
	for _, g := range groups {
		out.Series = append(out.Series, webui.TopicTrendSeries{
			Tag:    groupLabels[g.Key],
			Values: values[g.Key],
		})
	}
	return out, nil
}
