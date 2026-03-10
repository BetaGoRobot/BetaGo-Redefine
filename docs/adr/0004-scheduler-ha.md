# ADR 0004: Scheduler HA Model

## Status
Accepted

## Decision
- 当前保持单进程 ticker 模型
- 任务执行必须先 `claim` 再执行
- 调度执行通过 bounded executor 控制并发
- 后续多实例演进仍以 DB lease/claim 单活语义为基础，不引入双执行容忍

## Consequences
- 本轮解决的是“单实例下可观测、可限流、可停”
- 下轮若扩容到多实例，优先补 scheduler lease state 和跨实例可观测
