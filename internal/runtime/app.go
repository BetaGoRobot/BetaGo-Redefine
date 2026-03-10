package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// App 是进程内的运行时容器，负责统一管理模块顺序、启动/回滚策略、
// 逆序关闭流程以及共享健康注册表。
type App struct {
	mu       sync.Mutex
	registry *Registry
	modules  []Module
	started  []Module
	running  bool
}

// NewApp 创建一个空的运行时容器，并按给定顺序预注册模块。
func NewApp(modules ...Module) *App {
	app := &App{
		registry: NewRegistry(),
		modules:  make([]Module, 0, len(modules)),
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

		moduleName := module.Name()
		stats := moduleStats(module)

		a.registry.Update(moduleName, StateInitializing, "", stats)
		if err = module.Init(ctx); err != nil {
			span.AddEvent("module.init.failed", trace.WithAttributes(attribute.String("module.name", moduleName)))
			if errors.Is(err, ErrDisabled) {
				a.registry.Update(moduleName, StateDisabled, err.Error(), stats)
				err = nil
				continue
			}
			if stopErr := a.handleStartError(ctx, module, StateFailed, "init", err, started); stopErr != nil {
				return errors.Join(err, stopErr)
			}
			if !module.Critical() {
				err = nil
				continue
			}
			return err
		}

		a.registry.Update(moduleName, StateStarting, "", moduleStats(module))
		if err = module.Start(ctx); err != nil {
			span.AddEvent("module.start.failed", trace.WithAttributes(attribute.String("module.name", moduleName)))
			if errors.Is(err, ErrDisabled) {
				a.registry.Update(moduleName, StateDisabled, err.Error(), moduleStats(module))
				err = nil
				continue
			}
			if stopErr := a.handleStartError(ctx, module, StateFailed, "start", err, started); stopErr != nil {
				return errors.Join(err, stopErr)
			}
			if !module.Critical() {
				err = nil
				continue
			}
			return err
		}
		started = append(started, module)

		if err = module.Ready(ctx); err != nil {
			span.AddEvent("module.ready.failed", trace.WithAttributes(attribute.String("module.name", moduleName)))
			if errors.Is(err, ErrDisabled) {
				a.registry.Update(moduleName, StateDisabled, err.Error(), moduleStats(module))
				err = nil
				continue
			}
			if stopErr := a.handleStartError(ctx, module, StateDegraded, "ready", err, started); stopErr != nil {
				return errors.Join(err, stopErr)
			}
			if !module.Critical() {
				err = nil
				continue
			}
			return err
		}

		span.AddEvent("module.ready", trace.WithAttributes(attribute.String("module.name", moduleName)))
		a.registry.Update(moduleName, StateReady, "", moduleStats(module))
	}

	a.mu.Lock()
	a.started = started
	a.running = true
	a.mu.Unlock()
	a.registry.SetLive(true)
	return nil
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

	for idx := len(started) - 1; idx >= 0; idx-- {
		module := started[idx]
		if module == nil {
			continue
		}
		err := module.Stop(ctx)
		if err != nil {
			span.AddEvent("module.stop.failed", trace.WithAttributes(attribute.String("module.name", module.Name())))
			stopErr = errors.Join(stopErr, fmt.Errorf("%s stop: %w", module.Name(), err))
			a.registry.Update(module.Name(), StateFailed, err.Error(), moduleStats(module))
			continue
		}
		span.AddEvent("module.stopped", trace.WithAttributes(attribute.String("module.name", module.Name())))
		a.registry.Update(module.Name(), StateStopped, "", moduleStats(module))
	}
	return stopErr
}

// handleStartError 统一处理启动阶段错误，并按照 critical / optional
// 语义决定是回滚退出，还是降级继续。
func (a *App) handleStartError(ctx context.Context, module Module, failureState State, stage string, err error, started []Module) error {
	if module == nil {
		return err
	}
	message := strings.TrimSpace(stage + ": " + err.Error())
	if module.Critical() {
		a.registry.Update(module.Name(), failureState, message, moduleStats(module))
		return a.stopStarted(ctx, started)
	}
	a.registry.Update(module.Name(), StateDegraded, message, moduleStats(module))
	return nil
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
		err := module.Stop(ctx)
		if err != nil {
			stopErr = errors.Join(stopErr, fmt.Errorf("%s stop: %w", module.Name(), err))
		}
	}
	return stopErr
}

// moduleStats 用来读取模块的可选观测数据，不强制所有模块都实现
// StatsProvider。
func moduleStats(module Module) map[string]any {
	if provider, ok := module.(StatsProvider); ok {
		return provider.Stats()
	}
	return nil
}
