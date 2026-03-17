package handlers

import (
	"context"
	"errors"
	"strconv"
	"time"

	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ScheduleListArgs struct {
	Limit     int               `json:"limit"`
	ChatScope ScheduleChatScope `json:"chat_scope" cli:"-"`
	ChatID    string            `json:"chat_id" cli:"-"`
}

type ScheduleManageArgs struct {
	Limit     int               `json:"limit"`
	ChatScope ScheduleChatScope `json:"chat_scope" cli:"-"`
	ChatID    string            `json:"chat_id" cli:"-"`
}

type ScheduleQueryArgs struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Status        ScheduleStatus    `json:"status"`
	Type          ScheduleType      `json:"type"`
	ToolName      string            `json:"tool_name"`
	CreatorOpenID string            `json:"creator_open_id"`
	Limit         int               `json:"limit"`
	ChatScope     ScheduleChatScope `json:"chat_scope" cli:"-"`
	ChatID        string            `json:"chat_id" cli:"-"`
}

type ScheduleCreateArgs struct {
	Name          string       `json:"name"`
	Type          ScheduleType `json:"type"`
	RunAt         string       `json:"run_at"`
	CronExpr      string       `json:"cron_expr"`
	Timezone      string       `json:"timezone"`
	Message       string       `json:"message"`
	ToolName      string       `json:"tool_name"`
	ToolArgs      string       `json:"tool_args"`
	NotifyOnError bool         `json:"notify_on_error"`
	NotifyResult  bool         `json:"notify_result"`
}

type ScheduleDeleteArgs struct {
	ID        string            `json:"id"`
	ChatScope ScheduleChatScope `json:"chat_scope" cli:"-"`
	ChatID    string            `json:"chat_id" cli:"-"`
}

type SchedulePauseArgs struct {
	ID        string            `json:"id"`
	ChatScope ScheduleChatScope `json:"chat_scope" cli:"-"`
	ChatID    string            `json:"chat_id" cli:"-"`
}

type ScheduleResumeArgs struct {
	ID        string            `json:"id"`
	ChatScope ScheduleChatScope `json:"chat_scope" cli:"-"`
	ChatID    string            `json:"chat_id" cli:"-"`
}

type scheduleListHandler struct{}
type scheduleManageHandler struct{}
type scheduleQueryHandler struct{}
type scheduleCreateHandler struct{}
type scheduleDeleteHandler struct{}
type schedulePauseHandler struct{}
type scheduleResumeHandler struct{}

var ScheduleList scheduleListHandler
var ScheduleManage scheduleManageHandler
var ScheduleQuery scheduleQueryHandler
var ScheduleCreate scheduleCreateHandler
var ScheduleDelete scheduleDeleteHandler
var SchedulePause schedulePauseHandler
var ScheduleResume scheduleResumeHandler

func (scheduleListHandler) ParseCLI(args []string) (ScheduleListArgs, error) {
	argMap, _ := parseArgs(args...)
	limit, _ := strconv.Atoi(argMap["limit"])
	return ScheduleListArgs{
		Limit:     limit,
		ChatScope: ScheduleChatScopeCurrent,
	}, nil
}

func (scheduleManageHandler) ParseCLI(args []string) (ScheduleManageArgs, error) {
	argMap, _ := parseArgs(args...)
	limit, _ := strconv.Atoi(argMap["limit"])
	return ScheduleManageArgs{
		Limit:     limit,
		ChatScope: ScheduleChatScopeCurrent,
	}, nil
}

func (scheduleQueryHandler) ParseCLI(args []string) (ScheduleQueryArgs, error) {
	argMap, _ := parseArgs(args...)
	limit, _ := strconv.Atoi(argMap["limit"])
	status, err := xcommand.ParseEnum[ScheduleStatus](argMap["status"])
	if err != nil {
		return ScheduleQueryArgs{}, err
	}
	taskType, err := xcommand.ParseEnum[ScheduleType](argMap["type"])
	if err != nil {
		return ScheduleQueryArgs{}, err
	}
	return ScheduleQueryArgs{
		ID:            argMap["id"],
		Name:          argMap["name"],
		Status:        status,
		Type:          taskType,
		ToolName:      argMap["tool_name"],
		CreatorOpenID: firstNonEmpty(argMap["creator_open_id"], argMap["open_id"]),
		Limit:         limit,
		ChatScope:     ScheduleChatScopeCurrent,
	}, nil
}

func (scheduleCreateHandler) ParseCLI(args []string) (ScheduleCreateArgs, error) {
	argMap, _ := parseArgs(args...)
	taskType, err := xcommand.ParseEnum[ScheduleType](argMap["type"])
	if err != nil {
		return ScheduleCreateArgs{}, err
	}
	parsed := ScheduleCreateArgs{
		Name:     argMap["name"],
		Type:     taskType,
		RunAt:    argMap["run_at"],
		CronExpr: argMap["cron_expr"],
		Timezone: argMap["timezone"],
		Message:  argMap["message"],
		ToolName: argMap["tool_name"],
		ToolArgs: argMap["tool_args"],
	}
	if parsed.Name == "" || parsed.Type == "" {
		return ScheduleCreateArgs{}, errors.New("usage: /schedule create --name=任务名 --type=once|cron [--run_at=时间] [--cron_expr=表达式] [--message=内容] [--tool_name=工具] [--tool_args=JSON]")
	}
	notifyOnError, hasNotifyOnError, err := parseOptionalBoolArg(argMap, "notify_on_error")
	if err != nil {
		return ScheduleCreateArgs{}, err
	}
	if hasNotifyOnError {
		parsed.NotifyOnError = notifyOnError
	}
	notifyResult, hasNotifyResult, err := parseOptionalBoolArg(argMap, "notify_result")
	if err != nil {
		return ScheduleCreateArgs{}, err
	}
	if hasNotifyResult {
		parsed.NotifyResult = notifyResult
	}
	return parsed, nil
}

func (scheduleDeleteHandler) ParseCLI(args []string) (ScheduleDeleteArgs, error) {
	id, err := parseRequiredScheduleID(args, "delete")
	if err != nil {
		return ScheduleDeleteArgs{}, err
	}
	return ScheduleDeleteArgs{ID: id, ChatScope: ScheduleChatScopeCurrent}, nil
}

func (schedulePauseHandler) ParseCLI(args []string) (SchedulePauseArgs, error) {
	id, err := parseRequiredScheduleID(args, "pause")
	if err != nil {
		return SchedulePauseArgs{}, err
	}
	return SchedulePauseArgs{ID: id, ChatScope: ScheduleChatScopeCurrent}, nil
}

func (scheduleResumeHandler) ParseCLI(args []string) (ScheduleResumeArgs, error) {
	id, err := parseRequiredScheduleID(args, "resume")
	if err != nil {
		return ScheduleResumeArgs{}, err
	}
	return ScheduleResumeArgs{ID: id, ChatScope: ScheduleChatScopeCurrent}, nil
}

func (scheduleListHandler) ParseTool(raw string) (ScheduleListArgs, error) {
	parsed := ScheduleListArgs{}
	if raw == "" || raw == "{}" {
		parsed.ChatScope = ScheduleChatScopeCurrent
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ScheduleListArgs{}, err
	}
	parsed.ChatScope = ScheduleChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (scheduleManageHandler) ParseTool(raw string) (ScheduleManageArgs, error) {
	parsed := ScheduleManageArgs{}
	if raw == "" || raw == "{}" {
		parsed.ChatScope = ScheduleChatScopeCurrent
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ScheduleManageArgs{}, err
	}
	parsed.ChatScope = ScheduleChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (scheduleQueryHandler) ParseTool(raw string) (ScheduleQueryArgs, error) {
	parsed := ScheduleQueryArgs{}
	if raw == "" || raw == "{}" {
		parsed.ChatScope = ScheduleChatScopeCurrent
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ScheduleQueryArgs{}, err
	}
	status, err := xcommand.ParseEnum[ScheduleStatus](string(parsed.Status))
	if err != nil {
		return ScheduleQueryArgs{}, err
	}
	taskType, err := xcommand.ParseEnum[ScheduleType](string(parsed.Type))
	if err != nil {
		return ScheduleQueryArgs{}, err
	}
	parsed.Status = status
	parsed.Type = taskType
	parsed.ChatScope = ScheduleChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (scheduleCreateHandler) ParseTool(raw string) (ScheduleCreateArgs, error) {
	parsed := ScheduleCreateArgs{}
	if raw == "" || raw == "{}" {
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ScheduleCreateArgs{}, err
	}
	taskType, err := xcommand.ParseEnum[ScheduleType](string(parsed.Type))
	if err != nil {
		return ScheduleCreateArgs{}, err
	}
	parsed.Type = taskType
	if parsed.Name == "" || parsed.Type == "" {
		return ScheduleCreateArgs{}, errors.New("usage: /schedule create --name=任务名 --type=once|cron [--run_at=时间] [--cron_expr=表达式] [--message=内容] [--tool_name=工具] [--tool_args=JSON]")
	}
	return parsed, nil
}

func (scheduleDeleteHandler) ParseTool(raw string) (ScheduleDeleteArgs, error) {
	parsed := ScheduleDeleteArgs{}
	if raw == "" || raw == "{}" {
		parsed.ChatScope = ScheduleChatScopeCurrent
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ScheduleDeleteArgs{}, err
	}
	parsed.ChatScope = ScheduleChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (schedulePauseHandler) ParseTool(raw string) (SchedulePauseArgs, error) {
	parsed := SchedulePauseArgs{}
	if raw == "" || raw == "{}" {
		parsed.ChatScope = ScheduleChatScopeCurrent
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return SchedulePauseArgs{}, err
	}
	parsed.ChatScope = ScheduleChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (scheduleResumeHandler) ParseTool(raw string) (ScheduleResumeArgs, error) {
	parsed := ScheduleResumeArgs{}
	if raw == "" || raw == "{}" {
		parsed.ChatScope = ScheduleChatScopeCurrent
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ScheduleResumeArgs{}, err
	}
	parsed.ChatScope = ScheduleChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (scheduleCreateHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ScheduleCreateArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	var scheduledAt *time.Time
	if arg.RunAt != "" {
		parsedTime, parseErr := scheduleapp.ParseScheduleTime(arg.RunAt, arg.Timezone)
		if parseErr != nil {
			return parseErr
		}
		scheduledAt = &parsedTime
	}

	task, err := scheduleapp.GetService().CreateTask(ctx, &scheduleapp.CreateTaskRequest{
		ChatID:          currentChatID(data, metaData),
		CreatorID:       currentOpenID(data, metaData),
		SourceMessageID: currentMessageID(data),
		Name:            arg.Name,
		Type:            string(arg.Type),
		RunAt:           scheduledAt,
		CronExpr:        arg.CronExpr,
		Timezone:        arg.Timezone,
		Message:         arg.Message,
		ToolName:        arg.ToolName,
		ToolArgs:        arg.ToolArgs,
		NotifyOnError:   arg.NotifyOnError,
		NotifyResult:    arg.NotifyResult,
	})
	if err != nil {
		return err
	}
	return sendScheduleTaskViewCard(ctx, data, metaData, scheduleapp.NewTaskQueryCardView(task.ID, scheduleapp.TaskQuery{}, 1), []*model.ScheduledTask{task}, "_scheduleCreate")
}

func (scheduleListHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ScheduleListArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	targetChatID := resolveScheduleTargetChatID(arg.ChatScope, arg.ChatID, data, metaData)
	view := scheduleapp.NewTaskListCardView(arg.Limit)
	tasks, err := scheduleapp.GetService().ListTasks(ctx, &scheduleapp.ListTasksRequest{
		ChatID: targetChatID,
		Limit:  view.Limit,
	})
	if err != nil {
		return err
	}
	return sendScheduleTaskViewCard(ctx, data, metaData, view, tasks, "_scheduleList")
}

func (scheduleManageHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ScheduleManageArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	targetChatID := resolveScheduleTargetChatID(arg.ChatScope, arg.ChatID, data, metaData)
	view := scheduleapp.NewTaskListCardView(arg.Limit)
	if view.Limit > 20 {
		view.Limit = 20
	}
	tasks, err := scheduleapp.GetService().ListTasks(ctx, &scheduleapp.ListTasksRequest{
		ChatID: targetChatID,
		Limit:  view.Limit,
	})
	if err != nil {
		return err
	}
	return sendScheduleTaskViewCard(ctx, data, metaData, view, tasks, "_scheduleManage")
}

func (scheduleQueryHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ScheduleQueryArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	targetChatID := resolveScheduleTargetChatID(arg.ChatScope, arg.ChatID, data, metaData)
	if arg.ID != "" {
		view := scheduleapp.NewTaskQueryCardView(arg.ID, scheduleapp.TaskQuery{}, arg.Limit)
		task, getErr := getScheduleTaskForTarget(ctx, targetChatID, arg.ID)
		if getErr != nil {
			return getErr
		}
		return sendScheduleTaskViewCard(ctx, data, metaData, view, []*model.ScheduledTask{task}, "_scheduleQuery")
	}

	view := scheduleapp.NewTaskQueryCardView("", scheduleapp.TaskQuery{
		Name:          arg.Name,
		Status:        string(arg.Status),
		Type:          string(arg.Type),
		ToolName:      arg.ToolName,
		CreatorOpenID: arg.CreatorOpenID,
	}, arg.Limit)
	tasks, err := scheduleapp.GetService().ListTasks(ctx, &scheduleapp.ListTasksRequest{
		ChatID: targetChatID,
		Limit:  view.Limit,
	})
	if err != nil {
		return err
	}
	return sendScheduleTaskViewCard(ctx, data, metaData, view, scheduleapp.FilterTasks(tasks, view.Query()), "_scheduleQuery")
}

func (scheduleDeleteHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ScheduleDeleteArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	targetChatID := resolveScheduleTargetChatID(arg.ChatScope, arg.ChatID, data, metaData)
	if _, err := getScheduleTaskForTarget(ctx, targetChatID, arg.ID); err != nil {
		return err
	}
	if err := scheduleapp.GetService().DeleteTask(ctx, arg.ID, currentOpenID(data, metaData)); err != nil {
		return err
	}
	view := scheduleapp.NewTaskQueryCardView(arg.ID, scheduleapp.TaskQuery{}, 1)
	return sendScheduleTaskViewCard(ctx, data, metaData, view, nil, "_scheduleDelete")
}

func (schedulePauseHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg SchedulePauseArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	targetChatID := resolveScheduleTargetChatID(arg.ChatScope, arg.ChatID, data, metaData)
	if _, err := getScheduleTaskForTarget(ctx, targetChatID, arg.ID); err != nil {
		return err
	}
	if err := scheduleapp.GetService().PauseTask(ctx, arg.ID, currentOpenID(data, metaData)); err != nil {
		return err
	}
	task, err := getScheduleTaskForTarget(ctx, targetChatID, arg.ID)
	if err != nil {
		return err
	}
	view := scheduleapp.NewTaskQueryCardView(arg.ID, scheduleapp.TaskQuery{}, 1)
	return sendScheduleTaskViewCard(ctx, data, metaData, view, []*model.ScheduledTask{task}, "_schedulePause")
}

func (scheduleResumeHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ScheduleResumeArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	targetChatID := resolveScheduleTargetChatID(arg.ChatScope, arg.ChatID, data, metaData)
	if _, err := getScheduleTaskForTarget(ctx, targetChatID, arg.ID); err != nil {
		return err
	}
	task, err := scheduleapp.GetService().ResumeTask(ctx, arg.ID, currentOpenID(data, metaData))
	if err != nil {
		return err
	}
	if targetChatID != "" && task.ChatID != targetChatID {
		return errors.New("task not found")
	}
	view := scheduleapp.NewTaskQueryCardView(arg.ID, scheduleapp.TaskQuery{}, 1)
	return sendScheduleTaskViewCard(ctx, data, metaData, view, []*model.ScheduledTask{task}, "_scheduleResume")
}

func (scheduleListHandler) CommandDescription() string {
	return "查看当前群的 schedule 列表"
}

func (scheduleManageHandler) CommandDescription() string {
	return "打开当前群的 schedule 管理面板"
}

func (scheduleQueryHandler) CommandDescription() string {
	return "按 ID、名称、状态或工具名查询当前群的 schedule"
}

func (scheduleCreateHandler) CommandDescription() string {
	return "创建 schedule，并回显结果卡片"
}

func (scheduleDeleteHandler) CommandDescription() string {
	return "删除指定 schedule；仅操作当前群"
}

func (schedulePauseHandler) CommandDescription() string {
	return "暂停指定 schedule；仅操作当前群"
}

func (scheduleResumeHandler) CommandDescription() string {
	return "恢复指定 schedule；仅操作当前群"
}

func (scheduleListHandler) CommandExamples() []string {
	return []string{
		"/schedule list",
		"/schedule list --limit=20",
	}
}

func (scheduleManageHandler) CommandExamples() []string {
	return []string{
		"/schedule manage",
		"/schedule manage --limit=10",
	}
}

func (scheduleQueryHandler) CommandExamples() []string {
	return []string{
		"/schedule query --id=20260311120000-abc123",
		"/schedule query --name=提醒 --status=enabled",
		"/schedule query --tool_name=send_message",
	}
}

func (scheduleCreateHandler) CommandExamples() []string {
	return []string{
		"/schedule create --name=午休提醒 --type=once --run_at=2026-03-11T13:00:00+08:00 --message=记得午休",
		"/schedule create --name=早报 --type=cron --cron_expr=0 9 * * 1-5 --message=发早报",
	}
}

func (scheduleDeleteHandler) CommandExamples() []string {
	return []string{
		"/schedule delete --id=20260311120000-abc123",
	}
}

func (schedulePauseHandler) CommandExamples() []string {
	return []string{
		"/schedule pause --id=20260311120000-abc123",
	}
}

func (scheduleResumeHandler) CommandExamples() []string {
	return []string{
		"/schedule resume --id=20260311120000-abc123",
	}
}

func parseRequiredScheduleID(args []string, action string) (string, error) {
	argMap, _ := parseArgs(args...)
	id := argMap["id"]
	if id == "" {
		return "", errors.New("usage: /schedule " + action + " --id=task_id")
	}
	return id, nil
}

func normalizeScheduleChatScope(scope ScheduleChatScope) ScheduleChatScope {
	_ = scope
	return ScheduleChatScopeCurrent
}

func normalizedScheduleViewChatID(scope ScheduleChatScope, explicitChatID string) string {
	_, _ = scope, explicitChatID
	return ""
}

func resolveScheduleTargetChatID(scope ScheduleChatScope, explicitChatID string, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) string {
	_, _ = scope, explicitChatID
	return currentChatID(data, metaData)
}

func getScheduleTaskForTarget(ctx context.Context, targetChatID, id string) (*model.ScheduledTask, error) {
	if targetChatID = firstNonEmpty(targetChatID); targetChatID != "" {
		return scheduleapp.GetTaskForChat(ctx, targetChatID, id)
	}
	return scheduleapp.GetService().GetTask(ctx, id)
}

func sendScheduleTaskViewCard(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, view scheduleapp.TaskCardViewState, tasks []*model.ScheduledTask, suffix string) error {
	if view.LastModifierOpenID == "" {
		view.LastModifierOpenID = currentOpenID(data, metaData)
	}
	card := scheduleapp.BuildTaskListCard(ctx, view.Title(), tasks, view)
	content, err := card.JSON()
	if err != nil {
		return err
	}
	return sendCompatibleRawCard(ctx, data, metaData, content, suffix, false)
}
