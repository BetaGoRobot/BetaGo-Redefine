package schedule

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
)

type TaskType string

const (
	TaskTypeOnce TaskType = model.ScheduleTaskTypeOnce
	TaskTypeCron TaskType = model.ScheduleTaskTypeCron
)

func (TaskType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(TaskTypeOnce), Label: "单次"},
			{Value: string(TaskTypeCron), Label: "周期"},
		},
	}
}

type TaskStatus string

const (
	TaskStatusEnabled   TaskStatus = model.ScheduleTaskStatusEnabled
	TaskStatusPaused    TaskStatus = model.ScheduleTaskStatusPaused
	TaskStatusCompleted TaskStatus = model.ScheduleTaskStatusCompleted
	TaskStatusDisabled  TaskStatus = model.ScheduleTaskStatusDisabled
)

func (TaskStatus) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(TaskStatusEnabled), Label: "启用"},
			{Value: string(TaskStatusPaused), Label: "暂停"},
			{Value: string(TaskStatusCompleted), Label: "完成"},
			{Value: string(TaskStatusDisabled), Label: "禁用"},
		},
	}
}
