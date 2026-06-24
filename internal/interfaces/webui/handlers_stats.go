package webui

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultStatsWindowDays = 7
	maxStatsWindowDays     = 365
)

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	windowDays := parseWindowDays(r.URL.Query().Get("window"))
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)

	resp := StatsResponse{ChatID: chatID}

	// Token 消耗：DB 为主，不可用时返回空聚合而非报错。
	if s.store.available() {
		token, err := s.store.collect(r.Context(), chatID, since, windowDays)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "token stats query failed: "+err.Error())
			return
		}
		resp.Token = token
	} else {
		resp.Token = TokenStats{WindowDays: windowDays}
	}

	// 消息量：依赖 OpenSearch，缺失时优雅降级。
	resp.Messages = s.collectMessageStats(r, chatID, since, windowDays)

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) collectMessageStats(r *http.Request, chatID string, since time.Time, windowDays int) MessageStats {
	stats := MessageStats{WindowDays: windowDays}
	if s.messageStats == nil {
		stats.Unavailable = "message stats source not configured"
		return stats
	}
	count, err := s.messageStats(r.Context(), chatID, since)
	if err != nil {
		stats.Unavailable = err.Error()
		return stats
	}
	stats.Available = true
	stats.RecentCount = count
	return stats
}

// handleInsightsActivity 返回某群在窗口内按"周内小时"维度的发言量分布，
// 用于前端画 7×24 活跃度热力图。能力依赖 OpenSearch；未注入或查询失败时
// 返回 503 并附原因，前端按降级处理。
func (s *Server) handleInsightsActivity(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chatActivity == nil {
		writeError(w, http.StatusServiceUnavailable, "chat activity source not configured")
		return
	}
	windowDays := parseWindowDays(r.URL.Query().Get("window"))
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	activity, err := s.chatActivity(r.Context(), chatID, since)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chat activity query failed: "+err.Error())
		return
	}
	if activity == nil {
		activity = &ChatActivity{}
	}
	activity.WindowDays = windowDays
	writeJSON(w, http.StatusOK, activity)
}

const (
	defaultKeywordsTopN = 80
	maxKeywordsTopN     = 200
	defaultCommandsTopN = 20
	maxCommandsTopN     = 100
	defaultSendersTopN  = 20
	maxSendersTopN      = 100
)

// handleInsightsKeywords 返回某群在窗口内按词频排序的 Top 关键词，供前端画词云。
// top 参数允许调优，缺省 80，硬上限 200，避免一口气把整张词表拉过来。
func (s *Server) handleInsightsKeywords(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chatKeywords == nil {
		writeError(w, http.StatusServiceUnavailable, "chat keywords source not configured")
		return
	}
	windowDays := parseWindowDays(r.URL.Query().Get("window"))
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	topN := defaultKeywordsTopN
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			if n < 1 {
				n = 1
			}
			if n > maxKeywordsTopN {
				n = maxKeywordsTopN
			}
			topN = n
		}
	}
	kw, err := s.chatKeywords(r.Context(), chatID, since, topN)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chat keywords query failed: "+err.Error())
		return
	}
	if kw == nil {
		kw = &ChatKeywords{Items: []KeywordCount{}}
	}
	kw.WindowDays = windowDays
	writeJSON(w, http.StatusOK, kw)
}

// handleInsightsCommands 返回某群在窗口内被调用的 Top 命令分布。
// 数据来自 OpenSearch 索引中 is_command + main_command 字段。
func (s *Server) handleInsightsCommands(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chatCommands == nil {
		writeError(w, http.StatusServiceUnavailable, "chat commands source not configured")
		return
	}
	windowDays := parseWindowDays(r.URL.Query().Get("window"))
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	topN := defaultCommandsTopN
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			if n < 1 {
				n = 1
			}
			if n > maxCommandsTopN {
				n = maxCommandsTopN
			}
			topN = n
		}
	}
	cmds, err := s.chatCommands(r.Context(), chatID, since, topN)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chat commands query failed: "+err.Error())
		return
	}
	if cmds == nil {
		cmds = &ChatCommands{Items: []CommandCount{}}
	}
	cmds.WindowDays = windowDays
	writeJSON(w, http.StatusOK, cmds)
}

// handleInsightsTopSenders 返回某群在窗口内按发言数排序的 Top 用户。
// 数据走 OpenSearch terms agg user_id；Total 是窗口内的命中文档总数（含尾巴）。
func (s *Server) handleInsightsTopSenders(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chatTopSenders == nil {
		writeError(w, http.StatusServiceUnavailable, "chat top senders source not configured")
		return
	}
	windowDays := parseWindowDays(r.URL.Query().Get("window"))
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	topN := defaultSendersTopN
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			if n < 1 {
				n = 1
			}
			if n > maxSendersTopN {
				n = maxSendersTopN
			}
			topN = n
		}
	}
	senders, err := s.chatTopSenders(r.Context(), chatID, since, topN)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chat top senders query failed: "+err.Error())
		return
	}
	if senders == nil {
		senders = &ChatTopSenders{Items: []SenderRank{}}
	}
	senders.WindowDays = windowDays
	writeJSON(w, http.StatusOK, senders)
}

// handleInsightsMessageKinds 返回某群在窗口内按 message_type 维度的消息数分布。
func (s *Server) handleInsightsMessageKinds(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chatMessageKinds == nil {
		writeError(w, http.StatusServiceUnavailable, "chat message kinds source not configured")
		return
	}
	windowDays := parseWindowDays(r.URL.Query().Get("window"))
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	kinds, err := s.chatMessageKinds(r.Context(), chatID, since)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chat message kinds query failed: "+err.Error())
		return
	}
	if kinds == nil {
		kinds = &ChatMessageKinds{Items: []MessageKindCount{}}
	}
	kinds.WindowDays = windowDays
	writeJSON(w, http.StatusOK, kinds)
}

// handleInsightsCommandTrend 返回某群在窗口内按"日"聚合的总消息数与命令调用数。
// 用于前端把命令使用量叠加到日趋势图上。
func (s *Server) handleInsightsCommandTrend(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chatCommandTrend == nil {
		writeError(w, http.StatusServiceUnavailable, "chat command trend source not configured")
		return
	}
	windowDays := parseWindowDays(r.URL.Query().Get("window"))
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	trend, err := s.chatCommandTrend(r.Context(), chatID, since)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chat command trend query failed: "+err.Error())
		return
	}
	if trend == nil {
		trend = &ChatCommandTrend{Days: []string{}, Total: []int64{}, Commands: []int64{}}
	}
	trend.WindowDays = windowDays
	writeJSON(w, http.StatusOK, trend)
}

const (
	defaultMentionsTopN       = 20
	maxMentionsTopN           = 100
	defaultMentionsSampleSize = 500
	maxMentionsSampleSize     = 5000
)

// handleInsightsTopMentions 返回某群在窗口内被 @ 频次最高的用户。
// mentions 在索引里是 JSON 字符串，OpenSearch 无法直接 agg；这里以 search 取样
// 再客户端解析的方式实现，sample 与 top 都允许调用方调整。
func (s *Server) handleInsightsTopMentions(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chatTopMentions == nil {
		writeError(w, http.StatusServiceUnavailable, "chat top mentions source not configured")
		return
	}
	windowDays := parseWindowDays(r.URL.Query().Get("window"))
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	topN := defaultMentionsTopN
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			if n < 1 {
				n = 1
			}
			if n > maxMentionsTopN {
				n = maxMentionsTopN
			}
			topN = n
		}
	}
	sample := defaultMentionsSampleSize
	if raw := strings.TrimSpace(r.URL.Query().Get("sample")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			if n < 1 {
				n = 1
			}
			if n > maxMentionsSampleSize {
				n = maxMentionsSampleSize
			}
			sample = n
		}
	}
	mentions, err := s.chatTopMentions(r.Context(), chatID, since, sample, topN)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chat top mentions query failed: "+err.Error())
		return
	}
	if mentions == nil {
		mentions = &ChatTopMentions{Items: []MentionRank{}}
	}
	mentions.WindowDays = windowDays
	writeJSON(w, http.StatusOK, mentions)
}

// handleInsightsTopicTrend 返回某群在窗口内按"日"+"词性大类"切片的词频趋势，
// 给前端画堆叠面积图。
func (s *Server) handleInsightsTopicTrend(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chatTopicTrend == nil {
		writeError(w, http.StatusServiceUnavailable, "chat topic trend source not configured")
		return
	}
	windowDays := parseWindowDays(r.URL.Query().Get("window"))
	since := s.now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	trend, err := s.chatTopicTrend(r.Context(), chatID, since)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chat topic trend query failed: "+err.Error())
		return
	}
	if trend == nil {
		trend = &ChatTopicTrend{Days: []string{}, Series: []TopicTrendSeries{}}
	}
	trend.WindowDays = windowDays
	writeJSON(w, http.StatusOK, trend)
}

// parseWindowDays 解析 window 参数，支持 "7d"/"30d"/"24h" 或纯数字（天）。
func parseWindowDays(raw string) int {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return defaultStatsWindowDays
	}
	switch {
	case strings.HasSuffix(raw, "d"):
		if n, err := strconv.Atoi(strings.TrimSuffix(raw, "d")); err == nil {
			return clampWindowDays(n)
		}
	case strings.HasSuffix(raw, "h"):
		if n, err := strconv.Atoi(strings.TrimSuffix(raw, "h")); err == nil {
			days := n / 24
			if days < 1 {
				days = 1
			}
			return clampWindowDays(days)
		}
	default:
		if n, err := strconv.Atoi(raw); err == nil {
			return clampWindowDays(n)
		}
	}
	return defaultStatsWindowDays
}

func clampWindowDays(n int) int {
	if n < 1 {
		return 1
	}
	if n > maxStatsWindowDays {
		return maxStatsWindowDays
	}
	return n
}
