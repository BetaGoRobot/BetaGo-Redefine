package luckin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildPendingOrderCardContainsScopeAndActions(t *testing.T) {
	card := BuildPendingOrderCard(PendingOrder{
		ID:              "po_1",
		PayloadHash:     "hash_1",
		CheckoutMode:    CheckoutModeInitiatorUnified,
		CredentialScope: CredentialScope{Type: ScopeChat, ID: "chat"},
	})
	text := mustMarshalForTest(card)
	if !containsAll(text, "群聊默认瑞幸账号", "统一下单", "luckin_order_confirm", "luckin_order_cancel") {
		t.Fatalf("card missing required scope/action content")
	}
	if !containsAll(text, "po_1", "hash_1", "pending_order_id", "payload_hash") {
		t.Fatalf("card missing callback fields")
	}
}

func TestBuildPendingOrderCardSummarizesPreview(t *testing.T) {
	card := BuildPendingOrderCard(PendingOrder{
		ID:              "po_1",
		PayloadHash:     "hash_1",
		CheckoutMode:    CheckoutModeSelfService,
		CredentialScope: CredentialScope{Type: ScopePersonal, ID: "user"},
		PreviewResult: json.RawMessage(`{
			"shopInfo":{"deptName":"瑞幸咖啡 上海路店","address":"上海路 1 号","distance":0.8},
			"productInfoList":[{"name":"生椰拿铁","additionDesc":"少冰 / 标准糖","amount":2}],
			"discountPrice":19.8,
			"totalInitialPrice":58,
			"couponCodeList":["coupon-a"],
			"aboutTime":"10:30"
		}`),
	})

	text := mustMarshalForTest(card)
	if !containsAll(text, "瑞幸咖啡 上海路店", "上海路 1 号", "0.8km") {
		t.Fatalf("card missing shop summary")
	}
	if !containsAll(text, "自我下单") {
		t.Fatalf("card missing checkout mode")
	}
	if !containsAll(text, "生椰拿铁", "少冰 / 标准糖", "x 2") {
		t.Fatalf("card missing product summary")
	}
	if !containsAll(text, "瑞幸预估实付 ¥19.8", "商品原价 ¥58", "平台自动优惠", "10:30") {
		t.Fatalf("card missing price/time summary")
	}
	// 有可用优惠券时渲染多选表单供选择。
	if !containsAll(text, "luckin_coupon", "luckin_coupon_apply", "coupon-a") {
		t.Fatalf("card missing coupon select form")
	}
}

func TestBuildPendingOrderCardDoesNotExposePayload(t *testing.T) {
	card := BuildPendingOrderCard(PendingOrder{
		ID:                 "po_1",
		PayloadHash:        "hash_1",
		CheckoutMode:       CheckoutModeInitiatorUnified,
		CredentialScope:    CredentialScope{Type: ScopePersonal, ID: "user"},
		CreateOrderPayload: []byte(`{"secret":"full-order-payload"}`),
	})
	text := mustMarshalForTest(card)
	if containsAll(text, "full-order-payload") {
		t.Fatalf("card exposes order payload")
	}
}

func TestBuildPendingOrderCardWithNoticeKeepsConfirmActions(t *testing.T) {
	card := BuildPendingOrderCardWithNotice(PendingOrder{
		ID:              "po_2",
		PayloadHash:     "hash_2",
		CheckoutMode:    CheckoutModeInitiatorUnified,
		CredentialScope: CredentialScope{Type: ScopePersonal, ID: "user"},
		PreviewResult:   json.RawMessage(`{"couponCodeList":["coupon-b"],"discountPrice":9.9}`),
	}, "创建订单失败：优惠券不可用。可更换优惠券后重试，无需重新选店。")
	text := mustMarshalForTest(card)
	if !containsAll(text, "优惠券不可用", "订单草稿仍有效", "luckin_order_confirm", "luckin_coupon_apply", "po_2", "hash_2") {
		t.Fatalf("notice card should stay on confirm page with actions: %s", text)
	}
	if containsAll(text, "重新选择门店", "luckin_cart_checkout") {
		t.Fatalf("notice card must not fall back to shop search / re-checkout: %s", text)
	}
}

func TestBuildCartCardCheckoutModeFormContainsSubmitButton(t *testing.T) {
	card := BuildCartCard(ShopSelection{DeptName: "门店A"}, Cart{
		Items: []CartItem{{
			ProductID:   9,
			SkuCode:     "SP-9",
			ProductName: "拿铁",
			Amount:      1,
			LineID:      "line-1",
		}},
	}, CheckoutModeInitiatorUnified)

	text := mustMarshalForTest(card)
	if !containsAll(text, "luckin_checkout_mode_form", "luckin_checkout_submit", "\"form_action_type\":\"submit\"", "luckin_cart_checkout") {
		t.Fatalf("cart card missing checkout submit form: %s", text)
	}
}

func mustMarshalForTest(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
