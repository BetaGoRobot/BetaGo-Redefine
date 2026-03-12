package todo

import (
	domaintodo "github.com/BetaGoRobot/BetaGo-Redefine/internal/domain/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
)

type TodoPriority string

const (
	TodoPriorityLow    TodoPriority = TodoPriority(domaintodo.TodoPriorityLow)
	TodoPriorityMedium TodoPriority = TodoPriority(domaintodo.TodoPriorityMedium)
	TodoPriorityHigh   TodoPriority = TodoPriority(domaintodo.TodoPriorityHigh)
	TodoPriorityUrgent TodoPriority = TodoPriority(domaintodo.TodoPriorityUrgent)
)

func (TodoPriority) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(TodoPriorityLow), Label: "低"},
			{Value: string(TodoPriorityMedium), Label: "中"},
			{Value: string(TodoPriorityHigh), Label: "高"},
			{Value: string(TodoPriorityUrgent), Label: "紧急"},
		},
		DefaultValue: string(TodoPriorityMedium),
	}
}

type TodoStatus string

const (
	TodoStatusPending   TodoStatus = TodoStatus(domaintodo.TodoStatusPending)
	TodoStatusDoing     TodoStatus = TodoStatus(domaintodo.TodoStatusDoing)
	TodoStatusDone      TodoStatus = TodoStatus(domaintodo.TodoStatusDone)
	TodoStatusCancelled TodoStatus = TodoStatus(domaintodo.TodoStatusCancelled)
)

func (TodoStatus) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(TodoStatusPending), Label: "待处理"},
			{Value: string(TodoStatusDoing), Label: "进行中"},
			{Value: string(TodoStatusDone), Label: "已完成"},
			{Value: string(TodoStatusCancelled), Label: "已取消"},
		},
	}
}
