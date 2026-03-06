package ratelimit

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

// Decision 频控决策结果
type Decision struct {
	Allowed     bool   `json:"allowed"`
	Reason      string `json:"reason"`
	ShouldRetry bool   `json:"should_retry"`
	CooldownSec int    `json:"cooldown_sec"`
}

// Decider 频控决策器
type Decider struct {
	limiter *SmartRateLimiter
}

// NewDecider 创建频控决策器
func NewDecider(limiter *SmartRateLimiter) *Decider {
	if limiter == nil {
		limiter = Get()
	}
	return &Decider{limiter: limiter}
}

// DecideIntentReply 决定是否进行意图识别回复
func (d *Decider) DecideIntentReply(
	ctx context.Context,
	chatID string,
	analysis *intent.IntentAnalysis,
) *Decision {
	if !analysis.NeedReply {
		return &Decision{
			Allowed: false,
			Reason:  "意图分析表明不需要回复",
		}
	}

	allowed, reason := d.limiter.Allow(ctx, chatID, TriggerTypeIntent)
	if !allowed {
		return &Decision{
			Allowed: false,
			Reason:  fmt.Sprintf("频控限制: %s", reason),
		}
	}

	// 使用硬编码的默认值（暂时避免循环依赖）
	threshold := 70
	if analysis.ReplyConfidence >= threshold {
		return &Decision{
			Allowed: true,
			Reason:  fmt.Sprintf("高置信度回复 (置信度: %d)", analysis.ReplyConfidence),
		}
	}

	fallbackRate := 10.0 / 100.0
	stats := d.limiter.GetStats(ctx, chatID)
	adjustedRate := fallbackRate
	if stats.CurrentActivityScore > 0.7 {
		adjustedRate = fallbackRate * 1.5
	} else if stats.CurrentActivityScore < 0.3 {
		adjustedRate = fallbackRate * 0.5
	}

	if rand.Float64() < adjustedRate {
		return &Decision{
			Allowed: true,
			Reason:  fmt.Sprintf("低置信度概率通过 (置信度: %d, 概率: %.2f)", analysis.ReplyConfidence, adjustedRate),
		}
	}

	return &Decision{
		Allowed: false,
		Reason:  fmt.Sprintf("低置信度且未通过概率筛选 (置信度: %d, 阈值: %d)", analysis.ReplyConfidence, threshold),
	}
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
			Allowed: false,
			Reason:  fmt.Sprintf("频控限制: %s", reason),
		}
	}

	stats := d.limiter.GetStats(ctx, chatID)
	adjustedProb := baseProbability

	if stats.CurrentActivityScore > 0.8 {
		adjustedProb = baseProbability * 1.5
	} else if stats.CurrentActivityScore > 0.5 {
		adjustedProb = baseProbability * 1.2
	} else if stats.CurrentActivityScore < 0.2 {
		adjustedProb = baseProbability * 0.3
	}

	recentCount := float64(stats.TotalMessages1h)
	if recentCount > 20 {
		adjustedProb *= 0.5
	} else if recentCount > 10 {
		adjustedProb *= 0.8
	}

	if rand.Float64() < adjustedProb {
		return &Decision{
			Allowed: true,
			Reason:  fmt.Sprintf("随机概率通过 (原始概率: %.2f, 调整后: %.2f)", baseProbability, adjustedProb),
		}
	}

	return &Decision{
		Allowed: false,
		Reason:  fmt.Sprintf("未通过随机概率筛选 (原始概率: %.2f, 调整后: %.2f)", baseProbability, adjustedProb),
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
