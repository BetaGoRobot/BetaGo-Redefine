package todo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/bytedance/gg/gresult"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	globalService *Service
)

// Init 初始化待办服务（需要在应用启动时调用）
func Init(db *gorm.DB) {
	repo := todo.NewRepository(db)
	globalService = NewService(repo)
}

// GetService 获取全局服务实例
func GetService() *Service {
	return globalService
}

// RegisterTools 注册待办相关的 Function Call 工具
func RegisterTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	createTodoTool(ins)
	updateTodoTool(ins)
	listTodosTool(ins)
	deleteTodoTool(ins)
	createReminderTool(ins)
	listRemindersTool(ins)
	deleteReminderTool(ins)
}

// ============================================
// 创建待办工具
// ============================================

func createTodoTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("title", &tools.Prop{
			Type: "string",
			Desc: "待办事项的标题，必填",
		}).
		AddProp("description", &tools.Prop{
			Type: "string",
			Desc: "待办事项的详细描述，可选",
		}).
		AddProp("priority", &tools.Prop{
			Type: "string",
			Desc: "优先级: low(低), medium(中), high(高), urgent(紧急)，默认 medium",
		}).
		AddProp("due_at", &tools.Prop{
			Type: "string",
			Desc: "截止时间，格式为 RFC3339 或 YYYY-MM-DD HH:MM:SS，可选",
		}).
		AddProp("assignee_id", &tools.Prop{
			Type: "string",
			Desc: "负责人的飞书用户ID，可选",
		}).
		AddProp("tags", &tools.Prop{
			Type: "string",
			Desc: "标签，多个标签用逗号分隔，可选",
		}).
		AddRequired("title")

	ins.Add(unit.Name("create_todo").
		Desc("创建一个新的待办事项。当用户要求记录任务、添加待办、设置提醒事项时使用此工具").
		Params(params).
		Func(createTodoWrap))
}

type createTodoArgs struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	DueAt       string `json:"due_at"`
	AssigneeID  string `json:"assignee_id"`
	Tags        string `json:"tags"`
}

func createTodoWrap(ctx context.Context, argStr string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	args := &createTodoArgs{}
	if err := sonic.UnmarshalString(argStr, args); err != nil {
		return gresult.Err[string](err)
	}

	// 获取用户信息
	userInfo, err := larkuser.GetUserInfoCache(ctx, meta.ChatID, meta.UserID)
	if err != nil {
		logs.L().Ctx(ctx).Warn("Get user info failed", zap.Error(err))
	}
	userName := ""
	if userInfo != nil && userInfo.Name != nil {
		userName = *userInfo.Name
	}

	// 解析截止时间
	var dueAt *time.Time
	if args.DueAt != "" {
		t, err := parseTime(args.DueAt)
		if err == nil {
			dueAt = &t
		}
	}

	// 解析标签
	tags := make([]string, 0)
	if args.Tags != "" {
		for _, tag := range strings.Split(args.Tags, ",") {
			if t := strings.TrimSpace(tag); t != "" {
				tags = append(tags, t)
			}
		}
	}

	req := &CreateTodoRequest{
		ChatID:      meta.ChatID,
		CreatorID:   meta.UserID,
		CreatorName: userName,
		Title:       args.Title,
		Description: args.Description,
		Priority:    args.Priority,
		DueAt:       dueAt,
		Tags:        tags,
	}
	if args.AssigneeID != "" {
		req.AssigneeID = &args.AssigneeID
	}

	t, err := GetService().CreateTodo(ctx, req)
	if err != nil {
		return gresult.Err[string](err)
	}

	result := fmt.Sprintf("✅ 待办创建成功！\n\n标题: %s\nID: `%s`", t.Title, t.ID)
	if t.Description != "" {
		result += fmt.Sprintf("\n描述: %s", t.Description)
	}
	if t.DueAt != nil {
		result += fmt.Sprintf("\n截止: %s", t.DueAt.In(utils.UTC8Loc()).Format("2006-01-02 15:04:05"))
	}

	return gresult.OK(result)
}

// ============================================
// 更新待办工具
// ============================================

func updateTodoTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("id", &tools.Prop{
			Type: "string",
			Desc: "待办事项的ID，必填",
		}).
		AddProp("title", &tools.Prop{
			Type: "string",
			Desc: "新的标题，可选",
		}).
		AddProp("description", &tools.Prop{
			Type: "string",
			Desc: "新的描述，可选",
		}).
		AddProp("status", &tools.Prop{
			Type: "string",
			Desc: "状态: pending(待处理), doing(进行中), done(已完成), cancelled(已取消)",
		}).
		AddProp("priority", &tools.Prop{
			Type: "string",
			Desc: "优先级: low, medium, high, urgent",
		}).
		AddProp("due_at", &tools.Prop{
			Type: "string",
			Desc: "新的截止时间，格式 RFC3339 或 YYYY-MM-DD HH:MM:SS",
		}).
		AddProp("add_tags", &tools.Prop{
			Type: "string",
			Desc: "要添加的标签，多个用逗号分隔",
		}).
		AddProp("remove_tags", &tools.Prop{
			Type: "string",
			Desc: "要移除的标签，多个用逗号分隔",
		}).
		AddRequired("id")

	ins.Add(unit.Name("update_todo").
		Desc("更新待办事项的状态、标题、描述、截止时间等。当用户要求完成任务、修改待办、设置状态时使用").
		Params(params).
		Func(updateTodoWrap))
}

type updateTodoArgs struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	DueAt       string `json:"due_at"`
	AddTags     string `json:"add_tags"`
	RemoveTags  string `json:"remove_tags"`
}

func updateTodoWrap(ctx context.Context, argStr string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	args := &updateTodoArgs{}
	if err := sonic.UnmarshalString(argStr, args); err != nil {
		return gresult.Err[string](err)
	}

	req := &UpdateTodoRequest{
		ID: args.ID,
	}

	if args.Title != "" {
		req.Title = &args.Title
	}
	if args.Description != "" {
		req.Description = &args.Description
	}
	if args.Status != "" {
		req.Status = &args.Status
	}
	if args.Priority != "" {
		req.Priority = &args.Priority
	}
	if args.DueAt != "" {
		if t, err := parseTime(args.DueAt); err == nil {
			req.DueAt = &t
		}
	}
	if args.AddTags != "" {
		for _, tag := range strings.Split(args.AddTags, ",") {
			if t := strings.TrimSpace(tag); t != "" {
				req.AddTags = append(req.AddTags, t)
			}
		}
	}
	if args.RemoveTags != "" {
		for _, tag := range strings.Split(args.RemoveTags, ",") {
			if t := strings.TrimSpace(tag); t != "" {
				req.RemoveTags = append(req.RemoveTags, t)
			}
		}
	}

	t, err := GetService().UpdateTodo(ctx, req)
	if err != nil {
		return gresult.Err[string](err)
	}

	result := fmt.Sprintf("✅ 待办更新成功！\n\n标题: %s\n状态: %s", t.Title, t.Status)
	if args.Status == "done" {
		result = fmt.Sprintf("🎉 恭喜完成任务！\n\n标题: %s", t.Title)
	}

	return gresult.OK(result)
}

// ============================================
// 列出待办工具
// ============================================

func listTodosTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("status", &tools.Prop{
			Type: "string",
			Desc: "过滤状态: pending, doing, done, cancelled，不填则列出所有",
		}).
		AddProp("limit", &tools.Prop{
			Type: "number",
			Desc: "返回数量限制，默认 50",
		})

	ins.Add(unit.Name("list_todos").
		Desc("列出当前群组的所有待办事项。当用户要求查看待办、任务列表、有什么任务时使用").
		Params(params).
		Func(listTodosWrap))
}

type listTodosArgs struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

func listTodosWrap(ctx context.Context, argStr string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	args := &listTodosArgs{}
	_ = sonic.UnmarshalString(argStr, args)

	req := &ListTodosRequest{
		ChatID: meta.ChatID,
		Limit:  args.Limit,
	}
	if args.Status != "" {
		req.Status = &args.Status
	}

	todos, err := GetService().ListTodos(ctx, req)
	if err != nil {
		return gresult.Err[string](err)
	}

	return gresult.OK(FormatTodoList(todos))
}

// ============================================
// 删除待办工具
// ============================================

func deleteTodoTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("id", &tools.Prop{
			Type: "string",
			Desc: "要删除的待办事项ID",
		}).
		AddRequired("id")

	ins.Add(unit.Name("delete_todo").
		Desc("删除指定的待办事项。当用户要求删除任务、移除待办时使用").
		Params(params).
		Func(deleteTodoWrap))
}

type deleteTodoArgs struct {
	ID string `json:"id"`
}

func deleteTodoWrap(ctx context.Context, argStr string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	args := &deleteTodoArgs{}
	if err := sonic.UnmarshalString(argStr, args); err != nil {
		return gresult.Err[string](err)
	}

	if err := GetService().DeleteTodo(ctx, args.ID); err != nil {
		return gresult.Err[string](err)
	}

	return gresult.OK(fmt.Sprintf("✅ 待办已删除！ID: `%s`", args.ID))
}

// ============================================
// 创建提醒工具
// ============================================

func createReminderTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("title", &tools.Prop{
			Type: "string",
			Desc: "提醒的标题，必填",
		}).
		AddProp("content", &tools.Prop{
			Type: "string",
			Desc: "提醒的详细内容，可选",
		}).
		AddProp("trigger_at", &tools.Prop{
			Type: "string",
			Desc: "触发时间，格式 RFC3339 或 YYYY-MM-DD HH:MM:SS，必填",
		}).
		AddProp("type", &tools.Prop{
			Type: "string",
			Desc: "提醒类型: once(一次性), daily(每天), weekly(每周), monthly(每月)，默认 once",
		}).
		AddProp("todo_id", &tools.Prop{
			Type: "string",
			Desc: "关联的待办事项ID，可选",
		}).
		AddRequired("title").
		AddRequired("trigger_at")

	ins.Add(unit.Name("create_reminder").
		Desc("创建一个提醒。当用户要求设置闹钟、提醒我、到时通知等时使用此工具").
		Params(params).
		Func(createReminderWrap))
}

type createReminderArgs struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	TriggerAt string `json:"trigger_at"`
	Type      string `json:"type"`
	TodoID    string `json:"todo_id"`
}

func createReminderWrap(ctx context.Context, argStr string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	args := &createReminderArgs{}
	if err := sonic.UnmarshalString(argStr, args); err != nil {
		return gresult.Err[string](err)
	}

	triggerAt, err := parseTime(args.TriggerAt)
	if err != nil {
		return gresult.Err[string](fmt.Errorf("无法解析时间: %w", err))
	}

	req := &CreateReminderRequest{
		ChatID:     meta.ChatID,
		CreatorID:  meta.UserID,
		Title:      args.Title,
		Content:    args.Content,
		Type:       args.Type,
		TriggerAt:  triggerAt,
		TodoID:     args.TodoID,
	}

	rem, err := GetService().CreateReminder(ctx, req)
	if err != nil {
		return gresult.Err[string](err)
	}

	result := fmt.Sprintf("⏰ 提醒创建成功！\n\n标题: %s\n触发时间: %s\nID: `%s`",
		rem.Title,
		rem.TriggerAt.In(utils.UTC8Loc()).Format("2006-01-02 15:04:05"),
		rem.ID)
	if rem.Content != "" {
		result += fmt.Sprintf("\n内容: %s", rem.Content)
	}

	return gresult.OK(result)
}

// ============================================
// 列出提醒工具
// ============================================

func listRemindersTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("limit", &tools.Prop{
			Type: "number",
			Desc: "返回数量限制，默认 50",
		})

	ins.Add(unit.Name("list_reminders").
		Desc("列出当前群组的所有待触发的提醒。当用户要求查看提醒列表、有什么闹钟时使用").
		Params(params).
		Func(listRemindersWrap))
}

type listRemindersArgs struct {
	Limit int `json:"limit"`
}

func listRemindersWrap(ctx context.Context, argStr string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	args := &listRemindersArgs{}
	_ = sonic.UnmarshalString(argStr, args)

	req := &ListRemindersRequest{
		ChatID: meta.ChatID,
		Limit:  args.Limit,
	}

	reminders, err := GetService().ListReminders(ctx, req)
	if err != nil {
		return gresult.Err[string](err)
	}

	return gresult.OK(FormatReminderList(reminders))
}

// ============================================
// 删除提醒工具
// ============================================

func deleteReminderTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("id", &tools.Prop{
			Type: "string",
			Desc: "要删除的提醒ID",
		}).
		AddRequired("id")

	ins.Add(unit.Name("delete_reminder").
		Desc("删除指定的提醒。当用户要求取消提醒、删除闹钟时使用").
		Params(params).
		Func(deleteReminderWrap))
}

type deleteReminderArgs struct {
	ID string `json:"id"`
}

func deleteReminderWrap(ctx context.Context, argStr string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	args := &deleteReminderArgs{}
	if err := sonic.UnmarshalString(argStr, args); err != nil {
		return gresult.Err[string](err)
	}

	if err := GetService().DeleteReminder(ctx, args.ID); err != nil {
		return gresult.Err[string](err)
	}

	return gresult.OK(fmt.Sprintf("✅ 提醒已删除！ID: `%s`", args.ID))
}

// ============================================
// 辅助函数
// ============================================

func parseTime(s string) (time.Time, error) {
	// 尝试多种时间格式
	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"2006-01-02",
		"2006/01/02",
	}

	for _, format := range formats {
		if t, err := time.ParseInLocation(format, s, utils.UTC8Loc()); err == nil {
			return t, nil
		}
	}

	// 尝试解析时间戳
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(ts/1000, 0).In(utils.UTC8Loc()), nil
	}

	return time.Time{}, fmt.Errorf("无法解析时间: %s", s)
}
