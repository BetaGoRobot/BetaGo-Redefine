package akshareapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

func repoConfigPath(t *testing.T) string {
	t.Helper()

	path, err := filepath.Abs(filepath.Join("..", "..", "..", ".dev", "config.toml"))
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	return path
}

func resetPackageStateForTest(t *testing.T) {
	t.Helper()

	defaultBackend = noopBackend{reason: "akshareapi not initialized"}
	warnOnce = sync.Once{}
	if _, err := config.LoadFileE(repoConfigPath(t)); err != nil {
		t.Fatalf("config.LoadFileE() error = %v", err)
	}
	t.Cleanup(func() {
		defaultBackend = noopBackend{reason: "akshareapi not initialized"}
		warnOnce = sync.Once{}
		if _, err := config.LoadFileE(repoConfigPath(t)); err != nil {
			t.Fatalf("config.LoadFileE() cleanup error = %v", err)
		}
	})
}

func loadAKToolConfigForTest(t *testing.T, baseURL string) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := ""
	if baseURL != "" {
		content = "[aktool_config]\nbase_url = \"" + baseURL + "\"\n"
	}
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if _, err := config.LoadFileE(configPath); err != nil {
		t.Fatalf("config.LoadFileE() error = %v", err)
	}
}

func TestCatalogIncludesFocusedDomains(t *testing.T) {
	t.Helper()

	domains := []Domain{DomainStock, DomainGold, DomainFutures, DomainInfo}
	for _, domain := range domains {
		items := EndpointsForDomain(domain)
		if len(items) == 0 {
			t.Fatalf("expected endpoints for domain %s", domain)
		}
	}

	for _, name := range []string{
		"stock_zh_a_hist",
		"spot_quotations_sge",
		"futures_zh_spot",
		"stock_news_em",
	} {
		if _, ok := EndpointByName(name); !ok {
			t.Fatalf("expected endpoint %s in generated catalog", name)
		}
	}
}

func TestCatalogIncludesNonFocusedEndpoint(t *testing.T) {
	t.Helper()

	if _, ok := EndpointByName("air_city_table"); !ok {
		t.Fatalf("expected non-focused endpoint air_city_table in generated catalog")
	}
}

func TestClientCallsTypedEndpoint(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/stock_zh_a_hist" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("symbol") != "600000" {
			t.Fatalf("unexpected symbol: %s", query.Get("symbol"))
		}
		if query.Get("period") != "daily" {
			t.Fatalf("unexpected period: %s", query.Get("period"))
		}
		if query.Get("adjust") != "qfq" {
			t.Fatalf("unexpected adjust: %s", query.Get("adjust"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"日期": "2024-01-02", "开盘": 10.2, "收盘": 10.5},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, nil)
	rows, err := client.StockZhAHist(context.Background(), StockZhAHistParams{
		Symbol: "600000",
		Period: "daily",
		Adjust: "qfq",
	})
	if err != nil {
		t.Fatalf("StockZhAHist returned error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if got := rows[0]["日期"]; got != "2024-01-02" {
		t.Fatalf("unexpected date: %#v", got)
	}
}

func TestClientCallIntoTypedStructResponse(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/spot_hist_sge" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "Au99.99" {
			t.Fatalf("unexpected symbol: %s", r.URL.Query().Get("symbol"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"date": "2024-03-19", "open": 575.0, "close": 578.0, "low": 574.0, "high": 579.0},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, nil)
	type spotHistRow struct {
		Date  string  `json:"date"`
		Open  float64 `json:"open"`
		Close float64 `json:"close"`
		Low   float64 `json:"low"`
		High  float64 `json:"high"`
	}

	var rows []spotHistRow
	err := client.CallInto(context.Background(), EndpointSpotHistSge, SpotHistSgeParams{
		Symbol: "Au99.99",
	}, &rows)
	if err != nil {
		t.Fatalf("CallInto returned error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Date != "2024-03-19" || rows[0].Open != 575.0 || rows[0].High != 579.0 {
		t.Fatalf("unexpected typed row: %#v", rows[0])
	}
}

func TestClientCallIntoGeneratedSpotHistSgeRow(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/spot_hist_sge" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"date": "2024-03-19", "open": 575.0, "close": 578.0, "low": 574.0, "high": 579.0},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, nil)
	var rows []SpotHistSgeRow
	err := client.CallInto(context.Background(), EndpointSpotHistSge, SpotHistSgeParams{Symbol: "Au99.99"}, &rows)
	if err != nil {
		t.Fatalf("CallInto returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].Date != "2024-03-19" || rows[0].High != 579.0 {
		t.Fatalf("unexpected typed rows: %#v", rows)
	}
}

func TestClientCallIntoGeneratedSpotQuotationsSgeRow(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/spot_quotations_sge" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"品种": "Au99.99", "时间": "09:31:00", "现价": 578.32, "更新时间": "2026-03-20 09:31:00"},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, nil)
	var rows []SpotQuotationsSgeRow
	err := client.CallInto(context.Background(), EndpointSpotQuotationsSge, SpotQuotationsSgeParams{}, &rows)
	if err != nil {
		t.Fatalf("CallInto returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].X品种 != "Au99.99" || rows[0].X现价 != 578.32 {
		t.Fatalf("unexpected typed rows: %#v", rows)
	}
}

func TestClientCallIntoGeneratedStockZhAMinuteRow(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/stock_zh_a_minute" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"day": "2026-03-20 09:31:00", "open": "10.10", "high": "10.30", "low": "10.05", "close": "10.28", "volume": "12345"},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, nil)
	var rows []StockZhAMinuteRow
	err := client.CallInto(context.Background(), EndpointStockZhAMinute, StockZhAMinuteParams{Symbol: "sh600988"}, &rows)
	if err != nil {
		t.Fatalf("CallInto returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].Day != "2026-03-20 09:31:00" || rows[0].Close != "10.28" {
		t.Fatalf("unexpected typed rows: %#v", rows)
	}
}

func TestClientCallIntoGeneratedStockIndividualInfoEmRow(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/stock_individual_info_em" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"item": "股票简称", "value": "赤峰黄金"},
			{"item": "行业", "value": "有色金属"},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, nil)
	var rows []StockIndividualInfoEmRow
	err := client.CallInto(context.Background(), EndpointStockIndividualInfoEm, StockIndividualInfoEmParams{Symbol: "600988"}, &rows)
	if err != nil {
		t.Fatalf("CallInto returned error: %v", err)
	}
	if len(rows) != 2 || rows[0].Item != "股票简称" || rows[0].Value != "赤峰黄金" {
		t.Fatalf("unexpected typed rows: %#v", rows)
	}
}

func TestClientRetriesOnServerError(t *testing.T) {
	t.Helper()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/stock_zh_a_hist" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		attempts++
		if attempts < 3 {
			http.Error(w, "temporary upstream failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"日期": "2024-01-03", "开盘": 10.8, "收盘": 11.0},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, nil)
	rows, err := client.StockZhAHist(context.Background(), StockZhAHistParams{
		Symbol: "600000",
		Period: "daily",
	})
	if err != nil {
		t.Fatalf("StockZhAHist returned error after retries: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func TestClientDoesNotRetryOnClientError(t *testing.T) {
	t.Helper()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, nil)
	_, err := client.StockZhAHist(context.Background(), StockZhAHistParams{
		Symbol: "600000",
	})
	if err == nil {
		t.Fatal("expected error for client-side failure")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt for 4xx response, got %d", attempts)
	}
}

func TestInitFallsBackToNoopWhenConfigMissing(t *testing.T) {
	t.Helper()

	resetPackageStateForTest(t)
	loadAKToolConfigForTest(t, "")

	Init()

	ok, reason := Status()
	if ok {
		t.Fatal("expected akshareapi to stay noop without config")
	}
	if reason != "akshareapi config missing or base url empty" {
		t.Fatalf("unexpected disable reason: %q", reason)
	}
	err := ErrUnavailable()
	if !IsUnavailable(err) {
		t.Fatalf("ErrUnavailable() = %v, want unavailable error", err)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Fatalf("ErrUnavailable() = %v, want reason %q", err, reason)
	}
}

func TestCallIntoReturnsUnavailableWhenNotInitialized(t *testing.T) {
	t.Helper()

	resetPackageStateForTest(t)
	defaultBackend = noopBackend{reason: "test noop"}

	var rows []SpotHistSgeRow
	err := CallInto(context.Background(), EndpointSpotHistSge, SpotHistSgeParams{Symbol: "Au99.99"}, &rows)
	if !IsUnavailable(err) {
		t.Fatalf("CallInto() error = %v, want unavailable", err)
	}
	if !strings.Contains(err.Error(), "test noop") {
		t.Fatalf("CallInto() error = %v, want noop reason", err)
	}
}

func TestCallRowsReturnsUnavailableWhenNotInitialized(t *testing.T) {
	t.Helper()

	resetPackageStateForTest(t)
	defaultBackend = noopBackend{reason: "test noop"}

	_, err := CallRows(context.Background(), EndpointSpotHistSge, SpotHistSgeParams{Symbol: "Au99.99"})
	if !IsUnavailable(err) {
		t.Fatalf("CallRows() error = %v, want unavailable", err)
	}
}

func TestCallByNameReturnsUnavailableWhenNotInitialized(t *testing.T) {
	t.Helper()

	resetPackageStateForTest(t)
	defaultBackend = noopBackend{reason: "test noop"}

	_, err := CallByName(context.Background(), "spot_hist_sge", SpotHistSgeParams{Symbol: "Au99.99"})
	if !IsUnavailable(err) {
		t.Fatalf("CallByName() error = %v, want unavailable", err)
	}
}

func TestCallIntoUsesPackageClientAfterInit(t *testing.T) {
	t.Helper()

	resetPackageStateForTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/spot_hist_sge" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "Au99.99" {
			t.Fatalf("unexpected symbol: %s", r.URL.Query().Get("symbol"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"date": "2024-03-19", "open": 575.0, "close": 578.0, "low": 574.0, "high": 579.0},
		})
	}))
	defer srv.Close()

	loadAKToolConfigForTest(t, srv.URL)
	Init()

	ok, reason := Status()
	if !ok || reason != "" {
		t.Fatalf("Status() = (%v, %q), want (true, \"\")", ok, reason)
	}

	var rows []SpotHistSgeRow
	err := CallInto(context.Background(), EndpointSpotHistSge, SpotHistSgeParams{Symbol: "Au99.99"}, &rows)
	if err != nil {
		t.Fatalf("CallInto() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Date != "2024-03-19" || rows[0].High != 579.0 {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestCallRowsUsesPackageClientAfterInit(t *testing.T) {
	t.Helper()

	resetPackageStateForTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/spot_hist_sge" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"date": "2024-03-20", "open": 580.0, "close": 581.0},
		})
	}))
	defer srv.Close()

	loadAKToolConfigForTest(t, srv.URL)
	Init()

	rows, err := CallRows(context.Background(), EndpointSpotHistSge, SpotHistSgeParams{Symbol: "Au99.99"})
	if err != nil {
		t.Fatalf("CallRows() error = %v", err)
	}
	if len(rows) != 1 || rows[0]["date"] != "2024-03-20" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestCallByNameUsesPackageClientAfterInit(t *testing.T) {
	t.Helper()

	resetPackageStateForTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/stock_zh_a_hist" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "600000" {
			t.Fatalf("unexpected symbol: %s", r.URL.Query().Get("symbol"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"日期": "2024-01-02", "开盘": 10.2, "收盘": 10.5},
		})
	}))
	defer srv.Close()

	loadAKToolConfigForTest(t, srv.URL)
	Init()

	rows, err := CallByName(context.Background(), "stock_zh_a_hist", StockZhAHistParams{
		Symbol: "600000",
	})
	if err != nil {
		t.Fatalf("CallByName() error = %v", err)
	}
	if len(rows) != 1 || rows[0]["日期"] != "2024-01-02" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}
