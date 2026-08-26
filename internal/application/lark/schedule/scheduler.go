package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

type Scheduler struct {
	service   TaskService
	executor  taskSubmitter
	reviewer  TaskResultReviewer
	notifier  TaskNotifier
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	running   bool
	checkTick *time.Ticker
}

var (
	globalSchedulers   = make(map[string]*Scheduler)
	globalSchedulersMu sync.Mutex
)

type taskSubmitter interface {
	Submit(context.Context, string, func(context.Context) error) error
}

func NewScheduler(service TaskService) *Scheduler {
	return NewSchedulerWithExecutor(service, nil)
}

func NewSchedulerWithExecutor(service TaskService, executor taskSubmitter) *Scheduler {
	return newSchedulerWithDependencies(
		service,
		executor,
		newModelTaskResultReviewer(),
		newLarkTaskNotifier(),
	)
}

func newSchedulerWithDependencies(
	service TaskService,
	executor taskSubmitter,
	reviewer TaskResultReviewer,
	notifier TaskNotifier,
) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		service:  service,
		executor: executor,
		reviewer: reviewer,
		notifier: notifier,
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.checkTick = time.NewTicker(30 * time.Second)
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run()

	logs.L().Info("Scheduled task scheduler started")
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	if s.checkTick != nil {
		s.checkTick.Stop()
	}
	s.cancel()
	s.mu.Unlock()

	s.wg.Wait()
	logs.L().Info("Scheduled task scheduler stopped")
}

func (s *Scheduler) run() {
	defer s.wg.Done()

	s.checkAndTrigger()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.checkTick.C:
			s.checkAndTrigger()
		}
	}
}

func (s *Scheduler) checkAndTrigger() {
	ctx := s.ctx
	ctx, span := otel.StartNamed(ctx, "schedule.scheduler.check")
	defer span.End()

	tasks, err := s.service.GetDueTasks(ctx, 100)
	if err != nil {
		otel.RecordError(span, err)
		logs.L().Ctx(ctx).Error("Get due scheduled tasks failed", zap.Error(err))
		return
	}
	span.SetAttributes(attribute.Int("schedule.due_tasks.count", len(tasks)))
	if len(tasks) == 0 {
		return
	}

	now := time.Now()
	logs.L().Ctx(ctx).Info("Found due scheduled tasks", zap.Int("count", len(tasks)))
	for _, task := range tasks {
		claimed, err := s.service.ClaimTaskExecution(ctx, task, now)
		if err != nil {
			otel.RecordError(span, err)
			logs.L().Ctx(ctx).Error("Claim scheduled task failed",
				zap.Error(err),
				zap.String("task_id", task.ID),
				zap.String("task_name", task.Name))
			continue
		}
		if !claimed {
			continue
		}

		task.LastRunAt = &now
		if s.executor != nil {
			if err := s.executor.Submit(s.ctx, "schedule_task:"+task.ID, func(taskCtx context.Context) error {
				s.executeTask(taskCtx, task)
				return nil
			}); err != nil {
				logs.L().Ctx(ctx).Error("Submit scheduled task failed",
					zap.Error(err),
					zap.String("task_id", task.ID),
					zap.String("task_name", task.Name))
			}
			continue
		}
		go s.executeTask(s.ctx, task)
	}
}

func (s *Scheduler) executeTask(ctx context.Context, task *model.ScheduledTask) {
	ctx, span := otel.StartNamed(ctx, "schedule.scheduler.execute")
	defer span.End()
	span.SetAttributes(
		attribute.String("schedule.task_id", task.ID),
		attribute.String("schedule.task_name.preview", otel.PreviewString(task.Name, 128)),
		attribute.String("schedule.tool_name", task.ToolName),
	)
	logs.L().Ctx(ctx).Info("Executing scheduled task",
		zap.String("task_id", task.ID),
		zap.String("task_name", task.Name),
		zap.String("tool_name", task.ToolName))

	result, err := s.service.ExecuteTask(ctx, task)
	otel.RecordError(span, err)
	if err != nil {
		if strings.Contains(err.Error(), "Bot/User can NOT be out of the chat.") {
			logs.L().Ctx(ctx).Warn("Scheduled task execution failed, bot/user is out of the chat",
				zap.Error(err),
				zap.String("task_id", task.ID),
				zap.String("task_name", task.Name),
				zap.String("tool_name", task.ToolName))
			err = nil
		} else {
			logs.L().Ctx(ctx).Error("Scheduled task execution failed",
				zap.Error(err),
				zap.String("task_id", task.ID),
				zap.String("task_name", task.Name),
				zap.String("tool_name", task.ToolName))
		}
	} else {
		logs.L().Ctx(ctx).Info("Scheduled task executed successfully",
			zap.String("task_id", task.ID),
			zap.String("task_name", task.Name),
			zap.String("tool_name", task.ToolName))
	}

	finishedAt := time.Now()
	if updateErr := s.service.FinalizeTaskExecution(ctx, task, result, err, finishedAt); updateErr != nil {
		otel.RecordError(span, updateErr)
		logs.L().Ctx(ctx).Error("Finalize scheduled task execution failed",
			zap.Error(updateErr),
			zap.String("task_id", task.ID))
	}

	if err != nil && task.NotifyOnError {
		if notifyErr := s.notify(ctx, task, fmt.Sprintf("⚠️ 定时任务执行失败\n\n任务: %s\n工具: %s\n错误: %s", task.Name, task.ToolName, err.Error())); notifyErr != nil {
			otel.RecordError(span, notifyErr)
		}
		return
	}
	if err == nil {
		if reviewErr := s.reviewAndNotifyResult(ctx, task, result, finishedAt); reviewErr != nil {
			otel.RecordError(span, reviewErr)
		}
	}
}

func (s *Scheduler) reviewAndNotifyResult(
	ctx context.Context,
	task *model.ScheduledTask,
	result string,
	finishedAt time.Time,
) error {
	if task == nil {
		return errors.New("task is nil")
	}
	if !task.NotifyResult {
		return nil
	}
	ctx, span := otel.StartNamed(ctx, "schedule.result_review")
	defer span.End()
	span.SetAttributes(
		attribute.String("schedule.task_id", task.ID),
		attribute.String("schedule.tool_name", task.ToolName),
		attribute.Int("schedule.result.len", len(result)),
	)
	if s == nil || s.reviewer == nil {
		return errors.New("scheduled task result reviewer is not configured")
	}

	decision, err := s.reviewer.Review(ctx, task, result, finishedAt)
	if err != nil {
		reviewErr := fmt.Errorf("review scheduled task result: %w", err)
		otel.RecordError(span, reviewErr)
		logs.L().Ctx(ctx).Error("Scheduled task result review failed",
			zap.Error(reviewErr),
			zap.String("task_id", task.ID),
			zap.String("task_name", task.Name),
			zap.String("tool_name", task.ToolName))
		if !task.NotifyOnError {
			return reviewErr
		}
		notifyErr := s.notify(ctx, task, fmt.Sprintf(
			"⚠️ 定时任务结果审核失败\n\n任务: %s\n工具: %s\n原始结果已保存，可通过 schedule 查询查看。",
			task.Name,
			task.ToolName,
		))
		if notifyErr != nil {
			return errors.Join(reviewErr, notifyErr)
		}
		return reviewErr
	}

	span.SetAttributes(
		attribute.Bool("schedule.result_review.send", decision.Send),
		attribute.String("schedule.result_review.reason.preview", otel.PreviewString(decision.Reason, 128)),
	)
	if !decision.Send {
		logs.L().Ctx(ctx).Info("Scheduled task result notification suppressed",
			zap.String("task_id", task.ID),
			zap.String("task_name", task.Name),
			zap.String("tool_name", task.ToolName),
			zap.String("reason", decision.Reason))
		return nil
	}
	if err := s.notify(ctx, task, decision.Content); err != nil {
		return err
	}
	logs.L().Ctx(ctx).Info("Scheduled task result notification sent",
		zap.String("task_id", task.ID),
		zap.String("task_name", task.Name),
		zap.String("tool_name", task.ToolName),
		zap.String("reason", decision.Reason),
		zap.Int("content_len", len(decision.Content)))
	return nil
}

func (s *Scheduler) notify(ctx context.Context, task *model.ScheduledTask, content string) (err error) {
	ctx, span := otel.StartNamed(ctx, "schedule.notify")
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	taskID := ""
	chatID := ""
	if task != nil {
		taskID = task.ID
		chatID = task.ChatID
	}
	span.SetAttributes(
		attribute.String("schedule.task_id", taskID),
		attribute.String("schedule.chat_id", chatID),
		attribute.Int("schedule.content.len", len(content)),
		attribute.String("schedule.content.preview", otel.PreviewString(content, 128)),
	)
	if s == nil || s.notifier == nil {
		err = errors.New("scheduled task notifier is not configured")
	} else {
		err = s.notifier.Notify(ctx, task, content)
	}
	if err != nil {
		logs.L().Ctx(ctx).Error("Send scheduled task notification failed",
			zap.Error(err),
			zap.String("task_id", taskID),
			zap.String("chat_id", chatID))
	}
	return err
}

func StartScheduler() {
	identity := botidentity.Current()
	service := GetService()
	if !service.Available() {
		logs.L().Warn("Scheduled task service not initialized, scheduler not started")
		return
	}

	key := serviceRegistryKey(identity)
	scheduler := NewScheduler(service)

	globalSchedulersMu.Lock()
	prev := globalSchedulers[key]
	globalSchedulers[key] = scheduler
	globalSchedulersMu.Unlock()

	if prev != nil {
		prev.Stop()
	}
	scheduler.Start()
}

func StopScheduler() {
	key := serviceRegistryKey(botidentity.Current())
	globalSchedulersMu.Lock()
	scheduler := globalSchedulers[key]
	delete(globalSchedulers, key)
	globalSchedulersMu.Unlock()
	if scheduler != nil {
		scheduler.Stop()
	}
}
