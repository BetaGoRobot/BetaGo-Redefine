package todo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	globalService     TodoService = noopService{reason: "todo service not initialized"}
	serviceRegistry               = make(map[string]TodoService)
	serviceRegistryMu sync.RWMutex
	warnOnce          sync.Once
)

const todoToolResultKey = "todo_tool_result"

// Init 初始化待办服务（需要在应用启动时调用）
func Init(db *gorm.DB) {
	if db == nil {
		setNoopService("todo db unavailable")
		return
	}
	identity := botidentity.Current()
	if err := identity.Validate(); err != nil {
		setNoopService(err.Error())
		return
	}
	repo := todo.NewRepository(db, identity)
	serviceRegistryMu.Lock()
	serviceRegistry[todoServiceKey(identity)] = NewService(repo, identity)
	serviceRegistryMu.Unlock()
}

// GetService 获取全局服务实例
func GetService() TodoService {
	identity := botidentity.Current()
	if identity.Valid() {
		serviceRegistryMu.RLock()
		service, ok := serviceRegistry[todoServiceKey(identity)]
		serviceRegistryMu.RUnlock()
		if ok {
			return service
		}
	}
	return globalService
}

func todoServiceKey(identity botidentity.Identity) string {
	return identity.NamespaceKey("todo_service")
}

func setNoopService(reason string) {
	globalService = noopService{reason: reason}
	warnOnce.Do(func() {
		logs.L().Warn("Todo service disabled, falling back to noop",
			zap.String("reason", reason),
		)
	})
}

// RegisterTools 注册待办相关的 Function Call 工具
func RegisterTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, CreateTodo)
	xcommand.RegisterTool(ins, UpdateTodo)
	xcommand.RegisterTool(ins, ListTodos)
	xcommand.RegisterTool(ins, DeleteTodo)
}

type createTodoArgs struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	DueAt       string `json:"due_at"`
	AssigneeID  string `json:"assignee_id"`
	Tags        string `json:"tags"`
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

type listTodosArgs struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

type deleteTodoArgs struct {
	ID string `json:"id"`
}

type createTodoHandler struct{}

type updateTodoHandler struct{}

type listTodosHandler struct{}

type deleteTodoHandler struct{}

var CreateTodo createTodoHandler
var UpdateTodo updateTodoHandler
var ListTodos listTodosHandler
var DeleteTodo deleteTodoHandler

func toolResultSpec(name, desc string, params *tools.Param) xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name:   name,
		Desc:   desc,
		Params: params,
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(todoToolResultKey)
			return result
		},
	}
}

func (createTodoHandler) ParseTool(raw string) (createTodoArgs, error) {
	parsed := createTodoArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return createTodoArgs{}, err
	}
	return parsed, nil
}

func (createTodoHandler) ToolSpec() xcommand.ToolSpec {
	return toolResultSpec(
		"create_todo",
		"创建一个新的待办事项。当用户要求记录任务、添加待办、设置提醒事项时使用此工具",
		tools.NewParams("object").
			AddProp("title", &tools.Prop{Type: "string", Desc: "待办事项的标题，必填"}).
			AddProp("description", &tools.Prop{Type: "string", Desc: "待办事项的详细描述，可选"}).
			AddProp("priority", &tools.Prop{Type: "string", Desc: "优先级: low(低), medium(中), high(高), urgent(紧急)，默认 medium"}).
			AddProp("due_at", &tools.Prop{Type: "string", Desc: "截止时间，格式为 RFC3339 或 YYYY-MM-DD HH:MM:SS，可选"}).
			AddProp("assignee_id", &tools.Prop{Type: "string", Desc: "负责人的飞书用户ID，可选"}).
			AddProp("tags", &tools.Prop{Type: "string", Desc: "标签，多个标签用逗号分隔，可选"}).
			AddRequired("title"),
	)
}

func (createTodoHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args createTodoArgs) error {
	userInfo, err := larkuser.GetUserInfoCache(ctx, metaData.ChatID, metaData.OpenID)
	if err != nil {
		logs.L().Ctx(ctx).Warn("Get user info failed", zap.Error(err))
	}
	userName := ""
	if userInfo != nil && userInfo.Name != nil {
		userName = *userInfo.Name
	}

	var dueAt *time.Time
	if args.DueAt != "" {
		t, err := parseTime(args.DueAt)
		if err == nil {
			dueAt = &t
		}
	}

	tags := splitTags(args.Tags)
	req := &CreateTodoRequest{
		ChatID:      metaData.ChatID,
		CreatorID:   metaData.OpenID,
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
		return err
	}

	result := fmt.Sprintf("✅ 待办创建成功！\n\n标题: %s\nID: `%s`", t.Title, t.ID)
	if t.Description != "" {
		result += fmt.Sprintf("\n描述: %s", t.Description)
	}
	if t.DueAt != nil {
		result += fmt.Sprintf("\n截止: %s", t.DueAt.In(utils.UTC8Loc()).Format("2006-01-02 15:04:05"))
	}
	metaData.SetExtra(todoToolResultKey, result)
	return nil
}

func (updateTodoHandler) ParseTool(raw string) (updateTodoArgs, error) {
	parsed := updateTodoArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return updateTodoArgs{}, err
	}
	return parsed, nil
}

func (updateTodoHandler) ToolSpec() xcommand.ToolSpec {
	return toolResultSpec(
		"update_todo",
		"更新待办事项的状态、标题、描述、截止时间等。当用户要求完成任务、修改待办、设置状态时使用",
		tools.NewParams("object").
			AddProp("id", &tools.Prop{Type: "string", Desc: "待办事项的ID，必填"}).
			AddProp("title", &tools.Prop{Type: "string", Desc: "新的标题，可选"}).
			AddProp("description", &tools.Prop{Type: "string", Desc: "新的描述，可选"}).
			AddProp("status", &tools.Prop{Type: "string", Desc: "状态: pending(待处理), doing(进行中), done(已完成), cancelled(已取消)"}).
			AddProp("priority", &tools.Prop{Type: "string", Desc: "优先级: low, medium, high, urgent"}).
			AddProp("due_at", &tools.Prop{Type: "string", Desc: "新的截止时间，格式 RFC3339 或 YYYY-MM-DD HH:MM:SS"}).
			AddProp("add_tags", &tools.Prop{Type: "string", Desc: "要添加的标签，多个用逗号分隔"}).
			AddProp("remove_tags", &tools.Prop{Type: "string", Desc: "要移除的标签，多个用逗号分隔"}).
			AddRequired("id"),
	)
}

func (updateTodoHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args updateTodoArgs) error {
	req := &UpdateTodoRequest{ID: args.ID}
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
		req.AddTags = splitTags(args.AddTags)
	}
	if args.RemoveTags != "" {
		req.RemoveTags = splitTags(args.RemoveTags)
	}

	t, err := GetService().UpdateTodo(ctx, req)
	if err != nil {
		return err
	}

	result := fmt.Sprintf("✅ 待办更新成功！\n\n标题: %s\n状态: %s", t.Title, t.Status)
	if args.Status == "done" {
		result = fmt.Sprintf("🎉 恭喜完成任务！\n\n标题: %s", t.Title)
	}
	metaData.SetExtra(todoToolResultKey, result)
	return nil
}

func (listTodosHandler) ParseTool(raw string) (listTodosArgs, error) {
	parsed := listTodosArgs{}
	if raw == "" || raw == "{}" {
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return listTodosArgs{}, err
	}
	return parsed, nil
}

func (listTodosHandler) ToolSpec() xcommand.ToolSpec {
	return toolResultSpec(
		"list_todos",
		"列出当前群组的所有待办事项。当用户要求查看待办、任务列表、有什么任务时使用",
		tools.NewParams("object").
			AddProp("status", &tools.Prop{Type: "string", Desc: "过滤状态: pending, doing, done, cancelled，不填则列出所有"}).
			AddProp("limit", &tools.Prop{Type: "number", Desc: "返回数量限制，默认 50"}),
	)
}

func (listTodosHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args listTodosArgs) error {
	req := &ListTodosRequest{ChatID: metaData.ChatID, Limit: args.Limit}
	if args.Status != "" {
		req.Status = &args.Status
	}
	todos, err := GetService().ListTodos(ctx, req)
	if err != nil {
		return err
	}
	metaData.SetExtra(todoToolResultKey, FormatTodoList(todos))
	return nil
}

func (deleteTodoHandler) ParseTool(raw string) (deleteTodoArgs, error) {
	parsed := deleteTodoArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return deleteTodoArgs{}, err
	}
	return parsed, nil
}

func (deleteTodoHandler) ToolSpec() xcommand.ToolSpec {
	return toolResultSpec(
		"delete_todo",
		"删除指定的待办事项。当用户要求删除任务、移除待办时使用",
		tools.NewParams("object").
			AddProp("id", &tools.Prop{Type: "string", Desc: "要删除的待办事项ID"}).
			AddRequired("id"),
	)
}

func (deleteTodoHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args deleteTodoArgs) error {
	if err := GetService().DeleteTodo(ctx, args.ID); err != nil {
		return err
	}
	metaData.SetExtra(todoToolResultKey, fmt.Sprintf("✅ 待办已删除！ID: `%s`", args.ID))
	return nil
}

func splitTags(input string) []string {
	if input == "" {
		return nil
	}
	tags := make([]string, 0)
	for _, tag := range strings.Split(input, ",") {
		if t := strings.TrimSpace(tag); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// ============================================
// 辅助函数
// ============================================

func parseTime(s string) (time.Time, error) {
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

	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(ts/1000, 0).In(utils.UTC8Loc()), nil
	}

	return time.Time{}, fmt.Errorf("无法解析时间: %s", s)
}
