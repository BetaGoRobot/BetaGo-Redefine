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
	filterControls := buildTaskFilterControls(tasks, view)
	if len(filterControls) > 0 {
		elements = append(elements, filterControls...)
	}
	if len(tasks) == 0 {
		elements = append(elements, larkmsg.Markdown("暂无匹配的 schedule ⏲️"))
	} else {
		elements = append(elements, larkmsg.HintMarkdown(fmt.Sprintf("共 %d 项；可继续按 ID 精确查询后再做暂停、恢复或删除。", len(tasks))))
		taskSections := make([][]any, 0, len(tasks))
		for _, task := range tasks {
			taskSections = append(taskSections, []any{buildTaskSection(ctx, task, view)})
		}
		elements = larkmsg.AppendSectionsWithDividers(elements, taskSections...)
	}
	return larkmsg.NewStandardPanelCard(ctx, title, elements, larkmsg.StandardCardFooterOptions{
		RefreshPayload:     larkmsg.StringMapToAnyMap(BuildTaskViewValue(view)),
		LastModifierOpenID: view.LastModifierOpenID,
		ActionHistory: larkmsg.CardActionHistoryOptions{
			Enabled:        true,
			OpenMessageID:  view.MessageID,
			PendingRecords: view.PendingHistory,
		},
	})
}

func buildTaskSection(ctx context.Context, task *model.ScheduledTask, view TaskCardViewState) map[string]any {
	if task == nil {
		return larkmsg.Markdown("空 schedule")
	}

	loc, err := resolveLocation(task.Timezone)
	if err != nil {
		loc = utils.UTC8Loc()
	}

	leftLines := []string{
		fmt.Sprintf("**%s**", task.Name),
		fmt.Sprintf("ID: `%s`", task.ID),
		fmt.Sprintf("创建者: `%s`", shortScheduleID(task.CreatorID)),
		fmt.Sprintf("工具: `%s`  时区: `%s`", task.ToolName, task.Timezone),
	}
	if task.IsCron() {
		leftLines = append(leftLines, fmt.Sprintf("Cron: `%s`", task.CronExpr))
	}
	if task.SourceMessageID != "" {
		leftLines = append(leftLines, fmt.Sprintf("来源: `%s`", previewTaskResult(task.SourceMessageID, 24)))
	}

	rightLines := []string{
		fmt.Sprintf("状态: `%s`  模式: `%s`", task.Status, task.Type),
	}
	if task.IsOnce() && task.RunAt != nil {
		rightLines = append(rightLines, fmt.Sprintf("执行: `%s`", task.RunAt.In(loc).Format(timeLayout)))
	} else if task.IsCron() {
		rightLines = append(rightLines, fmt.Sprintf("下次: `%s`", task.NextRunAt.In(loc).Format(timeLayout)))
	}
	if task.LastRunAt != nil {
		rightLines = append(rightLines, fmt.Sprintf("上次: `%s`", task.LastRunAt.In(loc).Format(timeLayout)))
	}
	if task.LastError != "" {
		rightLines = append(rightLines, fmt.Sprintf("最近错误: `%s`", previewTaskResult(task.LastError, 72)))
	}
	if task.LastResult != "" {
		if resultLine := buildTaskResultLine(ctx, task.LastResult); resultLine != "" {
			rightLines = append(rightLines, resultLine)
		}
	}

	rightElements := []any{larkmsg.Markdown(strings.Join(rightLines, "\n"))}
	if buttons := buildTaskActionButtons(task, view); len(buttons) > 0 {
		rightElements = append(rightElements, larkmsg.ButtonRow("flow", buttons...))
	}

	return larkmsg.SplitColumns(
		[]any{larkmsg.Markdown(strings.Join(leftLines, "\n"))},
		rightElements,
		larkmsg.SplitColumnsOptions{
			Left: larkmsg.ColumnOptions{
				Weight:          3,
				VerticalAlign:   "top",
				VerticalSpacing: "4px",
			},
			Right: larkmsg.ColumnOptions{
				Weight:          2,
				VerticalAlign:   "top",
				VerticalSpacing: "6px",
			},
			Row: larkmsg.ColumnSetOptions{
				HorizontalSpacing: "12px",
				FlexMode:          "stretch",
			},
		},
	)
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

func buildTaskFilterControls(tasks []*model.ScheduledTask, view TaskCardViewState) []any {
	view = normalizeTaskCardView(view)

	elements := make([]any, 0, 2)
	if statusRow := buildTaskStatusFilterRow(view); statusRow != nil {
		elements = append(elements, statusRow)
	}
	if creatorRow := buildTaskCreatorFilterRow(tasks, view); creatorRow != nil {
		elements = append(elements, creatorRow)
	}
	return elements
}

func buildTaskStatusFilterRow(view TaskCardViewState) map[string]any {
	options := []struct {
		Label  string
		Status string
	}{
		{Label: "全部", Status: ""},
		{Label: "启用", Status: model.ScheduleTaskStatusEnabled},
		{Label: "暂停", Status: model.ScheduleTaskStatusPaused},
		{Label: "完成", Status: model.ScheduleTaskStatusCompleted},
	}
	buttons := make([]map[string]any, 0, len(options))
	for _, option := range options {
		nextView := withTaskFilterSelection(view, option.Status, view.CreatorOpenID)
		buttonType := "default"
		if strings.TrimSpace(view.Status) == option.Status || (option.Status == "" && strings.TrimSpace(view.Status) == "") {
			buttonType = "primary_filled"
		}
		buttons = append(buttons, buildTaskFilterButton(option.Label, buttonType, nextView))
	}
	return buildTaskFilterRow("状态", buttons)
}

func buildTaskCreatorFilterRow(tasks []*model.ScheduledTask, view TaskCardViewState) map[string]any {
	_ = tasks
	allButtonType := "default"
	if strings.TrimSpace(view.CreatorOpenID) == "" {
		allButtonType = "primary_filled"
	}
	clearButton := buildTaskFilterButton("全部", allButtonType, withTaskFilterSelection(view, view.Status, ""))
	controls := []any{
		buildTaskCreatorSelection(view),
		buildTaskCreatorPicker(view),
		clearButton,
	}
	return larkmsg.SplitColumns(
		[]any{larkmsg.Markdown("**创建者**")},
		[]any{
			larkmsg.ColumnSet([]any{
				larkmsg.Column([]any{controls[0]}, larkmsg.ColumnOptions{
					Width:         "auto",
					VerticalAlign: "center",
				}),
				larkmsg.Column([]any{controls[1]}, larkmsg.ColumnOptions{
					Width:         "auto",
					VerticalAlign: "top",
				}),
				larkmsg.Column([]any{controls[2]}, larkmsg.ColumnOptions{
					Width:         "auto",
					VerticalAlign: "center",
				}),
			}, larkmsg.ColumnSetOptions{
				HorizontalSpacing: "8px",
				FlexMode:          "none",
			}),
		},
		larkmsg.SplitColumnsOptions{
			Left: larkmsg.ColumnOptions{
				Weight:        1,
				VerticalAlign: "top",
			},
			Right: larkmsg.ColumnOptions{
				Weight:        5,
				VerticalAlign: "top",
			},
			Row: larkmsg.ColumnSetOptions{
				HorizontalSpacing: "12px",
				FlexMode:          "stretch",
			},
		},
	)
}

func buildTaskCreatorSelection(view TaskCardViewState) any {
	view = normalizeTaskCardView(view)
	current := strings.TrimSpace(view.CreatorOpenID)
	if current == "" {
		return larkmsg.TextDiv("当前筛选：全部", larkmsg.CardTextOptions{
			Size:  "notation",
			Color: "grey",
			Align: "left",
		})
	}

	showAvatar := true
	showName := false
	return larkmsg.ColumnSet([]any{
		larkmsg.Column([]any{
			larkmsg.TextDiv("当前筛选", larkmsg.CardTextOptions{
				Size:  "notation",
				Color: "grey",
				Align: "left",
			}),
		}, larkmsg.ColumnOptions{
			Width:         "auto",
			VerticalAlign: "center",
		}),
		larkmsg.Column([]any{
			larkmsg.Person(current, larkmsg.PersonOptions{
				Size:       "small",
				ShowAvatar: &showAvatar,
				ShowName:   &showName,
				Margin:     "0",
			}),
		}, larkmsg.ColumnOptions{
			Width:         "auto",
			VerticalAlign: "center",
		}),
	}, larkmsg.ColumnSetOptions{
		HorizontalSpacing: "6px",
		FlexMode:          "none",
	})
}

func buildTaskFilterRow(label string, buttons []map[string]any) map[string]any {
	if len(buttons) == 0 {
		return nil
	}
	return larkmsg.SplitColumns(
		[]any{larkmsg.Markdown("**" + label + "**")},
		[]any{larkmsg.ButtonRow("flow", buttons...)},
		larkmsg.SplitColumnsOptions{
			Left: larkmsg.ColumnOptions{
				Weight:        1,
				VerticalAlign: "top",
			},
			Right: larkmsg.ColumnOptions{
				Weight:        5,
				VerticalAlign: "top",
			},
			Row: larkmsg.ColumnSetOptions{
				HorizontalSpacing: "12px",
				FlexMode:          "stretch",
			},
		},
	)
}

func buildTaskFilterButton(label, buttonType string, view TaskCardViewState) map[string]any {
	return larkmsg.Button(label, larkmsg.ButtonOptions{
		Type:    buttonType,
		Payload: larkmsg.StringMapToAnyMap(BuildTaskViewValue(view)),
	})
}

func buildTaskCreatorPicker(view TaskCardViewState) map[string]any {
	view = normalizeTaskCardView(view)
	return larkmsg.SelectPerson(larkmsg.SelectPersonOptions{
		Placeholder:   "选择创建者筛选",
		Width:         "default",
		Type:          "default",
		InitialOption: view.CreatorOpenID,
		Payload:       larkmsg.StringMapToAnyMap(BuildTaskCreatorPickerValue(view)),
		ElementID:     "sched_creator_pick",
	})
}

func withTaskFilterSelection(view TaskCardViewState, status, creatorOpenID string) TaskCardViewState {
	view = normalizeTaskCardView(view)
	view.ID = ""
	view.Status = strings.TrimSpace(status)
	view.CreatorOpenID = strings.TrimSpace(creatorOpenID)
	if view.Name != "" || view.Status != "" || view.TaskType != "" || view.ToolName != "" || view.CreatorOpenID != "" {
		view.Mode = TaskCardViewModeQuery
	} else {
		view.Mode = TaskCardViewModeList
	}
	return normalizeTaskCardView(view)
}

func shortScheduleID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "-"
	}
	if len(id) <= 10 {
		return id
	}
	return id[:10]
}
