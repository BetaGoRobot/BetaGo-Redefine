package runtime

import (
	"context"
	"errors"
	"fmt"
)

// ErrDisabled 表示模块在当前配置或运行条件下被显式关闭。
// App 会把它当成一种可预期状态，而不是启动失败：组件会被标记为
// disabled，后续启动流程继续执行。
var ErrDisabled = errors.New("module disabled")

// Module 是所有运行时托管组件统一遵循的生命周期契约。
// 一个模块可以是外部基础设施连接、进程内工作池，也可以是对旧包级
// 初始化逻辑的薄适配层。
//
// 各阶段职责刻意拆开：
// - Init：构造本地状态、校验配置、分配客户端；
// - Start：真正启动后台任务、监听器或轮询器；
// - Ready：判断模块是否已经具备对外提供能力的条件；
// - Stop：按逆序释放资源。
type Module interface {
	Name() string
	Critical() bool
	Init(context.Context) error
	Start(context.Context) error
	Ready(context.Context) error
	Stop(context.Context) error
}

// StatsProvider 是一个可选扩展，用来给健康检查和状态面暴露模块的
// 运行时细节，例如队列深度、最近错误或已知限制。
type StatsProvider interface {
	Stats() map[string]any
}

// FuncModule 是这轮改造里最轻量的适配器，用闭包把现有包裹进 Module
// 契约，而不要求旧代码立刻重写成完整结构体。
type FuncModule struct {
	name     string
	critical bool
	initFn   func(context.Context) error
	startFn  func(context.Context) error
	readyFn  func(context.Context) error
	stopFn   func(context.Context) error
	statsFn  func() map[string]any
}

// FuncModuleOptions 与生命周期阶段一一对应，调用方只需要实现自己用到
// 的钩子。
type FuncModuleOptions struct {
	Name     string
	Critical bool
	Init     func(context.Context) error
	Start    func(context.Context) error
	Ready    func(context.Context) error
	Stop     func(context.Context) error
	Stats    func() map[string]any
}

// NewFuncModule 用纯回调的方式构造一个模块。它是旧式包级初始化向新
// 运行时装配顺序迁移时的桥接层。
func NewFuncModule(options FuncModuleOptions) *FuncModule {
	return &FuncModule{
		name:     options.Name,
		critical: options.Critical,
		initFn:   options.Init,
		startFn:  options.Start,
		readyFn:  options.Ready,
		stopFn:   options.Stop,
		statsFn:  options.Stats,
	}
}

// Name 返回模块在健康注册表和启动错误中的稳定名称。
func (m *FuncModule) Name() string {
	if m == nil {
		return ""
	}
	return m.name
}

// Critical 表示该模块失败时是否必须中止整个进程启动。
func (m *FuncModule) Critical() bool {
	return m != nil && m.critical
}

// Init 执行可选的初始化钩子。
func (m *FuncModule) Init(ctx context.Context) error {
	if m == nil || m.initFn == nil {
		return nil
	}
	return m.initFn(ctx)
}

// Start 执行可选的启动钩子。
func (m *FuncModule) Start(ctx context.Context) error {
	if m == nil || m.startFn == nil {
		return nil
	}
	return m.startFn(ctx)
}

// Ready 执行可选的就绪检查钩子。
func (m *FuncModule) Ready(ctx context.Context) error {
	if m == nil || m.readyFn == nil {
		return nil
	}
	return m.readyFn(ctx)
}

// Stop 执行可选的停止钩子。
func (m *FuncModule) Stop(ctx context.Context) error {
	if m == nil || m.stopFn == nil {
		return nil
	}
	return m.stopFn(ctx)
}

// Stats 返回可选的结构化状态数据，供 health/status 接口输出。
func (m *FuncModule) Stats() map[string]any {
	if m == nil || m.statsFn == nil {
		return nil
	}
	return m.statsFn()
}

// RecoverError 把旧式 panic 初始化转换成普通 error，让“是否中止启动”
// 这个决策始终留在 App 里，而不是被 panic 直接决定。
func RecoverError(name string, fn func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s panic: %v", name, recovered)
		}
	}()
	fn()
	return nil
}
