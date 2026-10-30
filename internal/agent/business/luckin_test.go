package business

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

type fixtureCaller struct {
	calls []mcpclient.CallRequest
	quote string
	fail  bool
}

func (f *fixtureCaller) CallTool(_ context.Context, r mcpclient.CallRequest) (mcpclient.CallResult, error) {
	f.calls = append(f.calls, r)
	if f.fail {
		return mcpclient.CallResult{}, errors.New("network private-token")
	}
	data := f.quote
	if r.ToolName == "createOrder" {
		data = `{"orderId":"order-7"}`
	}
	return mcpclient.CallResult{Content: json.RawMessage(`[{"type":"text","text":` + quoteJSON(`{"code":200,"data":`+data+`}`) + `}]`)}, nil
}
func quoteJSON(s string) string { b, _ := json.Marshal(s); return string(b) }
func businessIdentity() Identity {
	return Identity{Scope: domain.Scope{TenantID: "t", AppID: "app", ChatID: "chat", Visibility: "group"}, ActorID: "actor", TaskID: "task", ExecutionKey: "execution"}
}
func luckinFixture(t *testing.T) (*LuckinClient, *fixtureCaller) {
	t.Helper()
	f := &fixtureCaller{quote: `{"discountPrice":12,"couponCodeList":[]}`}
	c, err := NewLuckinClient(LuckinConfig{ServerURL: "https://coffee.example/mcp", SigningKey: []byte(strings.Repeat("s", 32)), Credentials: ActorCredentials{{Scope: businessIdentity().Scope, ActorID: "actor", Token: "private-token"}}, Caller: f})
	if err != nil {
		t.Fatal(err)
	}
	return c, f
}
func TestLuckinPrepareDoesNotCreateAndCommitBindsQuoteIdentityAndPayload(t *testing.T) {
	c, f := luckinFixture(t)
	in := businessIdentity()
	p, err := c.Prepare(context.Background(), in, json.RawMessage(`{"deptId":1,"productList":[{"productId":2,"skuCode":"s","amount":1}]}`))
	if err != nil || len(f.calls) != 1 || f.calls[0].ToolName != "previewOrder" {
		t.Fatalf("prepare created order: %+v %v", f.calls, err)
	}
	other := in
	other.ActorID = "other"
	if _, err = c.Commit(context.Background(), other, p.Preparation); err == nil {
		t.Fatal("cross-account commit accepted")
	}
	if _, err = c.Commit(context.Background(), in, p.Preparation+"x"); err == nil {
		t.Fatal("tampered preparation accepted")
	}
	out, err := c.Commit(context.Background(), in, p.Preparation)
	if err != nil || !strings.Contains(string(out), "order-7") {
		t.Fatalf("commit: %s %v", out, err)
	}
	if len(f.calls) != 3 || f.calls[1].ToolName != "previewOrder" || f.calls[2].ToolName != "createOrder" {
		t.Fatalf("quote was not revalidated: %+v", f.calls)
	}
	if f.calls[2].Server.Headers["Authorization"] != "Bearer private-token" || f.calls[2].Server.Headers["Idempotency-Key"] == "" {
		t.Fatal("server account/idempotency missing")
	}
}
func TestLuckinChangedQuoteAndExpiredPreparationsCannotCreate(t *testing.T) {
	c, f := luckinFixture(t)
	in := businessIdentity()
	p, _ := c.Prepare(context.Background(), in, json.RawMessage(`{"deptId":1,"productList":[{"productId":2,"skuCode":"s","amount":1}]}`))
	f.quote = `{"discountPrice":15}`
	if _, err := c.Commit(context.Background(), in, p.Preparation); !errors.Is(err, ErrQuoteChanged) {
		t.Fatalf("changed price allowed: %v", err)
	}
	c.now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := c.Commit(context.Background(), in, p.Preparation); !errors.Is(err, ErrPreparationExpired) {
		t.Fatalf("expired quote: %v", err)
	}
	for _, call := range f.calls {
		if call.ToolName == "createOrder" {
			t.Fatal("invalid quote created order")
		}
	}
}

func TestLuckinReboundAccountCannotUseEarlierPreparation(t *testing.T) {
	c, f := luckinFixture(t)
	p, err := c.Prepare(context.Background(), businessIdentity(), json.RawMessage(`{"deptId":1,"productList":[{"productId":2,"skuCode":"s","amount":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	c.credentials = ActorCredentials{{Scope: businessIdentity().Scope, ActorID: "actor", Token: "replacement-account-token"}}
	if _, err := c.Commit(context.Background(), businessIdentity(), p.Preparation); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("rebound account reused quote: %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatal("rebound account reached provider")
	}
}

func TestLuckinOversizedPreparationIsRejectedBeforeApproval(t *testing.T) {
	c, f := luckinFixture(t)
	f.quote = `{"couponList":"` + strings.Repeat("c", 32<<10) + `"}`
	if _, err := c.Prepare(context.Background(), businessIdentity(), json.RawMessage(`{"deptId":1,"productList":[{"productId":2,"skuCode":"s","amount":1}]}`)); err == nil {
		t.Fatal("preparation exceeds canonical tool argument budget")
	}
}

func TestLuckinRejectsMissingOrConflictingActorCredentials(t *testing.T) {
	for _, creds := range []ActorCredentials{{}, {{Scope: businessIdentity().Scope, ActorID: "actor", Token: "one"}, {Scope: businessIdentity().Scope, ActorID: "actor", Token: "two"}}, {{Scope: businessIdentity().Scope, ActorID: "actor", Token: "bad\ntoken"}}} {
		if _, err := NewLuckinClient(LuckinConfig{SigningKey: []byte(strings.Repeat("s", 32)), Credentials: creds}); err == nil {
			t.Fatal("unavailable or ambiguous account configuration accepted")
		}
	}
}
func TestLuckinUnknownCreateResultIsNotRetriedAndErrorsAreSafe(t *testing.T) {
	c, f := luckinFixture(t)
	in := businessIdentity()
	p, _ := c.Prepare(context.Background(), in, json.RawMessage(`{"deptId":1,"productList":[{"productId":2,"skuCode":"s","amount":1}]}`))
	c.caller = &failCreateCaller{f}
	_, err := c.Commit(context.Background(), in, p.Preparation)
	if !errors.Is(err, domain.ErrEffectUnknown) || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("unsafe uncertain create error: %v", err)
	}
	creates := 0
	for _, call := range f.calls {
		if call.ToolName == "createOrder" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("create retries: %d", creates)
	}
}

type failCreateCaller struct{ f *fixtureCaller }

func (c *failCreateCaller) CallTool(ctx context.Context, r mcpclient.CallRequest) (mcpclient.CallResult, error) {
	if r.ToolName == "createOrder" {
		c.f.calls = append(c.f.calls, r)
		return mcpclient.CallResult{}, errors.New("timeout private-token")
	}
	return c.f.CallTool(ctx, r)
}

type missingReceiptCaller struct{ f *fixtureCaller }

func (c missingReceiptCaller) CallTool(ctx context.Context, r mcpclient.CallRequest) (mcpclient.CallResult, error) {
	if r.ToolName == "createOrder" {
		c.f.calls = append(c.f.calls, r)
		return mcpclient.CallResult{Content: json.RawMessage(`[{"type":"text","text":"{\"code\":200,\"data\":{}}"}]`)}, nil
	}
	return c.f.CallTool(ctx, r)
}
func TestLuckinCreateWithoutOrderReceiptRemainsUnknown(t *testing.T) {
	c, f := luckinFixture(t)
	in := businessIdentity()
	p, err := c.Prepare(context.Background(), in, json.RawMessage(`{"deptId":1,"productList":[{"productId":2,"skuCode":"s","amount":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	c.caller = missingReceiptCaller{f}
	if _, err = c.Commit(context.Background(), in, p.Preparation); !errors.Is(err, domain.ErrEffectUnknown) {
		t.Fatalf("missing order receipt claimed success: %v", err)
	}
}

func TestLuckinQueriesRejectModelCredentialsAndNeverExposeTransportErrors(t *testing.T) {
	c, f := luckinFixture(t)
	for _, raw := range []string{`{"token":"model-token"}`, `{"nested":{"open_id":"other"}}`} {
		if _, err := c.Query(context.Background(), businessIdentity(), "queryShopList", json.RawMessage(raw)); err == nil {
			t.Fatal("model credential accepted")
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("spoofed credentials reached provider")
	}
	f.fail = true
	_, err := c.Query(context.Background(), businessIdentity(), "queryShopList", json.RawMessage(`{"longitude":121,"latitude":31}`))
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("unsafe provider error: %v", err)
	}
}
