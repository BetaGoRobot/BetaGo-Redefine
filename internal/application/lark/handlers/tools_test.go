package handlers

import "testing"

func TestBuildSchedulableToolsContainsStandardToolset(t *testing.T) {
	schedulable := BuildSchedulableTools()
	allTools := larktools()

	excluded := map[string]struct{}{
		"create_schedule": {},
		"list_schedules":  {},
		"delete_schedule": {},
		"pause_schedule":  {},
		"resume_schedule": {},
		"revert_message":  {},
	}

	for name := range allTools.FunctionCallMap {
		if _, skip := excluded[name]; skip {
			continue
		}
		if _, ok := schedulable.FunctionCallMap[name]; !ok {
			t.Fatalf("schedulable tools missing %q", name)
		}
	}

	if _, ok := schedulable.FunctionCallMap["gold_price_get"]; !ok {
		t.Fatal("schedulable tools missing gold_price_get")
	}
}
