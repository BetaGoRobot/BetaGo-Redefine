package aktool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/avast/retry-go/v4"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/hertz/pkg/app/client"
	"github.com/cloudwego/hertz/pkg/protocol"
	"go.uber.org/zap"
)

const (
	publicAPIURI             = "/api/public/"
	goldHandlerNameRealtime  = "spot_quotations_sge"
	goldHandlerNameHistory   = "spot_hist_sge"
	stockHandlerNameRealtime = "stock_zh_a_minute"
	stockSingleInfo          = "stock_individual_info_em"
)

var (
	defaultProvider Provider = noopProvider{reason: "aktool not initialized"}
	warnOnce        sync.Once
)

type Provider interface {
	GetRealtimeGoldPrice(ctx context.Context) (GoldPriceDataRTList, error)
	GetHistoryGoldPrice(ctx context.Context) (GoldPriceDataHS, error)
	GetStockPriceRT(ctx context.Context, symbol string) (StockPriceDataRTList, error)
	GetStockSymbolInfo(ctx context.Context, symbol string) (string, error)
}

type noopProvider struct {
	reason string
}

func (n noopProvider) GetRealtimeGoldPrice(context.Context) (GoldPriceDataRTList, error) {
	return nil, errors.New(n.reason)
}

func (n noopProvider) GetHistoryGoldPrice(context.Context) (GoldPriceDataHS, error) {
	return nil, errors.New(n.reason)
}

func (n noopProvider) GetStockPriceRT(context.Context, string) (StockPriceDataRTList, error) {
	return nil, errors.New(n.reason)
}

func (n noopProvider) GetStockSymbolInfo(context.Context, string) (string, error) {
	return "", errors.New(n.reason)
}

type httpProvider struct {
	baseURL string
}

func Init() {
	cfg := config.Get().AKToolConfig
	if cfg == nil || cfg.BaseURL == "" {
		setNoop("aktool config missing or base url empty")
		return
	}
	defaultProvider = httpProvider{baseURL: cfg.BaseURL}
}

func ErrUnavailable() error {
	if provider, ok := defaultProvider.(noopProvider); ok {
		return errors.New(provider.reason)
	}
	return errors.New("aktool not initialized")
}

func setNoop(reason string) {
	defaultProvider = noopProvider{reason: reason}
	warnOnce.Do(func() {
		logs.L().Warn("AKTool disabled, falling back to noop",
			zap.String("reason", reason),
		)
	})
}

type (
	GoldPriceDataRTList []*GoldPriceDataRT
	GoldPriceDataRT     struct {
		Kind       string  `json:"品种"`
		Time       string  `json:"时间"`
		Price      float64 `json:"现价"`
		UpdateTime string  `json:"更新时间"`
	}

	StockPriceDataRTList []*StockPriceDataRT
	StockPriceDataRT     struct {
		DateTime string `json:"day"`
		Open     string `json:"open"`
		High     string `json:"high"`
		Low      string `json:"low"`
		Close    string `json:"close"`
		Volume   string `json:"volume"`
	}
)

func (rt *GoldPriceDataRTList) ToLLMTable() string {
	if len(*rt) == 0 {
		return "没有有效的信息"
	}
	sb := strings.Builder{}
	sb.WriteString("时间,现价\n")
	for _, item := range *rt {
		sb.WriteString(fmt.Sprintf("%s,%.2f\n", item.Time, item.Price))
	}
	return sb.String()
}

type GoldPriceDataHS []struct {
	Date  string  `json:"date"`
	Open  float64 `json:"open"`
	Close float64 `json:"close"`
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
}

func (hs *GoldPriceDataHS) ToLLMTable() string {
	if len(*hs) == 0 {
		return "没有有效的信息"
	}
	sb := strings.Builder{}
	sb.WriteString("日期,开盘价,收盘价,最低价,最高价\n")
	for _, item := range *hs {
		sb.WriteString(fmt.Sprintf("%s,%.2f,%.2f,%.2f,%.2f\n", item.Date, item.Open, item.Close, item.Low, item.High))
	}
	return sb.String()
}

func GetRealtimeGoldPrice(ctx context.Context) (GoldPriceDataRTList, error) {
	return defaultProvider.GetRealtimeGoldPrice(ctx)
}

func (p httpProvider) GetRealtimeGoldPrice(ctx context.Context) (res GoldPriceDataRTList, err error) {
	_, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	res = make(GoldPriceDataRTList, 0)
	err = retry.Do(
		func() error {
			c, _ := client.NewClient()
			req, resp := protocol.AcquireRequest(), protocol.AcquireResponse()
			req.SetRequestURI(p.baseURL + publicAPIURI + goldHandlerNameRealtime)
			req.SetMethod("GET")
			err = c.Do(ctx, req, resp)
			if resp.StatusCode() != 200 {
				return fmt.Errorf("get gold price failed, status code: %d", resp.StatusCode())
			}
			return sonic.Unmarshal(resp.Body(), &res)
		},
		retry.Attempts(3),
	)
	return res, err
}

func GetHistoryGoldPrice(ctx context.Context) (GoldPriceDataHS, error) {
	return defaultProvider.GetHistoryGoldPrice(ctx)
}

func (p httpProvider) GetHistoryGoldPrice(ctx context.Context) (res GoldPriceDataHS, err error) {
	_, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	res = make(GoldPriceDataHS, 0)
	err = retry.Do(
		func() error {
			c, _ := client.NewClient()
			req, resp := protocol.AcquireRequest(), protocol.AcquireResponse()
			req.SetRequestURI(p.baseURL + publicAPIURI + goldHandlerNameHistory)
			req.SetMethod("GET")
			err = c.Do(ctx, req, resp)
			if resp.StatusCode() != 200 {
				return fmt.Errorf("get gold price failed, status code: %d", resp.StatusCode())
			}
			return sonic.Unmarshal(resp.Body(), &res)
		},
		retry.Attempts(3),
	)
	return res, err
}

func GetStockPriceRT(ctx context.Context, symbol string) (StockPriceDataRTList, error) {
	return defaultProvider.GetStockPriceRT(ctx, symbol)
}

func (p httpProvider) GetStockPriceRT(ctx context.Context, symbol string) (res StockPriceDataRTList, err error) {
	_, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	res = make(StockPriceDataRTList, 0)
	c, _ := client.NewClient()
	req, resp := protocol.AcquireRequest(), protocol.AcquireResponse()
	req.SetRequestURI(p.baseURL + publicAPIURI + stockHandlerNameRealtime)
	req.SetMethod("GET")
	req.SetQueryString(fmt.Sprintf("symbol=sh%s", symbol))

	err = c.Do(ctx, req, resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("get gold price failed, status code: %d", resp.StatusCode())
	}
	if err := sonic.Unmarshal(resp.Body(), &res); err != nil {
		return nil, err
	}
	return res, nil
}

func GetStockSymbolInfo(ctx context.Context, symbol string) (string, error) {
	return defaultProvider.GetStockSymbolInfo(ctx, symbol)
}

func (p httpProvider) GetStockSymbolInfo(ctx context.Context, symbol string) (stockName string, err error) {
	_, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	res := make([]map[string]any, 0)
	c, _ := client.NewClient()
	req, resp := protocol.AcquireRequest(), protocol.AcquireResponse()
	req.SetRequestURI(p.baseURL + publicAPIURI + stockSingleInfo)
	req.SetMethod("GET")
	req.SetQueryString(fmt.Sprintf("symbol=%s", symbol))
	err = c.Do(ctx, req, resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != 200 {
		return "", fmt.Errorf("get stock info failed, status code: %d", resp.StatusCode())
	}
	if err := sonic.Unmarshal(resp.Body(), &res); err != nil {
		return "", err
	}
	for _, item := range res {
		if item["item"].(string) == "股票简称" {
			return item["value"].(string), nil
		}
	}
	return "", nil
}
