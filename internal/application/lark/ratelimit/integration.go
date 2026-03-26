package ratelimit

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

// Decision 频控决策结果
type Decision struct {
	Allowed     bool        `json:"allowed"`
	Reason      string      `json:"reason"`
	ShouldRetry bool        `json:"should_retry"`
	CooldownSec int         `json:"cooldown_sec"`
	TriggerType TriggerType `json:"trigger_type"`
}

// Decider 频控决策器
type Decider struct {
	limiter *SmartRateLimiter

	intentReplyThreshold func(context.Context, string, string) int
	intentFallbackRate   func(context.Context, string, string) int
	randomFloat64        func() float64
}

// NewDecider 创建频控决策器
func NewDecider(limiter *SmartRateLimiter) *Decider {
	if limiter == nil {
		limiter = Get()
	}
	return &Decider{
		limiter:              limiter,
		intentReplyThreshold: appconfig.GetIntentReplyThreshold,
		intentFallbackRate:   appconfig.GetIntentFallbackRate,
		randomFloat64:        rand.Float64,
	}
}

// DecideIntentReply 决定是否进行意图识别回复
func (d *Decider) DecideIntentReply(
	ctx context.Context,
	chatID string,
	openID string,
	analysis *intent.IntentAnalysis,
) *Decision {
	if analysis == nil {
		return &Decision{
			Allowed: false,
			Reason:  "意图分析缺失",
		}
	}
	analysis.Sanitize()
	if !analysis.NeedReply {
		return &Decision{
			Allowed: false,
			Reason:  "意图分析表明不需要回复",
		}
	}

	triggerType := triggerTypeFromIntentAnalysis(analysis)
	if triggerType == TriggerTypeMention {
		return &Decision{
			Allowed:     true,
			Reason:      "直达消息绕过被动频控",
			TriggerType: TriggerTypeMention,
		}
	}

	allowed, reason := d.limiter.Allow(ctx, chatID, triggerType)
	if !allowed {
		return &Decision{
			Allowed:     false,
			Reason:      fmt.Sprintf("频控限制: %s", reason),
			TriggerType: triggerType,
		}
	}

	threshold := d.getIntentReplyThreshold(ctx, chatID, openID)
	score := calculateIntentReplyScore(analysis)
	if score >= float64(threshold) {
		return &Decision{
			Allowed:     true,
			Reason:      fmt.Sprintf("回复评分达阈值 (score: %.1f, threshold: %d)", score, threshold),
			TriggerType: triggerType,
		}
	}

	fallbackRate := float64(d.getIntentFallbackRate(ctx, chatID, openID)) / 100.0
	stats := d.limiter.GetStats(ctx, chatID)
	adjustedRate := adjustIntentFallbackRate(fallbackRate, stats, analysis, threshold, score)
	if adjustedRate < 0 {
		adjustedRate = 0
	}
	if adjustedRate > 1 {
		adjustedRate = 1
	}
	if d.randomFloat64() < adjustedRate {
		return &Decision{
			Allowed:     true,
			Reason:      fmt.Sprintf("低于阈值但通过概率筛选 (score: %.1f, threshold: %d, rate: %.2f)", score, threshold, adjustedRate),
			TriggerType: triggerType,
		}
	}

	return &Decision{
		Allowed:     false,
		Reason:      fmt.Sprintf("回复评分未达阈值且未通过概率筛选 (score: %.1f, threshold: %d, rate: %.2f)", score, threshold, adjustedRate),
		TriggerType: triggerType,
	}
}

func (d *Decider) getIntentReplyThreshold(ctx context.Context, chatID, openID string) int {
	if d != nil && d.intentReplyThreshold != nil {
		if threshold := d.intentReplyThreshold(ctx, chatID, openID); threshold > 0 {
			return threshold
		}
	}
	return 70
}

func (d *Decider) getIntentFallbackRate(ctx context.Context, chatID, openID string) int {
	if d != nil && d.intentFallbackRate != nil {
		if rate := d.intentFallbackRate(ctx, chatID, openID); rate >= 0 {
			return rate
		}
	}
	return 10
}

func triggerTypeFromIntentAnalysis(analysis *intent.IntentAnalysis) TriggerType {
	if analysis == nil {
		return TriggerTypeIntent
	}
	switch analysis.ReplyMode {
	case intent.ReplyModeDirect:
		return TriggerTypeMention
	case intent.ReplyModeActiveInterject:
		return TriggerTypeRandom
	case intent.ReplyModeIgnore:
		return TriggerTypeRandom
	default:
		return TriggerTypeIntent
	}
}

func calculateIntentReplyScore(analysis *intent.IntentAnalysis) float64 {
	if analysis == nil {
		return 0
	}
	score := float64(analysis.ReplyConfidence)*0.45 +
		float64(analysis.UserWillingness)*0.35 +
		float64(100-analysis.InterruptRisk)*0.20
	if analysis.ReplyMode == intent.ReplyModeActiveInterject {
		score -= 10
	}
	return math.Max(0, math.Min(100, score))
}

func adjustIntentFallbackRate(
	fallbackRate float64,
	stats *ChatStats,
	analysis *intent.IntentAnalysis,
	threshold int,
	score float64,
) float64 {
	adjustedRate := fallbackRate
	if adjustedRate <= 0 {
		return 0
	}
	if stats != nil {
		if stats.CurrentActivityScore > 0.7 {
			adjustedRate *= 1.2
		} else if stats.CurrentActivityScore < 0.3 {
			adjustedRate *= 0.6
		}
	}
	if analysis != nil && analysis.ReplyMode == intent.ReplyModeActiveInterject {
		adjustedRate *= 0.5
	}
	if gap := float64(threshold) - score; gap > 0 {
		adjustedRate *= math.Max(0.2, 1.0-gap/100.0)
	}
	return adjustedRate
}

// DecideRandomReply 决定是否进行随机回复
func (d *Decider) DecideRandomReply(
	ctx context.Context,
	chatID string,
	baseProbability float64,
) *Decision {
	allowed, reason := d.limiter.Allow(ctx, chatID, TriggerTypeRandom)
	if !allowed {
		return &Decision{
			Allowed:     false,
			Reason:      fmt.Sprintf("频控限制: %s", reason),
			TriggerType: TriggerTypeRandom,
		}
	}

	stats := d.limiter.GetStats(ctx, chatID)
	adjustedProb := baseProbability
	if stats.CurrentActivityScore > 0.7 {
		adjustedProb = baseProbability * 1.5
	} else if stats.CurrentActivityScore > 0.5 {
		adjustedProb = baseProbability * 1.5
	} else if stats.CurrentActivityScore < 0.2 {
		adjustedProb = baseProbability * 0.3
	}

	recentCount := float64(stats.TotalMessages1h)
	if recentCount > 20 {
		adjustedProb *= 0.5
	} else if recentCount > 10 {
		adjustedProb *= 0.8
	}

	if d.randomFloat64() < adjustedProb {
		return &Decision{
			Allowed:     true,
			Reason:      fmt.Sprintf("随机概率通过 (原始概率: %.2f, 调整后: %.2f)", baseProbability, adjustedProb),
			TriggerType: TriggerTypeRandom,
		}
	}

	return &Decision{
		Allowed:     false,
		Reason:      fmt.Sprintf("未通过随机概率筛选 (原始概率: %.2f, 调整后: %.2f)", baseProbability, adjustedProb),
		TriggerType: TriggerTypeRandom,
	}
}

// RecordReply 记录一次回复
func (d *Decider) RecordReply(ctx context.Context, chatID string, triggerType TriggerType) {
	d.limiter.Record(ctx, chatID, triggerType)
	logs.L().Ctx(ctx).Debug("reply recorded by rate limiter",
		zap.String("chat_id", chatID),
		zap.String("trigger_type", string(triggerType)),
	)
}

// GetChatStats 获取群组统计
func (d *Decider) GetChatStats(ctx context.Context, chatID string) *ChatStats {
	return d.limiter.GetStats(ctx, chatID)
}

var (
	globalDecider     *Decider
	globalDeciderOnce sync.Once
)

// GetDecider 获取全局决策器
func GetDecider() *Decider {
	globalDeciderOnce.Do(func() {
		globalDecider = NewDecider(nil)
	})
	return globalDecider
}
