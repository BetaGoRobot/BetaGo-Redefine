package luckin

import (
	"encoding/json"
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

// 瑞幸订单状态码。
const (
	OrderStatusUnpaid    = 10  // 待付款
	OrderStatusPlaced    = 20  // 下单成功
	OrderStatusMaking    = 30  // 制作中
	OrderStatusReady     = 60  // 等待取餐
	OrderStatusCompleted = 80  // 已完成
	OrderStatusCancelled = 100 // 已取消
)

// IsTerminalOrderStatus 判断是否为终止状态（已完成/已取消），轮询命中即停止。
func IsTerminalOrderStatus(status int) bool {
	return status == OrderStatusCompleted || status == OrderStatusCancelled
}

// OrderCreated 解析 createOrder 返回结果。
type OrderCreated struct {
	OrderID       string
	PayURL        string
	QRCodeURL     string
	DiscountPrice float64
	NeedPay       bool
}

func OrderCreatedFromResult(content json.RawMessage) OrderCreated {
	data := ExtractData(content)
	if len(data) == 0 {
		return OrderCreated{}
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return OrderCreated{}
	}
	created := OrderCreated{
		OrderID:       stringValue(obj["orderIdStr"]),
		PayURL:        stringValue(obj["payOrderUrl"]),
		QRCodeURL:     stringValue(obj["payOrderQrCodeUrl"]),
		DiscountPrice: numberFloat(obj["discountPrice"]),
	}
	if created.OrderID == "" {
		created.OrderID = stringValue(obj["orderId"])
	}
	if needPay, ok := obj["needPay"].(bool); ok {
		created.NeedPay = needPay
	}
	return created
}

// BuildOrderCreatedCard 渲染下单成功卡片：展示金额、二维码图片、微信支付按钮和查看状态按钮。
// qrImgKey 为支付二维码上传飞书后的 img_key，为空则降级为文字链接。
func BuildOrderCreatedCard(content json.RawMessage, qrImgKey string) map[string]any {
	created := OrderCreatedFromResult(content)
	elements := []any{
		larkmsg.Markdown("**✅ 瑞幸订单已创建**"),
	}
	if created.OrderID != "" {
		elements = append(elements, larkmsg.HintMarkdown("订单号："+created.OrderID))
	}
	if created.DiscountPrice > 0 {
		elements = append(elements, larkmsg.Markdown("瑞幸实付：<font color='red'>¥"+trimFloat(created.DiscountPrice)+"</font>"))
	}
	if created.NeedPay {
		elements = append(elements, larkmsg.Markdown("⚠️ 订单待支付，请尽快完成微信支付，否则将自动取消。"))
		if qrImgKey != "" {
			elements = append(elements,
				larkmsg.HintMarkdown("微信扫码支付："),
				map[string]any{"tag": "img", "img_key": qrImgKey, "alt": map[string]any{"tag": "plain_text", "content": "支付二维码"}, "preview": true, "scale_type": "fit_horizontal"},
			)
		}
		buttons := []map[string]any{}
		if created.PayURL != "" {
			buttons = append(buttons, larkmsg.Button("去微信支付", larkmsg.ButtonOptions{Type: "primary", URL: created.PayURL}))
		}
		buttons = append(buttons, orderStatusButton(created.OrderID))
		elements = append(elements, larkmsg.ButtonRow("none", buttons...))
	} else {
		elements = append(elements,
			larkmsg.Markdown("订单已支付/无需支付。"),
			larkmsg.ButtonRow("none", orderStatusButton(created.OrderID)),
		)
	}
	return wrapCard(elements)
}

func orderStatusButton(orderID string) map[string]any {
	return larkmsg.Button("查看订单状态", larkmsg.ButtonOptions{
		Type: "default",
		Payload: map[string]any{
			cardactionproto.ActionField:        cardactionproto.ActionLuckinOrderStatus,
			cardactionproto.LuckinOrderIDField: orderID,
		},
	})
}

// BuildOrderProcessingCard 操作进行中的过渡卡片。
func BuildOrderProcessingCard(message string) map[string]any {
	return wrapCard([]any{larkmsg.Markdown(message)})
}

// BuildOrderFailedCard 操作失败提示卡片。
func BuildOrderFailedCard(message string) map[string]any {
	return wrapCard([]any{larkmsg.Markdown("⚠️ " + message)})
}

// OrderDetail 解析 queryOrderDetailInfo 结果，用于状态卡与轮询。
type OrderDetail struct {
	OrderID        string
	Status         int
	StatusName     string
	AboutTime      int64
	TakeMealCode   string
	ShopName       string
	ShopAddress    string
	Products       []OrderDetailProduct
}

type OrderDetailProduct struct {
	Name    string
	Amount  int
	Addition string
}

func OrderDetailFromResult(content json.RawMessage) OrderDetail {
	data := ExtractData(content)
	if len(data) == 0 {
		return OrderDetail{}
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return OrderDetail{}
	}
	detail := OrderDetail{
		OrderID:    stringValue(obj["orderId"]),
		Status:     int(numberFloat(obj["orderStatus"])),
		StatusName: stringValue(obj["orderStatusName"]),
		AboutTime:  int64(numberFloat(obj["aboutTime"])),
	}
	if codeInfo, ok := obj["takeMealCodeInfo"].(map[string]any); ok {
		detail.TakeMealCode = stringValue(codeInfo["code"])
	}
	if shop, ok := obj["shopInfo"].(map[string]any); ok {
		detail.ShopName = stringValue(shop["deptName"])
		detail.ShopAddress = stringValue(shop["address"])
	}
	if products, ok := obj["productInfoList"].([]any); ok {
		for _, p := range products {
			pObj, ok := p.(map[string]any)
			if !ok {
				continue
			}
			detail.Products = append(detail.Products, OrderDetailProduct{
				Name:     stringValue(pObj["name"]),
				Amount:   int(numberFloat(pObj["amount"])),
				Addition: stringValue(pObj["additionDesc"]),
			})
		}
	}
	return detail
}

// BuildOrderStatusCard 渲染订单状态卡片，展示状态、取餐码、预计取餐时间、门店和商品。
func BuildOrderStatusCard(detail OrderDetail) map[string]any {
	statusName := detail.StatusName
	if statusName == "" {
		statusName = orderStatusLabel(detail.Status)
	}
	elements := []any{
		larkmsg.Markdown("**瑞幸订单状态：" + statusName + "**"),
	}
	if detail.OrderID != "" {
		elements = append(elements, larkmsg.HintMarkdown("订单号："+detail.OrderID))
	}
	if detail.ShopName != "" {
		line := "📍 " + detail.ShopName
		if detail.ShopAddress != "" {
			line += "（" + detail.ShopAddress + "）"
		}
		elements = append(elements, larkmsg.HintMarkdown(line))
	}
	for _, p := range detail.Products {
		text := p.Name
		if p.Addition != "" {
			text += "（" + p.Addition + "）"
		}
		if p.Amount > 0 {
			text += " x " + strconv.Itoa(p.Amount)
		}
		elements = append(elements, larkmsg.Markdown("• "+text))
	}
	if detail.TakeMealCode != "" {
		elements = append(elements, larkmsg.Markdown("🥤 取餐码：**"+detail.TakeMealCode+"**"))
	}
	if about := formatUnixMillis(detail.AboutTime); about != "" {
		elements = append(elements, larkmsg.HintMarkdown("预计取餐/送达："+about))
	}
	if !IsTerminalOrderStatus(detail.Status) {
		elements = append(elements, larkmsg.ButtonRow("none", orderStatusButton(detail.OrderID)))
	}
	return wrapCard(elements)
}

// BuildOrderNoticeCard 用于轮询节点主动通知（如制作中/等待取餐/已完成/已取消）。
func BuildOrderNoticeCard(notice string, detail OrderDetail) map[string]any {
	card := BuildOrderStatusCard(detail)
	if body, ok := card["body"].(map[string]any); ok {
		if elements, ok := body["elements"].([]any); ok {
			body["elements"] = append([]any{larkmsg.Markdown("**" + notice + "**")}, elements...)
		}
	}
	return card
}

// BuildUnpaidReminderCard 未支付阈值提醒卡。
func BuildUnpaidReminderCard(orderID string, payURL string) map[string]any {
	elements := []any{
		larkmsg.Markdown("**⏰ 订单还未支付**"),
		larkmsg.HintMarkdown("订单号：" + orderID),
		larkmsg.Markdown("请尽快完成支付，否则订单将自动取消。"),
	}
	buttons := []map[string]any{}
	if payURL != "" {
		buttons = append(buttons, larkmsg.Button("去微信支付", larkmsg.ButtonOptions{Type: "primary", URL: payURL}))
	}
	buttons = append(buttons, orderStatusButton(orderID))
	elements = append(elements, larkmsg.ButtonRow("none", buttons...))
	return wrapCard(elements)
}

func orderStatusLabel(status int) string {
	switch status {
	case OrderStatusUnpaid:
		return "待付款"
	case OrderStatusPlaced:
		return "下单成功"
	case OrderStatusMaking:
		return "制作中"
	case OrderStatusReady:
		return "等待取餐"
	case OrderStatusCompleted:
		return "已完成"
	case OrderStatusCancelled:
		return "已取消"
	default:
		return "未知状态"
	}
}

// OrderStatusNotice 返回某状态对应的节点通知文案；返回空表示该状态不单独通知。
func OrderStatusNotice(status int) string {
	switch status {
	case OrderStatusPlaced:
		return "☕ 下单成功"
	case OrderStatusMaking:
		return "👨‍🍳 正在制作中"
	case OrderStatusReady:
		return "🛎 可以取餐啦"
	case OrderStatusCompleted:
		return "✅ 订单已完成"
	case OrderStatusCancelled:
		return "❌ 订单已取消"
	default:
		return ""
	}
}
