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
