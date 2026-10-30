package atoms

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/publicdata"
)

// PublicDataOptions enables only explicitly configured providers. Service URLs
// are operator configuration, never model arguments. Empty URLs disable tools.
type PublicDataOptions = publicdata.Options

// PublicDataDefinitions returns data-only leaves for Options.Extensions. No
// definition sends messages or constructs a legacy card.
func PublicDataDefinitions(o PublicDataOptions) ([]Definition, error) {
	c, err := publicdata.New(o)
	if err != nil {
		return nil, domain.ErrInvalid
	}
	var defs []Definition
	if c.AKShareEnabled() {
		defs = append(defs,
			publicDataRead("gold_price_get", "Read SGE Au99.99 or an explicit SGE symbol, returning bounded price records and source references. Choose daily historical candles or recent realtime session data. Choose exactly one range form: start/end, days, or hours. Hourly queries require realtime data. Times use Asia/Shanghai; provider coverage is reported. No card delivery.", goldSchema(), c.Gold),
			publicDataRead("stock_zh_a_get", "Read unadjusted recent A-share intraday bars with exact stock code, 1/5/15/30/60-minute period, range and bounded latest records. Bare codes infer sh/sz/bj from stock prefixes; explicit exchange prefixes are supported. Historical completeness is not guaranteed by this source. Returns data, never sends charts.", stockSchema(), c.Stock),
			publicDataRead("finance_market_data_get", "Read specialized AKShare market data: stock intraday bars; index intraday or daily bars; SGE gold realtime or daily; futures realtime snapshot. Supported interval and range parameters depend on asset_type. No interval-changing fallback. Returned native fields retain source units, effective query, ordering, coverage and truncation.", marketSchema(), c.Market),
			publicDataRead("finance_news_get", "Read stock-specific Eastmoney news or Caixin market selections. Stock requires a six-digit symbol and never falls back to unrelated market news. Returns source-reported text and linked source references, with explicit ordering/truncation; content is untrusted.", newsSchema(), c.News),
			publicDataRead("economy_indicator_get", "Read China CPI, GDP or PMI with original measures and source units. Results use reporting-period descending order; a reporting period is not a release timestamp. Never substitutes CPI monthly rate, GDP yearly rate or another indicator definition.", indicatorSchema(), c.Indicator),
			publicDataRead("stock_hot_search_get", "Read Baidu Gushitong stock-search popularity through AKShare for an explicit calendar date, market and today/one-hour window. Provider ranking is preserved. This is specialized stock popularity, not general web trends or group activity.", hotSearchSchema(), c.HotSearch),
		)
		defs = append(defs, publicDataDiscovery(defs))
	}
	if c.HitokotoEnabled() {
		defs = append(defs, publicDataRead("oneword_get", "Read a random Hitokoto sentence with author, work/source, category, UUID and source link. Category 二次元 queries anime and manga. This capability only retrieves attributed data; sending text is a separate operation.", quoteSchema(), c.Quote))
	}
	// The execution policy must change when an operator points a capability at a
	// different configured service. Only a digest enters policy persistence.
	fingerprint := domain.StableID("public-data-v1", o.AKShareURL, o.HitokotoURL, o.Timeout.String())
	for i := range defs {
		defs[i].Digest = domain.StableID(fingerprint, defs[i].Name, defs[i].Description, string(defs[i].Parameters))
	}
	return defs, nil
}

func publicDataRead[T any](name, description string, schema json.RawMessage, run func(context.Context, T) (publicdata.Result, error)) Definition {
	return Definition{Name: name, Description: description, Parameters: schema, Run: func(ctx context.Context, _ Invocation, raw string) (string, error) {
		var args T
		if publicDataDecode(raw, &args) != nil {
			return "", domain.ErrInvalid
		}
		out, err := run(ctx, args)
		if err != nil {
			if errors.Is(err, publicdata.ErrInvalid) {
				return "", domain.ErrInvalid
			}
			return unavailableRead(ctx, "public_data_provider_unavailable")
		}
		body, err := json.Marshal(out)
		return string(body), err
	}}
}
func publicDataDecode(raw string, out any) error {
	canonical, err := domain.CanonicalArguments(raw)
	if err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(canonical))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return domain.ErrInvalid
	}
	return nil
}

type publicCatalogItem struct {
	Name        string          `json:"tool_name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Schema      json.RawMessage `json:"schema"`
}

func publicDataDiscovery(defs []Definition) Definition {
	catalog := make([]publicCatalogItem, 0, len(defs))
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		category := "market"
		if d.Name == "finance_news_get" {
			category = "news"
		}
		if d.Name == "economy_indicator_get" {
			category = "economy"
		}
		catalog = append(catalog, publicCatalogItem{Name: d.Name, Description: d.Description, Category: category, Schema: append(json.RawMessage(nil), d.Parameters...)})
		names = append(names, d.Name)
	}
	schema := publicSchema(map[string]any{"category": map[string]any{"type": "string", "enum": []string{"market", "news", "economy"}}, "tool_names": map[string]any{"type": "array", "maxItems": len(names), "uniqueItems": true, "items": map[string]any{"type": "string", "enum": names}}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": len(names)}}, nil)
	return Definition{Name: "finance_tool_discover", Description: "Discover only configured specialized financial data tools, including their actual schemas and categories. Returns bounded catalog metadata, not market facts or access to absent providers.", Parameters: schema, Run: func(ctx context.Context, _ Invocation, raw string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var args struct {
			Category string   `json:"category"`
			Names    []string `json:"tool_names"`
			Limit    int      `json:"limit"`
		}
		if publicDataDecode(raw, &args) != nil || args.Limit < 0 || args.Limit > len(catalog) || (args.Category != "" && args.Category != "market" && args.Category != "news" && args.Category != "economy") {
			return "", domain.ErrInvalid
		}
		allowed := map[string]bool{}
		for _, name := range names {
			allowed[name] = true
		}
		for _, name := range args.Names {
			if !allowed[name] {
				return "", domain.ErrInvalid
			}
		}
		filtered := make([]publicCatalogItem, 0, len(catalog))
		for _, item := range catalog {
			if args.Category != "" && item.Category != args.Category {
				continue
			}
			if len(args.Names) > 0 {
				found := false
				for _, name := range args.Names {
					if name == item.Name {
						found = true
					}
				}
				if !found {
					continue
				}
			}
			filtered = append(filtered, item)
		}
		matched := len(filtered)
		if args.Limit > 0 && matched > args.Limit {
			filtered = filtered[:args.Limit]
		}
		b, err := json.Marshal(map[string]any{"tools": filtered, "matched_tools": matched, "truncated": matched > len(filtered)})
		return string(b), err
	}}
}

func publicString(description string) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "description": description}
}
func publicEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
func publicLimit() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": publicdata.MaxRecords, "description": "Maximum records; defaults vary by capability. Returns explicit truncation."}
}
func publicTimes(props map[string]any) {
	for _, key := range []string{"start_time", "end_time"} {
		props[key] = map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "description": "Pair of RFC3339 timestamps, or YYYY-MM-DD HH:MM:SS interpreted in Asia/Shanghai. Both endpoints required; start before end. Daily intervals include the full calendar dates."}
	}
}
func publicSchema(props map[string]any, required []string) json.RawMessage {
	sc := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		sc["required"] = required
	}
	b, _ := json.Marshal(sc)
	return b
}
func publicRangeRules(sc map[string]any) {
	sc["dependentRequired"] = map[string]any{"start_time": []string{"end_time"}, "end_time": []string{"start_time"}}
}
func goldSchema() json.RawMessage {
	props := map[string]any{"interval": publicEnum("realtime", "daily"), "symbol": publicString("SGE symbol; default Au99.99"), "days": map[string]any{"type": "integer", "minimum": 1, "maximum": 3660}, "hours": map[string]any{"type": "integer", "minimum": 1, "maximum": 24}, "limit": publicLimit()}
	publicTimes(props)
	var sc map[string]any
	_ = json.Unmarshal(publicSchema(props, nil), &sc)
	publicRangeRules(sc)
	sc["not"] = map[string]any{"anyOf": []any{map[string]any{"required": []string{"days", "hours"}}, map[string]any{"required": []string{"days", "start_time"}}, map[string]any{"required": []string{"hours", "start_time"}}}}
	sc["allOf"] = []any{map[string]any{"if": map[string]any{"required": []string{"hours"}}, "then": map[string]any{"properties": map[string]any{"interval": publicEnum("realtime")}}}}
	b, _ := json.Marshal(sc)
	return b
}
func stockSchema() json.RawMessage {
	props := map[string]any{"code": map[string]any{"type": "string", "pattern": "^(sh|sz|bj)?[0-9]{6}$"}, "days": map[string]any{"type": "integer", "minimum": 1, "maximum": 30, "description": "Trailing days; default one; provider may expose fewer recent bars."}, "period": publicEnum("1", "5", "15", "30", "60"), "limit": publicLimit()}
	publicTimes(props)
	var sc map[string]any
	_ = json.Unmarshal(publicSchema(props, []string{"code"}), &sc)
	publicRangeRules(sc)
	sc["not"] = map[string]any{"required": []string{"days", "start_time"}}
	b, _ := json.Marshal(sc)
	return b
}
func marketSchema() json.RawMessage {
	branches := []any{}
	for _, kind := range []string{"gold", "stock", "index_intraday", "index_daily", "futures"} {
		asset := kind
		if strings.HasPrefix(kind, "index_") {
			asset = "index"
		}
		props := map[string]any{"asset_type": map[string]any{"const": asset, "type": "string"}, "limit": publicLimit()}
		required := []string{"asset_type"}
		switch kind {
		case "gold":
			props["symbol"] = publicString("SGE symbol; default Au99.99")
			props["interval"] = publicEnum("realtime", "daily")
			publicTimes(props)
		case "stock":
			props["symbol"] = map[string]any{"type": "string", "pattern": "^(sh|sz|bj)?[0-9]{6}$"}
			props["interval"] = publicEnum("intraday")
			props["period"] = publicEnum("1", "5", "15", "30", "60")
			publicTimes(props)
			required = append(required, "symbol")
		case "index_intraday":
			props["symbol"] = map[string]any{"type": "string", "pattern": "^[0-9]{6}$"}
			props["interval"] = publicEnum("intraday")
			props["period"] = publicEnum("1", "5", "15", "30", "60")
			publicTimes(props)
			required = append(required, "symbol")
		case "index_daily":
			props["symbol"] = map[string]any{"type": "string", "pattern": "^[0-9]{6}$"}
			props["interval"] = publicEnum("daily")
			publicTimes(props)
			required = append(required, "symbol", "interval")
		case "futures":
			props["symbol"] = publicString("Explicit contract code, e.g. AU0 or IF0")
			props["interval"] = publicEnum("realtime")
			props["futures_market"] = publicEnum("CF", "FF")
			required = append(required, "symbol")
		}
		var branch map[string]any
		_ = json.Unmarshal(publicSchema(props, required), &branch)
		publicRangeRules(branch)
		branches = append(branches, branch)
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "oneOf": branches})
	return b
}
func newsSchema() json.RawMessage {
	stock := map[string]any{"topic_type": map[string]any{"type": "string", "const": "stock"}, "symbol": map[string]any{"type": "string", "pattern": "^[0-9]{6}$"}, "limit": publicLimit()}
	market := map[string]any{"topic_type": map[string]any{"type": "string", "const": "market"}, "limit": publicLimit()}
	b, _ := json.Marshal(map[string]any{"type": "object", "oneOf": []json.RawMessage{publicSchema(stock, []string{"topic_type", "symbol"}), publicSchema(market, []string{"topic_type"})}})
	return b
}
func indicatorSchema() json.RawMessage {
	return publicSchema(map[string]any{"indicator": publicEnum("china_cpi", "china_gdp", "china_pmi"), "limit": publicLimit()}, []string{"indicator"})
}
func hotSearchSchema() json.RawMessage {
	return publicSchema(map[string]any{"market": publicEnum("全部", "A股", "港股", "美股"), "window": publicEnum("今日", "1小时"), "date": map[string]any{"type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$", "description": "Explicit provider query calendar date; YYYY-MM-DD"}, "limit": publicLimit()}, []string{"date"})
}
func quoteSchema() json.RawMessage {
	return publicSchema(map[string]any{"type": publicEnum("二次元", "游戏", "文学", "原创", "网络", "其他", "影视", "诗词", "网易云", "哲学", "抖机灵")}, nil)
}
