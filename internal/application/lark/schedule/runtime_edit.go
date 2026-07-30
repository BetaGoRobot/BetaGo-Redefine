package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

type RuntimeScheduleEditCapability struct {
	service TaskService
}

func NewRuntimeScheduleEditCapability(service TaskService) *RuntimeScheduleEditCapability {
	return &RuntimeScheduleEditCapability{service: service}
}

func (c *RuntimeScheduleEditCapability) ValidateScheduleEdit(
	ctx context.Context,
	actorOpenID string,
	input agentruntime.ScheduleEditTrustedInput,
) error {
	if c == nil || c.service == nil {
		return errors.New("runtime schedule edit capability is not configured")
	}
	task, err := c.service.GetTask(ctx, input.TaskID)
	if err != nil {
		return err
	}
	if task == nil {
		return errors.New("schedule task lookup returned nil")
	}
	if strings.TrimSpace(task.ChatID) != strings.TrimSpace(input.ChatID) {
		return errors.New("schedule task does not belong to the trusted chat")
	}
	return EnsureTaskMutationAllowed(ctx, actorOpenID, task)
}

func (c *RuntimeScheduleEditCapability) ExecuteScheduleEdit(
	ctx context.Context,
	actorOpenID string,
	input agentruntime.ScheduleEditTrustedInput,
) (json.RawMessage, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("runtime schedule edit capability is not configured")
	}
	req := &UpdateTaskRequest{
		ID: input.TaskID, ActorOpenID: actorOpenID,
		Name: input.NewValues.Name, CronExpr: input.NewValues.CronExpr,
		RunAt: input.NewValues.RunAt, Timezone: input.NewValues.Timezone,
		Message: input.NewValues.Message, NotifyOnError: input.NewValues.NotifyOnError,
		NotifyResult: input.NewValues.NotifyResult, SkipHolidays: input.NewValues.SkipHolidays,
	}
	task, err := c.service.UpdateTask(ctx, req)
	if errors.Is(err, ErrNoScheduleFieldsToUpdate) {
		task, err = c.service.GetTask(ctx, input.TaskID)
	}
	if err != nil {
		return nil, err
	}
	return encodeRuntimeScheduleEditResult(task)
}

func encodeRuntimeScheduleEditResult(task *model.ScheduledTask) (json.RawMessage, error) {
	if task == nil {
		return nil, fmt.Errorf("updated schedule task is nil")
	}
	return json.Marshal(struct {
		Status string `json:"status"`
		TaskID string `json:"task_id"`
		Name   string `json:"name"`
	}{
		Status: "updated",
		TaskID: task.ID,
		Name:   task.Name,
	})
}
