package schedule

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

type Scheduler struct {
	service   TaskService
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	running   bool
	checkTick *time.Ticker
}

var globalScheduler *Scheduler

func NewScheduler(service TaskService) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		service: service,
		ctx:     ctx,
		cancel:  cancel,
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tasks, err := s.service.GetDueTasks(ctx, 100)
	if err != nil {
		logs.L().Ctx(ctx).Error("Get due scheduled tasks failed", zap.Error(err))
		return
	}
	if len(tasks) == 0 {
		return
	}

	now := time.Now()
	logs.L().Ctx(ctx).Info("Found due scheduled tasks", zap.Int("count", len(tasks)))
	for _, task := range tasks {
		claimed, err := s.service.ClaimTaskExecution(ctx, task, now)
		if err != nil {
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
		go s.executeTask(task)
	}
}

func (s *Scheduler) executeTask(task *model.ScheduledTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	logs.L().Ctx(ctx).Info("Executing scheduled task",
		zap.String("task_id", task.ID),
		zap.String("task_name", task.Name),
		zap.String("tool_name", task.ToolName))

	result, err := s.service.ExecuteTask(ctx, task)
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
		logs.L().Ctx(ctx).Error("Finalize scheduled task execution failed",
			zap.Error(updateErr),
			zap.String("task_id", task.ID))
	}

	if err != nil && task.NotifyOnError {
		s.notify(ctx, task.ChatID, task.ID, fmt.Sprintf("⚠️ 定时任务执行失败\n\n任务: %s\n工具: %s\n错误: %s", task.Name, task.ToolName, err.Error()))
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

func (s *Scheduler) notify(ctx context.Context, chatID, taskID, content string) {
	if chatID == "" || content == "" {
		return
	}
	notifyID := fmt.Sprintf("schedule-notify-%s-%d", taskID, time.Now().UnixNano())
	if err := larkmsg.CreateMsgTextRaw(ctx, larkmsg.NewTextMsgBuilder().Text(content).Build(), notifyID, chatID); err != nil {
		logs.L().Ctx(ctx).Error("Send scheduled task notification failed",
			zap.Error(err),
			zap.String("chat_id", chatID))
	}
}

func StartScheduler() {
	if !GetService().Available() {
		logs.L().Warn("Scheduled task service not initialized, scheduler not started")
		return
	}
	globalScheduler = NewScheduler(GetService())
	globalScheduler.Start()
}

func StopScheduler() {
	if globalScheduler != nil {
		globalScheduler.Stop()
	}
}
