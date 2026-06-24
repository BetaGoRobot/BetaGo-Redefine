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

// Membership values exposed via ChatSummary.Membership.
const (
	membershipActive  = "active"
	membershipLeft    = "left"
	membershipUnknown = "unknown"
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
	} else {
		// 不带 metrics 也要给 Chat.List 的群打 active 标记，保持字段稳定。
		for i := range chats {
			if chats[i].Membership == "" {
				chats[i].Membership = membershipActive
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": chats,
		"total": len(chats),
	})
}

// enrichChatMetrics 给会话列表补全成员量 / 近 N 天发言量 / token 总量及派生均值。
//
// 列表 = Chat.List（当前还在的群）∪ token 表里属于本 bot 的 chat_id（含历史已离开
// 的群、单聊）。token 表查询已经按 bot_id 过滤，天然是当前 bot 的范围；不再依赖
// OpenSearch 白名单兜底，直接用 DB 结果。
//
// 每条结果带 Membership 字段：
//   - oc_* 在 Chat.List 里 → active；
//   - oc_* 不在 Chat.List 里 → left（机器人已被移除/退群/群解散）；
//   - ou_* → active（单聊无法验证当前是否仍可发，按通常情况视为 active）；
//   - 其他前缀 → unknown。
func (s *Server) enrichChatMetrics(ctx context.Context, chats []ChatSummary, windowDays int) []ChatSummary {
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)

	// 1) 批量取每个 chat 的 token 总量（带 TTL 缓存，已按 bot_id 过滤）。
	totals := s.cachedTokenTotalsByChat(ctx, since, windowDays)

	// 2) 用 Chat.List 中出现过的 chat_id 标记 active；未出现的根据前缀打 left/unknown。
	chatListSet := make(map[string]struct{}, len(chats))
	for i := range chats {
		chatListSet[chats[i].ChatID] = struct{}{}
		chats[i].Membership = membershipActive
	}

	// 3) 用 token 总量补全 Chat.List 拿不到的会话（单聊、被移除的群）。
	chats = mergeMissingChats(chats, totals, chatListSet)

	// 4) 逐会话补成员数 / 发言量 / 派生均值 / 单聊 avatar，并发受限。
	sem := make(chan struct{}, listEnrichConcurrency)
	var wg sync.WaitGroup
	for i := range chats {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			chats[idx].Metrics = s.buildChatMetric(ctx, chats[idx].ChatID, since, windowDays, totals)
			s.enrichP2PProfile(ctx, &chats[idx])
		}(i)
	}
	wg.Wait()
	return chats
}

// enrichP2PProfile 把单聊（ou_*）会话的对方头像和昵称填到 ChatSummary 上，
// 群聊场景跳过。下游缺失依赖（测试态）时 recover 吞掉。
func (s *Server) enrichP2PProfile(ctx context.Context, summary *ChatSummary) {
	defer func() { _ = recover() }()
	if summary == nil || guessChatStatus(summary.ChatID) != "p2p" {
		return
	}
	brief := larkuser.GetUserBriefCache(ctx, summary.ChatID)
	if summary.Name == "" || summary.Name == summary.ChatID {
		if brief.Name != "" {
			summary.Name = brief.Name
		}
	}
	if summary.Avatar == "" && brief.Avatar != "" {
		summary.Avatar = brief.Avatar
	}
}

// buildChatMetric 组装单个会话的指标。各下游调用失败时降级为 0，不阻断整体。
func (s *Server) buildChatMetric(ctx context.Context, chatID string, since time.Time, windowDays int, totals map[string]chatTokenTotal) *ChatMetrics {
	m := &ChatMetrics{WindowDays: windowDays}
	if t, ok := totals[chatID]; ok {
		m.TotalTokens = t.TotalTokens
	}
	if s.memberCount != nil && strings.HasPrefix(chatID, "oc_") {
		// 单聊没有"成员数"概念（永远是 2 人），跳过 Lark 调用减少噪音。
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
	key := webuiCacheKey("token_totals_by_chat:"+s.botID, windowDays)
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
	// 群主头像/昵称：用 larkuser 缓存补全，便于详情页直接渲染。
	// 下游缺失依赖时（测试态）recover 兜底，仅保留已填写字段。
	if detail != nil && detail.OwnerID != "" && (detail.OwnerName == "" || detail.OwnerAvatar == "") {
		func() {
			defer func() { _ = recover() }()
			brief := larkuser.GetUserBriefCache(r.Context(), detail.OwnerID)
			if detail.OwnerName == "" {
				detail.OwnerName = brief.Name
			}
			if detail.OwnerAvatar == "" {
				detail.OwnerAvatar = brief.Avatar
			}
		}()
	}
	// 单聊场景顺带补 name/avatar，与列表一致。
	if detail != nil && guessChatStatus(detail.ChatID) == "p2p" {
		s.enrichP2PProfile(r.Context(), &detail.ChatSummary)
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
	// 用 larkuser 缓存批量补头像；未命中时静默降级为空字符串，前端用首字母占位。
	enrichMemberAvatars(r.Context(), members)
	writeJSON(w, http.StatusOK, map[string]any{
		"items": members,
		"total": len(members),
	})
}

// enrichMemberAvatars 为成员列表补 avatar 字段。
//
// larkuser.GetUserBriefCache 内部已带 Redis + 本地缓存，这里再加一层并发限制避免
// 一次性发起几十个 contact.User.Get 请求。下游缺失依赖（典型为测试环境，没有
// lark_dal client、未加载 config）时通过 recover 兜底，确保不污染主请求。
func enrichMemberAvatars(ctx context.Context, members []ChatMember) {
	defer func() {
		if r := recover(); r != nil {
			// 任何下游兜底失败（例如测试态读不到 .dev/config.toml）都吞掉，
			// 让成员列表保持无 avatar 字段也能展示。
			_ = r
		}
	}()
	if len(members) == 0 {
		return
	}
	const memberAvatarConcurrency = 8
	sem := make(chan struct{}, memberAvatarConcurrency)
	var wg sync.WaitGroup
	for i := range members {
		if members[i].Avatar != "" || members[i].OpenID == "" {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer func() { _ = recover() }()
			sem <- struct{}{}
			defer func() { <-sem }()
			brief := larkuser.GetUserBriefCache(ctx, members[idx].OpenID)
			if members[idx].Name == "" {
				members[idx].Name = brief.Name
			}
			members[idx].Avatar = brief.Avatar
		}(i)
	}
	wg.Wait()
}

// mergeMissingChats 把仅出现在 token 记录中的 chat 补进列表。
//
// token 表的查询已按 bot_id 过滤，所以 totals 里的 chat_id 都是当前 bot 真正
// 产生过消耗的会话，无需额外白名单。chatListSet 是 Lark Chat.List 已经覆盖的
// 群集合，用来给"机器人已离开"的群打 left 标记，不丢历史数据。
func mergeMissingChats(chats []ChatSummary, totals map[string]chatTokenTotal, chatListSet map[string]struct{}) []ChatSummary {
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
		status := guessChatStatus(id)
		extra = append(extra, ChatSummary{
			ChatID:     id,
			Name:       name,
			ChatStatus: status,
			Membership: membershipFor(id, chatListSet),
		})
	}
	// 让补充项顺序稳定，避免每次刷新顺序抖动。
	sort.Slice(extra, func(i, j int) bool { return extra[i].ChatID < extra[j].ChatID })
	return append(chats, extra...)
}

// membershipFor 根据 chat_id 前缀与 Chat.List 集合判定 membership。
func membershipFor(chatID string, chatListSet map[string]struct{}) string {
	if _, ok := chatListSet[chatID]; ok {
		return membershipActive
	}
	switch guessChatStatus(chatID) {
	case "group":
		return membershipLeft
	case "p2p":
		return membershipActive
	default:
		return membershipUnknown
	}
}

// guessChatStatus 用 chat_id 前缀推断会话类型。
//
// 约定（飞书 Open Platform）：
//   - oc_ 前缀 → 群聊 chat
//   - ou_ 前缀 → 用户（对机器人来说表示 p2p 会话）
//   - 其它前缀 → unknown，交给前端处理，避免误判。
func guessChatStatus(chatID string) string {
	switch {
	case strings.HasPrefix(chatID, "oc_"):
		return "group"
	case strings.HasPrefix(chatID, "ou_"):
		return "p2p"
	default:
		return "unknown"
	}
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
		chatID := strings.TrimSpace(ptr(item.ChatId))
		status := strings.TrimSpace(ptr(item.ChatStatus))
		if status == "" {
			status = guessChatStatus(chatID)
		}
		summaries = append(summaries, ChatSummary{
			ChatID:      chatID,
			Name:        strings.TrimSpace(ptr(item.Name)),
			Avatar:      strings.TrimSpace(ptr(item.Avatar)),
			Description: strings.TrimSpace(ptr(item.Description)),
			ChatStatus:  status,
			External:    item.External != nil && *item.External,
			Tenant:      strings.TrimSpace(ptr(item.TenantKey)),
			Membership:  membershipActive,
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
	status := strings.TrimSpace(ptr(data.ChatStatus))
	if status == "" {
		status = guessChatStatus(chatID)
	}
	detail := &ChatDetail{
		ChatSummary: ChatSummary{
			ChatID:      chatID,
			Name:        strings.TrimSpace(ptr(data.Name)),
			Avatar:      strings.TrimSpace(ptr(data.Avatar)),
			Description: strings.TrimSpace(ptr(data.Description)),
			ChatStatus:  status,
			External:    data.External != nil && *data.External,
			Membership:  membershipActive,
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
