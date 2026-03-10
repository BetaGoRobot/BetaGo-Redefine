package ratelimit

import "fmt"

type StatsDisplayPolicy struct {
	Config *Config

	VolumeWarnRatio   float64
	VolumeDangerRatio float64

	BlockedRateWarn   float64
	BlockedRateDanger float64

	BurstWarnFactor   float64
	BurstDangerFactor float64
}

func NewStatsDisplayPolicy(cfg *Config) StatsDisplayPolicy {
	return StatsDisplayPolicy{
		Config:            cloneConfig(cfg),
		VolumeWarnRatio:   0.5,
		VolumeDangerRatio: 0.8,
		BlockedRateWarn:   15,
		BlockedRateDanger: 35,
		BurstWarnFactor:   1.2,
		BurstDangerFactor: 1.8,
	}
}

func (p StatsDisplayPolicy) DayVolume(total int64) (color, note string) {
	return p.volumePresentation(total, p.Config.MaxMessagesPerDay, "24h")
}

func (p StatsDisplayPolicy) HourVolume(total int64) (color, note string) {
	return p.volumePresentation(total, p.Config.MaxMessagesPerHour, "近 1 小时")
}

func (p StatsDisplayPolicy) volumePresentation(total int64, limit int, label string) (color, note string) {
	if limit <= 0 {
		return "blue", label + " 发送稳定"
	}

	ratio := float64(total) / float64(limit)
	switch {
	case ratio >= 1:
		return "red", fmt.Sprintf("%s 已达到配置上限", label)
	case ratio >= p.VolumeDangerRatio:
		return "red", fmt.Sprintf("%s 已接近配置上限", label)
	case ratio >= p.VolumeWarnRatio:
		return "orange", fmt.Sprintf("%s 使用率偏高", label)
	default:
		return "blue", label + " 使用率平稳"
	}
}

func (p StatsDisplayPolicy) ActivityScore(score float64) (color, note string) {
	switch {
	case score >= p.Config.ActivityThresholdHigh:
		return "blue", "当前会话较活跃"
	case score <= p.Config.ActivityThresholdLow:
		return "orange", "当前会话较冷"
	default:
		return "default", "当前会话中等活跃"
	}
}

func (p StatsDisplayPolicy) BurstFactor(factor float64) (color, note string) {
	switch {
	case factor >= p.BurstDangerFactor:
		return "red", "近期发送明显集中"
	case factor >= p.BurstWarnFactor:
		return "orange", "近期发送略有集中"
	default:
		return "green", "发送节奏平稳"
	}
}

func (p StatsDisplayPolicy) BlockedRateColor(rate float64) string {
	switch {
	case rate >= p.BlockedRateDanger:
		return "red"
	case rate >= p.BlockedRateWarn:
		return "orange"
	default:
		return "green"
	}
}

func (p StatsDisplayPolicy) Overview(stats *ChatStats, metrics *ChatMetrics, status, statusDetail string, blockedRate float64) (title, detail, color string) {
	hourColor, _ := p.HourVolume(stats.TotalMessages1h)
	dayColor, _ := p.DayVolume(stats.TotalMessages24h)

	switch {
	case status == "冷却中":
		return "当前处于冷却窗口", fmt.Sprintf("机器人会暂时拒绝主动发言，%s", statusDetail), "red"
	case blockedRate >= p.BlockedRateDanger:
		return "拒绝率偏高", fmt.Sprintf("当前未冷却，但最近判定中有 %.1f%% 被拒绝，建议检查短时发送节奏", blockedRate), p.BlockedRateColor(blockedRate)
	case hourColor == "red":
		return "小时发送接近上限", fmt.Sprintf("近 1 小时发送量已接近配置阈值 %d 条，建议关注连续触发", p.Config.MaxMessagesPerHour), "orange"
	case dayColor == "red":
		return "日发送接近上限", fmt.Sprintf("24h 发送量已接近配置阈值 %d 条，建议观察会话整体频率", p.Config.MaxMessagesPerDay), "orange"
	case stats.CooldownLevel >= 2:
		return "冷却压力仍在", fmt.Sprintf("当前已恢复，但冷却等级为 %d，需要继续关注爆发发送", stats.CooldownLevel), cooldownLevelColor(stats.CooldownLevel)
	case metrics == nil:
		return "频控状态稳定", "当前未处于冷却，诊断数据尚未建立，可继续观察发送节奏", "green"
	default:
		return "频控状态稳定", "当前未处于冷却，拒绝率与发送节奏都在可控范围内", "green"
	}
}
