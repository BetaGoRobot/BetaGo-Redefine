package handlers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/ratelimit"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
)

// ==========================================
// 频控诊断命令
// ==========================================

type RateLimitStatsArgs struct {
	ChatID string `json:"chat_id"`
}

type RateLimitListArgs struct{}

type rateLimitStatsHandler struct{}
type rateLimitListHandler struct{}

var RateLimitStats rateLimitStatsHandler
var RateLimitList rateLimitListHandler

func (rateLimitStatsHandler) ParseCLI(args []string) (RateLimitStatsArgs, error) {
	argMap, _ := parseArgs(args...)
	return RateLimitStatsArgs{
		ChatID: argMap["chat_id"],
	}, nil
}

func (rateLimitStatsHandler) ParseTool(raw string) (RateLimitStatsArgs, error) {
	parsed := RateLimitStatsArgs{}
	if raw == "" {
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return RateLimitStatsArgs{}, err
	}
	return parsed, nil
}

func (rateLimitStatsHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "ratelimit_stats_get",
		Desc: "查看某个群聊的频控统计与诊断信息",
		Params: arktools.NewParams("object").
			AddProp("chat_id", &arktools.Prop{
				Type: "string",
				Desc: "目标群聊 ID，不填则使用当前群聊",
			}),
	}
}

func (rateLimitStatsHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg RateLimitStatsArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(fmt.Sprintf("%v", data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	targetChatID := arg.ChatID
	if targetChatID == "" {
		targetChatID = *data.Event.Message.ChatId
	}

	// 获取频控器和 metrics
	limiter := ratelimit.Get()
	metrics := ratelimit.GetMetrics()

	// 获取频控统计
	stats := limiter.GetStats(ctx, targetChatID)

	// 获取内存 metrics
	chatMetrics := metrics.GetChatStats(targetChatID)

	// 构建表格数据
	lines := make([]map[string]string, 0)

	// 基础信息
	lines = append(lines, map[string]string{
		"title1": "会话ID",
		"title2": targetChatID,
		"title3": "-",
		"title4": "-",
	})

	lines = append(lines, map[string]string{
		"title1": "历史总发送",
		"title2": strconv.FormatInt(stats.TotalMessagesSent, 10),
		"title3": "近24小时",
		"title4": strconv.FormatInt(stats.TotalMessages24h, 10),
	})

	lines = append(lines, map[string]string{
		"title1": "近1小时发送",
		"title2": strconv.FormatInt(stats.TotalMessages1h, 10),
		"title3": "活跃度评分",
		"title4": fmt.Sprintf("%.2f", stats.CurrentActivityScore),
	})

	lines = append(lines, map[string]string{
		"title1": "爆发因子",
		"title2": fmt.Sprintf("%.2f", stats.CurrentBurstFactor),
		"title3": "冷却等级",
		"title4": strconv.Itoa(stats.CooldownLevel),
	})

	if !stats.CooldownUntil.IsZero() {
		remaining := stats.CooldownUntil.Sub(time.Now()).Seconds()
		if remaining > 0 {
			lines = append(lines, map[string]string{
				"title1": "冷却剩余",
				"title2": fmt.Sprintf("%.0f 秒", remaining),
				"title3": "-",
				"title4": "-",
			})
		}
	}

	// 如果有内存 metrics，添加
	if chatMetrics != nil {
		lines = append(lines, map[string]string{
			"title1": "---",
			"title2": "诊断 Metrics",
			"title3": "---",
			"title4": "---",
		})

		lines = append(lines, map[string]string{
			"title1": "检查次数",
			"title2": strconv.FormatInt(chatMetrics.ChecksTotal, 10),
			"title3": "通过次数",
			"title4": strconv.FormatInt(chatMetrics.AllowedTotal, 10),
		})

		lines = append(lines, map[string]string{
			"title1": "拒绝次数",
			"title2": strconv.FormatInt(chatMetrics.BlockedTotal, 10),
			"title3": "实际发送",
			"title4": strconv.FormatInt(chatMetrics.MessagesSentTotal, 10),
		})

		blockedRate := 0.0
		if chatMetrics.ChecksTotal > 0 {
			blockedRate = float64(chatMetrics.BlockedTotal) / float64(chatMetrics.ChecksTotal) * 100
		}
		allowedRate := 0.0
		if chatMetrics.ChecksTotal > 0 {
			allowedRate = float64(chatMetrics.AllowedTotal) / float64(chatMetrics.ChecksTotal) * 100
		}
		lines = append(lines, map[string]string{
			"title1": "拒绝率",
			"title2": fmt.Sprintf("%.1f%%", blockedRate),
			"title3": "通过率",
			"title4": fmt.Sprintf("%.1f%%", allowedRate),
		})

		lines = append(lines, map[string]string{
			"title1": "冷却中",
			"title2": strconv.FormatBool(chatMetrics.InCooldown),
			"title3": "最后更新",
			"title4": chatMetrics.LastUpdated.Format("15:04:05"),
		})
	}

	// 最近发送记录
	if len(stats.RecentSends) > 0 {
		lines = append(lines, map[string]string{
			"title1": "---",
			"title2": "最近发送记录",
			"title3": "---",
			"title4": "---",
		})

		// 显示最近5条
		start := 0
		if len(stats.RecentSends) > 5 {
			start = len(stats.RecentSends) - 5
		}
		for i := start; i < len(stats.RecentSends); i++ {
			send := stats.RecentSends[i]
			lines = append(lines, map[string]string{
				"title1": fmt.Sprintf("#%d", i+1),
				"title2": string(send.TriggerType),
				"title3": send.Timestamp.Format("15:04:05"),
				"title4": "-",
			})
		}
	}

	cardContent := larktpl.NewCardContent(
		ctx,
		larktpl.FourColSheetTemplate,
	).
		AddVariable("title1", "项").
		AddVariable("title2", "值").
		AddVariable("title3", "项2").
		AddVariable("title4", "值2").
		AddVariable("table_raw_array_1", lines)

	err = larkmsg.ReplyCard(ctx, cardContent, *data.Event.Message.MessageId, "_ratelimitStats", false)
	return err
}

func (rateLimitListHandler) ParseCLI(args []string) (RateLimitListArgs, error) {
	return RateLimitListArgs{}, nil
}

func (rateLimitListHandler) ParseTool(raw string) (RateLimitListArgs, error) {
	if err := parseEmptyToolArgs(raw); err != nil {
		return RateLimitListArgs{}, err
	}
	return RateLimitListArgs{}, nil
}

func (rateLimitListHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name:   "ratelimit_list",
		Desc:   "列出所有会话的频控统计概览",
		Params: arktools.NewParams("object"),
	}
}

func (rateLimitListHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg RateLimitListArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	metrics := ratelimit.GetMetrics()
	allStats := metrics.GetAllChatStats()

	lines := make([]map[string]string, 0)
	lines = append(lines, map[string]string{
		"title1": "会话ID",
		"title2": "检查",
		"title3": "通过/拒绝",
		"title4": "拒绝率",
	})

	for chatID, stats := range allStats {
		if stats == nil {
			continue
		}

		blockedRate := 0.0
		if stats.ChecksTotal > 0 {
			blockedRate = float64(stats.BlockedTotal) / float64(stats.ChecksTotal) * 100
		}

		// 截断会话ID显示
		displayID := chatID
		if len(displayID) > 12 {
			displayID = displayID[:12] + "..."
		}

		lines = append(lines, map[string]string{
			"title1": displayID,
			"title2": strconv.FormatInt(stats.ChecksTotal, 10),
			"title3": fmt.Sprintf("%d/%d", stats.AllowedTotal, stats.BlockedTotal),
			"title4": fmt.Sprintf("%.1f%%", blockedRate),
		})
	}

	if len(lines) == 1 {
		lines = append(lines, map[string]string{
			"title1": "暂无数据",
			"title2": "-",
			"title3": "-",
			"title4": "-",
		})
	}

	cardContent := larktpl.NewCardContent(
		ctx,
		larktpl.FourColSheetTemplate,
	).
		AddVariable("title1", "会话").
		AddVariable("title2", "检查").
		AddVariable("title3", "通过/拒绝").
		AddVariable("title4", "拒绝率").
		AddVariable("table_raw_array_1", lines)

	err = larkmsg.ReplyCard(ctx, cardContent, *data.Event.Message.MessageId, "_ratelimitList", false)
	return err
}

func (rateLimitStatsHandler) CommandDescription() string {
	return "查看频控详情"
}

func (rateLimitListHandler) CommandDescription() string {
	return "查看频控概览"
}

func (rateLimitStatsHandler) CommandExamples() []string {
	return []string{
		"/ratelimit stats",
		"/ratelimit stats --chat_id=oc_xxx",
	}
}

func (rateLimitListHandler) CommandExamples() []string {
	return []string{
		"/ratelimit list",
	}
}
