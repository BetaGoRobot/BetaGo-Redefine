package todo

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
)

// Querier 简化的查询接口
type Querier struct {
	db       *gorm.DB
	identity botidentity.Identity
}

// NewQuerier 创建查询器
func NewQuerier(db *gorm.DB, identity botidentity.Identity) *Querier {
	return &Querier{db: db, identity: identity}
}

// WithContext 带上下文
func (q *Querier) WithContext(ctx context.Context) *Querier {
	return &Querier{db: q.db.WithContext(ctx), identity: q.identity}
}

func (q *Querier) scopedDB() *gorm.DB {
	db := q.db
	if q.identity.AppID != "" {
		db = db.Where("app_id = ?", q.identity.AppID)
	}
	if q.identity.BotOpenID != "" {
		db = db.Where("bot_open_id = ?", q.identity.BotOpenID)
	}
	return db
}

// CreateTodoItem 创建待办
func (q *Querier) CreateTodoItem(item *model.TodoItem) error {
	return q.db.Create(item).Error
}

// UpdateTodoItem 更新待办
func (q *Querier) UpdateTodoItem(id string, item *model.TodoItem) (int64, error) {
	result := q.scopedDB().Model(&model.TodoItem{}).Where("id = ?", id).Updates(item)
	return result.RowsAffected, result.Error
}

// GetTodoItemByID 根据ID获取待办
func (q *Querier) GetTodoItemByID(id string) (*model.TodoItem, error) {
	var item model.TodoItem
	err := q.scopedDB().Where("id = ?", id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// ListTodoItemsByChatID 获取群待办列表
func (q *Querier) ListTodoItemsByChatID(chatID string, status *string, limit, offset int) ([]*model.TodoItem, error) {
	var items []*model.TodoItem
	query := q.scopedDB().Where("chat_id = ?", chatID)
	if status != nil {
		query = query.Where("status = ?", *status)
	}
	err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

// DeleteTodoItem 删除待办
func (q *Querier) DeleteTodoItem(id string) (int64, error) {
	result := q.scopedDB().Where("id = ?", id).Delete(&model.TodoItem{})
	return result.RowsAffected, result.Error
}
