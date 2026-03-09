package todo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/domain/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

// Scheduler 提醒调度器
type Scheduler struct {
	service   *Service
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	running   bool
	mu        sync.Mutex
	checkTick *time.Ticker
}

// NewScheduler 创建调度器
func NewScheduler(service *Service) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		service: service,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start 启动调度器
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

	logs.L().Info("Todo reminder scheduler started")
}

// Stop 停止调度器
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
	logs.L().Info("Todo reminder scheduler stopped")
}

func (s *Scheduler) run() {
	defer s.wg.Done()

	// 立即执行一次检查
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 获取待触发的提醒
	reminders, err := s.service.GetPendingReminders(ctx, 100)
	if err != nil {
		logs.L().Ctx(ctx).Error("Get pending reminders failed", zap.Error(err))
		return
	}

	if len(reminders) == 0 {
		return
	}

	logs.L().Ctx(ctx).Info(fmt.Sprintf("Found %d pending reminders to trigger", len(reminders)))

	for _, rem := range reminders {
		if !rem.ShouldTrigger() {
			continue
		}

		// 触发提醒
		go func(r *todo.Reminder) {
			s.triggerReminder(context.Background(), r)
		}(rem)
	}
}

func (s *Scheduler) triggerReminder(ctx context.Context, rem *todo.Reminder) {
	logs.L().Ctx(ctx).Info("Triggering reminder",
		zap.String("id", rem.ID),
		zap.String("title", rem.Title),
		zap.String("chat_id", rem.ChatID))

	// 构建提醒消息
	msg := fmt.Sprintf("⏰ **提醒**\n\n%s", rem.Title)
	if rem.Content != "" {
		msg += fmt.Sprintf("\n\n%s", rem.Content)
	}

	// 如果有关联的待办，添加待办信息
	if rem.TodoID != "" {
		if t, err := s.service.GetTodo(ctx, rem.TodoID); err == nil {
			msg += fmt.Sprintf("\n\n关联待办: %s (ID: `%s`)", t.Title, t.ID)
		}
	}

	// 发送消息
	err := larkmsg.CreateMsgTextRaw(ctx, larkmsg.NewTextMsgBuilder().Text(msg).Build(), "", rem.ChatID)
	if err != nil {
		logs.L().Ctx(ctx).Error("Send reminder message failed",
			zap.Error(err),
			zap.String("reminder_id", rem.ID))
		return
	}

	// 标记为已触发
	if err := s.service.MarkReminderTriggered(ctx, rem.ID); err != nil {
		logs.L().Ctx(ctx).Error("Mark reminder triggered failed",
			zap.Error(err),
			zap.String("reminder_id", rem.ID))
	}

	logs.L().Ctx(ctx).Info("Reminder triggered successfully", zap.String("id", rem.ID))
}

// 全局调度器实例
var globalScheduler *Scheduler

// StartScheduler 启动全局调度器
func StartScheduler() {
	if globalService == nil {
		logs.L().Warn("Todo service not initialized, scheduler not started")
		return
	}

	globalScheduler = NewScheduler(globalService)
	globalScheduler.Start()
}

// StopScheduler 停止全局调度器
func StopScheduler() {
	if globalScheduler != nil {
		globalScheduler.Stop()
	}
}
