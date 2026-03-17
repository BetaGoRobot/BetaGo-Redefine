package schedule

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestFilterQueriedSchedules(t *testing.T) {
	tasks := []*model.ScheduledTask{
		{
			ID:        "task-1",
			Name:      "早报提醒",
			CreatorID: "ou_creator_1",
			Status:    model.ScheduleTaskStatusEnabled,
			Type:      model.ScheduleTaskTypeCron,
			ToolName:  "send_message",
		},
		{
			ID:        "task-2",
			Name:      "晚间复盘",
			CreatorID: "ou_creator_2",
			Status:    model.ScheduleTaskStatusPaused,
			Type:      model.ScheduleTaskTypeOnce,
			ToolName:  "search_history",
		},
	}

	filtered := FilterTasks(tasks, TaskQuery{
		Name:     "提醒",
		Status:   model.ScheduleTaskStatusEnabled,
		Type:     model.ScheduleTaskTypeCron,
		ToolName: "send_message",
	})
	if len(filtered) != 1 {
		t.Fatalf("unexpected filtered count: %d", len(filtered))
	}
	if filtered[0].ID != "task-1" {
		t.Fatalf("unexpected filtered task: %+v", filtered[0])
	}
}

func TestFilterQueriedSchedulesByCreatorOpenID(t *testing.T) {
	tasks := []*model.ScheduledTask{
		{ID: "task-1", CreatorID: "ou_creator_1"},
		{ID: "task-2", CreatorID: "ou_creator_2"},
	}

	filtered := FilterTasks(tasks, TaskQuery{CreatorOpenID: "ou_creator_2"})
	if len(filtered) != 1 {
		t.Fatalf("unexpected filtered count: %d", len(filtered))
	}
	if filtered[0].ID != "task-2" {
		t.Fatalf("unexpected filtered task: %+v", filtered[0])
	}
}

func TestRegisterToolsInfersTypedEnums(t *testing.T) {
	ins := tools.New[larkim.P2MessageReceiveV1]()

	RegisterTools(ins)

	createUnit, ok := ins.Get("create_schedule")
	if !ok {
		t.Fatal("expected create_schedule tool")
	}
	createType := createUnit.Parameters.Props["type"]
	if createType == nil {
		t.Fatal("expected type prop on create_schedule")
	}
	if len(createType.Enum) != 2 || createType.Enum[0] != model.ScheduleTaskTypeOnce || createType.Enum[1] != model.ScheduleTaskTypeCron {
		t.Fatalf("unexpected create_schedule type enum: %+v", createType.Enum)
	}
	if createType.Default != nil {
		t.Fatalf("expected no create_schedule type default, got %#v", createType.Default)
	}

	queryUnit, ok := ins.Get("query_schedule")
	if !ok {
		t.Fatal("expected query_schedule tool")
	}
	statusProp := queryUnit.Parameters.Props["status"]
	if statusProp == nil {
		t.Fatal("expected status prop on query_schedule")
	}
	if len(statusProp.Enum) != 4 || statusProp.Enum[0] != model.ScheduleTaskStatusEnabled || statusProp.Enum[3] != model.ScheduleTaskStatusDisabled {
		t.Fatalf("unexpected query_schedule status enum: %+v", statusProp.Enum)
	}

	listUnit, ok := ins.Get("list_schedules")
	if !ok {
		t.Fatal("expected list_schedules tool")
	}
	if listUnit.Parameters.Props["chat_scope"] != nil {
		t.Fatalf("did not expect list_schedules to expose chat_scope: %+v", listUnit.Parameters.Props["chat_scope"])
	}
	if listUnit.Parameters.Props["chat_id"] != nil {
		t.Fatalf("did not expect list_schedules to expose chat_id: %+v", listUnit.Parameters.Props["chat_id"])
	}
}

func TestListSchedulesParseToolDefaultsToCurrentChatScope(t *testing.T) {
	arg, err := ListSchedules.ParseTool(`{}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}
	if arg.ChatScope != TaskChatScopeCurrent {
		t.Fatalf("expected default current chat scope, got %+v", arg)
	}
}

func TestQueryScheduleParseToolIgnoresLegacyCrossChatFields(t *testing.T) {
	arg, err := QuerySchedule.ParseTool(`{"status":"paused","type":"once","chat_scope":"all","chat_id":"oc_target_chat"}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}
	if arg.Status != TaskStatusPaused || arg.Type != TaskTypeOnce {
		t.Fatalf("expected typed status and type, got %+v", arg)
	}
	if arg.ChatScope != TaskChatScopeCurrent || arg.ChatID != "" {
		t.Fatalf("expected legacy cross-chat fields to normalize to current chat, got %+v", arg)
	}
}

func TestDeleteScheduleParseToolDefaultsToCurrentChatScope(t *testing.T) {
	arg, err := DeleteSchedule.ParseTool(`{"id":"task-1"}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}
	if arg.ID != "task-1" || arg.ChatScope != TaskChatScopeCurrent {
		t.Fatalf("expected current-scope delete args, got %+v", arg)
	}
}

func TestResolveToolScheduleTargetChatID(t *testing.T) {
	if got := resolveToolScheduleTargetChatID(TaskChatScopeCurrent, "", "oc_current_chat"); got != "oc_current_chat" {
		t.Fatalf("expected fallback current chat id, got %q", got)
	}
	if got := resolveToolScheduleTargetChatID(TaskChatScopeAll, "", "oc_current_chat"); got != "oc_current_chat" {
		t.Fatalf("expected legacy all-scope resolution to stay on current chat, got %q", got)
	}
	if got := resolveToolScheduleTargetChatID(TaskChatScopeCurrent, "oc_explicit_chat", "oc_current_chat"); got != "oc_current_chat" {
		t.Fatalf("expected explicit chat id override to be ignored, got %q", got)
	}
}
