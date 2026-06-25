package luckin

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

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

const shopSearchRegionLimit = 100

var (
	shopSearchProvinceOptionsOnce sync.Once
	shopSearchProvinceOptionsData []larkmsg.SelectStaticOption
	shopSearchRegionOptionsMu     sync.Mutex
	shopSearchRegionOptionsCache  = map[string][]larkmsg.SelectStaticOption{}
)

func BuildShopSelectCard(keyword string, shops []ShopOption) map[string]any {
	elements := []any{
		larkmsg.Markdown("**选择瑞幸门店**"),
		larkmsg.HintMarkdown("位置：" + keyword),
	}
	if len(shops) == 0 {
		elements = append(elements, larkmsg.Markdown("没有找到附近门店，换个更具体的位置再搜。"))
		elements = append(elements, shopSearchForm("")...)
		return wrapCard(elements)
	}
	for i, shop := range shops {
		if i > 0 {
			elements = append(elements, larkmsg.Divider())
		}
		elements = append(elements, shopOptionRow(shop))
	}
	elements = append(elements, larkmsg.Divider(), larkmsg.HintMarkdown("不是这些？换个位置重新搜："))
	elements = append(elements, shopSearchForm("")...)
	return wrapCard(elements)
}

// BuildSessionExpiredCard 保留无最近门店的兼容入口。
func BuildSessionExpiredCard() map[string]any {
	return BuildSessionExpiredCardWithRecent(nil)
}

// BuildSessionExpiredCard 会话过期（曾选过门店但已失效）时展示，提供位置输入直接重选门店，
// 无需用户从头自然语言交互。
func BuildSessionExpiredCardWithRecent(recent []ShopSelection) map[string]any {
	elements := []any{
		larkmsg.Markdown("**⏰ 点单会话已过期**"),
		larkmsg.HintMarkdown("之前选择的门店与购物车已失效，输入位置重新选择门店即可继续。"),
	}
	elements = append(elements, recentShopElements(recent)...)
	elements = append(elements, shopSearchForm("")...)
	return wrapCard(elements)
}

// BuildShopStartCard 尚未选择门店时展示，提供位置输入开始选店。
func BuildShopStartCard(recent []ShopSelection) map[string]any {
	elements := []any{
		larkmsg.Markdown("**选择瑞幸门店**"),
		larkmsg.HintMarkdown("可以直接选最近用过的门店，或先选城市/区县再补充门店关键词。"),
	}
	elements = append(elements, recentShopElements(recent)...)
	elements = append(elements, shopSearchForm("")...)
	return wrapCard(elements)
}

// BuildShopSearchingCard 门店搜索异步进行中的过渡卡片。
func BuildShopSearchingCard(location string) map[string]any {
	return wrapCard([]any{
		larkmsg.Markdown("**选择瑞幸门店**"),
		larkmsg.HintMarkdown("正在按「" + location + "」查询附近门店，请稍候…"),
	})
}

// shopSearchForm 位置输入 + 搜索按钮，提交后在卡片内异步刷新门店列表。
func BuildRegionSelectCard(province string, recent []ShopSelection) map[string]any {
	elements := []any{
		larkmsg.Markdown("**选择瑞幸门店**"),
		larkmsg.HintMarkdown("已选省份：" + strings.TrimSpace(province)),
	}
	elements = append(elements, recentShopElements(recent)...)
	elements = append(elements, shopSearchForm(province)...)
	return wrapCard(elements)
}

func shopSearchForm(province string) []any {
	province = strings.TrimSpace(province)
	provinceSubmit := larkmsg.Button("选择省份", larkmsg.ButtonOptions{
		Name:           "luckin_region_submit",
		Type:           "default",
		FormActionType: "submit",
		Payload: map[string]any{
			cardactionproto.ActionField: cardactionproto.ActionLuckinRegionSelect,
		},
	})
	submit := larkmsg.Button("搜索门店", larkmsg.ButtonOptions{
		Name:           "luckin_shop_search_submit",
		Type:           "primary",
		FormActionType: "submit",
		Payload: map[string]any{
			cardactionproto.ActionField: cardactionproto.ActionLuckinShopSearch,
		},
	})
	return []any{
		map[string]any{
			"tag":                "form",
			"name":               "luckin_shop_search_form",
			"vertical_spacing":   "8px",
			"horizontal_spacing": "8px",
			"elements": []any{
				larkmsg.SelectStatic(cardactionproto.LuckinProvinceFormField, larkmsg.SelectStaticOptions{
					Placeholder:   "省份",
					Width:         "fill",
					InitialOption: province,
					Options:       shopSearchProvinceOptions(),
				}),
				larkmsg.ButtonRow("none", provinceSubmit),
				larkmsg.SelectStatic(cardactionproto.LuckinRegionFormField, larkmsg.SelectStaticOptions{
					Placeholder: "城市/区县",
					Width:       "fill",
					Options:     shopSearchRegionOptions(province),
				}),
				larkmsg.TextInput(cardactionproto.LuckinLocationFormField, larkmsg.TextInputOptions{
					Placeholder: "门店/商圈关键词，如：安贞环宇荟",
				}),
				larkmsg.ButtonRow("none", submit),
			},
		},
	}
}

func shopSearchProvinceOptions() []larkmsg.SelectStaticOption {
	shopSearchProvinceOptionsOnce.Do(func() {
		regions := ProvinceOptions(shopSearchRegionLimit)
		shopSearchProvinceOptionsData = make([]larkmsg.SelectStaticOption, 0, len(regions))
		for _, region := range regions {
			shopSearchProvinceOptionsData = append(shopSearchProvinceOptionsData, larkmsg.SelectStaticOption{Text: region, Value: region})
		}
	})
	return append([]larkmsg.SelectStaticOption(nil), shopSearchProvinceOptionsData...)
}

func shopSearchRegionOptions(province string) []larkmsg.SelectStaticOption {
	province = strings.TrimSpace(province)
	if province == "" {
		return nil
	}
	shopSearchRegionOptionsMu.Lock()
	defer shopSearchRegionOptionsMu.Unlock()
	if cached, ok := shopSearchRegionOptionsCache[province]; ok {
		return append([]larkmsg.SelectStaticOption(nil), cached...)
	}
	regions := CityCountyOptions(province, shopSearchRegionLimit)
	options := make([]larkmsg.SelectStaticOption, 0, len(regions))
	for _, region := range regions {
		options = append(options, larkmsg.SelectStaticOption{Text: region, Value: region})
	}
	shopSearchRegionOptionsCache[province] = options
	return append([]larkmsg.SelectStaticOption(nil), options...)
}

func shopOptionRow(shop ShopOption) map[string]any {
	info := []any{}
	title := "**" + shop.DeptName + "**"
	if shop.Distance > 0 {
		title += "  ·  " + strconv.FormatFloat(shop.Distance, 'f', 1, 64) + "km"
	}
	info = append(info, larkmsg.Markdown(title))
	if addr := strings.TrimSpace(shop.Address); addr != "" {
		info = append(info, larkmsg.HintMarkdown("📍 "+addr))
	}
	if meta := shopMetaLine(shop); meta != "" {
		info = append(info, larkmsg.HintMarkdown(meta))
	}

	controls := larkmsg.ButtonRowsWithLimit(larkmsg.ButtonRowsOptions{MaxColumns: 1, ColumnWidth: "weighted"},
		larkmsg.Button("选这家", larkmsg.ButtonOptions{
			Type: "primary",
			Payload: map[string]any{
				cardactionproto.ActionField:             cardactionproto.ActionLuckinShopSelect,
				cardactionproto.LuckinDeptIDField:       strconv.FormatInt(shop.DeptID, 10),
				cardactionproto.LuckinDeptNameField:     shop.DeptName,
				cardactionproto.LuckinLocationFormField: shop.Address,
				cardactionproto.LuckinLongitudeField:    strconv.FormatFloat(shop.Longitude, 'f', -1, 64),
				cardactionproto.LuckinLatitudeField:     strconv.FormatFloat(shop.Latitude, 'f', -1, 64),
			},
		}),
	)

	return map[string]any{
		"tag":                "column_set",
		"flex_mode":          "stretch",
		"horizontal_spacing": "12px",
		"columns": []any{
			map[string]any{"tag": "column", "width": "weighted", "weight": 3, "vertical_align": "center", "elements": info},
			map[string]any{"tag": "column", "width": "weighted", "weight": 1, "vertical_align": "center", "elements": controls},
		},
	}
}

func recentShopElements(recent []ShopSelection) []any {
	if len(recent) == 0 {
		return nil
	}
	elements := []any{larkmsg.HintMarkdown("最近选择：")}
	buttons := make([]map[string]any, 0, len(recent))
	for _, shop := range recent {
		if shop.DeptID == 0 || strings.TrimSpace(shop.DeptName) == "" {
			continue
		}
		buttons = append(buttons, larkmsg.Button(shop.DeptName, larkmsg.ButtonOptions{
			Type: "default",
			Payload: map[string]any{
				cardactionproto.ActionField:             cardactionproto.ActionLuckinShopSelect,
				cardactionproto.LuckinDeptIDField:       strconv.FormatInt(shop.DeptID, 10),
				cardactionproto.LuckinDeptNameField:     shop.DeptName,
				cardactionproto.LuckinLocationFormField: shop.Address,
				cardactionproto.LuckinLongitudeField:    strconv.FormatFloat(shop.Longitude, 'f', -1, 64),
				cardactionproto.LuckinLatitudeField:     strconv.FormatFloat(shop.Latitude, 'f', -1, 64),
			},
		}))
		if len(buttons) >= 3 {
			break
		}
	}
	if len(buttons) == 0 {
		return nil
	}
	elements = append(elements, larkmsg.ButtonRowsWithLimit(larkmsg.ButtonRowsOptions{MaxColumns: 1}, buttons...)...)
	elements = append(elements, larkmsg.Divider())
	return elements
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

// BuildProductSelectCard 渲染购物车 + 商品搜索结果；购物车常驻在上半部分，搜索结果在下半部分刷新。
func BuildProductSelectCard(shop ShopSelection, cart Cart, products []ProductOption, imageKeys map[int64]string) map[string]any {
	header := cartElements(shop, cart, CheckoutModeInitiatorUnified)
	header = append(header, larkmsg.Divider(), larkmsg.HintMarkdown("搜索结果："))
	if len(products) == 0 {
		header = append(header, larkmsg.Markdown("没有找到匹配商品，换个关键词再搜。"))
		return wrapCard(append(header, productQueryForm(shop)...))
	}

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
	image := productImageElement(imgKey, product.ProductName)
	info := []any{}
	info = append(info, larkmsg.Markdown("**"+product.ProductName+"**"))
	if priceLine := productPriceLine(product); priceLine != "" {
		info = append(info, larkmsg.Markdown(priceLine))
	}
	if len(product.Tags) > 0 {
		info = append(info, larkmsg.HintMarkdown(strings.Join(product.Tags, " · ")))
	}
	controls := []any{larkmsg.TextInput(QtyFormField(product.ProductID), larkmsg.TextInputOptions{
		Placeholder:  "数量（默认 1）",
		DefaultValue: "1",
	})}
	controls = append(controls, larkmsg.ButtonRowsWithLimit(larkmsg.ButtonRowsOptions{MaxColumns: 1, ColumnWidth: "weighted", HorizontalSpacing: "6px"},
		larkmsg.Button("加入默认", larkmsg.ButtonOptions{
			Type:           "primary",
			Name:           "luckin_select_" + idValue,
			FormActionType: "submit",
			Fill:           true,
			Payload: map[string]any{
				cardactionproto.ActionField:          cardactionproto.ActionLuckinProductSelect,
				cardactionproto.LuckinProductIDField: idValue,
				cardactionproto.LuckinSkuCodeField:   product.SkuCode,
				cardactionproto.LuckinProductName:    product.ProductName,
				cardactionproto.LuckinUnitPriceField: strconv.FormatFloat(productUnitPrice(product), 'f', -1, 64),
				cardactionproto.LuckinImageKeyField:  imgKey,
			},
		}),
		larkmsg.Button("定制", larkmsg.ButtonOptions{
			Type:           "default",
			Name:           "luckin_customize_" + idValue,
			FormActionType: "submit",
			Fill:           true,
			Payload: map[string]any{
				cardactionproto.ActionField:          cardactionproto.ActionLuckinProductSelect,
				cardactionproto.LuckinProductIDField: idValue,
				cardactionproto.LuckinSkuCodeField:   product.SkuCode,
				cardactionproto.LuckinProductName:    product.ProductName,
				cardactionproto.LuckinUnitPriceField: strconv.FormatFloat(productUnitPrice(product), 'f', -1, 64),
				cardactionproto.LuckinImageKeyField:  imgKey,
				cardactionproto.LuckinCustomizeField: "1",
			},
		}),
	)...)

	columns := []any{}
	if image != nil {
		columns = append(columns, map[string]any{"tag": "column", "width": "auto", "vertical_align": "center", "elements": []any{image}})
	}
	columns = append(columns,
		map[string]any{"tag": "column", "width": "weighted", "weight": 3, "vertical_align": "center", "elements": info},
		map[string]any{"tag": "column", "width": "weighted", "weight": 1, "vertical_align": "center", "elements": controls},
	)
	rowBody := map[string]any{
		"tag":                "column_set",
		"flex_mode":          "stretch",
		"horizontal_spacing": "12px",
		"columns":            columns,
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

func productImageElement(imgKey, alt string) map[string]any {
	if strings.TrimSpace(imgKey) == "" {
		return nil
	}
	return map[string]any{"tag": "img", "img_key": imgKey, "alt": map[string]any{"tag": "plain_text", "content": alt}, "preview": true, "scale_type": "crop_center", "size": "medium"}
}

func productPriceLine(product ProductOption) string {
	if product.Price <= 0 && product.InitialPice <= 0 {
		return ""
	}
	if product.Price > 0 && product.InitialPice > product.Price {
		return "<font color='grey'>原价 ¥" + trimFloat(product.InitialPice) + "</font>  <font color='red'>预估到手 ¥" + trimFloat(product.Price) + "</font>"
	}
	price := productUnitPrice(product)
	return "<font color='red'>预估到手 ¥" + trimFloat(price) + "</font>"
}

func productUnitPrice(product ProductOption) float64 {
	if product.Price > 0 {
		return product.Price
	}
	return product.InitialPice
}

// QtyFormField 为每个商品生成唯一的数量字段名，避免同一卡片内 name 冲突。
func QtyFormField(productID int64) string {
	return cardactionproto.LuckinQtyFormField + "_" + strconv.FormatInt(productID, 10)
}

// BuildProductQueryCard 在用户选定门店后展示，提供商品搜索输入框，整条动线在卡片内完成。
func BuildProductQueryCard(shop ShopSelection, cart Cart) map[string]any {
	elements := cartElements(shop, cart, CheckoutModeInitiatorUnified)
	elements = append(elements, larkmsg.Divider(), larkmsg.HintMarkdown("想喝点什么？输入商品关键词搜索。"))
	elements = append(elements, productQueryForm(shop)...)
	return wrapCard(elements)
}

// BuildProductSearchingCard 在异步搜索商品期间展示的过渡卡片。
func BuildProductSearchingCard(shop ShopSelection, cart Cart, query string) map[string]any {
	elements := cartElements(shop, cart, CheckoutModeInitiatorUnified)
	elements = append(elements, larkmsg.Divider(), larkmsg.HintMarkdown("正在搜索「"+query+"」，请稍候…"))
	return wrapCard(elements)
}

// BuildProductSearchErrorCard 在异步搜索失败时展示，并保留搜索表单方便重试。
func BuildProductSearchErrorCard(shop ShopSelection, cart Cart, query string) map[string]any {
	elements := cartElements(shop, cart, CheckoutModeInitiatorUnified)
	elements = append(elements, larkmsg.Divider(), larkmsg.Markdown("搜索「"+query+"」失败，请重试或换个关键词。"))
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

func BuildBindTokenCard(chatType ChatType, ephemeralMessageID ...string) map[string]any {
	messageID := ""
	if len(ephemeralMessageID) > 0 {
		messageID = strings.TrimSpace(ephemeralMessageID[0])
	}
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
			cardactionproto.IDField:     messageID,
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
