package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

const (
	editConfirmAction = "schedule.edit_confirm"
	editCancelAction  = "schedule.edit_cancel"
	editTokenField    = "edit_token"
)

// Field keys for edit NewValues map
const (
	editFieldName          = "name"
	editFieldCronExpr     = "cron_expr"
	editFieldTimezone     = "timezone"
	editFieldRunAt        = "run_at"
	editFieldMessage      = "message"
	editFieldNotifyOnError = "notify_on_error"
	editFieldNotifyResult = "notify_result"
)

// buildEditChangeLines builds the per-field change description lines from newValues vs task.
// Exported for reuse by describeEditChanges.
func buildEditChangeLines(newValues map[string]any, task *model.ScheduledTask, loc *time.Location) []string {
	var lines []string
	if name, ok := newValues[editFieldName].(string); ok && name != task.Name {
		lines = append(lines, fmt.Sprintf("- 名称: `%s` → `%s`", task.Name, name))
	}
	if cronExpr, ok := newValues[editFieldCronExpr].(string); ok && cronExpr != task.CronExpr {
		lines = append(lines, fmt.Sprintf("- Cron: `%s` → `%s`", task.CronExpr, cronExpr))
	}
	if timezone, ok := newValues[editFieldTimezone].(string); ok && timezone != task.Timezone {
		lines = append(lines, fmt.Sprintf("- 时区: `%s` → `%s`", task.Timezone, timezone))
	}
	if runAt, ok := newValues[editFieldRunAt].(time.Time); ok {
		if task.RunAt != nil {
			lines = append(lines, fmt.Sprintf("- 执行时间: `%s` → `%s`",
				task.RunAt.In(loc).Format("2006-01-02 15:04:05"),
				runAt.In(loc).Format("2006-01-02 15:04:05")))
		} else {
			lines = append(lines, fmt.Sprintf("- 执行时间: → `%s`",
				runAt.In(loc).Format("2006-01-02 15:04:05")))
		}
	}
	if message, ok := newValues[editFieldMessage].(string); ok && message != "" {
		preview := message
		if len(preview) > 50 {
			preview = preview[:50] + "..."
		}
		lines = append(lines, fmt.Sprintf("- 消息: → `%s`", preview))
	}
	if notifyOnError, ok := newValues[editFieldNotifyOnError].(bool); ok {
		lines = append(lines, fmt.Sprintf("- 失败通知: `%v` → `%v`", task.NotifyOnError, notifyOnError))
	}
	if notifyResult, ok := newValues[editFieldNotifyResult].(bool); ok {
		lines = append(lines, fmt.Sprintf("- 结果通知: `%v` → `%v`", task.NotifyResult, notifyResult))
	}
	return lines
}

func buildEditConfirmCard(ctx context.Context, task *model.ScheduledTask, newValues map[string]any, editToken string) (map[string]any, error) {
	loc, err := resolveLocation(task.Timezone)
	if err != nil {
		loc = time.UTC
	}

	changeLines := buildEditChangeLines(newValues, task, loc)
	if len(changeLines) == 0 {
		changeLines = append(changeLines, "(无实际变更)")
	}

	header := fmt.Sprintf("**待修改 Schedule**\n名称: %s\nID: `%s`\n\n**修改内容**\n", task.Name, task.ID)

	elements := []any{
		larkmsg.Markdown(header + strings.Join(changeLines, "\n")),
		larkmsg.HintMarkdown("⚠️ 此操作需要确认，请点击下方按钮。确认后立即执行修改。"),
	}

	confirmPayload := cardactionproto.New(editConfirmAction).
		WithValue(editTokenField, editToken).
		WithValue(taskCardViewIDField, task.ID).
		Payload()

	cancelPayload := cardactionproto.New(editCancelAction).
		WithValue(editTokenField, editToken).
		Payload()

	elements = append(elements, larkmsg.ButtonRow("action",
		larkmsg.Button("✅ 确认修改", larkmsg.ButtonOptions{
			Type:    "primary_filled",
			Payload: larkmsg.StringMapToAnyMap(confirmPayload),
		}),
		larkmsg.Button("❌ 取消", larkmsg.ButtonOptions{
			Type:    "default",
			Payload: larkmsg.StringMapToAnyMap(cancelPayload),
		}),
	))

	card := larkmsg.NewStandardPanelCard(ctx, "📝 Schedule 修改确认", elements, larkmsg.StandardCardFooterOptions{
		RefreshPayload: larkmsg.StringMapToAnyMap(BuildTaskViewValue(TaskCardViewState{
			Mode: TaskCardViewModeQuery,
			ID:   task.ID,
		})),
	})
	return map[string]any(card), nil
}
