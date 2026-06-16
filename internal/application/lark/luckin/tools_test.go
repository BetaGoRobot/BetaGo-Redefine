package luckin

import "testing"

func TestToolPoliciesDoNotExposeCreateOrderDirectly(t *testing.T) {
	policies := ToolPolicies()
	for _, p := range policies {
		if p.RobotToolName == "createOrder" {
			t.Fatalf("raw createOrder robot tool must not be exposed")
		}
		if p.MCPToolName == "createOrder" && p.DirectLLM && p.RobotToolName != "luckin_order_prepare_create" {
			t.Fatalf("createOrder must only be exposed through prepare-create policy")
		}
	}
	if _, ok := PolicyByRobotTool("luckin_order_prepare_create"); !ok {
		t.Fatalf("missing prepare-create policy")
	}
}
