package schedule

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
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
	useWorkspaceConfigPath(t)
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

type fakeScheduleRuntimeResumeEnqueuer struct {
	seen redis_dal.ResumeEvent
}

func (f *fakeScheduleRuntimeResumeEnqueuer) EnqueueResumeEvent(ctx context.Context, event redis_dal.ResumeEvent) error {
	f.seen = event
	return nil
}

func TestAgentRuntimeResumeHandleEnqueuesResumeEvent(t *testing.T) {
	prev := buildScheduleAgentRuntimeResumeEnqueuer
	fake := &fakeScheduleRuntimeResumeEnqueuer{}
	buildScheduleAgentRuntimeResumeEnqueuer = func(context.Context) scheduleRuntimeResumeEnqueuer {
		return fake
	}
	defer func() { buildScheduleAgentRuntimeResumeEnqueuer = prev }()

	err := AgentRuntimeResume.Handle(context.Background(), nil, &xhandler.BaseMetaData{
		ChatID: "oc_chat",
		OpenID: "ou_actor",
	}, agentRuntimeResumeArgs{
		RunID:       "run_100",
		StepID:      "step_100",
		Revision:    5,
		Summary:     "定时触发日报续跑",
		PayloadJSON: json.RawMessage(`{"task_id":"task_daily","trigger":"cron"}`),
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if fake.seen.RunID != "run_100" || fake.seen.StepID != "step_100" || fake.seen.Revision != 5 || fake.seen.Source != "schedule" || fake.seen.ActorOpenID != "ou_actor" {
		t.Fatalf("unexpected resume event: %+v", fake.seen)
	}
	if fake.seen.Summary != "定时触发日报续跑" {
		t.Fatalf("summary = %q, want %q", fake.seen.Summary, "定时触发日报续跑")
	}
	if string(fake.seen.PayloadJSON) != `{"task_id":"task_daily","trigger":"cron"}` {
		t.Fatalf("payload json = %s, want %s", string(fake.seen.PayloadJSON), `{"task_id":"task_daily","trigger":"cron"}`)
	}
}

func TestScheduleWriteToolsDeferApprovalWhenCollectorPresent(t *testing.T) {
	useWorkspaceConfigPath(t)
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
			name:            "create_schedule",
			raw:             `{"name":"晨会提醒","type":"once","run_at":"2026-03-19 09:00:00","message":"9点晨会","notify_result":true}`,
			expectedResult:  "已发起审批，等待确认后创建 schedule。",
			expectedTitle:   "审批创建 schedule",
			expectedSummary: "将创建单次 schedule「晨会提醒」",
		},
		{
			name:            "delete_schedule",
			raw:             `{"id":"task_delete_1"}`,
			expectedResult:  "已发起审批，等待确认后删除 schedule。",
			expectedTitle:   "审批删除 schedule",
			expectedSummary: "将删除 schedule `task_delete_1`",
		},
		{
			name:            "pause_schedule",
			raw:             `{"id":"task_pause_1"}`,
			expectedResult:  "已发起审批，等待确认后暂停 schedule。",
			expectedTitle:   "审批暂停 schedule",
			expectedSummary: "将暂停 schedule `task_pause_1`",
		},
		{
			name:            "resume_schedule",
			raw:             `{"id":"task_resume_1"}`,
			expectedResult:  "已发起审批，等待确认后恢复 schedule。",
			expectedTitle:   "审批恢复 schedule",
			expectedSummary: "将恢复 schedule `task_resume_1`",
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
