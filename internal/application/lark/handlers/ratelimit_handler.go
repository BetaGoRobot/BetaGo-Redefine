package handlers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/ratelimit"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
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
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", fmt.Sprintf("%v", data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	targetChatID := arg.ChatID
	if targetChatID == "" {
		targetChatID = currentChatID(data, metaData)
	}
	cardData, err := ratelimit.BuildStatsCardJSON(ctx, targetChatID)
	if err != nil {
		return err
	}
	return sendCompatibleCardJSON(ctx, data, metaData, cardData, "_ratelimitStats", false)
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
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

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

	return sendCompatibleCard(ctx, data, metaData, cardContent, "_ratelimitList", false)
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
