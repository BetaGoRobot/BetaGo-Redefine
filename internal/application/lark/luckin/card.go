package luckin

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func ScopeLabel(scope CredentialScope) string {
	switch scope.Type {
	case ScopePersonal:
		return "个人瑞幸账号"
	case ScopeChat:
		return "群聊默认瑞幸账号"
	case ScopeSystem:
		return "系统默认瑞幸账号"
	default:
		return "未知瑞幸账号"
	}
}

func BuildPendingOrderCard(order PendingOrder) map[string]any {
	summary := previewSummaryFromOrder(order)
	available := AvailableCouponsFromPreview(order.PreviewResult)
	selected := selectedCouponsFromPayload(order.CreateOrderPayload)
	elements := []any{
		larkmsg.Markdown("**🧾 确认瑞幸订单**"),
		larkmsg.HintMarkdown("账号：" + ScopeLabel(order.CredentialScope)),
		larkmsg.Divider(),
		larkmsg.Markdown("🏬 **门店**\n" + summary.Shop),
		larkmsg.Markdown("☕ **商品**\n" + summary.Products),
	}
	elements = append(elements,
		larkmsg.Markdown("💰 **价格**\n"+summary.Price),
	)
	if summary.PriceNote != "" {
		elements = append(elements, larkmsg.HintMarkdown(summary.PriceNote))
	}
	elements = append(elements, larkmsg.HintMarkdown("⏰ 预计取餐/送达："+summary.AboutTime))
	if len(available) > 0 {
		elements = append(elements, larkmsg.Divider(), couponSelectForm(order, available, selected))
	} else if summary.Coupon != "" {
		elements = append(elements, larkmsg.Markdown("🎟 **优惠券**\n"+summary.Coupon))
	}
	elements = append(elements,
		larkmsg.Divider(),
		larkmsg.HintMarkdown("点击确认将创建瑞幸订单，确认后只创建订单、不会自动支付。"),
		larkmsg.ButtonRow("none",
			larkmsg.Button("确认下单", larkmsg.ButtonOptions{
				Type: "primary",
				Payload: map[string]any{
					cardactionproto.ActionField:         cardactionproto.ActionLuckinOrderConfirm,
					cardactionproto.PendingOrderIDField: order.ID,
					cardactionproto.PayloadHashField:    order.PayloadHash,
				},
			}),
			larkmsg.Button("取消", larkmsg.ButtonOptions{
				Type: "default",
				Payload: map[string]any{
					cardactionproto.ActionField:         cardactionproto.ActionLuckinOrderCancel,
					cardactionproto.PendingOrderIDField: order.ID,
					cardactionproto.PayloadHashField:    order.PayloadHash,
				},
			}),
		),
	)
	return map[string]any(larkmsg.NewCardV2("瑞幸点单", elements, larkmsg.StandardPanelCardV2Options()))
}

// couponSelectForm 渲染可用优惠券多选 + “应用优惠券”按钮，应用后重新预览刷新价格。
func couponSelectForm(order PendingOrder, available, selected []string) map[string]any {
	options := make([]larkmsg.SelectStaticOption, 0, len(available))
	for _, code := range available {
		options = append(options, larkmsg.SelectStaticOption{Text: couponLabel(code), Value: code})
	}
	apply := larkmsg.Button("应用优惠券", larkmsg.ButtonOptions{
		Name:           "luckin_coupon_apply",
		Type:           "default",
		FormActionType: "submit",
		Payload: map[string]any{
			cardactionproto.ActionField: cardactionproto.ActionLuckinCouponApply,
		},
	})
	return map[string]any{
		"tag":                "form",
		"name":               "luckin_coupon_form",
		"vertical_spacing":   "8px",
		"horizontal_spacing": "8px",
		"elements": []any{
			larkmsg.Markdown("🎟 **优惠券**（可多选后点应用）"),
			larkmsg.MultiSelectStatic(cardactionproto.LuckinCouponFormField, larkmsg.MultiSelectStaticOptions{
				Placeholder:    "选择可用优惠券",
				Width:          "fill",
				InitialOptions: selected,
				Options:        options,
			}),
			larkmsg.ButtonRow("none", apply),
		},
	}
}

func couponLabel(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return "优惠券"
	}
	if len(code) <= 12 {
		return "优惠券 " + code
	}
	return "优惠券 " + code[:4] + "…" + code[len(code)-4:]
}

// AvailableCouponsFromPreview 从 previewOrder 返回 data 中提取可用优惠券编码列表。
func AvailableCouponsFromPreview(preview json.RawMessage) []string {
	if len(preview) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(preview, &obj); err != nil {
		return nil
	}
	return stringSlice(obj["couponCodeList"])
}

func selectedCouponsFromPayload(payload json.RawMessage) []string {
	if len(payload) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil
	}
	return stringSlice(obj["couponCodeList"])
}

type previewSummary struct {
	Shop      string
	Products  string
	Price     string
	PriceNote string
	Coupon    string
	AboutTime string
}

func previewSummaryFromOrder(order PendingOrder) previewSummary {
	summary := previewSummary{
		Shop:      "未知门店",
		Products:  "未知商品",
		Price:     "未知价格",
		AboutTime: "未知时间",
	}
	var preview map[string]any
	if err := json.Unmarshal(order.PreviewResult, &preview); err != nil {
		return summary
	}

	if shop, ok := objectValue(preview["shopInfo"]); ok {
		parts := nonEmptyStrings(
			stringValue(shop["deptName"]),
			stringValue(shop["address"]),
			distanceValue(shop["distance"]),
		)
		if len(parts) > 0 {
			summary.Shop = strings.Join(parts, "｜")
		}
	}

	if products, ok := arrayValue(preview["productInfoList"]); ok && len(products) > 0 {
		productTexts := make([]string, 0, len(products))
		for _, item := range products {
			product, ok := objectValue(item)
			if !ok {
				continue
			}
			name := stringValue(product["name"])
			if name == "" {
				name = stringValue(product["productName"])
			}
			if name == "" {
				name = "未知商品"
			}
			amount := numberValue(product["amount"])
			addition := stringValue(product["additionDesc"])
			text := name
			if addition != "" {
				text += "（" + addition + "）"
			}
			if amount != "" {
				text += " x " + amount
			}
			productTexts = append(productTexts, text)
		}
		if len(productTexts) > 0 {
			summary.Products = strings.Join(productTexts, "；")
		}
	}

	selectedCoupons := selectedCouponsFromPayload(order.CreateOrderPayload)
	priceParts := nonEmptyStrings(
		moneyValue("瑞幸预估实付", preview["discountPrice"]),
		moneyValue("商品原价", preview["totalInitialPrice"]),
	)
	if saved := previewDiscountValue(preview); saved != "" {
		priceParts = append(priceParts, "接口优惠/立减 "+saved)
	}
	if len(priceParts) > 0 {
		summary.Price = strings.Join(priceParts, "；")
	}
	if len(selectedCoupons) == 0 {
		summary.PriceNote = "未选择优惠券；瑞幸接口返回的预估实付仍可能包含平台自动优惠。"
	} else {
		summary.PriceNote = fmt.Sprintf("已选择优惠券 %d 张；实付仍以瑞幸创建订单结果为准。", len(selectedCoupons))
	}
	if coupon := couponValue(preview["couponCodeList"]); coupon != "" {
		summary.Coupon = coupon
	}

	if about := timeValue(preview["aboutTime"]); about != "" {
		summary.AboutTime = about
	} else if about := timeValue(preview["expressExpectTime"]); about != "" {
		summary.AboutTime = about
	}
	return summary
}

func objectValue(v any) (map[string]any, bool) {
	obj, ok := v.(map[string]any)
	return obj, ok
}

func arrayValue(v any) ([]any, bool) {
	arr, ok := v.([]any)
	return arr, ok
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return trimFloat(x)
	default:
		return ""
	}
}

func numberValue(v any) string {
	switch x := v.(type) {
	case float64:
		return trimFloat(x)
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

func moneyValue(label string, v any) string {
	if n := numberValue(v); n != "" {
		return label + " ¥" + n
	}
	return ""
}

func previewDiscountValue(preview map[string]any) string {
	for _, key := range []string{"privilegeMoney", "discountMoney", "couponDiscountPrice"} {
		if n := numberValue(preview[key]); n != "" && n != "0" {
			return "¥" + n
		}
	}
	return ""
}

func distanceValue(v any) string {
	if n := numberValue(v); n != "" {
		return n + "km"
	}
	return ""
}

func couponValue(v any) string {
	arr, ok := arrayValue(v)
	if !ok || len(arr) == 0 {
		return ""
	}
	return fmt.Sprintf("优惠券 %d 张", len(arr))
}

func timeValue(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return formatUnixMillis(int64(x))
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return x.String()
		}
		return formatUnixMillis(i)
	default:
		return ""
	}
}

func formatUnixMillis(v int64) string {
	if v <= 0 {
		return ""
	}
	if v > 1_000_000_000_000 {
		return time.UnixMilli(v).Format("2006-01-02 15:04")
	}
	return time.Unix(v, 0).Format("2006-01-02 15:04")
}

func trimFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
