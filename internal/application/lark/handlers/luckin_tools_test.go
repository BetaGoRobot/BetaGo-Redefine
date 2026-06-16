package handlers

import "testing"

func TestBuildLarkToolsIncludeLuckinTools(t *testing.T) {
	useWorkspaceConfigPath(t)
	tools := BuildLarkTools()
	if _, ok := tools.Get("luckin_shop_search"); !ok {
		t.Fatalf("lark tools missing luckin_shop_search")
	}
	if _, ok := tools.Get("luckin_order_prepare_create"); !ok {
		t.Fatalf("lark tools missing luckin_order_prepare_create")
	}
	if _, ok := tools.Get("createOrder"); ok {
		t.Fatalf("lark tools registered raw createOrder")
	}
}

func TestSchedulableToolsDoNotIncludeLuckinCreate(t *testing.T) {
	useWorkspaceConfigPath(t)
	tools := BuildSchedulableTools()
	if _, ok := tools.Get("luckin_order_prepare_create"); ok {
		t.Fatalf("schedulable tools include luckin_order_prepare_create")
	}
}
