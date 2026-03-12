package todo

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestRegisterToolsInfersTypedEnums(t *testing.T) {
	ins := tools.New[larkim.P2MessageReceiveV1]()

	RegisterTools(ins)

	createUnit, ok := ins.Get("create_todo")
	if !ok {
		t.Fatal("expected create_todo tool")
	}
	priorityProp := createUnit.Parameters.Props["priority"]
	if priorityProp == nil {
		t.Fatal("expected priority prop on create_todo")
	}
	if len(priorityProp.Enum) != 4 || priorityProp.Enum[0] != "low" || priorityProp.Enum[3] != "urgent" {
		t.Fatalf("unexpected create_todo priority enum: %+v", priorityProp.Enum)
	}
	if priorityProp.Default != "medium" {
		t.Fatalf("unexpected create_todo priority default: %#v", priorityProp.Default)
	}

	listUnit, ok := ins.Get("list_todos")
	if !ok {
		t.Fatal("expected list_todos tool")
	}
	statusProp := listUnit.Parameters.Props["status"]
	if statusProp == nil {
		t.Fatal("expected status prop on list_todos")
	}
	if len(statusProp.Enum) != 4 || statusProp.Enum[0] != "pending" || statusProp.Enum[3] != "cancelled" {
		t.Fatalf("unexpected list_todos status enum: %+v", statusProp.Enum)
	}
	if statusProp.Default != nil {
		t.Fatalf("expected no list_todos status default, got %#v", statusProp.Default)
	}
}

func TestCreateTodoParseToolUsesDefaultPriority(t *testing.T) {
	args, err := CreateTodo.ParseTool(`{"title":"test"}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}
	if args.Priority != TodoPriorityMedium {
		t.Fatalf("expected default priority %q, got %q", TodoPriorityMedium, args.Priority)
	}
}

func TestUpdateTodoParseToolKeepsOptionalPriorityEmpty(t *testing.T) {
	args, err := UpdateTodo.ParseTool(`{"id":"todo-1"}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}
	if args.Priority != "" {
		t.Fatalf("expected empty priority, got %q", args.Priority)
	}
}
