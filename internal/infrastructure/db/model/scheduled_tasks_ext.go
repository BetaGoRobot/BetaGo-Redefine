package model

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

const (
	ScheduleTaskTypeOnce = "once"
	ScheduleTaskTypeCron = "cron"

	ScheduleTaskStatusEnabled   = "enabled"
	ScheduleTaskStatusPaused    = "paused"
	ScheduleTaskStatusCompleted = "completed"
	ScheduleTaskStatusDisabled  = "disabled"

	ScheduleTaskDefaultTimezone = "Asia/Shanghai"
)

func NewScheduledTask(name, taskType, chatID, creatorID, toolName, toolArgs, timezone, appID, botOpenID string) *ScheduledTask {
	now := time.Now()
	if timezone == "" {
		timezone = ScheduleTaskDefaultTimezone
	}
	return &ScheduledTask{
		ID:        generateScheduleTaskID(),
		Name:      name,
		Type:      taskType,
		ChatID:    chatID,
		AppID:     appID,
		BotOpenID: botOpenID,
		CreatorID: creatorID,
		ToolName:  toolName,
		ToolArgs:  toolArgs,
		Timezone:  timezone,
		Status:    ScheduleTaskStatusEnabled,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (t *ScheduledTask) Enabled() bool {
	return t != nil && t.Status == ScheduleTaskStatusEnabled
}

func (t *ScheduledTask) IsOnce() bool {
	return t != nil && t.Type == ScheduleTaskTypeOnce
}

func (t *ScheduledTask) IsCron() bool {
	return t != nil && t.Type == ScheduleTaskTypeCron
}

func (t *ScheduledTask) Pause() {
	if t == nil {
		return
	}
	t.Status = ScheduleTaskStatusPaused
	t.UpdatedAt = time.Now()
}

func (t *ScheduledTask) Resume() {
	if t == nil {
		return
	}
	t.Status = ScheduleTaskStatusEnabled
	t.UpdatedAt = time.Now()
}

func (t *ScheduledTask) Complete() {
	if t == nil {
		return
	}
	t.Status = ScheduleTaskStatusCompleted
	t.UpdatedAt = time.Now()
}

func (t *ScheduledTask) ValidateBasic() error {
	switch {
	case t == nil:
		return fmt.Errorf("task is nil")
	case t.Name == "":
		return fmt.Errorf("task name is required")
	case t.ChatID == "":
		return fmt.Errorf("chat_id is required")
	case t.AppID == "" && t.BotOpenID == "":
		return fmt.Errorf("at least one of app_id or bot_open_id is required")
	case t.CreatorID == "":
		return fmt.Errorf("creator_id is required")
	case t.ToolName == "":
		return fmt.Errorf("tool_name is required")
	case t.Type != ScheduleTaskTypeOnce && t.Type != ScheduleTaskTypeCron:
		return fmt.Errorf("task type must be once or cron")
	case t.IsOnce() && t.RunAt == nil:
		return fmt.Errorf("run_at is required for once task")
	case t.IsCron() && t.CronExpr == "":
		return fmt.Errorf("cron_expr is required for cron task")
	}
	return nil
}

func generateScheduleTaskID() string {
	return time.Now().Format("20060102150405") + "-" + randomScheduleTaskString(6)
}

func randomScheduleTaskString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			b[i] = letters[i%len(letters)]
			continue
		}
		b[i] = letters[idx.Int64()]
	}
	return string(b)
}
