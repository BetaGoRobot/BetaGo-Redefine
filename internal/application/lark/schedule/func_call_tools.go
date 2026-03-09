package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const scheduleToolResultKey = "schedule_tool_result"

type createScheduleArgs struct {
	Name          string          `json:"name"`
	Type          string          `json:"type"`
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
	Limit int `json:"limit"`
}

type deleteScheduleArgs struct {
	ID string `json:"id"`
}

type pauseScheduleArgs struct {
	ID string `json:"id"`
}

type resumeScheduleArgs struct {
	ID string `json:"id"`
}

type createScheduleHandler struct{}
type listSchedulesHandler struct{}
type deleteScheduleHandler struct{}
type pauseScheduleHandler struct{}
type resumeScheduleHandler struct{}

var CreateSchedule createScheduleHandler
var ListSchedules listSchedulesHandler
var DeleteSchedule deleteScheduleHandler
var PauseSchedule pauseScheduleHandler
var ResumeSchedule resumeScheduleHandler

func RegisterTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, CreateSchedule)
	xcommand.RegisterTool(ins, ListSchedules)
	xcommand.RegisterTool(ins, DeleteSchedule)
	xcommand.RegisterTool(ins, PauseSchedule)
	xcommand.RegisterTool(ins, ResumeSchedule)
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
	return parsed, nil
}

func (createScheduleHandler) ToolSpec() xcommand.ToolSpec {
	availableTools := strings.Join(GetService().AvailableTools(), ", ")
	desc := "创建统一的 schedule。单次提醒用 type=once + run_at + message；周期任务用 type=cron + cron_expr；如果要执行工具，则传 tool_name 和 tool_args"
	if availableTools != "" {
		desc += "。可调度工具: " + availableTools
	}
	return scheduleResultSpec(
		"create_schedule",
		desc,
		tools.NewParams("object").
			AddProp("name", &tools.Prop{Type: "string", Desc: "任务名称，必填"}).
			AddProp("type", &tools.Prop{Type: "string", Desc: "调度类型: once 或 cron，必填"}).
			AddProp("run_at", &tools.Prop{Type: "string", Desc: "单次任务的执行时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS"}).
			AddProp("cron_expr", &tools.Prop{Type: "string", Desc: "周期任务的标准 5 段 cron 表达式，例如 `0 9 * * 1-5`"}).
			AddProp("timezone", &tools.Prop{Type: "string", Desc: "IANA 时区名，例如 Asia/Shanghai，默认 Asia/Shanghai"}).
			AddProp("message", &tools.Prop{Type: "string", Desc: "如果只是提醒/发消息，直接传 message，不需要再传 tool_name"}).
			AddProp("tool_name", &tools.Prop{Type: "string", Desc: "需要自动执行的工具名。message 和 tool_name 二选一"}).
			AddProp("tool_args", &tools.Prop{Type: "object", Desc: "工具参数对象。使用 tool_name 时传入"}).
			AddProp("notify_on_error", &tools.Prop{Type: "boolean", Desc: "执行失败时是否额外发一条错误通知，默认 false"}).
			AddProp("notify_result", &tools.Prop{Type: "boolean", Desc: "工具返回文本结果时是否额外发送结果通知，默认 false"}).
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
		ChatID:        metaData.ChatID,
		CreatorID:     metaData.UserID,
		Name:          args.Name,
		Type:          args.Type,
		RunAt:         runAt,
		CronExpr:      strings.TrimSpace(args.CronExpr),
		Timezone:      strings.TrimSpace(args.Timezone),
		Message:       args.Message,
		ToolName:      strings.TrimSpace(args.ToolName),
		ToolArgs:      string(args.ToolArgs),
		NotifyOnError: args.NotifyOnError,
		NotifyResult:  args.NotifyResult,
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
	metaData.SetExtra(scheduleToolResultKey, result)
	return nil
}

func (listSchedulesHandler) ParseTool(raw string) (listSchedulesArgs, error) {
	parsed := listSchedulesArgs{}
	if raw == "" || raw == "{}" {
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return listSchedulesArgs{}, err
	}
	return parsed, nil
}

func (listSchedulesHandler) ToolSpec() xcommand.ToolSpec {
	return scheduleResultSpec(
		"list_schedules",
		"列出当前群聊的 schedule，包括单次提醒和 cron 任务",
		tools.NewParams("object").
			AddProp("limit", &tools.Prop{Type: "number", Desc: "返回数量限制，默认 50"}),
	)
}

func (listSchedulesHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args listSchedulesArgs) error {
	tasks, err := GetService().ListTasks(ctx, &ListTasksRequest{
		ChatID: metaData.ChatID,
		Limit:  args.Limit,
	})
	if err != nil {
		return err
	}
	metaData.SetExtra(scheduleToolResultKey, FormatTaskList(tasks))
	return nil
}

func (deleteScheduleHandler) ParseTool(raw string) (deleteScheduleArgs, error) {
	parsed := deleteScheduleArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return deleteScheduleArgs{}, err
	}
	return parsed, nil
}

func (deleteScheduleHandler) ToolSpec() xcommand.ToolSpec {
	return scheduleResultSpec(
		"delete_schedule",
		"删除一个 schedule",
		tools.NewParams("object").
			AddProp("id", &tools.Prop{Type: "string", Desc: "要删除的 schedule ID"}).
			AddRequired("id"),
	)
}

func (deleteScheduleHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args deleteScheduleArgs) error {
	if err := GetService().DeleteTask(ctx, args.ID); err != nil {
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
	return parsed, nil
}

func (pauseScheduleHandler) ToolSpec() xcommand.ToolSpec {
	return scheduleResultSpec(
		"pause_schedule",
		"暂停一个 schedule",
		tools.NewParams("object").
			AddProp("id", &tools.Prop{Type: "string", Desc: "要暂停的 schedule ID"}).
			AddRequired("id"),
	)
}

func (pauseScheduleHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args pauseScheduleArgs) error {
	if err := GetService().PauseTask(ctx, args.ID); err != nil {
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
	return parsed, nil
}

func (resumeScheduleHandler) ToolSpec() xcommand.ToolSpec {
	return scheduleResultSpec(
		"resume_schedule",
		"恢复一个已暂停的 schedule",
		tools.NewParams("object").
			AddProp("id", &tools.Prop{Type: "string", Desc: "要恢复的 schedule ID"}).
			AddRequired("id"),
	)
}

func (resumeScheduleHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args resumeScheduleArgs) error {
	task, err := GetService().ResumeTask(ctx, args.ID)
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
