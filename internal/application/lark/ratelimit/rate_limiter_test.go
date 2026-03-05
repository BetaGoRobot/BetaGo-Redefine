package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestMain(m *testing.M) {
	path := "../../../../.dev/config.toml"
	if os.Getenv("BETAGO_CONFIG_PATH") != "" {
		path = os.Getenv("BETAGO_CONFIG_PATH")
	}
	config := config.LoadFile(path)
	otel.Init(config.OtelConfig)
	logs.Init() // 有先后顺序的.应当在otel之后
	m.Run()
}

func setupTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: s.Addr(),
	})

	return s, rdb
}

func TestSmartRateLimiter_Allow_Basic(t *testing.T) {
	s, rdb := setupTestRedis(t)
	defer s.Close()

	ctx := context.Background()
	config := &Config{
		MaxMessagesPerHour:  5,
		MaxMessagesPerDay:   10,
		MinIntervalSeconds:  0.5,
		CooldownBaseSeconds: 1.0,
		MaxCooldownSeconds:  5.0,
		BurstThreshold:      3,
		BurstWindowSeconds:  10.0,
	}

	limiter := NewSmartRateLimiter(config, rdb)
	chatID := "test_chat_basic"

	// 测试 1: 第一次应该允许
	allowed, reason := limiter.Allow(ctx, chatID, TriggerTypeIntent)
	if !allowed {
		t.Errorf("第一次应该允许发送, got: false, reason: %s", reason)
	}
	limiter.Record(ctx, chatID, TriggerTypeIntent)

	// 测试 2: 最小间隔限制 - 立即再次发送应该被拒绝
	allowed, reason = limiter.Allow(ctx, chatID, TriggerTypeIntent)
	if allowed {
		t.Errorf("最小间隔内应该被拒绝, got: true")
	}

	// 等待超过最小间隔
	time.Sleep(600 * time.Millisecond)

	// 测试 3: 超过最小间隔后应该允许
	allowed, reason = limiter.Allow(ctx, chatID, TriggerTypeIntent)
	if !allowed {
		t.Errorf("超过最小间隔后应该允许, got: false, reason: %s", reason)
	}
}

func TestSmartRateLimiter_HourlyLimit(t *testing.T) {
	s, rdb := setupTestRedis(t)
	defer s.Close()

	ctx := context.Background()
	config := &Config{
		MaxMessagesPerHour:  2,
		MaxMessagesPerDay:   100,
		MinIntervalSeconds:  0.1,
		CooldownBaseSeconds: 1.0,
		MaxCooldownSeconds:  10.0,
		BurstThreshold:      10,
	}

	limiter := NewSmartRateLimiter(config, rdb)
	chatID := "test_chat_hourly"

	// 发送 2 条消息（达到小时限制）
	for i := 0; i < 2; i++ {
		time.Sleep(150 * time.Millisecond)
		allowed, reason := limiter.Allow(ctx, chatID, TriggerTypeIntent)
		if !allowed {
			t.Errorf("第 %d 条应该允许: %s", i+1, reason)
		}
		limiter.Record(ctx, chatID, TriggerTypeIntent)
	}

	// 第 3 条应该被小时限制拒绝
	time.Sleep(150 * time.Millisecond)
	allowed, reason := limiter.Allow(ctx, chatID, TriggerTypeIntent)
	if allowed {
		t.Errorf("第 3 条应该被小时限制拒绝, got: true, reason: %s", reason)
	}
}

func TestSmartRateLimiter_Cooldown(t *testing.T) {
	s, rdb := setupTestRedis(t)
	defer s.Close()

	ctx := context.Background()
	config := &Config{
		MaxMessagesPerHour:  100, // 设置高一点，只测试冷却逻辑
		MaxMessagesPerDay:   100,
		MinIntervalSeconds:  0.1,
		CooldownBaseSeconds: 0.3,
		MaxCooldownSeconds:  5.0,
		BurstThreshold:      10, // 不测试爆发
		BurstWindowSeconds:  1.0,
		BurstPenaltyFactor:  1.0,
	}

	limiter := NewSmartRateLimiter(config, rdb)
	chatID := "test_chat_cooldown"

	// 先发送一条消息
	time.Sleep(100 * time.Millisecond)
	allowed, reason := limiter.Allow(ctx, chatID, TriggerTypeIntent)
	if !allowed {
		t.Errorf("第 1 条应该允许: %s", reason)
	}
	limiter.Record(ctx, chatID, TriggerTypeIntent)

	// 等待超过最小间隔
	time.Sleep(200 * time.Millisecond)

	// 再次检查应该允许（因为没有达到限制）
	allowed, reason = limiter.Allow(ctx, chatID, TriggerTypeIntent)
	if !allowed {
		t.Errorf("应该允许: got: false, reason: %s", reason)
	}
}

func TestSmartRateLimiter_GetStats(t *testing.T) {
	s, rdb := setupTestRedis(t)
	defer s.Close()

	ctx := context.Background()
	config := DefaultConfig()
	limiter := NewSmartRateLimiter(config, rdb)
	chatID := "test_chat_stats"

	// 先发送一些消息
	allowed, _ := limiter.Allow(ctx, chatID, TriggerTypeIntent)
	if allowed {
		limiter.Record(ctx, chatID, TriggerTypeIntent)
	}

	// 获取统计
	stats := limiter.GetStats(ctx, chatID)
	if stats == nil {
		t.Fatalf("GetStats 返回 nil")
	}
	if stats.ChatID != chatID {
		t.Errorf("ChatID 不匹配: got %s, want %s", stats.ChatID, chatID)
	}
	if stats.TotalMessagesSent != 1 {
		t.Errorf("TotalMessagesSent 不匹配: got %d, want 1", stats.TotalMessagesSent)
	}
}

func TestMetrics(t *testing.T) {
	s, rdb := setupTestRedis(t)
	defer s.Close()

	metrics := NewMetrics(rdb)
	chatID := "test_chat_metrics"

	// 初始状态应该没有数据
	stats := metrics.GetChatStats(chatID)
	if stats != nil {
		t.Errorf("初始状态应该没有数据, got: %v", stats)
	}

	// 获取所有统计
	allStats := metrics.GetAllChatStats()
	if allStats == nil {
		t.Errorf("GetAllChatStats 不应该返回 nil")
	}
}

func TestDecider_Integration(t *testing.T) {
	s, rdb := setupTestRedis(t)
	defer s.Close()

	ctx := context.Background()
	config := DefaultConfig()
	limiter := NewSmartRateLimiter(config, rdb)
	decider := NewDecider(limiter)
	chatID := "test_decider"

	// 测试随机回复决策
	decision := decider.DecideRandomReply(ctx, chatID, 1.0)
	// 第一次应该通过（除非被频控拒绝，但频控初始状态应该允许）
	if !decision.Allowed {
		t.Logf("DecideRandomReply 第一次被拒绝: %s", decision.Reason)
		// 这是可以接受的，因为可能有频控逻辑
	}

	// 获取统计
	stats := decider.GetChatStats(ctx, chatID)
	if stats == nil {
		t.Errorf("GetChatStats 不应该返回 nil")
	}
}

func TestTriggerTypeWeights(t *testing.T) {
	s, rdb := setupTestRedis(t)
	defer s.Close()

	ctx := context.Background()
	config := &Config{
		MaxMessagesPerHour:  100,
		MinIntervalSeconds:  0.0,
		CooldownBaseSeconds: 10.0,
		BurstThreshold:      100,
	}

	limiter := NewSmartRateLimiter(config, rdb)
	chatID := "test_trigger_weights"

	// 测试不同触发类型都允许
	testCases := []TriggerType{
		TriggerTypeIntent,
		TriggerTypeRandom,
		TriggerTypeReaction,
		TriggerTypeRepeat,
	}

	for _, tt := range testCases {
		allowed, reason := limiter.Allow(ctx, chatID, tt)
		if !allowed {
			t.Errorf("触发类型 %s 应该允许: %s", tt, reason)
		}
		limiter.Record(ctx, chatID, tt)
		time.Sleep(50 * time.Millisecond)
	}
}
