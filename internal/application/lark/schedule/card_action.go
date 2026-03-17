package schedule

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

const (
	taskCardViewModeField      = "schedule_view_mode"
	taskCardViewIDField        = "schedule_view_id"
	taskCardViewNameField      = "schedule_view_name"
	taskCardViewStatusField    = "schedule_view_status"
	taskCardViewTaskTypeField  = "schedule_view_task_type"
	taskCardViewToolNameField  = "schedule_view_tool_name"
	taskCardViewCreatorField   = "schedule_view_creator_open_id"
	taskCardViewChatScopeField = "schedule_view_chat_scope"
	taskCardViewChatIDField    = "schedule_view_chat_id"
	taskCardViewLimitField     = "schedule_view_limit"
	taskCardLastModifierField  = "schedule_last_modifier_open_id"
	taskCardViewSelectField    = "schedule_view_select_field"
)

const taskCardViewSelectCreator = "creator_open_id"

type TaskAction string

const (
	TaskActionPause  TaskAction = "pause"
	TaskActionResume TaskAction = "resume"
	TaskActionDelete TaskAction = "delete"
)

type TaskCardViewMode string

const (
	TaskCardViewModeList  TaskCardViewMode = "list"
	TaskCardViewModeQuery TaskCardViewMode = "query"
)

type TaskChatScope string

const (
	TaskChatScopeCurrent TaskChatScope = "current"
	TaskChatScopeAll     TaskChatScope = "all"
)

type TaskCardViewState struct {
	Mode               TaskCardViewMode
	ID                 string
	Name               string
	Status             string
	TaskType           string
	ToolName           string
	CreatorOpenID      string
	ChatScope          TaskChatScope
	ChatID             string
	LastModifierOpenID string
	MessageID          string
	PendingHistory     []larkmsg.CardActionHistoryRecord
	Limit              int
}

type TaskActionRequest struct {
	Action TaskAction
	ID     string
	View   TaskCardViewState
}

type TaskViewRequest struct {
	View TaskCardViewState
}

func NewTaskListCardView(limit int) TaskCardViewState {
	return normalizeTaskCardView(TaskCardViewState{
		Mode:  TaskCardViewModeList,
		Limit: limit,
	})
}

func NewTaskQueryCardView(id string, query TaskQuery, limit int) TaskCardViewState {
	return normalizeTaskCardView(TaskCardViewState{
		Mode:          TaskCardViewModeQuery,
		ID:            id,
		Name:          query.Name,
		Status:        query.Status,
		TaskType:      query.Type,
		ToolName:      query.ToolName,
		CreatorOpenID: query.CreatorOpenID,
		Limit:         limit,
	})
}

func BuildTaskViewValue(view TaskCardViewState) map[string]string {
	view = normalizeTaskCardView(view)
	return cardactionproto.New(cardactionproto.ActionScheduleView).
		WithValue(taskCardViewModeField, string(view.Mode)).
		WithValue(taskCardViewIDField, view.ID).
		WithValue(taskCardViewNameField, view.Name).
		WithValue(taskCardViewStatusField, view.Status).
		WithValue(taskCardViewTaskTypeField, view.TaskType).
		WithValue(taskCardViewToolNameField, view.ToolName).
		WithValue(taskCardViewCreatorField, view.CreatorOpenID).
		WithValue(taskCardLastModifierField, view.LastModifierOpenID).
		WithValue(taskCardViewLimitField, strconv.Itoa(view.Limit)).
		Payload()
}

func BuildTaskCreatorPickerValue(view TaskCardViewState) map[string]string {
	payload := BuildTaskViewValue(withTaskFilterSelection(view, view.Status, view.CreatorOpenID))
	payload[taskCardViewSelectField] = taskCardViewSelectCreator
	return payload
}

func BuildTaskActionValue(action TaskAction, taskID string, view TaskCardViewState) map[string]string {
	actionName, ok := taskActionName(action)
	if !ok {
		return nil
	}

	view = normalizeTaskCardView(view)
	return cardactionproto.New(actionName).
		WithID(strings.TrimSpace(taskID)).
		WithValue(taskCardViewModeField, string(view.Mode)).
		WithValue(taskCardViewIDField, view.ID).
		WithValue(taskCardViewNameField, view.Name).
		WithValue(taskCardViewStatusField, view.Status).
		WithValue(taskCardViewTaskTypeField, view.TaskType).
		WithValue(taskCardViewToolNameField, view.ToolName).
		WithValue(taskCardViewCreatorField, view.CreatorOpenID).
		WithValue(taskCardLastModifierField, view.LastModifierOpenID).
		WithValue(taskCardViewLimitField, strconv.Itoa(view.Limit)).
		Payload()
}

func ParseTaskViewRequest(parsed *cardactionproto.Parsed) (*TaskViewRequest, error) {
	if parsed == nil {
		return nil, fmt.Errorf("schedule view action is nil")
	}
	if parsed.Name != cardactionproto.ActionScheduleView {
		return nil, fmt.Errorf("unsupported schedule view action: %s", parsed.Name)
	}
	return &TaskViewRequest{
		View: parseTaskCardViewState(parsed),
	}, nil
}

func ParseTaskActionRequest(parsed *cardactionproto.Parsed) (*TaskActionRequest, error) {
	if parsed == nil {
		return nil, fmt.Errorf("schedule action is nil")
	}

	action, ok := taskActionFromName(parsed.Name)
	if !ok {
		return nil, fmt.Errorf("unsupported schedule action: %s", parsed.Name)
	}

	id, err := parsed.RequiredString(cardactionproto.IDField)
	if err != nil {
		return nil, err
	}

	return &TaskActionRequest{
		Action: action,
		ID:     strings.TrimSpace(id),
		View:   parseTaskCardViewState(parsed),
	}, nil
}

func BuildTaskCardPayloadForView(ctx context.Context, chatID string, view TaskCardViewState, allowMissingID bool) (map[string]any, error) {
	tasks, err := loadTasksForView(ctx, chatID, view, allowMissingID)
	if err != nil {
		return nil, err
	}
	card := BuildTaskListCard(ctx, view.Title(), tasks, view)
	return map[string]any(card), nil
}

func (view TaskCardViewState) Title() string {
	if normalizeTaskCardView(view).Mode == TaskCardViewModeList {
		return "Schedule 列表"
	}
	return "Schedule 查询"
}

func (view TaskCardViewState) Query() TaskQuery {
	view = normalizeTaskCardView(view)
	return TaskQuery{
		Name:          view.Name,
		Status:        view.Status,
		Type:          view.TaskType,
		ToolName:      view.ToolName,
		CreatorOpenID: view.CreatorOpenID,
	}
}

func normalizeTaskCardView(view TaskCardViewState) TaskCardViewState {
	view.Mode = TaskCardViewMode(strings.TrimSpace(string(view.Mode)))
	if view.Mode != TaskCardViewModeQuery {
		view.Mode = TaskCardViewModeList
	}

	view.ID = strings.TrimSpace(view.ID)
	view.Name = strings.TrimSpace(view.Name)
	view.Status = strings.TrimSpace(view.Status)
	view.TaskType = strings.TrimSpace(view.TaskType)
	view.ToolName = strings.TrimSpace(view.ToolName)
	view.CreatorOpenID = strings.TrimSpace(view.CreatorOpenID)
	view.ChatScope = TaskChatScopeCurrent
	view.ChatID = ""
	view.LastModifierOpenID = strings.TrimSpace(view.LastModifierOpenID)
	view.MessageID = strings.TrimSpace(view.MessageID)

	if view.Limit <= 0 {
		if view.Mode == TaskCardViewModeList {
			view.Limit = 50
		} else {
			view.Limit = 100
		}
	}
	return view
}

func parseTaskCardViewState(parsed *cardactionproto.Parsed) TaskCardViewState {
	limit, _ := strconv.Atoi(readScheduleActionValue(parsed, taskCardViewLimitField))
	creatorOpenID := readScheduleActionValue(parsed, taskCardViewCreatorField)
	if shouldApplyTaskCreatorPicker(parsed) {
		if selected := parsed.SelectedOption(); selected != "" {
			creatorOpenID = selected
		}
	}
	return normalizeTaskCardView(TaskCardViewState{
		Mode:               TaskCardViewMode(readScheduleActionValue(parsed, taskCardViewModeField)),
		ID:                 readScheduleActionValue(parsed, taskCardViewIDField),
		Name:               readScheduleActionValue(parsed, taskCardViewNameField),
		Status:             readScheduleActionValue(parsed, taskCardViewStatusField),
		TaskType:           readScheduleActionValue(parsed, taskCardViewTaskTypeField),
		ToolName:           readScheduleActionValue(parsed, taskCardViewToolNameField),
		CreatorOpenID:      creatorOpenID,
		ChatScope:          TaskChatScope(readScheduleActionValue(parsed, taskCardViewChatScopeField)),
		ChatID:             readScheduleActionValue(parsed, taskCardViewChatIDField),
		LastModifierOpenID: readScheduleActionValue(parsed, taskCardLastModifierField),
		Limit:              limit,
	})
}

func shouldApplyTaskCreatorPicker(parsed *cardactionproto.Parsed) bool {
	if parsed == nil {
		return false
	}
	if strings.TrimSpace(parsed.Tag) != "select_person" {
		return false
	}
	return readScheduleActionValue(parsed, taskCardViewSelectField) == taskCardViewSelectCreator
}

func loadTasksForView(ctx context.Context, chatID string, view TaskCardViewState, allowMissingID bool) ([]*model.ScheduledTask, error) {
	view = normalizeTaskCardView(view)
	targetChatID := resolveViewTargetChatID(chatID, view)

	if view.Mode == TaskCardViewModeQuery && view.ID != "" {
		task, err := getTaskForView(ctx, targetChatID, view.ID)
		if err != nil {
			if allowMissingID && isTaskNotFoundErr(err) {
				return nil, nil
			}
			return nil, err
		}
		return []*model.ScheduledTask{task}, nil
	}

	tasks, err := GetService().ListTasks(ctx, &ListTasksRequest{
		ChatID: targetChatID,
		Limit:  view.Limit,
	})
	if err != nil {
		return nil, err
	}
	if view.Mode == TaskCardViewModeList {
		return tasks, nil
	}
	return FilterTasks(tasks, view.Query()), nil
}

func resolveViewTargetChatID(fallbackChatID string, view TaskCardViewState) string {
	_ = normalizeTaskCardView(view)
	return strings.TrimSpace(fallbackChatID)
}

func getTaskForView(ctx context.Context, targetChatID, id string) (*model.ScheduledTask, error) {
	targetChatID = strings.TrimSpace(targetChatID)
	if targetChatID == "" {
		return GetService().GetTask(ctx, strings.TrimSpace(id))
	}
	return GetTaskForChat(ctx, targetChatID, id)
}

func GetTaskForChat(ctx context.Context, chatID, id string) (*model.ScheduledTask, error) {
	task, err := GetService().GetTask(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	chatID = strings.TrimSpace(chatID)
	if chatID != "" && strings.TrimSpace(task.ChatID) != chatID {
		return nil, fmt.Errorf("task not found")
	}
	return task, nil
}

func readScheduleActionValue(parsed *cardactionproto.Parsed, key string) string {
	if parsed == nil {
		return ""
	}
	if value, ok := parsed.FormString(key); ok {
		return strings.TrimSpace(value)
	}
	if value, ok := parsed.String(key); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func taskActionName(action TaskAction) (string, bool) {
	switch action {
	case TaskActionPause:
		return cardactionproto.ActionSchedulePause, true
	case TaskActionResume:
		return cardactionproto.ActionScheduleResume, true
	case TaskActionDelete:
		return cardactionproto.ActionScheduleDelete, true
	default:
		return "", false
	}
}

func taskActionFromName(name string) (TaskAction, bool) {
	switch name {
	case cardactionproto.ActionSchedulePause:
		return TaskActionPause, true
	case cardactionproto.ActionScheduleResume:
		return TaskActionResume, true
	case cardactionproto.ActionScheduleDelete:
		return TaskActionDelete, true
	default:
		return "", false
	}
}

func isTaskNotFoundErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}
