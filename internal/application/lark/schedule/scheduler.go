package schedule

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mention"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

type Scheduler struct {
	service   TaskService
	executor  taskSubmitter
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
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		service:  service,
		executor: executor,
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
		logs.L().Ctx(ctx).Error("Scheduled task execution failed",
			zap.Error(err),
			zap.String("task_id", task.ID),
			zap.String("task_name", task.Name),
			zap.String("tool_name", task.ToolName))
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
		s.notify(ctx, task, fmt.Sprintf("⚠️ 定时任务执行失败\n\n任务: %s\n工具: %s\n错误: %s", task.Name, task.ToolName, err.Error()))
		return
	}
	if err == nil && task.NotifyResult && result != "" {
		logs.L().Ctx(ctx).Info("Scheduled task execution result",
			zap.String("task_id", task.ID),
			zap.String("task_name", task.Name),
			zap.String("tool_name", task.ToolName),
			zap.String("result", result))
	}
}

func (s *Scheduler) notify(ctx context.Context, task *model.ScheduledTask, content string) {
	chatID := ""
	taskID := ""
	sourceMessageID := ""
	if task != nil {
		chatID = task.ChatID
		taskID = task.ID
		sourceMessageID = task.SourceMessageID
	}
	if chatID == "" || content == "" {
		return
	}
	ctx, span := otel.StartNamed(ctx, "schedule.notify")
	defer span.End()
	var err error
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(
		attribute.String("schedule.task_id", taskID),
		attribute.String("schedule.chat_id", chatID),
		attribute.Int("schedule.content.len", len(content)),
		attribute.String("schedule.content.preview", otel.PreviewString(content, 128)),
	)
	if normalized, normalizeErr := mention.NormalizeOutgoingText(ctx, chatID, content); normalizeErr == nil {
		content = normalized
	}
	notifyID := fmt.Sprintf("schedule-notify-%s-%d", taskID, time.Now().UnixNano())
	if sourceMessageID != "" {
		if _, err = larkmsg.ReplyMsgText(ctx, content, sourceMessageID, "_scheduleNotify", false); err == nil {
			return
		}
	}
	err = larkmsg.CreateMsgTextRaw(ctx, larkmsg.NewTextMsgBuilder().Text(content).Build(), notifyID, chatID)
	if err != nil {
		logs.L().Ctx(ctx).Error("Send scheduled task notification failed",
			zap.Error(err),
			zap.String("chat_id", chatID))
	}
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
