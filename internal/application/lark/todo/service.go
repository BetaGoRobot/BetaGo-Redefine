package todo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/domain/todo"
	todorepo "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"go.uber.org/zap"
)

type TodoService interface {
	CreateTodo(ctx context.Context, req *CreateTodoRequest) (*todo.Todo, error)
	UpdateTodo(ctx context.Context, req *UpdateTodoRequest) (*todo.Todo, error)
	GetTodo(ctx context.Context, id string) (*todo.Todo, error)
	ListTodos(ctx context.Context, req *ListTodosRequest) ([]*todo.Todo, error)
	DeleteTodo(ctx context.Context, id string) error
	CreateReminder(ctx context.Context, req *CreateReminderRequest) (*todo.Reminder, error)
	UpdateReminder(ctx context.Context, req *UpdateReminderRequest) (*todo.Reminder, error)
	GetReminder(ctx context.Context, id string) (*todo.Reminder, error)
	ListReminders(ctx context.Context, req *ListRemindersRequest) ([]*todo.Reminder, error)
	DeleteReminder(ctx context.Context, id string) error
	GetPendingReminders(ctx context.Context, limit int) ([]*todo.Reminder, error)
	MarkReminderTriggered(ctx context.Context, id string) error
	Available() bool
}

var errServiceUnavailable = errors.New("todo service unavailable")

type noopService struct {
	reason string
}

func (s noopService) unavailableErr() error {
	if s.reason == "" {
		return errServiceUnavailable
	}
	return fmt.Errorf("%w: %s", errServiceUnavailable, s.reason)
}

func (s noopService) CreateTodo(context.Context, *CreateTodoRequest) (*todo.Todo, error) {
	return nil, s.unavailableErr()
}

func (s noopService) UpdateTodo(context.Context, *UpdateTodoRequest) (*todo.Todo, error) {
	return nil, s.unavailableErr()
}

func (s noopService) GetTodo(context.Context, string) (*todo.Todo, error) {
	return nil, s.unavailableErr()
}

func (s noopService) ListTodos(context.Context, *ListTodosRequest) ([]*todo.Todo, error) {
	return nil, s.unavailableErr()
}

func (s noopService) DeleteTodo(context.Context, string) error {
	return s.unavailableErr()
}

func (s noopService) CreateReminder(context.Context, *CreateReminderRequest) (*todo.Reminder, error) {
	return nil, s.unavailableErr()
}

func (s noopService) UpdateReminder(context.Context, *UpdateReminderRequest) (*todo.Reminder, error) {
	return nil, s.unavailableErr()
}

func (s noopService) GetReminder(context.Context, string) (*todo.Reminder, error) {
	return nil, s.unavailableErr()
}

func (s noopService) ListReminders(context.Context, *ListRemindersRequest) ([]*todo.Reminder, error) {
	return nil, s.unavailableErr()
}

func (s noopService) DeleteReminder(context.Context, string) error {
	return s.unavailableErr()
}

func (s noopService) GetPendingReminders(context.Context, int) ([]*todo.Reminder, error) {
	return nil, nil
}

func (s noopService) MarkReminderTriggered(context.Context, string) error {
	return s.unavailableErr()
}

func (s noopService) Available() bool {
	return false
}

// Service 待办事项应用服务
type Service struct {
	repo *todorepo.Repository
}

// NewService 创建服务
func NewService(repo *todorepo.Repository) *Service {
	return &Service{
		repo: repo,
	}
}

func (s *Service) Available() bool {
	return s != nil && s.repo != nil
}

// CreateTodoRequest 创建待办请求
type CreateTodoRequest struct {
	ChatID      string
	CreatorID   string
	CreatorName string
	Title       string
	Description string
	Priority    string
	DueAt       *time.Time
	AssigneeID  *string
	Tags        []string
}

// CreateTodo 创建待办事项
func (s *Service) CreateTodo(ctx context.Context, req *CreateTodoRequest) (*todo.Todo, error) {
	priority := todo.TodoPriorityMedium
	if req.Priority != "" {
		priority = todo.TodoPriority(strings.ToLower(req.Priority))
	}

	t := todo.NewTodo(req.ChatID, req.CreatorID, req.CreatorName, req.Title, req.Description, priority)
	if req.DueAt != nil {
		t.SetDueDate(*req.DueAt)
	}
	if req.AssigneeID != nil {
		t.AssignTo(*req.AssigneeID)
	}
	for _, tag := range req.Tags {
		if tag != "" {
			t.AddTag(tag)
		}
	}

	if err := s.repo.CreateTodo(ctx, t); err != nil {
		logs.L().Ctx(ctx).Error("Create todo failed", zap.Error(err))
		return nil, err
	}
	return t, nil
}

// UpdateTodoRequest 更新待办请求
type UpdateTodoRequest struct {
	ID          string
	Title       *string
	Description *string
	Status      *string
	Priority    *string
	DueAt       *time.Time
	AssigneeID  *string
	AddTags     []string
	RemoveTags  []string
}

// UpdateTodo 更新待办事项
func (s *Service) UpdateTodo(ctx context.Context, req *UpdateTodoRequest) (*todo.Todo, error) {
	t, err := s.repo.GetTodoByID(ctx, req.ID)
	if err != nil {
		return nil, err
	}

	if req.Title != nil {
		t.Title = *req.Title
	}
	if req.Description != nil {
		t.Description = *req.Description
	}
	if req.Status != nil {
		t.UpdateStatus(todo.TodoStatus(*req.Status))
	}
	if req.Priority != nil {
		t.Priority = todo.TodoPriority(*req.Priority)
	}
	if req.DueAt != nil {
		t.SetDueDate(*req.DueAt)
	}
	if req.AssigneeID != nil {
		t.AssignTo(*req.AssigneeID)
	}
	for _, tag := range req.AddTags {
		t.AddTag(tag)
	}
	if len(req.RemoveTags) > 0 {
		newTags := make([]string, 0)
		for _, tag := range t.Tags {
			keep := true
			for _, rt := range req.RemoveTags {
				if tag == rt {
					keep = false
					break
				}
			}
			if keep {
				newTags = append(newTags, tag)
			}
		}
		t.Tags = newTags
	}

	if err := s.repo.UpdateTodo(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// GetTodo 获取待办事项
func (s *Service) GetTodo(ctx context.Context, id string) (*todo.Todo, error) {
	return s.repo.GetTodoByID(ctx, id)
}

// ListTodosRequest 获取待办列表请求
type ListTodosRequest struct {
	ChatID string
	Status *string
	Limit  int
	Offset int
}

// ListTodos 获取待办列表
func (s *Service) ListTodos(ctx context.Context, req *ListTodosRequest) ([]*todo.Todo, error) {
	var status *todo.TodoStatus
	if req.Status != nil {
		s := todo.TodoStatus(*req.Status)
		status = &s
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}
	return s.repo.ListTodosByChatID(ctx, req.ChatID, status, req.Limit, req.Offset)
}

// DeleteTodo 删除待办事项
func (s *Service) DeleteTodo(ctx context.Context, id string) error {
	return s.repo.DeleteTodo(ctx, id)
}

// CreateReminderRequest 创建提醒请求
type CreateReminderRequest struct {
	ChatID     string
	CreatorID  string
	TodoID     string
	Title      string
	Content    string
	Type       string
	TriggerAt  time.Time
	RepeatRule string
}

// CreateReminder 创建提醒
func (s *Service) CreateReminder(ctx context.Context, req *CreateReminderRequest) (*todo.Reminder, error) {
	remType := todo.ReminderTypeOnce
	if req.Type != "" {
		remType = todo.ReminderType(strings.ToLower(req.Type))
	}

	rem := todo.NewReminder(req.ChatID, req.CreatorID, req.Title, req.Content, req.TriggerAt, remType)
	rem.TodoID = req.TodoID
	rem.RepeatRule = req.RepeatRule

	if err := s.repo.CreateReminder(ctx, rem); err != nil {
		return nil, err
	}

	// 如果关联了待办，同时更新待办
	if req.TodoID != "" {
		if t, err := s.repo.GetTodoByID(ctx, req.TodoID); err == nil {
			t.AddReminder(rem)
			_ = s.repo.UpdateTodo(ctx, t)
		}
	}

	return rem, nil
}

// UpdateReminderRequest 更新提醒请求
type UpdateReminderRequest struct {
	ID         string
	Title      *string
	Content    *string
	Status     *string
	TriggerAt  *time.Time
	RepeatRule *string
}

// UpdateReminder 更新提醒
func (s *Service) UpdateReminder(ctx context.Context, req *UpdateReminderRequest) (*todo.Reminder, error) {
	rem, err := s.repo.GetReminderByID(ctx, req.ID)
	if err != nil {
		return nil, err
	}

	if req.Title != nil {
		rem.Title = *req.Title
	}
	if req.Content != nil {
		rem.Content = *req.Content
	}
	if req.Status != nil {
		status := todo.ReminderStatus(*req.Status)
		if status == todo.ReminderStatusCancelled {
			rem.Cancel()
		} else if status == todo.ReminderStatusTriggered {
			rem.MarkTriggered()
		}
	}
	if req.TriggerAt != nil {
		rem.TriggerAt = *req.TriggerAt
	}
	if req.RepeatRule != nil {
		rem.RepeatRule = *req.RepeatRule
	}

	if err := s.repo.UpdateReminder(ctx, rem); err != nil {
		return nil, err
	}
	return rem, nil
}

// GetReminder 获取提醒
func (s *Service) GetReminder(ctx context.Context, id string) (*todo.Reminder, error) {
	return s.repo.GetReminderByID(ctx, id)
}

// ListRemindersRequest 获取提醒列表请求
type ListRemindersRequest struct {
	ChatID string
	Limit  int
	Offset int
}

// ListReminders 获取提醒列表
func (s *Service) ListReminders(ctx context.Context, req *ListRemindersRequest) ([]*todo.Reminder, error) {
	if req.Limit <= 0 {
		req.Limit = 50
	}
	return s.repo.ListRemindersByChatID(ctx, req.ChatID, req.Limit, req.Offset)
}

// DeleteReminder 删除提醒
func (s *Service) DeleteReminder(ctx context.Context, id string) error {
	return s.repo.DeleteReminder(ctx, id)
}

// GetPendingReminders 获取待触发的提醒（用于调度器）
func (s *Service) GetPendingReminders(ctx context.Context, limit int) ([]*todo.Reminder, error) {
	return s.repo.ListPendingReminders(ctx, time.Now(), limit)
}

// MarkReminderTriggered 标记提醒已触发
func (s *Service) MarkReminderTriggered(ctx context.Context, id string) error {
	rem, err := s.repo.GetReminderByID(ctx, id)
	if err != nil {
		return err
	}
	rem.MarkTriggered()
	return s.repo.UpdateReminder(ctx, rem)
}

// FormatTodoList 格式化待办列表为文本
func FormatTodoList(todos []*todo.Todo) string {
	if len(todos) == 0 {
		return "暂无待办事项 🎉"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 待办事项列表（共 %d 项）\n\n", len(todos)))

	for i, t := range todos {
		statusIcon := getStatusIcon(t.Status)
		priorityIcon := getPriorityIcon(t.Priority)

		sb.WriteString(fmt.Sprintf("%d. %s %s **%s**", i+1, statusIcon, priorityIcon, t.Title))

		if t.Description != "" {
			desc := t.Description
			if len(desc) > 50 {
				desc = desc[:50] + "..."
			}
			sb.WriteString(fmt.Sprintf("\n   %s", desc))
		}

		meta := make([]string, 0)
		if t.DueAt != nil {
			meta = append(meta, fmt.Sprintf("截止: %s", t.DueAt.In(utils.UTC8Loc()).Format("2006-01-02 15:04:05")))
		}
		if t.AssigneeID != "" {
			meta = append(meta, fmt.Sprintf("负责人: %s", t.AssigneeID))
		}
		if len(t.Tags) > 0 {
			meta = append(meta, fmt.Sprintf("标签: %s", strings.Join(t.Tags, ", ")))
		}
		if len(meta) > 0 {
			sb.WriteString(fmt.Sprintf("\n   %s", strings.Join(meta, " | ")))
		}
		sb.WriteString(fmt.Sprintf("\n   ID: `%s`\n\n", t.ID))
	}

	return sb.String()
}

// FormatReminderList 格式化提醒列表为文本
func FormatReminderList(reminders []*todo.Reminder) string {
	if len(reminders) == 0 {
		return "暂无提醒 ⏰"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⏰ 提醒列表（共 %d 项）\n\n", len(reminders)))

	for i, rem := range reminders {
		typeIcon := getReminderTypeIcon(rem.Type)
		sb.WriteString(fmt.Sprintf("%d. %s **%s**\n", i+1, typeIcon, rem.Title))

		if rem.Content != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", rem.Content))
		}

		sb.WriteString(fmt.Sprintf("   触发时间: %s\n", rem.TriggerAt.In(utils.UTC8Loc()).Format("2006-01-02 15:04:05")))
		sb.WriteString(fmt.Sprintf("   ID: `%s`\n\n", rem.ID))
	}

	return sb.String()
}

func getStatusIcon(status todo.TodoStatus) string {
	switch status {
	case todo.TodoStatusPending:
		return "⭕"
	case todo.TodoStatusDoing:
		return "🔄"
	case todo.TodoStatusDone:
		return "✅"
	case todo.TodoStatusCancelled:
		return "❌"
	default:
		return "⭕"
	}
}

func getPriorityIcon(priority todo.TodoPriority) string {
	switch priority {
	case todo.TodoPriorityUrgent:
		return "🔴"
	case todo.TodoPriorityHigh:
		return "🟠"
	case todo.TodoPriorityMedium:
		return "🟡"
	case todo.TodoPriorityLow:
		return "🟢"
	default:
		return "🟡"
	}
}

func getReminderTypeIcon(remType todo.ReminderType) string {
	switch remType {
	case todo.ReminderTypeOnce:
		return "⏰"
	case todo.ReminderTypeDaily:
		return "📅"
	case todo.ReminderTypeWeekly:
		return "📆"
	case todo.ReminderTypeMonthly:
		return "🗓️"
	default:
		return "⏰"
	}
}
