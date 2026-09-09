package luckinaction

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

type accountTokens struct {
	lookups []luckin.CredentialLookup
	values  map[string]string
}

func (s *accountTokens) FindToken(_ context.Context, lookup luckin.CredentialLookup) (luckin.Credential, error) {
	s.lookups = append(s.lookups, lookup)
	token, ok := s.values[lookup.Scope.ID]
	if !ok {
		return luckin.Credential{}, luckin.ErrCredentialNotFound
	}
	return luckin.Credential{Token: token, Scope: lookup.Scope}, nil
}

type accountCaller struct{ requests []mcpclient.CallRequest }

func (c *accountCaller) CallTool(_ context.Context, req mcpclient.CallRequest) (mcpclient.CallResult, error) {
	c.requests = append(c.requests, req)
	return mcpclient.CallResult{Content: json.RawMessage(`{"data":{"discountPrice":10}}`)}, nil
}

func TestCheckoutUsesSelectedUsersPersonalAccount(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		initiator string
		missing   bool
	}{
		{name: "self", mode: "self_service", initiator: "ou_initiator"},
		{name: "self_missing_binding", mode: "self_service", initiator: "ou_initiator", missing: true},
		{name: "stored_self_mode", initiator: "ou_initiator"},
		{name: "unified", mode: "initiator_unified", initiator: "ou_user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := []luckin.CartItem{
				{LineID: "mine", AddedByOpenID: "ou_user", ProductID: 1, SkuCode: "sku", Amount: 1},
				{LineID: "other", AddedByOpenID: "ou_other", ProductID: 2, SkuCode: "sku2", Amount: 1},
			}
			session := newMemStoreWithSession("checkout", luckin.OrderSession{
				InitiatorOpenID: tc.initiator, ChatID: "oc_chat", CheckoutMode: luckin.CheckoutModeSelfService,
				Shop: luckin.ShopSelection{DeptID: 1}, Cart: luckin.Cart{Items: items},
			})
			tokens := &accountTokens{values: map[string]string{"ou_initiator": "initiator-token"}}
			if !tc.missing {
				tokens.values["ou_user"] = "own-token"
			}
			caller := &accountCaller{}
			pending := &memPendingStore{}
			var binding []string
			effects := checkoutEffects{
				patch: func(context.Context, string, any) error { return nil },
				reply: func(context.Context, string, any, string, bool) error { return nil },
				bind:  func(_ context.Context, req luckin.CredentialRequest) { binding = append(binding, req.OpenID) },
				lock:  func(_ context.Context, _ string, fn func() error) error { return fn() },
			}
			action := testActionContextWithMsgAndForm(nil, map[string]any{cardactionproto.LuckinCheckoutModeField: tc.mode}, "checkout")
			task, err := checkoutTaskWithEffects(session, luckin.NewDraftService(caller, luckin.ServerURL), pending, tokens, nil, effects)(context.Background(), action)
			if err != nil {
				t.Fatal(err)
			}
			task(context.Background())
			if len(tokens.lookups) != 1 || tokens.lookups[0].Scope != (luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "ou_user"}) {
				t.Fatalf("credential lookup = %+v, want only the checkout user's personal account", tokens.lookups)
			}
			if tc.missing {
				if len(caller.requests) != 0 || pending.order.ID != "" || !reflect.DeepEqual(binding, []string{"ou_user"}) {
					t.Fatal("missing own binding must only guide the user, without preview or pending creation")
				}
				if !reflect.DeepEqual(session.sessions["checkout"].Cart.Items, items) {
					t.Fatal("missing credentials removed cart items")
				}
				return
			}
			wantCalls := 1
			if tc.mode == "initiator_unified" {
				wantCalls = 2
			}
			if len(caller.requests) != wantCalls {
				t.Fatalf("preview calls = %d, want %d", len(caller.requests), wantCalls)
			}
			for _, req := range caller.requests {
				if req.ToolName != "previewOrder" || req.Server.Headers["Authorization"] != "Bearer own-token" {
					t.Fatal("preview did not use own token")
				}
			}
			if pending.order.InitiatorOpenID != tc.initiator || pending.order.RequesterOpenID != "ou_user" || pending.order.CredentialScope.ID != "ou_user" {
				t.Fatalf("pending lost separate initiator/requester/account identities: %+v", pending.order)
			}
		})
	}
}

func TestCouponCannotUseAnotherPersonsAccount(t *testing.T) {
	for _, tc := range []struct{ name, requester, owner, chat string }{
		{"not_requester", "ou_other", "ou_other", "oc_chat"},
		{"legacy_mismatched_account", "ou_user", "ou_initiator", "oc_chat"},
		{"other_chat", "ou_user", "ou_user", "oc_other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			order := luckin.NewPendingOrder(luckin.NewPendingOrderRequest{
				ChatID: tc.chat, InitiatorOpenID: "ou_initiator", RequesterOpenID: tc.requester,
				Credential:         luckin.Credential{Scope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: tc.owner}},
				CreateOrderPayload: json.RawMessage(`{"deptId":1}`), CartSnapshot: []luckin.CartItem{{ProductID: 1, Amount: 1}}, Now: time.Now(),
			})
			action := testActionContextNoMsgWithForm(map[string]any{cardactionproto.PendingOrderIDField: order.ID, cardactionproto.PayloadHashField: order.PayloadHash}, nil)
			req := credentialRequestFromAction(action)
			order.AppID = req.AppID
			order.BotOpenID = req.BotOpenID
			pending := &memPendingStore{order: order}
			tokens := &accountTokens{values: map[string]string{tc.owner: "token"}}
			caller := &accountCaller{}
			task, err := handleCouponApply(nil, luckin.NewDraftService(caller, luckin.ServerURL), pending, tokens)(context.Background(), action)
			if err != nil {
				t.Fatal(err)
			}
			task(context.Background())
			if len(tokens.lookups) > 0 || len(caller.requests) > 0 || pending.updated {
				t.Fatal("unauthorized coupon operation read credentials or changed order")
			}
		})
	}
}

func TestCouponSelfServiceKeepsUsersAccount(t *testing.T) {
	action := testActionContextNoMsgWithForm(nil, map[string]any{cardactionproto.LuckinCouponFormField: []string{"own-coupon"}})
	req := credentialRequestFromAction(action)
	order := luckin.NewPendingOrder(luckin.NewPendingOrderRequest{
		AppID: req.AppID, BotOpenID: req.BotOpenID, ChatID: req.ChatID,
		InitiatorOpenID: "ou_initiator", RequesterOpenID: req.OpenID,
		CheckoutMode:       luckin.CheckoutModeSelfService,
		Credential:         luckin.Credential{Scope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: req.OpenID}},
		CreateOrderPayload: json.RawMessage(`{"deptId":1}`),
		CartSnapshot:       []luckin.CartItem{{ProductID: 1, Amount: 1}}, Now: time.Now(),
	})
	action.Action.Value = map[string]any{cardactionproto.PendingOrderIDField: order.ID, cardactionproto.PayloadHashField: order.PayloadHash}
	pending := &memPendingStore{order: order}
	tokens := &accountTokens{values: map[string]string{req.OpenID: "own-token", "ou_initiator": "other-token"}}
	caller := &accountCaller{}
	task, err := handleCouponApply(nil, luckin.NewDraftService(caller, luckin.ServerURL), pending, tokens)(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	task(context.Background())
	if !pending.updated || len(caller.requests) != 1 || caller.requests[0].Server.Headers["Authorization"] != "Bearer own-token" {
		t.Fatal("self-service coupon preview must use the buyer's personal account")
	}
	if pending.order.CredentialScope != order.CredentialScope || pending.order.RequesterOpenID != req.OpenID || pending.order.InitiatorOpenID != "ou_initiator" || !pending.order.ExpiresAt.Equal(order.ExpiresAt) {
		t.Fatal("coupon revision changed the account owner, requester, initiator or expiry")
	}
}
