package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// AppOptions controls startup-failure cleanup independently of the startup
// context. CleanupTimeout is a shared budget for the current failure and its
// rollback; modules must honor the deadline passed to Stop.
type AppOptions struct {
	CleanupTimeout time.Duration
	Logger         *log.Logger
}

// App 是进程内的运行时容器，负责统一管理模块顺序、启动/回滚策略、
// 逆序关闭流程以及共享健康注册表。
type App struct {
	mu             sync.Mutex
	registry       *Registry
	modules        []Module
	started        []Module
	running        bool
	cleanupTimeout time.Duration
	logger         *log.Logger
}

// NewApp 创建一个空的运行时容器，并按给定顺序预注册模块。
func NewApp(modules ...Module) *App {
	return NewAppWithOptions(AppOptions{}, modules...)
}

// NewAppWithOptions adds instance-scoped cleanup and logging configuration.
// Non-positive timeouts use 30 seconds; a nil logger uses log.Default().
func NewAppWithOptions(options AppOptions, modules ...Module) *App {
	if options.CleanupTimeout <= 0 {
		options.CleanupTimeout = 30 * time.Second
	}
	if options.Logger == nil {
		options.Logger = log.Default()
	}
	app := &App{
		registry:       NewRegistry(),
		modules:        make([]Module, 0, len(modules)),
		cleanupTimeout: options.CleanupTimeout,
		logger:         options.Logger,
	}
	for _, module := range modules {
		app.AddModule(module)
	}
	return app
}

// AddModule 把模块追加到启动列表中，并立即写入健康注册表。这样即使
// 还没调用 Start，状态面里也能先看到完整拓扑。
func (a *App) AddModule(module Module) {
	if a == nil || module == nil {
		return
	}
	a.modules = append(a.modules, module)
	a.registry.Register(module.Name(), module.Critical())
	if provider, ok := module.(StatsProvider); ok {
		a.registry.RegisterProvider(module.Name(), provider)
	}
	if provider, ok := module.(DynamicHealthProvider); ok {
		a.registry.RegisterDynamicHealth(module.Name(), provider)
	}
}

// ModuleNames 返回模块的注册顺序，供启动拓扑诊断和测试使用。
// 返回值不复用 App 的内部切片，调用方可以安全修改。
func (a *App) ModuleNames() []string {
	if a == nil {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	names := make([]string, 0, len(a.modules))
	for _, module := range a.modules {
		if module != nil {
			names = append(names, module.Name())
		}
	}
	return names
}

// Registry 返回管理面使用的共享健康注册表。
func (a *App) Registry() *Registry {
	if a == nil {
		return nil
	}
	return a.registry
}

// Start 按注册顺序依次启动所有模块。
//
// 这里的策略刻意保持严格：
// - 每个模块都必须完整经过 Init -> Start -> Ready；
// - critical 模块失败时，要先回滚已启动模块，再返回错误；
// - optional 模块失败时，只记录 degraded，启动流程继续；
// - disabled 是显式状态，不算异常。
func (a *App) Start(ctx context.Context) (err error) {
	if a == nil {
		return errors.New("app is nil")
	}

	ctx, span := otel.StartNamed(ctx, "runtime.app.start")
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(attribute.Int("modules.count", len(a.modules)))

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	started := make([]Module, 0, len(a.modules))
	for _, module := range a.modules {
		if module == nil {
			continue
		}

		stage, startErr := a.startModule(ctx, module)
		if startErr != nil {
			span.AddEvent("module."+stage+".failed", trace.WithAttributes(attribute.String("module.name", module.Name())))
			keepRunning, failureErr := a.handleStartError(ctx, module, stage, startErr, started)
			if failureErr != nil {
				return failureErr
			}
			if keepRunning {
				started = append(started, module)
			}
			continue
		}

		started = append(started, module)
		span.AddEvent("module.ready", trace.WithAttributes(attribute.String("module.name", module.Name())))
		a.registry.Update(module.Name(), StateReady, "", moduleStats(module))
	}

	a.mu.Lock()
	a.started = started
	a.running = true
	a.mu.Unlock()
	a.registry.SetLive(true)
	return nil
}

func (a *App) startModule(ctx context.Context, module Module) (string, error) {
	a.registry.Update(module.Name(), StateInitializing, "", moduleStats(module))
	if err := module.Init(ctx); err != nil {
		return "init", err
	}
	a.registry.Update(module.Name(), StateStarting, "", moduleStats(module))
	if err := module.Start(ctx); err != nil {
		return "start", err
	}
	return "ready", module.Ready(ctx)
}

// Stop 按启动逆序关闭模块，对齐依赖的反向释放顺序。
// 这么做的原因是：上层服务通常依赖于更早启动的下层模块，必须先停上层，
// 再停底层连接和客户端。
func (a *App) Stop(ctx context.Context) (stopErr error) {
	if a == nil {
		return nil
	}

	ctx, span := otel.StartNamed(ctx, "runtime.app.stop")
	defer span.End()
	defer otel.RecordErrorPtr(span, &stopErr)

	a.mu.Lock()
	started := append([]Module(nil), a.started...)
	a.started = nil
	a.running = false
	a.mu.Unlock()
	a.registry.SetLive(false)

	return a.stopStarted(ctx, started)
}

// handleStartError owns the failed module separately from previously started
// modules, including resources allocated before Start succeeded. keepRunning is
// true only for an optional module whose readiness check failed.
func (a *App) handleStartError(ctx context.Context, module Module, stage string, err error, started []Module) (keepRunning bool, failureErr error) {
	disabled := errors.Is(err, ErrDisabled)
	if disabled && stage == "init" {
		a.registry.Update(module.Name(), StateDisabled, err.Error(), moduleStats(module))
		return false, nil
	}
	if !disabled && stage == "ready" && !module.Critical() {
		a.recordStartFailure(module, stage, err)
		return true, nil
	}

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cleanupTimeout)
	defer cancel()
	cleanupErr := a.stopModule(cleanupCtx, module)
	if disabled && cleanupErr == nil {
		a.registry.Update(module.Name(), StateDisabled, err.Error(), moduleStats(module))
		return false, nil
	}
	failure := errors.Join(fmt.Errorf("%s %s: %w", module.Name(), stage, err), cleanupErr)
	a.recordStartFailure(module, stage, failure)
	if !module.Critical() {
		return false, nil
	}
	return false, errors.Join(failure, a.stopStarted(cleanupCtx, started))
}

func (a *App) recordStartFailure(module Module, stage string, err error) {
	state := StateDegraded
	if module.Critical() {
		state = StateFailed
	}
	a.registry.Update(module.Name(), state, stage+": "+err.Error(), moduleStats(module))
	if !module.Critical() {
		a.logger.Printf("[ERROR] optional module degraded: module=%s stage=%s error=%v", module.Name(), stage, err)
	}
}

// stopStarted 是 critical 模块启动失败时的回滚路径，用来清理前面已经
// 成功启动的模块，避免进程停留在半启动状态。
func (a *App) stopStarted(ctx context.Context, started []Module) error {
	var stopErr error
	for idx := len(started) - 1; idx >= 0; idx-- {
		module := started[idx]
		if module == nil {
			continue
		}
		stopErr = errors.Join(stopErr, a.stopModule(ctx, module))
	}
	return stopErr
}

func (a *App) stopModule(ctx context.Context, module Module) error {
	span := trace.SpanFromContext(ctx)
	if err := module.Stop(ctx); err != nil {
		span.AddEvent("module.stop.failed", trace.WithAttributes(attribute.String("module.name", module.Name())))
		a.registry.Update(module.Name(), StateFailed, err.Error(), moduleStats(module))
		return fmt.Errorf("%s stop: %w", module.Name(), err)
	}
	span.AddEvent("module.stopped", trace.WithAttributes(attribute.String("module.name", module.Name())))
	a.registry.Update(module.Name(), StateStopped, "", moduleStats(module))
	return nil
}

// moduleStats 用来读取模块的可选观测数据，不强制所有模块都实现
// StatsProvider。
func moduleStats(module Module) map[string]any {
	if provider, ok := module.(StatsProvider); ok {
		return provider.Stats()
	}
	return nil
}
