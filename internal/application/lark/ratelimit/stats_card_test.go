package ratelimit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildStatsCardDataWithCooldownAndDiagnostics(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.Local)
	data := buildStatsCardData(buildStatsSnapshot(
		"oc_test_chat_123456",
		&ChatStats{
			ChatID:               "oc_test_chat_123456",
			TotalMessagesSent:    18,
			TotalMessages24h:     9,
			TotalMessages1h:      3,
			CurrentActivityScore: 0.76,
			CurrentBurstFactor:   1.34,
			CooldownLevel:        2,
			CooldownUntil:        now.Add(45 * time.Second),
			RecentSends: []SendRecord{
				{Timestamp: now.Add(-4 * time.Minute), TriggerType: TriggerTypeRandom},
				{Timestamp: now.Add(-2 * time.Minute), TriggerType: TriggerTypeIntent},
				{Timestamp: now.Add(-1 * time.Minute), TriggerType: TriggerTypeReaction},
			},
		},
		&ChatMetrics{
			ChecksTotal:       20,
			AllowedTotal:      15,
			BlockedTotal:      5,
			MessagesSentTotal: 12,
			InCooldown:        true,
			LastUpdated:       now,
		},
		DefaultConfig(),
		now,
	))

	if data.Status != "冷却中" {
		t.Fatalf("expected cooldown status, got %+v", data)
	}
	if !strings.Contains(data.StatusDetail, "45 秒") {
		t.Fatalf("expected cooldown detail, got %+v", data)
	}
	if data.OverviewTitle == "" || data.OverviewDetail == "" {
		t.Fatalf("expected overview summary, got %+v", data)
	}
	if len(data.HeroMetrics) != 3 {
		t.Fatalf("expected three hero metrics, got %+v", data)
	}
	if !data.HasDiagnostics || len(data.DiagnosticMetrics) == 0 {
		t.Fatalf("expected diagnostics metrics, got %+v", data)
	}
	if len(data.RecentSendRecords) != 3 {
		t.Fatalf("expected recent send records, got %+v", data)
	}
}

func TestBuildStatsCardDataUsesConfigDrivenVolumePolicy(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.Local)
	cfg := DefaultConfig()
	cfg.MaxMessagesPerHour = 10
	cfg.MaxMessagesPerDay = 20

	data := buildStatsCardData(buildStatsSnapshot(
		"oc_test_chat_123456",
		&ChatStats{
			ChatID:               "oc_test_chat_123456",
			TotalMessagesSent:    18,
			TotalMessages24h:     12,
			TotalMessages1h:      9,
			CurrentActivityScore: 0.76,
			CurrentBurstFactor:   1.34,
		},
		nil,
		cfg,
		now,
	))

	hourMetric := findMetricByLabel(data.SummaryMetrics, "近1小时")
	if hourMetric == nil || hourMetric.ValueColor != "red" || !strings.Contains(hourMetric.Note, "接近配置上限") {
		t.Fatalf("expected hour metric to follow config-driven threshold, got %+v", hourMetric)
	}

	dayMetric := findMetricByLabel(data.SummaryMetrics, "近24小时")
	if dayMetric == nil || dayMetric.ValueColor != "orange" || !strings.Contains(dayMetric.Note, "使用率偏高") {
		t.Fatalf("expected day metric to follow config-driven threshold, got %+v", dayMetric)
	}
}

func TestBuildStatsRawCardUsesSchemaV2Structure(t *testing.T) {
	card := buildStatsRawCard(&StatsCardData{
		ChatID:         "oc_test_chat_123456",
		Status:         "正常",
		StatusDetail:   "当前未处于冷却状态",
		OverviewTitle:  "频控状态稳定",
		OverviewDetail: "当前未处于冷却，拒绝率与发送节奏都在可控范围内",
		OverviewColor:  "green",
		HeroMetrics: []StatsCardMetric{
			{Label: "当前状态", Value: "正常", Note: "当前未处于冷却状态", ValueColor: "green"},
			{Label: "冷却等级", Value: "0", Note: "当前稳定", ValueColor: "green"},
			{Label: "拒绝率", Value: "25.0%", Note: "通过率 75.0%", ValueColor: "orange"},
		},
		SummaryMetrics: []StatsCardMetric{
			{Label: "历史总发送", Value: "18"},
			{Label: "近24小时", Value: "9"},
		},
		DiagnosticMetrics: []StatsCardMetric{
			{Label: "检查次数", Value: "20"},
			{Label: "拒绝率", Value: "25.0%"},
		},
		HasDiagnostics: true,
		RecentSendRecords: []StatsCardRecentSend{
			{Trigger: "intent", Time: "11:59:00"},
		},
		UpdatedAt: "12:00:00",
	})
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"schema":"2.0"`) {
		t.Fatalf("expected schema v2 card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"频控详情"`) {
		t.Fatalf("expected card title in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"核心指标"`) {
		t.Fatalf("expected summary section in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"最近发送"`) {
		t.Fatalf("expected recent sends section in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"text_size":"heading-1"`) {
		t.Fatalf("expected emphasized overview text in json: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"tag":"table"`) || strings.Contains(jsonStr, `"table_raw_array_1"`) {
		t.Fatalf("did not expect legacy table template fields in card json: %s", jsonStr)
	}
}

func TestBuildStatsRawCardStaysWithinElementLimit(t *testing.T) {
	card := buildStatsRawCard(&StatsCardData{
		ChatID:         "oc_test_chat_123456",
		Status:         "冷却中",
		StatusDetail:   "剩余 45 秒",
		OverviewTitle:  "当前处于冷却窗口",
		OverviewDetail: "机器人会暂时拒绝主动发言，剩余 45 秒",
		OverviewColor:  "red",
		HeroMetrics: []StatsCardMetric{
			{Label: "当前状态", Value: "冷却中", Note: "剩余 45 秒", ValueColor: "red"},
			{Label: "冷却等级", Value: "2", Note: "存在冷却压力", ValueColor: "orange"},
			{Label: "拒绝率", Value: "25.0%", Note: "通过率 75.0%", ValueColor: "orange"},
		},
		SummaryMetrics: []StatsCardMetric{
			{Label: "历史总发送", Value: "18", Note: "累计发送次数", ValueColor: "blue"},
			{Label: "近24小时", Value: "9", Note: "24h 发送量平稳", ValueColor: "blue"},
			{Label: "近1小时", Value: "3", Note: "近 1 小时发送稳定", ValueColor: "blue"},
			{Label: "活跃度评分", Value: "0.76", Note: "当前会话较活跃", ValueColor: "blue"},
			{Label: "爆发因子", Value: "1.34", Note: "近期发送略有集中", ValueColor: "orange"},
			{Label: "更新时间", Value: "12:00:00", Note: "统计快照生成时间", ValueColor: "grey"},
		},
		DiagnosticMetrics: []StatsCardMetric{
			{Label: "检查次数", Value: "20", Note: "进入频控判定的总次数", ValueColor: "blue"},
			{Label: "通过次数", Value: "15", ValueColor: "green"},
			{Label: "拒绝次数", Value: "5", ValueColor: "red"},
			{Label: "实际发送", Value: "12", Note: "最终真正发出的消息数", ValueColor: "blue"},
			{Label: "拒绝率", Value: "25.0%", ValueColor: "orange"},
			{Label: "通过率", Value: "75.0%", ValueColor: "green"},
			{Label: "冷却中", Value: "是", ValueColor: "red"},
			{Label: "最后更新", Value: "12:00:00", Note: "Metrics 最近刷新时间", ValueColor: "grey"},
		},
		HasDiagnostics: true,
		RecentSendRecords: []StatsCardRecentSend{
			{Trigger: "intent", Time: "11:59:00"},
			{Trigger: "random", Time: "11:58:30"},
			{Trigger: "reaction", Time: "11:58:00"},
			{Trigger: "mention", Time: "11:57:30"},
			{Trigger: "repeat", Time: "11:57:00"},
		},
		UpdatedAt: "12:00:00",
	})

	if countTaggedNodes(card) >= 200 {
		t.Fatalf("expected card node count below schema v2 limit, got %d", countTaggedNodes(card))
	}
}

func countTaggedNodes(value any) int {
	switch v := value.(type) {
	case map[string]any:
		total := 0
		if _, ok := v["tag"]; ok {
			total++
		}
		for _, child := range v {
			total += countTaggedNodes(child)
		}
		return total
	case []any:
		total := 0
		for _, item := range v {
			total += countTaggedNodes(item)
		}
		return total
	default:
		return 0
	}
}

func findMetricByLabel(metrics []StatsCardMetric, label string) *StatsCardMetric {
	for i := range metrics {
		if metrics[i].Label == label {
			return &metrics[i]
		}
	}
	return nil
}
