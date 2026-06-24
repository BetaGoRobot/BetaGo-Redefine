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
