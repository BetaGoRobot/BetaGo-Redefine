package todo

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
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

func TestTodoWriteToolsDeferApprovalWhenCollectorPresent(t *testing.T) {
	ins := tools.New[larkim.P2MessageReceiveV1]()
	RegisterTools(ins)

	cases := []struct {
		name            string
		raw             string
		expectedResult  string
		expectedTitle   string
		expectedSummary string
	}{
		{
			name:            "create_todo",
			raw:             `{"title":"准备周会材料","description":"整理议程","priority":"high"}`,
			expectedResult:  "已发起审批，等待确认后创建待办。",
			expectedTitle:   "审批创建待办",
			expectedSummary: "将创建待办「准备周会材料」",
		},
		{
			name:            "update_todo",
			raw:             `{"id":"todo_update_1","status":"done"}`,
			expectedResult:  "已发起审批，等待确认后更新待办。",
			expectedTitle:   "审批更新待办",
			expectedSummary: "将完成待办 `todo_update_1`",
		},
		{
			name:            "delete_todo",
			raw:             `{"id":"todo_delete_1"}`,
			expectedResult:  "已发起审批，等待确认后删除待办。",
			expectedTitle:   "审批删除待办",
			expectedSummary: "将删除待办 `todo_delete_1`",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unit, ok := ins.Get(tc.name)
			if !ok {
				t.Fatalf("expected %s tool", tc.name)
			}

			ctx := runtimecontext.WithDeferredToolCallCollector(context.Background(), runtimecontext.NewDeferredToolCallCollector())
			result := unit.Function(ctx, tc.raw, tools.FCMeta[larkim.P2MessageReceiveV1]{
				ChatID: "oc_chat",
				OpenID: "ou_user",
			})
			if result.IsErr() {
				t.Fatalf("tool returned error: %v", result.Err())
			}
			if result.Value() != tc.expectedResult {
				t.Fatalf("result = %q, want %q", result.Value(), tc.expectedResult)
			}

			deferred, ok := runtimecontext.PopDeferredToolCall(ctx)
			if !ok {
				t.Fatal("expected deferred tool call to be recorded")
			}
			if deferred.ApprovalType != "capability" {
				t.Fatalf("approval type = %q, want %q", deferred.ApprovalType, "capability")
			}
			if deferred.ApprovalTitle != tc.expectedTitle {
				t.Fatalf("approval title = %q, want %q", deferred.ApprovalTitle, tc.expectedTitle)
			}
			if strings.TrimSpace(deferred.PlaceholderOutput) != tc.expectedResult {
				t.Fatalf("placeholder output = %q, want %q", deferred.PlaceholderOutput, tc.expectedResult)
			}
			if deferred.ApprovalSummary != tc.expectedSummary {
				t.Fatalf("approval summary = %q, want %q", deferred.ApprovalSummary, tc.expectedSummary)
			}
		})
	}
}
