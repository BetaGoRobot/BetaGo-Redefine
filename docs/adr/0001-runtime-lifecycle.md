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

## Consequences
- critical dependency fail-fast
- optional dependency degrade-and-continue
- 健康状态可以统一输出
- 后续新模块必须挂到 `App` 生命周期，不允许再新增匿名后台 goroutine 作为默认模式
