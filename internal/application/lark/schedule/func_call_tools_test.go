package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/bytedance/mockey"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type editHandleTestService struct {
	noopService
	task *model.ScheduledTask
}

func (s editHandleTestService) GetTask(context.Context, string) (*model.ScheduledTask, error) {
	return s.task, nil
}

type recordingInteractionStarter struct {
	request  agentruntime.StartScheduleEditRequest
	envelope *agentruntime.RuntimeEnvelope
	err      error
	mutate   bool
	calls    int
}

func (s *recordingInteractionStarter) StartScheduleEdit(
	_ context.Context,
	req agentruntime.StartScheduleEditRequest,
) (*agentruntime.RuntimeEnvelope, error) {
	s.calls++
	s.request = req.Clone()
	if s.mutate {
		req.NewValues[editFieldName] = "starter-mutated-value"
	}
	return s.envelope, s.err
}

func TestEditScheduleHandleUsesDurableRuntimeWait(t *testing.T) {
	resetPendingEditsForTest(t)
	envelope := validScheduleEditEnvelope()
	starter := &recordingInteractionStarter{envelope: &envelope, mutate: true}
	var sentCard any
	sendCalls := 0
	patchEditHandleDependencies(t, func(_ context.Context, _, _ string, card any) (string, error) {
		sendCalls++
		sentCard = card
		return "om-card", nil
	})

	ctx := agentruntime.WithInteractionStarter(context.Background(), starter)
	meta := &xhandler.BaseMetaData{ChatID: "oc-chat", OpenID: "ou-creator"}
	name := "可信的新名称"
	err := EditSchedule.Handle(ctx, editScheduleEvent("om-source"), meta, editScheduleArgs{
		ID:   "task-1",
		Name: &name,
	})
	if err != nil {
		t.Fatalf("EditSchedule.Handle() error = %v", err)
	}
	if starter.calls != 1 || sendCalls != 1 {
		t.Fatalf("calls = starter:%d send:%d, want 1 each", starter.calls, sendCalls)
	}
	if starter.request.TaskID != "task-1" ||
		starter.request.ActorOpenID != "ou-creator" ||
		starter.request.ChatID != "oc-chat" ||
		starter.request.SourceMessageID != "om-source" ||
		starter.request.NewValues[editFieldName] != name {
		t.Fatalf("unexpected trusted start request: %#v", starter.request)
	}
	assertNoPendingEdits(t)

	payloads := collectActionPayloads(sentCard)
	if payloads[cardactionproto.ActionScheduleEditConfirm] == nil ||
		payloads[cardactionproto.ActionScheduleEditCancel] == nil {
		t.Fatal("runtime card missing continuation actions")
	}
	cardJSON, err := json.Marshal(sentCard)
	if err != nil {
		t.Fatalf("json.Marshal(card) error = %v", err)
	}
	if !strings.Contains(string(cardJSON), name) || strings.Contains(string(cardJSON), "starter-mutated-value") {
		t.Fatal("starter mutation leaked back into rendered card")
	}
	result, ok := meta.GetExtra(scheduleToolResultKey)
	if !ok ||
		!strings.Contains(result, envelope.RunID) ||
		!strings.Contains(result, envelope.StepID) ||
		!strings.Contains(result, envelope.InteractionID) ||
		strings.Contains(result, envelope.Token) {
		t.Fatal("runtime result metadata is incomplete or exposes the interaction token")
	}
}

func TestEditScheduleHandlePassesResolvedTargetChatToRuntimeStarter(t *testing.T) {
	resetPendingEditsForTest(t)
	envelope := validScheduleEditEnvelope()
	starter := &recordingInteractionStarter{envelope: &envelope}
	patchEditHandleDependenciesForChat(t, "oc-target", func(context.Context, string, string, any) (string, error) {
		return "om-card", nil
	})
	resolverPatch := mockey.Mock(resolveToolScheduleTargetChatID).Return("oc-target").Build()
	t.Cleanup(func() {
		resolverPatch.UnPatch()
	})

	name := "new-name"
	err := EditSchedule.Handle(
		agentruntime.WithInteractionStarter(context.Background(), starter),
		editScheduleEvent("om-source"),
		&xhandler.BaseMetaData{ChatID: "oc-origin", OpenID: "ou-creator"},
		editScheduleArgs{ID: "task-1", Name: &name},
	)
	if err != nil {
		t.Fatalf("EditSchedule.Handle() error = %v", err)
	}
	if starter.request.ChatID != "oc-target" {
		t.Fatalf("starter request chat ID = %q, want resolved target chat", starter.request.ChatID)
	}
}

func TestEditScheduleHandleKeepsLegacyPendingEditFallback(t *testing.T) {
	resetPendingEditsForTest(t)
	var sentCard any
	patchEditHandleDependencies(t, func(_ context.Context, _, _ string, card any) (string, error) {
		sentCard = card
		return "om-card", nil
	})

	meta := &xhandler.BaseMetaData{ChatID: "oc-chat", OpenID: "ou-creator"}
	name := "legacy-name"
	err := EditSchedule.Handle(context.Background(), editScheduleEvent("om-source"), meta, editScheduleArgs{
		ID:   "task-1",
		Name: &name,
	})
	if err != nil {
		t.Fatalf("EditSchedule.Handle() error = %v", err)
	}

	payload := collectActionPayloads(sentCard)[editConfirmAction]
	token, _ := payload[editTokenField].(string)
	if token == "" {
		t.Fatal("legacy confirm card missing edit token")
	}
	pending, ok := GetPendingEdit(token)
	if !ok || pending.TaskID != "task-1" || pending.ActorOpenID != "ou-creator" ||
		pending.NewValues[editFieldName] != name {
		t.Fatalf("unexpected legacy pending edit: %#v, %v", pending, ok)
	}
	if _, ok := payload["run_id"]; ok {
		t.Fatal("legacy payload unexpectedly contains runtime fields")
	}
}

func TestEditScheduleHandleStarterFailureDoesNotSendOrFallback(t *testing.T) {
	resetPendingEditsForTest(t)
	starterErr := errors.New("durable wait unavailable")
	starter := &recordingInteractionStarter{err: starterErr}
	sendCalls := 0
	patchEditHandleDependencies(t, func(context.Context, string, string, any) (string, error) {
		sendCalls++
		return "", nil
	})

	name := "new-name"
	err := EditSchedule.Handle(
		agentruntime.WithInteractionStarter(context.Background(), starter),
		editScheduleEvent("om-source"),
		&xhandler.BaseMetaData{ChatID: "oc-chat", OpenID: "ou-creator"},
		editScheduleArgs{ID: "task-1", Name: &name},
	)
	if !errors.Is(err, starterErr) {
		t.Fatalf("EditSchedule.Handle() error = %v, want starter error", err)
	}
	if sendCalls != 0 {
		t.Fatalf("send calls = %d, want 0", sendCalls)
	}
	assertNoPendingEdits(t)
}

func TestEditScheduleHandleRejectsInvalidRuntimeEnvelopeBeforeSend(t *testing.T) {
	resetPendingEditsForTest(t)
	envelope := validScheduleEditEnvelope()
	envelope.InteractionKind = "other"
	starter := &recordingInteractionStarter{envelope: &envelope}
	sendCalls := 0
	patchEditHandleDependencies(t, func(context.Context, string, string, any) (string, error) {
		sendCalls++
		return "", nil
	})

	name := "new-name"
	err := EditSchedule.Handle(
		agentruntime.WithInteractionStarter(context.Background(), starter),
		editScheduleEvent("om-source"),
		&xhandler.BaseMetaData{ChatID: "oc-chat", OpenID: "ou-creator"},
		editScheduleArgs{ID: "task-1", Name: &name},
	)
	if err == nil || !strings.Contains(err.Error(), "schedule_edit") {
		t.Fatalf("EditSchedule.Handle() error = %v, want invalid schedule_edit envelope", err)
	}
	if sendCalls != 0 {
		t.Fatalf("send calls = %d, want 0", sendCalls)
	}
	assertNoPendingEdits(t)
}

func TestEditScheduleHandleCleansLegacyPendingEditWhenSendFails(t *testing.T) {
	resetPendingEditsForTest(t)
	sendErr := errors.New("send failed")
	patchEditHandleDependencies(t, func(context.Context, string, string, any) (string, error) {
		return "", sendErr
	})

	name := "new-name"
	err := EditSchedule.Handle(
		context.Background(),
		editScheduleEvent("om-source"),
		&xhandler.BaseMetaData{ChatID: "oc-chat", OpenID: "ou-creator"},
		editScheduleArgs{ID: "task-1", Name: &name},
	)
	if !errors.Is(err, sendErr) {
		t.Fatalf("EditSchedule.Handle() error = %v, want send error", err)
	}
	assertNoPendingEdits(t)
}

func TestEditScheduleHandleRuntimeSendFailureDoesNotFallback(t *testing.T) {
	resetPendingEditsForTest(t)
	envelope := validScheduleEditEnvelope()
	starter := &recordingInteractionStarter{envelope: &envelope}
	sendErr := errors.New("send failed")
	patchEditHandleDependencies(t, func(context.Context, string, string, any) (string, error) {
		return "", sendErr
	})

	name := "new-name"
	err := EditSchedule.Handle(
		agentruntime.WithInteractionStarter(context.Background(), starter),
		editScheduleEvent("om-source"),
		&xhandler.BaseMetaData{ChatID: "oc-chat", OpenID: "ou-creator"},
		editScheduleArgs{ID: "task-1", Name: &name},
	)
	if !errors.Is(err, sendErr) {
		t.Fatalf("EditSchedule.Handle() error = %v, want send error", err)
	}
	if starter.calls != 1 {
		t.Fatalf("starter calls = %d, want 1", starter.calls)
	}
	assertNoPendingEdits(t)
}

func validScheduleEditEnvelope() agentruntime.RuntimeEnvelope {
	return agentruntime.RuntimeEnvelope{
		RunID:           "run-1",
		StepID:          "step-1",
		InteractionID:   "interaction-1",
		Revision:        4,
		Token:           "opaque-runtime-token",
		InteractionKind: "schedule_edit",
		ContinueAgent:   true,
	}
}

func editScheduleEvent(messageID string) *larkim.P2MessageReceiveV1 {
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{MessageId: &messageID},
		},
	}
}

func patchEditHandleDependencies(
	t *testing.T,
	send func(context.Context, string, string, any) (string, error),
) {
	patchEditHandleDependenciesForChat(t, "oc-chat", send)
}

func patchEditHandleDependenciesForChat(
	t *testing.T,
	taskChatID string,
	send func(context.Context, string, string, any) (string, error),
) {
	t.Helper()
	task := &model.ScheduledTask{
		ID:        "task-1",
		Name:      "original-name",
		Timezone:  "Asia/Shanghai",
		CreatorID: "ou-creator",
		ChatID:    taskChatID,
	}
	service := TaskService(editHandleTestService{
		noopService: noopService{reason: "unused test methods"},
		task:        task,
	})
	servicePatch := mockey.Mock(GetService).Return(service).Build()
	sendPatch := mockey.Mock(larkmsg.SendEphemeralCard).To(send).Build()
	t.Cleanup(func() {
		sendPatch.UnPatch()
		servicePatch.UnPatch()
	})
}

func resetPendingEditsForTest(t *testing.T) {
	t.Helper()
	deleteAll := func() {
		pendingEditsMu.RLock()
		tokens := make([]string, 0, len(pendingEdits))
		for token := range pendingEdits {
			tokens = append(tokens, token)
		}
		pendingEditsMu.RUnlock()
		for _, token := range tokens {
			DeletePendingEdit(token)
		}
	}
	deleteAll()
	t.Cleanup(deleteAll)
}

func assertNoPendingEdits(t *testing.T) {
	t.Helper()
	pendingEditsMu.RLock()
	defer pendingEditsMu.RUnlock()
	if len(pendingEdits) != 0 {
		t.Fatalf("pending edit count = %d, want 0", len(pendingEdits))
	}
	if len(pendingEditTimers) != 0 {
		t.Fatalf("pending edit timer count = %d, want 0", len(pendingEditTimers))
	}
}

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

func TestCreateScheduleParseToolRejectsIncompleteOnce(t *testing.T) {
	raw := `{ "name": "洛克王国货单提醒", "type": "once" }`

	args, err := CreateSchedule.ParseTool(raw)
	if err == nil {
		t.Fatal("ParseTool() error = nil, want incomplete arguments error")
	}
	if !reflect.DeepEqual(args, createScheduleArgs{}) {
		t.Fatalf("ParseTool() args = %#v, want zero value", args)
	}
	feedback, ok := xerror.ToolFeedback(err)
	if !ok {
		t.Fatalf("ToolFeedback() ok = false for error %v", err)
	}
	for _, want := range []string{"message 或 tool_name", "run_at", "先询问用户"} {
		if !strings.Contains(feedback, want) {
			t.Fatalf("ToolFeedback() = %q, want substring %q", feedback, want)
		}
	}
	if strings.Contains(feedback, raw) || strings.Contains(feedback, "洛克王国货单提醒") {
		t.Fatalf("ToolFeedback() unexpectedly exposes raw tool arguments: %q", feedback)
	}
}

func TestCreateScheduleParseToolAcceptsValidOnceMessage(t *testing.T) {
	args, err := CreateSchedule.ParseTool(`{
		"name": "发货提醒",
		"type": "once",
		"run_at": " 2026-08-27 09:00:00 ",
		"message": "  请核对发货单  "
	}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}
	if args.Type != TaskTypeOnce || args.RunAt != "2026-08-27 09:00:00" {
		t.Fatalf("ParseTool() args = %#v, want normalized once schedule", args)
	}
	if args.Message != "  请核对发货单  " {
		t.Fatalf("ParseTool() message = %q, want message body preserved", args.Message)
	}
}

func TestCreateScheduleParseToolAcceptsValidCronToolName(t *testing.T) {
	args, err := CreateSchedule.ParseTool(`{
		"name": "工作日报",
		"type": "cron",
		"cron_expr": " 0 9 * * 1-5 ",
		"tool_name": " send_message "
	}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}
	if args.Type != TaskTypeCron || args.CronExpr != "0 9 * * 1-5" || args.ToolName != "send_message" {
		t.Fatalf("ParseTool() args = %#v, want normalized cron tool schedule", args)
	}
}

func TestCreateScheduleParseToolRejectsBothMessageAndToolName(t *testing.T) {
	args, err := CreateSchedule.ParseTool(`{
		"name": "重复动作",
		"type": "once",
		"run_at": "2026-08-27 09:00:00",
		"message": "提醒我",
		"tool_name": "send_message"
	}`)
	if err == nil {
		t.Fatal("ParseTool() error = nil, want mutually exclusive action error")
	}
	if !reflect.DeepEqual(args, createScheduleArgs{}) {
		t.Fatalf("ParseTool() args = %#v, want zero value", args)
	}
	feedback, ok := xerror.ToolFeedback(err)
	if !ok {
		t.Fatalf("ToolFeedback() ok = false for error %v", err)
	}
	for _, want := range []string{"二选一", "不能同时"} {
		if !strings.Contains(feedback, want) {
			t.Fatalf("ToolFeedback() = %q, want substring %q", feedback, want)
		}
	}
}

func TestCreateScheduleParseToolRejectsCronWithoutExpression(t *testing.T) {
	args, err := CreateSchedule.ParseTool(`{
		"name": "工作日报",
		"type": "cron",
		"cron_expr": " \t ",
		"tool_name": "send_message"
	}`)
	if err == nil {
		t.Fatal("ParseTool() error = nil, want missing cron_expr error")
	}
	if !reflect.DeepEqual(args, createScheduleArgs{}) {
		t.Fatalf("ParseTool() args = %#v, want zero value", args)
	}
	feedback, ok := xerror.ToolFeedback(err)
	if !ok || !strings.Contains(feedback, "cron_expr") {
		t.Fatalf("ToolFeedback() = %q, %v, want trusted cron_expr feedback", feedback, ok)
	}
}

func TestCreateScheduleToolSpecWarnsAgainstIncompleteCalls(t *testing.T) {
	desc := CreateSchedule.ToolSpec().Desc

	for _, want := range []string{
		"信息缺失时不要调用 create_schedule",
		"不要猜测",
		"询问用户",
		"不能只传 name/type",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("ToolSpec().Desc = %q, want substring %q", desc, want)
		}
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
