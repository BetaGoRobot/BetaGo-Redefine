package luckin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

func TestShopOptionsFromResultParsesMCPContent(t *testing.T) {
	content := json.RawMessage(`[{"type":"text","text":"{\"code\":0,\"data\":[{\"deptId\":245062453,\"deptName\":\"AI点单专用\",\"address\":\"北京安贞\",\"longitude\":116.39,\"latitude\":39.98}]}"}]`)
	shops := ShopOptionsFromResult(content, 5)
	if len(shops) != 1 {
		t.Fatalf("shops = %+v", shops)
	}
	if shops[0].DeptID != 245062453 || shops[0].DeptName != "AI点单专用" || shops[0].Longitude == 0 {
		t.Fatalf("shop mismatch: %+v", shops[0])
	}
}

func TestProductOptionsFromResultParsesMCPContent(t *testing.T) {
	content := json.RawMessage(`[{"type":"text","text":"{\"data\":[{\"productId\":5293,\"productName\":\"生椰拿铁\",\"skuCode\":\"SP-1\",\"estimatePrice\":16}]}"}]`)
	products := ProductOptionsFromResult(content, 5)
	if len(products) != 1 {
		t.Fatalf("products = %+v", products)
	}
	if products[0].ProductID != 5293 || products[0].SkuCode != "SP-1" || products[0].Price != 16 {
		t.Fatalf("product mismatch: %+v", products[0])
	}
}

func TestBuildShopSelectCardContainsCallbackValues(t *testing.T) {
	card := BuildShopSelectCard("人民广场", []ShopOption{{DeptID: 1, DeptName: "门店A", Longitude: 1.1, Latitude: 2.2}})
	text := mustMarshalForTest(card)
	if !containsAll(text, "luckin_shop_select", "luckin_dept_id", "门店A", "luckin_longitude") {
		t.Fatalf("shop card missing fields: %s", text)
	}
	if !containsAll(text, `"tag":"column_set"`, `"columns"`, "选这家") {
		t.Fatalf("shop card should render each shop as horizontal row: %s", text)
	}
}

func TestNormalizeLocationTextUsesRegionData(t *testing.T) {
	got := NormalizeLocationText("朝阳区 安贞环宇荟")
	if got != "北京市 朝阳区 安贞环宇荟" {
		t.Fatalf("NormalizeLocationText() = %q, want Beijing district completion", got)
	}

	got = NormalizeLocationText("广东省 深圳市 南山区 科技园")
	if got != "广东省 深圳市 南山区 科技园" {
		t.Fatalf("NormalizeLocationText() should keep full region, got %q", got)
	}
}

func TestBuildProductSelectCardContainsCallbackValues(t *testing.T) {
	card := BuildProductSelectCard(ShopSelection{DeptName: "门店A"}, Cart{}, []ProductOption{{ProductID: 9, SkuCode: "SP-9", ProductName: "拿铁"}}, nil)
	text := mustMarshalForTest(card)
	if !containsAll(text, "luckin_product_select", "luckin_product_id", "SP-9", "拿铁") {
		t.Fatalf("product card missing fields: %s", text)
	}
}

func TestBuildBindTokenCardIsPersonalOnly(t *testing.T) {
	text := mustMarshalForTest(BuildBindTokenCard(ChatTypeGroup))
	if !containsAll(text, "luckin_bind_token", "luckin_token", "open.lkcoffee.com") {
		t.Fatalf("bind card missing fields: %s", text)
	}
	// 个人专属：不再有作用域选择。
	if containsAll(text, "luckin_scope") {
		t.Fatalf("bind card should not have scope select: %s", text)
	}
}

func TestDraftServiceBuildsPendingOrderWithPreview(t *testing.T) {
	caller := &fakeToolCaller{result: mcpclient.CallResult{Content: json.RawMessage(`[{"type":"text","text":"{\"data\":{\"discountPrice\":16}}"}]`)}}
	svc := NewDraftService(caller, ServerURL)
	order, card, err := svc.Draft(context.Background(), DraftRequest{
		AppID:           "app",
		BotOpenID:       "bot",
		ChatID:          "chat",
		RequesterOpenID: "user",
		Credential:      Credential{Token: "token-1", Scope: CredentialScope{Type: ScopePersonal, ID: "user"}},
		Shop:            ShopSelection{DeptID: 100, DeptName: "门店A", Longitude: 1.1, Latitude: 2.2},
		Items:           []CartItem{{ProductID: 9, SkuCode: "SP-9", ProductName: "拿铁", Amount: 1}},
		Now:             time.Unix(100, 0),
	})
	if err != nil {
		t.Fatalf("Draft error = %v", err)
	}
	if caller.req.ToolName != "previewOrder" {
		t.Fatalf("preview tool = %q", caller.req.ToolName)
	}
	if order.ID == "" || order.PayloadHash == "" {
		t.Fatalf("order id/hash missing")
	}
	var payload map[string]any
	if err := json.Unmarshal(order.CreateOrderPayload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload["deptId"].(float64) != 100 || payload["longitude"].(float64) != 1.1 {
		t.Fatalf("payload missing shop fields: %+v", payload)
	}
	if string(order.PreviewResult) != `{"discountPrice":16}` {
		t.Fatalf("preview result mismatch: %s", order.PreviewResult)
	}
	if card == nil {
		t.Fatalf("card is nil")
	}
}
