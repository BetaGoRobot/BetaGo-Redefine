package holiday

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const holidayToolResultKey = "holiday_tool_result"

// ========== 工具参数结构 ==========

type queryDateInfoArgs struct {
	Date string `json:"date"` // YYYY-MM-DD 格式，可选
}

type queryNextHolidayArgs struct {
	Date string `json:"date"` // YYYY-MM-DD 格式，可选
}

type queryNextWorkdayArgs struct {
	Date string `json:"date"` // YYYY-MM-DD 格式，可选
}

type queryYearHolidaysArgs struct {
	Year string `json:"year"` // YYYY 格式，可选
}

type queryTTSArgs struct {
	Type string `json:"type"` // holiday/next/tomorrow，可选
}

type batchQueryArgs struct {
	Dates []string `json:"dates"` // 日期数组，最多50个
}

// ========== 工具处理器 ==========

type (
	queryDateInfoHandler     struct{}
	queryNextHolidayHandler  struct{}
	queryNextWorkdayHandler  struct{}
	queryYearHolidaysHandler struct{}
	queryTTSHandler          struct{}
	batchQueryHandler        struct{}
	checkWorkdayHandler      struct{}
)

var (
	QueryDateInfo       queryDateInfoHandler
	QueryNextHoliday    queryNextHolidayHandler
	QueryNextWorkday    queryNextWorkdayHandler
	QueryYearHolidays   queryYearHolidaysHandler
	QueryTTS            queryTTSHandler
	BatchQuery          batchQueryHandler
	CheckWorkdayHandler checkWorkdayHandler
)

// ========== 注册工具 ==========

func RegisterTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, QueryDateInfo)
	xcommand.RegisterTool(ins, QueryNextHoliday)
	xcommand.RegisterTool(ins, QueryNextWorkday)
	xcommand.RegisterTool(ins, QueryYearHolidays)
	xcommand.RegisterTool(ins, QueryTTS)
	xcommand.RegisterTool(ins, BatchQuery)
	xcommand.RegisterTool(ins, CheckWorkdayHandler)
}

// ========== 工具1: 查询日期信息 ==========

func (queryDateInfoHandler) ParseTool(raw string) (queryDateInfoArgs, error) {
	parsed := queryDateInfoArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return queryDateInfoArgs{}, err
	}
	return parsed, nil
}

func (queryDateInfoHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "query_date_info",
		Desc: "查询指定日期的节假日信息，包括是否为节假日、周末、调休，以及节假日名称、薪资倍数等。不指定日期则查询今天。",
		Params: tools.NewParams("object").
			AddProp("date", &tools.Prop{
				Type: "string",
				Desc: "要查询的日期，格式 YYYY-MM-DD（如 2026-01-01）。不填则查询今天",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(holidayToolResultKey)
			return result
		},
	}
}

func (queryDateInfoHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args queryDateInfoArgs) error {
	date := strings.TrimSpace(args.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	info, err := GetService().GetDateInfo(ctx, date)
	if err != nil {
		return err
	}

	// 构建结果
	var result string
	dateType := "工作日"
	switch info.Type.Type {
	case 0:
		dateType = "工作日"
	case 1:
		dateType = "周末"
	case 2:
		dateType = "节假日"
	}

	result = fmt.Sprintf("📅 日期信息查询\n\n")
	result += fmt.Sprintf("日期: %s\n", date)
	result += fmt.Sprintf("类型: %s\n", dateType)

	weekDays := []string{"", "周一", "周二", "周三", "周四", "周五", "周六", "周日"}
	if info.Type.Week >= 1 && info.Type.Week <= 7 {
		result += fmt.Sprintf("星期: %s\n", weekDays[info.Type.Week])
	}

	if info.Holiday != nil && info.Holiday.Holiday {
		result += fmt.Sprintf("节假日: ✅ %s\n", info.Holiday.Name)
		if info.Holiday.Wage > 1 {
			result += fmt.Sprintf("薪资倍数: %d倍\n", info.Holiday.Wage)
		}
	}

	metaData.SetExtra(holidayToolResultKey, result)
	return nil
}

// ========== 工具2: 查询下一个节假日 ==========

func (queryNextHolidayHandler) ParseTool(raw string) (queryNextHolidayArgs, error) {
	parsed := queryNextHolidayArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return queryNextHolidayArgs{}, err
	}
	return parsed, nil
}

func (queryNextHolidayHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "query_next_holiday",
		Desc: "查询下一个节假日，包括节假日名称、日期、距离天数等。还可以查看是否有调休安排。不指定日期则从今天开始查询。",
		Params: tools.NewParams("object").
			AddProp("date", &tools.Prop{
				Type: "string",
				Desc: "起始日期，格式 YYYY-MM-DD。不填则从今天开始查询",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(holidayToolResultKey)
			return result
		},
	}
}

func (queryNextHolidayHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args queryNextHolidayArgs) error {
	date := strings.TrimSpace(args.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	info, err := GetService().GetNextHoliday(ctx, date)
	if err != nil {
		return err
	}

	// 构建结果
	result := fmt.Sprintf("🎊 下一个节假日\n\n")
	result += fmt.Sprintf("节假日: %s\n", info.Holiday.Name)
	result += fmt.Sprintf("日期: %s\n", info.Holiday.Date)

	// 计算距离天数
	now := time.Now()
	if holidayDate, err := time.Parse("2006-01-02", info.Holiday.Date); err == nil {
		days := int(holidayDate.Sub(now).Hours() / 24)
		if days > 0 {
			result += fmt.Sprintf("距离: %d 天\n", days)
		} else if days == 0 {
			result += fmt.Sprintf("就是今天！🎉\n")
		}
	}

	if info.Workday != nil {
		result += fmt.Sprintf("\n⚠️ 调休提醒\n")
		result += fmt.Sprintf("调休日期: %s\n", info.Workday.Date)
	}

	metaData.SetExtra(holidayToolResultKey, result)
	return nil
}

// ========== 工具3: 查询下一个工作日 ==========

func (queryNextWorkdayHandler) ParseTool(raw string) (queryNextWorkdayArgs, error) {
	parsed := queryNextWorkdayArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return queryNextWorkdayArgs{}, err
	}
	return parsed, nil
}

func (queryNextWorkdayHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "query_next_workday",
		Desc: "查询下一个工作日的日期和距离天数。工作日包括正常工作日和调休补班日。",
		Params: tools.NewParams("object").
			AddProp("date", &tools.Prop{
				Type: "string",
				Desc: "起始日期，格式 YYYY-MM-DD。不填则从今天开始查询",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(holidayToolResultKey)
			return result
		},
	}
}

func (queryNextWorkdayHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args queryNextWorkdayArgs) error {
	date := strings.TrimSpace(args.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	info, err := GetService().GetNextWorkday(ctx, date)
	if err != nil {
		return err
	}

	// 构建结果
	result := fmt.Sprintf("💼 下一个工作日\n\n")
	result += fmt.Sprintf("日期: %s\n", info.Workday.Date)

	typeName := "工作日"
	if info.Workday.Type == 1 {
		typeName = "周末"
	} else if info.Workday.Type == 2 {
		typeName = "节假日"
	}
	result += fmt.Sprintf("类型: %s\n", typeName)

	if info.Workday.Name != "" {
		result += fmt.Sprintf("说明: %s\n", info.Workday.Name)
	}

	if info.Workday.Rest > 0 {
		result += fmt.Sprintf("距离: %d 天\n", info.Workday.Rest)
	} else if info.Workday.Rest == 0 {
		result += fmt.Sprintf("就是今天！\n")
	}

	metaData.SetExtra(holidayToolResultKey, result)
	return nil
}

// ========== 工具4: 查询年度节假日 ==========

func (queryYearHolidaysHandler) ParseTool(raw string) (queryYearHolidaysArgs, error) {
	parsed := queryYearHolidaysArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return queryYearHolidaysArgs{}, err
	}
	return parsed, nil
}

func (queryYearHolidaysHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "query_year_holidays",
		Desc: "查询指定年份的所有节假日列表，包括元旦、春节、清明、五一、端午、中秋、国庆等法定节假日。",
		Params: tools.NewParams("object").
			AddProp("year", &tools.Prop{
				Type: "string",
				Desc: "年份，格式 YYYY（如 2026）。不填则查询今年",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(holidayToolResultKey)
			return result
		},
	}
}

func (queryYearHolidaysHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args queryYearHolidaysArgs) error {
	year := strings.TrimSpace(args.Year)
	if year == "" {
		year = fmt.Sprintf("%d", time.Now().Year())
	}

	info, err := GetService().GetYearHolidays(ctx, year)
	if err != nil {
		return err
	}

	// 构建结果
	result := fmt.Sprintf("📅 %s年节假日安排\n\n", year)

	count := 0
	holidaySorted := utils.SortMapToSlice(info.Holiday)
	for date, holiday := range holidaySorted {
		if holiday.Holiday {
			count++
			result += fmt.Sprintf("%d. **%s** - %s\n", count, holiday.Name, date)
			if holiday.Wage > 1 {
				result += fmt.Sprintf("   薪资倍数: %d倍\n", holiday.Wage)
			}
		}
	}

	if count == 0 {
		result += "暂无节假日数据\n"
	} else {
		result += fmt.Sprintf("\n共 %d 个节假日\n", count)
	}

	metaData.SetExtra(holidayToolResultKey, result)
	return nil
}

// ========== 工具5: 语音播报文本 ==========

func (queryTTSHandler) ParseTool(raw string) (queryTTSArgs, error) {
	parsed := queryTTSArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return queryTTSArgs{}, err
	}
	return parsed, nil
}

func (queryTTSHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "query_holiday_tts",
		Desc: "获取节假日相关的语音播报文本，适合语音助手场景。可以查询放假安排、下一个节假日、明天是否放假等。",
		Params: tools.NewParams("object").
			AddProp("type", &tools.Prop{
				Type: "string",
				Desc: "播报类型：holiday(放假安排)、next(下一个节假日)、tomorrow(明天放假吗)。不填默认为放假安排",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(holidayToolResultKey)
			return result
		},
	}
}

func (queryTTSHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args queryTTSArgs) error {
	ttsType := strings.TrimSpace(args.Type)
	if ttsType == "" {
		ttsType = "holiday"
	}

	tts, err := GetService().GetTTS(ctx, ttsType)
	if err != nil {
		return err
	}

	result := fmt.Sprintf("📢 %s\n\n%s", "节假日播报", tts)

	metaData.SetExtra(holidayToolResultKey, result)
	return nil
}

// ========== 工具6: 批量查询 ==========

func (batchQueryHandler) ParseTool(raw string) (batchQueryArgs, error) {
	parsed := batchQueryArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return batchQueryArgs{}, err
	}
	return parsed, nil
}

func (batchQueryHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "batch_query_holidays",
		Desc: "批量查询多个日期的节假日信息，最多支持50个日期。适合需要一次性查询多个日期的场景。",
		Params: tools.NewParams("object").
			AddProp("dates", &tools.Prop{
				Type: "array",
				Desc: "日期数组，格式 YYYY-MM-DD，最多50个。例如: [\"2026-01-01\", \"2026-02-10\"]",
			}).
			AddRequired("dates"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(holidayToolResultKey)
			return result
		},
	}
}

func (batchQueryHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args batchQueryArgs) error {
	if len(args.Dates) == 0 {
		return fmt.Errorf("dates cannot be empty")
	}

	if len(args.Dates) > 50 {
		return fmt.Errorf("too many dates, maximum is 50")
	}

	results, err := GetService().BatchGetDateInfo(ctx, args.Dates)
	if err != nil {
		return err
	}

	// 构建结果
	result := fmt.Sprintf("📅 批量查询结果（共 %d 个日期）\n\n", len(results))

	count := 0
	for date, info := range results {
		count++
		dateType := "工作日"
		if info.Type.Type == 1 {
			dateType = "周末"
		} else if info.Type.Type == 2 {
			dateType = "节假日"
		}

		result += fmt.Sprintf("%d. %s - %s", count, date, dateType)
		if info.Holiday != nil && info.Holiday.Holiday {
			result += fmt.Sprintf(" (%s)", info.Holiday.Name)
		}
		result += "\n"
	}

	metaData.SetExtra(holidayToolResultKey, result)
	return nil
}

// ========== 工具7: 检查是否工作日 ==========

func (checkWorkdayHandler) ParseTool(raw string) (queryDateInfoArgs, error) {
	parsed := queryDateInfoArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return queryDateInfoArgs{}, err
	}
	return parsed, nil
}

func (checkWorkdayHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "check_workday",
		Desc: "检查指定日期是否为工作日。工作日包括正常工作日和调休补班日，不包括周末和法定节假日。不指定日期则检查今天。",
		Params: tools.NewParams("object").
			AddProp("date", &tools.Prop{
				Type: "string",
				Desc: "要检查的日期，格式 YYYY-MM-DD。不填则检查今天",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(holidayToolResultKey)
			return result
		},
	}
}

func (checkWorkdayHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args queryDateInfoArgs) error {
	dateStr := strings.TrimSpace(args.Date)
	var checkDate time.Time
	var err error

	if dateStr == "" {
		checkDate = time.Now()
	} else {
		checkDate, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return fmt.Errorf("invalid date format: %w", err)
		}
	}

	isWorkday, err := GetService().IsWorkday(ctx, checkDate)
	if err != nil {
		return err
	}

	// 构建结果
	result := fmt.Sprintf("💼 工作日检查\n\n")
	result += fmt.Sprintf("日期: %s\n", checkDate.Format("2006-01-02"))

	if isWorkday {
		result += fmt.Sprintf("结果: ✅ 是工作日\n")
	} else {
		result += fmt.Sprintf("结果: ❌ 不是工作日\n")
	}

	metaData.SetExtra(holidayToolResultKey, result)
	return nil
}
