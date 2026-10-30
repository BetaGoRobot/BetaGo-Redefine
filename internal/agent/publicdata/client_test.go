package publicdata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Options{AKShareURL: srv.URL, HitokotoURL: srv.URL + "/quote"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func jsonRows(w http.ResponseWriter, rows any) { _ = json.NewEncoder(w).Encode(rows) }

func TestIndexDailyTransmitsRangeAndReturnsLatestBoundedRecords(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/public/index_zh_a_hist" {
			t.Errorf("wire %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("period") != "daily" || q.Get("symbol") != "399006" || q.Get("start_date") != "20261001" || q.Get("end_date") != "20261027" {
			t.Errorf("query %v", q)
		}
		jsonRows(w, []map[string]any{{"日期": "2026-10-03", "收盘": 3}, {"日期": "2026-10-01", "收盘": 1}, {"日期": "2026-10-02", "收盘": 2}})
	})
	got, err := c.Market(context.Background(), MarketRequest{AssetType: "index", Symbol: "399006", Interval: "daily", StartTime: "2026-10-01T00:00:00+08:00", EndTime: "2026-10-27T23:59:59+08:00", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Available || !got.Untrusted || !got.Truncated || got.MatchedRecords != 3 || len(got.Records) != 2 || got.Records[0].Data["日期"] != "2026-10-02" || len(got.Sources) != 1 || got.Records[0].ID == "" || got.FetchedAt.IsZero() {
		t.Fatalf("result %+v", got)
	}
}

func TestStockNormalizesShenzhenAndFiltersRange(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/public/stock_zh_a_minute" || q.Get("symbol") != "sz000001" || q.Get("period") != "5" || q.Has("start_time") {
			t.Errorf("wire %s %v", r.URL.Path, q)
		}
		jsonRows(w, []map[string]any{{"day": "2026-10-26 09:30:00", "close": "1"}, {"day": "2026-10-27 09:30:00", "close": "2"}, {"day": "2026-10-27 10:30:00", "close": "3"}})
	})
	got, err := c.Stock(context.Background(), StockRequest{Code: "000001", Period: "5", StartTime: "2026-10-27T09:00:00+08:00", EndTime: "2026-10-27T10:00:00+08:00"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 || got.Records[0].Data["close"] != "2" || got.Query["symbol"] != "sz000001" || !strings.Contains(got.Coverage, "recent") {
		t.Fatalf("result %+v", got)
	}
}

func TestGoldRealtimeUsesProviderUpdateDateInsteadOfMachineToday(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/spot_quotations_sge" || r.URL.Query().Get("symbol") != "Au99.99" {
			t.Errorf("wire %s %v", r.URL.Path, r.URL.Query())
		}
		jsonRows(w, []map[string]any{{"品种": "Au99.99", "时间": "09:30:00", "现价": 500, "更新时间": "2025年04月14日 22:28:55"}, {"品种": "Au99.99", "时间": "10:30:00", "现价": 501, "更新时间": "2025年04月14日 22:28:55"}})
	})
	got, err := c.Gold(context.Background(), GoldRequest{Interval: "realtime", StartTime: "2025-04-14T09:00:00+08:00", EndTime: "2025-04-14T10:00:00+08:00"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 || got.Records[0].ObservedAt != "2025-04-14T09:30:00+08:00" {
		t.Fatalf("result %+v", got)
	}
}

func TestGoldHistoryFiltersAndDoesNotSynthesizeDailyCandle(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/public/spot_hist_sge" {
			t.Errorf("unexpected fallback or merge %s", r.URL.Path)
		}
		jsonRows(w, []map[string]any{{"date": "2026-10-01T00:00:00.000", "close": 1}, {"date": "2026-10-26", "close": 2}, {"date": "2026-10-27", "close": 3}})
	})
	got, err := c.Gold(context.Background(), GoldRequest{Interval: "daily", StartTime: "2026-10-25T00:00:00+08:00", EndTime: "2026-10-27T23:59:59+08:00"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 2 || calls.Load() != 1 {
		t.Fatalf("rows %d calls %d", len(got.Records), calls.Load())
	}
}

func TestUnsupportedSemanticsAndInvalidBoundsNeverCallUpstream(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); jsonRows(w, []any{}) })
	for _, req := range []MarketRequest{
		{AssetType: "stock", Symbol: "600519", Interval: "daily"},
		{AssetType: "index", Symbol: "399006", Interval: "realtime"},
		{AssetType: "futures", Symbol: "AU0", Interval: "daily"},
		{AssetType: "futures", Symbol: "AU0", StartTime: "2026-10-01T00:00:00Z", EndTime: "2026-10-02T00:00:00Z"},
		{AssetType: "stock", Symbol: "600519", Period: "7"},
		{AssetType: "gold", Limit: 501},
		{AssetType: "gold", StartTime: "2026-10-01T00:00:00Z"},
		{AssetType: "gold", StartTime: "2026-10-02T00:00:00Z", EndTime: "2026-10-01T00:00:00Z"},
	} {
		if _, err := c.Market(context.Background(), req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v err %v", req, err)
		}
	}
	if _, err := c.Gold(context.Background(), GoldRequest{Days: 2, Hours: 3}); !errors.Is(err, ErrInvalid) {
		t.Errorf("conflicting gold range %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls %d", calls.Load())
	}
}

func TestProviderFailureDoesNotChangeNewsTopicOrIndicatorDefinition(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "sensitive upstream body", 503)
	})
	_, err := c.News(context.Background(), NewsRequest{TopicType: "stock", Symbol: "600519"})
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("news err %v", err)
	}
	_, err = c.Indicator(context.Background(), IndicatorRequest{Indicator: "china_cpi"})
	if err == nil || calls.Load() != 2 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
}

func TestNewsAndIndicatorKeepNativeMeasuresAndStableSourceReferences(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/stock_news_em":
			if r.URL.Query().Get("symbol") != "600519" {
				t.Errorf("symbol %v", r.URL.Query())
			}
			jsonRows(w, []map[string]any{{"新闻标题": "甲", "发布时间": "2026-10-27 09:00:00", "新闻链接": "https://example.com/a"}, {"新闻标题": "乙", "发布时间": "2026-10-27 10:00:00", "新闻链接": "https://example.com/b"}})
		case "/api/public/macro_china_cpi":
			jsonRows(w, []map[string]any{{"月份": "2026年09月份", "全国-同比增长": 1.2}, {"月份": "2026年10月份", "全国-同比增长": 1.3}})
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	})
	news, err := c.News(context.Background(), NewsRequest{TopicType: "stock", Symbol: "600519", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !news.Truncated || news.Records[0].Data["新闻标题"] != "乙" || len(news.Sources) != 2 {
		t.Fatalf("news %+v", news)
	}
	a, err := c.Indicator(context.Background(), IndicatorRequest{Indicator: "china_cpi", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Indicator(context.Background(), IndicatorRequest{Indicator: "china_cpi", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if a.Records[0].ID != b.Records[0].ID || a.Records[0].Data["月份"] != "2026年10月份" || a.Records[0].Data["全国-同比增长"] != json.Number("1.3") {
		t.Fatalf("indicator %+v", a)
	}
}

func TestQuoteCategoryWireAndAttribution(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/quote" || strings.Join(r.URL.Query()["c"], ",") != "a,b" || r.URL.Query().Get("encode") != "json" {
			t.Errorf("wire %s %v", r.URL.Path, r.URL.Query())
		}
		jsonRows(w, map[string]any{"id": 12, "uuid": "sentence-uuid", "hitokoto": "hello", "type": "a", "from": "Book", "from_who": "Author"})
	})
	got, err := c.Quote(context.Background(), QuoteRequest{Type: "二次元"})
	if err != nil {
		t.Fatal(err)
	}
	data := got.Records[0].Data
	if data["text"] != "hello" || data["author"] != "Author" || data["category"] != "a" || data["source"] != "Book" || got.Sources[0].URL != "https://hitokoto.cn/?uuid=sentence-uuid" {
		t.Fatalf("quote %+v", got)
	}
}

func TestHotSearchTransmitsExactProviderEnumsAndKeepsRanking(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/public/stock_hot_search_baidu" || q.Get("symbol") != "港股" || q.Get("date") != "20261027" || q.Get("time") != "1小时" {
			t.Errorf("wire %s %v", r.URL.Path, q)
		}
		jsonRows(w, []map[string]any{{"名称/代码": "甲", "综合热度": 300}, {"名称/代码": "乙", "综合热度": 200}})
	})
	got, err := c.HotSearch(context.Background(), HotSearchRequest{Market: "港股", Date: "2026-10-27", Window: "1小时", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || got.Records[0].Data["名称/代码"] != "甲" || got.Order != "provider_rank" {
		t.Fatalf("hot %+v", got)
	}
}

func TestHTTPBoundedResponsesRedirectsAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"oversize", strings.Repeat(" ", MaxResponseBytes+1), 200}, {"malformed", "not JSON", 200}, {"wrong_shape", `{"error":"bad"}`, 200}, {"redirect", "", 302}, {"rate_limit", "secret", 429},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/quote")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			if _, err := c.Indicator(context.Background(), IndicatorRequest{Indicator: "china_cpi"}); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Indicator(ctx, IndicatorRequest{Indicator: "china_cpi"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}
}

func TestConfigurationRejectsCredentialURLsAndBounds(t *testing.T) {
	for _, o := range []Options{{AKShareURL: "ftp://example.com"}, {AKShareURL: "https://u:p@example.com"}, {HitokotoURL: "https://example.com?token=secret"}, {Timeout: -time.Second}, {Timeout: 2 * time.Minute}} {
		if _, err := New(o); !errors.Is(err, ErrInvalid) {
			t.Errorf("config %+v error %v", o, err)
		}
	}
}

func TestResultBudgetKeepsWholeRecordsAndReportsTruncation(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		rows := make([]map[string]any, 80)
		for i := range rows {
			rows[i] = map[string]any{"tag": "test", "summary": strings.Repeat("中", 2000), "url": "https://example.com/" + strings.Repeat("x", 200) + string(rune('a'+i))}
		}
		jsonRows(w, rows)
	})
	got, err := c.News(context.Background(), NewsRequest{TopicType: "market", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 256<<10 || !got.Truncated || got.MatchedRecords != 80 || len(got.Records) == 0 || len(got.Records) >= 80 {
		t.Fatalf("bytes %d records %d matched %d truncated %v", len(encoded), len(got.Records), got.MatchedRecords, got.Truncated)
	}
	for _, r := range got.Records {
		if len(r.Data["summary"].(string)) != len(strings.Repeat("中", 2000)) {
			t.Fatal("silently shortened native field")
		}
	}
}

func TestRecordReferencesBindInstrumentWithoutDependingOnLimit(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		jsonRows(w, []map[string]any{{"day": "2026-10-27 09:30:00", "close": "2"}})
	})
	a, err := c.Market(context.Background(), MarketRequest{AssetType: "stock", Symbol: "600519", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Market(context.Background(), MarketRequest{AssetType: "stock", Symbol: "000001", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := c.Market(context.Background(), MarketRequest{AssetType: "stock", Symbol: "600519", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if a.Records[0].ID == b.Records[0].ID || a.Records[0].ID != a2.Records[0].ID {
		t.Fatal("record reference omits instrument identity or includes presentation limit")
	}
}

func TestGoldHourlyRangeCannotBecomeDailyCalendarData(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { jsonRows(w, []any{}) })
	if _, err := c.Gold(context.Background(), GoldRequest{Hours: 1, Interval: "daily"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("hour range changed granularity: %v", err)
	}
}
func TestFuturesMarketInferenceUsesFullContractPrefix(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		want := "CF"
		if r.URL.Query().Get("subscribe_list") == "IF0" {
			want = "FF"
		}
		if r.URL.Path != "/api/public/futures_zh_spot" || r.URL.Query().Get("market") != want || r.URL.Query().Get("adjust") != "0" {
			t.Errorf("wire %s %v want %s", r.URL.Path, r.URL.Query(), want)
		}
		jsonRows(w, []map[string]any{{"symbol": r.URL.Query().Get("subscribe_list"), "time": "093000", "current_price": 123}})
	})
	for _, symbol := range []string{"IF0", "TA0"} {
		if _, err := c.Market(context.Background(), MarketRequest{AssetType: "futures", Symbol: symbol}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestQuoteRejectsOversizedSourceIdentity(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		jsonRows(w, map[string]any{"uuid": strings.Repeat("x", 300000), "hitokoto": "hello", "from": "work", "type": "a"})
	})
	if _, err := c.Quote(context.Background(), QuoteRequest{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("accepted oversized source identity %v", err)
	}
}
