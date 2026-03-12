package schedule

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestBuildTaskActionValueUsesStandardAction(t *testing.T) {
	payload := BuildTaskActionValue(TaskActionPause, "task-1", NewTaskQueryCardView("task-1", TaskQuery{
		CreatorOpenID: "ou_creator",
	}, 0))
	if payload[cardactionproto.ActionField] != cardactionproto.ActionSchedulePause {
		t.Fatalf("unexpected action field: %q", payload[cardactionproto.ActionField])
	}
	if payload[cardactionproto.IDField] != "task-1" {
		t.Fatalf("unexpected task id: %q", payload[cardactionproto.IDField])
	}
	if payload[taskCardViewModeField] != string(TaskCardViewModeQuery) {
		t.Fatalf("unexpected view mode: %q", payload[taskCardViewModeField])
	}
	if payload[taskCardViewCreatorField] != "ou_creator" {
		t.Fatalf("unexpected creator payload: %q", payload[taskCardViewCreatorField])
	}
}

func TestBuildTaskViewValueUsesStandardAction(t *testing.T) {
	payload := BuildTaskViewValue(NewTaskQueryCardView("task-1", TaskQuery{
		Name:          "提醒",
		CreatorOpenID: "ou_creator",
	}, 20))
	if payload[cardactionproto.ActionField] != cardactionproto.ActionScheduleView {
		t.Fatalf("unexpected action field: %q", payload[cardactionproto.ActionField])
	}
	if payload[taskCardViewIDField] != "task-1" || payload[taskCardViewNameField] != "提醒" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload[taskCardViewCreatorField] != "ou_creator" {
		t.Fatalf("unexpected creator payload: %+v", payload)
	}
}

func TestBuildTaskCreatorPickerValueMarksCreatorSelection(t *testing.T) {
	payload := BuildTaskCreatorPickerValue(NewTaskQueryCardView("task-1", TaskQuery{
		Status:        model.ScheduleTaskStatusPaused,
		CreatorOpenID: "ou_creator",
	}, 20))
	if payload[taskCardViewSelectField] != taskCardViewSelectCreator {
		t.Fatalf("unexpected picker selector field: %+v", payload)
	}
	if payload[taskCardViewIDField] != "" {
		t.Fatalf("expected creator picker to clear precise id view: %+v", payload)
	}
}

func TestParseTaskActionRequest(t *testing.T) {
	req, err := ParseTaskActionRequest(&cardactionproto.Parsed{
		Name: cardactionproto.ActionScheduleResume,
		Value: map[string]any{
			cardactionproto.IDField:   "task-2",
			taskCardViewModeField:     "query",
			taskCardViewNameField:     "提醒",
			taskCardViewStatusField:   "paused",
			taskCardViewToolNameField: "send_message",
			taskCardViewCreatorField:  "ou_creator",
			taskCardViewLimitField:    "25",
		},
	})
	if err != nil {
		t.Fatalf("ParseTaskActionRequest() error = %v", err)
	}
	if req.Action != TaskActionResume || req.ID != "task-2" {
		t.Fatalf("unexpected req: %+v", req)
	}
	if req.View.Mode != TaskCardViewModeQuery || req.View.Name != "提醒" || req.View.Status != "paused" || req.View.ToolName != "send_message" || req.View.CreatorOpenID != "ou_creator" || req.View.Limit != 25 {
		t.Fatalf("unexpected view: %+v", req.View)
	}
}

func TestParseTaskViewRequest(t *testing.T) {
	req, err := ParseTaskViewRequest(&cardactionproto.Parsed{
		Name: cardactionproto.ActionScheduleView,
		Value: map[string]any{
			taskCardViewModeField:     "query",
			taskCardViewIDField:       "task-2",
			taskCardViewNameField:     "提醒",
			taskCardViewStatusField:   "paused",
			taskCardViewTaskTypeField: "once",
			taskCardViewToolNameField: "send_message",
			taskCardViewCreatorField:  "ou_creator",
			taskCardViewLimitField:    "25",
		},
	})
	if err != nil {
		t.Fatalf("ParseTaskViewRequest() error = %v", err)
	}
	if req.View.Mode != TaskCardViewModeQuery || req.View.ID != "task-2" || req.View.Name != "提醒" || req.View.TaskType != "once" || req.View.CreatorOpenID != "ou_creator" || req.View.Limit != 25 {
		t.Fatalf("unexpected view: %+v", req.View)
	}
}

func TestParseTaskViewRequestUsesSelectPersonOption(t *testing.T) {
	req, err := ParseTaskViewRequest(&cardactionproto.Parsed{
		Name:   cardactionproto.ActionScheduleView,
		Tag:    "select_person",
		Option: "ou_picker_selected",
		Value: map[string]any{
			taskCardViewModeField:    "query",
			taskCardViewStatusField:  "paused",
			taskCardViewSelectField:  taskCardViewSelectCreator,
			taskCardViewCreatorField: "ou_creator_old",
		},
	})
	if err != nil {
		t.Fatalf("ParseTaskViewRequest() error = %v", err)
	}
	if req.View.CreatorOpenID != "ou_picker_selected" {
		t.Fatalf("expected picker selected creator, got %+v", req.View)
	}
}

func TestParseTaskViewRequestUsesSelectPersonOptionsFallback(t *testing.T) {
	req, err := ParseTaskViewRequest(&cardactionproto.Parsed{
		Name:    cardactionproto.ActionScheduleView,
		Tag:     "select_person",
		Options: []string{"ou_picker_selected"},
		Value: map[string]any{
			taskCardViewModeField:    "query",
			taskCardViewStatusField:  "paused",
			taskCardViewSelectField:  taskCardViewSelectCreator,
			taskCardViewCreatorField: "ou_creator_old",
		},
	})
	if err != nil {
		t.Fatalf("ParseTaskViewRequest() error = %v", err)
	}
	if req.View.CreatorOpenID != "ou_picker_selected" {
		t.Fatalf("expected picker selected creator from options fallback, got %+v", req.View)
	}
}

func TestBuildTaskCardPayloadForViewIgnoresDeletedIDQuery(t *testing.T) {
	useWorkspaceConfigPath(t)
	previous := globalService
	globalService = scheduleTestService{
		noopService: noopService{reason: "test"},
		getTaskFunc: func(context.Context, string) (*model.ScheduledTask, error) {
			return nil, errTestTaskNotFound
		},
	}
	t.Cleanup(func() { globalService = previous })

	card, err := BuildTaskCardPayloadForView(context.Background(), "chat-1", NewTaskQueryCardView("task-404", TaskQuery{}, 0), true)
	if err != nil {
		t.Fatalf("BuildTaskCardPayloadForView() error = %v", err)
	}
	body, ok := card["body"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected card body: %#v", card["body"])
	}
	elements, ok := body["elements"].([]any)
	if !ok || len(elements) == 0 {
		t.Fatalf("unexpected card elements: %#v", body["elements"])
	}
	allContent := strings.Join(collectMarkdownContents(elements), "\n")
	if !strings.Contains(allContent, "暂无匹配的 schedule ⏲️") {
		t.Fatalf("unexpected empty content: %#v", elements)
	}
}

func TestBuildTaskCardPayloadForViewFiltersTasks(t *testing.T) {
	useWorkspaceConfigPath(t)
	previous := globalService
	globalService = scheduleTestService{
		noopService: noopService{reason: "test"},
		listTasksFunc: func(context.Context, *ListTasksRequest) ([]*model.ScheduledTask, error) {
			return []*model.ScheduledTask{
				{
					ID:        "task-1",
					ChatID:    "chat-1",
					Name:      "早报提醒",
					Status:    model.ScheduleTaskStatusEnabled,
					Type:      model.ScheduleTaskTypeCron,
					ToolName:  "send_message",
					CronExpr:  "0 9 * * 1-5",
					NextRunAt: time.Now(),
					Timezone:  model.ScheduleTaskDefaultTimezone,
				},
				{
					ID:       "task-2",
					ChatID:   "chat-1",
					Name:     "晚间复盘",
					Status:   model.ScheduleTaskStatusPaused,
					Type:     model.ScheduleTaskTypeOnce,
					ToolName: "search_history",
					Timezone: model.ScheduleTaskDefaultTimezone,
				},
			}, nil
		},
	}
	t.Cleanup(func() { globalService = previous })

	card, err := BuildTaskCardPayloadForView(context.Background(), "chat-1", NewTaskQueryCardView("", TaskQuery{
		Status: model.ScheduleTaskStatusPaused,
	}, 0), false)
	if err != nil {
		t.Fatalf("BuildTaskCardPayloadForView() error = %v", err)
	}
	body := card["body"].(map[string]any)
	elements := body["elements"].([]any)
	allContent := strings.Join(collectMarkdownContents(elements), "\n")
	foundTask2 := containsAll(allContent, "晚间复盘", "状态: `paused`")
	foundTask1 := containsAll(allContent, "早报提醒", "状态: `enabled`")
	if !foundTask2 || foundTask1 {
		t.Fatalf("unexpected filtered card elements: %#v", elements)
	}
}

func collectMarkdownContents(value any) []string {
	switch typed := value.(type) {
	case []any:
		result := make([]string, 0)
		for _, item := range typed {
			result = append(result, collectMarkdownContents(item)...)
		}
		return result
	case map[string]any:
		result := make([]string, 0)
		if tag, _ := typed["tag"].(string); tag == "markdown" {
			if content, _ := typed["content"].(string); content != "" {
				result = append(result, content)
			}
		}
		for _, field := range typed {
			result = append(result, collectMarkdownContents(field)...)
		}
		return result
	default:
		return nil
	}
}

func TestGetTaskForChatRejectsCrossChatTask(t *testing.T) {
	useWorkspaceConfigPath(t)
	previous := globalService
	globalService = scheduleTestService{
		noopService: noopService{reason: "test"},
		getTaskFunc: func(context.Context, string) (*model.ScheduledTask, error) {
			return &model.ScheduledTask{
				ID:     "task-1",
				ChatID: "chat-other",
			}, nil
		},
	}
	t.Cleanup(func() { globalService = previous })

	_, err := GetTaskForChat(context.Background(), "chat-1", "task-1")
	if err == nil || !isTaskNotFoundErr(err) {
		t.Fatalf("expected not found error, got %v", err)
	}
}

var errTestTaskNotFound = testTaskNotFoundError("task not found")

type testTaskNotFoundError string

func (e testTaskNotFoundError) Error() string { return string(e) }

type scheduleTestService struct {
	noopService
	getTaskFunc   func(context.Context, string) (*model.ScheduledTask, error)
	listTasksFunc func(context.Context, *ListTasksRequest) ([]*model.ScheduledTask, error)
}

func (s scheduleTestService) Available() bool { return true }

func (s scheduleTestService) GetTask(ctx context.Context, id string) (*model.ScheduledTask, error) {
	if s.getTaskFunc != nil {
		return s.getTaskFunc(ctx, id)
	}
	return s.noopService.GetTask(ctx, id)
}

func (s scheduleTestService) ListTasks(ctx context.Context, req *ListTasksRequest) ([]*model.ScheduledTask, error) {
	if s.listTasksFunc != nil {
		return s.listTasksFunc(ctx, req)
	}
	return s.noopService.ListTasks(ctx, req)
}

func containsAll(content string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(content, part) {
			return false
		}
	}
	return true
}
