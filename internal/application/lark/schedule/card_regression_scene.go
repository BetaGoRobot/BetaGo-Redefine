package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	scheduleinfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/schedule"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
)

const (
	scheduleListSceneKey     = "schedule.list"
	scheduleQuerySceneKey    = "schedule.query"
	scheduleRegressionChatID = "oc_sample_debug_chat"
	scheduleRegressionActor  = "ou_sample_creator"
	scheduleRegressionTaskID = "20260312093000-debugA"
)

func RegisterRegressionScenes(registry *cardregression.Registry) {
	if registry == nil {
		return
	}
	for _, scene := range []cardregression.CardSceneProtocol{
		scheduleListRegressionScene{},
		scheduleQueryRegressionScene{},
	} {
		if _, exists := registry.Get(scene.SceneKey()); exists {
			continue
		}
		registry.MustRegister(scene)
	}
}

type scheduleListRegressionScene struct{}

type scheduleQueryRegressionScene struct{}

func (scheduleListRegressionScene) SceneKey() string { return scheduleListSceneKey }

func (scheduleListRegressionScene) Meta() cardregression.CardSceneMeta {
	return cardregression.CardSceneMeta{
		Name:        scheduleListSceneKey,
		Description: "Schedule 列表卡回归场景",
		Tags:        []string{"schema-v2", "schedule"},
		Owner:       "schedule",
	}
}

func (scheduleListRegressionScene) TestCases() []cardregression.CardRegressionCase {
	return []cardregression.CardRegressionCase{
		{
			Name:        "smoke-default",
			Description: "使用样例任务构建 Schedule 列表卡",
			Args: map[string]string{
				"chat_id":       scheduleRegressionChatID,
				"actor_open_id": scheduleRegressionActor,
			},
			Tags: []string{"smoke"},
		},
		{
			Name:        "live-default",
			Description: "使用真实 chat_id 构建 Schedule 列表卡",
			Requires: cardregression.CardRequirementSet{
				NeedBusinessChatID: true,
				NeedDB:             true,
			},
			Tags: []string{"live"},
		},
	}
}

func (s scheduleListRegressionScene) BuildCard(ctx context.Context, req cardregression.CardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, req.Args, false)
}

func (s scheduleListRegressionScene) BuildTestCard(ctx context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, mergeScheduleRegressionArgs(req.Case.Args, req.Args), strings.TrimSpace(req.Case.Name) == "smoke-default")
}

func (scheduleListRegressionScene) build(ctx context.Context, business cardregression.CardBusinessContext, args map[string]string, smoke bool) (*cardregression.BuiltCard, error) {
	if smoke {
		card := BuildTaskListCard(ctx, "Schedule 列表", buildSampleScheduleTasks(firstNonEmptySchedule(business.ChatID, args["chat_id"]), firstNonEmptySchedule(business.ActorOpenID, args["actor_open_id"])), NewTaskListCardView(10))
		return &cardregression.BuiltCard{
			Mode:     cardregression.BuiltCardModeCardJSON,
			Label:    scheduleListSceneKey,
			CardJSON: map[string]any(card),
		}, nil
	}

	chatID := firstNonEmptySchedule(business.ChatID, args["chat_id"])
	repo, err := newRegressionRepository()
	if err != nil {
		return nil, err
	}
	tasks, err := repo.ListTasksByChatID(ctx, chatID, 50, 0)
	if err != nil {
		return nil, err
	}
	view := NewTaskListCardView(50)
	view.ChatID = chatID
	card := BuildTaskListCard(ctx, view.Title(), tasks, view)
	return &cardregression.BuiltCard{
		Mode:     cardregression.BuiltCardModeCardJSON,
		Label:    scheduleListSceneKey,
		CardJSON: map[string]any(card),
	}, nil
}

func (scheduleQueryRegressionScene) SceneKey() string { return scheduleQuerySceneKey }

func (scheduleQueryRegressionScene) Meta() cardregression.CardSceneMeta {
	return cardregression.CardSceneMeta{
		Name:        scheduleQuerySceneKey,
		Description: "Schedule 查询卡回归场景",
		Tags:        []string{"schema-v2", "schedule"},
		Owner:       "schedule",
	}
}

func (scheduleQueryRegressionScene) TestCases() []cardregression.CardRegressionCase {
	return []cardregression.CardRegressionCase{
		{
			Name:        "smoke-default",
			Description: "使用样例任务构建 Schedule 查询卡",
			Args: map[string]string{
				"id":            scheduleRegressionTaskID,
				"chat_id":       scheduleRegressionChatID,
				"actor_open_id": scheduleRegressionActor,
			},
			Tags: []string{"smoke"},
		},
		{
			Name:        "live-default",
			Description: "使用真实 task_id 构建 Schedule 查询卡",
			Requires: cardregression.CardRequirementSet{
				NeedObjectID: true,
				NeedDB:       true,
			},
			Tags: []string{"live"},
		},
	}
}

func (s scheduleQueryRegressionScene) BuildCard(ctx context.Context, req cardregression.CardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, req.Args, false)
}

func (s scheduleQueryRegressionScene) BuildTestCard(ctx context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, mergeScheduleRegressionArgs(req.Case.Args, req.Args), strings.TrimSpace(req.Case.Name) == "smoke-default")
}

func (scheduleQueryRegressionScene) build(ctx context.Context, business cardregression.CardBusinessContext, args map[string]string, smoke bool) (*cardregression.BuiltCard, error) {
	chatID := firstNonEmptySchedule(business.ChatID, args["chat_id"])
	taskID := firstNonEmptySchedule(business.ObjectID, args["id"])
	if smoke {
		tasks := buildSampleScheduleTasks(chatID, firstNonEmptySchedule(business.ActorOpenID, args["actor_open_id"]))
		task := tasks[0]
		view := NewTaskQueryCardView(task.ID, TaskQuery{ChatID: task.ChatID}, 50)
		card := BuildTaskListCard(ctx, view.Title(), []*model.ScheduledTask{task}, view)
		return &cardregression.BuiltCard{
			Mode:     cardregression.BuiltCardModeCardJSON,
			Label:    scheduleQuerySceneKey,
			CardJSON: map[string]any(card),
		}, nil
	}

	repo, err := newRegressionRepository()
	if err != nil {
		return nil, err
	}
	task, err := repo.GetTaskByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if chatID != "" && strings.TrimSpace(task.ChatID) != chatID {
		return nil, fmt.Errorf("task not found")
	}
	view := NewTaskQueryCardView(taskID, TaskQuery{ChatID: task.ChatID}, 100)
	card := BuildTaskListCard(ctx, view.Title(), []*model.ScheduledTask{task}, view)
	return &cardregression.BuiltCard{
		Mode:     cardregression.BuiltCardModeCardJSON,
		Label:    scheduleQuerySceneKey,
		CardJSON: map[string]any(card),
	}, nil
}

func newRegressionRepository() (*scheduleinfra.Repository, error) {
	if infraDB.DB() == nil {
		return nil, fmt.Errorf("db is required for schedule regression scene")
	}
	identity := botidentity.Current()
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	return scheduleinfra.NewRepository(infraDB.DB(), identity), nil
}

func buildSampleScheduleTasks(chatID, creatorOpenID string) []*model.ScheduledTask {
	now := time.Now().In(utils.UTC8Loc())
	chatID = firstNonEmptySchedule(chatID, scheduleRegressionChatID)
	creatorOpenID = firstNonEmptySchedule(creatorOpenID, scheduleRegressionActor)

	onceRunAt := now.Add(20 * time.Minute)
	onceLastRunAt := now.Add(-2 * time.Hour)
	cronNextRunAt := now.Add(47 * time.Minute)
	cronLastRunAt := now.Add(-23 * time.Hour)

	onceTask := &model.ScheduledTask{
		ID:              scheduleRegressionTaskID,
		Name:            "午休提醒",
		Type:            model.ScheduleTaskTypeOnce,
		ChatID:          chatID,
		CreatorID:       creatorOpenID,
		ToolName:        "send_message",
		ToolArgs:        `{"message":"记得午休 20 分钟"}`,
		RunAt:           &onceRunAt,
		Timezone:        "Asia/Shanghai",
		Status:          model.ScheduleTaskStatusEnabled,
		NotifyOnError:   true,
		NotifyResult:    false,
		LastRunAt:       &onceLastRunAt,
		NextRunAt:       onceRunAt,
		LastResult:      "已发送提醒消息，用户点击了卡片中的确认按钮。",
		RunCount:        2,
		CreatedAt:       now.Add(-48 * time.Hour),
		UpdatedAt:       now.Add(-6 * time.Minute),
		AppID:           "cli_sample_debug",
		BotOpenID:       "ou_sample_bot",
		SourceMessageID: "om_sample_source_1",
	}
	cronTask := &model.ScheduledTask{
		ID:              "20260312094500-debugB",
		Name:            "日报汇总",
		Type:            model.ScheduleTaskTypeCron,
		ChatID:          chatID,
		CreatorID:       creatorOpenID,
		ToolName:        "report_daily",
		ToolArgs:        `{"channel":"ops","format":"markdown"}`,
		CronExpr:        "0 18 * * *",
		Timezone:        "Asia/Shanghai",
		Status:          model.ScheduleTaskStatusPaused,
		NotifyOnError:   true,
		NotifyResult:    true,
		LastRunAt:       &cronLastRunAt,
		NextRunAt:       cronNextRunAt,
		LastError:       "上次执行时下游报表服务超时，已自动暂停等待人工确认。",
		RunCount:        14,
		CreatedAt:       now.Add(-7 * 24 * time.Hour),
		UpdatedAt:       now.Add(-28 * time.Minute),
		AppID:           "cli_sample_debug",
		BotOpenID:       "ou_sample_bot",
		SourceMessageID: "om_sample_source_2",
	}
	return []*model.ScheduledTask{onceTask, cronTask}
}

func mergeScheduleRegressionArgs(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	for k, v := range override {
		merged[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return merged
}

func firstNonEmptySchedule(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
