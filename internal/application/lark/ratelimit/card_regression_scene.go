package ratelimit

import (
	"context"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
)

const (
	rateLimitStatsSceneKey    = "ratelimit.stats"
	rateLimitRegressionChatID = "oc_sample_debug_chat"
)

func RegisterRegressionScenes(registry *cardregression.Registry) {
	if registry == nil {
		return
	}
	scene := rateLimitStatsRegressionScene{}
	if _, exists := registry.Get(scene.SceneKey()); exists {
		return
	}
	registry.MustRegister(scene)
}

type rateLimitStatsRegressionScene struct{}

func (rateLimitStatsRegressionScene) SceneKey() string { return rateLimitStatsSceneKey }

func (rateLimitStatsRegressionScene) Meta() cardregression.CardSceneMeta {
	return cardregression.CardSceneMeta{
		Name:        rateLimitStatsSceneKey,
		Description: "频控统计卡回归场景",
		Tags:        []string{"schema-v2", "ratelimit"},
		Owner:       "ratelimit",
	}
}

func (rateLimitStatsRegressionScene) TestCases() []cardregression.CardRegressionCase {
	return []cardregression.CardRegressionCase{
		{
			Name:        "smoke-default",
			Description: "使用样例指标构建频控统计卡",
			Args:        map[string]string{"chat_id": rateLimitRegressionChatID},
			Tags:        []string{"smoke"},
		},
		{
			Name:        "live-default",
			Description: "使用真实 chat_id 构建频控统计卡",
			Requires: cardregression.CardRequirementSet{
				NeedBusinessChatID: true,
				NeedRedis:          true,
			},
			Tags: []string{"live"},
		},
	}
}

func (s rateLimitStatsRegressionScene) BuildCard(ctx context.Context, req cardregression.CardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business.ChatID, false)
}

func (s rateLimitStatsRegressionScene) BuildTestCard(ctx context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
	chatID := strings.TrimSpace(req.Business.ChatID)
	if chatID == "" {
		chatID = strings.TrimSpace(req.Args["chat_id"])
	}
	if chatID == "" {
		chatID = strings.TrimSpace(req.Case.Args["chat_id"])
	}
	return s.build(ctx, chatID, strings.TrimSpace(req.Case.Name) == "smoke-default")
}

func (rateLimitStatsRegressionScene) build(ctx context.Context, chatID string, smoke bool) (*cardregression.BuiltCard, error) {
	chatID = strings.TrimSpace(chatID)
	if smoke || chatID == "" {
		if chatID == "" {
			chatID = rateLimitRegressionChatID
		}
		return &cardregression.BuiltCard{
			Mode:     cardregression.BuiltCardModeCardJSON,
			Label:    rateLimitStatsSceneKey,
			CardJSON: BuildStatsCardJSONFromData(ctx, buildSampleStatsCardData(chatID)),
		}, nil
	}
	card, err := BuildStatsCardJSON(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return &cardregression.BuiltCard{
		Mode:     cardregression.BuiltCardModeCardJSON,
		Label:    rateLimitStatsSceneKey,
		CardJSON: card,
	}, nil
}

func buildSampleStatsCardData(chatID string) *StatsCardData {
	now := time.Now().In(utils.UTC8Loc())
	return &StatsCardData{
		ChatID:         chatID,
		Status:         "冷却中",
		StatusDetail:   "剩余 18 秒",
		OverviewTitle:  "发送压力偏高",
		OverviewDetail: "当前卡片使用的是样例数据，便于在没有真实消息流量时也能直接验证版式与字段密度。",
		OverviewColor:  "red",
		HeroMetrics: []StatsCardMetric{
			{Label: "当前状态", Value: "冷却中", Note: "剩余 18 秒", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "冷却等级", Value: "3", Note: "已触发连续退避", ValueColor: "orange", BackgroundStyle: "grey"},
			{Label: "拒绝率", Value: "32.4%", Note: "通过率 67.6%", ValueColor: "orange", BackgroundStyle: "grey"},
		},
		SummaryMetrics: []StatsCardMetric{
			{Label: "历史总发送", Value: "842", Note: "累计发送次数", ValueColor: "blue", BackgroundStyle: "grey"},
			{Label: "近24小时", Value: "119", Note: "较昨日高 14%", ValueColor: "orange", BackgroundStyle: "grey"},
			{Label: "近1小时", Value: "27", Note: "峰值时段", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "活跃度评分", Value: "8.40", Note: "群内互动高", ValueColor: "orange", BackgroundStyle: "grey"},
			{Label: "爆发因子", Value: "2.70", Note: "存在短时集中发送", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "更新时间", Value: now.Format("15:04:05"), Note: "示例快照生成时间", ValueColor: "grey", BackgroundStyle: "grey"},
		},
		HasDiagnostics: true,
		DiagnosticMetrics: []StatsCardMetric{
			{Label: "检查次数", Value: "311", Note: "进入频控判定", ValueColor: "blue", BackgroundStyle: "grey"},
			{Label: "通过次数", Value: "210", ValueColor: "green", BackgroundStyle: "grey"},
			{Label: "拒绝次数", Value: "101", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "实际发送", Value: "198", Note: "真正发出的消息数", ValueColor: "blue", BackgroundStyle: "grey"},
			{Label: "拒绝率", Value: "32.4%", ValueColor: "orange", BackgroundStyle: "grey"},
			{Label: "通过率", Value: "67.6%", ValueColor: "green", BackgroundStyle: "grey"},
			{Label: "冷却中", Value: "是", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "最后更新", Value: now.Format("15:04:05"), Note: "Metrics 最近刷新时间", ValueColor: "grey", BackgroundStyle: "grey"},
		},
		RecentSendRecords: []StatsCardRecentSend{
			{Trigger: "intent", Time: now.Add(-4 * time.Minute).Format("15:04:05")},
			{Trigger: "random", Time: now.Add(-3 * time.Minute).Format("15:04:05")},
			{Trigger: "reaction", Time: now.Add(-2 * time.Minute).Format("15:04:05")},
			{Trigger: "mention", Time: now.Add(-90 * time.Second).Format("15:04:05")},
			{Trigger: "card.refresh", Time: now.Add(-1 * time.Minute).Format("15:04:05")},
		},
		UpdatedAt: now.Format("15:04:05"),
	}
}
