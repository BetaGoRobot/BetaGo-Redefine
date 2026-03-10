package todo

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"gorm.io/gorm"
)

// Querier 简化的查询接口
type Querier struct {
	q        *query.Query
	ctx      context.Context
	identity botidentity.Identity
}

// NewQuerier 创建查询器
func NewQuerier(db *gorm.DB, identity botidentity.Identity) *Querier {
	return &Querier{
		q:        query.Use(infraDB.WithoutQueryCache(db)),
		identity: identity,
	}
}

// WithContext 带上下文
func (q *Querier) WithContext(ctx context.Context) *Querier {
	return &Querier{q: q.q, ctx: ctx, identity: q.identity}
}

func (q *Querier) queryContext() context.Context {
	if q.ctx != nil {
		return q.ctx
	}
	return context.Background()
}

func (q *Querier) scopedTodoItem() query.ITodoItemDo {
	ins := q.q.TodoItem
	todoQuery := ins.WithContext(q.queryContext())
	if q.identity.AppID != "" {
		todoQuery = todoQuery.Where(ins.AppID.Eq(q.identity.AppID))
	}
	if q.identity.BotOpenID != "" {
		todoQuery = todoQuery.Where(ins.BotOpenID.Eq(q.identity.BotOpenID))
	}
	return todoQuery
}

// CreateTodoItem 创建待办
func (q *Querier) CreateTodoItem(item *model.TodoItem) error {
	return q.q.TodoItem.WithContext(q.queryContext()).Create(item)
}

// UpdateTodoItem 更新待办
func (q *Querier) UpdateTodoItem(id string, item *model.TodoItem) (int64, error) {
	ins := q.q.TodoItem
	result, err := q.scopedTodoItem().Where(ins.ID.Eq(id)).Updates(item)
	return result.RowsAffected, err
}

// GetTodoItemByID 根据ID获取待办
func (q *Querier) GetTodoItemByID(id string) (*model.TodoItem, error) {
	ins := q.q.TodoItem
	return q.scopedTodoItem().Where(ins.ID.Eq(id)).Take()
}

// ListTodoItemsByChatID 获取群待办列表
func (q *Querier) ListTodoItemsByChatID(chatID string, status *string, limit, offset int) ([]*model.TodoItem, error) {
	ins := q.q.TodoItem
	todoQuery := q.scopedTodoItem().Where(ins.ChatID.Eq(chatID))
	if status != nil {
		todoQuery = todoQuery.Where(ins.Status.Eq(*status))
	}
	return todoQuery.Order(ins.CreatedAt.Desc()).Limit(limit).Offset(offset).Find()
}

// DeleteTodoItem 删除待办
func (q *Querier) DeleteTodoItem(id string) (int64, error) {
	ins := q.q.TodoItem
	result, err := q.scopedTodoItem().Where(ins.ID.Eq(id)).Delete()
	return result.RowsAffected, err
}
