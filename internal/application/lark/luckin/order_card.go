package luckin

import (
	"encoding/json"
	"strconv"
	"strings"

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

	OrderStatusModeReply = "reply"
)

// orderStatusDef 统一描述状态码、展示文案与推断关键词，orderStatusLabel 与
// inferOrderStatus 共用同一份数据，避免双向映射不一致。
type orderStatusDef struct {
	code     int
	label    string
	keywords []string
}

var orderStatusDefinitions = []orderStatusDef{
	{code: OrderStatusUnpaid, label: "待付款", keywords: []string{"待付款", "待支付", "未支付", "支付中"}},
	{code: OrderStatusPlaced, label: "下单成功", keywords: []string{"下单成功", "已下单", "支付成功", "已支付", "已接单"}},
	{code: OrderStatusMaking, label: "制作中", keywords: []string{"制作中", "制作", "备餐中", "备餐", "准备中"}},
	{code: OrderStatusReady, label: "等待取餐", keywords: []string{"等待取餐", "待取餐", "可取餐", "待领取"}},
	{code: OrderStatusCompleted, label: "已完成", keywords: []string{"已完成", "取餐完成", "已取餐", "完成"}},
	{code: OrderStatusCancelled, label: "已取消", keywords: []string{"已取消", "已关闭", "已作废", "已退单"}},
}

// queryOrderDetailInfo 返回的字段名在不同状态下不稳定，这里集中维护候选别名，
// 新增变体时只需追加一处，避免解析逻辑散落在多个 if 分支里。
var (
	orderStatusCodeFieldAliases = []string{
		"orderStatus", "status", "orderState", "state",
		"orderStatusCode", "tradeStatus", "payStatus",
	}
	orderStatusNameFieldAliases = []string{
		"orderStatusName", "statusName", "orderStateName", "stateName",
		"tradeStatusName", "payStatusName", "statusDesc", "orderStatusDesc",
	}
	// 优先取字符串形态的订单号字段：瑞幸 orderId 常为大整数，JSON 解析成 float64 会丢精度，
	// 必须优先用 orderIdStr/orderNo 等字符串字段，仅在没有字符串字段时才回退到数字 orderId。
	orderIDFieldAliases      = []string{"orderIdStr", "orderNo", "orderId"}
	takeMealCodeFieldAliases = []string{"takeMealCode", "mealCode", "pickupCode", "takeCode"}
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
		OrderID:       firstNonEmptyString(obj, orderIDFieldAliases...),
		PayURL:        stringValue(obj["payOrderUrl"]),
		QRCodeURL:     stringValue(obj["payOrderQrCodeUrl"]),
		DiscountPrice: numberFloat(obj["discountPrice"]),
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
		buttons = append(buttons, orderStatusButton(created.OrderID, OrderStatusModeReply))
		elements = append(elements, larkmsg.ButtonRow("none", buttons...))
	} else {
		elements = append(elements,
			larkmsg.Markdown("订单已支付/无需支付。"),
			larkmsg.ButtonRow("none", orderStatusButton(created.OrderID)),
		)
	}
	return wrapCard(elements)
}

func orderStatusButton(orderID string, mode ...string) map[string]any {
	payload := map[string]any{
		cardactionproto.ActionField:        cardactionproto.ActionLuckinOrderStatus,
		cardactionproto.LuckinOrderIDField: orderID,
	}
	if len(mode) > 0 && mode[0] != "" {
		payload[cardactionproto.LuckinStatusModeField] = mode[0]
	}
	return larkmsg.Button("查看订单状态", larkmsg.ButtonOptions{
		Type:    "default",
		Payload: payload,
	})
}

// BuildOrderProcessingCard 操作进行中的过渡卡片。
func BuildOrderProcessingCard(message string) map[string]any {
	return wrapCard([]any{larkmsg.Markdown(message)})
}

// BuildOrderFailedCard 操作失败提示卡片。
func BuildOrderFailedCard(message string) map[string]any {
	elements := []any{
		larkmsg.Markdown("⚠️ " + message),
		larkmsg.HintMarkdown("可以重新结算当前购物车；如果会话已失效，也可以直接重选门店。"),
		larkmsg.ButtonRow("none", larkmsg.Button("重新结算", larkmsg.ButtonOptions{
			Type: "primary",
			Payload: map[string]any{
				cardactionproto.ActionField: cardactionproto.ActionLuckinCartCheckout,
			},
		})),
		larkmsg.Divider(),
		larkmsg.HintMarkdown("重新选择门店："),
	}
	elements = append(elements, shopSearchForm("")...)
	return wrapCard(elements)
}

// OrderDetail 解析 queryOrderDetailInfo 结果，用于状态卡与轮询。
type OrderDetail struct {
	OrderID      string
	Status       int
	StatusName   string
	AboutTime    int64
	TakeMealCode string
	ShopName     string
	ShopAddress  string
	Products     []OrderDetailProduct
}

type OrderDetailProduct struct {
	Name     string
	Amount   int
	Addition string
	ImageURL string
	Price    float64
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
	statusCode := firstPositiveIntValue(obj, orderStatusCodeFieldAliases...)
	statusName := firstNonEmptyString(obj, orderStatusNameFieldAliases...)
	if statusCode == 0 {
		statusCode = inferOrderStatus(statusName)
	}
	if statusName == "" {
		statusName = orderStatusLabel(statusCode)
	}
	detail := OrderDetail{
		OrderID:    firstNonEmptyString(obj, orderIDFieldAliases...),
		Status:     statusCode,
		StatusName: statusName,
		AboutTime:  int64(numberFloat(obj["aboutTime"])),
	}
	if codeInfo, ok := obj["takeMealCodeInfo"].(map[string]any); ok {
		detail.TakeMealCode = stringValue(codeInfo["code"])
	}
	if detail.TakeMealCode == "" {
		detail.TakeMealCode = firstNonEmptyString(obj, takeMealCodeFieldAliases...)
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
				ImageURL: stringValue(pObj["pictureUrl"]),
				Price:    firstPositiveFloat(pObj["estimatePrice"], pObj["discountPrice"], pObj["price"], pObj["initialPrice"]),
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
		elements = append(elements, orderDetailProductRow(p))
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

func orderDetailProductRow(p OrderDetailProduct) map[string]any {
	text := p.Name
	if p.Addition != "" {
		text += "（" + p.Addition + "）"
	}
	if p.Amount > 0 {
		text += " x " + strconv.Itoa(p.Amount)
	}
	info := []any{larkmsg.Markdown("• " + text)}
	if p.Price > 0 {
		info = append(info, larkmsg.HintMarkdown("价格 ¥"+trimFloat(p.Price)))
	}
	return map[string]any{"tag": "column_set", "flex_mode": "stretch", "horizontal_spacing": "12px", "columns": []any{
		map[string]any{"tag": "column", "width": "weighted", "weight": 1, "elements": info},
	}}
}

func firstPositiveFloat(values ...any) float64 {
	for _, value := range values {
		if n := numberFloat(value); n > 0 {
			return n
		}
	}
	return 0
}

// firstNonEmptyString 按候选字段名依次取值，返回第一个非空字符串。
func firstNonEmptyString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := stringValue(obj[key]); v != "" {
			return v
		}
	}
	return ""
}

// firstPositiveIntValue 按候选字段名依次取值，返回第一个正整数。
func firstPositiveIntValue(obj map[string]any, keys ...string) int {
	for _, key := range keys {
		if n := int(numberFloat(obj[key])); n > 0 {
			return n
		}
	}
	return 0
}

func inferOrderStatus(statusName string) int {
	name := strings.TrimSpace(statusName)
	if name == "" {
		return 0
	}
	for _, def := range orderStatusDefinitions {
		for _, kw := range def.keywords {
			if strings.Contains(name, kw) {
				return def.code
			}
		}
	}
	return 0
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

// BuildOrderReadyCard 取餐通知卡。除了基础订单信息外，按购物车快照渲染按人分账：
//
//	@张三   ¥18.50
//	@李四   ¥21.00
//
// paidTotal 通常用瑞幸接口返回的 discountPrice（用户实付）；上层调用方可用 OrderRecord.DiscountPrice。
func BuildOrderReadyCard(notice string, detail OrderDetail, snapshot []CartItem, paidTotal float64) map[string]any {
	card := BuildOrderNoticeCard(notice, detail)
	splits := SplitBillByUser(snapshot, paidTotal)
	if len(splits) == 0 {
		return card
	}
	body, ok := card["body"].(map[string]any)
	if !ok {
		return card
	}
	elements, _ := body["elements"].([]any)
	elements = append(elements, larkmsg.Divider(), larkmsg.Markdown("**🧮 按人分账（实付按预估比例摊销）**"))
	for _, s := range splits {
		line := larkmsg.AtUserMD(s.OpenID) + "  ¥" + trimFloat(s.Paid)
		if s.Estimated > 0 && s.Estimated != s.Paid {
			line += "  <font color='grey'>（预估 ¥" + trimFloat(s.Estimated) + "）</font>"
		}
		elements = append(elements, larkmsg.Markdown(line))
	}
	body["elements"] = elements
	return card
}

// UserBillSplit 单个用户的账单切片。
type UserBillSplit struct {
	OpenID    string
	Estimated float64 // 预估小计（cart_snapshot 内的 unit_price * amount）
	Paid      float64 // 摊销后的实付
}

// SplitBillByUser 按 cart_snapshot 聚合每个加入者的预估小计，并按比例把 paidTotal 摊销给每人。
//
// 退化策略：
//   - paidTotal <= 0：直接退回预估小计（界面提示"实付以瑞幸为准"）；
//   - 任何用户预估都为 0：按数量平分 paidTotal，避免 0/0；
//   - 加入者 OpenID 为空：聚合到 "" 桶，渲染时由调用方决定是否展示。
func SplitBillByUser(items []CartItem, paidTotal float64) []UserBillSplit {
	if len(items) == 0 {
		return nil
	}
	type agg struct {
		openID    string
		estimated float64
		amount    int
	}
	order := make([]string, 0)
	bucket := make(map[string]*agg)
	totalEst := 0.0
	totalAmt := 0
	for _, item := range items {
		if item.Amount <= 0 {
			continue
		}
		key := strings.TrimSpace(item.AddedByOpenID)
		if _, ok := bucket[key]; !ok {
			bucket[key] = &agg{openID: key}
			order = append(order, key)
		}
		sub := item.UnitPrice * float64(item.Amount)
		bucket[key].estimated += sub
		bucket[key].amount += item.Amount
		totalEst += sub
		totalAmt += item.Amount
	}
	out := make([]UserBillSplit, 0, len(order))
	for _, key := range order {
		a := bucket[key]
		split := UserBillSplit{OpenID: a.openID, Estimated: a.estimated}
		switch {
		case paidTotal <= 0:
			split.Paid = a.estimated
		case totalEst > 0:
			split.Paid = paidTotal * a.estimated / totalEst
		case totalAmt > 0:
			split.Paid = paidTotal * float64(a.amount) / float64(totalAmt)
		default:
			split.Paid = 0
		}
		out = append(out, split)
	}
	return out
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
	for _, def := range orderStatusDefinitions {
		if def.code == status {
			return def.label
		}
	}
	return "未知状态"
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
