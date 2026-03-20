package aktool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPProviderGetRealtimeGoldPrice(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/spot_quotations_sge" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"品种": "Au99.99", "时间": "09:31:00", "现价": 578.32, "更新时间": "2026-03-20 09:31:00"},
		})
	}))
	defer srv.Close()

	provider := httpProvider{baseURL: srv.URL}
	rows, err := provider.GetRealtimeGoldPrice(context.Background())
	if err != nil {
		t.Fatalf("GetRealtimeGoldPrice returned error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Kind != "Au99.99" || rows[0].Price != 578.32 {
		t.Fatalf("unexpected row: %#v", rows[0])
	}
}

func TestHTTPProviderGetHistoryGoldPrice(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/spot_hist_sge" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"date": "2026-03-19", "open": 575.0, "close": 578.0, "low": 574.0, "high": 579.0},
		})
	}))
	defer srv.Close()

	provider := httpProvider{baseURL: srv.URL}
	rows, err := provider.GetHistoryGoldPrice(context.Background())
	if err != nil {
		t.Fatalf("GetHistoryGoldPrice returned error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Date != "2026-03-19" || rows[0].High != 579.0 {
		t.Fatalf("unexpected row: %#v", rows[0])
	}
}

func TestHTTPProviderGetStockPriceRT(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/stock_zh_a_minute" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "sh600988" {
			t.Fatalf("unexpected symbol: %s", r.URL.Query().Get("symbol"))
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"day": "2026-03-20 09:31:00", "open": "10.10", "high": "10.30", "low": "10.05", "close": "10.28", "volume": "12345"},
		})
	}))
	defer srv.Close()

	provider := httpProvider{baseURL: srv.URL}
	rows, err := provider.GetStockPriceRT(context.Background(), "600988")
	if err != nil {
		t.Fatalf("GetStockPriceRT returned error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].DateTime != "2026-03-20 09:31:00" || rows[0].Close != "10.28" {
		t.Fatalf("unexpected row: %#v", rows[0])
	}
}

func TestHTTPProviderGetStockSymbolInfo(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/stock_individual_info_em" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "600988" {
			t.Fatalf("unexpected symbol: %s", r.URL.Query().Get("symbol"))
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"item": "股票简称", "value": "赤峰黄金"},
			{"item": "行业", "value": "有色金属"},
		})
	}))
	defer srv.Close()

	provider := httpProvider{baseURL: srv.URL}
	name, err := provider.GetStockSymbolInfo(context.Background(), "600988")
	if err != nil {
		t.Fatalf("GetStockSymbolInfo returned error: %v", err)
	}
	if name != "赤峰黄金" {
		t.Fatalf("unexpected stock name: %s", name)
	}
}
