package todo

import (
	"slices"
	"time"
)

// TodoStatus 待办事项状态
type TodoStatus string

const (
	TodoStatusPending   TodoStatus = "pending"   // 待处理
	TodoStatusDoing     TodoStatus = "doing"     // 进行中
	TodoStatusDone      TodoStatus = "done"      // 已完成
	TodoStatusCancelled TodoStatus = "cancelled" // 已取消
)

// TodoPriority 待办事项优先级
type TodoPriority string

const (
	TodoPriorityLow    TodoPriority = "low"    // 低优先级
	TodoPriorityMedium TodoPriority = "medium" // 中优先级
	TodoPriorityHigh   TodoPriority = "high"   // 高优先级
	TodoPriorityUrgent TodoPriority = "urgent" // 紧急
)

// Todo 待办事项领域模型
type Todo struct {
	ID          string       `json:"id"`
	ChatID      string       `json:"chat_id"`      // 所在群组/单聊ID
	CreatorID   string       `json:"creator_id"`   // 创建者ID
	CreatorName string       `json:"creator_name"` // 创建者名称
	AssigneeID  string       `json:"assignee_id"`  // 负责人ID（可选）
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Status      TodoStatus   `json:"status"`
	Priority    TodoPriority `json:"priority"`
	DueAt       *time.Time   `json:"due_at"`       // 截止时间
	CompletedAt *time.Time   `json:"completed_at"` // 完成时间
	Tags        []string     `json:"tags"`         // 标签
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// NewTodo 创建新的待办事项
func NewTodo(chatID, creatorID, creatorName, title, description string, priority TodoPriority) *Todo {
	now := time.Now()
	return &Todo{
		ID:          generateID(),
		ChatID:      chatID,
		CreatorID:   creatorID,
		CreatorName: creatorName,
		Title:       title,
		Description: description,
		Status:      TodoStatusPending,
		Priority:    priority,
		Tags:        make([]string, 0),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// UpdateStatus 更新待办状态
func (t *Todo) UpdateStatus(status TodoStatus) {
	t.Status = status
	if status == TodoStatusDone {
		now := time.Now()
		t.CompletedAt = &now
	}
	t.UpdatedAt = time.Now()
}

// SetDueDate 设置截止日期
func (t *Todo) SetDueDate(dueAt time.Time) {
	t.DueAt = &dueAt
	t.UpdatedAt = time.Now()
}

// AssignTo 分配给某人
func (t *Todo) AssignTo(assigneeID string) {
	t.AssigneeID = assigneeID
	t.UpdatedAt = time.Now()
}

// AddTag 添加标签
func (t *Todo) AddTag(tag string) {
	if slices.Contains(t.Tags, tag) {
		return
	}
	t.Tags = append(t.Tags, tag)
	t.UpdatedAt = time.Now()
}

// IsOverdue 检查是否已过期
func (t *Todo) IsOverdue() bool {
	if t.DueAt == nil || t.Status == TodoStatusDone {
		return false
	}
	return time.Now().After(*t.DueAt)
}

// generateID 生成简单ID
func generateID() string {
	return time.Now().Format("20060102150405") + "-" + randomString(6)
}

// randomString 生成随机字符串
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}
