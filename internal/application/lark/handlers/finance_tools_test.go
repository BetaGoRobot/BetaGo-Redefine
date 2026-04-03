package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/aktool"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

func TestFinanceToolDiscoverFiltersAndReturnsStableSchema(t *testing.T) {
	metaData := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_user"}
	if err := FinanceToolDiscover.Handle(context.Background(), nil, metaData, FinanceToolDiscoverArgs{
		Category: "economy",
		Limit:    1,
	}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	raw := FinanceToolDiscover.ToolSpec().Result(metaData)
	if raw == "" {
		t.Fatal("expected discover result")
	}

	var result FinanceToolDiscoverResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal discover result: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(result.Tools))
	}
	if result.Tools[0].ToolName != "economy_indicator_get" {
		t.Fatalf("tool name = %q, want %q", result.Tools[0].ToolName, "economy_indicator_get")
	}
	if result.Tools[0].Schema == nil || result.Tools[0].Schema.Props["indicator"] == nil {
		t.Fatalf("schema = %+v, want indicator prop", result.Tools[0].Schema)
	}
	if len(result.Tools[0].Categories) == 0 || result.Tools[0].Categories[0] != "economy" {
		t.Fatalf("categories = %+v, want include economy", result.Tools[0].Categories)
	}
}

func TestFinanceToolDiscoverToolSpecUsesEnumsOnly(t *testing.T) {
	spec := FinanceToolDiscover.ToolSpec()
	if spec.Params == nil {
		t.Fatal("expected discover params")
	}
	if _, ok := spec.Params.Props["query"]; ok {
		t.Fatal("discover schema should not expose free-form query")
	}

	category := spec.Params.Props["category"]
	if category == nil {
		t.Fatal("expected category prop")
	}
	if got := len(category.Enum); got != 3 {
		t.Fatalf("category enum count = %d, want 3", got)
	}

	toolNames := spec.Params.Props["tool_names"]
	if toolNames == nil {
		t.Fatal("expected tool_names prop")
	}
	if len(toolNames.Items) != 1 {
		t.Fatalf("tool_names items len = %d, want 1", len(toolNames.Items))
	}
	if got := len(toolNames.Items[0].Enum); got != 3 {
		t.Fatalf("tool_names enum count = %d, want 3", got)
	}
}

func TestBuildLarkToolsIncludesDiscoverButNotInjectableFinanceTools(t *testing.T) {
	useWorkspaceConfigPath(t)
	tools := BuildLarkTools()

	if _, ok := tools.Get("finance_tool_discover"); !ok {
		t.Fatal("expected finance_tool_discover in default toolset")
	}
	for _, name := range []string{
		"gold_price_get",
		"stock_zh_a_get",
	} {
		if _, ok := tools.Get(name); !ok {
			t.Fatalf("expected legacy finance tool %q to remain in default toolset", name)
		}
	}
	for _, name := range []string{
		"finance_market_data_get",
		"finance_news_get",
		"economy_indicator_get",
	} {
		if _, ok := tools.Get(name); ok {
			t.Fatalf("default toolset should not expose %q", name)
		}
	}
}

func TestBuildInjectableFinanceToolsExposeReadOnlyFinanceHandlers(t *testing.T) {
	tools := BuildInjectableFinanceTools()

	for _, name := range []string{
		"finance_market_data_get",
		"finance_news_get",
		"economy_indicator_get",
	} {
		if _, ok := tools.Get(name); !ok {
			t.Fatalf("injectable finance tools missing %q", name)
		}
	}
	if _, ok := tools.Get("finance_tool_discover"); ok {
		t.Fatal("injectable finance toolset should not include finance_tool_discover")
	}
}

func TestFinanceMarketDataHandlerReturnsStructuredJSON(t *testing.T) {
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

	handler := financeMarketDataHandler{provider: aktool.NewFinanceProvider(srv.URL)}
	metaData := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_user"}
	if err := handler.Handle(context.Background(), nil, metaData, FinanceMarketDataArgs{
		AssetType: "gold",
		Limit:     1,
	}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	raw := handler.ToolSpec().Result(metaData)
	if raw == "" {
		t.Fatal("expected market data result")
	}

	var result aktool.FinanceToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal market result: %v", err)
	}
	if result.ToolName != "finance_market_data_get" {
		t.Fatalf("tool name = %q, want %q", result.ToolName, "finance_market_data_get")
	}
	if result.Source != "spot_quotations_sge" {
		t.Fatalf("source = %q, want %q", result.Source, "spot_quotations_sge")
	}
	if len(result.Records) != 1 {
		t.Fatalf("record count = %d, want 1", len(result.Records))
	}
}
