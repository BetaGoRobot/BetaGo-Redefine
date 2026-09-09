package luckin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

type recoveryCaller struct {
	requests   []mcpclient.CallRequest
	createErr  error
	previewErr error
	preview    json.RawMessage
}

func (c *recoveryCaller) CallTool(_ context.Context, req mcpclient.CallRequest) (mcpclient.CallResult, error) {
	c.requests = append(c.requests, req)
	if req.ToolName == "createOrder" {
		return mcpclient.CallResult{Content: json.RawMessage(`{"data":{"orderId":"created"}}`)}, c.createErr
	}
	return mcpclient.CallResult{Content: c.preview}, c.previewErr
}

func couponRecoveryOrder() PendingOrder {
	order := testConfirmableOrder(json.RawMessage(`{"deptId":1,"longitude":12,"latitude":34,"productList":[{"productId":2,"skuCode":"sku","amount":1}],"couponCodeList":["used-coupon"],"extra":"preserve"}`), time.Now().Add(time.Minute))
	order.CartSnapshot = []CartItem{{ProductID: 2, SkuCode: "sku", Amount: 1, AddedByOpenID: "user"}}
	order.PreviewResult = json.RawMessage(`{"couponCodeList":["used-coupon"],"discountPrice":1}`)
	return order
}

func recoveryConfirm(t *testing.T, order PendingOrder, store PendingOrderStore, caller ToolCaller) (ConfirmationService, error) {
	t.Helper()
	service := NewConfirmationService(store, &fakeCredentialLookup{credential: Credential{Token: "own-token", Scope: order.CredentialScope}}, caller, ServerURL)
	_, err := service.Confirm(context.Background(), ConfirmRequest{PendingOrderID: order.ID, PayloadHash: order.PayloadHash, OperatorOpenID: order.RequesterOpenID, ChatID: order.ChatID, Now: time.Now()})
	return service, err
}

func TestCouponRejectionRefreshesDraftBeforeOfferingResubmit(t *testing.T) {
	order := couponRecoveryOrder()
	store := &fakePendingStore{order: order}
	caller := &recoveryCaller{createErr: &mcpclient.ToolError{Message: "优惠券已被使用"}, preview: json.RawMessage(`{"data":{"couponCodeList":["new-coupon"],"discountPrice":19}}`)}
	service, err := recoveryConfirm(t, order, store, caller)
	if err == nil {
		t.Fatal("coupon failure must not be reported as a created order")
	}
	if len(caller.requests) != 2 || caller.requests[0].ToolName != "createOrder" || caller.requests[1].ToolName != "previewOrder" {
		t.Fatalf("calls=%v, want one create followed by fresh preview", caller.requests)
	}
	var previewArgs map[string]any
	if json.Unmarshal(caller.requests[1].Arguments, &previewArgs) != nil || len(previewArgs["couponCodeList"].([]any)) != 0 {
		t.Fatal("refresh must clear rejected selected coupons")
	}
	if caller.requests[1].Server.Headers["Authorization"] != "Bearer own-token" {
		t.Fatal("refresh used another account")
	}
	updated := store.order
	if updated.ID != order.ID || updated.CredentialScope != order.CredentialScope || updated.RequesterOpenID != order.RequesterOpenID || !updated.ExpiresAt.Equal(order.ExpiresAt) || !reflect.DeepEqual(updated.CartSnapshot, order.CartSnapshot) {
		t.Fatal("refresh changed order identity, items or expiry")
	}
	if updated.PayloadHash == order.PayloadHash || len(selectedCouponsFromPayload(updated.CreateOrderPayload)) != 0 || !strings.Contains(string(updated.CreateOrderPayload), `"extra":"preserve"`) {
		t.Fatal("refresh must save cleared coupons and preserve other submission parameters")
	}
	if store.markConfirmedCalled {
		t.Fatal("rejected order marked confirmed")
	}
	card := string(mustJSON(service.CardAfterConfirmError(context.Background(), order.ID, err, "failed")))
	if !containsJSON(card, "new-coupon", "19", "luckin_order_confirm", updated.PayloadHash, "重新确认") || strings.Contains(card, "used-coupon") {
		t.Fatalf("card did not use refreshed quote: %s", card)
	}
}

func TestCouponRefreshFailureOffersPreviewOnlyRetry(t *testing.T) {
	for _, preview := range []struct {
		name    string
		content json.RawMessage
		err     error
	}{
		{"network", nil, mcpclient.ErrTimeout},
		{"malformed", json.RawMessage(`{"data":null}`), nil},
		{"business_error", json.RawMessage(`{"code":500,"message":"preview failed"}`), nil},
	} {
		t.Run(preview.name, func(t *testing.T) {
			order := couponRecoveryOrder()
			store := &fakePendingStore{order: order}
			caller := &recoveryCaller{createErr: &mcpclient.ToolError{Message: "coupon already used"}, preview: preview.content, previewErr: preview.err}
			service, err := recoveryConfirm(t, order, store, caller)
			card := string(mustJSON(service.CardAfterConfirmError(context.Background(), order.ID, err, "failed")))
			if !containsJSON(card, "刷新优惠券与价格", "luckin_coupon_apply", order.ID, order.PayloadHash) || strings.Contains(card, "luckin_order_confirm") {
				t.Fatalf("failed refresh must offer preview only: %s", card)
			}
			if store.order.PayloadHash != order.PayloadHash {
				t.Fatal("failed preview changed the draft")
			}
		})
	}
}

func TestUncertainSubmissionDoesNotOfferCreateRetry(t *testing.T) {
	for _, createErr := range []error{mcpclient.ErrTimeout, context.Canceled, errors.New("connection reset"), &mcpclient.ToolError{Message: "coupon service timeout"}, &mcpclient.ToolError{Message: "coupon service unavailable"}, &mcpclient.ToolError{Message: "coupon validation failed: authentication expired"}, errors.New("coupon already used")} {
		t.Run(createErr.Error(), func(t *testing.T) {
			order := couponRecoveryOrder()
			store := &fakePendingStore{order: order}
			caller := &recoveryCaller{createErr: createErr}
			service, err := recoveryConfirm(t, order, store, caller)
			card := string(mustJSON(service.CardAfterConfirmError(context.Background(), order.ID, err, "failed")))
			if len(caller.requests) != 1 || strings.Contains(card, "luckin_order_confirm") || strings.Contains(card, "luckin_coupon_apply") || strings.Contains(card, "luckin_cart_checkout") || strings.Contains(card, "重新结算") || !strings.Contains(card, "核对订单") {
				t.Fatalf("uncertain submission offered a fresh create: %s", card)
			}
		})
	}
}

func TestRefreshedPriceChangesConfirmationHash(t *testing.T) {
	caller := &recoveryCaller{preview: json.RawMessage(`{"data":{"discountPrice":10}}`)}
	draft := NewDraftService(caller, ServerURL)
	first, err := draft.RefreshPending(context.Background(), couponRecoveryOrder(), Credential{Token: "own"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	caller.preview = json.RawMessage(`{"data":{"discountPrice":20}}`)
	second, err := draft.RefreshPending(context.Background(), first, Credential{Token: "own"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.PayloadHash == second.PayloadHash {
		t.Fatal("a changed quote must invalidate the old confirmation even with unchanged coupons")
	}
}

func TestCouponServiceProtocolFailureIsNotARejection(t *testing.T) {
	for _, message := range []string{"invalid coupon service response", "优惠券无效响应：解析失败", "coupon unavailable: service error"} {
		if isCouponRejection(&mcpclient.ToolError{Message: message}) {
			t.Errorf("service error treated as definite coupon rejection: %s", message)
		}
	}
}

type recoveryWriteStore struct {
	fakePendingStore
	writeErr          error
	commitBeforeError bool
	confirmErr        error
}

func (s *recoveryWriteStore) UpdateDraft(ctx context.Context, order PendingOrder, expected string, now time.Time) error {
	if s.commitBeforeError {
		if err := s.fakePendingStore.UpdateDraft(ctx, order, expected, now); err != nil {
			return err
		}
	}
	return s.writeErr
}

func (s *recoveryWriteStore) MarkConfirmed(context.Context, string, string, string, json.RawMessage, time.Time) error {
	return s.confirmErr
}

func TestCouponRecoveryReadsBackAmbiguousDraftWrite(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "not_saved", true: "saved_but_ack_lost"}[committed], func(t *testing.T) {
			order := couponRecoveryOrder()
			store := &recoveryWriteStore{fakePendingStore: fakePendingStore{order: order}, commitBeforeError: committed, writeErr: errors.New("database timeout")}
			caller := &recoveryCaller{createErr: &mcpclient.ToolError{Message: "coupon already used"}, preview: json.RawMessage(`{"data":{"discountPrice":20,"couponCodeList":["fresh"]}}`)}
			service, err := recoveryConfirm(t, order, store, caller)
			card := string(mustJSON(service.CardAfterConfirmError(context.Background(), order.ID, err, "failed")))
			if committed {
				if !containsJSON(card, "luckin_order_confirm", store.order.PayloadHash, "fresh") {
					t.Fatal("saved draft not recovered after lost acknowledgement")
				}
			} else if strings.Contains(card, "luckin_order_confirm") || !strings.Contains(card, "刷新优惠券与价格") {
				t.Fatal("unsaved draft offered a create")
			}
		})
	}
}

func TestCreatedOrderWithFailedLocalWriteDoesNotOfferResubmit(t *testing.T) {
	order := couponRecoveryOrder()
	store := &recoveryWriteStore{fakePendingStore: fakePendingStore{order: order}, confirmErr: errors.New("database unavailable")}
	caller := &recoveryCaller{}
	service, err := recoveryConfirm(t, order, store, caller)
	card := string(mustJSON(service.CardAfterConfirmError(context.Background(), order.ID, err, "failed")))
	if len(caller.requests) != 1 || strings.Contains(card, "luckin_order_confirm") || !strings.Contains(card, "核对订单") {
		t.Fatal("created order with failed local write offered resubmit")
	}
}

type competingCouponCaller struct {
	mu                sync.Mutex
	creates, previews int
	bothCreating      chan struct{}
}

func (c *competingCouponCaller) CallTool(_ context.Context, req mcpclient.CallRequest) (mcpclient.CallResult, error) {
	c.mu.Lock()
	if req.ToolName == "previewOrder" {
		c.previews++
		c.mu.Unlock()
		return mcpclient.CallResult{Content: json.RawMessage(`{"data":{"discountPrice":19,"couponCodeList":["another-coupon"]}}`)}, nil
	}
	c.creates++
	first := c.creates == 1
	if c.creates == 2 {
		close(c.bothCreating)
	}
	c.mu.Unlock()
	if first {
		<-c.bothCreating
		return mcpclient.CallResult{Content: json.RawMessage(`{"data":{"orderId":"created-one"}}`)}, nil
	}
	return mcpclient.CallResult{}, &mcpclient.ToolError{Message: "coupon already used"}
}

func TestConcurrentSplitOrdersRefreshOnlyRejectedChild(t *testing.T) {
	caller := &competingCouponCaller{bothCreating: make(chan struct{})}
	var wg sync.WaitGroup
	stores := []*fakePendingStore{{order: couponRecoveryOrder()}, {order: couponRecoveryOrder()}}
	errs := make([]error, 2)
	cards := make([]map[string]any, 2)
	for i, store := range stores {
		store.order.ID = []string{"child-one", "child-two"}[i]
		wg.Add(1)
		go func(i int, store *fakePendingStore) {
			defer wg.Done()
			order := store.order
			service := NewConfirmationService(store, &fakeCredentialLookup{credential: Credential{Token: "same-account"}}, caller, ServerURL)
			cards[i], errs[i] = service.Confirm(context.Background(), ConfirmRequest{PendingOrderID: order.ID, PayloadHash: order.PayloadHash, OperatorOpenID: order.RequesterOpenID, ChatID: order.ChatID, Now: time.Now()})
			if errs[i] != nil {
				cards[i] = service.CardAfterConfirmError(context.Background(), order.ID, errs[i], "failed")
			}
		}(i, store)
	}
	wg.Wait()
	confirmed, recovered := 0, 0
	for i, store := range stores {
		if errs[i] == nil {
			confirmed++
			if !store.markConfirmedCalled || !strings.Contains(string(mustJSON(cards[i])), "created-one") {
				t.Fatal("successful sibling lost its result")
			}
		} else {
			recovered++
			if store.markConfirmedCalled || !containsJSON(string(mustJSON(cards[i])), store.order.ID, "another-coupon", "luckin_order_confirm") {
				t.Fatal("rejected sibling did not receive its own fresh draft")
			}
		}
	}
	if confirmed != 1 || recovered != 1 || caller.creates != 2 || caller.previews != 1 {
		t.Fatalf("creates=%d previews=%d confirmed=%d recovered=%d", caller.creates, caller.previews, confirmed, recovered)
	}
}
