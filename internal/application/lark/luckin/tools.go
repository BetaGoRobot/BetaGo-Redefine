package luckin

import "time"

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
}

func ToolPolicies() []ToolPolicy {
	return []ToolPolicy{
		{RobotToolName: "luckin_shop_search", MCPToolName: "queryShopList", Description: "查询瑞幸咖啡门店列表", DirectLLM: true},
		{RobotToolName: "luckin_product_search", MCPToolName: "searchProductForMcp", Description: "按用户查询文本搜索瑞幸商品", DirectLLM: true},
		{RobotToolName: "luckin_product_detail", MCPToolName: "queryProductDetailInfo", Description: "查询瑞幸商品详情", DirectLLM: true},
		{RobotToolName: "luckin_product_switch", MCPToolName: "switchProduct", Description: "切换瑞幸商品规格属性", DirectLLM: true},
		{RobotToolName: "luckin_order_preview", MCPToolName: "previewOrder", Description: "预览瑞幸订单价格和取餐信息", DirectLLM: true},
		{RobotToolName: "luckin_order_detail", MCPToolName: "queryOrderDetailInfo", Description: "查询瑞幸订单详情", DirectLLM: true},
		{RobotToolName: "luckin_order_prepare_create", MCPToolName: "createOrder", Description: "创建待确认瑞幸订单草稿，不直接下单", DirectLLM: true, HighRisk: true},
	}
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
