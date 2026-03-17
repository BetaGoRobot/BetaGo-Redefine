package handlers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/aktool"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/vadvisor"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

type GoldArgs struct {
	Days      int    `json:"days" cli:"d"`
	Hours     int    `json:"hours" cli:"h"`
	StartTime string `json:"start_time" cli:"st"`
	EndTime   string `json:"end_time" cli:"et"`
}

type ZhAStockArgs struct {
	Code      string `json:"code"`
	Days      int    `json:"days"`
	StartTime string `json:"start_time" cli:"st"`
	EndTime   string `json:"end_time" cli:"et"`
}

type (
	goldHandler     struct{}
	zhAStockHandler struct{}
)

var (
	Gold     goldHandler
	ZhAStock zhAStockHandler
)

func (goldHandler) ParseCLI(args []string) (GoldArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := GoldArgs{
		StartTime: argMap["st"],
		EndTime:   argMap["et"],
	}
	if daysStr := argMap["d"]; daysStr != "" {
		if days, err := strconv.Atoi(daysStr); err == nil {
			parsed.Days = days
		}
	}
	if hoursStr := argMap["h"]; hoursStr != "" {
		if hours, err := strconv.Atoi(hoursStr); err == nil {
			parsed.Hours = hours
		}
	}
	return parsed, nil
}

func (goldHandler) ParseTool(raw string) (GoldArgs, error) {
	parsed := GoldArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return GoldArgs{}, err
	}
	if parsed.StartTime != "" && parsed.EndTime != "" {
		parsed.StartTime = normalizeRFC3339(parsed.StartTime)
		parsed.EndTime = normalizeRFC3339(parsed.EndTime)
	}
	return parsed, nil
}

func (goldHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "gold_price_get",
		Desc: "搜索指定时间范围内的金价变化情况，可选相对时间天或小时，也可以指定时间范围",
		Params: arktools.NewParams("object").
			AddProp("start_time", &arktools.Prop{
				Type: "string",
				Desc: "开始时间，默认可以不穿，格式为RFC3339: 2006-01-02T15:04:05Z07:00",
			}).
			AddProp("end_time", &arktools.Prop{
				Type: "string",
				Desc: "结束时间，默认可以不传，格式为RFC3339: 2006-01-02T15:04:05Z07:00",
			}).
			AddProp("hours", &arktools.Prop{
				Type: "number",
				Desc: "查询的小时数，默认1小时",
			}).
			AddProp("days", &arktools.Prop{
				Type: "number",
				Desc: "查询的天数，默认30天",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("gold_result")
			return result
		},
	}
}

func (goldHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg GoldArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	var (
		cardContent *larktpl.TemplateCardContent
		defaultDays = 30
		llmResult   string // 为LLM准备的输出
		st, et      time.Time
	)
	defer func() {
		if err != nil {
			metaData.SetExtra("gold_result", "执行失败，错误原因"+err.Error())
		} else {
			if llmResult != "" {
				metaData.SetExtra("gold_result", llmResult)
			} else {
				metaData.SetExtra("gold_result", "执行成功")
			}
		}
	}()
	// 如果有st，et的配置，用st，et的配置来覆盖
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

	if arg.Hours > 0 {
		if st.IsZero() || et.IsZero() {
			hoursInt := arg.Hours
			st = time.Now().Add(time.Duration(-1*hoursInt) * time.Hour)
			et = time.Now()
		}

		llmResult, cardContent, err = GetRealtimeGoldPriceGraph(ctx, st, et)
		if err != nil {
			return err
		}
	} else if arg.Days > 0 {
		days := arg.Days
		if st.IsZero() || et.IsZero() {
			if days <= 0 {
				days = defaultDays
			}
			st = time.Now().AddDate(0, 0, -1*days)
			et = time.Now()
		}
		if days == 1 { // 1 天应该用实时数据.
			llmResult, cardContent, err = GetRealtimeGoldPriceGraph(ctx, st, et)
			if err != nil {
				return err
			}
		} else {
			llmResult, cardContent, err = GetHistoryGoldGraph(ctx, st, et)
			if err != nil {
				return err
			}
		}
	} else {
		st = time.Now().AddDate(0, 0, -1*defaultDays)
		et = time.Now()
		llmResult, cardContent, err = GetHistoryGoldGraph(ctx, st, et)
		if err != nil {
			return err
		}
	}

	return sendCompatibleCard(ctx, data, metaData, cardContent, "", false)
}

func (zhAStockHandler) ParseCLI(args []string) (ZhAStockArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := ZhAStockArgs{
		Code:      argMap["code"],
		Days:      1,
		StartTime: argMap["st"],
		EndTime:   argMap["et"],
	}
	if parsed.Code == "" {
		return ZhAStockArgs{}, fmt.Errorf("stock code is required: %w", xerror.ErrArgsIncompelete)
	}
	if daysStr := argMap["days"]; daysStr != "" {
		days, err := strconv.Atoi(daysStr)
		if err != nil || days <= 0 {
			parsed.Days = 1
		} else {
			parsed.Days = days
		}
	}
	return parsed, nil
}

func (zhAStockHandler) ParseTool(raw string) (ZhAStockArgs, error) {
	parsed := ZhAStockArgs{Days: 1}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ZhAStockArgs{}, err
	}
	if parsed.Code == "" {
		return ZhAStockArgs{}, fmt.Errorf("stock code is required: %w", xerror.ErrArgsIncompelete)
	}
	if parsed.Days <= 0 {
		parsed.Days = 1
	}
	if parsed.StartTime != "" && parsed.EndTime != "" {
		parsed.StartTime = normalizeRFC3339(parsed.StartTime)
		parsed.EndTime = normalizeRFC3339(parsed.EndTime)
	}
	return parsed, nil
}

func (zhAStockHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "stock_zh_a_get",
		Desc: "查询沪深 A 股指定股票的近期价格走势图",
		Params: arktools.NewParams("object").
			AddProp("code", &arktools.Prop{
				Type: "string",
				Desc: "A 股股票代码，例如 600519 或 000001",
			}).
			AddProp("days", &arktools.Prop{
				Type: "number",
				Desc: "查询近几天的数据，默认 1",
			}).
			AddProp("start_time", &arktools.Prop{
				Type: "string",
				Desc: "开始时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}).
			AddProp("end_time", &arktools.Prop{
				Type: "string",
				Desc: "结束时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}).
			AddRequired("code"),
	}
}

func (zhAStockHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ZhAStockArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	var (
		days                  = arg.Days
		defaultDays           = 1
		st, et      time.Time = time.Now().AddDate(0, 0, -1*defaultDays), time.Now()
	)
	if days <= 0 {
		days = defaultDays
		st, et = time.Now().AddDate(0, 0, -1*days), time.Now()
	}

	// 如果有st，et的配置，用st，et的配置来覆盖
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
	graph := vadvisor.NewMultiSeriesLineGraph[string, float64](ctx)
	stockPrice, err := aktool.GetStockPriceRT(ctx, arg.Code)
	if err != nil {
		return err
	}
	stockName, err := aktool.GetStockSymbolInfo(ctx, arg.Code)
	if err != nil {
		return err
	}
	graph.AddPointSeries(
		func(yield func(vadvisor.XYSUnit[string, float64]) bool) {
			for _, price := range stockPrice {
				t, err := time.ParseInLocation(time.DateTime, price.DateTime, utils.UTC8Loc())
				if err != nil {
					return
				}
				if t.Before(st) || t.After(et) {
					continue
				}

				if !yield(vadvisor.XYSUnit[string, float64]{X: t.In(utils.UTC8Loc()).Format(time.DateTime), Y: utils.Must2Float(price.Open), S: "开盘"}) {
					return
				}
				if !yield(vadvisor.XYSUnit[string, float64]{X: t.In(utils.UTC8Loc()).Format(time.DateTime), Y: utils.Must2Float(price.Close), S: "收盘"}) {
					return
				}
				if !yield(vadvisor.XYSUnit[string, float64]{X: t.In(utils.UTC8Loc()).Format(time.DateTime), Y: utils.Must2Float(price.High), S: "最高"}) {
					return
				}
				if !yield(vadvisor.XYSUnit[string, float64]{X: t.In(utils.UTC8Loc()).Format(time.DateTime), Y: utils.Must2Float(price.Low), S: "最低"}) {
					return
				}
			}
		},
	)
	cardContent := larkcard.NewCardBuildGraphHelper(graph).
		SetTitle(fmt.Sprintf("沪A-[%s]%s-近<%d>天", arg.Code, stockName, days)).
		SetStartTime(st).
		SetEndTime(et).
		Build(ctx)
	return sendCompatibleCard(ctx, data, metaData, cardContent, "", false)
}

func GetHistoryGoldGraph(ctx context.Context, st, et time.Time) (string, *larktpl.TemplateCardContent, error) {
	_, span := otel.Start(ctx)
	defer span.End()

	logs.L().Ctx(ctx).Info("GetHistoryGoldGraph", zap.String("st", st.Format(time.RFC3339)), zap.String("et", et.Format(time.RFC3339)))
	graph := vadvisor.NewMultiSeriesLineGraph[string, float64](ctx)
	goldPrices, err := aktool.GetHistoryGoldPrice(ctx)
	if err != nil {
		return "", nil, err
	}
	graph.
		AddPointSeries(
			func(yield func(vadvisor.XYSUnit[string, float64]) bool) {
				for _, price := range goldPrices {
					t, err := time.Parse("2006-01-02T00:00:00.000", price.Date)
					if err != nil {
						return
					}
					if t.Before(st) || t.After(et) {
						continue
					}
					d := t.Format(time.DateOnly)
					if !yield(vadvisor.XYSUnit[string, float64]{X: d, Y: price.Close, S: "收盘价"}) ||
						!yield(vadvisor.XYSUnit[string, float64]{X: d, Y: price.Open, S: "开盘价"}) ||
						!yield(vadvisor.XYSUnit[string, float64]{X: d, Y: price.High, S: "最高价"}) ||
						!yield(vadvisor.XYSUnit[string, float64]{X: d, Y: price.Low, S: "最低价"}) {
						return
					}
				}
			},
		)

	// 历史模式拉不到当天的，判断一下有没有涵盖当天
	if et.Year() == time.Now().Year() && et.YearDay() == time.Now().YearDay() {
		// 有当天，我们把当天的数据补进去.
		res, err := aktool.GetRealtimeGoldPrice(ctx)
		if err != nil {
			// 弱依赖，错误可以丢弃
			logs.L().Ctx(ctx).Warn("get today's gold price failed.")
		}
		// 计算开、收、最高、最低

		if len(res) > 0 {
			resultMap := map[string]*vadvisor.XYSUnit[string, float64]{
				"开盘价": {X: time.Now().Format(time.DateOnly), Y: res[0].Price, S: "开盘价"},
				"收盘价": {X: time.Now().Format(time.DateOnly), Y: res[len(res)-1].Price, S: "收盘价"},
				"最高价": {X: time.Now().Format(time.DateOnly), Y: res[0].Price, S: "最高价"},
				"最低价": {X: time.Now().Format(time.DateOnly), Y: res[0].Price, S: "最低价"},
			}

			for _, price := range res {
				dStr := time.Now().Format(time.DateOnly) + " " + price.Time
				t, err := time.ParseInLocation(time.DateTime, dStr, utils.UTC8Loc())
				if err != nil {
					continue
				}
				if t.Before(st) || t.After(et) {
					continue
				}
				if price.Price > resultMap["最高价"].Y {
					resultMap["最高价"].Y = price.Price
				}
				if price.Price < resultMap["最低价"].Y {
					resultMap["最低价"].Y = price.Price
				}
			}
			for _, v := range resultMap {
				graph.AddData(v.X, v.Y, v.S)
				graph.UpdateMinMax(v.Y)
			}
		}
	}
	card := larkcard.NewCardBuildGraphHelper(graph).
		SetTitle("沪金所价格数据").
		SetStartTime(st).
		SetEndTime(et).
		Build(ctx)
	return goldPrices.ToLLMTable(), card, nil
}

func GetRealtimeGoldPriceGraph(ctx context.Context, st, et time.Time) (string, *larktpl.TemplateCardContent, error) {
	graph := vadvisor.NewMultiSeriesLineGraph[string, float64](ctx)
	goldPrice, err := aktool.GetRealtimeGoldPrice(ctx)
	if err != nil {
		return "", nil, err
	}
	graph.
		AddPointSeries(
			func(yield func(vadvisor.XYSUnit[string, float64]) bool) {
				for _, price := range goldPrice {
					dStr := time.Now().Format(time.DateOnly) + " " + price.Time
					t, err := time.ParseInLocation(time.DateTime, dStr, utils.UTC8Loc())
					if err != nil {
						return
					}
					if t.Before(st) || t.After(et) {
						continue
					}
					if !yield(vadvisor.XYSUnit[string, float64]{X: t.Format(time.TimeOnly), Y: price.Price, S: price.Kind}) {
						return
					}
				}
			},
		)

	card := larkcard.NewCardBuildGraphHelper(graph).
		SetTitle("沪金所价格数据").
		SetStartTime(st).
		SetEndTime(et).
		Build(ctx)
	return goldPrice.ToLLMTable(), card, nil
}

// func init() {
// 	params := tools.NewParameters("object").
// 		AddProperty("start_time", &tools.Property{
// 			Type:        "string",
// 			Description: "开始时间，默认可以不穿，格式为YYYY-MM-DD HH:MM:SS",
// 		}).
// 		AddProperty("end_time", &tools.Property{
// 			Type:        "string",
// 			Description: "结束时间，默认可以不传，格式为YYYY-MM-DD HH:MM:SS",
// 		}).
// 		AddProperty("hours", &tools.Property{
// 			Type:        "number",
// 			Description: "查询的小时数，默认1小时",
// 		}).
// 		AddProperty("days", &tools.Property{
// 			Type:        "number",
// 			Description: "查询的天数，默认30天",
// 		})
// 	fcu := tools.NewFunctionCallUnit().
// 		Name("gold_price_get").Desc("搜索指定时间范围内的金价变化情况，可选相对时间天或小时，也可以指定时间范围").Params(params).Func(goldWrap)
// 	tools.M().Add(fcu)
// }

// func goldWrap(ctx context.Context, meta *tools.FunctionCallMeta, args string) (any, error) {
// 	s := struct {
// 		StartTime string `json:"start_time"`
// 		EndTime   string `json:"end_time"`
// 		Days      *int   `json:"days"`
// 		Hours     *int   `json:"hours"`
// 	}{}
// 	err := utils.UnmarshallStringPre(args, &s)
// 	if err != nil {
// 		return nil, err
// 	}
// 	argsSlice := make([]string, 0)
// 	if s.Days != nil && *s.Days > 0 {
// 		argsSlice = append(argsSlice, "--d", strconv.Itoa(*s.Days))
// 	}
// 	if s.Hours != nil && *s.Hours > 0 {
// 		argsSlice = append(argsSlice, "--h", strconv.Itoa(*s.Hours))
// 	}
// 	if s.StartTime != "" {
// 		argsSlice = append(argsSlice, "--st="+s.StartTime)
// 	}
// 	if s.EndTime != "" {
// 		argsSlice = append(argsSlice, "--et="+s.EndTime)
// 	}
// 	metaData := xhandler.NewBaseMetaDataWithChatIDOpenID(ctx, meta.ChatID, meta.OpenID)
// 	if err := GoldHandler(ctx, meta.LarkData, metaData, argsSlice...); err != nil {
// 		return nil, err
// 	}
// 	return goption.Of(metaData.GetExtra("gold_result")).ValueOr("执行完成但没有结果"), nil
// }

func (goldHandler) CommandDescription() string {
	return "查看金价走势"
}

func (zhAStockHandler) CommandDescription() string {
	return "查看 A 股走势"
}

func (goldHandler) CommandExamples() []string {
	return []string{
		"/stock gold --d=7",
		"/stock gold --h=12",
	}
}

func (zhAStockHandler) CommandExamples() []string {
	return []string{
		"/stock zh_a --code=600519",
		"/stock zh_a --code=000001 --days=5",
	}
}
