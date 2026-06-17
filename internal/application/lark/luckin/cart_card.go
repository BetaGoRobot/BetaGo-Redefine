package luckin

import (
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

// BuildCartCard 渲染会话购物车：已选门店、购物车条目（含数量调整/删除）、继续搜索表单与去结算按钮。
func BuildCartCard(shop ShopSelection, cart Cart) map[string]any {
	elements := cartElements(shop, cart)
	elements = append(elements, larkmsg.Divider(), larkmsg.HintMarkdown("继续添加商品："))
	elements = append(elements, productQueryForm(shop)...)
	return wrapCard(elements)
}

func cartElements(shop ShopSelection, cart Cart) []any {
	elements := []any{
		larkmsg.Markdown("**🛒 已选门店：" + shop.DeptName + "**"),
	}
	if cart.Empty() {
		elements = append(elements, larkmsg.HintMarkdown("购物车还是空的，搜索商品加入吧。"))
		return elements
	}

	elements = append(elements, larkmsg.HintMarkdown("共 "+strconv.Itoa(cart.TotalAmount())+" 件，预估 ¥"+trimFloat(cart.EstimatedTotal())+"（实付以结算为准）"))
	for _, item := range cart.Items {
		elements = append(elements, larkmsg.Divider(), cartItemRow(item))
	}
	elements = append(elements, larkmsg.Divider())
	elements = append(elements, larkmsg.ButtonRow("none",
		larkmsg.Button("去结算", larkmsg.ButtonOptions{
			Type: "primary",
			Payload: map[string]any{
				cardactionproto.ActionField: cardactionproto.ActionLuckinCartCheckout,
			},
			}),
		))
	return elements
}

func cartItemRow(item CartItem) map[string]any {
	title := "**" + item.ProductName + "**  x " + strconv.Itoa(item.Amount)
	info := []any{larkmsg.Markdown(title)}
	if item.UnitPrice > 0 {
		info = append(info, larkmsg.HintMarkdown("预估到手 ¥"+trimFloat(item.UnitPrice)+" / 杯"))
	}
	idValue := strconv.FormatInt(item.ProductID, 10)
	controls := []any{larkmsg.ButtonRow("none",
		larkmsg.Button("－", larkmsg.ButtonOptions{
			Type: "default",
			Payload: map[string]any{
				cardactionproto.ActionField:          cardactionproto.ActionLuckinCartUpdate,
				cardactionproto.LuckinProductIDField: idValue,
				cardactionproto.LuckinSkuCodeField:   item.SkuCode,
				cardactionproto.LuckinQtyFormField:   strconv.Itoa(item.Amount - 1),
			},
		}),
		larkmsg.Button("＋", larkmsg.ButtonOptions{
			Type: "default",
			Payload: map[string]any{
				cardactionproto.ActionField:          cardactionproto.ActionLuckinCartUpdate,
				cardactionproto.LuckinProductIDField: idValue,
				cardactionproto.LuckinSkuCodeField:   item.SkuCode,
				cardactionproto.LuckinQtyFormField:   strconv.Itoa(item.Amount + 1),
			},
		}),
		larkmsg.Button("删除", larkmsg.ButtonOptions{
			Type: "danger",
			Payload: map[string]any{
				cardactionproto.ActionField:          cardactionproto.ActionLuckinCartRemove,
				cardactionproto.LuckinProductIDField: idValue,
				cardactionproto.LuckinSkuCodeField:   item.SkuCode,
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
		larkmsg.HintMarkdown("正在为你预览订单价格与优惠券，请稍候…"),
	})
}
