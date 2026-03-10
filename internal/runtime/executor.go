package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	// ErrExecutorNotStarted 表示调用方在工作池启动前就尝试投递任务。
	ErrExecutorNotStarted = errors.New("executor not started")
	// ErrExecutorClosed 表示工作池正在关闭，或已经不再接受新任务。
	ErrExecutorClosed = errors.New("executor closed")
	// ErrExecutorQueueFull 是显式背压信号，表示调用方产出任务的速度已经
	// 超过当前配置允许的预算。
	ErrExecutorQueueFull = errors.New("executor queue full")
)

// TaskFunc 是受控后台工作池执行的最小任务单元。
type TaskFunc func(context.Context) error

// ExecutorConfig 定义某一类后台工作的并发预算和超时时间。
type ExecutorConfig struct {
	Name        string
	Workers     int
	QueueSize   int
	TaskTimeout time.Duration
}

// task 是放进执行器缓冲队列的内部任务对象。
type task struct {
	ctx  context.Context
	name string
	fn   TaskFunc
}

// Executor 是运行时托管的工作池，用来替代消息入口、chunk 聚合、调度执行
// 这类热点路径上无边界的 goroutine 扇出。
//
// 这里的设计取舍是：
// - 用有界队列提供背压，而不是无限制增长 goroutine；
// - 用固定 worker 数量控制 CPU / IO 压力；
// - 在执行器边界统一施加每任务超时；
// - 通过 Stats() 暴露实时计数器，接入健康面和状态面。
type Executor struct {
	name        string
	workers     int
	queueSize   int
	taskTimeout time.Duration

	queue   chan task
	wg      sync.WaitGroup
	mu      sync.RWMutex
	started bool
	closed  bool

	running   atomic.Int64
	completed atomic.Int64
	failed    atomic.Int64
	rejected  atomic.Int64

	lastError atomic.Pointer[string]
}

// NewExecutor 把不完整配置归一化成一个安全的工作池形态。
func NewExecutor(cfg ExecutorConfig) *Executor {
	workers := cfg.Workers
	if workers <= 0 {
		workers = 1
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = workers * 16
	}
	return &Executor{
		name:        cfg.Name,
		workers:     workers,
		queueSize:   queueSize,
		taskTimeout: cfg.TaskTimeout,
		queue:       make(chan task, queueSize),
	}
}

// Name 返回执行器在运行时注册表中的组件名。
func (e *Executor) Name() string {
	if e == nil {
		return ""
	}
	return e.name
}

// Critical 返回 true，因为一旦某类工作已经被设计为必须经过执行器，再
// 静默失去这个执行器，就会破坏该链路的正确性和可观测性假设。
func (e *Executor) Critical() bool {
	return true
}

// Init 目前只是为了满足 Module 接口，本身不做额外工作。
func (e *Executor) Init(context.Context) error {
	return nil
}

// Start 启动配置好的 worker goroutine。
func (e *Executor) Start(context.Context) error {
	if e == nil {
		return errors.New("executor is nil")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return nil
	}
	if e.closed {
		return ErrExecutorClosed
	}
	for idx := 0; idx < e.workers; idx++ {
		e.wg.Add(1)
		go e.worker()
	}
	e.started = true
	return nil
}

// Ready 判断执行器当前是否还能接收任务。
func (e *Executor) Ready(context.Context) error {
	if e == nil {
		return errors.New("executor is nil")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.started {
		return ErrExecutorNotStarted
	}
	if e.closed {
		return ErrExecutorClosed
	}
	return nil
}

// Stop 关闭任务队列，并在给定关闭上下文限制内等待 worker 把已接收任务
// 尽量处理完。
func (e *Executor) Stop(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	if !e.started || e.closed {
		e.closed = true
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	close(e.queue)
	e.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.wg.Wait()
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Submit 以“快速失败”的方式投递任务，不会让调用方无限阻塞。
//
// 队列满时直接返回 ErrExecutorQueueFull，让调用方显式决定记录日志、
// 重试还是丢弃，而不是偷偷再起一个 goroutine。
func (e *Executor) Submit(ctx context.Context, name string, fn func(context.Context) error) error {
	if e == nil {
		return errors.New("executor is nil")
	}
	if fn == nil {
		return errors.New("task func is nil")
	}
	e.mu.RLock()
	started := e.started
	closed := e.closed
	e.mu.RUnlock()
	if !started {
		return ErrExecutorNotStarted
	}
	if closed {
		return ErrExecutorClosed
	}

	taskCtx := context.WithoutCancel(ctx)
	select {
	case e.queue <- task{ctx: taskCtx, name: name, fn: TaskFunc(fn)}:
		return nil
	default:
		e.rejected.Add(1)
		err := fmt.Errorf("%w: %s", ErrExecutorQueueFull, e.name)
		e.setLastError(err.Error())
		trace.SpanFromContext(ctx).AddEvent("executor.submit.rejected", trace.WithAttributes(
			attribute.String("executor.name", e.name),
			attribute.String("task.name", name),
		))
		otel.RecordError(trace.SpanFromContext(ctx), err)
		return err
	}
}

// Stats 输出运行时计数器，这些数据会被共享健康注册表和管理面 HTTP 直接
// 暴露出来。
func (e *Executor) Stats() map[string]any {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	started := e.started
	closed := e.closed
	e.mu.RUnlock()

	stats := map[string]any{
		"workers":      e.workers,
		"queue_size":   e.queueSize,
		"queue_depth":  len(e.queue),
		"running":      e.running.Load(),
		"completed":    e.completed.Load(),
		"failed":       e.failed.Load(),
		"rejected":     e.rejected.Load(),
		"started":      started,
		"closed":       closed,
		"task_timeout": e.taskTimeout.String(),
	}
	if lastError := e.getLastError(); lastError != "" {
		stats["last_error"] = lastError
	}
	return stats
}

// worker 是单个 goroutine 对应的常驻消费循环。
func (e *Executor) worker() {
	defer e.wg.Done()
	for task := range e.queue {
		e.running.Add(1)
		err := e.runTask(task)
		if err != nil {
			e.failed.Add(1)
			e.setLastError(err.Error())
		} else {
			e.completed.Add(1)
		}
		e.running.Add(-1)
	}
}

// runTask 在真正执行任务前套上一层执行器级别的统一超时约束。
func (e *Executor) runTask(task task) error {
	ctx := task.ctx
	if e.taskTimeout > 0 {
		timeoutCtx, _ := context.WithTimeout(ctx, e.taskTimeout)
		// TODO: 这里不能主动取消，因为超时会自动触发上下文取消，下游其实有异步处理依赖。除非保证下游的所有异步都直接withoutCancel。
		ctx = timeoutCtx
	}
	ctx, span := otel.StartNamed(ctx, "runtime.executor.run")
	defer span.End()
	defer func() {
		if spanErr := recover(); spanErr != nil {
			panic(spanErr)
		}
	}()
	span.SetAttributes(
		attribute.String("executor.name", e.name),
		attribute.String("task.name", task.name),
		attribute.Int("executor.workers", e.workers),
		attribute.Int("executor.queue_size", e.queueSize),
		attribute.Int64("task.timeout_ms", e.taskTimeout.Milliseconds()),
	)

	err := task.fn(ctx)
	otel.RecordError(span, err)
	return err
}

// setLastError 保存最近一次执行器错误，供 health/status 输出。
func (e *Executor) setLastError(message string) {
	if message == "" {
		return
	}
	msg := message
	e.lastError.Store(&msg)
}

// getLastError 返回最近一次记录的执行器错误。
func (e *Executor) getLastError() string {
	value := e.lastError.Load()
	if value == nil {
		return ""
	}
	return *value
}
