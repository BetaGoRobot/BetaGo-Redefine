package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const scheduleToolResultKey = "schedule_tool_result"

type createScheduleArgs struct {
	Name          string          `json:"name"`
	Type          TaskType        `json:"type"`
	RunAt         string          `json:"run_at"`
	CronExpr      string          `json:"cron_expr"`
	Timezone      string          `json:"timezone"`
	Message       string          `json:"message"`
	ToolName      string          `json:"tool_name"`
	ToolArgs      json.RawMessage `json:"tool_args"`
	NotifyOnError bool            `json:"notify_on_error"`
	NotifyResult  bool            `json:"notify_result"`
}

type listSchedulesArgs struct {
	ChatScope TaskChatScope `json:"chat_scope"`
	ChatID    string        `json:"chat_id"`
	Limit     int           `json:"limit"`
}

type TaskQuery struct {
	Name          string
	Status        string
	Type          string
	ToolName      string
	CreatorOpenID string
	ChatScope     string
	ChatID        string
}

type queryScheduleArgs struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Status        TaskStatus    `json:"status"`
	Type          TaskType      `json:"type"`
	ToolName      string        `json:"tool_name"`
	CreatorOpenID string        `json:"creator_open_id"`
	ChatScope     TaskChatScope `json:"chat_scope"`
	ChatID        string        `json:"chat_id"`
	Limit         int           `json:"limit"`
}

type deleteScheduleArgs struct {
	ID        string        `json:"id"`
	ChatScope TaskChatScope `json:"chat_scope"`
	ChatID    string        `json:"chat_id"`
}

type pauseScheduleArgs struct {
	ID        string        `json:"id"`
	ChatScope TaskChatScope `json:"chat_scope"`
	ChatID    string        `json:"chat_id"`
}

type resumeScheduleArgs struct {
	ID        string        `json:"id"`
	ChatScope TaskChatScope `json:"chat_scope"`
	ChatID    string        `json:"chat_id"`
}

type (
	createScheduleHandler     struct{}
	listSchedulesHandler      struct{}
	queryScheduleHandler      struct{}
	deleteScheduleHandler     struct{}
	pauseScheduleHandler      struct{}
	resumeScheduleHandler     struct{}
)

var (
	CreateSchedule     createScheduleHandler
	ListSchedules      listSchedulesHandler
	QuerySchedule      queryScheduleHandler
	DeleteSchedule     deleteScheduleHandler
	PauseSchedule      pauseScheduleHandler
	ResumeSchedule     resumeScheduleHandler
)

func RegisterTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, CreateSchedule)
	xcommand.RegisterTool(ins, ListSchedules)
	xcommand.RegisterTool(ins, QuerySchedule)
	xcommand.RegisterTool(ins, DeleteSchedule)
	xcommand.RegisterTool(ins, PauseSchedule)
	xcommand.RegisterTool(ins, ResumeSchedule)
}

func RegisterRuntimeTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	_ = ins
}

func scheduleResultSpec(name, desc string, params *tools.Param) xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name:   name,
		Desc:   desc,
		Params: params,
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(scheduleToolResultKey)
			return result
		},
	}
}

func (createScheduleHandler) ParseTool(raw string) (createScheduleArgs, error) {
	parsed := createScheduleArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return createScheduleArgs{}, err
	}
	taskType, err := xcommand.ParseEnum[TaskType](string(parsed.Type))
	if err != nil {
		return createScheduleArgs{}, err
	}
	parsed.Type = taskType
	return parsed, nil
}

func (createScheduleHandler) ToolSpec() xcommand.ToolSpec {
	availableTools := strings.Join(GetService().AvailableTools(), ", ")
	desc := "创建统一的 schedule。单次提醒用 type=once + run_at + message；周期任务用 type=cron + cron_expr；如果要执行工具，则传 tool_name 和 tool_args。message 里如果需要@成员，优先输出飞书 `<at user_id=\"open_id\">姓名</at>`；如果只知道名字，也可以写 `@姓名`，系统会尝试按当前群成员匹配"
	if availableTools != "" {
		desc += "。可调度工具: " + availableTools
	}
	return scheduleResultSpec(
		"create_schedule",
		desc,
		tools.NewParams("object").
			AddProp("name", &tools.Prop{Type: "string", Desc: "任务名称，必填"}).
			AddProp("type", &tools.Prop{Type: "string", Desc: "调度模式，必填"}).
			AddProp("run_at", &tools.Prop{Type: "string", Desc: "单次任务的执行时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS"}).
			AddProp("cron_expr", &tools.Prop{Type: "string", Desc: "周期任务的标准 5 段 cron 表达式，例如 `0 9 * * 1-5`"}).
			AddProp("timezone", &tools.Prop{Type: "string", Desc: "IANA 时区名，例如 Asia/Shanghai"}).
			AddProp("message", &tools.Prop{Type: "string", Desc: "如果只是提醒/发消息，直接传 message，不需要再传 tool_name"}).
			AddProp("tool_name", &tools.Prop{Type: "string", Desc: "需要自动执行的工具名。message 和 tool_name 二选一"}).
			AddProp("tool_args", &tools.Prop{Type: "object", Desc: "工具参数对象。使用 tool_name 时传入"}).
			AddProp("notify_on_error", &tools.Prop{Type: "boolean", Desc: "执行失败时是否额外发一条错误通知"}).
			AddProp("notify_result", &tools.Prop{Type: "boolean", Desc: "工具返回文本结果时是否额外发送结果通知"}).
			AddRequired("name").
			AddRequired("type"),
	)
}

func (createScheduleHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args createScheduleArgs) error {
	var runAt *time.Time
	if strings.TrimSpace(args.RunAt) != "" {
		t, err := parseScheduleTime(args.RunAt, strings.TrimSpace(args.Timezone))
		if err != nil {
			return err
		}
		runAt = &t
	}

	task, err := GetService().CreateTask(ctx, &CreateTaskRequest{
		ChatID:          metaData.ChatID,
		CreatorID:       metaData.OpenID,
		SourceMessageID: scheduleSourceMessageID(data),
		Name:            args.Name,
		Type:            string(args.Type),
		RunAt:           runAt,
		CronExpr:        strings.TrimSpace(args.CronExpr),
		Timezone:        strings.TrimSpace(args.Timezone),
		Message:         args.Message,
		ToolName:        strings.TrimSpace(args.ToolName),
		ToolArgs:        string(args.ToolArgs),
		NotifyOnError:   args.NotifyOnError,
		NotifyResult:    args.NotifyResult,
	})
	if err != nil {
		return err
	}

	result := fmt.Sprintf("✅ Schedule 创建成功！\n\n名称: %s\n模式: %s\n动作: %s\nID: `%s`",
		task.Name,
		task.Type,
		task.ToolName,
		task.ID,
	)
	if task.IsOnce() && task.RunAt != nil {
		result += fmt.Sprintf("\n执行时间: %s", task.RunAt.Format("2006-01-02 15:04:05 MST"))
	} else {
		result += fmt.Sprintf("\nCron: `%s`\n下次执行: %s", task.CronExpr, task.NextRunAt.Format("2006-01-02 15:04:05 MST"))
	}
	if task.SourceMessageID != "" {
		result += fmt.Sprintf("\n来源消息: `%s`", task.SourceMessageID)
	}
	metaData.SetExtra(scheduleToolResultKey, result)
	return nil
}

func (listSchedulesHandler) ParseTool(raw string) (listSchedulesArgs, error) {
	parsed := listSchedulesArgs{}
	if raw == "" || raw == "{}" {
		parsed.ChatScope = TaskChatScopeCurrent
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return listSchedulesArgs{}, err
	}
	parsed.ChatScope = TaskChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (listSchedulesHandler) ToolSpec() xcommand.ToolSpec {
	return scheduleResultSpec(
		"list_schedules",
		"列出当前群的 schedule",
		tools.NewParams("object").
			AddProp("limit", &tools.Prop{Type: "number", Desc: "返回数量限制，默认 50"}),
	)
}

func (listSchedulesHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args listSchedulesArgs) error {
	view := NewTaskListCardView(args.Limit)
	view.LastModifierOpenID = strings.TrimSpace(metaData.OpenID)
	targetChatID := resolveToolScheduleTargetChatID(args.ChatScope, args.ChatID, metaData.ChatID)
	tasks, err := GetService().ListTasks(ctx, &ListTasksRequest{
		ChatID: targetChatID,
		Limit:  view.Limit,
	})
	if err != nil {
		return err
	}
	if err := sendScheduleRawCard(ctx, data, metaData, BuildTaskListCard(ctx, "Schedule 列表", tasks, view), "_scheduleList"); err != nil {
		return err
	}
	metaData.SetExtra(scheduleToolResultKey, FormatTaskList(tasks))
	return nil
}

func (queryScheduleHandler) ParseTool(raw string) (queryScheduleArgs, error) {
	parsed := queryScheduleArgs{}
	if raw == "" || raw == "{}" {
		parsed.ChatScope = TaskChatScopeCurrent
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return queryScheduleArgs{}, err
	}
	status, err := xcommand.ParseEnum[TaskStatus](string(parsed.Status))
	if err != nil {
		return queryScheduleArgs{}, err
	}
	taskType, err := xcommand.ParseEnum[TaskType](string(parsed.Type))
	if err != nil {
		return queryScheduleArgs{}, err
	}
	parsed.Status = status
	parsed.Type = taskType
	parsed.ChatScope = TaskChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (queryScheduleHandler) ToolSpec() xcommand.ToolSpec {
	return scheduleResultSpec(
		"query_schedule",
		"按 ID、名称、状态、类型或工具名查询当前群的 schedule",
		tools.NewParams("object").
			AddProp("id", &tools.Prop{Type: "string", Desc: "要精确查询的 schedule ID"}).
			AddProp("name", &tools.Prop{Type: "string", Desc: "按名称模糊匹配"}).
			AddProp("status", &tools.Prop{Type: "string", Desc: "按状态过滤"}).
			AddProp("type", &tools.Prop{Type: "string", Desc: "按调度模式过滤"}).
			AddProp("tool_name", &tools.Prop{Type: "string", Desc: "按执行工具名过滤，例如 send_message"}).
			AddProp("creator_open_id", &tools.Prop{Type: "string", Desc: "按创建者 OpenID 过滤"}).
			AddProp("limit", &tools.Prop{Type: "number", Desc: "返回数量限制；未传时默认 100"}),
	)
}

func (queryScheduleHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args queryScheduleArgs) error {
	targetChatID := resolveToolScheduleTargetChatID(args.ChatScope, args.ChatID, metaData.ChatID)
	if scheduleID := strings.TrimSpace(args.ID); scheduleID != "" {
		view := NewTaskQueryCardView(scheduleID, TaskQuery{}, args.Limit)
		view.LastModifierOpenID = strings.TrimSpace(metaData.OpenID)
		task, err := getToolScheduleTaskForTarget(ctx, targetChatID, scheduleID)
		if err != nil {
			return err
		}
		if err := sendScheduleRawCard(ctx, data, metaData, BuildTaskListCard(ctx, "Schedule 查询", []*model.ScheduledTask{task}, view), "_scheduleQuery"); err != nil {
			return err
		}
		metaData.SetExtra(scheduleToolResultKey, FormatTaskList([]*model.ScheduledTask{task}))
		return nil
	}

	view := NewTaskQueryCardView("", TaskQuery{
		Name:          args.Name,
		Status:        string(args.Status),
		Type:          string(args.Type),
		ToolName:      args.ToolName,
		CreatorOpenID: args.CreatorOpenID,
	}, args.Limit)
	view.LastModifierOpenID = strings.TrimSpace(metaData.OpenID)
	tasks, err := GetService().ListTasks(ctx, &ListTasksRequest{
		ChatID: targetChatID,
		Limit:  view.Limit,
	})
	if err != nil {
		return err
	}

	filtered := FilterTasks(tasks, view.Query())
	if len(filtered) == 0 {
		metaData.SetExtra(scheduleToolResultKey, "未找到匹配的 schedule ⏲️")
		return sendScheduleRawCard(ctx, data, metaData, BuildTaskListCard(ctx, "Schedule 查询", nil, view), "_scheduleQuery")
	}
	if err := sendScheduleRawCard(ctx, data, metaData, BuildTaskListCard(ctx, "Schedule 查询", filtered, view), "_scheduleQuery"); err != nil {
		return err
	}
	metaData.SetExtra(scheduleToolResultKey, FormatTaskList(filtered))
	return nil
}

func (deleteScheduleHandler) ParseTool(raw string) (deleteScheduleArgs, error) {
	parsed := deleteScheduleArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return deleteScheduleArgs{}, err
	}
	parsed.ChatScope = TaskChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (deleteScheduleHandler) ToolSpec() xcommand.ToolSpec {
	return scheduleResultSpec(
		"delete_schedule",
		"删除当前群中的一个 schedule",
		tools.NewParams("object").
			AddProp("id", &tools.Prop{Type: "string", Desc: "要删除的 schedule ID"}).
			AddRequired("id"),
	)
}

func (deleteScheduleHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args deleteScheduleArgs) error {
	targetChatID := resolveToolScheduleTargetChatID(args.ChatScope, args.ChatID, metaData.ChatID)
	if _, err := getToolScheduleTaskForTarget(ctx, targetChatID, args.ID); err != nil {
		return err
	}
	if err := GetService().DeleteTask(ctx, args.ID, metaData.OpenID); err != nil {
		return err
	}
	metaData.SetExtra(scheduleToolResultKey, fmt.Sprintf("✅ Schedule 已删除！ID: `%s`", args.ID))
	return nil
}

func (pauseScheduleHandler) ParseTool(raw string) (pauseScheduleArgs, error) {
	parsed := pauseScheduleArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return pauseScheduleArgs{}, err
	}
	parsed.ChatScope = TaskChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (pauseScheduleHandler) ToolSpec() xcommand.ToolSpec {
	return scheduleResultSpec(
		"pause_schedule",
		"暂停当前群中的一个 schedule",
		tools.NewParams("object").
			AddProp("id", &tools.Prop{Type: "string", Desc: "要暂停的 schedule ID"}).
			AddRequired("id"),
	)
}

func (pauseScheduleHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args pauseScheduleArgs) error {
	targetChatID := resolveToolScheduleTargetChatID(args.ChatScope, args.ChatID, metaData.ChatID)
	if _, err := getToolScheduleTaskForTarget(ctx, targetChatID, args.ID); err != nil {
		return err
	}
	if err := GetService().PauseTask(ctx, args.ID, metaData.OpenID); err != nil {
		return err
	}
	metaData.SetExtra(scheduleToolResultKey, fmt.Sprintf("⏸️ Schedule 已暂停！ID: `%s`", args.ID))
	return nil
}

func (resumeScheduleHandler) ParseTool(raw string) (resumeScheduleArgs, error) {
	parsed := resumeScheduleArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return resumeScheduleArgs{}, err
	}
	parsed.ChatScope = TaskChatScopeCurrent
	parsed.ChatID = ""
	return parsed, nil
}

func (resumeScheduleHandler) ToolSpec() xcommand.ToolSpec {
	return scheduleResultSpec(
		"resume_schedule",
		"恢复当前群中一个已暂停的 schedule",
		tools.NewParams("object").
			AddProp("id", &tools.Prop{Type: "string", Desc: "要恢复的 schedule ID"}).
			AddRequired("id"),
	)
}

func (resumeScheduleHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args resumeScheduleArgs) error {
	targetChatID := resolveToolScheduleTargetChatID(args.ChatScope, args.ChatID, metaData.ChatID)
	if _, err := getToolScheduleTaskForTarget(ctx, targetChatID, args.ID); err != nil {
		return err
	}
	task, err := GetService().ResumeTask(ctx, args.ID, metaData.OpenID)
	if err != nil {
		return err
	}

	result := fmt.Sprintf("▶️ Schedule 已恢复！\n\n名称: %s\n模式: %s\nID: `%s`", task.Name, task.Type, task.ID)
	if task.IsOnce() {
		result += fmt.Sprintf("\n执行时间: %s", task.NextRunAt.Format("2006-01-02 15:04:05 MST"))
	} else {
		result += fmt.Sprintf("\n下次执行: %s", task.NextRunAt.Format("2006-01-02 15:04:05 MST"))
	}
	metaData.SetExtra(scheduleToolResultKey, result)
	return nil
}

func parseScheduleTime(s, timezone string) (time.Time, error) {
	loc, err := resolveLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}

	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
	}

	for _, format := range formats {
		if format == time.RFC3339 {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t, nil
			}
			continue
		}
		if t, err := time.ParseInLocation(format, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析 schedule 时间: %s", s)
}

func ParseScheduleTime(s, timezone string) (time.Time, error) {
	return parseScheduleTime(s, timezone)
}

func FilterTasks(tasks []*model.ScheduledTask, query TaskQuery) []*model.ScheduledTask {
	if len(tasks) == 0 {
		return nil
	}

	nameQuery := strings.ToLower(strings.TrimSpace(query.Name))
	statusQuery := strings.ToLower(strings.TrimSpace(query.Status))
	typeQuery := strings.ToLower(strings.TrimSpace(query.Type))
	toolNameQuery := strings.ToLower(strings.TrimSpace(query.ToolName))
	creatorOpenIDQuery := strings.TrimSpace(query.CreatorOpenID)

	filtered := make([]*model.ScheduledTask, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if nameQuery != "" && !strings.Contains(strings.ToLower(task.Name), nameQuery) {
			continue
		}
		if statusQuery != "" && strings.ToLower(task.Status) != statusQuery {
			continue
		}
		if typeQuery != "" && strings.ToLower(task.Type) != typeQuery {
			continue
		}
		if toolNameQuery != "" && strings.ToLower(task.ToolName) != toolNameQuery {
			continue
		}
		if creatorOpenIDQuery != "" && strings.TrimSpace(task.CreatorID) != creatorOpenIDQuery {
			continue
		}
		filtered = append(filtered, task)
	}
	return filtered
}

func sendScheduleRawCard(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, card larkmsg.RawCard, suffix string) error {
	content, err := card.JSON()
	if err != nil {
		return err
	}

	if data != nil && data.Event.Message.MessageId != nil {
		msgID := strings.TrimSpace(*data.Event.Message.MessageId)
		if msgID != "" {
			if metaData != nil && metaData.Refresh {
				return larkmsg.PatchRawCard(ctx, msgID, content)
			}
			return larkmsg.ReplyRawCard(ctx, msgID, content, suffix, false)
		}
	}

	chatID := ""
	if data != nil && data.Event.Message.ChatId != nil {
		chatID = strings.TrimSpace(*data.Event.Message.ChatId)
	}
	if chatID == "" && metaData != nil {
		chatID = strings.TrimSpace(metaData.ChatID)
	}
	if chatID == "" {
		return errors.New("chat_id is required")
	}

	msgID := fmt.Sprintf("schedule-tool-card-%d", time.Now().UnixNano())
	return larkmsg.CreateRawCard(ctx, chatID, content, msgID, suffix)
}

func normalizeToolScheduleChatScope(scope TaskChatScope) TaskChatScope {
	_ = scope
	return TaskChatScopeCurrent
}

func normalizedToolScheduleViewChatID(scope TaskChatScope, explicitChatID string) string {
	_, _ = scope, explicitChatID
	return ""
}

func resolveToolScheduleTargetChatID(scope TaskChatScope, explicitChatID, fallbackChatID string) string {
	_, _ = scope, explicitChatID
	return strings.TrimSpace(fallbackChatID)
}

func getToolScheduleTaskForTarget(ctx context.Context, targetChatID, id string) (*model.ScheduledTask, error) {
	targetChatID = strings.TrimSpace(targetChatID)
	if targetChatID == "" {
		return GetService().GetTask(ctx, id)
	}
	return GetTaskForChat(ctx, targetChatID, id)
}

func scheduleSourceMessageID(data *larkim.P2MessageReceiveV1) string {
	if data == nil || data.Event == nil || data.Event.Message == nil || data.Event.Message.MessageId == nil {
		return ""
	}
	return strings.TrimSpace(*data.Event.Message.MessageId)
}
