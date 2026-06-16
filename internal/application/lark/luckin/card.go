package luckin

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"body": map[string]any{
			"elements": []any{
				map[string]any{"tag": "markdown", "content": "**瑞幸订单确认**"},
				map[string]any{"tag": "markdown", "content": "账号作用域：" + ScopeLabel(order.CredentialScope)},
				map[string]any{"tag": "markdown", "content": "门店：" + summary.Shop},
				map[string]any{"tag": "markdown", "content": "商品：" + summary.Products},
				map[string]any{"tag": "markdown", "content": "价格优惠：" + summary.Price},
				map[string]any{"tag": "markdown", "content": "预计取餐/送达：" + summary.AboutTime},
				map[string]any{"tag": "markdown", "content": "点击确认将创建瑞幸订单，但不会自动支付。"},
				map[string]any{
					"tag":  "button",
					"text": map[string]any{"tag": "plain_text", "content": "确认下单"},
					"type": "primary",
					"behaviors": []any{map[string]any{
						"type": "callback",
						"value": map[string]any{
							cardactionproto.ActionField:         cardactionproto.ActionLuckinOrderConfirm,
							cardactionproto.PendingOrderIDField: order.ID,
							cardactionproto.PayloadHashField:    order.PayloadHash,
						},
					}},
				},
				map[string]any{
					"tag":  "button",
					"text": map[string]any{"tag": "plain_text", "content": "取消"},
					"type": "default",
					"behaviors": []any{map[string]any{
						"type": "callback",
						"value": map[string]any{
							cardactionproto.ActionField:         cardactionproto.ActionLuckinOrderCancel,
							cardactionproto.PendingOrderIDField: order.ID,
						},
					}},
				},
			},
		},
	}
}

type previewSummary struct {
	Shop      string
	Products  string
	Price     string
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

	priceParts := nonEmptyStrings(
		moneyValue("实付", preview["discountPrice"]),
		moneyValue("原价", preview["totalInitialPrice"]),
		couponValue(preview["couponCodeList"]),
	)
	if len(priceParts) > 0 {
		summary.Price = strings.Join(priceParts, "；")
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
