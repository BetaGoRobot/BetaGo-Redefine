package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestSmartRateLimiter_GetDetailSnapshotIncludesCooldownAndMetricsState(t *testing.T) {
	s, rdb := setupTestRedis(t)
	defer s.Close()

	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.MaxMessagesPerHour = 7
	limiter := NewSmartRateLimiter(cfg, rdb)
	chatID := "test_chat_snapshot"

	if err := limiter.setCooldown(ctx, chatID, time.Now().Add(2*time.Minute), 2); err != nil {
		t.Fatalf("setCooldown() error = %v", err)
	}

	if err := limiter.metrics.saveChatMetrics(ctx, chatID, &ChatMetrics{
		ChecksTotal:  3,
		AllowedTotal: 2,
		BlockedTotal: 1,
		InCooldown:   false,
	}); err != nil {
		t.Fatalf("saveChatMetrics() error = %v", err)
	}

	snapshot := limiter.GetDetailSnapshot(ctx, chatID)
	if snapshot == nil {
		t.Fatal("GetDetailSnapshot() returned nil")
	}
	if snapshot.Stats == nil || snapshot.Stats.CooldownLevel != 2 {
		t.Fatalf("expected cooldown level from cooldown key, got %+v", snapshot.Stats)
	}
	if snapshot.Metrics == nil || !snapshot.Metrics.InCooldown {
		t.Fatalf("expected metrics cooldown state to be normalized from cooldown key, got %+v", snapshot.Metrics)
	}
	if snapshot.Config == nil || snapshot.Config.MaxMessagesPerHour != 7 {
		t.Fatalf("expected snapshot config copy, got %+v", snapshot.Config)
	}
}

func TestSmartRateLimiter_GetStatsReadsCooldownState(t *testing.T) {
	s, rdb := setupTestRedis(t)
	defer s.Close()

	ctx := context.Background()
	limiter := NewSmartRateLimiter(DefaultConfig(), rdb)
	chatID := "test_chat_stats_cooldown"

	if err := limiter.setCooldown(ctx, chatID, time.Now().Add(90*time.Second), 3); err != nil {
		t.Fatalf("setCooldown() error = %v", err)
	}

	stats := limiter.GetStats(ctx, chatID)
	if stats == nil {
		t.Fatal("GetStats() returned nil")
	}
	if stats.CooldownLevel != 3 {
		t.Fatalf("expected cooldown level from separate cooldown key, got %+v", stats)
	}
	if stats.CooldownUntil.IsZero() {
		t.Fatalf("expected cooldown until to be populated, got %+v", stats)
	}
}
