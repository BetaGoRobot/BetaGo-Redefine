package luckinaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

type controlledCouponCaller struct {
	requests     []mcpclient.CallRequest
	err          error
	preview      json.RawMessage
	beforeReturn func()
}

func (c *controlledCouponCaller) CallTool(_ context.Context, req mcpclient.CallRequest) (mcpclient.CallResult, error) {
	c.requests = append(c.requests, req)
	if c.beforeReturn != nil {
		c.beforeReturn()
	}
	return mcpclient.CallResult{Content: c.preview}, c.err
}

func TestCouponRefreshUpdatesOriginalChildCardAndCanRetryPreview(t *testing.T) {
	action := testActionContextWithMsgAndForm(nil, nil, "child-two")
	req := credentialRequestFromAction(action)
	order := luckin.NewPendingOrder(luckin.NewPendingOrderRequest{
		AppID: req.AppID, BotOpenID: req.BotOpenID, ChatID: req.ChatID, RequesterOpenID: req.OpenID, InitiatorOpenID: "other-initiator",
		Credential:         luckin.Credential{Scope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: req.OpenID}},
		CreateOrderPayload: json.RawMessage(`{"deptId":1,"productList":[{"productId":2,"amount":1}],"couponCodeList":["used-coupon"]}`), Now: time.Now(),
	})
	action.Action.Value = map[string]any{cardactionproto.PendingOrderIDField: order.ID, cardactionproto.PayloadHashField: order.PayloadHash}
	store := &memPendingStore{order: order}
	caller := &controlledCouponCaller{err: errors.New("preview unavailable")}
	tokens := &accountTokens{values: map[string]string{req.OpenID: "own-token"}}
	var cards []string
	patch := func(_ context.Context, msgID string, card any) error {
		if msgID != "child-two" {
			t.Fatalf("patched sibling or parent card %s", msgID)
		}
		cards = append(cards, mustMarshalForTest(card))
		return nil
	}
	handler := handleCouponApplyWithPatch(nil, luckin.NewDraftService(caller, luckin.ServerURL), store, tokens, patch)
	task, err := handler(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	task(context.Background())
	if len(cards) != 1 || !strings.Contains(cards[0], "刷新优惠券与价格") || strings.Contains(cards[0], "luckin_order_confirm") {
		t.Fatalf("preview failure must remain retryable, cards=%v", cards)
	}
	caller.err = nil
	caller.preview = json.RawMessage(`{"data":{"discountPrice":18,"couponCodeList":["fresh-coupon"]}}`)
	task, err = handler(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	task(context.Background())
	if len(cards) != 2 || !strings.Contains(cards[1], "luckin_order_confirm") || !strings.Contains(cards[1], "fresh-coupon") || strings.Contains(cards[1], "used-coupon") {
		t.Fatal("refresh did not restore a fresh confirm card")
	}
	if store.order.ID != order.ID || !store.order.ExpiresAt.Equal(order.ExpiresAt) {
		t.Fatal("refresh replaced the order or extended its expiry")
	}
	for _, call := range caller.requests {
		if call.ToolName != "previewOrder" || call.Server.Headers["Authorization"] != "Bearer own-token" || strings.Contains(string(call.Arguments), "used-coupon") {
			t.Fatal("refresh must only preview the same account with cleared coupons")
		}
	}
}

func TestDelayedCouponPreviewCannotReplaceNewerDraftOrCard(t *testing.T) {
	for _, tc := range []struct {
		name           string
		complete, fail bool
	}{{"revised", false, false}, {"confirmed", true, false}, {"late_failure_revised", false, true}, {"late_failure_confirmed", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			action := testActionContextWithMsgAndForm(nil, map[string]any{cardactionproto.LuckinCouponFormField: []string{"slow-coupon"}}, "child-two")
			req := credentialRequestFromAction(action)
			order := luckin.NewPendingOrder(luckin.NewPendingOrderRequest{AppID: req.AppID, BotOpenID: req.BotOpenID, ChatID: req.ChatID, RequesterOpenID: req.OpenID, Credential: luckin.Credential{Scope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: req.OpenID}}, CreateOrderPayload: json.RawMessage(`{"deptId":1,"productList":[{"productId":2,"amount":1}]}`), Now: time.Now()})
			action.Action.Value = map[string]any{cardactionproto.PendingOrderIDField: order.ID, cardactionproto.PayloadHashField: order.PayloadHash}
			store := &memPendingStore{order: order}
			patches := 0
			caller := &controlledCouponCaller{preview: json.RawMessage(`{"data":{"discountPrice":10}}`), beforeReturn: func() {
				if tc.complete {
					store.order.Status = luckin.PendingStatusConfirmed
				} else {
					store.order.PayloadHash = "newer-hash"
				}
			}}
			if tc.fail {
				caller.err = errors.New("preview failed after another request completed")
			}
			handler := handleCouponApplyWithPatch(nil, luckin.NewDraftService(caller, luckin.ServerURL), store, &accountTokens{values: map[string]string{req.OpenID: "own-token"}}, func(context.Context, string, any) error { patches++; return nil })
			task, err := handler(context.Background(), action)
			if err != nil {
				t.Fatal(err)
			}
			task(context.Background())
			if store.updated || patches != 0 {
				t.Fatal("late quote replaced a newer draft or its card")
			}
			// Replayed stale action must also leave the current view intact.
			task, err = handler(context.Background(), action)
			if err != nil {
				t.Fatal(err)
			}
			task(context.Background())
			if len(caller.requests) != 1 || patches != 0 {
				t.Fatal("stale callback started another preview or changed the card")
			}
		})
	}
}

type delayedCouponTokens struct{ pending *memPendingStore }

func (s delayedCouponTokens) FindToken(context.Context, luckin.CredentialLookup) (luckin.Credential, error) {
	s.pending.order.Status = luckin.PendingStatusConfirmed
	return luckin.Credential{}, errors.New("credential lookup failed after confirmation")
}

func TestDelayedCouponCredentialFailureDoesNotReplaceCompletedCard(t *testing.T) {
	action := testActionContextWithMsgAndForm(nil, nil, "child-two")
	req := credentialRequestFromAction(action)
	order := luckin.NewPendingOrder(luckin.NewPendingOrderRequest{AppID: req.AppID, BotOpenID: req.BotOpenID, ChatID: req.ChatID, RequesterOpenID: req.OpenID, Credential: luckin.Credential{Scope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: req.OpenID}}, CreateOrderPayload: json.RawMessage(`{"deptId":1,"productList":[]}`), Now: time.Now()})
	action.Action.Value = map[string]any{cardactionproto.PendingOrderIDField: order.ID, cardactionproto.PayloadHashField: order.PayloadHash}
	store := &memPendingStore{order: order}
	patches := 0
	handler := handleCouponApplyWithPatch(nil, luckin.DraftService{}, store, delayedCouponTokens{store}, func(context.Context, string, any) error { patches++; return nil })
	task, err := handler(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	task(context.Background())
	if patches != 0 {
		t.Fatal("delayed credential failure replaced the completed card")
	}
}
