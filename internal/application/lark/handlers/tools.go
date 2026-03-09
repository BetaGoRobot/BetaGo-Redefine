package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	todoapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/bytedance/gg/goption"
	"github.com/bytedance/gg/gresult"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func larktools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := tools.New[larkim.P2MessageReceiveV1]().WebSearch()
	musicSearch(ins)
	oneWordTool(ins)
	muteBot(ins)
	goldReport(ins)
	stockZhATool(ins)
	revert(ins)
	hybridSearch(ins)
	trendReportTool(ins)
	wordCloudTool(ins)
	configTools(ins)
	featureTools(ins)
	wordTools(ins)
	replyTools(ins)
	imageTools(ins)
	rateLimitTools(ins)
	sendMessageTool(ins)
	todoapp.RegisterTools(ins)
	return ins
}

func hybridSearch(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("keywords", &tools.Prop{
			Type: "string",
			Desc: "需要检索的关键词列表,逗号隔开",
			// Items: []*tools.Prop{ // sb方舟，用array的在1.8及以上不支持function call，回退到string就可以，傻逼。
			// 	{
			// 		Type: "string",
			// 		Desc: "关键词",
			// 	},
			// },
		}).
		AddProp("user_id", &tools.Prop{
			Type: "string",
			Desc: "用户ID",
		}).
		AddProp("start_time", &tools.Prop{
			Type: "string",
			Desc: "开始时间，格式为YYYY-MM-DD HH:MM:SS",
		}).
		AddProp("end_time", &tools.Prop{
			Type: "string",
			Desc: "结束时间，格式为YYYY-MM-DD HH:MM:SS",
		}).
		AddProp("top_k", &tools.Prop{
			Type: "number",
			Desc: "返回的结果数量",
		}).
		AddRequired("keywords")
	ins.Add(unit.Name("search_history").Desc("根据输入的关键词搜索相关的历史对话记录").
		Params(params).Func(hybridSearchWrap))
}

type SearchArgs struct {
	Keywords  string `json:"keywords"`
	TopK      int    `json:"top_k"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	UserID    string `json:"user_id"`
}

func hybridSearchWrap(ctx context.Context, argStr string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	args := &SearchArgs{}
	err := sonic.UnmarshalString(argStr, &args)
	if err != nil {
		return gresult.Err[string](err)
	}
	res, err := history.HybridSearch(ctx,
		history.HybridSearchRequest{
			QueryText: strings.Split(args.Keywords, ","),
			TopK:      args.TopK,
			UserID:    args.UserID,
			ChatID:    meta.ChatID,
			StartTime: args.StartTime,
			EndTime:   args.EndTime,
		}, ark_dal.EmbeddingText)
	if err != nil {
		return gresult.Err[string](err)
	}
	return gresult.OK(utils.MustMarshalString(res))
}

func musicSearch(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("type", &tools.Prop{
			Type: "string",
			Desc: "搜索类型，可选值：song、album。默认 song",
		}).
		AddProp("keywords", &tools.Prop{
			Type: "string",
			Desc: "音乐搜索的关键词, 多个关键词之间用空格隔开",
		}).AddRequired("keywords")
	ins.Add(unit.Name("music_search").Desc("根据输入的关键词搜索相关的音乐并发送卡片").Params(params).Func(musicSearchWrap))
}

func muteBot(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("time", &tools.Prop{
			Type: "string",
			Desc: "禁言的时间 duration 格式, 例如 3m 表示禁言三分钟",
		}).AddProp("cancel", &tools.Prop{
		Type: "boolean",
		Desc: "是否取消禁言, 默认为 false",
	})
	ins.Add(unit.Name("mute_robot").Desc("为机器人设置或解除禁言.当用户要求机器人说话时，可以先尝试调用此函数取消禁言。当用户要求机器人闭嘴或者不要说话时，需要调用此函数设置禁言").
		Params(params).Func(muteWrap))
}

func goldReport(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("start_time", &tools.Prop{
			Type: "string",
			Desc: "开始时间，默认可以不穿，格式为RFC3339: 2006-01-02T15:04:05Z07:00",
		}).
		AddProp("end_time", &tools.Prop{
			Type: "string",
			Desc: "结束时间，默认可以不传，格式为RFC3339: 2006-01-02T15:04:05Z07:00",
		}).
		AddProp("hours", &tools.Prop{
			Type: "number",
			Desc: "查询的小时数，默认1小时",
		}).
		AddProp("days", &tools.Prop{
			Type: "number",
			Desc: "查询的天数，默认30天",
		})
	ins.Add(unit.Name("gold_price_get").
		Desc("搜索指定时间范围内的金价变化情况，可选相对时间天或小时，也可以指定时间范围").
		Params(params).Func(goldWrap))
}

func revert(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	params := tools.NewParams("object")
	fcu := tools.NewUnit[larkim.P2MessageReceiveV1]().
		Name("revert_message").Desc("可以撤回指定消息,调用时不需要任何参数，工具会判断要撤回的消息是什么，并且返回撤回的结果。如果不是机器人发出的消息,是不能撤回的").Params(params).Func(revertWrap)
	ins.Add(fcu)
}

func musicSearchWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Type     string `json:"type"`
		Keywords string `json:"keywords"`
	}{}
	err := utils.UnmarshalStringPre(args, &s)
	if err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 2)
	argsSlice = appendStringArg(argsSlice, "type", s.Type)
	argsSlice = append(argsSlice, s.Keywords)
	return callHandlerTool(ctx, meta, MusicSearchHandler, argsSlice...)
}

func muteWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Time   string `json:"time"`
		Cancel bool   `json:"cancel"`
	}{}
	err := utils.UnmarshalStringPre(args, &s)
	if err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0)
	if s.Cancel {
		argsSlice = append(argsSlice, "--cancel")
	}
	if s.Time != "" {
		argsSlice = append(argsSlice, "--t="+s.Time)
	}
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, meta.ChatID, meta.UserID)
	if err := MuteHandler(ctx, meta.Data, metaData, argsSlice...); err != nil {
		return gresult.Err[string](err)
	}
	return gresult.Of(goption.Of(metaData.GetExtra("mute_result")).ValueOr("执行完成但没有结果"), nil)
}

func goldWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		Days      *int   `json:"days"`
		Hours     *int   `json:"hours"`
	}{}
	err := utils.UnmarshalStringPre(args, &s)
	if err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0)
	if s.Days != nil && *s.Days > 0 {
		argsSlice = append(argsSlice, "--d="+strconv.Itoa(*s.Days))
	}
	if s.Hours != nil && *s.Hours > 0 {
		argsSlice = append(argsSlice, "--h="+strconv.Itoa(*s.Hours))
	}
	if s.StartTime != "" {
		argsSlice = append(argsSlice, "--st="+s.StartTime)
	}
	if s.EndTime != "" {
		argsSlice = append(argsSlice, "--et="+s.EndTime)
	}
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, meta.ChatID, meta.UserID)
	if err := GoldHandler(ctx, meta.Data, metaData, argsSlice...); err != nil {
		return gresult.Err[string](err)
	}

	return gresult.Of(goption.Of(metaData.GetExtra("gold_result")).ValueOr("执行完成但没有结果"), nil)
}

func revertWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Time   string `json:"time"`
		Cancel bool   `json:"cancel"`
	}{}
	err := utils.UnmarshalStringPre(args, &s)
	if err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0)
	if s.Cancel {
		argsSlice = append(argsSlice, "--cancel")
	}
	if s.Time != "" {
		argsSlice = append(argsSlice, "--t="+s.Time)
	}
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, meta.ChatID, meta.UserID)
	if err := DebugRevertHandler(ctx, meta.Data, metaData, argsSlice...); err != nil {
		return gresult.Err[string](err)
	}
	return gresult.Of(goption.Of(metaData.GetExtra("revert_result")).ValueOr("执行完成但没有结果"), nil)
}

type larkToolHandler func(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) error

func callHandlerTool(ctx context.Context, meta tools.FCMeta[larkim.P2MessageReceiveV1], handler larkToolHandler, args ...string) gresult.R[string] {
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, meta.ChatID, meta.UserID)
	if err := handler(ctx, meta.Data, metaData, args...); err != nil {
		return gresult.Err[string](err)
	}
	return gresult.OK("执行成功")
}

func appendStringArg(args []string, key, value string) []string {
	if value == "" {
		return args
	}
	return append(args, "--"+key+"="+value)
}

func appendBoolArg(args []string, key string, enabled bool) []string {
	if !enabled {
		return args
	}
	return append(args, "--"+key)
}

func appendIntArg(args []string, key string, value *int) []string {
	if value == nil {
		return args
	}
	return append(args, "--"+key+"="+strconv.Itoa(*value))
}

func normalizeRFC3339(value string) string {
	if value == "" {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return value
	}
	if t, err := time.ParseInLocation(time.DateTime, value, utils.UTC8Loc()); err == nil {
		return t.Format(time.RFC3339)
	}
	return value
}

func normalizeDateTime(value string) string {
	if value == "" {
		return ""
	}
	if _, err := time.ParseInLocation(time.DateTime, value, utils.UTC8Loc()); err == nil {
		return value
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.In(utils.UTC8Loc()).Format(time.DateTime)
	}
	return value
}

func oneWordTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("type", &tools.Prop{
			Type: "string",
			Desc: "一言类型，可选值：二次元、游戏、文学、原创、网络、其他、影视、诗词、网易云、哲学、抖机灵",
		})
	ins.Add(unit.Name("oneword_get").Desc("获取一句一言/诗词并发送到当前对话").Params(params).Func(oneWordWrap))
}

func oneWordWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Type string `json:"type"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 1)
	argsSlice = appendStringArg(argsSlice, "type", s.Type)
	return callHandlerTool(ctx, meta, OneWordHandler, argsSlice...)
}

func stockZhATool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("code", &tools.Prop{
			Type: "string",
			Desc: "A 股股票代码，例如 600519 或 000001",
		}).
		AddProp("days", &tools.Prop{
			Type: "number",
			Desc: "查询近几天的数据，默认 1",
		}).
		AddProp("start_time", &tools.Prop{
			Type: "string",
			Desc: "开始时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
		}).
		AddProp("end_time", &tools.Prop{
			Type: "string",
			Desc: "结束时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
		}).
		AddRequired("code")
	ins.Add(unit.Name("stock_zh_a_get").Desc("查询沪深 A 股指定股票的近期价格走势图").Params(params).Func(stockZhAWrap))
}

func stockZhAWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Code      string `json:"code"`
		Days      *int   `json:"days"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 4)
	argsSlice = appendStringArg(argsSlice, "code", s.Code)
	argsSlice = appendIntArg(argsSlice, "days", s.Days)
	if s.StartTime != "" && s.EndTime != "" {
		argsSlice = appendStringArg(argsSlice, "st", normalizeRFC3339(s.StartTime))
		argsSlice = appendStringArg(argsSlice, "et", normalizeRFC3339(s.EndTime))
	}
	return callHandlerTool(ctx, meta, ZhAStockHandler, argsSlice...)
}

func trendReportTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("days", &tools.Prop{
			Type: "number",
			Desc: "统计近几天的发言趋势，默认 7",
		}).
		AddProp("interval", &tools.Prop{
			Type: "string",
			Desc: "聚合间隔，例如 1d、12h、1h",
		}).
		AddProp("chart_type", &tools.Prop{
			Type: "string",
			Desc: "图表类型，可选值：line、pie、bar，默认 line",
		}).
		AddProp("start_time", &tools.Prop{
			Type: "string",
			Desc: "开始时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
		}).
		AddProp("end_time", &tools.Prop{
			Type: "string",
			Desc: "结束时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
		})
	ins.Add(unit.Name("talkrate_get").Desc("统计当前群聊的发言趋势，并生成趋势图").Params(params).Func(trendReportWrap))
}

func trendReportWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Days      *int   `json:"days"`
		Interval  string `json:"interval"`
		ChartType string `json:"chart_type"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 5)
	argsSlice = appendIntArg(argsSlice, "days", s.Days)
	argsSlice = appendStringArg(argsSlice, "interval", s.Interval)
	if s.StartTime != "" && s.EndTime != "" {
		argsSlice = appendStringArg(argsSlice, "st", normalizeRFC3339(s.StartTime))
		argsSlice = appendStringArg(argsSlice, "et", normalizeRFC3339(s.EndTime))
	}
	switch s.ChartType {
	case "bar", "pie":
		argsSlice = appendStringArg(argsSlice, "play", s.ChartType)
	}
	return callHandlerTool(ctx, meta, TrendHandler, argsSlice...)
}

func wordCloudTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("days", &tools.Prop{
			Type: "number",
			Desc: "统计近几天的词云，默认 7",
		}).
		AddProp("interval", &tools.Prop{
			Type: "string",
			Desc: "聚合间隔，例如 1d、12h、1h",
		}).
		AddProp("message_top", &tools.Prop{
			Type: "number",
			Desc: "活跃用户 Top N，默认 10",
		}).
		AddProp("chunk_top", &tools.Prop{
			Type: "number",
			Desc: "热点话题块 Top N，默认 5",
		}).
		AddProp("chat_id", &tools.Prop{
			Type: "string",
			Desc: "目标群聊 ID，不填则使用当前群聊",
		}).
		AddProp("sort", &tools.Prop{
			Type: "string",
			Desc: "摘要排序方式，可选值：relevance、time",
		}).
		AddProp("start_time", &tools.Prop{
			Type: "string",
			Desc: "开始时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
		}).
		AddProp("end_time", &tools.Prop{
			Type: "string",
			Desc: "结束时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
		})
	ins.Add(unit.Name("word_cloud_get").Desc("生成群聊词云、活跃用户和热点话题摘要").Params(params).Func(wordCloudWrap))
}

func wordCloudWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Days       *int   `json:"days"`
		Interval   string `json:"interval"`
		MessageTop *int   `json:"message_top"`
		ChunkTop   *int   `json:"chunk_top"`
		ChatID     string `json:"chat_id"`
		Sort       string `json:"sort"`
		StartTime  string `json:"start_time"`
		EndTime    string `json:"end_time"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 8)
	argsSlice = appendIntArg(argsSlice, "days", s.Days)
	argsSlice = appendStringArg(argsSlice, "interval", s.Interval)
	argsSlice = appendIntArg(argsSlice, "mtop", s.MessageTop)
	argsSlice = appendIntArg(argsSlice, "ctop", s.ChunkTop)
	argsSlice = appendStringArg(argsSlice, "chat_id", s.ChatID)
	argsSlice = appendStringArg(argsSlice, "sort", s.Sort)
	if s.StartTime != "" && s.EndTime != "" {
		argsSlice = appendStringArg(argsSlice, "st", normalizeDateTime(s.StartTime))
		argsSlice = appendStringArg(argsSlice, "et", normalizeDateTime(s.EndTime))
	}
	return callHandlerTool(ctx, meta, WordCloudHandler, argsSlice...)
}

func configTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	configListTool(ins)
	configSetTool(ins)
	configDeleteTool(ins)
}

func configListTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("scope", &tools.Prop{
			Type: "string",
			Desc: "配置范围，可选值：chat、user、global。默认 chat",
		})
	ins.Add(unit.Name("config_list").Desc("列出当前上下文可见的配置项").Params(params).Func(configListWrap))
}

func configListWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Scope string `json:"scope"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 1)
	argsSlice = appendStringArg(argsSlice, "scope", s.Scope)
	return callHandlerTool(ctx, meta, ConfigListHandler, argsSlice...)
}

func configSetTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("key", &tools.Prop{
			Type: "string",
			Desc: "配置键名",
		}).
		AddProp("value", &tools.Prop{
			Type: "string",
			Desc: "配置值。布尔配置传 true/false，数值配置传整数字符串",
		}).
		AddProp("scope", &tools.Prop{
			Type: "string",
			Desc: "配置范围，可选值：chat、user、global。默认 chat",
		}).
		AddRequired("key").
		AddRequired("value")
	ins.Add(unit.Name("config_set").Desc("设置机器人配置项").Params(params).Func(configSetWrap))
}

func configSetWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Scope string `json:"scope"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 3)
	argsSlice = appendStringArg(argsSlice, "key", s.Key)
	argsSlice = appendStringArg(argsSlice, "value", s.Value)
	argsSlice = appendStringArg(argsSlice, "scope", s.Scope)
	return callHandlerTool(ctx, meta, ConfigSetHandler, argsSlice...)
}

func configDeleteTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("key", &tools.Prop{
			Type: "string",
			Desc: "配置键名",
		}).
		AddProp("scope", &tools.Prop{
			Type: "string",
			Desc: "配置范围，可选值：chat、user、global。默认 chat",
		}).
		AddRequired("key")
	ins.Add(unit.Name("config_delete").Desc("删除机器人配置项").Params(params).Func(configDeleteWrap))
}

func configDeleteWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Key   string `json:"key"`
		Scope string `json:"scope"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 2)
	argsSlice = appendStringArg(argsSlice, "key", s.Key)
	argsSlice = appendStringArg(argsSlice, "scope", s.Scope)
	return callHandlerTool(ctx, meta, ConfigDeleteHandler, argsSlice...)
}

func featureTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	featureListTool(ins)
	featureBlockTool(ins)
	featureUnblockTool(ins)
}

func featureListTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object")
	ins.Add(unit.Name("feature_list").Desc("列出当前群聊的功能开关状态").Params(params).Func(featureListWrap))
}

func featureListWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	return callHandlerTool(ctx, meta, FeatureListHandler)
}

func featureBlockTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("feature", &tools.Prop{
			Type: "string",
			Desc: "功能名称",
		}).
		AddProp("scope", &tools.Prop{
			Type: "string",
			Desc: "生效范围，可选值：chat、user、chat_user。默认 chat",
		}).
		AddRequired("feature")
	ins.Add(unit.Name("feature_block").Desc("屏蔽指定机器人功能").Params(params).Func(featureBlockWrap))
}

func featureBlockWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Feature string `json:"feature"`
		Scope   string `json:"scope"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 2)
	argsSlice = appendStringArg(argsSlice, "feature", s.Feature)
	argsSlice = appendStringArg(argsSlice, "scope", s.Scope)
	return callHandlerTool(ctx, meta, FeatureBlockHandler, argsSlice...)
}

func featureUnblockTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("feature", &tools.Prop{
			Type: "string",
			Desc: "功能名称",
		}).
		AddProp("scope", &tools.Prop{
			Type: "string",
			Desc: "生效范围，可选值：chat、user、chat_user。默认 chat",
		}).
		AddRequired("feature")
	ins.Add(unit.Name("feature_unblock").Desc("取消屏蔽指定机器人功能").Params(params).Func(featureUnblockWrap))
}

func featureUnblockWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Feature string `json:"feature"`
		Scope   string `json:"scope"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 2)
	argsSlice = appendStringArg(argsSlice, "feature", s.Feature)
	argsSlice = appendStringArg(argsSlice, "scope", s.Scope)
	return callHandlerTool(ctx, meta, FeatureUnblockHandler, argsSlice...)
}

func wordTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	wordAddTool(ins)
	wordGetTool(ins)
}

func wordAddTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("word", &tools.Prop{
			Type: "string",
			Desc: "触发词",
		}).
		AddProp("rate", &tools.Prop{
			Type: "number",
			Desc: "触发概率/权重",
		}).
		AddRequired("word").
		AddRequired("rate")
	ins.Add(unit.Name("word_add").Desc("新增或更新复读词条").Params(params).Func(wordAddWrap))
}

func wordAddWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Word string `json:"word"`
		Rate int    `json:"rate"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 2)
	argsSlice = appendStringArg(argsSlice, "word", s.Word)
	argsSlice = append(argsSlice, "--rate="+strconv.Itoa(s.Rate))
	return callHandlerTool(ctx, meta, WordAddHandler, argsSlice...)
}

func wordGetTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object")
	ins.Add(unit.Name("word_get").Desc("查看当前群聊的复读词条配置").Params(params).Func(wordGetWrap))
}

func wordGetWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	return callHandlerTool(ctx, meta, WordGetHandler)
}

func replyTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	replyAddTool(ins)
	replyGetTool(ins)
}

func replyAddTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("word", &tools.Prop{
			Type: "string",
			Desc: "触发关键词",
		}).
		AddProp("type", &tools.Prop{
			Type: "string",
			Desc: "匹配类型，可选值：substr、full、regex",
		}).
		AddProp("reply", &tools.Prop{
			Type: "string",
			Desc: "文本回复内容。reply_type=img 时可以不传，改为使用当前引用图片",
		}).
		AddProp("reply_type", &tools.Prop{
			Type: "string",
			Desc: "回复类型，可选值：text、image。默认 text",
		}).
		AddRequired("word").
		AddRequired("type")
	ins.Add(unit.Name("reply_add").Desc("新增群聊关键词回复规则").Params(params).Func(replyAddWrap))
}

func replyAddWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Word      string `json:"word"`
		Type      string `json:"type"`
		Reply     string `json:"reply"`
		ReplyType string `json:"reply_type"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 4)
	argsSlice = appendStringArg(argsSlice, "word", s.Word)
	argsSlice = appendStringArg(argsSlice, "type", s.Type)
	argsSlice = appendStringArg(argsSlice, "reply", s.Reply)
	argsSlice = appendStringArg(argsSlice, "reply_type", s.ReplyType)
	return callHandlerTool(ctx, meta, ReplyAddHandler, argsSlice...)
}

func replyGetTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object")
	ins.Add(unit.Name("reply_get").Desc("查看当前群聊的关键词回复规则").Params(params).Func(replyGetWrap))
}

func replyGetWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	return callHandlerTool(ctx, meta, ReplyGetHandler)
}

func imageTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	imageAddTool(ins)
	imageGetTool(ins)
	imageDeleteTool(ins)
}

func imageAddTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("url", &tools.Prop{
			Type: "string",
			Desc: "图片 URL。与 img_key 二选一；都不传时尝试使用当前引用/话题中的图片",
		}).
		AddProp("img_key", &tools.Prop{
			Type: "string",
			Desc: "飞书图片 key。与 url 二选一；都不传时尝试使用当前引用/话题中的图片",
		})
	ins.Add(unit.Name("image_add").Desc("把图片素材加入当前群聊的图片素材库").Params(params).Func(imageAddWrap))
}

func imageAddWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		URL    string `json:"url"`
		ImgKey string `json:"img_key"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 2)
	argsSlice = appendStringArg(argsSlice, "url", s.URL)
	argsSlice = appendStringArg(argsSlice, "img_key", s.ImgKey)
	return callHandlerTool(ctx, meta, ImageAddHandler, argsSlice...)
}

func imageGetTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object")
	ins.Add(unit.Name("image_get").Desc("查看当前群聊已登记的图片素材").Params(params).Func(imageGetWrap))
}

func imageGetWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	return callHandlerTool(ctx, meta, ImageGetHandler)
}

func imageDeleteTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object")
	ins.Add(unit.Name("image_delete").Desc("删除当前引用消息或话题中对应的图片素材").Params(params).Func(imageDeleteWrap))
}

func imageDeleteWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	return callHandlerTool(ctx, meta, ImageDelHandler)
}

func rateLimitTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	rateLimitStatsTool(ins)
	rateLimitListTool(ins)
}

func rateLimitStatsTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("chat_id", &tools.Prop{
			Type: "string",
			Desc: "目标群聊 ID，不填则使用当前群聊",
		})
	ins.Add(unit.Name("ratelimit_stats_get").Desc("查看某个群聊的频控统计与诊断信息").Params(params).Func(rateLimitStatsWrap))
}

func rateLimitStatsWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		ChatID string `json:"chat_id"`
	}{}
	if err := utils.UnmarshalStringPre(args, &s); err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0, 1)
	argsSlice = appendStringArg(argsSlice, "chat_id", s.ChatID)
	return callHandlerTool(ctx, meta, RateLimitStatsHandler, argsSlice...)
}

func rateLimitListTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object")
	ins.Add(unit.Name("ratelimit_list").Desc("列出所有会话的频控统计概览").Params(params).Func(rateLimitListWrap))
}

func rateLimitListWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	return callHandlerTool(ctx, meta, RateLimitListHandler)
}

// ============================================
// 发送消息工具
// ============================================

func sendMessageTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("content", &tools.Prop{
			Type: "string",
			Desc: "要发送的消息内容",
		}).
		AddProp("chat_id", &tools.Prop{
			Type: "string",
			Desc: "目标群组ID，不填则发送到当前对话",
		}).
		AddRequired("content")

	ins.Add(unit.Name("send_message").
		Desc("发送一条消息到当前对话或指定群组。当你需要主动通知用户、发送提醒确认、或者发送额外信息时使用此工具").
		Params(params).
		Func(sendMessageWrap))
}

func sendMessageWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Content string `json:"content"`
		ChatID  string `json:"chat_id"`
	}{}
	err := utils.UnmarshalStringPre(args, &s)
	if err != nil {
		return gresult.Err[string](err)
	}

	targetChatID := meta.ChatID
	if s.ChatID != "" {
		targetChatID = s.ChatID
	}

	err = larkmsg.CreateMsgTextRaw(ctx, larkmsg.NewTextMsgBuilder().Text(s.Content).Build(), "", targetChatID)
	if err != nil {
		return gresult.Err[string](err)
	}

	return gresult.OK("消息发送成功")
}
