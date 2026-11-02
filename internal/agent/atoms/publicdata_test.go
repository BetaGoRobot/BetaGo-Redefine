package atoms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	jsonschema "github.com/google/jsonschema-go/jsonschema"
)

func publicDefinition(t *testing.T, defs []Definition, name string) Definition {
	t.Helper()
	for _, d := range defs {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("missing %s", name)
	return Definition{}
}
func TestPublicDataRegistersOnlyExplicitProviders(t *testing.T) {
	defs, err := PublicDataDefinitions(PublicDataOptions{})
	if err != nil || len(defs) != 0 {
		t.Fatalf("implicit tools %v %v", defs, err)
	}
	defs, err = PublicDataDefinitions(PublicDataOptions{HitokotoURL: "https://v1.hitokoto.cn"})
	if err != nil || len(defs) != 1 || defs[0].Name != "oneword_get" {
		t.Fatalf("quote registration %v %v", defs, err)
	}
	defs, err = PublicDataDefinitions(PublicDataOptions{AKShareURL: "https://akshare.example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gold_price_get", "stock_zh_a_get", "finance_market_data_get", "finance_news_get", "economy_indicator_get", "finance_tool_discover", "stock_hot_search_get"} {
		d := publicDefinition(t, defs, name)
		if d.SideEffect || d.AutoExecute {
			t.Fatalf("data read owns delivery %s", name)
		}
	}
	if len(defs) != 7 {
		t.Fatalf("unexpected capability count %d", len(defs))
	}
}
func TestPublicDataSchemasRejectUnsupportedIntervalCombinationsAndScopeOverride(t *testing.T) {
	defs, err := PublicDataDefinitions(PublicDataOptions{AKShareURL: "https://akshare.example"})
	if err != nil {
		t.Fatal(err)
	}
	d := publicDefinition(t, defs, "finance_market_data_get")
	var schema jsonschema.Schema
	if err = json.Unmarshal(d.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	v, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"asset_type":"stock","symbol":"600519","interval":"daily"}`, `{"asset_type":"futures","symbol":"AU0","interval":"daily"}`, `{"asset_type":"stock","symbol":"600519","chat_id":"private"}`, `{"asset_type":"index","symbol":"399006","period":"7"}`, `{"asset_type":"gold","limit":501}`} {
		var value any
		_ = json.Unmarshal([]byte(raw), &value)
		if v.Validate(value) == nil {
			t.Errorf("schema advertises %s", raw)
		}
	}
	for _, raw := range []string{`{"asset_type":"stock","symbol":"600519","interval":"intraday","period":"5"}`, `{"asset_type":"index","symbol":"399006","interval":"daily"}`, `{"asset_type":"gold","interval":"daily"}`, `{"asset_type":"futures","symbol":"AU0","interval":"realtime","futures_market":"CF"}`} {
		var value any
		_ = json.Unmarshal([]byte(raw), &value)
		if err = v.Validate(value); err != nil {
			t.Errorf("valid request %s: %v", raw, err)
		}
	}
}
func TestPublicDataDiscoveryReturnsActualConfiguredSchemas(t *testing.T) {
	defs, err := PublicDataDefinitions(PublicDataOptions{AKShareURL: "https://akshare.example"})
	if err != nil {
		t.Fatal(err)
	}
	d := publicDefinition(t, defs, "finance_tool_discover")
	raw, err := d.Run(context.Background(), Invocation{}, `{"category":"market","tool_names":["stock_zh_a_get"],"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Tools []struct {
			Name   string          `json:"tool_name"`
			Schema json.RawMessage `json:"schema"`
		}
		Truncated bool `json:"truncated"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil || len(result.Tools) != 1 || result.Tools[0].Name != "stock_zh_a_get" || string(result.Tools[0].Schema) != string(publicDefinition(t, defs, "stock_zh_a_get").Parameters) {
		t.Fatalf("discovery %s", raw)
	}
	if strings.Contains(raw, "oneword_get") || strings.Contains(raw, "akshare.example") {
		t.Fatalf("discovery exposes absent tools or configuration %s", raw)
	}
}
func TestPublicDataAtomWireReadAndUnavailableObservation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/public/stock_news_em" {
			_ = json.NewEncoder(w).Encode([]map[string]any{{"新闻标题": "新闻", "发布时间": "2026-10-27 09:30:00", "新闻链接": "https://example.com/news"}})
			return
		}
		http.Error(w, "private diagnostic body", 503)
	}))
	defer srv.Close()
	defs, err := PublicDataDefinitions(PublicDataOptions{AKShareURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	news := publicDefinition(t, defs, "finance_news_get")
	raw, err := news.Run(context.Background(), Invocation{}, `{"topic_type":"stock","symbol":"600519"}`)
	if err != nil || !strings.Contains(raw, `"available":true`) || !strings.Contains(raw, `"source_refs"`) {
		t.Fatalf("wire result %s %v", raw, err)
	}
	if _, err = news.Run(context.Background(), Invocation{}, `{"topic_type":"stock","symbol":"600519","unknown":1}`); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("direct call accepts override %v", err)
	}
	macro := publicDefinition(t, defs, "economy_indicator_get")
	raw, err = macro.Run(context.Background(), Invocation{}, `{"indicator":"china_cpi"}`)
	if err != nil || !strings.Contains(raw, `"available":false`) || strings.Contains(raw, "private") {
		t.Fatalf("failure %s %v", raw, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = macro.Run(ctx, Invocation{}, `{"indicator":"china_cpi"}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel %v", err)
	}
}

func TestPublicDataGoldSchemaRejectsHourlyDailyConversion(t *testing.T) {
	defs, err := PublicDataDefinitions(PublicDataOptions{AKShareURL: "https://akshare.example"})
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err = json.Unmarshal(publicDefinition(t, defs, "gold_price_get").Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Validate(map[string]any{"hours": float64(1), "interval": "daily"}) == nil {
		t.Fatal("schema advertises hourly query as daily data")
	}
}
