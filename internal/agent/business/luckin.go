package business

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

var ErrQuoteChanged = errors.New("luckin quote changed; prepare a fresh order and obtain approval again")
var ErrPreparationExpired = errors.New("luckin preparation expired; prepare a fresh order and obtain approval again")

type Identity struct {
	Scope                         domain.Scope
	ActorID, TaskID, ExecutionKey string
}

func (i Identity) valid() bool { return i.Scope.Validate() == nil && i.ActorID != "" && i.TaskID != "" }

type Credential struct{ ActorID, Token string }
type CredentialResolver interface {
	Resolve(context.Context, domain.Scope, string) (Credential, error)
}
type CredentialResolverFunc func(context.Context, domain.Scope, string) (Credential, error)

func (f CredentialResolverFunc) Resolve(ctx context.Context, s domain.Scope, a string) (Credential, error) {
	return f(ctx, s, a)
}

type ActorCredential struct {
	Scope          domain.Scope
	ActorID, Token string
}
type ActorCredentials []ActorCredential

func (c ActorCredentials) Resolve(_ context.Context, s domain.Scope, actor string) (Credential, error) {
	for _, x := range c {
		if x.Scope == s && x.ActorID == actor && x.Token != "" {
			return Credential{ActorID: actor, Token: x.Token}, nil
		}
	}
	return Credential{}, domain.ErrUnauthorized
}

type ToolCaller interface {
	CallTool(context.Context, mcpclient.CallRequest) (mcpclient.CallResult, error)
}
type Geocoder interface {
	Locate(context.Context, string) (GeoPoint, error)
}
type GeoPoint struct {
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}
type LuckinConfig struct {
	ServerURL, BindingURL string
	SigningKey            []byte
	Credentials           CredentialResolver
	Caller                ToolCaller
	Geocoder              Geocoder
	HTTPClient            *http.Client
	Timeout               time.Duration
	// Now is a server-owned clock, never a tool argument.
	Now func() time.Time
}
type LuckinClient struct {
	url, binding string
	key          []byte
	credentials  CredentialResolver
	caller       ToolCaller
	geocoder     Geocoder
	timeout      time.Duration
	now          func() time.Time
}

func NewLuckinClient(cfg LuckinConfig) (*LuckinClient, error) {
	if cfg.ServerURL == "" {
		cfg.ServerURL = "https://gwmcp.lkcoffee.com/order/user/mcp"
	}
	if cfg.BindingURL == "" {
		cfg.BindingURL = "https://open.lkcoffee.com"
	}
	if _, err := endpoint(cfg.ServerURL); err != nil {
		return nil, domain.ErrInvalid
	}
	if _, err := endpoint(cfg.BindingURL); err != nil {
		return nil, domain.ErrInvalid
	}
	if cfg.Credentials == nil || len(cfg.SigningKey) < 32 {
		return nil, domain.ErrInvalid
	}
	if rows, ok := cfg.Credentials.(ActorCredentials); ok {
		if len(rows) == 0 {
			return nil, domain.ErrInvalid
		}
		seen := map[string]bool{}
		for _, row := range rows {
			key := domain.StableID(row.Scope.Key(), row.ActorID)
			if row.Scope.Validate() != nil || row.ActorID == "" || row.Token == "" || strings.ContainsAny(row.ActorID+row.Token, "\r\n") || seen[key] {
				return nil, domain.ErrInvalid
			}
			seen[key] = true
		}
		cfg.Credentials = append(ActorCredentials(nil), rows...)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	hc, err := httpClient(cfg.HTTPClient, cfg.Timeout)
	if err != nil {
		return nil, domain.ErrInvalid
	}
	hc.Transport = &limitTransport{base: hc.Transport, limit: 1 << 20}
	caller := cfg.Caller
	if caller == nil {
		caller = mcpclient.New(mcpclient.ClientOptions{HTTPClient: hc})
	}
	geo := cfg.Geocoder
	if geo == nil {
		geo = NewNominatimGeocoder(hc)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &LuckinClient{url: cfg.ServerURL, binding: cfg.BindingURL, key: append([]byte(nil), cfg.SigningKey...), credentials: cfg.Credentials, caller: caller, geocoder: geo, timeout: cfg.Timeout, now: now}, nil
}
func (c *LuckinClient) Fingerprint() string {
	return domain.StableID("luckin-v1", c.url, c.binding, hex.EncodeToString(c.key))
}
func (c *LuckinClient) BindingGuide() any {
	return map[string]any{"provider": "luckin", "binding_url": c.binding, "binding_tool": "luckin_account_bind", "instructions": "请求个人账号绑定卡片，在仅自己可见的表单中提交瑞幸开放平台的个人 Token。服务会独立加密保存，支持重新绑定和解绑；上游拒绝凭证后需要本人重新登录授权。不要把 Token 发到群聊或工具参数中。", "account_scope": "current_actor_only"}
}
func (c *LuckinClient) credential(ctx context.Context, in Identity) (Credential, error) {
	if !in.valid() {
		return Credential{}, domain.ErrUnauthorized
	}
	cred, err := c.credentials.Resolve(ctx, in.Scope, in.ActorID)
	if err != nil || cred.ActorID != in.ActorID || cred.Token == "" || strings.ContainsAny(cred.Token, "\r\n") {
		return Credential{}, domain.ErrUnauthorized
	}
	return cred, nil
}
func (c *LuckinClient) Query(ctx context.Context, in Identity, tool string, args json.RawMessage) (json.RawMessage, error) {
	allowed := map[string]bool{"queryShopList": true, "searchProductForMcp": true, "queryProductDetailInfo": true, "switchProduct": true, "previewOrder": true, "queryOrderDetailInfo": true}
	if !allowed[tool] {
		return nil, domain.ErrInvalid
	}
	canonical, err := domain.CanonicalArguments(string(args))
	if err != nil {
		return nil, err
	}
	if forbiddenKeys([]byte(canonical)) {
		return nil, domain.ErrUnauthorized
	}
	cred, err := c.credential(ctx, in)
	if err != nil {
		return nil, err
	}
	return c.call(ctx, in, cred, tool, json.RawMessage(canonical))
}
func forbiddenKeys(raw []byte) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(x any) bool {
		switch a := x.(type) {
		case map[string]any:
			for k, v := range a {
				n := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(k))
				switch n {
				case "token", "authorization", "cookie", "userid", "openid", "accountid", "credential", "credentials":
					return true
				}
				if visit(v) {
					return true
				}
			}
		case []any:
			for _, v := range a {
				if visit(v) {
					return true
				}
			}
		}
		return false
	}
	return visit(v)
}
func (c *LuckinClient) call(ctx context.Context, in Identity, cred Credential, tool string, args json.RawMessage) (json.RawMessage, error) {
	headers := map[string]string{"Authorization": "Bearer " + cred.Token, "X-Agent-Actor-ID": in.ActorID, "X-Agent-Task-ID": in.TaskID, "X-Agent-Tenant-ID": in.Scope.TenantID, "X-Agent-App-ID": in.Scope.AppID, "X-Agent-Chat-ID": in.Scope.ChatID}
	if tool == "createOrder" {
		if in.ExecutionKey == "" {
			return nil, domain.ErrInvalid
		}
		headers["Idempotency-Key"] = domain.StableUUID(in.Scope.Key(), in.TaskID, in.ExecutionKey)
	}
	result, err := c.caller.CallTool(ctx, mcpclient.CallRequest{Server: mcpclient.ServerConfig{Name: "my-coffee", URL: c.url, Headers: headers, Timeout: c.timeout}, ToolName: tool, Arguments: args})
	if err != nil {
		unauthorized := rejectedCredentialError(err)
		var invalidationErr error
		if unauthorized {
			invalidationErr = c.invalidateCredential(ctx, in, cred)
		}
		if tool == "createOrder" {
			return nil, domain.ErrEffectUnknown
		}
		if invalidationErr != nil {
			return nil, ErrProvider
		}
		if unauthorized {
			return nil, domain.ErrUnauthorized
		}
		return nil, ErrProvider
	}
	if rejectedCredentialContent(result.Content) {
		invalidationErr := c.invalidateCredential(ctx, in, cred)
		if tool == "createOrder" {
			return nil, domain.ErrEffectUnknown
		}
		if invalidationErr != nil {
			return nil, ErrProvider
		}
		return nil, domain.ErrUnauthorized
	}
	data, err := extractData(result.Content)
	if err != nil || containsCredential(data, cred.Token) {
		if tool == "createOrder" {
			return nil, domain.ErrEffectUnknown
		}
		return nil, ErrProvider
	}
	if tool == "createOrder" && !hasOrderReceipt(data) {
		return nil, domain.ErrEffectUnknown
	}
	return data, nil
}

func (c *LuckinClient) invalidateCredential(ctx context.Context, in Identity, cred Credential) error {
	if invalidator, ok := c.credentials.(interface {
		Invalidate(context.Context, domain.Scope, string, string) error
	}); ok {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		return invalidator.Invalidate(persist, in.Scope, in.ActorID, cred.Token)
	}
	return nil
}
func rejectedCredentialError(err error) bool {
	if errors.Is(err, mcpclient.ErrUnauthorized) {
		return true
	}
	var remote *mcpclient.ToolError
	return errors.As(err, &remote) && rejectedCredentialBody(remote.Message)
}
func rejectedCredentialBody(text string) bool {
	var body struct {
		Code int `json:"code"`
	}
	if json.Unmarshal([]byte(text), &body) == nil && (body.Code == 401 || body.Code == 403) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "unauthorized", "forbidden", "invalid token", "invalid_token", "token expired", "token_expired":
		return true
	}
	return false
}
func rejectedCredentialContent(raw json.RawMessage) bool {
	var blocks []struct{ Type, Text string }
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "text" && rejectedCredentialBody(block.Text) {
			return true
		}
	}
	return false
}

// Inspect decoded values and keys: JSON escaping must not let an upstream echo
// of a credential reach model-visible tool results.
func containsCredential(raw json.RawMessage, token string) bool {
	if token == "" {
		return true
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return true
	}
	var contains func(any) bool
	contains = func(v any) bool {
		switch x := v.(type) {
		case string:
			return strings.Contains(x, token)
		case []any:
			for _, item := range x {
				if contains(item) {
					return true
				}
			}
		case map[string]any:
			for key, value := range x {
				if strings.Contains(key, token) || contains(value) {
					return true
				}
			}
		}
		return false
	}
	return contains(v)
}
func hasOrderReceipt(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for _, name := range []string{"orderIdStr", "orderNo", "orderId"} {
		v, ok := fields[name]
		if !ok {
			continue
		}
		var text string
		if json.Unmarshal(v, &text) == nil && strings.TrimSpace(text) != "" {
			return true
		}
		var number json.Number
		if json.Unmarshal(v, &number) == nil {
			n, err := number.Int64()
			if err == nil && n > 0 {
				return true
			}
		}
	}
	return false
}
func extractData(content json.RawMessage) (json.RawMessage, error) {
	if len(content) > 1<<20 {
		return nil, ErrProvider
	}
	var blocks []struct{ Type, Text string }
	if json.Unmarshal(content, &blocks) != nil {
		return nil, ErrProvider
	}
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		var body map[string]json.RawMessage
		if json.Unmarshal([]byte(block.Text), &body) != nil {
			continue
		}
		if code, ok := body["code"]; ok {
			var n int
			if json.Unmarshal(code, &n) != nil || (n != 0 && n != 200) {
				return nil, ErrProvider
			}
		}
		data, ok := body["data"]
		if !ok {
			data = json.RawMessage(block.Text)
		}
		var normalized any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if dec.Decode(&normalized) != nil {
			return nil, ErrProvider
		}
		switch normalized.(type) {
		case map[string]any, []any:
		default:
			return nil, ErrProvider
		}
		out, err := json.Marshal(normalized)
		return out, err
	}
	return nil, ErrProvider
}

type orderProduct struct {
	Amount    int    `json:"amount"`
	ProductID int64  `json:"productId"`
	SKUCode   string `json:"skuCode"`
}
type orderInput struct {
	DeptID         int64          `json:"deptId"`
	ProductList    []orderProduct `json:"productList"`
	CouponCodeList []string       `json:"couponCodeList"`
	Longitude      *float64       `json:"longitude,omitempty"`
	Latitude       *float64       `json:"latitude,omitempty"`
}

func orderPayload(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > 32<<10 || forbiddenKeys(raw) {
		return nil, domain.ErrInvalid
	}
	var a orderInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&a) != nil || a.DeptID <= 0 || len(a.ProductList) < 1 || len(a.ProductList) > 20 || len(a.CouponCodeList) > 20 {
		return nil, domain.ErrInvalid
	}
	for _, p := range a.ProductList {
		if p.ProductID <= 0 || p.Amount < 1 || p.Amount > 50 || len(p.SKUCode) < 1 || len(p.SKUCode) > 256 {
			return nil, domain.ErrInvalid
		}
	}
	for _, c := range a.CouponCodeList {
		if len(c) < 1 || len(c) > 256 {
			return nil, domain.ErrInvalid
		}
	}
	if (a.Latitude == nil) != (a.Longitude == nil) || (a.Latitude != nil && (*a.Latitude < -90 || *a.Latitude > 90 || *a.Longitude < -180 || *a.Longitude > 180)) {
		return nil, domain.ErrInvalid
	}
	if a.CouponCodeList == nil {
		a.CouponCodeList = []string{}
	}
	return json.Marshal(a)
}

type Preparation struct {
	Preparation string          `json:"preparation"`
	Payload     json.RawMessage `json:"payload"`
	Quote       json.RawMessage `json:"quote"`
	ExpiresAt   time.Time       `json:"expires_at"`
	PayloadHash string          `json:"payload_hash"`
}
type frozenQuote struct {
	ScopeKey, ActorID, TaskID string
	CredentialFingerprint     string
	ExpiresAt                 int64
	Payload, Quote            json.RawMessage
}

func (c *LuckinClient) Prepare(ctx context.Context, in Identity, args json.RawMessage) (Preparation, error) {
	payload, err := orderPayload(args)
	if err != nil {
		return Preparation{}, err
	}
	cred, err := c.credential(ctx, in)
	if err != nil {
		return Preparation{}, err
	}
	quote, err := c.call(ctx, in, cred, "previewOrder", payload)
	if err != nil {
		return Preparation{}, err
	}
	expires := c.now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	f := frozenQuote{ScopeKey: in.Scope.Key(), ActorID: in.ActorID, TaskID: in.TaskID, CredentialFingerprint: domain.StableID(cred.Token), ExpiresAt: expires.Unix(), Payload: payload, Quote: quote}
	body, _ := json.Marshal(f)
	if len(body) > 24<<10 {
		return Preparation{}, ErrProvider
	}
	mac := hmac.New(sha256.New, c.key)
	mac.Write(body)
	token := base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(token) > 32768 {
		return Preparation{}, ErrProvider
	}
	return Preparation{Preparation: token, Payload: payload, Quote: quote, ExpiresAt: expires, PayloadHash: domain.StableID(string(payload), string(quote))}, nil
}
func (c *LuckinClient) Commit(ctx context.Context, in Identity, token string) (json.RawMessage, error) {
	if !in.valid() || len(token) > 32768 {
		return nil, domain.ErrUnauthorized
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, domain.ErrInvalid
	}
	body, e := base64.RawURLEncoding.DecodeString(parts[0])
	sig, e2 := base64.RawURLEncoding.DecodeString(parts[1])
	mac := hmac.New(sha256.New, c.key)
	mac.Write(body)
	if e != nil || e2 != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, domain.ErrUnauthorized
	}
	var f frozenQuote
	if json.Unmarshal(body, &f) != nil || f.ScopeKey != in.Scope.Key() || f.ActorID != in.ActorID || f.TaskID != in.TaskID {
		return nil, domain.ErrUnauthorized
	}
	if c.now().Unix() >= f.ExpiresAt {
		return nil, ErrPreparationExpired
	}
	cred, err := c.credential(ctx, in)
	if err != nil {
		return nil, err
	}
	if domain.StableID(cred.Token) != f.CredentialFingerprint {
		return nil, domain.ErrUnauthorized
	}
	quote, err := c.call(ctx, in, cred, "previewOrder", f.Payload)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(quote, f.Quote) {
		return nil, ErrQuoteChanged
	}
	return c.call(ctx, in, cred, "createOrder", f.Payload)
}
func (c *LuckinClient) SearchShops(ctx context.Context, in Identity, location, name string, point *GeoPoint) (json.RawMessage, error) {
	if point == nil {
		if strings.TrimSpace(location) == "" || len(location) > 1024 {
			return nil, domain.ErrInvalid
		}
		p, err := c.geocoder.Locate(ctx, location)
		if err != nil {
			return nil, ErrProvider
		}
		point = &p
	}
	if point.Latitude < -90 || point.Latitude > 90 || point.Longitude < -180 || point.Longitude > 180 {
		return nil, domain.ErrInvalid
	}
	args := map[string]any{"longitude": point.Longitude, "latitude": point.Latitude}
	if name != "" {
		args["deptName"] = name
	}
	b, _ := json.Marshal(args)
	return c.Query(ctx, in, "queryShopList", b)
}

type limitTransport struct {
	base  http.RoundTripper
	limit int64
}

func (t *limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	r, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if r.ContentLength > t.limit {
		r.Body.Close()
		return nil, ErrProvider
	}
	r.Body = &limitedBody{ReadCloser: r.Body, remaining: t.limit}
	return r, nil
}
