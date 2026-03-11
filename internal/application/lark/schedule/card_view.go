package schedule

import (
	"context"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
)

func BuildTaskListCard(ctx context.Context, title string, tasks []*model.ScheduledTask, view TaskCardViewState) larkmsg.RawCard {
	view = normalizeTaskCardView(view)
	if strings.TrimSpace(title) == "" {
		title = view.Title()
	}

	elements := []any{}
	if len(tasks) == 0 {
		elements = append(elements, larkmsg.Markdown("暂无匹配的 schedule ⏲️"))
	} else {
		elements = append(elements, larkmsg.HintMarkdown(fmt.Sprintf("共 %d 项；可继续按 ID 精确查询后再做暂停、恢复或删除。", len(tasks))))
		taskSections := make([][]any, 0, len(tasks))
		for _, task := range tasks {
			section := []any{buildTaskSection(ctx, task)}
			if actionRow := buildTaskActionRow(task, view); actionRow != nil {
				section = append(section, actionRow)
			}
			taskSections = append(taskSections, section)
		}
		elements = larkmsg.AppendSectionsWithDividers(elements, taskSections...)
	}

	return larkmsg.NewStandardPanelCard(ctx, title, elements, larkmsg.StandardCardFooterOptions{
		RefreshPayload: larkmsg.StringMapToAnyMap(BuildTaskViewValue(view)),
	})
}

func buildTaskSection(ctx context.Context, task *model.ScheduledTask) map[string]any {
	if task == nil {
		return larkmsg.Markdown("空 schedule")
	}

	loc, err := resolveLocation(task.Timezone)
	if err != nil {
		loc = utils.UTC8Loc()
	}

	lines := []string{
		fmt.Sprintf("**%s**", task.Name),
		fmt.Sprintf("ID: `%s`", task.ID),
		fmt.Sprintf("状态: `%s`  模式: `%s`", task.Status, task.Type),
		fmt.Sprintf("动作: `%s`", task.ToolName),
		fmt.Sprintf("时区: `%s`", task.Timezone),
	}
	if task.SourceMessageID != "" {
		lines = append(lines, fmt.Sprintf("来源消息: `%s`", task.SourceMessageID))
	}
	if task.IsOnce() && task.RunAt != nil {
		lines = append(lines, fmt.Sprintf("执行时间: `%s`", task.RunAt.In(loc).Format(timeLayout)))
	}
	if task.IsCron() {
		lines = append(lines,
			fmt.Sprintf("Cron: `%s`", task.CronExpr),
			fmt.Sprintf("下次执行: `%s`", task.NextRunAt.In(loc).Format(timeLayout)),
		)
	}
	if task.LastRunAt != nil {
		lines = append(lines, fmt.Sprintf("上次执行: `%s`", task.LastRunAt.In(loc).Format(timeLayout)))
	}
	if task.LastError != "" {
		lines = append(lines, fmt.Sprintf("最近错误: `%s`", task.LastError))
	}
	if task.LastResult != "" {
		if resultLine := buildTaskResultLine(ctx, task.LastResult); resultLine != "" {
			lines = append(lines, resultLine)
		}
	}

	return larkmsg.Markdown(strings.Join(lines, "\n"))
}

func buildTaskActionRow(task *model.ScheduledTask, view TaskCardViewState) map[string]any {
	buttons := buildTaskActionButtons(task, view)
	if len(buttons) == 0 {
		return nil
	}
	return larkmsg.ButtonRow("flow", buttons...)
}

func buildTaskActionButtons(task *model.ScheduledTask, view TaskCardViewState) []map[string]any {
	if task == nil {
		return nil
	}

	buttons := make([]map[string]any, 0, 2)
	switch task.Status {
	case model.ScheduleTaskStatusEnabled:
		buttons = append(buttons,
			buildTaskActionButton("暂停", "default", TaskActionPause, task.ID, view),
			buildTaskActionButton("删除", "danger", TaskActionDelete, task.ID, view),
		)
	case model.ScheduleTaskStatusPaused:
		buttons = append(buttons,
			buildTaskActionButton("恢复", "primary_filled", TaskActionResume, task.ID, view),
			buildTaskActionButton("删除", "danger", TaskActionDelete, task.ID, view),
		)
	case model.ScheduleTaskStatusCompleted, model.ScheduleTaskStatusDisabled:
		buttons = append(buttons,
			buildTaskActionButton("删除", "danger", TaskActionDelete, task.ID, view),
		)
	}
	return buttons
}

func buildTaskActionButton(label, buttonType string, action TaskAction, taskID string, view TaskCardViewState) map[string]any {
	return larkmsg.Button(label, larkmsg.ButtonOptions{
		Type:    buttonType,
		Payload: larkmsg.StringMapToAnyMap(BuildTaskActionValue(action, taskID, view)),
	})
}

const timeLayout = "2006-01-02 15:04:05"
