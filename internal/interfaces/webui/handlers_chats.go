package webui

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chatmetrics"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/cache"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// listEnrichConcurrency 限制列表指标补全时对 Lark / OpenSearch 的并发，
// 避免群多时打爆下游。
const listEnrichConcurrency = 8

func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	if s.chats == nil {
		writeError(w, http.StatusServiceUnavailable, "chat service unavailable")
		return
	}
	chats, err := s.chats.ListChats(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// ?metrics=1 时补全可排序指标；默认不补，保持列表轻量快速。
	if isTruthy(r.URL.Query().Get("metrics")) {
		windowDays := parseWindowDays(r.URL.Query().Get("window"))
		chats = s.enrichChatMetrics(r.Context(), chats, windowDays)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": chats,
		"total": len(chats),
	})
}

// enrichChatMetrics 给群列表补全成员量 / 近 N 天发言量 / token 总量及派生均值。
//
// 数据来源尽量走缓存：token 总量用一次 GROUP BY chat_id 批量查询并按窗口缓存；
// 成员数与发言量逐群获取，但底层分别命中 larkuser 成员缓存与 OpenSearch。
// 同时把仅出现在 token 记录里的单聊（p2p）补进列表，使其也能被统计与排序。
func (s *Server) enrichChatMetrics(ctx context.Context, chats []ChatSummary, windowDays int) []ChatSummary {
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)

	// 1) 批量取每个 chat 的 token 总量（带 TTL 缓存）。
	totals := s.cachedTokenTotalsByChat(ctx, since, windowDays)

	// 2) 用 token 记录里的 chat 补全单聊：Lark 群列表只含群，不含 p2p。
	chats = mergeMissingChats(chats, totals)

	// 3) 逐群补成员数 / 发言量 / 派生均值，并发受限。
	sem := make(chan struct{}, listEnrichConcurrency)
	var wg sync.WaitGroup
	for i := range chats {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			chats[idx].Metrics = s.buildChatMetric(ctx, chats[idx].ChatID, since, windowDays, totals)
		}(i)
	}
	wg.Wait()
	return chats
}

// buildChatMetric 组装单个群的指标。各下游调用失败时降级为 0，不阻断整体。
func (s *Server) buildChatMetric(ctx context.Context, chatID string, since time.Time, windowDays int, totals map[string]chatTokenTotal) *ChatMetrics {
	m := &ChatMetrics{WindowDays: windowDays}
	if t, ok := totals[chatID]; ok {
		m.TotalTokens = t.TotalTokens
	}
	if s.memberCount != nil {
		if c, err := s.cachedMemberCount(ctx, chatID); err == nil {
			m.MemberCount = c
		}
	}
	if s.messageStats != nil {
		if c, err := s.cachedRecentMessages(ctx, chatID, since, windowDays); err == nil {
			m.RecentMessages = c
		}
	}
	if m.MemberCount > 0 {
		m.TokensPerMember = round2(float64(m.TotalTokens) / float64(m.MemberCount))
	}
	if m.RecentMessages > 0 {
		m.TokensPerMessage = round2(float64(m.TotalTokens) / float64(m.RecentMessages))
	}
	return m
}

// cachedTokenTotalsByChat 把按 chat 的 token 总量按窗口缓存，列表刷新不会反复重查 DB。
func (s *Server) cachedTokenTotalsByChat(ctx context.Context, since time.Time, windowDays int) map[string]chatTokenTotal {
	if !s.store.available() {
		return map[string]chatTokenTotal{}
	}
	key := webuiCacheKey("token_totals_by_chat", windowDays)
	totals, err := cache.GetOrExecute(ctx, key, func() (map[string]chatTokenTotal, error) {
		return s.store.totalsByChat(ctx, since)
	})
	if err != nil || totals == nil {
		return map[string]chatTokenTotal{}
	}
	return totals
}

// cachedMemberCount 复用 larkuser 的成员映射缓存换算成员数，避免额外 API 调用。
func (s *Server) cachedMemberCount(ctx context.Context, chatID string) (int, error) {
	return s.memberCount(ctx, chatID)
}

// cachedRecentMessages 把近 N 天发言量按 chat+窗口缓存，降低对 OpenSearch 的压力。
func (s *Server) cachedRecentMessages(ctx context.Context, chatID string, since time.Time, windowDays int) (int, error) {
	key := webuiCacheKey("recent_messages:"+chatID, windowDays)
	return cache.GetOrExecute(ctx, key, func() (int, error) {
		return s.messageStats(ctx, chatID, since)
	})
}

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chats == nil {
		writeError(w, http.StatusServiceUnavailable, "chat service unavailable")
		return
	}
	detail, err := s.chats.GetChat(r.Context(), chatID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleListMembers 返回某群的成员列表（底层命中 larkuser 成员缓存）。
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.memberList == nil {
		writeError(w, http.StatusServiceUnavailable, "member list unavailable")
		return
	}
	members, err := s.memberList(r.Context(), chatID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": members,
		"total": len(members),
	})
}

// mergeMissingChats 把仅出现在 token 记录中的 chat（通常是单聊）补进列表。
func mergeMissingChats(chats []ChatSummary, totals map[string]chatTokenTotal) []ChatSummary {
	seen := make(map[string]struct{}, len(chats))
	for _, c := range chats {
		seen[c.ChatID] = struct{}{}
	}
	extra := make([]ChatSummary, 0)
	for id, t := range totals {
		if _, ok := seen[id]; ok {
			continue
		}
		name := t.ChatName
		if name == "" {
			name = id
		}
		extra = append(extra, ChatSummary{ChatID: id, Name: name, ChatStatus: "p2p"})
	}
	// 让补充项顺序稳定，避免每次刷新顺序抖动。
	sort.Slice(extra, func(i, j int) bool { return extra[i].ChatID < extra[j].ChatID })
	return append(chats, extra...)
}

// larkChatService 是基于 Lark OpenAPI 的默认 ChatService 实现。
//
// 群列表复用 chatmetrics.ListChats（含 avatar 字段），详情复用
// larkchat.GetChatInfoCache + chatmetrics.CountChatMembers。
type larkChatService struct{}

// NewLarkChatService 返回基于 Lark OpenAPI 的群信息服务。
func NewLarkChatService() ChatService { return &larkChatService{} }

func (l *larkChatService) ListChats(ctx context.Context) ([]ChatSummary, error) {
	summaries := make([]ChatSummary, 0)
	for item := range chatmetrics.ListChats(ctx) {
		if item == nil {
			continue
		}
		summaries = append(summaries, ChatSummary{
			ChatID:      strings.TrimSpace(ptr(item.ChatId)),
			Name:        strings.TrimSpace(ptr(item.Name)),
			Avatar:      strings.TrimSpace(ptr(item.Avatar)),
			Description: strings.TrimSpace(ptr(item.Description)),
			ChatStatus:  strings.TrimSpace(ptr(item.ChatStatus)),
			External:    item.External != nil && *item.External,
			Tenant:      strings.TrimSpace(ptr(item.TenantKey)),
		})
	}
	return summaries, nil
}

func (l *larkChatService) GetChat(ctx context.Context, chatID string) (*ChatDetail, error) {
	data, err := larkchat.GetChatInfoCache(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.New("chat not found")
	}
	detail := &ChatDetail{
		ChatSummary: ChatSummary{
			ChatID:      chatID,
			Name:        strings.TrimSpace(ptr(data.Name)),
			Avatar:      strings.TrimSpace(ptr(data.Avatar)),
			Description: strings.TrimSpace(ptr(data.Description)),
			ChatStatus:  strings.TrimSpace(ptr(data.ChatStatus)),
			External:    data.External != nil && *data.External,
		},
		OwnerID:  strings.TrimSpace(ptr(data.OwnerId)),
		ChatMode: strings.TrimSpace(ptr(data.ChatMode)),
	}
	if count, err := LarkMemberCount(ctx, chatID); err == nil {
		detail.MemberCount = count
	}
	return detail, nil
}

// LarkMemberCount 复用 larkuser 的成员映射缓存返回成员数，避免额外 API 调用。
// 失败时回退到 chatmetrics.CountChatMembers。
func LarkMemberCount(ctx context.Context, chatID string) (int, error) {
	if memberMap, err := larkuser.GetUserMapFromChatIDCacheWithRedis(ctx, chatID); err == nil {
		return len(memberMap), nil
	}
	return chatmetrics.CountChatMembers(ctx, chatID)
}

// LarkMemberList 复用 larkuser 的成员映射缓存返回成员列表。
func LarkMemberList(ctx context.Context, chatID string) ([]ChatMember, error) {
	memberMap, err := larkuser.GetUserMapFromChatIDCacheWithRedis(ctx, chatID)
	if err != nil {
		return nil, err
	}
	members := make([]ChatMember, 0, len(memberMap))
	for openID, m := range memberMap {
		members = append(members, ChatMember{
			OpenID: openID,
			Name:   strings.TrimSpace(memberName(m)),
			Tenant: strings.TrimSpace(ptr(memberTenant(m))),
		})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	return members, nil
}

func memberName(m *larkim.ListMember) string {
	if m == nil {
		return ""
	}
	return ptr(m.Name)
}

func memberTenant(m *larkim.ListMember) *string {
	if m == nil {
		return nil
	}
	return m.TenantKey
}

func ptr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
