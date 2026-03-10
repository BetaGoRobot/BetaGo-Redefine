package todo

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	domaintodo "github.com/BetaGoRobot/BetaGo-Redefine/internal/domain/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/lib/pq"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
)

// Repository 待办事项仓储实现
type Repository struct {
	q *Querier
}

// NewRepository 创建仓储
func NewRepository(db *gorm.DB, identity botidentity.Identity) *Repository {
	return &Repository{
		q: NewQuerier(db, identity),
	}
}

// CreateTodo 创建待办事项
func (r *Repository) CreateTodo(ctx context.Context, t *domaintodo.Todo) error {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("todo.id", t.ID),
		attribute.String("chat.id", t.ChatID),
		attribute.String("todo.status", string(t.Status)),
		attribute.Int("todo.title.len", len(t.Title)),
	)
	defer span.End()

	item := toDBTodo(t)
	err := r.q.WithContext(ctx).CreateTodoItem(item)
	otel.RecordError(span, err)
	return err
}

// UpdateTodo 更新待办事项
func (r *Repository) UpdateTodo(ctx context.Context, t *domaintodo.Todo) error {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("todo.id", t.ID),
		attribute.String("chat.id", t.ChatID),
		attribute.String("todo.status", string(t.Status)),
	)
	defer span.End()

	item := toDBTodo(t)
	_, err := r.q.WithContext(ctx).UpdateTodoItem(t.ID, item)
	otel.RecordError(span, err)
	return err
}

// GetTodoByID 根据ID获取待办事项
func (r *Repository) GetTodoByID(ctx context.Context, id string) (*domaintodo.Todo, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("todo.id", id))
	defer span.End()

	item, err := r.q.WithContext(ctx).GetTodoItemByID(id)
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.String("chat.id", item.ChatID))
	return toDomainTodo(item), nil
}

// ListTodosByChatID 获取群组的待办事项列表
func (r *Repository) ListTodosByChatID(ctx context.Context, chatID string, status *domaintodo.TodoStatus, limit, offset int) ([]*domaintodo.Todo, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("chat.id", chatID),
		attribute.Int("limit", limit),
		attribute.Int("offset", offset),
	)
	if status != nil {
		span.SetAttributes(attribute.String("todo.status", string(*status)))
	}
	defer span.End()

	var statusStr *string
	if status != nil {
		s := string(*status)
		statusStr = &s
	}
	items, err := r.q.WithContext(ctx).ListTodoItemsByChatID(chatID, statusStr, limit, offset)
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("todo.count", len(items)))

	// 转换为领域模型
	result := make([]*domaintodo.Todo, 0, len(items))
	for _, item := range items {
		result = append(result, toDomainTodo(item))
	}
	return result, nil
}

// DeleteTodo 删除待办事项
func (r *Repository) DeleteTodo(ctx context.Context, id string) error {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("todo.id", id))
	defer span.End()

	// 删除待办
	_, err := r.q.WithContext(ctx).DeleteTodoItem(id)
	otel.RecordError(span, err)
	return err
}

// 转换函数
func toDBTodo(t *domaintodo.Todo) *model.TodoItem {
	item := &model.TodoItem{
		ID:          t.ID,
		ChatID:      t.ChatID,
		AppID:       t.AppID,
		BotOpenID:   t.BotOpenID,
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
		AppID:       item.AppID,
		BotOpenID:   item.BotOpenID,
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
