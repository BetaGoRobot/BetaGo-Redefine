package aktool

import (
	"context"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/akshareapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
)

type httpProvider struct {
	baseURL string
	client  *akshareapi.Client
}

func (p httpProvider) apiClient() *akshareapi.Client {
	if p.client != nil {
		return p.client
	}
	return akshareapi.NewClient(p.baseURL, nil)
}

func (p httpProvider) callInto(ctx context.Context, endpoint akshareapi.Endpoint, params any, out any) error {
	if p.client == nil && p.baseURL == "" {
		return akshareapi.CallInto(ctx, endpoint, params, out)
	}
	return p.apiClient().CallInto(ctx, endpoint, params, out)
}

type (
	GoldPriceDataRTList []*GoldPriceDataRT
	GoldPriceDataRT     struct {
		Kind       string  `json:"品种"`
		Time       string  `json:"时间"`
		Price      float64 `json:"现价"`
		UpdateTime string  `json:"更新时间"`

		Valid bool `json:"-"`
	}

	StockPriceDataRTList []*StockPriceDataRT
	StockPriceDataRT     struct {
		DateTime string `json:"day"`
		Open     string `json:"open"`
		High     string `json:"high"`
		Low      string `json:"low"`
		Close    string `json:"close"`
		Volume   string `json:"volume"`

		Valid bool `json:"-"`
	}
)

func (hs *StockPriceDataRTList) ToLLMTable(kind string) string {
	if len(*hs) == 0 {
		return "没有有效的信息"
	}
	sb := strings.Builder{}
	sb.WriteString("代号,日期,开盘价,收盘价,最低价,最高价\n")
	for _, item := range *hs {
		if item.Valid {
			sb.WriteString(fmt.Sprintf("%s,%s,%s,%s,%s,%s\n", kind, item.DateTime, item.Open, item.Close, item.Low, item.High))
		}
	}
	return sb.String()
}

func (hs *StockPriceDataRT) MarkValid() {
	hs.Valid = true
}

func (rt *GoldPriceDataRTList) ToLLMTable(kind string) string {
	if len(*rt) == 0 {
		return "没有有效的信息"
	}
	sb := strings.Builder{}
	sb.WriteString("时间,现价\n")
	for _, item := range *rt {
		if item.Valid {
			sb.WriteString(fmt.Sprintf("%s,%s,%.2f\n", kind, item.Time, item.Price))
		}
	}
	return sb.String()
}

func (hs *GoldPriceDataRT) MarkValid() {
	hs.Valid = true
}

type GoldPriceDataHS []*GoldPriceDataHSItem

type GoldPriceDataHSItem struct {
	Date  string  `json:"date"`
	Open  float64 `json:"open"`
	Close float64 `json:"close"`
	Low   float64 `json:"low"`
	High  float64 `json:"high"`

	Valid bool `json:"-"`
}

func (hs *GoldPriceDataHS) ToLLMTable() string {
	if len(*hs) == 0 {
		return "没有有效的信息"
	}
	sb := strings.Builder{}
	sb.WriteString("日期,开盘价,收盘价,最低价,最高价\n")
	for _, item := range *hs {
		if item.Valid {
			sb.WriteString(fmt.Sprintf("%s,%.2f,%.2f,%.2f,%.2f\n", item.Date, item.Open, item.Close, item.Low, item.High))
		}
	}
	return sb.String()
}

func (hs *GoldPriceDataHSItem) MarkValid() {
	hs.Valid = true
}

// TODO: 待迁移。当前仍保留在 aktool 中作为上层 stock_handler 的领域适配入口。
func GetRealtimeGoldPrice(ctx context.Context) (GoldPriceDataRTList, error) {
	return httpProvider{}.GetRealtimeGoldPrice(ctx)
}

func (p httpProvider) GetRealtimeGoldPrice(ctx context.Context) (res GoldPriceDataRTList, err error) {
	_, span := otel.Start(ctx)
	defer span.End()

	var rows []akshareapi.SpotQuotationsSgeRow
	err = p.callInto(ctx, akshareapi.EndpointSpotQuotationsSge, akshareapi.SpotQuotationsSgeParams{}, &rows)
	if err != nil {
		return nil, err
	}
	res = make(GoldPriceDataRTList, 0, len(rows))
	for _, row := range rows {
		res = append(res, &GoldPriceDataRT{
			Kind:       row.X品种,
			Time:       row.X时间,
			Price:      row.X现价,
			UpdateTime: row.X更新时间,
		})
	}
	return res, nil
}

// TODO: 待迁移。当前仍保留在 aktool 中作为上层 stock_handler 的领域适配入口。
func GetHistoryGoldPrice(ctx context.Context) (GoldPriceDataHS, error) {
	return httpProvider{}.GetHistoryGoldPrice(ctx)
}

func (p httpProvider) GetHistoryGoldPrice(ctx context.Context) (res GoldPriceDataHS, err error) {
	_, span := otel.Start(ctx)
	defer span.End()

	var rows []akshareapi.SpotHistSgeRow
	err = p.callInto(ctx, akshareapi.EndpointSpotHistSge, akshareapi.SpotHistSgeParams{}, &rows)
	if err != nil {
		return nil, err
	}
	res = make(GoldPriceDataHS, 0, len(rows))
	for _, row := range rows {
		res = append(res, &GoldPriceDataHSItem{
			Date:  row.Date,
			Open:  row.Open,
			Close: row.Close,
			Low:   row.Low,
			High:  row.High,
		})
	}
	return res, nil
}

// TODO: 待迁移。当前仍保留在 aktool 中作为上层 stock_handler 的领域适配入口。
func GetStockPriceRT(ctx context.Context, symbol string) (StockPriceDataRTList, error) {
	return httpProvider{}.GetStockPriceRT(ctx, symbol)
}

func (p httpProvider) GetStockPriceRT(ctx context.Context, symbol string) (res StockPriceDataRTList, err error) {
	_, span := otel.Start(ctx)
	defer span.End()

	var rows []akshareapi.StockZhAMinuteRow
	err = p.callInto(ctx, akshareapi.EndpointStockZhAMinute, akshareapi.StockZhAMinuteParams{
		Symbol: fmt.Sprintf("sh%s", symbol),
	}, &rows)
	if err != nil {
		return nil, err
	}
	res = make(StockPriceDataRTList, 0, len(rows))
	for _, row := range rows {
		res = append(res, &StockPriceDataRT{
			DateTime: row.Day,
			Open:     row.Open,
			High:     row.High,
			Low:      row.Low,
			Close:    row.Close,
			Volume:   row.Volume,
		})
	}
	return res, nil
}

// TODO: 待迁移。当前仍保留在 aktool 中作为上层 stock_handler 的领域适配入口。
func GetStockSymbolInfo(ctx context.Context, symbol string) (string, error) {
	return httpProvider{}.GetStockSymbolInfo(ctx, symbol)
}

func (p httpProvider) GetStockSymbolInfo(ctx context.Context, symbol string) (stockName string, err error) {
	_, span := otel.Start(ctx)
	defer span.End()

	var rows []akshareapi.StockIndividualInfoEmRow
	err = p.callInto(ctx, akshareapi.EndpointStockIndividualSpotXq, akshareapi.StockIndividualInfoEmParams{
		Symbol: "sh" + symbol,
	}, &rows)
	if err != nil {
		return "", err
	}

	for _, item := range rows {
		if fmt.Sprint(item.Item) == "org_short_name_cn" || fmt.Sprint(item.Item) == "股票简称" {
			return fmt.Sprint(item.Value), nil
		}
	}
	return "", err
}
