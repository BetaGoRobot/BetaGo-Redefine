package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	toolkit "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	scheduleinfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/schedule"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const onceTaskLeaseDuration = 2 * time.Minute
const onceTaskRetryDelay = time.Minute

var (
	globalService TaskService = noopService{reason: "schedule service not initialized"}
	warnOnce      sync.Once
)

type TaskService interface {
	Available() bool
	AvailableTools() []string
	CreateTask(ctx context.Context, req *CreateTaskRequest) (*model.ScheduledTask, error)
	ListTasks(ctx context.Context, req *ListTasksRequest) ([]*model.ScheduledTask, error)
	DeleteTask(ctx context.Context, id string) error
	PauseTask(ctx context.Context, id string) error
	ResumeTask(ctx context.Context, id string) (*model.ScheduledTask, error)
	GetDueTasks(ctx context.Context, limit int) ([]*model.ScheduledTask, error)
	ClaimTaskExecution(ctx context.Context, task *model.ScheduledTask, now time.Time) (bool, error)
	ExecuteTask(ctx context.Context, task *model.ScheduledTask) (string, error)
	FinalizeTaskExecution(ctx context.Context, task *model.ScheduledTask, resultText string, execErr error, finishedAt time.Time) error
}

type Service struct {
	repo     *scheduleinfra.Repository
	executor *ToolExecutor
}

type noopService struct {
	reason string
}

type CreateTaskRequest struct {
	ChatID        string
	CreatorID     string
	Name          string
	Type          string
	RunAt         *time.Time
	CronExpr      string
	Timezone      string
	Message       string
	ToolName      string
	ToolArgs      string
	NotifyOnError bool
	NotifyResult  bool
}

type ListTasksRequest struct {
	ChatID string
	Limit  int
	Offset int
}

type sendMessageArgs struct {
	Content string `json:"content"`
}

func Init(db *gorm.DB, schedulableTools *toolkit.Impl[larkim.P2MessageReceiveV1]) {
	if db == nil {
		setNoopService("schedule db unavailable")
		return
	}
	if schedulableTools == nil {
		setNoopService("schedule tool registry unavailable")
		return
	}
	globalService = &Service{
		repo:     scheduleinfra.NewRepository(db),
		executor: NewToolExecutor(schedulableTools),
	}
}

func GetService() TaskService {
	return globalService
}

func setNoopService(reason string) {
	globalService = noopService{reason: reason}
	warnOnce.Do(func() {
		logs.L().Warn("Schedule service disabled, falling back to noop",
			zap.String("reason", reason),
		)
	})
}

func NewService(repo *scheduleinfra.Repository, executor *ToolExecutor) *Service {
	return &Service{repo: repo, executor: executor}
}

func (s *Service) Available() bool {
	return s != nil && s.repo != nil && s.executor != nil
}

func (s *Service) AvailableTools() []string {
	if !s.Available() {
		return nil
	}
	return s.executor.AvailableTools()
}

func (s *Service) CreateTask(ctx context.Context, req *CreateTaskRequest) (*model.ScheduledTask, error) {
	taskType := strings.ToLower(strings.TrimSpace(req.Type))
	toolName, toolArgs, err := s.resolveAction(req)
	if err != nil {
		return nil, err
	}

	task := model.NewScheduledTask(req.Name, taskType, req.ChatID, req.CreatorID, toolName, toolArgs, strings.TrimSpace(req.Timezone))
	task.RunAt = req.RunAt
	task.CronExpr = strings.TrimSpace(req.CronExpr)
	task.NotifyOnError = req.NotifyOnError
	task.NotifyResult = req.NotifyResult
	if task.ToolName == "send_message" {
		task.NotifyResult = false
	}

	if err := task.ValidateBasic(); err != nil {
		return nil, err
	}
	if err := validateToolArgs(task.ToolArgs); err != nil {
		return nil, err
	}
	if !s.executor.CanExecute(task.ToolName) {
		return nil, fmt.Errorf("tool %q is not schedulable, available tools: %s", task.ToolName, strings.Join(s.AvailableTools(), ", "))
	}

	nextRunAt, normalizedRunAt, err := s.computeInitialRun(task, time.Now())
	if err != nil {
		return nil, err
	}
	task.RunAt = normalizedRunAt
	task.NextRunAt = nextRunAt

	if err := s.repo.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) ListTasks(ctx context.Context, req *ListTasksRequest) ([]*model.ScheduledTask, error) {
	if req.Limit <= 0 {
		req.Limit = 50
	}
	return s.repo.ListTasksByChatID(ctx, req.ChatID, req.Limit, req.Offset)
}

func (s *Service) DeleteTask(ctx context.Context, id string) error {
	return s.repo.DeleteTask(ctx, id)
}

func (s *Service) PauseTask(ctx context.Context, id string) error {
	task, err := s.repo.GetTaskByID(ctx, id)
	if err != nil {
		return err
	}
	if task.Status != model.ScheduleTaskStatusEnabled {
		return fmt.Errorf("only enabled schedules can be paused")
	}
	return s.repo.PauseTask(ctx, id)
}

func (s *Service) ResumeTask(ctx context.Context, id string) (*model.ScheduledTask, error) {
	task, err := s.repo.GetTaskByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status != model.ScheduleTaskStatusPaused {
		return nil, fmt.Errorf("only paused schedules can be resumed")
	}

	now := time.Now()
	nextRunAt, err := s.computeResumeRun(task, now)
	if err != nil {
		return nil, err
	}
	if err := s.repo.ResumeTask(ctx, id, nextRunAt); err != nil {
		return nil, err
	}
	task.Resume()
	task.NextRunAt = nextRunAt
	return task, nil
}

func (s *Service) GetDueTasks(ctx context.Context, limit int) ([]*model.ScheduledTask, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.repo.ListDueTasks(ctx, time.Now(), limit)
}

func (s *Service) ClaimTaskExecution(ctx context.Context, task *model.ScheduledTask, now time.Time) (bool, error) {
	updates := map[string]any{}
	if task.IsOnce() {
		leaseUntil := now.Add(onceTaskLeaseDuration)
		updates["next_run_at"] = leaseUntil
		task.NextRunAt = leaseUntil
	} else {
		nextRunAt, err := computeNextRun(task.CronExpr, task.Timezone, now)
		if err != nil {
			return false, err
		}
		updates["next_run_at"] = nextRunAt
		task.NextRunAt = nextRunAt
	}

	ok, err := s.repo.ClaimTaskRun(ctx, task.ID, now, updates)
	if ok {
		task.LastRunAt = &now
	}
	return ok, err
}

func (s *Service) ExecuteTask(ctx context.Context, task *model.ScheduledTask) (string, error) {
	return s.executor.Execute(ctx, task)
}

func (s *Service) FinalizeTaskExecution(ctx context.Context, task *model.ScheduledTask, resultText string, execErr error, finishedAt time.Time) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}

	updates := map[string]any{
		"last_result": resultText,
		"last_error":  "",
	}

	if execErr != nil {
		updates["last_error"] = execErr.Error()
		if task.IsOnce() {
			updates["next_run_at"] = finishedAt.Add(onceTaskRetryDelay)
			updates["status"] = model.ScheduleTaskStatusEnabled
			task.Status = model.ScheduleTaskStatusEnabled
			task.NextRunAt = finishedAt.Add(onceTaskRetryDelay)
		}
	} else if task.IsOnce() {
		updates["status"] = model.ScheduleTaskStatusCompleted
		updates["next_run_at"] = finishedAt
		task.Complete()
		task.NextRunAt = finishedAt
	}

	task.LastResult = resultText
	if execErr != nil {
		task.LastError = execErr.Error()
	} else {
		task.LastError = ""
	}

	return s.repo.UpdateTaskFields(ctx, task.ID, updates)
}

func (s *Service) resolveAction(req *CreateTaskRequest) (string, string, error) {
	message := strings.TrimSpace(req.Message)
	toolName := strings.TrimSpace(req.ToolName)
	toolArgs := normalizeToolArgs(req.ToolArgs)

	switch {
	case message != "" && toolName != "":
		return "", "", fmt.Errorf("message and tool_name are mutually exclusive")
	case message != "":
		payload := sendMessageArgs{Content: message}
		return "send_message", utils.MustMarshalString(payload), nil
	case toolName == "":
		return "", "", fmt.Errorf("either message or tool_name is required")
	default:
		return toolName, toolArgs, nil
	}
}

func (s *Service) computeInitialRun(task *model.ScheduledTask, now time.Time) (time.Time, *time.Time, error) {
	if task.IsCron() {
		nextRunAt, err := computeNextRun(task.CronExpr, task.Timezone, now)
		return nextRunAt, nil, err
	}

	if task.RunAt == nil {
		return time.Time{}, nil, fmt.Errorf("run_at is required for once task")
	}
	loc, err := resolveLocation(task.Timezone)
	if err != nil {
		return time.Time{}, nil, err
	}
	runAt := task.RunAt.In(loc)
	if runAt.Before(now.Add(-1 * time.Second)) {
		return time.Time{}, nil, fmt.Errorf("run_at must be in the future")
	}
	return runAt, &runAt, nil
}

func (s *Service) computeResumeRun(task *model.ScheduledTask, now time.Time) (time.Time, error) {
	if task.IsCron() {
		return computeNextRun(task.CronExpr, task.Timezone, now)
	}
	if task.RunAt == nil {
		return time.Time{}, fmt.Errorf("once schedule is missing run_at")
	}
	if task.Status == model.ScheduleTaskStatusCompleted {
		return time.Time{}, fmt.Errorf("completed one-time schedules cannot be resumed")
	}
	if task.RunAt.Before(now) {
		return now, nil
	}
	return *task.RunAt, nil
}

func (noopService) Available() bool { return false }

func (noopService) AvailableTools() []string { return nil }

func (n noopService) CreateTask(context.Context, *CreateTaskRequest) (*model.ScheduledTask, error) {
	return nil, fmt.Errorf("schedule service unavailable: %s", n.reason)
}

func (n noopService) ListTasks(context.Context, *ListTasksRequest) ([]*model.ScheduledTask, error) {
	return nil, fmt.Errorf("schedule service unavailable: %s", n.reason)
}

func (n noopService) DeleteTask(context.Context, string) error {
	return fmt.Errorf("schedule service unavailable: %s", n.reason)
}

func (n noopService) PauseTask(context.Context, string) error {
	return fmt.Errorf("schedule service unavailable: %s", n.reason)
}

func (n noopService) ResumeTask(context.Context, string) (*model.ScheduledTask, error) {
	return nil, fmt.Errorf("schedule service unavailable: %s", n.reason)
}

func (n noopService) GetDueTasks(context.Context, int) ([]*model.ScheduledTask, error) {
	return nil, fmt.Errorf("schedule service unavailable: %s", n.reason)
}

func (n noopService) ClaimTaskExecution(context.Context, *model.ScheduledTask, time.Time) (bool, error) {
	return false, fmt.Errorf("schedule service unavailable: %s", n.reason)
}

func (n noopService) ExecuteTask(context.Context, *model.ScheduledTask) (string, error) {
	return "", fmt.Errorf("schedule service unavailable: %s", n.reason)
}

func (n noopService) FinalizeTaskExecution(context.Context, *model.ScheduledTask, string, error, time.Time) error {
	return fmt.Errorf("schedule service unavailable: %s", n.reason)
}

func FormatTaskList(tasks []*model.ScheduledTask) string {
	if len(tasks) == 0 {
		return "暂无 schedule ⏲️"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⏲️ Schedule 列表（共 %d 项）\n\n", len(tasks)))
	for i, task := range tasks {
		loc, err := resolveLocation(task.Timezone)
		if err != nil {
			loc = utils.UTC8Loc()
		}
		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, task.Name))
		sb.WriteString(fmt.Sprintf("   模式: %s\n", task.Type))
		sb.WriteString(fmt.Sprintf("   动作: %s\n", task.ToolName))
		if task.IsOnce() && task.RunAt != nil {
			sb.WriteString(fmt.Sprintf("   执行时间: %s\n", task.RunAt.In(loc).Format("2006-01-02 15:04:05")))
		}
		if task.IsCron() {
			sb.WriteString(fmt.Sprintf("   Cron: `%s`\n", task.CronExpr))
			sb.WriteString(fmt.Sprintf("   下次执行: %s\n", task.NextRunAt.In(loc).Format("2006-01-02 15:04:05")))
		}
		sb.WriteString(fmt.Sprintf("   时区: %s\n", task.Timezone))
		sb.WriteString(fmt.Sprintf("   状态: %s\n", task.Status))
		if task.LastRunAt != nil {
			sb.WriteString(fmt.Sprintf("   上次执行: %s\n", task.LastRunAt.In(loc).Format("2006-01-02 15:04:05")))
		}
		if task.LastError != "" {
			sb.WriteString(fmt.Sprintf("   最近错误: %s\n", task.LastError))
		}
		sb.WriteString(fmt.Sprintf("   ID: `%s`\n\n", task.ID))
	}
	return sb.String()
}

func computeNextRun(cronExpr, timezone string, from time.Time) (time.Time, error) {
	loc, err := resolveLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}

	schedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
	}

	next := schedule.Next(from.In(loc))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("cron expression %q does not yield a next run time", cronExpr)
	}
	return next, nil
}

func resolveLocation(timezone string) (*time.Location, error) {
	switch timezone {
	case "", model.ScheduleTaskDefaultTimezone:
		return time.LoadLocation(model.ScheduleTaskDefaultTimezone)
	case "UTC+8", "UTC+08:00":
		return utils.UTC8Loc(), nil
	default:
		return time.LoadLocation(timezone)
	}
}

func normalizeToolArgs(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	return raw
}

func validateToolArgs(raw string) error {
	if !json.Valid([]byte(raw)) {
		return fmt.Errorf("tool_args must be valid JSON")
	}
	return nil
}

func uniqueSorted(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, item := range input {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}
