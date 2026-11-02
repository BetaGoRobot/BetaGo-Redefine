package atoms_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

type businessMessenger struct {
	sent, typ, body, uuid string
	calls                 int
}

type businessLuckinCaller struct {
	quote     int
	creates   int
	uncertain bool
}

func (c *businessLuckinCaller) CallTool(_ context.Context, r mcpclient.CallRequest) (mcpclient.CallResult, error) {
	if r.ToolName == "createOrder" {
		c.creates++
		if c.uncertain {
			return mcpclient.CallResult{}, errors.New("timeout")
		}
	}
	text, _ := json.Marshal(map[string]any{"code": 200, "data": map[string]any{"price": c.quote, "orderId": "actual-order"}})
	content, _ := json.Marshal([]any{map[string]any{"type": "text", "text": string(text)}})
	return mcpclient.CallResult{Content: content}, nil
}
func TestLuckinCreateAtomDefiniteRejectionsFinishWithoutUncertainLedger(t *testing.T) {
	for _, mode := range []string{"price_changed", "expired", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			caller := &businessLuckinCaller{quote: 12}
			scope := domain.Scope{TenantID: "t", AppID: "app", ChatID: "chat", Visibility: "group"}
			client, err := business.NewLuckinClient(business.LuckinConfig{SigningKey: []byte(strings.Repeat("s", 32)), Credentials: business.ActorCredentials{{Scope: scope, ActorID: "actor", Token: "private-token"}}, Caller: caller, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			r := businessRegistry(t, atoms.BusinessOptions{Luckin: client})
			in := atoms.Invocation{Task: domain.Task{ID: "task", Scope: scope}, ActorID: "actor", ExecutionKey: "exec"}
			prepare := namedTool(t, r, "luckin_order_prepare_create")
			raw, err := prepare.Run(context.Background(), in, `{"deptId":1,"productList":[{"productId":2,"skuCode":"sku","amount":1}]}`)
			if err != nil {
				t.Fatal(err)
			}
			var prepared business.Preparation
			if json.Unmarshal([]byte(raw), &prepared) != nil {
				t.Fatal("invalid prepared output")
			}
			switch mode {
			case "price_changed":
				caller.quote = 15
			case "expired":
				now = now.Add(time.Hour)
			case "uncertain":
				caller.uncertain = true
			}
			args, _ := json.Marshal(map[string]string{"preparation": prepared.Preparation})
			out, err := namedTool(t, r, "luckin_order_create").Run(context.Background(), in, string(args))
			if mode == "uncertain" {
				if !errors.Is(err, domain.ErrEffectUnknown) || caller.creates != 1 {
					t.Fatalf("uncertainty was concealed: %s %v %d", out, err, caller.creates)
				}
				return
			}
			if err != nil || caller.creates != 0 || !strings.Contains(out, `"created":false`) || !strings.Contains(out, `"status":"rejected"`) {
				t.Fatalf("safe preflight rejection became unknown: %s %v creates=%d", out, err, caller.creates)
			}
		})
	}
}

func (m *businessMessenger) Send(_ context.Context, _ domain.Scope, parent, typ, body, uuid string) (string, error) {
	m.calls++
	m.sent = parent
	m.typ = typ
	m.body = body
	m.uuid = uuid
	return "sent", nil
}
func businessRegistry(t *testing.T, o atoms.BusinessOptions) *atoms.Registry {
	t.Helper()
	defs, err := atoms.BusinessDefinitions(o)
	if err != nil {
		t.Fatal(err)
	}
	r, err := atoms.New(atoms.Options{Extensions: defs})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestBusinessToolsAreOnlyRegisteredWhenConfigured(t *testing.T) {
	defs, err := atoms.BusinessDefinitions(atoms.BusinessOptions{})
	if err != nil || len(defs) != 0 {
		t.Fatalf("unconfigured fake tools: %v %v", defs, err)
	}
	c, err := business.NewLuckinClient(business.LuckinConfig{Credentials: business.ActorCredentials{{Scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}, ActorID: "actor", Token: "private-token"}}, SigningKey: []byte(strings.Repeat("x", 32))})
	if err != nil {
		t.Fatal(err)
	}
	r := businessRegistry(t, atoms.BusinessOptions{Luckin: c})
	for _, name := range []string{"luckin_bind_token_guide", "luckin_shop_search", "luckin_product_search", "luckin_product_detail", "luckin_product_switch", "luckin_order_preview", "luckin_order_detail", "luckin_order_prepare_create", "luckin_order_create"} {
		d := namedTool(t, r, name)
		if d.SideEffect != (name == "luckin_order_create") || d.AutoExecute {
			t.Fatalf("unsafe classification %s", name)
		}
	}
	d := namedTool(t, r, "luckin_product_search")
	for _, raw := range []string{`{"deptId":1,"query":"coffee","token":"bad"}`, `{"deptId":1,"query":"coffee","user_id":"other"}`} {
		if _, err := d.Validate(raw); err == nil {
			t.Fatal("tool accepted identity or credential override")
		}
	}
}

type bindingRequestFixture struct {
	identity business.Identity
	calls    int
	manage   bool
}

func (f *bindingRequestFixture) RequestBinding(_ context.Context, in business.Identity, manage bool) (business.BindingReceipt, error) {
	f.identity = in
	f.manage = manage
	f.calls++
	return business.BindingReceipt{MessageID: "private-receipt", Visibility: "current_actor_only"}, nil
}
func TestPrivateBindingIsAnExplicitEffectAndGuideStillDoesNotSend(t *testing.T) {
	c, err := business.NewLuckinClient(business.LuckinConfig{SigningKey: []byte(strings.Repeat("s", 32)), Credentials: business.CredentialResolverFunc(func(context.Context, domain.Scope, string) (business.Credential, error) {
		return business.Credential{}, domain.ErrUnauthorized
	})})
	if err != nil {
		t.Fatal(err)
	}
	f := &bindingRequestFixture{}
	r := businessRegistry(t, atoms.BusinessOptions{Luckin: c, LuckinBinding: f})
	in := atoms.Invocation{ActorID: "actor", ExecutionKey: "exec", Task: domain.Task{ID: "task", Scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}}}
	guide := namedTool(t, r, "luckin_bind_token_guide")
	if guide.SideEffect {
		t.Fatal("reading instructions silently sends a form")
	}
	if _, err = guide.Run(context.Background(), in, `{}`); err != nil || f.calls != 0 {
		t.Fatal("guide performed a private delivery")
	}
	d := namedTool(t, r, "luckin_account_bind")
	if !d.SideEffect || !d.AutoExecute || !d.PerActorTurn {
		t.Fatal("private form must be recorded as ordinary delivery effect")
	}
	for _, raw := range []string{`{"token":"credential"}`, `{"actor_id":"other"}`, `{"chat_id":"other"}`} {
		if _, err = d.Validate(raw); err == nil {
			t.Fatal("binding tool accepted credential or identity overrides")
		}
	}
	for _, raw := range []string{`{"manage":true}`, `{"manage":false}`} {
		if _, err = d.Validate(raw); err != nil {
			t.Fatalf("explicit management boolean must be accepted: %s %v", raw, err)
		}
	}
	if _, err = d.Validate(`{"manage":"true"}`); err == nil {
		t.Fatal("management intent must be boolean")
	}
	out, err := d.Run(context.Background(), in, `{}`)
	if err != nil || f.calls != 1 || f.identity.ActorID != in.ActorID || f.identity.Scope != in.Task.Scope || f.identity.ExecutionKey != in.ExecutionKey || !strings.Contains(out, "private-receipt") {
		t.Fatal("private binding did not preserve verified execution identity")
	}
	if f.manage {
		t.Fatal("default binding silently requested account management")
	}
	if _, err = d.Run(context.Background(), in, `{"manage":true}`); err != nil || !f.manage || f.calls != 2 {
		t.Fatal("explicit account management intent was lost")
	}
	if _, err = d.Run(context.Background(), in, `{"manage":false}`); err != nil || f.manage || f.calls != 3 {
		t.Fatal("explicit default ensure intent was lost")
	}
}
func TestMusicSearchDoesNotSendAndSelectedIDCardDoesNotResearch(t *testing.T) {
	paths := []string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		r.ParseForm()
		switch r.URL.Path {
		case "/cloudsearch":
			w.Write([]byte(`{"code":200,"result":{"songs":[{"id":123,"name":"selected"}],"songCount":1}}`))
		case "/song/detail":
			if r.Form.Get("ids") != "123" {
				t.Error("selected ID changed")
			}
			w.Write([]byte(`{"code":200,"songs":[{"id":123,"name":"selected"}]}`))
		default:
			t.Errorf("unexpected operation %s", r.URL.Path)
		}
	}))
	defer s.Close()
	music, _ := business.NewMusicClient(business.MusicConfig{BaseURL: s.URL})
	sender := &businessMessenger{}
	presenter := &musicPresenterFixture{}
	r := businessRegistry(t, atoms.BusinessOptions{Music: music, Messenger: sender, MusicCards: presenter})
	in := atoms.Invocation{Task: domain.Task{ID: "task", Scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}, Root: domain.SourceRef{MessageID: "parent"}}, ActorID: "actor", ExecutionKey: "key"}
	d := namedTool(t, r, "music_search")
	raw, err := d.Validate(`{"keywords":"q","type":"song"}`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Run(context.Background(), in, raw)
	if err != nil || sender.calls != 0 || !strings.Contains(out, `"id":123`) {
		t.Fatalf("search side effect: %s %v", out, err)
	}
	d = namedTool(t, r, "music_send_card")
	if !d.SideEffect || !d.AutoExecute {
		t.Fatal("music send lost effect ledger or ordinary authorization")
	}
	raw, err = d.Validate(`{"type":"song","id":123}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Run(context.Background(), in, raw)
	if err != nil || presenter.calls != 1 || presenter.parent != "parent" || presenter.request.Kind != "song" || presenter.request.ID != 123 || presenter.identity.ExecutionKey != "key" {
		t.Fatal("selected music card did not hand exact stable identity to the rich presenter")
	}
	if len(paths) != 1 || paths[0] != "/cloudsearch" {
		t.Fatalf("presentation atom performed an unstable new search: %v", paths)
	}
}
