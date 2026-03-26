package aktool

import (
	"context"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/akshareapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

type FinanceToolResult struct {
	ToolName string           `json:"tool_name"`
	Category string           `json:"category"`
	Source   string           `json:"source"`
	Query    map[string]any   `json:"query,omitempty"`
	Summary  string           `json:"summary,omitempty"`
	Records  []map[string]any `json:"records,omitempty"`
}

type FinanceMarketDataRequest struct {
	AssetType string `json:"asset_type"`
	Symbol    string `json:"symbol"`
	Interval  string `json:"interval"`
	Limit     int    `json:"limit"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

type FinanceNewsRequest struct {
	TopicType string `json:"topic_type"`
	Symbol    string `json:"symbol"`
	Limit     int    `json:"limit"`
}

type FinanceEconomyIndicatorRequest struct {
	Indicator string `json:"indicator"`
	Limit     int    `json:"limit"`
}

type FinanceProvider struct {
	http httpProvider
}

type financeSourceAttempt struct {
	Kind         string
	EndpointName string
	Err          error
}

func NewFinanceProvider(baseURL string) FinanceProvider {
	return FinanceProvider{
		http: httpProvider{baseURL: strings.TrimSpace(baseURL)},
	}
}

func (p FinanceProvider) GetMarketData(ctx context.Context, req FinanceMarketDataRequest) (FinanceToolResult, error) {
	def, ok := LookupFinanceToolDefinition("finance_market_data_get")
	if !ok {
		return FinanceToolResult{}, fmt.Errorf("finance market data definition is missing")
	}
	assetType := strings.TrimSpace(req.AssetType)
	if assetType == "" {
		return FinanceToolResult{}, fmt.Errorf("asset_type is required")
	}
	route, ok := def.SourceRoute(assetType)
	if !ok {
		return FinanceToolResult{}, fmt.Errorf("unsupported asset_type %q", assetType)
	}

	var attempts []financeSourceAttempt
	for _, source := range route.Fallbacks {
		result, err := p.fetchMarketData(ctx, def, source, req)
		if err == nil {
			logs.L().Ctx(ctx).Info("finance market data source selected",
				zap.String("asset_type", assetType),
				zap.String("source", source.EndpointName),
			)
			return result, nil
		}
		attempts = append(attempts, financeSourceAttempt{Kind: assetType, EndpointName: source.EndpointName, Err: err})
	}
	return FinanceToolResult{}, buildFinanceSourceError(assetType, attempts)
}

func (p FinanceProvider) GetNews(ctx context.Context, req FinanceNewsRequest) (FinanceToolResult, error) {
	def, ok := LookupFinanceToolDefinition("finance_news_get")
	if !ok {
		return FinanceToolResult{}, fmt.Errorf("finance news definition is missing")
	}
	topicType := strings.TrimSpace(req.TopicType)
	if topicType == "" {
		return FinanceToolResult{}, fmt.Errorf("topic_type is required")
	}
	route, ok := def.SourceRoute(topicType)
	if !ok {
		return FinanceToolResult{}, fmt.Errorf("unsupported topic_type %q", topicType)
	}

	var attempts []financeSourceAttempt
	for _, source := range route.Fallbacks {
		result, err := p.fetchNews(ctx, def, source, req)
		if err == nil {
			logs.L().Ctx(ctx).Info("finance news source selected",
				zap.String("topic_type", topicType),
				zap.String("source", source.EndpointName),
			)
			return result, nil
		}
		attempts = append(attempts, financeSourceAttempt{Kind: topicType, EndpointName: source.EndpointName, Err: err})
	}
	return FinanceToolResult{}, buildFinanceSourceError(topicType, attempts)
}

func (p FinanceProvider) GetEconomyIndicator(ctx context.Context, req FinanceEconomyIndicatorRequest) (FinanceToolResult, error) {
	def, ok := LookupFinanceToolDefinition("economy_indicator_get")
	if !ok {
		return FinanceToolResult{}, fmt.Errorf("economy indicator definition is missing")
	}
	indicator := strings.TrimSpace(req.Indicator)
	if indicator == "" {
		return FinanceToolResult{}, fmt.Errorf("indicator is required")
	}
	route, ok := def.SourceRoute(indicator)
	if !ok {
		return FinanceToolResult{}, fmt.Errorf("unsupported indicator %q", indicator)
	}

	var attempts []financeSourceAttempt
	for _, source := range route.Fallbacks {
		result, err := p.fetchEconomyIndicator(ctx, def, source, req)
		if err == nil {
			logs.L().Ctx(ctx).Info("economy indicator source selected",
				zap.String("indicator", indicator),
				zap.String("source", source.EndpointName),
			)
			return result, nil
		}
		attempts = append(attempts, financeSourceAttempt{Kind: indicator, EndpointName: source.EndpointName, Err: err})
	}
	return FinanceToolResult{}, buildFinanceSourceError(indicator, attempts)
}

func (p FinanceProvider) fetchMarketData(ctx context.Context, def FinanceToolDefinition, source FinanceSourceSpec, req FinanceMarketDataRequest) (FinanceToolResult, error) {
	limit := normalizeFinanceLimit(req.Limit, 10)
	query := map[string]any{
		"asset_type": strings.TrimSpace(req.AssetType),
		"symbol":     strings.TrimSpace(req.Symbol),
		"interval":   defaultString(strings.TrimSpace(req.Interval), "realtime"),
		"limit":      limit,
	}

	switch source.EndpointName {
	case "spot_quotations_sge":
		var rows []akshareapi.SpotQuotationsSgeRow
		if err := p.http.callInto(ctx, akshareapi.EndpointSpotQuotationsSge, akshareapi.SpotQuotationsSgeParams{}, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"symbol":      row.X品种,
				"time":        row.X时间,
				"price":       row.X现价,
				"update_time": row.X更新时间,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条黄金实时行情", len(records)), records), nil
	case "spot_hist_sge":
		var rows []akshareapi.SpotHistSgeRow
		if err := p.http.callInto(ctx, akshareapi.EndpointSpotHistSge, akshareapi.SpotHistSgeParams{}, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"date":  row.Date,
				"open":  row.Open,
				"close": row.Close,
				"high":  row.High,
				"low":   row.Low,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条黄金历史行情", len(records)), records), nil
	case "stock_zh_a_minute":
		symbol := normalizeStockMinuteSymbol(req.Symbol)
		if symbol == "" {
			return FinanceToolResult{}, fmt.Errorf("symbol is required for stock market data")
		}
		var rows []akshareapi.StockZhAMinuteRow
		if err := p.http.callInto(ctx, akshareapi.EndpointStockZhAMinute, akshareapi.StockZhAMinuteParams{Symbol: symbol}, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"time":   row.Day,
				"open":   row.Open,
				"close":  row.Close,
				"high":   row.High,
				"low":    row.Low,
				"volume": row.Volume,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条 A 股分时行情", len(records)), records), nil
	case "index_zh_a_hist_min_em":
		symbol := strings.TrimSpace(req.Symbol)
		if symbol == "" {
			return FinanceToolResult{}, fmt.Errorf("symbol is required for index market data")
		}
		var rows []akshareapi.IndexZhAHistMinEmRow
		if err := p.http.callInto(ctx, akshareapi.EndpointIndexZhAHistMinEm, akshareapi.IndexZhAHistMinEmParams{
			Symbol:    symbol,
			Period:    defaultString(strings.TrimSpace(req.Interval), "1"),
			StartDate: defaultString(strings.TrimSpace(req.StartTime), "1979-09-01 09:32:00"),
			EndDate:   defaultString(strings.TrimSpace(req.EndTime), "2222-01-01 09:32:00"),
		}, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"time":   row.X时间,
				"open":   row.X开盘,
				"close":  row.X收盘,
				"high":   row.X最高,
				"low":    row.X最低,
				"volume": row.X成交量,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条指数分时行情", len(records)), records), nil
	case "index_zh_a_hist":
		symbol := strings.TrimSpace(req.Symbol)
		if symbol == "" {
			return FinanceToolResult{}, fmt.Errorf("symbol is required for index market data")
		}
		var rows []akshareapi.IndexZhAHistRow
		if err := p.http.callInto(ctx, akshareapi.EndpointIndexZhAHist, akshareapi.IndexZhAHistParams{
			Symbol:    symbol,
			Period:    "daily",
			StartDate: "19700101",
			EndDate:   "22220101",
		}, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"date":         row.X日期,
				"open":         row.X开盘,
				"close":        row.X收盘,
				"high":         row.X最高,
				"low":          row.X最低,
				"change_ratio": row.X涨跌幅,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条指数日线行情", len(records)), records), nil
	case "futures_zh_spot":
		symbol := strings.TrimSpace(req.Symbol)
		if symbol == "" {
			return FinanceToolResult{}, fmt.Errorf("symbol is required for futures market data")
		}
		var rows []akshareapi.FuturesZhSpotRow
		if err := p.http.callInto(ctx, akshareapi.EndpointFuturesZhSpot, akshareapi.FuturesZhSpotParams{
			SubscribeList: symbol,
			Market:        guessFuturesMarket(symbol),
			Adjust:        "0",
		}, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"symbol":        row.Symbol,
				"time":          row.Time,
				"current_price": row.CurrentPrice,
				"high":          row.High,
				"low":           row.Low,
				"volume":        row.Volume,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条期货实时行情", len(records)), records), nil
	case "futures_zh_realtime":
		symbol := strings.TrimSpace(req.Symbol)
		if symbol == "" {
			return FinanceToolResult{}, fmt.Errorf("symbol is required for futures market data")
		}
		var rows []akshareapi.FuturesZhRealtimeRow
		if err := p.http.callInto(ctx, akshareapi.EndpointFuturesZhRealtime, akshareapi.FuturesZhRealtimeParams{Symbol: symbol}, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"symbol":        row.Symbol,
				"exchange":      row.Exchange,
				"name":          row.Name,
				"trade":         row.Trade,
				"changepercent": row.Changepercent,
				"ticktime":      row.Ticktime,
				"tradedate":     row.Tradedate,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条期货实时行情", len(records)), records), nil
	default:
		return FinanceToolResult{}, fmt.Errorf("unsupported market data source %q", source.EndpointName)
	}
}

func (p FinanceProvider) fetchNews(ctx context.Context, def FinanceToolDefinition, source FinanceSourceSpec, req FinanceNewsRequest) (FinanceToolResult, error) {
	limit := normalizeFinanceLimit(req.Limit, 5)
	query := map[string]any{
		"topic_type": strings.TrimSpace(req.TopicType),
		"symbol":     strings.TrimSpace(req.Symbol),
		"limit":      limit,
	}

	switch source.EndpointName {
	case "stock_news_em":
		if strings.TrimSpace(req.Symbol) == "" {
			return FinanceToolResult{}, fmt.Errorf("symbol is required for stock news")
		}
		var rows []akshareapi.StockNewsEmRow
		if err := p.http.callInto(ctx, akshareapi.EndpointStockNewsEm, akshareapi.StockNewsEmParams{Symbol: strings.TrimSpace(req.Symbol)}, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"keyword":      row.X关键词,
				"title":        row.X新闻标题,
				"content":      row.X新闻内容,
				"published_at": row.X发布时间,
				"source":       row.X文章来源,
				"url":          row.X新闻链接,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条个股新闻", len(records)), records), nil
	case "stock_news_main_cx":
		var rows []akshareapi.StockNewsMainCxRow
		if err := p.http.callInto(ctx, akshareapi.EndpointStockNewsMainCx, nil, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"tag":     row.Tag,
				"summary": row.Summary,
				"url":     row.Url,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条市场精选资讯", len(records)), records), nil
	default:
		return FinanceToolResult{}, fmt.Errorf("unsupported news source %q", source.EndpointName)
	}
}

func (p FinanceProvider) fetchEconomyIndicator(ctx context.Context, def FinanceToolDefinition, source FinanceSourceSpec, req FinanceEconomyIndicatorRequest) (FinanceToolResult, error) {
	limit := normalizeFinanceLimit(req.Limit, 6)
	query := map[string]any{
		"indicator": strings.TrimSpace(req.Indicator),
		"limit":     limit,
	}

	switch source.EndpointName {
	case "macro_china_cpi":
		var rows []akshareapi.MacroChinaCpiRow
		if err := p.http.callInto(ctx, akshareapi.EndpointMacroChinaCpi, nil, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"period":              row.X月份,
				"national_current":    row.X全国当月,
				"national_yoy":        row.X全国同比增长,
				"national_mom":        row.X全国环比增长,
				"national_cumulative": row.X全国累计,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条中国 CPI 数据", len(records)), records), nil
	case "macro_china_cpi_monthly":
		var rows []akshareapi.MacroChinaCpiMonthlyRow
		if err := p.http.callInto(ctx, akshareapi.EndpointMacroChinaCpiMonthly, nil, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"item":     row.X商品,
				"date":     row.X日期,
				"current":  row.X今值,
				"forecast": row.X预测值,
				"previous": row.X前值,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条中国 CPI 月率数据", len(records)), records), nil
	case "macro_china_gdp":
		var rows []akshareapi.MacroChinaGdpRow
		if err := p.http.callInto(ctx, akshareapi.EndpointMacroChinaGdp, nil, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"quarter":       row.X季度,
				"gdp_value":     row.X国内生产总值绝对值,
				"gdp_yoy":       row.X国内生产总值同比增长,
				"primary_yoy":   row.X第一产业同比增长,
				"secondary_yoy": row.X第二产业同比增长,
				"tertiary_yoy":  row.X第三产业同比增长,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条中国 GDP 数据", len(records)), records), nil
	case "macro_china_gdp_yearly":
		var rows []akshareapi.MacroChinaGdpYearlyRow
		if err := p.http.callInto(ctx, akshareapi.EndpointMacroChinaGdpYearly, nil, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"item":     row.X商品,
				"date":     row.X日期,
				"current":  row.X今值,
				"forecast": row.X预测值,
				"previous": row.X前值,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条中国 GDP 年率数据", len(records)), records), nil
	case "macro_china_pmi":
		var rows []akshareapi.MacroChinaPmiRow
		if err := p.http.callInto(ctx, akshareapi.EndpointMacroChinaPmi, nil, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"period":              row.X月份,
				"manufacturing_index": row.X制造业指数,
				"manufacturing_yoy":   row.X制造业同比增长,
				"services_index":      row.X非制造业指数,
				"services_yoy":        row.X非制造业同比增长,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条中国 PMI 数据", len(records)), records), nil
	case "macro_china_pmi_yearly":
		var rows []akshareapi.MacroChinaPmiYearlyRow
		if err := p.http.callInto(ctx, akshareapi.EndpointMacroChinaPmiYearly, nil, &rows); err != nil {
			return FinanceToolResult{}, err
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"item":     row.X商品,
				"date":     row.X日期,
				"current":  row.X今值,
				"forecast": row.X预测值,
				"previous": row.X前值,
			})
		}
		records = truncateFinanceRecords(records, limit)
		return buildFinanceResult(def, source.EndpointName, query, fmt.Sprintf("返回 %d 条中国 PMI 年率数据", len(records)), records), nil
	default:
		return FinanceToolResult{}, fmt.Errorf("unsupported economy source %q", source.EndpointName)
	}
}

func buildFinanceResult(def FinanceToolDefinition, source string, query map[string]any, summary string, records []map[string]any) FinanceToolResult {
	return FinanceToolResult{
		ToolName: def.Name,
		Category: string(def.Category),
		Source:   strings.TrimSpace(source),
		Query:    query,
		Summary:  strings.TrimSpace(summary),
		Records:  records,
	}
}

func buildFinanceSourceError(kind string, attempts []financeSourceAttempt) error {
	if len(attempts) == 0 {
		return fmt.Errorf("%s: no available finance source", strings.TrimSpace(kind))
	}
	parts := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		parts = append(parts, fmt.Sprintf("%s: %v", attempt.EndpointName, attempt.Err))
	}
	return fmt.Errorf("%s: no available finance source (%s)", strings.TrimSpace(kind), strings.Join(parts, "; "))
}

func normalizeFinanceLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value > 50 {
		return 50
	}
	return value
}

func truncateFinanceRecords(records []map[string]any, limit int) []map[string]any {
	if limit <= 0 || len(records) <= limit {
		return records
	}
	return records[:limit]
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizeStockMinuteSymbol(symbol string) string {
	trimmed := strings.TrimSpace(symbol)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "sh") || strings.HasPrefix(trimmed, "sz") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "6") {
		return "sh" + trimmed
	}
	return "sz" + trimmed
}

func guessFuturesMarket(symbol string) string {
	upper := strings.ToUpper(strings.TrimSpace(symbol))
	for _, prefix := range []string{"IF", "IH", "IC", "IM", "T", "TF", "TS", "TL"} {
		if strings.HasPrefix(upper, prefix) {
			return "FF"
		}
	}
	return "CF"
}
