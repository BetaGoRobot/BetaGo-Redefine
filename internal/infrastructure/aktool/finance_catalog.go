package aktool

import (
	"strings"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
)

type FinanceToolCategory string

const (
	FinanceToolCategoryMarketData FinanceToolCategory = "market"
	FinanceToolCategoryNews       FinanceToolCategory = "news"
	FinanceToolCategoryEconomy    FinanceToolCategory = "economy"
)

type FinanceSourceSpec struct {
	Name         string `json:"name"`
	EndpointName string `json:"endpoint_name"`
}

type FinanceSourceRoute struct {
	RequestKind string              `json:"request_kind"`
	Fallbacks   []FinanceSourceSpec `json:"fallbacks"`
}

type FinanceToolDefinition struct {
	Name        string               `json:"name"`
	Category    FinanceToolCategory  `json:"category"`
	Description string               `json:"description"`
	Schema      *arktools.Param      `json:"schema"`
	Examples    []string             `json:"examples,omitempty"`
	Routes      []FinanceSourceRoute `json:"routes,omitempty"`
}

func (d FinanceToolDefinition) SourceRoute(kind string) (FinanceSourceRoute, bool) {
	trimmed := strings.TrimSpace(kind)
	for _, route := range d.Routes {
		if strings.TrimSpace(route.RequestKind) == trimmed {
			return route, true
		}
	}
	return FinanceSourceRoute{}, false
}

func FinanceToolCatalog() []FinanceToolDefinition {
	copied := make([]FinanceToolDefinition, 0, len(financeToolCatalog))
	for _, item := range financeToolCatalog {
		copied = append(copied, item)
	}
	return copied
}

func LookupFinanceToolDefinition(name string) (FinanceToolDefinition, bool) {
	for _, item := range financeToolCatalog {
		if item.Name == strings.TrimSpace(name) {
			return item, true
		}
	}
	return FinanceToolDefinition{}, false
}

func enumValues(values ...string) []any {
	res := make([]any, 0, len(values))
	for _, value := range values {
		res = append(res, value)
	}
	return res
}

var financeToolCatalog = []FinanceToolDefinition{
	{
		Name:        "finance_market_data_get",
		Category:    FinanceToolCategoryMarketData,
		Description: "读取只读市场行情，首批覆盖 A 股、指数、黄金和期货，返回结构化 JSON 摘要。",
		Schema: arktools.NewParams("object").
			AddProp("asset_type", &arktools.Prop{
				Type: "string",
				Desc: "市场品类，支持 stock、index、gold、futures",
				Enum: enumValues("stock", "index", "gold", "futures"),
			}).
			AddProp("symbol", &arktools.Prop{
				Type: "string",
				Desc: "证券、指数或期货代码。gold 可留空，其他品类建议显式提供",
			}).
			AddProp("interval", &arktools.Prop{
				Type: "string",
				Desc: "时间粒度，支持 realtime、intraday、daily",
				Enum: enumValues("realtime", "intraday", "daily"),
			}).
			AddProp("limit", &arktools.Prop{
				Type: "number",
				Desc: "最多返回多少条记录，默认 10",
			}).
			AddProp("start_time", &arktools.Prop{
				Type: "string",
				Desc: "可选开始时间，格式建议 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}).
			AddProp("end_time", &arktools.Prop{
				Type: "string",
				Desc: "可选结束时间，格式建议 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}).
			AddRequired("asset_type"),
		Examples: []string{
			`{"asset_type":"gold","limit":5}`,
			`{"asset_type":"stock","symbol":"600519","limit":10}`,
			`{"asset_type":"index","symbol":"399006","interval":"daily","limit":5}`,
		},
		Routes: []FinanceSourceRoute{
			{RequestKind: "gold", Fallbacks: []FinanceSourceSpec{{Name: "gold_realtime", EndpointName: "spot_quotations_sge"}, {Name: "gold_history", EndpointName: "spot_hist_sge"}}},
			{RequestKind: "stock", Fallbacks: []FinanceSourceSpec{{Name: "stock_intraday", EndpointName: "stock_zh_a_minute"}}},
			{RequestKind: "index", Fallbacks: []FinanceSourceSpec{{Name: "index_intraday", EndpointName: "index_zh_a_hist_min_em"}, {Name: "index_daily", EndpointName: "index_zh_a_hist"}}},
			{RequestKind: "futures", Fallbacks: []FinanceSourceSpec{{Name: "futures_spot", EndpointName: "futures_zh_spot"}, {Name: "futures_realtime", EndpointName: "futures_zh_realtime"}}},
		},
	},
	{
		Name:        "finance_news_get",
		Category:    FinanceToolCategoryNews,
		Description: "读取金融资讯，首批覆盖个股新闻和市场精选内容，返回结构化 JSON。",
		Schema: arktools.NewParams("object").
			AddProp("topic_type", &arktools.Prop{
				Type: "string",
				Desc: "资讯类型，支持 stock 或 market",
				Enum: enumValues("stock", "market"),
			}).
			AddProp("symbol", &arktools.Prop{
				Type: "string",
				Desc: "个股代码；topic_type=stock 时建议提供",
			}).
			AddProp("limit", &arktools.Prop{
				Type: "number",
				Desc: "最多返回多少条记录，默认 5",
			}).
			AddRequired("topic_type"),
		Examples: []string{
			`{"topic_type":"stock","symbol":"600519","limit":3}`,
			`{"topic_type":"market","limit":5}`,
		},
		Routes: []FinanceSourceRoute{
			{RequestKind: "stock", Fallbacks: []FinanceSourceSpec{{Name: "stock_news_em", EndpointName: "stock_news_em"}, {Name: "stock_news_main_cx", EndpointName: "stock_news_main_cx"}}},
			{RequestKind: "market", Fallbacks: []FinanceSourceSpec{{Name: "stock_news_main_cx", EndpointName: "stock_news_main_cx"}}},
		},
	},
	{
		Name:        "economy_indicator_get",
		Category:    FinanceToolCategoryEconomy,
		Description: "读取宏观经济指标，首批覆盖中国 CPI、GDP、PMI，返回结构化 JSON。",
		Schema: arktools.NewParams("object").
			AddProp("indicator", &arktools.Prop{
				Type: "string",
				Desc: "指标类型，支持 china_cpi、china_gdp、china_pmi",
				Enum: enumValues("china_cpi", "china_gdp", "china_pmi"),
			}).
			AddProp("limit", &arktools.Prop{
				Type: "number",
				Desc: "最多返回多少条记录，默认 6",
			}).
			AddRequired("indicator"),
		Examples: []string{
			`{"indicator":"china_cpi","limit":6}`,
			`{"indicator":"china_gdp","limit":4}`,
		},
		Routes: []FinanceSourceRoute{
			{RequestKind: "china_cpi", Fallbacks: []FinanceSourceSpec{{Name: "macro_china_cpi", EndpointName: "macro_china_cpi"}, {Name: "macro_china_cpi_monthly", EndpointName: "macro_china_cpi_monthly"}}},
			{RequestKind: "china_gdp", Fallbacks: []FinanceSourceSpec{{Name: "macro_china_gdp", EndpointName: "macro_china_gdp"}, {Name: "macro_china_gdp_yearly", EndpointName: "macro_china_gdp_yearly"}}},
			{RequestKind: "china_pmi", Fallbacks: []FinanceSourceSpec{{Name: "macro_china_pmi", EndpointName: "macro_china_pmi"}, {Name: "macro_china_pmi_yearly", EndpointName: "macro_china_pmi_yearly"}}},
		},
	},
}
