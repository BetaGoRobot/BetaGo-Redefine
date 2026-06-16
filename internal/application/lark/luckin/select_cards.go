package luckin

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

type ShopOption struct {
	DeptID        int64
	DeptName      string
	Address       string
	Distance      float64
	Longitude     float64
	Latitude      float64
	WorkTimeStart string
	WorkTimeEnd   string
	Tags          []string
}

type ProductOption struct {
	ProductID   int64
	SkuCode     string
	ProductName string
	PictureURL  string
	Price       float64
	InitialPice float64
	Tags        []string
}

func BuildShopSelectCard(keyword string, shops []ShopOption) map[string]any {
	elements := []any{
		larkmsg.Markdown("**选择瑞幸门店**"),
		larkmsg.HintMarkdown("位置：" + keyword),
	}
	if len(shops) == 0 {
		elements = append(elements, larkmsg.Markdown("没有找到附近门店，换个更具体的位置试试。"))
		return wrapCard(elements)
	}
	for i, shop := range shops {
		if i > 0 {
			elements = append(elements, larkmsg.Divider())
		}
		title := "**" + shop.DeptName + "**"
		if shop.Distance > 0 {
			title += "  ·  " + strconv.FormatFloat(shop.Distance, 'f', 1, 64) + "km"
		}
		elements = append(elements, larkmsg.Markdown(title))
		if addr := strings.TrimSpace(shop.Address); addr != "" {
			elements = append(elements, larkmsg.HintMarkdown("📍 "+addr))
		}
		if meta := shopMetaLine(shop); meta != "" {
			elements = append(elements, larkmsg.HintMarkdown(meta))
		}
		elements = append(elements, larkmsg.ButtonRow("none", larkmsg.Button("选这家", larkmsg.ButtonOptions{
			Type: "primary",
			Payload: map[string]any{
				cardactionproto.ActionField:          cardactionproto.ActionLuckinShopSelect,
				cardactionproto.LuckinDeptIDField:    strconv.FormatInt(shop.DeptID, 10),
				cardactionproto.LuckinDeptNameField:  shop.DeptName,
				cardactionproto.LuckinLongitudeField: strconv.FormatFloat(shop.Longitude, 'f', -1, 64),
				cardactionproto.LuckinLatitudeField:  strconv.FormatFloat(shop.Latitude, 'f', -1, 64),
			},
		})))
	}
	return wrapCard(elements)
}

func shopMetaLine(shop ShopOption) string {
	parts := make([]string, 0, 2)
	if shop.WorkTimeStart != "" && shop.WorkTimeEnd != "" {
		parts = append(parts, "🕐 "+shop.WorkTimeStart+"-"+shop.WorkTimeEnd)
	}
	if len(shop.Tags) > 0 {
		parts = append(parts, "🏷 "+strings.Join(shop.Tags, "/"))
	}
	return strings.Join(parts, "    ")
}

// BuildProductSelectCard 渲染商品搜索结果，每个商品左图右文，提供数量输入与“加入购物车”按钮。
// 每个商品独立成一个 form 以便提交各自的数量；imageKeys 为 productID->img_key。
func BuildProductSelectCard(shop ShopSelection, products []ProductOption, imageKeys map[int64]string) map[string]any {
	header := []any{
		larkmsg.Markdown("**已选门店：" + shop.DeptName + "**"),
	}
	if len(products) == 0 {
		header = append(header, larkmsg.Markdown("没有找到匹配商品，换个关键词再搜。"))
		return wrapCard(append(header, productQueryForm(shop)...))
	}
	header = append(header, larkmsg.HintMarkdown("选择数量后加入购物车，可继续搜索其它商品："))

	body := make([]any, 0, len(products)*2+2)
	for _, product := range products {
		body = append(body, larkmsg.Divider(), productRow(product, imageKeys[product.ProductID]))
	}
	tail := append(body, larkmsg.Divider())
	tail = append(tail, productQueryForm(shop)...)
	return wrapCard(append(header, tail...))
}

func productRow(product ProductOption, imgKey string) map[string]any {
	idValue := strconv.FormatInt(product.ProductID, 10)
	info := []any{larkmsg.Markdown("**" + product.ProductName + "**")}
	if priceLine := productPriceLine(product); priceLine != "" {
		info = append(info, larkmsg.Markdown(priceLine))
	}
	if len(product.Tags) > 0 {
		info = append(info, larkmsg.HintMarkdown(strings.Join(product.Tags, " · ")))
	}
	info = append(info, larkmsg.TextInput(cardactionproto.LuckinQtyFormField, larkmsg.TextInputOptions{
		Placeholder:  "数量（默认 1）",
		DefaultValue: "1",
	}))
	info = append(info, larkmsg.ButtonRow("none", larkmsg.Button("加入购物车", larkmsg.ButtonOptions{
		Type:           "primary",
		Name:           "luckin_select_" + idValue,
		FormActionType: "submit",
		Payload: map[string]any{
			cardactionproto.ActionField:          cardactionproto.ActionLuckinProductSelect,
			cardactionproto.LuckinProductIDField: idValue,
			cardactionproto.LuckinSkuCodeField:   product.SkuCode,
			cardactionproto.LuckinProductName:    product.ProductName,
			cardactionproto.LuckinUnitPriceField: strconv.FormatFloat(productUnitPrice(product), 'f', -1, 64),
		},
	})))

	var rowBody map[string]any
	if imgKey == "" {
		rowBody = map[string]any{"tag": "column_set", "flex_mode": "stretch", "horizontal_spacing": "12px", "columns": []any{
			map[string]any{"tag": "column", "width": "weighted", "weight": 1, "elements": info},
		}}
	} else {
		rowBody = map[string]any{
			"tag":                "column_set",
			"flex_mode":          "stretch",
			"horizontal_spacing": "12px",
			"columns": []any{
				map[string]any{"tag": "column", "width": "auto", "vertical_align": "center", "elements": []any{
					map[string]any{"tag": "img", "img_key": imgKey, "alt": map[string]any{"tag": "plain_text", "content": product.ProductName}, "preview": true, "scale_type": "crop_center", "size": "medium"},
				}},
				map[string]any{"tag": "column", "width": "weighted", "weight": 1, "vertical_align": "center", "elements": info},
			},
		}
	}
	// form 必须是卡片根级元素，因此让 form 包住整行 column_set，
	// 数量输入与提交按钮作为 form 的后代被一并提交。
	return map[string]any{
		"tag":                "form",
		"name":               "luckin_add_form_" + idValue,
		"vertical_spacing":   "8px",
		"horizontal_spacing": "8px",
		"elements":           []any{rowBody},
	}
}

func productPriceLine(product ProductOption) string {
	if product.Price <= 0 && product.InitialPice <= 0 {
		return ""
	}
	if product.Price > 0 && product.InitialPice > product.Price {
		return "<font color='red'>¥" + trimFloat(product.Price) + "</font>  <font color='grey'>~~¥" + trimFloat(product.InitialPice) + "~~</font>"
	}
	price := productUnitPrice(product)
	return "<font color='red'>¥" + trimFloat(price) + "</font>"
}

func productUnitPrice(product ProductOption) float64 {
	if product.Price > 0 {
		return product.Price
	}
	return product.InitialPice
}

// BuildProductQueryCard 在用户选定门店后展示，提供商品搜索输入框，整条动线在卡片内完成。
func BuildProductQueryCard(shop ShopSelection) map[string]any {
	elements := []any{
		larkmsg.Markdown("**已选门店：" + shop.DeptName + "**"),
		larkmsg.HintMarkdown("想喝点什么？输入商品关键词搜索。"),
	}
	elements = append(elements, productQueryForm(shop)...)
	return wrapCard(elements)
}

// BuildProductSearchingCard 在异步搜索商品期间展示的过渡卡片。
func BuildProductSearchingCard(shop ShopSelection, query string) map[string]any {
	return wrapCard([]any{
		larkmsg.Markdown("**已选门店：" + shop.DeptName + "**"),
		larkmsg.HintMarkdown("正在搜索「" + query + "」，请稍候…"),
	})
}

// BuildProductSearchErrorCard 在异步搜索失败时展示，并保留搜索表单方便重试。
func BuildProductSearchErrorCard(shop ShopSelection, query string) map[string]any {
	elements := []any{
		larkmsg.Markdown("**已选门店：" + shop.DeptName + "**"),
		larkmsg.Markdown("搜索「" + query + "」失败，请重试或换个关键词。"),
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
		larkmsg.Markdown("**绑定瑞幸账号**"),
		larkmsg.Markdown("请到 [瑞幸开放平台](https://open.lkcoffee.com) 登录后复制 Token，粘贴到下方完成绑定。Token 仅加密保存，机器人不会展示完整内容。"),
		larkmsg.HintMarkdown("出于优惠券归属与隐私考虑，Token 仅按个人绑定，仅你自己可用。"),
		larkmsg.TextInput(cardactionproto.LuckinTokenFormField, larkmsg.TextInputOptions{
			Placeholder: "粘贴 Bearer Token",
		}),
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
			DeptID:        deptID,
			DeptName:      stringValue(obj["deptName"]),
			Address:       stringValue(obj["address"]),
			Distance:      numberFloat(obj["distance"]),
			Longitude:     numberFloat(obj["longitude"]),
			Latitude:      numberFloat(obj["latitude"]),
			WorkTimeStart: stringValue(obj["workTimeStart"]),
			WorkTimeEnd:   stringValue(obj["workTimeEnd"]),
			Tags:          stringSlice(obj["deptTags"]),
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
		out = append(out, ProductOption{
			ProductID:   productID,
			SkuCode:     stringValue(obj["skuCode"]),
			ProductName: name,
			PictureURL:  stringValue(obj["pictureUrl"]),
			Price:       numberFloat(obj["estimatePrice"]),
			InitialPice: numberFloat(obj["initialPrice"]),
			Tags:        stringSlice(obj["tags"]),
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s := strings.TrimSpace(stringValue(item)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// UploadProductImages 上传商品图片并返回 productID->img_key 映射，失败的商品降级为纯文字。
func UploadProductImages(ctx context.Context, uploader ImageUploader, products []ProductOption) map[int64]string {
	if uploader == nil {
		return nil
	}
	out := make(map[int64]string, len(products))
	for _, p := range products {
		if p.PictureURL == "" {
			continue
		}
		if key := uploader.UploadByURL(ctx, p.PictureURL); key != "" {
			out[p.ProductID] = key
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
