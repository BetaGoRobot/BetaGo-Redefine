package luckin

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

type ShopOption struct {
	DeptID    int64
	DeptName  string
	Address   string
	Distance  float64
	Longitude float64
	Latitude  float64
}

type ProductOption struct {
	ProductID   int64
	SkuCode     string
	ProductName string
	Price       float64
}

func BuildShopSelectCard(keyword string, shops []ShopOption) map[string]any {
	elements := []any{
		map[string]any{"tag": "markdown", "content": "**选择瑞幸门店**"},
		map[string]any{"tag": "markdown", "content": "关键词：" + keyword},
	}
	if len(shops) == 0 {
		elements = append(elements, map[string]any{"tag": "markdown", "content": "没有找到匹配门店，换个关键词试试。"})
		return wrapCard(elements)
	}
	for _, shop := range shops {
		label := shop.DeptName
		desc := strings.TrimSpace(shop.Address)
		if desc != "" {
			label += "｜" + desc
		}
		elements = append(elements,
			map[string]any{"tag": "markdown", "content": label},
			map[string]any{
				"tag":  "button",
				"text": map[string]any{"tag": "plain_text", "content": "选这家"},
				"type": "primary",
				"behaviors": []any{map[string]any{
					"type": "callback",
					"value": map[string]any{
						cardactionproto.ActionField:        cardactionproto.ActionLuckinShopSelect,
						cardactionproto.LuckinDeptIDField:   strconv.FormatInt(shop.DeptID, 10),
						cardactionproto.LuckinDeptNameField: shop.DeptName,
						cardactionproto.LuckinLongitudeField: strconv.FormatFloat(shop.Longitude, 'f', -1, 64),
						cardactionproto.LuckinLatitudeField:  strconv.FormatFloat(shop.Latitude, 'f', -1, 64),
					},
				}},
			},
		)
	}
	return wrapCard(elements)
}

func BuildProductSelectCard(shop ShopSelection, products []ProductOption) map[string]any {
	header := []any{
		map[string]any{"tag": "markdown", "content": "**已选门店：" + shop.DeptName + "**"},
	}
	if len(products) == 0 {
		header = append(header, map[string]any{"tag": "markdown", "content": "没有找到匹配商品，换个关键词再搜。"})
		return wrapCard(append(header, productQueryForm(shop)...))
	}
	header = append(header, map[string]any{"tag": "markdown", "content": "选择商品："})
	for _, product := range products {
		label := product.ProductName
		if product.Price > 0 {
			label += "｜¥" + strconv.FormatFloat(product.Price, 'f', -1, 64)
		}
		header = append(header,
			map[string]any{"tag": "markdown", "content": label},
			map[string]any{
				"tag":  "button",
				"text": map[string]any{"tag": "plain_text", "content": "下这杯"},
				"type": "primary",
				"behaviors": []any{map[string]any{
					"type": "callback",
					"value": map[string]any{
						cardactionproto.ActionField:          cardactionproto.ActionLuckinProductSelect,
						cardactionproto.LuckinProductIDField: strconv.FormatInt(product.ProductID, 10),
						cardactionproto.LuckinSkuCodeField:   product.SkuCode,
						cardactionproto.LuckinProductName:    product.ProductName,
					},
				}},
			},
		)
	}
	header = append(header, map[string]any{"tag": "hr"})
	return wrapCard(append(header, productQueryForm(shop)...))
}

// BuildProductQueryCard 在用户选定门店后展示，提供商品搜索输入框，整条动线在卡片内完成。
func BuildProductQueryCard(shop ShopSelection) map[string]any {
	elements := []any{
		map[string]any{"tag": "markdown", "content": "**已选门店：" + shop.DeptName + "**"},
		map[string]any{"tag": "markdown", "content": "想喝点什么？输入商品关键词搜索。"},
	}
	elements = append(elements, productQueryForm(shop)...)
	return wrapCard(elements)
}

// productQueryForm 返回一个商品搜索表单（输入框 + 搜索按钮），提交后在卡片内刷新商品列表。
func productQueryForm(shop ShopSelection) []any {
	submit := larkmsg.Button("搜索商品", larkmsg.ButtonOptions{
		Name:           "luckin_product_query_submit",
		Type:           "primary",
		FormActionType: "submit",
		Payload: map[string]any{
			cardactionproto.ActionField: cardactionproto.ActionLuckinProductQuery,
		},
	})
	return []any{
		map[string]any{
			"tag":                "form",
			"name":               "luckin_product_query_form",
			"vertical_spacing":   "8px",
			"horizontal_spacing": "8px",
			"elements": []any{
				larkmsg.TextInput(cardactionproto.LuckinQueryFormField, larkmsg.TextInputOptions{
					Placeholder: "例如：生椰拿铁",
				}),
				larkmsg.ButtonRow("none", submit),
			},
		},
	}
}

func BuildBindTokenCard(chatType ChatType) map[string]any {
	formElements := []any{
		map[string]any{"tag": "markdown", "content": "**绑定瑞幸账号**"},
		map[string]any{"tag": "markdown", "content": "请到 [瑞幸开放平台](https://open.lkcoffee.com) 登录后复制 Token，粘贴到下方完成绑定。Token 仅加密保存，机器人不会展示完整内容。"},
		larkmsg.TextInput(cardactionproto.LuckinTokenFormField, larkmsg.TextInputOptions{
			Placeholder: "粘贴 Bearer Token",
		}),
	}
	if chatType == ChatTypeGroup {
		formElements = append(formElements, larkmsg.SelectStatic(cardactionproto.LuckinScopeFormField, larkmsg.SelectStaticOptions{
			Placeholder: "选择作用域",
			Width:       "fill",
			Options: []larkmsg.SelectStaticOption{
				{Text: "仅个人使用", Value: string(ScopePersonal)},
				{Text: "本群默认", Value: string(ScopeChat)},
			},
		}))
	}
	submit := larkmsg.Button("提交绑定", larkmsg.ButtonOptions{
		Name:           "luckin_bind_submit",
		Type:           "primary",
		FormActionType: "submit",
		Payload: map[string]any{
			cardactionproto.ActionField: cardactionproto.ActionLuckinBindToken,
		},
	})
	formElements = append(formElements, larkmsg.ButtonRow("none", submit))
	return wrapCard([]any{
		map[string]any{
			"tag":                "form",
			"name":               "luckin_bind_form",
			"vertical_spacing":   "8px",
			"horizontal_spacing": "8px",
			"elements":           formElements,
		},
	})
}

func wrapCard(elements []any) map[string]any {
	return map[string]any(larkmsg.NewCardV2("瑞幸点单", elements, larkmsg.StandardPanelCardV2Options()))
}

func ShopOptionsFromResult(content json.RawMessage, limit int) []ShopOption {
	items := dataArray(content)
	out := make([]ShopOption, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		deptID := int64(numberFloat(obj["deptId"]))
		if deptID == 0 {
			continue
		}
		out = append(out, ShopOption{
			DeptID:    deptID,
			DeptName:  stringValue(obj["deptName"]),
			Address:   stringValue(obj["address"]),
			Distance:  numberFloat(obj["distance"]),
			Longitude: numberFloat(obj["longitude"]),
			Latitude:  numberFloat(obj["latitude"]),
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func ProductOptionsFromResult(content json.RawMessage, limit int) []ProductOption {
	items := dataArray(content)
	out := make([]ProductOption, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		productID := int64(numberFloat(obj["productId"]))
		if productID == 0 {
			continue
		}
		name := stringValue(obj["productName"])
		if name == "" {
			name = stringValue(obj["name"])
		}
		price := numberFloat(obj["estimatePrice"])
		if price == 0 {
			price = numberFloat(obj["initialPrice"])
		}
		out = append(out, ProductOption{
			ProductID:   productID,
			SkuCode:     stringValue(obj["skuCode"]),
			ProductName: name,
			Price:       price,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func numberFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		return 0
	}
}

// ExtractData 解析 MCP 工具返回内容，取出业务 data 字段。
// MCP 内容形如 [{"type":"text","text":"{...}"}]，text 内是瑞幸 {code,msg,data} 包裹。
func ExtractData(content json.RawMessage) json.RawMessage {
	text := mcpTextPayload(content)
	if len(text) == 0 {
		return nil
	}
	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(text, &wrapper); err != nil {
		return nil
	}
	return wrapper.Data
}

func dataArray(content json.RawMessage) []any {
	data := ExtractData(content)
	if len(data) == 0 {
		return nil
	}
	var arr []any
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil
	}
	return arr
}

func mcpTextPayload(content json.RawMessage) json.RawMessage {
	if len(content) == 0 {
		return nil
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &items); err == nil {
		for _, item := range items {
			if strings.TrimSpace(item.Text) != "" {
				return json.RawMessage(item.Text)
			}
		}
		return nil
	}
	// content 本身可能就是对象（非 MCP 包裹），直接返回。
	return content
}
