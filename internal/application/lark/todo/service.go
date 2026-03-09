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
