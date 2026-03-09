package todo

import (
	"context"

	domaintodo "github.com/BetaGoRobot/BetaGo-Redefine/internal/domain/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// Repository 待办事项仓储实现
type Repository struct {
	q *Querier
}

// NewRepository 创建仓储
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{
		q: NewQuerier(db),
	}
}

// CreateTodo 创建待办事项
func (r *Repository) CreateTodo(ctx context.Context, t *domaintodo.Todo) error {
	item := toDBTodo(t)
	return r.q.WithContext(ctx).CreateTodoItem(item)
}

// UpdateTodo 更新待办事项
func (r *Repository) UpdateTodo(ctx context.Context, t *domaintodo.Todo) error {
	item := toDBTodo(t)
	_, err := r.q.WithContext(ctx).UpdateTodoItem(t.ID, item)
	return err
}

// GetTodoByID 根据ID获取待办事项
func (r *Repository) GetTodoByID(ctx context.Context, id string) (*domaintodo.Todo, error) {
	item, err := r.q.WithContext(ctx).GetTodoItemByID(id)
	if err != nil {
		return nil, err
	}
	return toDomainTodo(item), nil
}

// ListTodosByChatID 获取群组的待办事项列表
func (r *Repository) ListTodosByChatID(ctx context.Context, chatID string, status *domaintodo.TodoStatus, limit, offset int) ([]*domaintodo.Todo, error) {
	var statusStr *string
	if status != nil {
		s := string(*status)
		statusStr = &s
	}
	items, err := r.q.WithContext(ctx).ListTodoItemsByChatID(chatID, statusStr, limit, offset)
	if err != nil {
		return nil, err
	}

	// 转换为领域模型
	result := make([]*domaintodo.Todo, 0, len(items))
	for _, item := range items {
		result = append(result, toDomainTodo(item))
	}
	return result, nil
}

// DeleteTodo 删除待办事项
func (r *Repository) DeleteTodo(ctx context.Context, id string) error {
	// 删除待办
	_, err := r.q.WithContext(ctx).DeleteTodoItem(id)
	return err
}

// 转换函数
func toDBTodo(t *domaintodo.Todo) *model.TodoItem {
	item := &model.TodoItem{
		ID:          t.ID,
		ChatID:      t.ChatID,
		CreatorID:   t.CreatorID,
		CreatorName: t.CreatorName,
		AssigneeID:  t.AssigneeID,
		Title:       t.Title,
		Description: t.Description,
		Status:      string(t.Status),
		Priority:    string(t.Priority),
		DueAt:       t.DueAt,
		CompletedAt: t.CompletedAt,
		Tags:        pq.StringArray(t.Tags),
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
	return item
}

func toDomainTodo(item *model.TodoItem) *domaintodo.Todo {
	t := &domaintodo.Todo{
		ID:          item.ID,
		ChatID:      item.ChatID,
		CreatorID:   item.CreatorID,
		CreatorName: item.CreatorName,
		AssigneeID:  item.AssigneeID,
		Title:       item.Title,
		Description: item.Description,
		Status:      domaintodo.TodoStatus(item.Status),
		Priority:    domaintodo.TodoPriority(item.Priority),
		DueAt:       item.DueAt,
		CompletedAt: item.CompletedAt,
		Tags:        []string(item.Tags),
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
	return t
}
