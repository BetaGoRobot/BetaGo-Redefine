package business

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/accounts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

type managedAccountFixture struct {
	status                string
	beginCalls            int
	ensureCalls           int
	bindingErr            error
	binding               accounts.Binding
	principal             accounts.Principal
	requestKey            string
	token                 string
	message               string
	invalidated           bool
	invalidateErr         error
	invalidateContextLive bool
	pinError              error
}

func (f *managedAccountFixture) Resolve(_ context.Context, p accounts.Principal) (string, error) {
	f.principal = p
	return f.token, nil
}
func (f *managedAccountFixture) Status(context.Context, accounts.Principal) (string, error) {
	if f.status != "" {
		return f.status, nil
	}
	return accounts.StatusUnbound, nil
}
func (f *managedAccountFixture) BeginBinding(_ context.Context, p accounts.Principal, key string) (accounts.Binding, error) {
	f.beginCalls++
	f.principal = p
	f.requestKey = key
	return f.binding, f.bindingErr
}
func (f *managedAccountFixture) EnsureBinding(_ context.Context, p accounts.Principal, key string) (accounts.Binding, error) {
	f.ensureCalls++
	f.principal = p
	f.requestKey = key
	return f.binding, f.bindingErr
}
func (f *managedAccountFixture) SetBindingMessage(_ context.Context, p accounts.Principal, id, message string) error {
	f.principal = p
	f.message = message
	return f.pinError
}
func (f *managedAccountFixture) Invalidate(ctx context.Context, p accounts.Principal, token string) error {
	f.principal = p
	f.invalidated = token == f.token
	_, bounded := ctx.Deadline()
	f.invalidateContextLive = ctx.Err() == nil && bounded
	return f.invalidateErr
}

type bindingSenderFixture struct {
	calls     int
	scope     domain.Scope
	actor, id string
	err       error
}

func (f *bindingSenderFixture) SendLuckinBinding(_ context.Context, s domain.Scope, a, id string) (string, error) {
	f.calls++
	f.scope = s
	f.actor = a
	f.id = id
	return "ephemeral-receipt", f.err
}
func managedIdentity() Identity {
	return Identity{Scope: domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}, ActorID: "alice", TaskID: "task", ExecutionKey: "execution"}
}

func TestManagedBindingUsesVerifiedActorAndPinsActualPrivateMessage(t *testing.T) {
	f := &managedAccountFixture{binding: accounts.Binding{ID: "opaque-binding", ExpiresAt: time.Now().Add(time.Minute)}}
	sender := &bindingSenderFixture{}
	m := &ManagedLuckinAccounts{Store: f, Sender: sender}
	in := managedIdentity()
	r, err := m.RequestBinding(context.Background(), in, false)
	if err != nil || r.MessageID != "ephemeral-receipt" || f.message != r.MessageID || sender.calls != 1 || sender.scope != in.Scope || sender.actor != in.ActorID || f.principal.ActorID != in.ActorID || f.requestKey == "" {
		t.Fatal("private binding identity or receipt was lost")
	}
	f.binding.MessageID = r.MessageID
	if _, err = m.RequestBinding(context.Background(), in, false); err != nil || sender.calls != 1 {
		t.Fatal("known private receipt must not send twice")
	}
	for _, bad := range []Identity{{}, {Scope: in.Scope, ActorID: in.ActorID, TaskID: in.TaskID}} {
		if _, err = m.RequestBinding(context.Background(), bad, false); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("missing server execution identity accepted")
		}
	}
}
func TestManagedBindingUncertainSendOrReceiptNeverLooksLikeSafeRetry(t *testing.T) {
	for _, pin := range []bool{false, true} {
		f := &managedAccountFixture{binding: accounts.Binding{ID: "binding"}}
		s := &bindingSenderFixture{}
		if pin {
			f.pinError = errors.New("unavailable")
		} else {
			s.err = errors.New("unavailable")
		}
		m := &ManagedLuckinAccounts{Store: f, Sender: s}
		if _, err := m.RequestBinding(context.Background(), managedIdentity(), false); !errors.Is(err, domain.ErrEffectUnknown) {
			t.Fatal("uncertain private delivery must not be replayed")
		}
	}
}
func TestManagedCredentialsResolveOnlyStoredOwnerAndInvalidateRejectedToken(t *testing.T) {
	in := managedIdentity()
	f := &managedAccountFixture{token: "fixture-personal-credential"}
	m := &ManagedLuckinAccounts{Store: f}
	cred, err := m.Resolve(context.Background(), in.Scope, in.ActorID)
	if err != nil || cred.ActorID != in.ActorID || cred.Token != f.token || f.principal.Scope != in.Scope || f.principal.ActorID != in.ActorID {
		t.Fatal("personal credential identity changed")
	}
	client, err := NewLuckinClient(LuckinConfig{ServerURL: "https://coffee.invalid", SigningKey: make([]byte, 32), Credentials: m, Caller: rejectCredentialCaller{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Query(context.Background(), in, "queryShopList", []byte(`{"longitude":116,"latitude":39}`))
	if !errors.Is(err, domain.ErrUnauthorized) || !f.invalidated {
		t.Fatal("upstream authorization rejection must invalidate this personal credential")
	}
}

type rejectCredentialCaller struct{}

func (rejectCredentialCaller) CallTool(context.Context, mcpclient.CallRequest) (mcpclient.CallResult, error) {
	return mcpclient.CallResult{}, mcpclient.ErrUnauthorized
}

type shapedRejectionCaller struct {
	err     error
	content string
}

func (f shapedRejectionCaller) CallTool(context.Context, mcpclient.CallRequest) (mcpclient.CallResult, error) {
	return mcpclient.CallResult{Content: json.RawMessage(f.content)}, f.err
}
func TestExplicitMCPAuthorizationRejectionInvalidatesButBusinessErrorsDoNot(t *testing.T) {
	for _, test := range []struct {
		caller   shapedRejectionCaller
		rejected bool
	}{
		{shapedRejectionCaller{err: &mcpclient.ToolError{Message: `{"code":401,"message":"invalid credential"}`}}, true},
		{shapedRejectionCaller{err: &mcpclient.ToolError{Message: "token expired"}}, true},
		{shapedRejectionCaller{content: `[{"type":"text","text":"{\"code\":403,\"data\":null}"}]`}, true},
		{shapedRejectionCaller{err: &mcpclient.ToolError{Message: "coupon token not found"}}, false},
	} {
		f := &managedAccountFixture{token: "personal-fixture"}
		m := &ManagedLuckinAccounts{Store: f}
		c, err := NewLuckinClient(LuckinConfig{SigningKey: make([]byte, 32), Credentials: m, Caller: test.caller})
		if err != nil {
			t.Fatal("fixture invalid")
		}
		_, err = c.Query(context.Background(), managedIdentity(), "queryShopList", []byte(`{}`))
		if errors.Is(err, domain.ErrUnauthorized) != test.rejected || f.invalidated != test.rejected {
			t.Fatal("explicit authentication rejection was misclassified")
		}
	}
}

type canceledCredentialCaller struct{ cancel context.CancelFunc }

func (f canceledCredentialCaller) CallTool(context.Context, mcpclient.CallRequest) (mcpclient.CallResult, error) {
	f.cancel()
	return mcpclient.CallResult{}, mcpclient.ErrUnauthorized
}
func TestRejectedCredentialPersistenceHasIndependentBoundedContextAndSurfacesFailure(t *testing.T) {
	in := managedIdentity()
	f := &managedAccountFixture{token: "personal-fixture", invalidateErr: errors.New("storage unavailable")}
	m := &ManagedLuckinAccounts{Store: f}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := NewLuckinClient(LuckinConfig{SigningKey: make([]byte, 32), Credentials: m, Caller: canceledCredentialCaller{cancel: cancel}})
	if err != nil {
		t.Fatal("fixture invalid")
	}
	_, err = c.Query(ctx, in, "queryShopList", []byte(`{}`))
	if !errors.Is(err, ErrProvider) || !f.invalidateContextLive {
		t.Fatal("known rejection must persist with its own bounded context or report unavailable storage")
	}
}

type echoCredentialCaller struct {
	token string
	key   bool
}

func (f echoCredentialCaller) CallTool(context.Context, mcpclient.CallRequest) (mcpclient.CallResult, error) {
	var data any = map[string]any{"nested": []any{map[string]string{"echo": "prefix" + f.token + "suffix"}}}
	if f.key {
		data = map[string]string{f.token: "value"}
	}
	body, _ := json.Marshal(map[string]any{"code": 200, "data": data})
	content, _ := json.Marshal([]map[string]string{{"type": "text", "text": string(body)}})
	return mcpclient.CallResult{Content: content}, nil
}
func TestProviderCannotEchoEscapedPersonalCredentialsIntoModelResults(t *testing.T) {
	in := managedIdentity()
	for _, token := range []string{"<personal&credential>", `personal"credential\value`, "personál-credential"} {
		for _, key := range []bool{false, true} {
			f := &managedAccountFixture{token: token}
			m := &ManagedLuckinAccounts{Store: f}
			c, err := NewLuckinClient(LuckinConfig{SigningKey: make([]byte, 32), Credentials: m, Caller: echoCredentialCaller{token: token, key: key}})
			if err != nil {
				t.Fatal("fixture invalid")
			}
			out, err := c.Query(context.Background(), in, "queryShopList", []byte(`{}`))
			if !errors.Is(err, ErrProvider) || len(out) > 0 {
				t.Fatal("provider credential echo escaped the response filter")
			}
		}
	}
}

func TestManagedBindingBoundDefaultDoesNotCreateOrSendForm(t *testing.T) {
	f := &managedAccountFixture{status: accounts.StatusBound}
	sender := &bindingSenderFixture{}
	m := &ManagedLuckinAccounts{Store: f, Sender: sender}
	receipt, err := m.RequestBinding(context.Background(), managedIdentity(), false)
	if err != nil || sender.calls != 0 || f.beginCalls != 0 || f.ensureCalls != 0 {
		t.Fatalf("bound ensure must not create or send a form: receipt=%+v err=%v sends=%d begins=%d ensures=%d", receipt, err, sender.calls, f.beginCalls, f.ensureCalls)
	}
	body, err := json.Marshal(receipt)
	var fields map[string]any
	if err != nil || json.Unmarshal(body, &fields) != nil || fields["outcome"] != "already_bound" || receipt.AccountStatus != accounts.StatusBound {
		t.Fatalf("bound ensure must return explicit already_bound receipt: %s", body)
	}
	if _, ok := fields["message_id"]; ok {
		t.Fatal("no-op invented a message receipt")
	}
	if _, ok := fields["expires_at"]; ok {
		t.Fatal("no-op invented a form expiry")
	}
	if _, ok := fields["delivery_location"]; ok {
		t.Fatal("no-op invented a form delivery location")
	}
}

func TestManagedBindingDefaultUsesEnsureAndReportsSentOrReused(t *testing.T) {
	for _, message := range []string{"", "existing-private-receipt"} {
		f := &managedAccountFixture{binding: accounts.Binding{ID: "binding", MessageID: message, ExpiresAt: time.Now().Add(time.Minute)}}
		sender := &bindingSenderFixture{}
		m := &ManagedLuckinAccounts{Store: f, Sender: sender}
		receipt, err := m.RequestBinding(context.Background(), managedIdentity(), false)
		body, _ := json.Marshal(receipt)
		var fields map[string]any
		_ = json.Unmarshal(body, &fields)
		want := "sent"
		if message != "" {
			want = "reused"
		}
		if err != nil || f.ensureCalls != 1 || f.beginCalls != 0 || fields["outcome"] != want {
			t.Fatalf("default binding must ensure and report %s: %s %v begins=%d ensures=%d", want, body, err, f.beginCalls, f.ensureCalls)
		}
		if fields["delivery_location"] != "current_chat" {
			t.Errorf("%s form must identify its actual location as the current chat: %s", want, body)
		}
		if message != "" && (sender.calls != 0 || receipt.MessageID != message) {
			t.Fatal("reusing a private form sent again")
		}
	}
}

func TestManagedBindingUnknownReservationDoesNotSendAgain(t *testing.T) {
	f := &managedAccountFixture{bindingErr: domain.ErrEffectUnknown}
	sender := &bindingSenderFixture{}
	m := &ManagedLuckinAccounts{Store: f, Sender: sender}
	if _, err := m.RequestBinding(context.Background(), managedIdentity(), false); !errors.Is(err, domain.ErrEffectUnknown) || sender.calls != 0 {
		t.Fatalf("unconfirmed reservation must remain unknown without send: %v sends=%d", err, sender.calls)
	}
}

func TestManagedBindingExplicitManagePreservesRebindAndRevokeForm(t *testing.T) {
	f := &managedAccountFixture{status: accounts.StatusBound, binding: accounts.Binding{ID: "management-binding", ExpiresAt: time.Now().Add(time.Minute)}}
	sender := &bindingSenderFixture{}
	m := &ManagedLuckinAccounts{Store: f, Sender: sender}
	receipt, err := m.RequestBinding(context.Background(), managedIdentity(), true)
	if err != nil || sender.calls != 1 || f.beginCalls != 1 || f.ensureCalls != 0 || receipt.MessageID != "ephemeral-receipt" || receipt.AccountStatus != accounts.StatusBound {
		t.Fatalf("explicit account management must still send the private form: %+v %v sends=%d begins=%d ensures=%d", receipt, err, sender.calls, f.beginCalls, f.ensureCalls)
	}
}
