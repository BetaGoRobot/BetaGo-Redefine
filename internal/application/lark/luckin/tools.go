package luckin

import (
	"time"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
)

const (
	ProviderName = ProviderLuckin
	ServerName   = "my-coffee"
	ServerURL    = "https://gwmcp.lkcoffee.com/order/user/mcp"
)

type ToolPolicy struct {
	RobotToolName string
	MCPToolName   string
	Description   string
	DirectLLM     bool
	HighRisk      bool
	Params        *arktools.Param
}

func ToolPolicies() []ToolPolicy {
	return []ToolPolicy{
		{RobotToolName: "luckin_bind_token_guide", Description: "发送瑞幸账号 Token 绑定/重新绑定引导卡片", DirectLLM: true, Params: bindTokenGuideParams()},
		{RobotToolName: "luckin_shop_search", MCPToolName: "queryShopList", Description: "查询瑞幸咖啡门店列表", DirectLLM: true, Params: shopSearchParams()},
		{RobotToolName: "luckin_product_search", MCPToolName: "searchProductForMcp", Description: "按用户查询文本搜索瑞幸商品", DirectLLM: true, Params: productSearchParams()},
		{RobotToolName: "luckin_product_detail", MCPToolName: "queryProductDetailInfo", Description: "查询瑞幸商品详情", DirectLLM: true, Params: productDetailParams()},
		{RobotToolName: "luckin_product_switch", MCPToolName: "switchProduct", Description: "切换瑞幸商品规格属性", DirectLLM: true, Params: productSwitchParams()},
		{RobotToolName: "luckin_order_preview", MCPToolName: "previewOrder", Description: "预览瑞幸订单价格和取餐信息", DirectLLM: true, Params: orderPreviewParams()},
		{RobotToolName: "luckin_order_detail", MCPToolName: "queryOrderDetailInfo", Description: "查询瑞幸订单详情", DirectLLM: true, Params: orderDetailParams()},
		{RobotToolName: "luckin_order_prepare_create", MCPToolName: "createOrder", Description: "创建待确认瑞幸订单草稿，不直接下单", DirectLLM: true, HighRisk: true, Params: orderCreateParams()},
	}
}

func bindTokenGuideParams() *arktools.Param {
	return arktools.NewParams("object")
}

func shopSearchParams() *arktools.Param {
	return arktools.NewParams("object").
		AddProp("locationText", &arktools.Prop{Type: "string", Desc: "用户描述的位置，可选；如果用户没有提供位置，传空对象 {}，系统会发送门店搜索入口卡片让用户输入位置或选择最近门店"}).
		AddProp("deptName", &arktools.Prop{Type: "string", Desc: "门店名称关键词，可选，用于在附近门店中进一步过滤"})
}

func productSearchParams() *arktools.Param {
	return arktools.NewParams("object").
		AddProp("deptId", &arktools.Prop{Type: "integer", Desc: "门店ID"}).
		AddProp("query", &arktools.Prop{Type: "string", Desc: "用户原始查询文本，例如“生椰拿铁”"}).
		AddRequired("deptId").
		AddRequired("query")
}

func productDetailParams() *arktools.Param {
	return arktools.NewParams("object").
		AddProp("deptId", &arktools.Prop{Type: "integer", Desc: "门店ID"}).
		AddProp("productId", &arktools.Prop{Type: "integer", Desc: "商品ID"}).
		AddRequired("deptId").
		AddRequired("productId")
}

func productSwitchParams() *arktools.Param {
	return arktools.NewParams("object").
		AddProp("deptId", &arktools.Prop{Type: "integer", Desc: "门店ID"}).
		AddProp("productId", &arktools.Prop{Type: "integer", Desc: "商品ID"}).
		AddProp("skuCode", &arktools.Prop{Type: "string", Desc: "商品 SKU 编码"}).
		AddProp("attrOperationParam", &arktools.Prop{
			Type: "object",
			Desc: "属性切换参数",
			Props: map[string]*arktools.Prop{
				"attributeId": {Type: "integer", Desc: "属性组ID"},
				"subAttr": {
					Type: "object",
					Desc: "属性值操作信息",
					Props: map[string]*arktools.Prop{
						"attributeId": {Type: "integer", Desc: "属性值ID"},
						"operation":   {Type: "integer", Desc: "操作类型，选中传 3"},
					},
					Required: []string{"attributeId", "operation"},
				},
			},
			Required: []string{"attributeId", "subAttr"},
		}).
		AddProp("amount", &arktools.Prop{Type: "integer", Desc: "商品数量"}).
		AddRequired("deptId").
		AddRequired("productId").
		AddRequired("skuCode").
		AddRequired("attrOperationParam").
		AddRequired("amount")
}

func orderProductListProp() *arktools.Prop {
	return &arktools.Prop{
		Type: "array",
		Desc: "订单商品列表",
		Items: &arktools.Prop{
			Type: "object",
			Desc: "订单商品",
			Props: map[string]*arktools.Prop{
				"amount":    {Type: "integer", Desc: "商品数量"},
				"productId": {Type: "integer", Desc: "商品ID"},
				"skuCode":   {Type: "string", Desc: "商品 SKU 编码"},
			},
			Required: []string{"amount", "productId", "skuCode"},
		},
	}
}

func orderPreviewParams() *arktools.Param {
	return arktools.NewParams("object").
		AddProp("deptId", &arktools.Prop{Type: "integer", Desc: "门店ID"}).
		AddProp("productList", orderProductListProp()).
		AddRequired("deptId").
		AddRequired("productList")
}

func orderDetailParams() *arktools.Param {
	return arktools.NewParams("object").
		AddProp("orderId", &arktools.Prop{Type: "string", Desc: "订单ID"}).
		AddRequired("orderId")
}

func orderCreateParams() *arktools.Param {
	return arktools.NewParams("object").
		AddProp("deptId", &arktools.Prop{Type: "integer", Desc: "门店ID"}).
		AddProp("productList", orderProductListProp()).
		AddProp("couponCodeList", &arktools.Prop{
			Type:  "array",
			Desc:  "优惠券编码列表，可选",
			Items: &arktools.Prop{Type: "string", Desc: "优惠券编码"},
		}).
		AddRequired("deptId").
		AddRequired("productList")
}

func PolicyByRobotTool(name string) (ToolPolicy, bool) {
	for _, policy := range ToolPolicies() {
		if policy.RobotToolName == name {
			return policy, true
		}
	}
	return ToolPolicy{}, false
}

func DefaultTimeout() time.Duration {
	return 15 * time.Second
}
