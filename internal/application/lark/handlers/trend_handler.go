package handlers

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/vadvisor"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
)

type TrendArgs struct {
	Days      int    `json:"days"`
	Interval  string `json:"interval"`
	ChartType string `json:"chart_type" cli:"play"`
	StartTime string `json:"start_time" cli:"st"`
	EndTime   string `json:"end_time" cli:"et"`
}

type trendHandler struct{}

var Trend trendHandler

func (trendHandler) ParseCLI(args []string) (TrendArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := TrendArgs{
		Days:      7,
		Interval:  "1d",
		ChartType: argMap["play"],
		StartTime: argMap["st"],
		EndTime:   argMap["et"],
	}
	if parsed.ChartType == "" {
		parsed.ChartType = "line"
	}
	if inputInterval := argMap["interval"]; inputInterval != "" {
		parsed.Interval = inputInterval
	}
	if daysStr := argMap["days"]; daysStr != "" {
		days, err := strconv.Atoi(daysStr)
		if err != nil || days <= 0 {
			parsed.Days = 30
		} else {
			parsed.Days = days
		}
	}
	return parsed, nil
}

func (trendHandler) ParseTool(raw string) (TrendArgs, error) {
	parsed := TrendArgs{
		Days:     7,
		Interval: "1d",
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return TrendArgs{}, err
	}
	if parsed.Days <= 0 {
		parsed.Days = 7
	}
	if parsed.Interval == "" {
		parsed.Interval = "1d"
	}
	switch parsed.ChartType {
	case "", "line", "pie", "bar":
	default:
		parsed.ChartType = "line"
	}
	if parsed.StartTime != "" && parsed.EndTime != "" {
		parsed.StartTime = normalizeRFC3339(parsed.StartTime)
		parsed.EndTime = normalizeRFC3339(parsed.EndTime)
	}
	return parsed, nil
}

func (trendHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "talkrate_get",
		Desc: "统计当前群聊的发言趋势，并生成趋势图",
		Params: arktools.NewParams("object").
			AddProp("days", &arktools.Prop{
				Type: "number",
				Desc: "统计近几天的发言趋势，默认 7",
			}).
			AddProp("interval", &arktools.Prop{
				Type: "string",
				Desc: "聚合间隔，例如 1d、12h、1h",
			}).
			AddProp("chart_type", &arktools.Prop{
				Type: "string",
				Desc: "图表类型，可选值：line、pie、bar，默认 line",
			}).
			AddProp("start_time", &arktools.Prop{
				Type: "string",
				Desc: "开始时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}).
			AddProp("end_time", &arktools.Prop{
				Type: "string",
				Desc: "结束时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}),
	}
}

func (trendHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg TrendArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	st, et := GetBackDays(arg.Days)
	if arg.StartTime != "" && arg.EndTime != "" {
		st, err = time.Parse(time.RFC3339, arg.StartTime)
		if err != nil {
			return err
		}
		et, err = time.Parse(time.RFC3339, arg.EndTime)
		if err != nil {
			return err
		}
	}
	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return fmt.Errorf("chat_id is required")
	}
	helper := &trendInternalHelper{
		days:     arg.Days,
		st:       st,
		et:       et,
		msgID:    currentMessageID(data),
		chatID:   chatID,
		interval: arg.Interval,
	}

	trend, err := helper.TrendByUser(ctx)
	if err != nil {
		return err
	}

	switch arg.ChartType {
	case "bar":
		err = helper.DrawTrendBar(ctx, trend, !metaData.Refresh)
	case "pie":
		err = helper.DrawTrendPie(ctx, trend, !metaData.Refresh)
	default:
		graph := vadvisor.NewMultiSeriesLineGraph[string, int64](ctx)
		graph.AddPointSeries(
			func(yield func(vadvisor.XYSUnit[string, int64]) bool) {
				for _, item := range trend {
					if item.Key == "你" {
						item.Key = "机器人"
					}
					if !yield(vadvisor.XYSUnit[string, int64]{
						X: item.Time,
						Y: item.Value,
						S: item.Key,
					}) {
						return
					}
				}
			},
		)
		title := fmt.Sprintf("[%s]水群频率表-%ddays", larkchat.GetChatName(ctx, chatID), arg.Days)
		cardContent := larkcard.NewCardBuildGraphHelper(graph).
			SetTitle(title).Build(ctx)
		err = sendCompatibleCard(ctx, data, metaData, cardContent, "", false)
	}

	return
}

type trendInternalHelper struct {
	days          int
	st, et        time.Time
	msgID, chatID string
	interval      string
}

func (h *trendInternalHelper) DrawTrendPie(ctx context.Context, trend history.TrendSeries, reply bool) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	graph := vadvisor.NewPieChartsGraphWithPlayer[string, int64]()
	for _, item := range trend {
		t, err := time.ParseInLocation(time.DateTime, item.Time, utils.UTC8Loc())
		if err != nil {
			return err
		}
		if item.Key == "你" {
			item.Key = "机器人"
		}
		graph.AddData(
			t.Format(time.DateOnly),
			&vadvisor.ValueUnit[string, int64]{
				XField:      t.Format(time.DateOnly),
				SeriesField: item.Key,
				YField:      item.Value,
			})

	}
	graph.BuildPlayer(ctx)
	title := fmt.Sprintf("[%s]水群频率表-%ddays", larkchat.GetChatName(ctx, h.chatID), h.days)
	cardContent := larkcard.NewCardBuildGraphHelper(graph).
		SetStartTime(h.st).
		SetEndTime(h.et).
		SetTitle(title).Build(ctx)
	if h.msgID == "" {
		return larkmsg.CreateMsgCard(ctx, cardContent, h.chatID)
	}
	if reply {
		return larkmsg.ReplyCard(ctx, cardContent, h.msgID, "", false)
	}
	return larkmsg.PatchCard(ctx, cardContent, h.msgID)
}

func (h *trendInternalHelper) DrawTrendBar(ctx context.Context, trend history.TrendSeries, reply bool) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	graph := vadvisor.NewBarChartsGraphWithPlayer[string, int64]()
	for _, item := range trend {
		t, err := time.ParseInLocation(time.DateTime, item.Time, utils.UTC8Loc())
		if err != nil {
			return err
		}
		if item.Key == "你" {
			item.Key = "机器人"
		}
		graph.AddData(
			t.Format(time.DateOnly),
			&vadvisor.ValueUnit[string, int64]{
				XField:      item.Key,
				SeriesField: item.Key,
				YField:      item.Value,
			},
		)
	}
	graph.SetDirection("horizontal").ReverseAxis()
	graph.SetSortFunc(func(a, b *vadvisor.ValueUnit[string, int64]) int {
		return cmp.Compare(b.YField, a.YField)
	})
	graph.BuildPlayer(ctx)
	title := fmt.Sprintf("[%s]水群频率表-%ddays", larkchat.GetChatName(ctx, h.chatID), h.days)
	cardContent := larkcard.NewCardBuildGraphHelper(graph).
		SetStartTime(h.st).
		SetEndTime(h.et).
		SetTitle(title).Build(ctx)
	if h.msgID == "" {
		return larkmsg.CreateMsgCard(ctx, cardContent, h.chatID)
	}
	if reply {
		return larkmsg.ReplyCard(ctx, cardContent, h.msgID, "", false)
	}
	return larkmsg.PatchCard(ctx, cardContent, h.msgID)
}

func (h *trendInternalHelper) TrendByUser(ctx context.Context) (trend history.TrendSeries, err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	trend, err = history.New(ctx).
		Query(
			osquery.Bool().
				Must(
					osquery.Term("chat_id", h.chatID),
					osquery.Range("create_time_v2").
						Gte(h.st.Format(time.RFC3339)).
						Lte(h.et.Format(time.RFC3339)),
				),
		).
		GetTrend(
			h.interval,
			"user_name",
		)
	return
}

func (h *trendInternalHelper) TrendRate(ctx context.Context, indexName, field string, size uint64) (singleDimAggs *history.SingleDimAggregate, err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	singleDimAggs = &history.SingleDimAggregate{}
	// 通过Opensearch统计发言数量
	req := osquery.Search().Query(
		osquery.Bool().
			Must(
				osquery.Term("chat_id", h.chatID),
				osquery.Range("create_time_v2").
					Gte(h.st.Format(time.RFC3339)).
					Lte(h.et.Format(time.RFC3339)),
			),
	).Size(0).Aggs(osquery.TermsAgg("dimension", field).Size(size))

	resp, err := opensearch.
		SearchData(
			ctx,
			indexName,
			req,
		)

	err = sonic.Unmarshal(resp.Aggregations, singleDimAggs)
	return
}

func GetBackDays(days int) (st, et time.Time) {
	st, et = time.Now().AddDate(0, 0, -1*days), time.Now()
	return
}

func (trendHandler) CommandDescription() string {
	return "查看发言趋势"
}

func (trendHandler) CommandExamples() []string {
	return []string{
		"/talkrate --days=7 --interval=1d",
		"/talkrate --play=bar --st=2026-03-01T00:00:00+08:00 --et=2026-03-07T23:59:59+08:00",
	}
}
