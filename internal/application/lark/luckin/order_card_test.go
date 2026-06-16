package luckin

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOrderCreatedFromResultParsesQRAndPayURL(t *testing.T) {
	content := json.RawMessage(`[{"type":"text","text":"{\"code\":0,\"data\":{\"orderId\":7651928906786668553,\"payOrderUrl\":\"weixin://wxpay/bizpayurl?pr=abc\",\"payOrderQrCodeUrl\":\"https://open.lkcoffee.com/transfer/qrcode?token=xyz\",\"discountPrice\":21.0,\"needPay\":true,\"orderIdStr\":\"7651928906786668553\"}}"}]`)
	created := OrderCreatedFromResult(content)
	if created.OrderID != "7651928906786668553" {
		t.Fatalf("order id = %q", created.OrderID)
	}
	if created.PayURL == "" || created.QRCodeURL == "" || !created.NeedPay || created.DiscountPrice != 21.0 {
		t.Fatalf("created mismatch: %+v", created)
	}
}

func TestBuildOrderCreatedCardHasPayButtonAndQR(t *testing.T) {
	content := json.RawMessage(`[{"type":"text","text":"{\"data\":{\"orderIdStr\":\"123\",\"payOrderUrl\":\"weixin://x\",\"discountPrice\":21,\"needPay\":true}}"}]`)
	card := BuildOrderCreatedCard(content, "img_qr_1")
	text := mustMarshalForTest(card)
	if !containsAll(text, "weixin://x", "img_qr_1", "luckin_order_status", "¥21") {
		t.Fatalf("order created card missing fields: %s", text)
	}
}

func TestOrderDetailFromResultParsesStatusAndTakeMeal(t *testing.T) {
	content := json.RawMessage(`[{"type":"text","text":"{\"data\":{\"orderId\":\"123\",\"orderStatus\":60,\"orderStatusName\":\"等待取餐\",\"aboutTime\":1778751420000,\"takeMealCodeInfo\":{\"code\":\"A12\"},\"shopInfo\":{\"deptName\":\"门店A\",\"address\":\"地址A\"},\"productInfoList\":[{\"name\":\"拿铁\",\"amount\":1,\"additionDesc\":\"热\"}]}}"}]`)
	detail := OrderDetailFromResult(content)
	if detail.Status != OrderStatusReady || detail.TakeMealCode != "A12" || detail.ShopName != "门店A" {
		t.Fatalf("detail mismatch: %+v", detail)
	}
	if len(detail.Products) != 1 || detail.Products[0].Name != "拿铁" {
		t.Fatalf("products mismatch: %+v", detail.Products)
	}
}

func TestEvaluatePollTransitionsAndStops(t *testing.T) {
	cfg := DefaultOrderPollConfig()
	now := time.Unix(1000, 0)
	base := OrderRecord{
		CreatedAt:        now.Add(-time.Minute),
		PollDeadline:     now.Add(time.Hour),
		LastRemoteStatus: OrderStatusUnpaid,
	}

	// 制作中：非终态，记录时间戳 + 通知 + 继续轮询。
	d := EvaluatePoll(base, OrderDetail{Status: OrderStatusMaking}, cfg, now)
	if d.Status != "" || d.NextPollAt == nil || d.NoticeText == "" {
		t.Fatalf("making decision = %+v", d)
	}
	if _, ok := d.StatusTimestamps["making_at"]; !ok {
		t.Fatalf("making_at not recorded: %+v", d.StatusTimestamps)
	}

	// 已完成：终态，停止。
	d = EvaluatePoll(base, OrderDetail{Status: OrderStatusCompleted}, cfg, now)
	if d.Status != OrderRecordCompleted || d.NextPollAt != nil {
		t.Fatalf("completed decision = %+v", d)
	}

	// 已取消：终态，停止。
	d = EvaluatePoll(base, OrderDetail{Status: OrderStatusCancelled}, cfg, now)
	if d.Status != OrderRecordCancelled {
		t.Fatalf("cancelled decision = %+v", d)
	}
}

func TestEvaluatePollUnpaidReminderOnceAndTimeout(t *testing.T) {
	cfg := DefaultOrderPollConfig()
	now := time.Unix(10000, 0)

	// 超过提醒阈值但未超时：发一次提醒。
	rec := OrderRecord{CreatedAt: now.Add(-cfg.UnpaidRemindAt - time.Second), PollDeadline: now.Add(time.Hour), LastRemoteStatus: OrderStatusUnpaid}
	d := EvaluatePoll(rec, OrderDetail{Status: OrderStatusUnpaid}, cfg, now)
	if !d.SendUnpaidReminder || d.UnpaidReminded == nil || !*d.UnpaidReminded {
		t.Fatalf("expected unpaid reminder: %+v", d)
	}
	if d.NextPollAt == nil {
		t.Fatalf("should keep polling after reminder")
	}

	// 已提醒过：不再重复提醒。
	rec.UnpaidReminded = true
	d = EvaluatePoll(rec, OrderDetail{Status: OrderStatusUnpaid}, cfg, now)
	if d.SendUnpaidReminder {
		t.Fatalf("should not remind twice")
	}

	// 超过未支付超时：停止并标记 expired。
	rec2 := OrderRecord{CreatedAt: now.Add(-cfg.UnpaidTimeout - time.Second), PollDeadline: now.Add(time.Hour), LastRemoteStatus: OrderStatusUnpaid}
	d = EvaluatePoll(rec2, OrderDetail{Status: OrderStatusUnpaid}, cfg, now)
	if d.Status != OrderRecordExpired {
		t.Fatalf("expected expired on unpaid timeout: %+v", d)
	}
}

func TestEvaluatePollDeadlineBackstop(t *testing.T) {
	cfg := DefaultOrderPollConfig()
	now := time.Unix(99999, 0)
	rec := OrderRecord{CreatedAt: now.Add(-time.Hour), PollDeadline: now.Add(-time.Second), LastRemoteStatus: OrderStatusMaking}
	d := EvaluatePoll(rec, OrderDetail{Status: OrderStatusMaking}, cfg, now)
	if d.Status != OrderRecordExpired || d.NextPollAt != nil {
		t.Fatalf("expected expired on deadline: %+v", d)
	}
}

func TestBuildSpecSelectCardAndParse(t *testing.T) {
	detail := ProductDetail{
		ProductID:   9,
		SkuCode:     "SP-9",
		ProductName: "拿铁",
		Attrs: []ProductAttr{{
			AttributeID:   1,
			AttributeName: "杯型",
			SubAttrs: []ProductSubAttr{
				{AttributeID: 11, AttributeName: "大杯", Price: 3, Selected: true},
				{AttributeID: 12, AttributeName: "标准"},
			},
		}},
	}
	card := BuildSpecSelectCard(ShopSelection{DeptName: "门店A"}, detail, "", 1)
	text := mustMarshalForTest(card)
	if !containsAll(text, "luckin_spec_1", "杯型", "大杯", "luckin_product_select") {
		t.Fatalf("spec card missing fields: %s", text)
	}
	sel := ParseSpecSelection(map[string]string{"luckin_spec_1": "11", "other": "x"})
	if len(sel) != 1 || sel[1] != 11 {
		t.Fatalf("spec selection mismatch: %+v", sel)
	}
}
