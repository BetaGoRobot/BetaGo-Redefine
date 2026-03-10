# ADR 0005: Health And Degradation

## Status
Accepted

## Decision
模块状态统一使用：
- `ready`
- `degraded`
- `failed`
- `disabled`
- `stopped`

并通过 management HTTP 输出：
- `/livez`
- `/readyz`
- `/healthz`
- `/statusz`

## Consequences
- optional dependency 允许 degraded
- critical dependency 不允许 silent noop
- 新模块需要明确自己的 `ready` 和 `stop` 语义
