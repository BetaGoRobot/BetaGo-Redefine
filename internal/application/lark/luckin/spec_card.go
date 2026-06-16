package luckin

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

// ProductAttr 商品属性组（如杯型/温度/糖度），含可选值。
type ProductAttr struct {
	AttributeID   int64
	AttributeName string
	SubAttrs      []ProductSubAttr
}

// ProductSubAttr 属性值（如大杯/热/标准糖），含加价与是否选中。
type ProductSubAttr struct {
	AttributeID   int64
	AttributeName string
	Price         float64
	Selected      bool
}

// ProductDetail 商品详情，含规格属性，用于规格选择卡。
type ProductDetail struct {
	ProductID   int64
	SkuCode     string
	ProductName string
	PictureURL  string
	Price       float64
	Attrs       []ProductAttr
}

func ProductDetailFromResult(content json.RawMessage) ProductDetail {
	data := ExtractData(content)
	if len(data) == 0 {
		return ProductDetail{}
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return ProductDetail{}
	}
	name := stringValue(obj["productName"])
	if name == "" {
		name = stringValue(obj["name"])
	}
	price := numberFloat(obj["estimatePrice"])
	if price == 0 {
		price = numberFloat(obj["initialPrice"])
	}
	detail := ProductDetail{
		ProductID:   int64(numberFloat(obj["productId"])),
		SkuCode:     stringValue(obj["skuCode"]),
		ProductName: name,
		PictureURL:  stringValue(obj["pictureUrl"]),
		Price:       price,
	}
	if attrs, ok := obj["productAttrs"].([]any); ok {
		for _, a := range attrs {
			attrObj, ok := a.(map[string]any)
			if !ok {
				continue
			}
			attr := ProductAttr{
				AttributeID:   int64(numberFloat(attrObj["attributeId"])),
				AttributeName: stringValue(attrObj["attributeName"]),
			}
			if subs, ok := attrObj["productSubAttrs"].([]any); ok {
				for _, s := range subs {
					subObj, ok := s.(map[string]any)
					if !ok {
						continue
					}
					sub := ProductSubAttr{
						AttributeID:   int64(numberFloat(subObj["attributeId"])),
						AttributeName: stringValue(subObj["attributeName"]),
						Price:         numberFloat(subObj["price"]),
					}
					if selected, ok := subObj["selected"].(bool); ok {
						sub.Selected = selected
					}
					attr.SubAttrs = append(attr.SubAttrs, sub)
				}
			}
			if len(attr.SubAttrs) > 0 {
				detail.Attrs = append(detail.Attrs, attr)
			}
		}
	}
	return detail
}

// HasSpecs 商品是否含可选规格。
func (d ProductDetail) HasSpecs() bool {
	return len(d.Attrs) > 0
}

// BuildSpecSelectCard 渲染规格选择卡：每个属性组一个下拉框，确认后切换规格并下单。
func BuildSpecSelectCard(shop ShopSelection, detail ProductDetail, imgKey string) map[string]any {
	formElements := []any{
		larkmsg.Markdown("**" + detail.ProductName + "**"),
	}
	if detail.Price > 0 {
		formElements = append(formElements, larkmsg.Markdown("<font color='red'>¥"+trimFloat(detail.Price)+"</font>"))
	}
	for _, attr := range detail.Attrs {
		options := make([]larkmsg.SelectStaticOption, 0, len(attr.SubAttrs))
		initial := ""
		for _, sub := range attr.SubAttrs {
			label := sub.AttributeName
			if sub.Price > 0 {
				label += " (+¥" + trimFloat(sub.Price) + ")"
			}
			value := strconv.FormatInt(sub.AttributeID, 10)
			options = append(options, larkmsg.SelectStaticOption{Text: label, Value: value})
			if sub.Selected && initial == "" {
				initial = value
			}
		}
		formElements = append(formElements, larkmsg.SelectStatic(specFormField(attr.AttributeID), larkmsg.SelectStaticOptions{
			Placeholder:   attr.AttributeName,
			Width:         "fill",
			InitialOption: initial,
			Options:       options,
		}))
	}
	confirm := larkmsg.Button("确认规格并下单", larkmsg.ButtonOptions{
		Name:           "luckin_spec_submit",
		Type:           "primary",
		FormActionType: "submit",
		Payload: map[string]any{
			cardactionproto.ActionField:          cardactionproto.ActionLuckinProductSelect,
			cardactionproto.LuckinProductIDField: strconv.FormatInt(detail.ProductID, 10),
			cardactionproto.LuckinSkuCodeField:   detail.SkuCode,
			cardactionproto.LuckinProductName:    detail.ProductName,
		},
	})
	formElements = append(formElements, larkmsg.ButtonRow("none", confirm))

	form := map[string]any{
		"tag":                "form",
		"name":               "luckin_spec_form",
		"vertical_spacing":   "8px",
		"horizontal_spacing": "8px",
		"elements":           formElements,
	}
	header := []any{larkmsg.Markdown("**已选门店：" + shop.DeptName + "**"), larkmsg.HintMarkdown("选择规格：")}
	if imgKey != "" {
		header = append(header, map[string]any{"tag": "img", "img_key": imgKey, "alt": map[string]any{"tag": "plain_text", "content": detail.ProductName}, "preview": true, "scale_type": "crop_center", "size": "large"})
	}
	return wrapCard(append(header, form))
}

func specFormField(attributeID int64) string {
	return cardactionproto.LuckinSpecFormFieldPrefix + strconv.FormatInt(attributeID, 10)
}

// ParseSpecSelection 从表单值中解析出 attributeId(组) -> 选中的 subAttributeId。
func ParseSpecSelection(formValues map[string]string) map[int64]int64 {
	out := make(map[int64]int64)
	for k, v := range formValues {
		if !strings.HasPrefix(k, cardactionproto.LuckinSpecFormFieldPrefix) {
			continue
		}
		attrID, err := strconv.ParseInt(strings.TrimPrefix(k, cardactionproto.LuckinSpecFormFieldPrefix), 10, 64)
		if err != nil {
			continue
		}
		subID, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		out[attrID] = subID
	}
	return out
}
