package ark_dal

import (
	"strings"
	"testing"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestAdditionalToolsFromToolResultBuildsFinanceToolsFromDiscoverOutput(t *testing.T) {
	output := `{"tools":[{"tool_name":"finance_market_data_get","description":"读取只读市场行情","schema":{"type":"object","properties":{"asset_type":{"type":"string","description":"市场品类"}},"required":["asset_type"]}},{"tool_name":"finance_news_get","description":"读取金融资讯","schema":{"type":"object","properties":{"topic_type":{"type":"string","description":"资讯类型"}},"required":["topic_type"]}}]}`

	tools := additionalToolsFromToolResult("finance_tool_discover", output)
	if len(tools) != 2 {
		t.Fatalf("additional tool count = %d, want 2", len(tools))
	}
	if got := tools[0].GetToolFunction().GetName(); got != "finance_market_data_get" {
		t.Fatalf("first tool name = %q, want %q", got, "finance_market_data_get")
	}
	if got := string(tools[0].GetToolFunction().GetParameters().GetValue()); !strings.Contains(got, `"asset_type"`) || !strings.Contains(got, `"required":["asset_type"]`) {
		t.Fatalf("first tool parameters = %s, want asset_type schema", got)
	}
	if got := tools[1].GetToolFunction().GetName(); got != "finance_news_get" {
		t.Fatalf("second tool name = %q, want %q", got, "finance_news_get")
	}
}

func TestAdditionalToolsFromToolResultIgnoresMalformedDiscoverOutput(t *testing.T) {
	if tools := additionalToolsFromToolResult("finance_tool_discover", `{"tools":[{"tool_name":"","schema":{}}]}`); len(tools) != 0 {
		t.Fatalf("malformed discover output produced %d tools, want 0", len(tools))
	}
	if tools := additionalToolsFromToolResult("finance_tool_discover", `not-json`); len(tools) != 0 {
		t.Fatalf("invalid json produced %d tools, want 0", len(tools))
	}
	if tools := additionalToolsFromToolResult("stock_zh_a_get", `{"tools":[{"tool_name":"finance_market_data_get"}]}`); len(tools) != 0 {
		t.Fatalf("non-discover tool produced %d tools, want 0", len(tools))
	}
}

func TestMergeResponseToolsDedupesDiscoveredFinanceToolsAgainstBaseSet(t *testing.T) {
	base := []*responsesToolFixture{
		{name: "finance_market_data_get", schema: arktools.NewParams("object").AddProp("asset_type", &arktools.Prop{Type: "string"})},
		{name: "finance_news_get", schema: arktools.NewParams("object").AddProp("topic_type", &arktools.Prop{Type: "string"})},
	}
	extra := []*responsesToolFixture{
		{name: "finance_market_data_get", schema: arktools.NewParams("object").AddProp("asset_type", &arktools.Prop{Type: "string"})},
		{name: "economy_indicator_get", schema: arktools.NewParams("object").AddProp("indicator", &arktools.Prop{Type: "string"})},
	}

	merged := mergeResponseTools(responseToolFixtures(base...), responseToolFixtures(extra...))
	if len(merged) != 3 {
		t.Fatalf("merged tool count = %d, want 3", len(merged))
	}
	if got := merged[2].GetToolFunction().GetName(); got != "economy_indicator_get" {
		t.Fatalf("merged third tool = %q, want %q", got, "economy_indicator_get")
	}
}

type responsesToolFixture struct {
	name   string
	schema *arktools.Param
}

func responseToolFixtures(items ...*responsesToolFixture) []*responses.ResponsesTool {
	tools := make([]*responses.ResponsesTool, 0, len(items))
	for _, item := range items {
		tools = append(tools, responseFunctionTool(item.name, "", item.schema))
	}
	return tools
}
