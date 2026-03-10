package ratelimit

import (
	"context"
	"time"
)

type StatsSnapshot struct {
	ChatID  string
	Now     time.Time
	Stats   *ChatStats
	Metrics *ChatMetrics
	Config  *Config
}

func BuildStatsSnapshot(ctx context.Context, chatID string) *StatsSnapshot {
	return Get().GetDetailSnapshot(ctx, chatID)
}

func buildStatsSnapshot(chatID string, stats *ChatStats, metrics *ChatMetrics, cfg *Config, now time.Time) *StatsSnapshot {
	if stats == nil {
		stats = &ChatStats{ChatID: chatID}
	}
	if stats.ChatID == "" {
		stats.ChatID = chatID
	}

	var metricsCopy *ChatMetrics
	if metrics != nil {
		copyValue := *metrics
		metricsCopy = &copyValue
	}

	return &StatsSnapshot{
		ChatID:  chatID,
		Now:     now,
		Stats:   stats,
		Metrics: metricsCopy,
		Config:  cloneConfig(cfg),
	}
}

func (s *SmartRateLimiter) GetDetailSnapshot(ctx context.Context, chatID string) *StatsSnapshot {
	now := time.Now()

	stats, _ := s.getOrCreateChatStats(ctx, chatID)
	if stats == nil {
		stats = &ChatStats{ChatID: chatID}
	}

	statsCopy := *stats
	if statsCopy.ChatID == "" {
		statsCopy.ChatID = chatID
	}

	hourly, _ := s.getHourlyActivity(ctx, chatID)
	recentSends, _ := s.getRecentSends(ctx, chatID)
	cooldownUntil, cooldownLevel, _ := s.getCooldown(ctx, chatID)

	statsCopy.CooldownUntil = cooldownUntil
	statsCopy.CooldownLevel = cooldownLevel
	s.updateDerivedStats(&statsCopy, hourly, recentSends, now)
	statsCopy.RecentSends = recentSends

	metrics := s.metrics.getChatStats(ctx, chatID)
	if metrics != nil {
		metricsCopy := *metrics
		metricsCopy.InCooldown = !cooldownUntil.IsZero() && now.Before(cooldownUntil)
		metrics = &metricsCopy
	}

	return buildStatsSnapshot(chatID, &statsCopy, metrics, s.CurrentConfig(), now)
}

func cloneConfig(cfg *Config) *Config {
	if cfg == nil {
		return DefaultConfig()
	}
	copyValue := *cfg
	return &copyValue
}
