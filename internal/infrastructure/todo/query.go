package todo

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
)

// Querier 简化的查询接口
type Querier struct {
	db *gorm.DB
}

// NewQuerier 创建查询器
func NewQuerier(db *gorm.DB) *Querier {
	return &Querier{db: db}
}

// WithContext 带上下文
func (q *Querier) WithContext(ctx context.Context) *Querier {
	return &Querier{db: q.db.WithContext(ctx)}
}

// CreateTodoItem 创建待办
func (q *Querier) CreateTodoItem(item *model.TodoItem) error {
	return q.db.Create(item).Error
}

// UpdateTodoItem 更新待办
func (q *Querier) UpdateTodoItem(id string, item *model.TodoItem) (int64, error) {
	result := q.db.Model(&model.TodoItem{}).Where("id = ?", id).Updates(item)
	return result.RowsAffected, result.Error
}

// GetTodoItemByID 根据ID获取待办
func (q *Querier) GetTodoItemByID(id string) (*model.TodoItem, error) {
	var item model.TodoItem
	err := q.db.Where("id = ?", id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// ListTodoItemsByChatID 获取群待办列表
func (q *Querier) ListTodoItemsByChatID(chatID string, status *string, limit, offset int) ([]*model.TodoItem, error) {
	var items []*model.TodoItem
	query := q.db.Where("chat_id = ?", chatID)
	if status != nil {
		query = query.Where("status = ?", *status)
	}
	err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

// DeleteTodoItem 删除待办
func (q *Querier) DeleteTodoItem(id string) (int64, error) {
	result := q.db.Where("id = ?", id).Delete(&model.TodoItem{})
	return result.RowsAffected, result.Error
}
