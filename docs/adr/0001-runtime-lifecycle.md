# ADR 0001: Runtime Lifecycle

## Status
Accepted

## Decision
引入 `internal/runtime`，统一定义：
- `Module`
- `App`
- `Registry`
- `Executor`

主程序不再直接做裸 `Init()` 链和 `select {}`，而是由 `App.Start()` 负责启动顺序，`App.Stop()` 负责逆序停止。

### 2026-09-08: Startup Failure Resource Ownership

- 普通 Init/Start 失败的模块也由 App 调用 Stop，包含部分初始化得到的资源。Stop 必须支持部分初始化、已自行回滚的状态，并遵守调用方的截止时间。
- Init 返回 ErrDisabled 必须不持有资源；Start/Ready 返回 ErrDisabled 时立即清理。清理失败不能伪装成成功禁用，按 critical/optional 策略报告。
- critical 失败先清理当前模块，再逆序回滚此前运行模块。optional Init/Start 失败清理后降级并继续；optional Ready 失败仍保留已运行能力，在正常退出时停止。
- 一次启动失败的清理与回滚共用 `context.WithoutCancel` 派生的超时上下文，保留 trace 等上下文值。`AppOptions.CleanupTimeout` 可注入，默认 30 秒；这是协作式截止时间，不会强制中断不响应 context 的 Stop。
- 原始启动错误与清理错误聚合返回；optional 错误保留在健康状态和实例日志中。回滚成功的此前模块标记 stopped，清理失败标记 failed，当前失败模块保留失败阶段和原因。
- 日志通过 `AppOptions.Logger` 注入，默认使用标准 logger；测试不再重赋值包级日志函数。

装配仍位于 `cmd/larkrobot`，分别组织组件构造、基础设施、搜索、应用和评估；scheduler 和搜索 provisioner 工厂归属装配实例。模块注册顺序、关键性与业务默认值保持兼容。

## Consequences
- critical dependency fail-fast
- optional dependency degrade-and-continue
- 健康状态可以统一输出
- 后续新模块必须挂到 `App` 生命周期，不允许再新增匿名后台 goroutine 作为默认模式
