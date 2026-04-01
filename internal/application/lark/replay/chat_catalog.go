package replay

import (
	"context"
	"sort"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
)

type ChatCatalogQuery struct {
	Keyword string
	Days    int
	Limit   int
}

const defaultChatCatalogLoadLimit = 5000

type ChatCandidate struct {
	ChatID               string `json:"chat_id"`
	ChatName             string `json:"chat_name"`
	MessageCountInWindow int    `json:"message_count_in_window"`
	LastMessageAt        string `json:"last_message_at"`
	MatchedBy            string `json:"matched_by,omitempty"`
}

type chatCatalogLoader func(context.Context, ChatCatalogQuery) ([]*xmodel.MessageIndex, error)
type chatCatalogCandidateLoader func(context.Context, ChatCatalogQuery) ([]ChatCandidate, error)

type ChatCatalogService struct {
	loadMessages chatCatalogLoader
	loadCatalog  chatCatalogCandidateLoader
}

func (s ChatCatalogService) Search(ctx context.Context, query ChatCatalogQuery) ([]ChatCandidate, error) {
	query = normalizeChatCatalogQuery(query)
	if s.loadMessages == nil {
		candidates, err := s.catalogLoader()(ctx, query)
		if err != nil {
			return nil, err
		}
		candidates = filterCatalogCandidates(candidates, query.Keyword)
		if len(candidates) > query.Limit {
			candidates = candidates[:query.Limit]
		}
		return candidates, nil
	}

	messages, err := s.loader()(ctx, normalizeChatCatalogQuery(query))
	if err != nil {
		return nil, err
	}
	candidates := groupChatCandidates(messages, strings.TrimSpace(query.Keyword))
	limit := normalizeChatCatalogQuery(query).Limit
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func (s ChatCatalogService) LoadCatalog(ctx context.Context, query ChatCatalogQuery) ([]ChatCandidate, error) {
	query = normalizeChatCatalogLoadQuery(query)
	candidates, err := s.catalogLoader()(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(candidates) > query.Limit {
		candidates = candidates[:query.Limit]
	}
	return candidates, nil
}

func (s ChatCatalogService) loader() chatCatalogLoader {
	if s.loadMessages != nil {
		return s.loadMessages
	}
	return defaultChatCatalogLoader
}

func (s ChatCatalogService) catalogLoader() chatCatalogCandidateLoader {
	if s.loadCatalog != nil {
		return s.loadCatalog
	}
	return defaultChatCatalogCandidateLoader
}

func defaultChatCatalogLoader(ctx context.Context, query ChatCatalogQuery) ([]*xmodel.MessageIndex, error) {
	query = normalizeChatCatalogQuery(query)
	windowStart := time.Now().AddDate(0, 0, -query.Days).Format(time.RFC3339)
	size := uint64(max(query.Limit*100, 500))
	return history.New(ctx).
		Query(osquery.Bool().Must(
			osquery.Range("create_time_v2").Gte(windowStart),
		)).
		Source("chat_id", "chat_name", "create_time", "create_time_v2").
		Size(size).
		Sort("create_time_v2", osquery.OrderDesc).
		GetAll()
}

type chatCatalogAggResponse struct {
	ByChat struct {
		Buckets []struct {
			Key      string `json:"key"`
			DocCount int    `json:"doc_count"`
			Latest   struct {
				Hits struct {
					Hits []struct {
						Source struct {
							ChatID       string `json:"chat_id"`
							ChatName     string `json:"chat_name"`
							CreateTime   string `json:"create_time"`
							CreateTimeV2 string `json:"create_time_v2"`
						} `json:"_source"`
					} `json:"hits"`
				} `json:"hits"`
			} `json:"latest"`
		} `json:"buckets"`
	} `json:"by_chat"`
}

func defaultChatCatalogCandidateLoader(ctx context.Context, query ChatCatalogQuery) ([]ChatCandidate, error) {
	query = normalizeChatCatalogQuery(query)
	windowStart := time.Now().AddDate(0, 0, -query.Days).Format(time.RFC3339)
	size := max(query.Limit, 5000)
	req := map[string]any{
		"size": 0,
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{
						"range": map[string]any{
							"create_time_v2": map[string]any{
								"gte": windowStart,
							},
						},
					},
				},
			},
		},
		"aggs": map[string]any{
			"by_chat": map[string]any{
				"terms": map[string]any{
					"field": "chat_id",
					"size":  size,
				},
				"aggs": map[string]any{
					"latest": map[string]any{
						"top_hits": map[string]any{
							"size": 1,
							"sort": []map[string]any{
								{"create_time_v2": map[string]any{"order": "desc"}},
							},
							"_source": map[string]any{
								"includes": []string{"chat_id", "chat_name", "create_time", "create_time_v2"},
							},
						},
					},
				},
			},
		},
	}

	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, "", ""), req)
	if err != nil {
		return nil, err
	}
	agg := &chatCatalogAggResponse{}
	if err := sonic.Unmarshal(resp.Aggregations, agg); err != nil {
		return nil, err
	}

	candidates := make([]ChatCandidate, 0, len(agg.ByChat.Buckets))
	for _, bucket := range agg.ByChat.Buckets {
		latest := bucket.Latest.Hits.Hits
		chatID := strings.TrimSpace(bucket.Key)
		chatName := chatID
		lastMessageAt := ""
		if len(latest) > 0 {
			chatID = firstNonEmpty(latest[0].Source.ChatID, chatID)
			chatName = firstNonEmpty(latest[0].Source.ChatName, chatID)
			lastMessageAt = firstNonEmpty(strings.TrimSpace(latest[0].Source.CreateTime), strings.TrimSpace(latest[0].Source.CreateTimeV2))
		}
		candidates = append(candidates, ChatCandidate{
			ChatID:               chatID,
			ChatName:             chatName,
			MessageCountInWindow: bucket.DocCount,
			LastMessageAt:        strings.TrimSpace(lastMessageAt),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := parseCatalogTimeValue(candidates[i].LastMessageAt)
		right := parseCatalogTimeValue(candidates[j].LastMessageAt)
		if left.Equal(right) {
			return candidates[i].ChatID < candidates[j].ChatID
		}
		return left.After(right)
	})
	return candidates, nil
}

func normalizeChatCatalogQuery(query ChatCatalogQuery) ChatCatalogQuery {
	query.Keyword = strings.TrimSpace(query.Keyword)
	if query.Days <= 0 {
		query.Days = 7
	}
	if query.Limit <= 0 {
		query.Limit = 20
	}
	return query
}

func normalizeChatCatalogLoadQuery(query ChatCatalogQuery) ChatCatalogQuery {
	query.Keyword = strings.TrimSpace(query.Keyword)
	if query.Days <= 0 {
		query.Days = 7
	}
	if query.Limit <= 0 {
		query.Limit = defaultChatCatalogLoadLimit
	}
	return query
}

func groupChatCandidates(messages []*xmodel.MessageIndex, keyword string) []ChatCandidate {
	type aggregate struct {
		candidate ChatCandidate
		lastAt    time.Time
	}

	keyword = strings.TrimSpace(keyword)
	grouped := make(map[string]*aggregate)
	for _, item := range messages {
		if item == nil {
			continue
		}
		chatID := strings.TrimSpace(item.ChatID)
		if chatID == "" {
			continue
		}
		chatName := strings.TrimSpace(item.ChatName)
		if keyword != "" && !strings.Contains(strings.ToLower(chatName), strings.ToLower(keyword)) {
			continue
		}
		lastAt := parseCatalogTime(item)
		entry, exists := grouped[chatID]
		if !exists {
			entry = &aggregate{
				candidate: ChatCandidate{
					ChatID:   chatID,
					ChatName: firstNonEmpty(chatName, chatID),
					MatchedBy: func() string {
						if keyword == "" {
							return ""
						}
						return keyword
					}(),
				},
				lastAt: lastAt,
			}
			grouped[chatID] = entry
		}
		entry.candidate.MessageCountInWindow++
		if strings.TrimSpace(entry.candidate.ChatName) == "" || entry.candidate.ChatName == chatID {
			entry.candidate.ChatName = firstNonEmpty(chatName, chatID)
		}
		if !lastAt.IsZero() && (entry.lastAt.IsZero() || lastAt.After(entry.lastAt)) {
			entry.lastAt = lastAt
		}
		entry.candidate.LastMessageAt = firstNonEmpty(formatCatalogTime(entry.lastAt), strings.TrimSpace(item.CreateTime))
	}

	candidates := make([]ChatCandidate, 0, len(grouped))
	for _, item := range grouped {
		candidates = append(candidates, item.candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := parseCatalogTimeValue(candidates[i].LastMessageAt)
		right := parseCatalogTimeValue(candidates[j].LastMessageAt)
		if left.Equal(right) {
			return candidates[i].ChatID < candidates[j].ChatID
		}
		return left.After(right)
	})
	return candidates
}

func filterCatalogCandidates(candidates []ChatCandidate, keyword string) []ChatCandidate {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return append([]ChatCandidate(nil), candidates...)
	}
	out := make([]ChatCandidate, 0, len(candidates))
	for _, item := range candidates {
		if strings.Contains(strings.ToLower(strings.TrimSpace(item.ChatName)), keyword) ||
			strings.Contains(strings.ToLower(strings.TrimSpace(item.ChatID)), keyword) {
			item.MatchedBy = strings.TrimSpace(keyword)
			out = append(out, item)
		}
	}
	return out
}

func parseCatalogTime(item *xmodel.MessageIndex) time.Time {
	if item == nil {
		return time.Time{}
	}
	if parsed := parseCatalogTimeValue(item.CreateTimeV2); !parsed.IsZero() {
		return parsed
	}
	if parsed := parseCatalogTimeValue(item.CreateTime); !parsed.IsZero() {
		return parsed
	}
	return item.CreatedAt
}

func parseCatalogTimeValue(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	layouts := []string{
		time.DateTime,
		time.RFC3339,
		"2006-01-02 15:04:05 -0700 MST",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func formatCatalogTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.DateTime)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
