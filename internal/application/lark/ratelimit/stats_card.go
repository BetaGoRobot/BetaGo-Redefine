package ratelimit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

type StatsCardData struct {
	HeroMetrics       []StatsCardMetric
	ChatID            string
	Status            string
	StatusDetail      string
	OverviewTitle     string
	OverviewDetail    string
	OverviewColor     string
	SummaryMetrics    []StatsCardMetric
	DiagnosticMetrics []StatsCardMetric
	RecentSendRecords []StatsCardRecentSend
	UpdatedAt         string
	HasDiagnostics    bool
}

type StatsCardMetric struct {
	Label           string
	Value           string
	Note            string
	ValueColor      string
	BackgroundStyle string
}

type StatsCardRecentSend struct {
	Trigger string
	Time    string
}

func BuildStatsCardJSON(ctx context.Context, chatID string) (map[string]any, error) {
	return BuildStatsCardJSONWithOptions(ctx, chatID, StatsCardOptions{})
}

func BuildStatsCardJSONFromData(ctx context.Context, data *StatsCardData) map[string]any {
	return map[string]any(buildStatsRawCard(ctx, data))
}

type StatsCardOptions struct {
	MessageID      string
	PendingHistory []larkmsg.CardActionHistoryRecord
}

func BuildStatsCardJSONWithOptions(ctx context.Context, chatID string, opts StatsCardOptions) (map[string]any, error) {
	data := BuildStatsCardData(ctx, chatID)
	card := buildStatsRawCardWithOptions(ctx, data, opts)
	return map[string]any(card), nil
}

func BuildStatsCardData(ctx context.Context, chatID string) *StatsCardData {
	return buildStatsCardData(BuildStatsSnapshot(ctx, chatID))
}

func buildStatsCardData(snapshot *StatsSnapshot) *StatsCardData {
	if snapshot == nil {
		snapshot = buildStatsSnapshot("", nil, nil, nil, time.Now())
	}

	stats := snapshot.Stats
	chatMetrics := snapshot.Metrics
	policy := NewStatsDisplayPolicy(snapshot.Config)
	now := snapshot.Now

	status := "正常"
	statusDetail := "当前未处于冷却状态"
	if !stats.CooldownUntil.IsZero() && now.Before(stats.CooldownUntil) {
		status = "冷却中"
		statusDetail = fmt.Sprintf("剩余 %.0f 秒", stats.CooldownUntil.Sub(now).Seconds())
	}

	blockedRate, allowedRate := diagnosticRates(chatMetrics)
	overviewTitle, overviewDetail, overviewColor := policy.Overview(stats, chatMetrics, status, statusDetail, blockedRate)
	dayVolumeColor, dayVolumeNote := policy.DayVolume(stats.TotalMessages24h)
	hourVolumeColor, hourVolumeNote := policy.HourVolume(stats.TotalMessages1h)
	activityColor, activityNote := policy.ActivityScore(stats.CurrentActivityScore)
	burstColor, burstNote := policy.BurstFactor(stats.CurrentBurstFactor)

	data := &StatsCardData{
		ChatID:         snapshot.ChatID,
		Status:         status,
		StatusDetail:   statusDetail,
		OverviewTitle:  overviewTitle,
		OverviewDetail: overviewDetail,
		OverviewColor:  overviewColor,
		HeroMetrics: []StatsCardMetric{
			{
				Label:           "当前状态",
				Value:           status,
				Note:            statusDetail,
				ValueColor:      statsStatusColor(status),
				BackgroundStyle: "grey",
			},
			{
				Label:           "冷却等级",
				Value:           fmt.Sprintf("%d", stats.CooldownLevel),
				Note:            cooldownLevelNote(stats.CooldownLevel),
				ValueColor:      cooldownLevelColor(stats.CooldownLevel),
				BackgroundStyle: "grey",
			},
		},
		SummaryMetrics: []StatsCardMetric{
			{Label: "历史总发送", Value: fmt.Sprintf("%d", stats.TotalMessagesSent), Note: "累计发送次数", ValueColor: "blue", BackgroundStyle: "grey"},
			{Label: "近24小时", Value: fmt.Sprintf("%d", stats.TotalMessages24h), Note: dayVolumeNote, ValueColor: dayVolumeColor, BackgroundStyle: "grey"},
			{Label: "近1小时", Value: fmt.Sprintf("%d", stats.TotalMessages1h), Note: hourVolumeNote, ValueColor: hourVolumeColor, BackgroundStyle: "grey"},
			{Label: "活跃度评分", Value: fmt.Sprintf("%.2f", stats.CurrentActivityScore), Note: activityNote, ValueColor: activityColor, BackgroundStyle: "grey"},
			{Label: "爆发因子", Value: fmt.Sprintf("%.2f", stats.CurrentBurstFactor), Note: burstNote, ValueColor: burstColor, BackgroundStyle: "grey"},
			{Label: "更新时间", Value: now.Format("15:04:05"), Note: "统计快照生成时间", ValueColor: "grey", BackgroundStyle: "grey"},
		},
		UpdatedAt: now.Format("15:04:05"),
	}

	if chatMetrics != nil {
		data.HeroMetrics = append(data.HeroMetrics,
			StatsCardMetric{
				Label:           "拒绝率",
				Value:           fmt.Sprintf("%.1f%%", blockedRate),
				Note:            fmt.Sprintf("通过率 %.1f%%", allowedRate),
				ValueColor:      policy.BlockedRateColor(blockedRate),
				BackgroundStyle: "grey",
			},
		)
		data.HasDiagnostics = true
		lastUpdated := "-"
		if !chatMetrics.LastUpdated.IsZero() {
			lastUpdated = chatMetrics.LastUpdated.Format("15:04:05")
		}
		data.DiagnosticMetrics = []StatsCardMetric{
			{Label: "检查次数", Value: fmt.Sprintf("%d", chatMetrics.ChecksTotal), Note: "进入频控判定的总次数", ValueColor: "blue", BackgroundStyle: "grey"},
			{Label: "通过次数", Value: fmt.Sprintf("%d", chatMetrics.AllowedTotal), ValueColor: "green", BackgroundStyle: "grey"},
			{Label: "拒绝次数", Value: fmt.Sprintf("%d", chatMetrics.BlockedTotal), ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "实际发送", Value: fmt.Sprintf("%d", chatMetrics.MessagesSentTotal), Note: "最终真正发出的消息数", ValueColor: "blue", BackgroundStyle: "grey"},
			{Label: "拒绝率", Value: fmt.Sprintf("%.1f%%", blockedRate), ValueColor: policy.BlockedRateColor(blockedRate), BackgroundStyle: "grey"},
			{Label: "通过率", Value: fmt.Sprintf("%.1f%%", allowedRate), ValueColor: "green", BackgroundStyle: "grey"},
			{Label: "冷却中", Value: boolLabel(chatMetrics.InCooldown), ValueColor: boolColor(chatMetrics.InCooldown), BackgroundStyle: "grey"},
			{Label: "最后更新", Value: lastUpdated, Note: "Metrics 最近刷新时间", ValueColor: "grey", BackgroundStyle: "grey"},
		}
	} else {
		data.HeroMetrics = append(data.HeroMetrics,
			StatsCardMetric{
				Label:           "拒绝率",
				Value:           "-",
				Note:            "暂无诊断数据",
				ValueColor:      "grey",
				BackgroundStyle: "grey",
			},
		)
	}

	if len(stats.RecentSends) == 0 {
		data.RecentSendRecords = []StatsCardRecentSend{{Trigger: "暂无发送记录", Time: "-"}}
		return data
	}

	start := 0
	if len(stats.RecentSends) > 5 {
		start = len(stats.RecentSends) - 5
	}
	records := make([]StatsCardRecentSend, 0, len(stats.RecentSends)-start)
	for i := len(stats.RecentSends) - 1; i >= start; i-- {
		send := stats.RecentSends[i]
		records = append(records, StatsCardRecentSend{
			Trigger: string(send.TriggerType),
			Time:    send.Timestamp.Format("15:04:05"),
		})
	}
	data.RecentSendRecords = records

	return data
}

func buildStatsRawCard(ctx context.Context, data *StatsCardData) larkmsg.RawCard {
	return buildStatsRawCardWithOptions(ctx, data, StatsCardOptions{})
}

func buildStatsRawCardWithOptions(ctx context.Context, data *StatsCardData, opts StatsCardOptions) larkmsg.RawCard {
	elements := []any{
		buildStatsOverviewBlock(data),
	}
	if hero := buildStatsHeroRow(data.HeroMetrics); len(hero) > 0 {
		elements = append(elements, hero)
	}
	elements = append(elements, statsDivider())
	elements = append(elements, statsSectionTitle("核心指标"))
	elements = append(elements, statsSectionHint("先看发送量、活跃度和爆发压力"))
	elements = append(elements, buildStatsMetricGrid(data.SummaryMetrics)...)
	if data.HasDiagnostics {
		elements = append(elements, statsDivider())
		elements = append(elements, statsSectionTitle("诊断指标"))
		elements = append(elements, statsSectionHint("Metrics 侧观测数据，用于辅助排查被拒原因"))
		elements = append(elements, buildStatsMetricGrid(data.DiagnosticMetrics)...)
	}
	elements = append(elements, statsDivider())
	elements = append(elements, statsSectionTitle("最近发送"))
	elements = append(elements, statsSectionHint("仅展示最近 5 条已发送记录"))
	elements = append(elements, buildRecentSendElements(data.RecentSendRecords)...)
	elements = append(elements, statsHintBlock("说明：诊断指标来自 Metrics，核心指标来自频控器统计快照"))

	return larkmsg.NewStandardPanelCard(ctx, "频控详情", elements, larkmsg.StandardCardFooterOptions{
		RefreshPayload: larkmsg.StringMapToAnyMap(BuildStatsViewValue(data.ChatID)),
		ActionHistory: larkmsg.CardActionHistoryOptions{
			Enabled:        true,
			OpenMessageID:  opts.MessageID,
			PendingRecords: opts.PendingHistory,
		},
	})
}

func buildStatsMetricGrid(metrics []StatsCardMetric) []any {
	elements := make([]any, 0, (len(metrics)+1)/2)
	for i := 0; i < len(metrics); i += 2 {
		columns := []any{
			statsMetricColumn(metrics[i]),
		}
		if i+1 < len(metrics) {
			columns = append(columns, statsMetricColumn(metrics[i+1]))
		} else {
			columns = append(columns, statsEmptyColumn())
		}
		elements = append(elements, larkmsg.ColumnSet(columns, larkmsg.ColumnSetOptions{
			HorizontalSpacing: "8px",
			FlexMode:          "bisect",
		}))
	}
	return elements
}

func buildStatsHeroRow(metrics []StatsCardMetric) map[string]any {
	if len(metrics) == 0 {
		return nil
	}
	columns := make([]any, 0, len(metrics))
	for _, metric := range metrics {
		columns = append(columns, statsHeroMetricColumn(metric))
	}
	return larkmsg.ColumnSet(columns, larkmsg.ColumnSetOptions{
		HorizontalSpacing: "8px",
		FlexMode:          "trisect",
	})
}

func statsMetricColumn(metric StatsCardMetric) map[string]any {
	return statsMetricColumnWithMode(metric, false)
}

func statsHeroMetricColumn(metric StatsCardMetric) map[string]any {
	return statsMetricColumnWithMode(metric, true)
}

func statsMetricColumnWithMode(metric StatsCardMetric, highlight bool) map[string]any {
	backgroundStyle := metric.BackgroundStyle
	if backgroundStyle == "" {
		backgroundStyle = "grey"
	}
	elements := []any{
		statsMarkdownBlock(statsMetricMarkdown(metric)),
	}
	if highlight {
		elements = []any{
			statsPlainText(metric.Value, "heading-2", safeTextColor(metric.ValueColor), "left"),
			statsMarkdownBlock(statsMetricMetaMarkdown(metric)),
		}
	}
	return larkmsg.Column(elements, larkmsg.ColumnOptions{
		Width:           "weighted",
		Weight:          1,
		VerticalAlign:   "top",
		BackgroundStyle: backgroundStyle,
		Padding:         "8px",
	})
}

func statsEmptyColumn() map[string]any {
	return larkmsg.Column([]any{statsMarkdownBlock(" ")}, larkmsg.ColumnOptions{
		Width:         "weighted",
		Weight:        1,
		VerticalAlign: "top",
	})
}

func buildStatsOverviewBlock(data *StatsCardData) map[string]any {
	title, detail, color := statsOverviewContent(data)
	return larkmsg.SplitColumns(
		[]any{
			statsPlainText(title, "heading-1", color, "left"),
			statsMarkdownBlock(strings.Join([]string{
				"<font color='grey'>当前判断</font>",
				detail,
			}, "\n")),
		},
		[]any{
			statsMarkdownBlock(strings.Join([]string{
				"<font color='grey'>会话</font>",
				fmt.Sprintf("**%s**", statsShortID(data.ChatID)),
				fmt.Sprintf("<font color='grey'>更新时间 %s</font>", statsDisplayValue(data.UpdatedAt)),
			}, "\n")),
		},
		larkmsg.SplitColumnsOptions{
			Left: larkmsg.ColumnOptions{
				Weight:          3,
				VerticalAlign:   "top",
				BackgroundStyle: "grey",
				Padding:         "8px",
			},
			Right: larkmsg.ColumnOptions{
				Weight:          2,
				VerticalAlign:   "top",
				BackgroundStyle: "grey",
				Padding:         "8px",
			},
			Row: larkmsg.ColumnSetOptions{
				HorizontalSpacing: "8px",
				FlexMode:          "stretch",
			},
		},
	)
}

func statsOverviewContent(data *StatsCardData) (title, detail, color string) {
	title = data.OverviewTitle
	detail = data.OverviewDetail
	color = data.OverviewColor
	if title == "" {
		title = data.Status
	}
	if title == "" {
		title = "频控状态"
	}
	if detail == "" {
		detail = data.StatusDetail
	}
	if detail == "" {
		detail = "查看当前会话的频控运行状态"
	}
	if color == "" {
		color = statsStatusColor(data.Status)
	}
	return title, detail, safeTextColor(color)
}

func buildRecentSendElements(records []StatsCardRecentSend) []any {
	lines := make([]string, 0, len(records))
	for _, record := range records {
		lines = append(lines, fmt.Sprintf("<font color='%s'>**%s**</font>  <font color='grey'>%s</font>",
			recentSendColor(record.Trigger),
			record.Trigger,
			statsDisplayValue(record.Time),
		))
	}
	return []any{
		statsMarkdownBlock(strings.Join(lines, "\n")),
	}
}

func statsSectionTitle(title string) map[string]any {
	return statsPlainText(title, "heading-3", "default", "left")
}

func statsSectionHint(content string) map[string]any {
	return statsPlainText(content, "notation", "grey", "left")
}

func statsMetricMarkdown(metric StatsCardMetric) string {
	lines := []string{
		fmt.Sprintf("<font color='grey'>%s</font>", metric.Label),
		fmt.Sprintf("<font color='%s'>**%s**</font>", safeTextColor(metric.ValueColor), metric.Value),
	}
	if metric.Note != "" {
		lines = append(lines, fmt.Sprintf("<font color='grey'>%s</font>", metric.Note))
	}
	return strings.Join(lines, "\n")
}

func statsMetricMetaMarkdown(metric StatsCardMetric) string {
	lines := []string{
		fmt.Sprintf("<font color='grey'>%s</font>", metric.Label),
	}
	if metric.Note != "" {
		lines = append(lines, fmt.Sprintf("<font color='grey'>%s</font>", metric.Note))
	}
	return strings.Join(lines, "\n")
}

func statsMarkdownBlock(content string) map[string]any {
	return larkmsg.Markdown(content)
}

func statsPlainText(content, textSize, textColor, textAlign string) map[string]any {
	return larkmsg.TextDiv(content, larkmsg.CardTextOptions{
		Size:  safeTextSize(textSize),
		Color: safeTextColor(textColor),
		Align: textAlign,
	})
}

func statsSubtleText(content string) map[string]any {
	return statsPlainText(content, "normal", "grey", "left")
}

func statsHintBlock(content string) map[string]any {
	return statsSubtleText(content)
}

func statsDivider() map[string]any {
	return larkmsg.Divider()
}

func statsShortID(chatID string) string {
	if chatID == "" {
		return "-"
	}
	if len(chatID) <= 12 {
		return chatID
	}
	return chatID[:12] + "..."
}

func diagnosticRates(chatMetrics *ChatMetrics) (blockedRate, allowedRate float64) {
	if chatMetrics == nil || chatMetrics.ChecksTotal == 0 {
		return 0, 0
	}
	blockedRate = float64(chatMetrics.BlockedTotal) / float64(chatMetrics.ChecksTotal) * 100
	allowedRate = float64(chatMetrics.AllowedTotal) / float64(chatMetrics.ChecksTotal) * 100
	return blockedRate, allowedRate
}

func boolLabel(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

func boolColor(value bool) string {
	if value {
		return "red"
	}
	return "green"
}

func statsStatusColor(status string) string {
	if status == "冷却中" {
		return "red"
	}
	return "green"
}

func cooldownLevelColor(level int) string {
	switch {
	case level >= 3:
		return "red"
	case level >= 1:
		return "orange"
	default:
		return "green"
	}
}

func cooldownLevelNote(level int) string {
	switch {
	case level >= 3:
		return "需要重点关注"
	case level >= 1:
		return "存在冷却压力"
	default:
		return "当前稳定"
	}
}

func recentSendColor(trigger string) string {
	switch trigger {
	case string(TriggerTypeRandom):
		return "orange"
	case string(TriggerTypeIntent), string(TriggerTypeMention):
		return "blue"
	case string(TriggerTypeReaction):
		return "green"
	default:
		return "default"
	}
}

func statsDisplayValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func safeTextColor(color string) string {
	switch color {
	case "default", "grey", "blue", "green", "orange", "red":
		return color
	default:
		return "default"
	}
}

func safeTextSize(size string) string {
	switch size {
	case "heading-0", "heading-1", "heading-2", "heading-3", "heading-4",
		"heading", "normal", "notation",
		"xxxx-large", "xxx-large", "xx-large", "x-large", "large", "medium", "small", "x-small":
		return size
	default:
		return "normal"
	}
}
