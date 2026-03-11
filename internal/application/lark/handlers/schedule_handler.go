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
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ScheduleListArgs struct {
	Limit int `json:"limit"`
}

type ScheduleManageArgs struct {
	Limit int `json:"limit"`
}

type ScheduleQueryArgs struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Type          string `json:"type"`
	ToolName      string `json:"tool_name"`
	CreatorOpenID string `json:"creator_open_id"`
	Limit         int    `json:"limit"`
}

type ScheduleCreateArgs struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	RunAt         string `json:"run_at"`
	CronExpr      string `json:"cron_expr"`
	Timezone      string `json:"timezone"`
	Message       string `json:"message"`
	ToolName      string `json:"tool_name"`
	ToolArgs      string `json:"tool_args"`
	NotifyOnError bool   `json:"notify_on_error"`
	NotifyResult  bool   `json:"notify_result"`
}

type ScheduleDeleteArgs struct {
	ID string `json:"id"`
}

type SchedulePauseArgs struct {
	ID string `json:"id"`
}

type ScheduleResumeArgs struct {
	ID string `json:"id"`
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
	return ScheduleListArgs{Limit: limit}, nil
}

func (scheduleManageHandler) ParseCLI(args []string) (ScheduleManageArgs, error) {
	argMap, _ := parseArgs(args...)
	limit, _ := strconv.Atoi(argMap["limit"])
	return ScheduleManageArgs{Limit: limit}, nil
}

func (scheduleQueryHandler) ParseCLI(args []string) (ScheduleQueryArgs, error) {
	argMap, _ := parseArgs(args...)
	limit, _ := strconv.Atoi(argMap["limit"])
	return ScheduleQueryArgs{
		ID:            argMap["id"],
		Name:          argMap["name"],
		Status:        argMap["status"],
		Type:          argMap["type"],
		ToolName:      argMap["tool_name"],
		CreatorOpenID: firstNonEmpty(argMap["creator_open_id"], argMap["open_id"]),
		Limit:         limit,
	}, nil
}

func (scheduleCreateHandler) ParseCLI(args []string) (ScheduleCreateArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := ScheduleCreateArgs{
		Name:     argMap["name"],
		Type:     argMap["type"],
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
	var err error
	if argMap["notify_on_error"] != "" {
		parsed.NotifyOnError, err = strconv.ParseBool(argMap["notify_on_error"])
		if err != nil {
			return ScheduleCreateArgs{}, err
		}
	}
	if argMap["notify_result"] != "" {
		parsed.NotifyResult, err = strconv.ParseBool(argMap["notify_result"])
		if err != nil {
			return ScheduleCreateArgs{}, err
		}
	}
	return parsed, nil
}

func (scheduleDeleteHandler) ParseCLI(args []string) (ScheduleDeleteArgs, error) {
	id, err := parseRequiredScheduleID(args, "delete")
	if err != nil {
		return ScheduleDeleteArgs{}, err
	}
	return ScheduleDeleteArgs{ID: id}, nil
}

func (schedulePauseHandler) ParseCLI(args []string) (SchedulePauseArgs, error) {
	id, err := parseRequiredScheduleID(args, "pause")
	if err != nil {
		return SchedulePauseArgs{}, err
	}
	return SchedulePauseArgs{ID: id}, nil
}

func (scheduleResumeHandler) ParseCLI(args []string) (ScheduleResumeArgs, error) {
	id, err := parseRequiredScheduleID(args, "resume")
	if err != nil {
		return ScheduleResumeArgs{}, err
	}
	return ScheduleResumeArgs{ID: id}, nil
}

func (scheduleListHandler) ParseTool(raw string) (ScheduleListArgs, error) {
	parsed := ScheduleListArgs{}
	if raw == "" || raw == "{}" {
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ScheduleListArgs{}, err
	}
	return parsed, nil
}

func (scheduleManageHandler) ParseTool(raw string) (ScheduleManageArgs, error) {
	parsed := ScheduleManageArgs{}
	if raw == "" || raw == "{}" {
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ScheduleManageArgs{}, err
	}
	return parsed, nil
}

func (scheduleQueryHandler) ParseTool(raw string) (ScheduleQueryArgs, error) {
	parsed := ScheduleQueryArgs{}
	if raw == "" || raw == "{}" {
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ScheduleQueryArgs{}, err
	}
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
		Type:            arg.Type,
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

	view := scheduleapp.NewTaskListCardView(arg.Limit)
	tasks, err := scheduleapp.GetService().ListTasks(ctx, &scheduleapp.ListTasksRequest{
		ChatID: currentChatID(data, metaData),
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

	view := scheduleapp.NewTaskListCardView(arg.Limit)
	if view.Limit > 20 {
		view.Limit = 20
	}
	tasks, err := scheduleapp.GetService().ListTasks(ctx, &scheduleapp.ListTasksRequest{
		ChatID: currentChatID(data, metaData),
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

	chatID := currentChatID(data, metaData)
	if arg.ID != "" {
		view := scheduleapp.NewTaskQueryCardView(arg.ID, scheduleapp.TaskQuery{}, arg.Limit)
		task, getErr := scheduleapp.GetService().GetTask(ctx, arg.ID)
		if getErr != nil {
			return getErr
		}
		if task.ChatID != chatID {
			return sendScheduleTaskViewCard(ctx, data, metaData, view, nil, "_scheduleQuery")
		}
		return sendScheduleTaskViewCard(ctx, data, metaData, view, []*model.ScheduledTask{task}, "_scheduleQuery")
	}

	view := scheduleapp.NewTaskQueryCardView("", scheduleapp.TaskQuery{
		Name:          arg.Name,
		Status:        arg.Status,
		Type:          arg.Type,
		ToolName:      arg.ToolName,
		CreatorOpenID: arg.CreatorOpenID,
	}, arg.Limit)
	tasks, err := scheduleapp.GetService().ListTasks(ctx, &scheduleapp.ListTasksRequest{
		ChatID: chatID,
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

	chatID := currentChatID(data, metaData)
	if _, err := scheduleapp.GetTaskForChat(ctx, chatID, arg.ID); err != nil {
		return err
	}
	if err := scheduleapp.GetService().DeleteTask(ctx, arg.ID, currentOpenID(data, metaData)); err != nil {
		return err
	}
	return sendScheduleTaskViewCard(ctx, data, metaData, scheduleapp.NewTaskQueryCardView(arg.ID, scheduleapp.TaskQuery{}, 1), nil, "_scheduleDelete")
}

func (schedulePauseHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg SchedulePauseArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	chatID := currentChatID(data, metaData)
	if _, err := scheduleapp.GetTaskForChat(ctx, chatID, arg.ID); err != nil {
		return err
	}
	if err := scheduleapp.GetService().PauseTask(ctx, arg.ID, currentOpenID(data, metaData)); err != nil {
		return err
	}
	task, err := scheduleapp.GetTaskForChat(ctx, chatID, arg.ID)
	if err != nil {
		return err
	}
	return sendScheduleTaskViewCard(ctx, data, metaData, scheduleapp.NewTaskQueryCardView(arg.ID, scheduleapp.TaskQuery{}, 1), []*model.ScheduledTask{task}, "_schedulePause")
}

func (scheduleResumeHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ScheduleResumeArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	chatID := currentChatID(data, metaData)
	if _, err := scheduleapp.GetTaskForChat(ctx, chatID, arg.ID); err != nil {
		return err
	}
	task, err := scheduleapp.GetService().ResumeTask(ctx, arg.ID, currentOpenID(data, metaData))
	if err != nil {
		return err
	}
	if chatID != "" && task.ChatID != chatID {
		return errors.New("task not found")
	}
	return sendScheduleTaskViewCard(ctx, data, metaData, scheduleapp.NewTaskQueryCardView(arg.ID, scheduleapp.TaskQuery{}, 1), []*model.ScheduledTask{task}, "_scheduleResume")
}

func (scheduleListHandler) CommandDescription() string {
	return "查看当前群聊的 schedule 列表"
}

func (scheduleManageHandler) CommandDescription() string {
	return "打开 schedule 管理面板"
}

func (scheduleQueryHandler) CommandDescription() string {
	return "按 ID、名称、状态或工具名查询 schedule，并发送卡片"
}

func (scheduleCreateHandler) CommandDescription() string {
	return "创建 schedule，并回显结果卡片"
}

func (scheduleDeleteHandler) CommandDescription() string {
	return "删除指定 schedule，并回显结果卡片"
}

func (schedulePauseHandler) CommandDescription() string {
	return "暂停指定 schedule，并回显结果卡片"
}

func (scheduleResumeHandler) CommandDescription() string {
	return "恢复指定 schedule，并回显结果卡片"
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
