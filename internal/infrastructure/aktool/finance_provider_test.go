package aktool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFinanceToolCatalogDefinitionsIncludeFallbackOrder(t *testing.T) {
	t.Helper()

	market, ok := LookupFinanceToolDefinition("finance_market_data_get")
	if !ok {
		t.Fatal("expected finance_market_data_get definition")
	}
	if market.Category != FinanceToolCategoryMarketData {
		t.Fatalf("market category = %q, want %q", market.Category, FinanceToolCategoryMarketData)
	}
	if market.Schema == nil || market.Schema.Props["asset_type"] == nil || market.Schema.Props["symbol"] == nil {
		t.Fatalf("market schema = %+v, want asset_type and symbol props", market.Schema)
	}
	futuresRoute, ok := market.SourceRoute("futures")
	if !ok {
		t.Fatal("expected futures route in market tool definition")
	}
	if len(futuresRoute.Fallbacks) != 2 {
		t.Fatalf("futures fallback count = %d, want 2", len(futuresRoute.Fallbacks))
	}
	if futuresRoute.Fallbacks[0].EndpointName != "futures_zh_spot" || futuresRoute.Fallbacks[1].EndpointName != "futures_zh_realtime" {
		t.Fatalf("unexpected futures fallback order: %+v", futuresRoute.Fallbacks)
	}

	news, ok := LookupFinanceToolDefinition("finance_news_get")
	if !ok {
		t.Fatal("expected finance_news_get definition")
	}
	if news.Category != FinanceToolCategoryNews {
		t.Fatalf("news category = %q, want %q", news.Category, FinanceToolCategoryNews)
	}
	if news.Schema == nil || news.Schema.Props["topic_type"] == nil {
		t.Fatalf("news schema = %+v, want topic_type", news.Schema)
	}

	economy, ok := LookupFinanceToolDefinition("economy_indicator_get")
	if !ok {
		t.Fatal("expected economy_indicator_get definition")
	}
	if economy.Category != FinanceToolCategoryEconomy {
		t.Fatalf("economy category = %q, want %q", economy.Category, FinanceToolCategoryEconomy)
	}
	cpiRoute, ok := economy.SourceRoute("china_cpi")
	if !ok {
		t.Fatal("expected china_cpi route in economy tool definition")
	}
	if len(cpiRoute.Fallbacks) != 2 {
		t.Fatalf("china_cpi fallback count = %d, want 2", len(cpiRoute.Fallbacks))
	}
	if cpiRoute.Fallbacks[0].EndpointName != "macro_china_cpi" || cpiRoute.Fallbacks[1].EndpointName != "macro_china_cpi_monthly" {
		t.Fatalf("unexpected china_cpi fallback order: %+v", cpiRoute.Fallbacks)
	}
}

func TestFinanceProviderGetMarketDataFallsBackToSecondarySource(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/futures_zh_spot":
			http.Error(w, "primary unavailable", http.StatusBadGateway)
		case "/api/public/futures_zh_realtime":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"symbol":        "AU0",
					"exchange":      "SHFE",
					"name":          "沪金主力",
					"trade":         578.32,
					"changepercent": 1.23,
					"ticktime":      "14:35:00",
					"tradedate":     "2026-03-26",
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	provider := NewFinanceProvider(srv.URL)
	result, err := provider.GetMarketData(context.Background(), FinanceMarketDataRequest{
		AssetType: "futures",
		Symbol:    "AU0",
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("GetMarketData returned error: %v", err)
	}
	if result.ToolName != "finance_market_data_get" {
		t.Fatalf("tool name = %q, want %q", result.ToolName, "finance_market_data_get")
	}
	if result.Source != "futures_zh_realtime" {
		t.Fatalf("source = %q, want %q", result.Source, "futures_zh_realtime")
	}
	if len(result.Records) != 1 {
		t.Fatalf("record count = %d, want 1", len(result.Records))
	}
	if got := result.Records[0]["symbol"]; got != "AU0" {
		t.Fatalf("record symbol = %#v, want %q", got, "AU0")
	}
}

func TestFinanceProviderGetEconomyIndicatorReturnsDiagnosticErrorWhenAllSourcesFail(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/macro_china_cpi", "/api/public/macro_china_cpi_monthly":
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	provider := NewFinanceProvider(srv.URL)
	_, err := provider.GetEconomyIndicator(context.Background(), FinanceEconomyIndicatorRequest{
		Indicator: "china_cpi",
		Limit:     2,
	})
	if err == nil {
		t.Fatal("expected GetEconomyIndicator to return error")
	}
	if got := err.Error(); got == "" || !containsAll(got, "china_cpi", "macro_china_cpi", "macro_china_cpi_monthly") {
		t.Fatalf("error = %q, want diagnostic source details", got)
	}
}

func containsAll(input string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(input, part) {
			return false
		}
	}
	return true
}
