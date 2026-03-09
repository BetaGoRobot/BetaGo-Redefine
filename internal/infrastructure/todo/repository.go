package todo

import (
	"context"
	"time"

	domaintodo "github.com/BetaGoRobot/BetaGo-Redefine/internal/domain/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/lib/pq"
	"go.uber.org/zap"
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
	// 获取关联的提醒
	reminders, err := r.q.WithContext(ctx).ListTodoRemindersByTodoIDs([]string{id})
	if err != nil {
		logs.L().Ctx(ctx).Warn("Get reminders failed", zap.Error(err))
	}
	return toDomainTodo(item, reminders), nil
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

	// 获取所有待办的ID
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	// 批量获取提醒
	reminderMap := make(map[string][]*model.TodoReminder)
	if len(ids) > 0 {
		reminders, err := r.q.WithContext(ctx).ListTodoRemindersByTodoIDs(ids)
		if err == nil {
			for _, rem := range reminders {
				reminderMap[rem.TodoID] = append(reminderMap[rem.TodoID], rem)
			}
		}
	}

	// 转换为领域模型
	result := make([]*domaintodo.Todo, 0, len(items))
	for _, item := range items {
		result = append(result, toDomainTodo(item, reminderMap[item.ID]))
	}
	return result, nil
}

// DeleteTodo 删除待办事项
func (r *Repository) DeleteTodo(ctx context.Context, id string) error {
	// 先删除关联的提醒
	_, err := r.q.WithContext(ctx).DeleteTodoRemindersByTodoID(id)
	if err != nil {
		logs.L().Ctx(ctx).Warn("Delete reminders failed", zap.Error(err))
	}
	// 删除待办
	_, err = r.q.WithContext(ctx).DeleteTodoItem(id)
	return err
}

// CreateReminder 创建提醒
func (r *Repository) CreateReminder(ctx context.Context, rem *domaintodo.Reminder) error {
	item := toDBReminder(rem)
	return r.q.WithContext(ctx).CreateTodoReminder(item)
}

// UpdateReminder 更新提醒
func (r *Repository) UpdateReminder(ctx context.Context, rem *domaintodo.Reminder) error {
	item := toDBReminder(rem)
	_, err := r.q.WithContext(ctx).UpdateTodoReminder(rem.ID, item)
	return err
}

// GetReminderByID 根据ID获取提醒
func (r *Repository) GetReminderByID(ctx context.Context, id string) (*domaintodo.Reminder, error) {
	item, err := r.q.WithContext(ctx).GetTodoReminderByID(id)
	if err != nil {
		return nil, err
	}
	return toDomainReminder(item), nil
}

// ListPendingReminders 获取待触发的提醒
func (r *Repository) ListPendingReminders(ctx context.Context, before time.Time, limit int) ([]*domaintodo.Reminder, error) {
	items, err := r.q.WithContext(ctx).ListPendingReminders(before, limit)
	if err != nil {
		return nil, err
	}

	result := make([]*domaintodo.Reminder, 0, len(items))
	for _, item := range items {
		result = append(result, toDomainReminder(item))
	}
	return result, nil
}

// ListRemindersByChatID 获取群组的提醒列表
func (r *Repository) ListRemindersByChatID(ctx context.Context, chatID string, limit, offset int) ([]*domaintodo.Reminder, error) {
	items, err := r.q.WithContext(ctx).ListTodoRemindersByChatID(chatID, limit, offset)
	if err != nil {
		return nil, err
	}

	result := make([]*domaintodo.Reminder, 0, len(items))
	for _, item := range items {
		result = append(result, toDomainReminder(item))
	}
	return result, nil
}

// DeleteReminder 删除提醒
func (r *Repository) DeleteReminder(ctx context.Context, id string) error {
	_, err := r.q.WithContext(ctx).DeleteTodoReminder(id)
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

func toDomainTodo(item *model.TodoItem, reminders []*model.TodoReminder) *domaintodo.Todo {
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
		Reminders:   make([]*domaintodo.Reminder, 0, len(reminders)),
	}
	for _, rem := range reminders {
		t.Reminders = append(t.Reminders, toDomainReminder(rem))
	}
	return t
}

func toDBReminder(rem *domaintodo.Reminder) *model.TodoReminder {
	return &model.TodoReminder{
		ID:         rem.ID,
		TodoID:     rem.TodoID,
		ChatID:     rem.ChatID,
		CreatorID:  rem.CreatorID,
		Title:      rem.Title,
		Content:    rem.Content,
		Type:       string(rem.Type),
		Status:     string(rem.Status),
		TriggerAt:  rem.TriggerAt,
		RepeatRule: rem.RepeatRule,
		CreatedAt:  rem.CreatedAt,
		UpdatedAt:  rem.UpdatedAt,
	}
}

func toDomainReminder(item *model.TodoReminder) *domaintodo.Reminder {
	return &domaintodo.Reminder{
		ID:         item.ID,
		TodoID:     item.TodoID,
		ChatID:     item.ChatID,
		CreatorID:  item.CreatorID,
		Title:      item.Title,
		Content:    item.Content,
		Type:       domaintodo.ReminderType(item.Type),
		Status:     domaintodo.ReminderStatus(item.Status),
		TriggerAt:  item.TriggerAt,
		RepeatRule: item.RepeatRule,
		CreatedAt:  item.CreatedAt,
		UpdatedAt:  item.UpdatedAt,
	}
}
