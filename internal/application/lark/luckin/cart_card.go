package luckin

import (
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

// BuildCartCard 渲染会话购物车：已选门店、购物车条目（含加入者标识、数量调整/删除）、继续搜索表单与去结算按钮。
func BuildCartCard(shop ShopSelection, cart Cart, mode CheckoutMode) map[string]any {
	elements := cartElements(shop, cart, mode)
	elements = append(elements, larkmsg.Divider(), larkmsg.HintMarkdown("继续添加商品："))
	elements = append(elements, productQueryForm(shop)...)
	return wrapCard(elements)
}

func cartElements(shop ShopSelection, cart Cart, mode CheckoutMode) []any {
	mode = NormalizeCheckoutMode(string(mode))
	elements := []any{
		larkmsg.Markdown("**🛒 已选门店：" + shop.DeptName + "**"),
	}
	if cart.Empty() {
		elements = append(elements, larkmsg.HintMarkdown("购物车还是空的，搜索商品加入吧。"))
		return elements
	}

	elements = append(elements, larkmsg.HintMarkdown("共 "+strconv.Itoa(cart.TotalAmount())+" 件，预估 ¥"+trimFloat(cart.EstimatedTotal())+"（实付以结算为准）"))
	elements = append(elements, checkoutModeForm(mode))
	for _, item := range cart.Items {
		elements = append(elements, larkmsg.Divider(), cartItemRow(item))
	}
	elements = append(elements, larkmsg.Divider())
	return elements
}

func checkoutModeForm(mode CheckoutMode) map[string]any {
	options := []larkmsg.SelectStaticOption{
		{Text: "统一下单", Value: string(CheckoutModeInitiatorUnified)},
		{Text: "自我下单", Value: string(CheckoutModeSelfService)},
	}
	description := "统一下单：用发起人账号统一结算，只有发起人可以继续操作。"
	if mode == CheckoutModeSelfService {
		description = "自我下单：结算者只下自己加入的商品，任何人都能各自完成自己的订单。"
	}
	checkoutSubmit := larkmsg.Button("去结算（仅发起人可点）", larkmsg.ButtonOptions{
		Name:           "luckin_checkout_submit",
		Type:           "primary",
		FormActionType: "submit",
		Payload: map[string]any{
			cardactionproto.ActionField: cardactionproto.ActionLuckinCartCheckout,
		},
	})
	return map[string]any{
		"tag":                "form",
		"name":               "luckin_checkout_mode_form",
		"vertical_spacing":   "8px",
		"horizontal_spacing": "8px",
		"elements": []any{
			larkmsg.Markdown("🧾 **结算模式**"),
			larkmsg.SelectStatic(cardactionproto.LuckinCheckoutModeField, larkmsg.SelectStaticOptions{
				Placeholder:   "选择结算模式",
				Width:         "fill",
				InitialOption: string(mode),
				Options:       options,
			}),
			larkmsg.HintMarkdown(description),
			larkmsg.ButtonRow("none", checkoutSubmit),
		},
	}
}

// cartItemRow 渲染购物车单行。
//
// 关键点：
//   - 标题中带 <at> 标识"由谁加入"，方便分账场景一眼看清；
//   - +/- / 删除按钮 payload 用 LineID，避免不同人加入同 SKU 时互相覆盖；
//   - 是否真的允许操作由 handler 端按 CanModifyLine 校验，按钮本身对所有人可见。
func cartItemRow(item CartItem) map[string]any {
	title := "**" + item.ProductName + "**  x " + strconv.Itoa(item.Amount)
	if openID := item.AddedByOpenID; openID != "" {
		title += "  · 由 " + larkmsg.AtUserMD(openID)
	}
	info := []any{larkmsg.Markdown(title)}
	if item.UnitPrice > 0 {
		info = append(info, larkmsg.HintMarkdown("预估到手 ¥"+trimFloat(item.UnitPrice)+" / 杯"))
	}
	controls := []any{larkmsg.ButtonRow("none",
		larkmsg.Button("－", larkmsg.ButtonOptions{
			Type: "default",
			Payload: map[string]any{
				cardactionproto.ActionField:        cardactionproto.ActionLuckinCartUpdate,
				cardactionproto.LuckinLineIDField:  item.LineID,
				cardactionproto.LuckinQtyFormField: strconv.Itoa(item.Amount - 1),
			},
		}),
		larkmsg.Button("＋", larkmsg.ButtonOptions{
			Type: "default",
			Payload: map[string]any{
				cardactionproto.ActionField:        cardactionproto.ActionLuckinCartUpdate,
				cardactionproto.LuckinLineIDField:  item.LineID,
				cardactionproto.LuckinQtyFormField: strconv.Itoa(item.Amount + 1),
			},
		}),
		larkmsg.Button("删除", larkmsg.ButtonOptions{
			Type: "danger",
			Payload: map[string]any{
				cardactionproto.ActionField:       cardactionproto.ActionLuckinCartRemove,
				cardactionproto.LuckinLineIDField: item.LineID,
			},
		}),
	)}
	columns := []any{}
	if image := productImageElement(item.ImageKey, item.ProductName); image != nil {
		columns = append(columns, map[string]any{"tag": "column", "width": "auto", "vertical_align": "center", "elements": []any{image}})
	}
	columns = append(columns,
		map[string]any{"tag": "column", "width": "weighted", "weight": 3, "vertical_align": "center", "elements": info},
		map[string]any{"tag": "column", "width": "weighted", "weight": 2, "vertical_align": "center", "elements": controls},
	)
	return map[string]any{"tag": "column_set", "flex_mode": "stretch", "horizontal_spacing": "12px", "columns": columns}
}

// BuildCartCheckoutProcessingCard 结算预览期间的过渡卡片。
func BuildCartCheckoutProcessingCard(shop ShopSelection) map[string]any {
	return wrapCard([]any{
		larkmsg.Markdown("**🛒 已选门店：" + shop.DeptName + "**"),
		larkmsg.HintMarkdown("正在按当前结算模式拆单预览订单价格与优惠券，请稍候…"),
	})
}
