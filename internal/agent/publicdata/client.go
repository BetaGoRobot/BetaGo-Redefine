// Package publicdata reads explicitly configured financial and quotation
// providers. It has no group credentials, message delivery, or old runtime imports.
package publicdata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const MaxResponseBytes = 4 << 20
const MaxRecords = 500
const MaxResultBytes = 256 << 10

var ErrInvalid = errors.New("invalid public data input")
var ErrUnavailable = errors.New("public data provider unavailable")
var china = time.FixedZone("Asia/Shanghai", 8*60*60)

type Options struct {
	AKShareURL  string
	HitokotoURL string
	HTTPClient  *http.Client
	Timeout     time.Duration
}

type Client struct {
	akshare, hitokoto string
	http              *http.Client
	timeout           time.Duration
}

type Source struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
}
type Record struct {
	ID         string         `json:"id"`
	ObservedAt string         `json:"observed_at,omitempty"`
	Data       map[string]any `json:"data"`
}
type Result struct {
	Available       bool           `json:"available"`
	Provider        string         `json:"provider"`
	Endpoint        string         `json:"endpoint"`
	FetchedAt       time.Time      `json:"fetched_at"`
	Query           map[string]any `json:"query"`
	Records         []Record       `json:"records"`
	Sources         []Source       `json:"source_refs"`
	ObservedRecords int            `json:"observed_records"`
	MatchedRecords  int            `json:"matched_records"`
	Truncated       bool           `json:"truncated"`
	Order           string         `json:"order"`
	Coverage        string         `json:"coverage"`
	Untrusted       bool           `json:"untrusted"`
}

type MarketRequest struct {
	AssetType     string `json:"asset_type"`
	Symbol        string `json:"symbol"`
	Interval      string `json:"interval"`
	Period        string `json:"period"`
	FuturesMarket string `json:"futures_market"`
	StartTime     string `json:"start_time"`
	EndTime       string `json:"end_time"`
	Limit         int    `json:"limit"`
}
type GoldRequest struct {
	Interval  string `json:"interval"`
	Symbol    string `json:"symbol"`
	Days      int    `json:"days"`
	Hours     int    `json:"hours"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Limit     int    `json:"limit"`
}
type StockRequest struct {
	Code      string `json:"code"`
	Days      int    `json:"days"`
	Period    string `json:"period"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Limit     int    `json:"limit"`
}
type NewsRequest struct {
	TopicType string `json:"topic_type"`
	Symbol    string `json:"symbol"`
	Limit     int    `json:"limit"`
}
type IndicatorRequest struct {
	Indicator string `json:"indicator"`
	Limit     int    `json:"limit"`
}
type QuoteRequest struct {
	Type string `json:"type"`
}
type HotSearchRequest struct {
	Market string `json:"market"`
	Date   string `json:"date"`
	Window string `json:"window"`
	Limit  int    `json:"limit"`
}

func New(o Options) (*Client, error) {
	for _, raw := range []string{o.AKShareURL, o.HitokotoURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "\r\n\x00") {
			return nil, ErrInvalid
		}
	}
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.Timeout < 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	h := http.Client{}
	if o.HTTPClient != nil {
		h = *o.HTTPClient
	}
	// Configured service URLs are operator-owned; never follow a service redirect
	// to another host or expose service bodies/URLs in diagnostics.
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	h.Jar = nil
	return &Client{akshare: strings.TrimRight(o.AKShareURL, "/"), hitokoto: o.HitokotoURL, http: &h, timeout: o.Timeout}, nil
}

func (c *Client) AKShareEnabled() bool  { return c != nil && c.akshare != "" }
func (c *Client) HitokotoEnabled() bool { return c != nil && c.hitokoto != "" }

func (c *Client) get(ctx context.Context, base string, q url.Values, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if base == "" {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if len(q) > 0 {
		base += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return ErrInvalid
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil || len(body) > MaxResponseBytes || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUnavailable
	}
	d := json.NewDecoder(strings.NewReader(string(body)))
	d.UseNumber()
	if d.Decode(out) != nil {
		return ErrUnavailable
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) rows(ctx context.Context, endpoint string, q url.Values) ([]map[string]any, error) {
	if !c.AKShareEnabled() {
		return nil, ErrUnavailable
	}
	var rows []map[string]any
	if err := c.get(ctx, c.akshare+"/api/public/"+endpoint, q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, ErrUnavailable
	}
	for _, row := range rows {
		if row == nil {
			return nil, ErrUnavailable
		}
	}
	return rows, nil
}
func limit(value, defaultValue int) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > MaxRecords {
		return 0, ErrInvalid
	}
	return value, nil
}
func choose(s, defaultValue string) string {
	if s == "" {
		return defaultValue
	}
	return s
}
func member(s string, options ...string) bool {
	for _, o := range options {
		if s == o {
			return true
		}
	}
	return false
}
func validSymbol(s string) bool {
	return s != "" && len(s) <= 64 && !strings.ContainsAny(s, "\r\n\x00?&#/\\ ")
}

var stockCode = regexp.MustCompile(`^(sh|sz|bj)?[0-9]{6}$`)

func normalizeStock(s string) (string, error) {
	if !stockCode.MatchString(s) {
		return "", ErrInvalid
	}
	if len(s) == 8 {
		return s, nil
	}
	switch s[0] {
	case '6':
		return "sh" + s, nil
	case '0', '3':
		return "sz" + s, nil
	case '4', '8', '9':
		return "bj" + s, nil
	}
	return "", ErrInvalid
}
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.In(china), nil
	}
	for _, layout := range []string{time.DateTime, "2006-01-02T15:04:05.999999999", time.DateOnly, "2006年01月02日 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, china); err == nil {
			return t, nil
		}
	}
	return time.Time{}, ErrInvalid
}
func timeRange(start, end string) (time.Time, time.Time, error) {
	if start == "" && end == "" {
		return time.Time{}, time.Time{}, nil
	}
	if start == "" || end == "" {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	st, e := parseTime(start)
	if e != nil {
		return st, time.Time{}, e
	}
	et, e := parseTime(end)
	if e != nil || !et.After(st) || et.Sub(st) > 3660*24*time.Hour {
		return st, et, ErrInvalid
	}
	return st, et, nil
}
func queryRange(q map[string]any, st, et time.Time) {
	if !st.IsZero() {
		q["start_time"] = st.Format(time.RFC3339)
		q["end_time"] = et.Format(time.RFC3339)
	}
}

func (c *Client) Gold(ctx context.Context, r GoldRequest) (Result, error) {
	if r.Days < 0 || r.Days > 3660 || r.Hours < 0 || r.Hours > 24 || (r.Hours > 0 && r.Interval == "daily") || (r.Days > 0 && r.Hours > 0) || ((r.StartTime != "" || r.EndTime != "") && (r.Days > 0 || r.Hours > 0)) {
		return Result{}, ErrInvalid
	}
	st, et, err := timeRange(r.StartTime, r.EndTime)
	if err != nil {
		return Result{}, err
	}
	interval := r.Interval
	if st.IsZero() {
		et = time.Now().In(china)
		switch {
		case r.Hours > 0:
			st = et.Add(-time.Duration(r.Hours) * time.Hour)
			interval = choose(interval, "realtime")
		case r.Days > 0:
			st = et.AddDate(0, 0, -r.Days)
			if r.Days == 1 {
				interval = choose(interval, "realtime")
			} else {
				interval = choose(interval, "daily")
			}
		default:
			if interval == "realtime" {
				st = et.Add(-time.Hour)
			} else {
				st = et.AddDate(0, 0, -30)
				interval = choose(interval, "daily")
			}
		}
	} else if interval == "" {
		if et.Sub(st) <= 24*time.Hour {
			interval = "realtime"
		} else {
			interval = "daily"
		}
	}
	return c.Market(ctx, MarketRequest{AssetType: "gold", Symbol: r.Symbol, Interval: interval, StartTime: st.Format(time.RFC3339), EndTime: et.Format(time.RFC3339), Limit: r.Limit})
}
func (c *Client) Stock(ctx context.Context, r StockRequest) (Result, error) {
	if r.Days < 0 || r.Days > 30 || ((r.StartTime != "" || r.EndTime != "") && r.Days > 0) {
		return Result{}, ErrInvalid
	}
	if r.StartTime == "" && r.EndTime == "" {
		days := r.Days
		if days == 0 {
			days = 1
		}
		et := time.Now().In(china)
		r.StartTime = et.AddDate(0, 0, -days).Format(time.RFC3339)
		r.EndTime = et.Format(time.RFC3339)
	}
	return c.Market(ctx, MarketRequest{AssetType: "stock", Symbol: r.Code, Interval: "intraday", Period: r.Period, StartTime: r.StartTime, EndTime: r.EndTime, Limit: r.Limit})
}

func (c *Client) Market(ctx context.Context, r MarketRequest) (Result, error) {
	n, err := limit(r.Limit, 100)
	if err != nil {
		return Result{}, err
	}
	st, et, err := timeRange(r.StartTime, r.EndTime)
	if err != nil {
		return Result{}, err
	}
	q := url.Values{}
	query := map[string]any{"asset_type": r.AssetType, "limit": n, "timezone": "Asia/Shanghai"}
	queryRange(query, st, et)
	endpoint, timeKey, source, coverage := "", "", "", ""
	dateOnly := false
	switch r.AssetType {
	case "gold":
		r.Interval = choose(r.Interval, "realtime")
		r.Symbol = choose(r.Symbol, "Au99.99")
		if !validSymbol(r.Symbol) || r.Period != "" || r.FuturesMarket != "" || !member(r.Interval, "realtime", "daily") {
			return Result{}, ErrInvalid
		}
		q.Set("symbol", r.Symbol)
		source = "https://www.sge.com.cn/"
		if r.Interval == "daily" {
			endpoint = "spot_hist_sge"
			timeKey = "date"
			dateOnly = true
			coverage = "Historical daily candles only; dates in Asia/Shanghai; no synthetic current-day candle."
		} else {
			endpoint = "spot_quotations_sge"
			timeKey = "时间"
			coverage = "Provider recent session only; timestamps use the provider update date in Asia/Shanghai, never the machine date."
		}
	case "stock":
		r.Interval = choose(r.Interval, "intraday")
		r.Period = choose(r.Period, "1")
		if r.Interval != "intraday" || !member(r.Period, "1", "5", "15", "30", "60") || r.FuturesMarket != "" {
			return Result{}, ErrInvalid
		}
		r.Symbol, err = normalizeStock(r.Symbol)
		if err != nil {
			return Result{}, err
		}
		endpoint = "stock_zh_a_minute"
		timeKey = "day"
		q.Set("symbol", r.Symbol)
		q.Set("period", r.Period)
		q.Set("adjust", "")
		source = "https://finance.sina.com.cn/"
		coverage = "Unadjusted recent intraday bars available from provider; requested range is filtered locally and may not be fully covered."
	case "index":
		r.Interval = choose(r.Interval, "intraday")
		if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(r.Symbol) || r.FuturesMarket != "" {
			return Result{}, ErrInvalid
		}
		q.Set("symbol", r.Symbol)
		source = "https://quote.eastmoney.com/center/hszs.html"
		if r.Interval == "daily" {
			if r.Period != "" {
				return Result{}, ErrInvalid
			}
			endpoint = "index_zh_a_hist"
			timeKey = "日期"
			dateOnly = true
			q.Set("period", "daily")
			coverage = "Historical daily index candles; requested calendar dates inclusive in Asia/Shanghai."
			if !st.IsZero() {
				q.Set("start_date", st.Format("20060102"))
				q.Set("end_date", et.Format("20060102"))
			}
		} else if r.Interval == "intraday" {
			r.Period = choose(r.Period, "1")
			if !member(r.Period, "1", "5", "15", "30", "60") {
				return Result{}, ErrInvalid
			}
			endpoint = "index_zh_a_hist_min_em"
			timeKey = "时间"
			q.Set("period", r.Period)
			coverage = "Provider recent intraday bars only; one-minute data is current-session only. Requested range may not be fully covered."
			if !st.IsZero() {
				q.Set("start_date", st.Format(time.DateTime))
				q.Set("end_date", et.Format(time.DateTime))
			}
		} else {
			return Result{}, ErrInvalid
		}
	case "futures":
		r.Interval = choose(r.Interval, "realtime")
		if r.FuturesMarket == "" {
			r.FuturesMarket = "CF"
			if regexp.MustCompile(`^(IF|IH|IC|IM|TF|TS|TL|T)[0-9]+$`).MatchString(strings.ToUpper(r.Symbol)) {
				r.FuturesMarket = "FF"
			}
		}
		if r.Interval != "realtime" || !st.IsZero() || r.Period != "" || !validSymbol(r.Symbol) || !member(r.FuturesMarket, "CF", "FF") {
			return Result{}, ErrInvalid
		}
		endpoint = "futures_zh_spot"
		q.Set("subscribe_list", r.Symbol)
		q.Set("market", r.FuturesMarket)
		q.Set("adjust", "0")
		query["futures_market"] = r.FuturesMarket
		source = "https://finance.sina.com.cn/futuremarket/"
		coverage = "Current futures snapshot only; source time may omit date. No historical range or inferred trading date."
	default:
		return Result{}, ErrInvalid
	}
	query["symbol"] = r.Symbol
	query["interval"] = r.Interval
	if r.Period != "" {
		query["period"] = r.Period
	}
	rows, err := c.rows(ctx, endpoint, q)
	if err != nil {
		return Result{}, err
	}
	observed := len(rows)
	records := make([]Record, 0, len(rows))
	for _, row := range rows {
		var at time.Time
		if timeKey != "" {
			raw, ok := row[timeKey].(string)
			if !ok {
				return Result{}, ErrUnavailable
			}
			if endpoint == "spot_quotations_sge" {
				updated, ok := row["更新时间"].(string)
				if !ok {
					return Result{}, ErrUnavailable
				}
				update, e := parseTime(updated)
				if e != nil {
					return Result{}, ErrUnavailable
				}
				raw = update.Format(time.DateOnly) + " " + raw
			}
			at, err = parseTime(raw)
			if err != nil {
				return Result{}, ErrUnavailable
			}
			filterStart, filterEnd := st, et
			if dateOnly && !st.IsZero() {
				filterStart = time.Date(st.Year(), st.Month(), st.Day(), 0, 0, 0, 0, china)
				filterEnd = time.Date(et.Year(), et.Month(), et.Day(), 23, 59, 59, 999999999, china)
			}
			if !st.IsZero() && (at.Before(filterStart) || at.After(filterEnd)) {
				continue
			}
		}
		records = append(records, record(endpoint, row, at))
	}
	order := "provider_order"
	if timeKey != "" {
		order = "observed_at_ascending"
		sort.SliceStable(records, func(i, j int) bool { return records[i].ObservedAt < records[j].ObservedAt })
	}
	matched := len(records)
	if matched > n {
		if timeKey != "" {
			records = records[matched-n:]
		} else {
			records = records[:n]
		}
	}
	return result("akshare", endpoint, query, records, []Source{sourceRef("akshare", endpoint, source, endpoint)}, observed, matched, order, coverage), nil
}

func (c *Client) News(ctx context.Context, r NewsRequest) (Result, error) {
	n, err := limit(r.Limit, 5)
	if err != nil {
		return Result{}, err
	}
	q := url.Values{}
	endpoint := ""
	switch r.TopicType {
	case "stock":
		if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(r.Symbol) {
			return Result{}, ErrInvalid
		}
		endpoint = "stock_news_em"
		q.Set("symbol", r.Symbol)
	case "market":
		if r.Symbol != "" {
			return Result{}, ErrInvalid
		}
		endpoint = "stock_news_main_cx"
	default:
		return Result{}, ErrInvalid
	}
	rows, err := c.rows(ctx, endpoint, q)
	if err != nil {
		return Result{}, err
	}
	records := make([]Record, 0, len(rows))
	order := "provider_order"
	for _, row := range rows {
		var at time.Time
		if r.TopicType == "stock" {
			raw, ok := row["发布时间"].(string)
			if !ok {
				return Result{}, ErrUnavailable
			}
			at, err = parseTime(raw)
			if err != nil {
				return Result{}, ErrUnavailable
			}
		}
		records = append(records, record(endpoint, row, at))
	}
	if r.TopicType == "stock" {
		order = "observed_at_descending"
		sort.SliceStable(records, func(i, j int) bool { return records[i].ObservedAt > records[j].ObservedAt })
	}
	matched := len(records)
	if len(records) > n {
		records = records[:n]
	}
	sources := []Source{sourceRef("akshare", endpoint, "https://finance.eastmoney.com/", endpoint)}
	if r.TopicType == "market" {
		sources = []Source{sourceRef("akshare", endpoint, "https://finance.caixin.com/", endpoint)}
	}
	for _, row := range records {
		for _, key := range []string{"新闻链接", "url"} {
			if raw, ok := row.Data[key].(string); ok && publicSourceURL(raw) {
				sources = append(sources, sourceRef("akshare", endpoint, raw, "News item"))
				break
			}
		}
	}
	return result("akshare", endpoint, map[string]any{"topic_type": r.TopicType, "symbol": r.Symbol, "limit": n}, records, sources, len(rows), matched, order, "News is source-reported content; stock requests never fall back to unrelated market news."), nil
}

var periodNumbers = regexp.MustCompile(`[0-9]+`)

func periodKey(s string) string {
	parts := periodNumbers.FindAllString(s, -1)
	var b strings.Builder
	for _, p := range parts {
		v, _ := strconv.Atoi(p)
		fmt.Fprintf(&b, "%04d", v)
	}
	return b.String()
}
func (c *Client) Indicator(ctx context.Context, r IndicatorRequest) (Result, error) {
	n, err := limit(r.Limit, 6)
	if err != nil {
		return Result{}, err
	}
	endpoint, key := "", "月份"
	switch r.Indicator {
	case "china_cpi":
		endpoint = "macro_china_cpi"
	case "china_gdp":
		endpoint = "macro_china_gdp"
		key = "季度"
	case "china_pmi":
		endpoint = "macro_china_pmi"
	default:
		return Result{}, ErrInvalid
	}
	rows, err := c.rows(ctx, endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	records := make([]Record, 0, len(rows))
	for _, row := range rows {
		value, ok := row[key].(string)
		if !ok || len(periodNumbers.FindAllString(value, -1)) < 2 {
			return Result{}, ErrUnavailable
		}
		records = append(records, record(endpoint, row, time.Time{}))
	}
	sort.SliceStable(records, func(i, j int) bool {
		return periodKey(fmt.Sprint(records[i].Data[key])) > periodKey(fmt.Sprint(records[j].Data[key]))
	})
	matched := len(records)
	if matched > n {
		records = records[:n]
	}
	return result("akshare", endpoint, map[string]any{"indicator": r.Indicator, "limit": n}, records, []Source{sourceRef("akshare", endpoint, "https://data.eastmoney.com/cjsj/", endpoint)}, len(rows), matched, "reporting_period_descending", "Native indicator fields and units retained. Reporting period is not release timestamp; no fallback to monthly-rate or yearly-rate series."), nil
}

var quoteCategories = map[string][]string{"二次元": {"a", "b"}, "游戏": {"c"}, "文学": {"d"}, "原创": {"e"}, "网络": {"f"}, "其他": {"g"}, "影视": {"h"}, "诗词": {"i"}, "网易云": {"j"}, "哲学": {"k"}, "抖机灵": {"l"}}

func (c *Client) Quote(ctx context.Context, r QuoteRequest) (Result, error) {
	q := url.Values{"encode": {"json"}}
	if r.Type != "" {
		cats, ok := quoteCategories[r.Type]
		if !ok {
			return Result{}, ErrInvalid
		}
		q["c"] = cats
	}
	if !c.HitokotoEnabled() {
		return Result{}, ErrUnavailable
	}
	var body struct {
		ID     int64   `json:"id"`
		UUID   string  `json:"uuid"`
		Text   string  `json:"hitokoto"`
		Type   string  `json:"type"`
		From   string  `json:"from"`
		Author *string `json:"from_who"`
	}
	if err := c.get(ctx, c.hitokoto, q, &body); err != nil {
		return Result{}, err
	}
	if body.Text == "" || len(body.Text) > 16384 || body.UUID == "" || len(body.UUID) > 128 || len(body.From) > 4096 || (body.Author != nil && len(*body.Author) > 4096) || !member(body.Type, "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l") {
		return Result{}, ErrUnavailable
	}
	author := ""
	if body.Author != nil {
		author = *body.Author
	}
	data := map[string]any{"text": body.Text, "author": author, "category": body.Type, "source": body.From, "uuid": body.UUID, "id": body.ID}
	return result("hitokoto", "sentence", map[string]any{"type": r.Type}, []Record{record("sentence", data, time.Time{})}, []Source{sourceRef("hitokoto", "sentence", "https://hitokoto.cn/?uuid="+url.QueryEscape(body.UUID), body.From)}, 1, 1, "single_random_sentence", "Random quotation with provider attribution; read only. Delivery is a separate capability."), nil
}
func (c *Client) HotSearch(ctx context.Context, r HotSearchRequest) (Result, error) {
	n, err := limit(r.Limit, 10)
	if err != nil {
		return Result{}, err
	}
	r.Market = choose(r.Market, "A股")
	r.Window = choose(r.Window, "今日")
	if !member(r.Market, "全部", "A股", "港股", "美股") || !member(r.Window, "今日", "1小时") {
		return Result{}, ErrInvalid
	}
	date, err := time.Parse(time.DateOnly, r.Date)
	if err != nil {
		return Result{}, ErrInvalid
	}
	endpoint := "stock_hot_search_baidu"
	rows, err := c.rows(ctx, endpoint, url.Values{"symbol": {r.Market}, "date": {date.Format("20060102")}, "time": {r.Window}})
	if err != nil {
		return Result{}, err
	}
	records := make([]Record, 0, len(rows))
	for _, row := range rows {
		records = append(records, record(endpoint, row, time.Time{}))
	}
	matched := len(records)
	if matched > n {
		records = records[:n]
	}
	return result("akshare", endpoint, map[string]any{"market": r.Market, "date": r.Date, "window": r.Window, "limit": n}, records, []Source{sourceRef("akshare", endpoint, "https://gushitong.baidu.com/expressnews", "Baidu stock hot search")}, len(rows), matched, "provider_rank", "Baidu stock-search popularity only; date and window are explicit provider parameters, not general internet trends."), nil
}
func record(endpoint string, data map[string]any, at time.Time) Record {
	b, _ := json.Marshal(data)
	r := Record{ID: stable(endpoint, string(b)), Data: data}
	if !at.IsZero() {
		r.ObservedAt = at.Format(time.RFC3339)
	}
	return r
}
func stable(parts ...string) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func sourceRef(provider, endpoint, raw, title string) Source {
	return Source{ID: stable(provider, endpoint, raw), URL: raw, Title: title, Provider: provider, Endpoint: endpoint}
}
func result(provider, endpoint string, q map[string]any, records []Record, sources []Source, observed, matched int, order, coverage string) Result {
	// Bind references to the instrument/series, independent of presentation
	// limits and requested range. Native rows often omit the instrument code.
	identity := map[string]any{}
	for _, key := range []string{"asset_type", "symbol", "interval", "period", "futures_market", "indicator", "topic_type", "market", "date", "window"} {
		if value, ok := q[key]; ok {
			identity[key] = value
		}
	}
	binding, _ := json.Marshal(identity)
	for i := range records {
		records[i].ID = stable(provider, endpoint, string(binding), records[i].ID)
	}
	out := Result{Available: true, Provider: provider, Endpoint: endpoint, FetchedAt: time.Now().UTC(), Query: q, Records: records, Sources: sources, ObservedRecords: observed, MatchedRecords: matched, Truncated: matched > len(records), Order: order, Coverage: coverage, Untrusted: true}
	// Reserve a bounded part for source references, then retain complete native
	// rows using a linear byte budget. Native text/values are never clipped.
	sourceBytes := 0
	for i, source := range out.Sources {
		b, _ := json.Marshal(source)
		if sourceBytes+len(b)+1 > 64<<10 {
			out.Sources = out.Sources[:i]
			out.Truncated = true
			break
		}
		sourceBytes += len(b) + 1
	}
	metadata := out
	metadata.Records = []Record{}
	b, _ := json.Marshal(metadata)
	remaining := MaxResultBytes - len(b) - 16
	retained := make([]Record, 0, len(records))
	for offset := range records {
		i := offset
		if order == "observed_at_ascending" {
			i = len(records) - 1 - offset
		}
		b, _ := json.Marshal(records[i])
		if len(b)+1 > remaining {
			out.Truncated = true
			break
		}
		remaining -= len(b) + 1
		retained = append(retained, records[i])
	}
	if order == "observed_at_ascending" {
		for i, j := 0, len(retained)-1; i < j; i, j = i+1, j-1 {
			retained[i], retained[j] = retained[j], retained[i]
		}
	}
	out.Records = retained
	return out
}
func publicSourceURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(raw, "\r\n\x00") && len(raw) <= 4096
}
