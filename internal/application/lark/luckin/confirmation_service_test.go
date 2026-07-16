package luckin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

func TestConfirmationServiceConfirmCreatesRemoteOrderAndMarksConfirmed(t *testing.T) {
	now := time.Unix(100, 0)
	payload := json.RawMessage(`{"deptId":1}`)
	order := testConfirmableOrder(payload, now.Add(time.Minute))
	store := &fakePendingStore{order: order}
	credentials := &fakeCredentialLookup{credential: Credential{Token: "token-one"}}
	caller := &fakeToolCaller{result: mcpclient.CallResult{Content: json.RawMessage(`[{"text":"created"}]`)}}
	service := NewConfirmationService(store, credentials, caller, ServerURL)

	card, err := service.Confirm(context.Background(), ConfirmRequest{
		PendingOrderID: order.ID,
		PayloadHash:    order.PayloadHash,
		OperatorOpenID: order.RequesterOpenID,
		ChatID:         order.ChatID,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("Confirm error = %v", err)
	}
	if card == nil {
		t.Fatalf("Confirm card is nil")
	}
	if caller.req.ToolName != "createOrder" {
		t.Fatalf("tool name = %q", caller.req.ToolName)
	}
	if string(caller.req.Arguments) != string(payload) {
		t.Fatalf("remote createOrder payload mismatch")
	}
	if caller.req.Server.Headers["Authorization"] != "Bearer token-one" {
		t.Fatalf("Authorization header mismatch")
	}
	if !store.markConfirmedCalled || store.markHash != order.PayloadHash {
		t.Fatalf("MarkConfirmed was not called with expected hash")
	}
	if !jsonEqualForLuckinTest(store.markResult, caller.result.Content) {
		t.Fatalf("MarkConfirmed result mismatch")
	}
	if credentials.lookup.Scope != order.CredentialScope {
		t.Fatalf("credential lookup scope = %+v", credentials.lookup.Scope)
	}
}

func TestCardAfterConfirmErrorRestoresPendingConfirmCard(t *testing.T) {
	now := time.Now()
	order := testConfirmableOrder(json.RawMessage(`{"deptId":1}`), now.Add(time.Minute))
	order.PreviewResult = json.RawMessage(`{"couponCodeList":["coupon-a"],"discountPrice":12}`)
	store := &fakePendingStore{order: order}
	service := NewConfirmationService(store, &fakeCredentialLookup{}, &fakeToolCaller{}, ServerURL)

	card := service.CardAfterConfirmError(context.Background(), order.ID, errors.New("coupon already used"), "创建订单失败：coupon already used")
	text := string(mustJSON(card))
	if !containsJSON(text, "coupon already used", "luckin_order_confirm", order.ID, order.PayloadHash) {
		t.Fatalf("expected restored confirm card, got %s", text)
	}
	if containsJSON(text, "重新选择门店", "luckin_cart_checkout") {
		t.Fatalf("must not fall back to initial shop search: %s", text)
	}
}

func TestCardAfterConfirmErrorUsesTerminalFailedWhenExpired(t *testing.T) {
	service := NewConfirmationService(&fakePendingStore{}, &fakeCredentialLookup{}, &fakeToolCaller{}, ServerURL)
	card := service.CardAfterConfirmError(context.Background(), "po_x", ErrPendingOrderExpired, "订单已过期，请重新结算")
	text := string(mustJSON(card))
	if !containsJSON(text, "订单已过期", "重新结算") {
		t.Fatalf("expected terminal failed card, got %s", text)
	}
}

func TestConfirmationServiceConfirmRejectsInvalidPendingOrder(t *testing.T) {
	now := time.Unix(100, 0)
	base := testConfirmableOrder(json.RawMessage(`{"deptId":1}`), now.Add(time.Minute))
	tests := []struct {
		name    string
		req     ConfirmRequest
		edit    func(*PendingOrder)
		wantErr error
	}{
		{name: "hash", req: ConfirmRequest{PayloadHash: "wrong", OperatorOpenID: base.RequesterOpenID, ChatID: base.ChatID, Now: now}, wantErr: ErrPendingOrderPayloadMismatch},
		{name: "chat", req: ConfirmRequest{PayloadHash: base.PayloadHash, OperatorOpenID: base.RequesterOpenID, ChatID: "other-chat", Now: now}, wantErr: ErrPendingOrderChatMismatch},
		{name: "operator", req: ConfirmRequest{PayloadHash: base.PayloadHash, OperatorOpenID: "other-user", ChatID: base.ChatID, Now: now}, wantErr: ErrPendingOrderNotOwnedByOperator},
		{name: "status", req: ConfirmRequest{PayloadHash: base.PayloadHash, OperatorOpenID: base.RequesterOpenID, ChatID: base.ChatID, Now: now}, edit: func(order *PendingOrder) {
			order.Status = PendingStatusCancelled
		}, wantErr: ErrPendingOrderAlreadyDone},
		{name: "expired", req: ConfirmRequest{PayloadHash: base.PayloadHash, OperatorOpenID: base.RequesterOpenID, ChatID: base.ChatID, Now: now}, edit: func(order *PendingOrder) {
			order.ExpiresAt = now.Add(-time.Second)
		}, wantErr: ErrPendingOrderExpired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := base
			if tt.edit != nil {
				tt.edit(&order)
			}
			tt.req.PendingOrderID = order.ID
			store := &fakePendingStore{order: order}
			service := NewConfirmationService(store, &fakeCredentialLookup{}, &fakeToolCaller{}, ServerURL)
			if _, err := service.Confirm(context.Background(), tt.req); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Confirm err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func testConfirmableOrder(payload json.RawMessage, expiresAt time.Time) PendingOrder {
	order := NewPendingOrder(NewPendingOrderRequest{
		AppID:              "app",
		BotOpenID:          "bot",
		ChatID:             "chat",
		InitiatorOpenID:    "user",
		RequesterOpenID:    "user",
		CheckoutMode:       CheckoutModeInitiatorUnified,
		Credential:         Credential{Scope: CredentialScope{Type: ScopePersonal, ID: "user"}},
		CreateOrderPayload: payload,
		PreviewResult:      json.RawMessage(`{}`),
		Now:                expiresAt.Add(-time.Minute),
	})
	order.ID = "po_1"
	order.ExpiresAt = expiresAt
	return order
}

type fakePendingStore struct {
	order               PendingOrder
	findErr             error
	markConfirmedCalled bool
	markHash            string
	markResult          json.RawMessage
}

func (s *fakePendingStore) FindPendingOrder(ctx context.Context, id string) (PendingOrder, error) {
	if s.findErr != nil {
		return PendingOrder{}, s.findErr
	}
	return s.order, nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func containsJSON(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

func (s *fakePendingStore) MarkConfirmed(ctx context.Context, id, payloadHash, confirmedByOpenID string, resultJSON json.RawMessage, now time.Time) error {
	s.markConfirmedCalled = true
	s.markHash = payloadHash
	s.markResult = resultJSON
	return nil
}

func (s *fakePendingStore) MarkCancelled(ctx context.Context, id, payloadHash, operatorOpenID, chatID string, now time.Time) error {
	return nil
}

func (s *fakePendingStore) UpdateDraft(ctx context.Context, order PendingOrder, now time.Time) error {
	s.order = order
	return nil
}

type fakeCredentialLookup struct {
	credential Credential
	lookup     CredentialLookup
}

func (s *fakeCredentialLookup) FindToken(ctx context.Context, lookup CredentialLookup) (Credential, error) {
	s.lookup = lookup
	return s.credential, nil
}

type fakeToolCaller struct {
	req    mcpclient.CallRequest
	result mcpclient.CallResult
}

func (c *fakeToolCaller) CallTool(ctx context.Context, req mcpclient.CallRequest) (mcpclient.CallResult, error) {
	c.req = req
	return c.result, nil
}

func jsonEqualForLuckinTest(left, right []byte) bool {
	var leftValue any
	var rightValue any
	return json.Unmarshal(left, &leftValue) == nil &&
		json.Unmarshal(right, &rightValue) == nil &&
		reflect.DeepEqual(leftValue, rightValue)
}
