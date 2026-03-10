# BetaGo_v2 Runtime Governance

Current implementation design, including Mermaid diagrams for the refactored
runtime, is documented in `docs/architecture/runtime-refactor-design.md`.
Next-step refactor roadmap is documented in `docs/architecture/runtime-refactor-plan.md`.

## Runtime Topology
`cmd/larkrobot` 现在通过 `internal/runtime.App` 统一编排启动和关闭：

1. 加载 `config.toml`
2. 初始化 critical modules：`logging`、`db`、`redis`
3. 初始化 optional modules：`otel`、`opensearch`、`ark_runtime`、`minio`、`gotify`、`aktool`、`xhttp`、`netease_music`、`retriever`
4. 启动受控后台执行器：`message/reaction/recording/chunk/schedule`
5. 初始化应用服务：`todo`、`schedule`、Lark handler wiring、recording submitter、chunk executor
6. 启动 chunking cleaner、management HTTP、scheduler、Lark WS client

消息主链路：
- Lark WS event
- `internal/interfaces/lark.HandlerSet`
- bounded ingress executor
- `messages.Handler` / `reaction.Handler`
- recording executor / chunk executor / scheduler executor
- DAL + external dependencies

## Dependency Inventory
| Module | Critical | Startup Behavior | Ready Signal | Stop Behavior |
| --- | --- | --- | --- | --- |
| `db` | yes | panic-safe init | DB ping | close SQL pool |
| `redis` | yes | explicit ping | Redis ping | close client |
| `logging` | yes | direct init | implicit | none |
| `otel` | no | best-effort init | implicit | none |
| `opensearch` | no | noop fallback allowed | `Status()` | none |
| `ark_runtime` | no | noop fallback allowed | `Status()` | none |
| `minio` | no | noop fallback allowed | `Status()` | none |
| `gotify` | no | noop fallback allowed | `ErrUnavailable()==nil` | none |
| `aktool` | no | noop fallback allowed | `Status()` | none |
| `retriever` | no | noop fallback allowed | `Status()` | none |
| `management_http` | no | disabled if addr empty | listener exists | graceful HTTP shutdown |
| `scheduler` | no | disabled if service unavailable | start success | stop ticker + wait |
| `lark_ws` | yes | connect or fail fast | no immediate start error | upstream library has no graceful stop |

## Shared State Inventory
包级共享状态仍然存在，但已被收敛到明确的治理清单：

- `messages.Handler` / `reaction.Handler`
- `appconfig.GetManager()`
- `db.globalDB`
- `redis_dal.RedisClient`
- `larkchunking.M`
- `scheduleapp` service registry
- package-level noop/live backends in `opensearch` / `retriever` / `gotify` / `aktool` / `miniodal` / `ark_dal`

新增运行时统一面：
- `internal/runtime.App`
- `internal/runtime.Registry`
- `internal/runtime.Executor`
- `internal/interfaces/lark.HandlerSet`

## Semantic Matrix
| Concern | Standard |
| --- | --- |
| User identity | `OpenID` 优先，缺失时才 fallback |
| Bot identity | `AppID + BotOpenID` |
| Dynamic config | 业务运行时只能走 config manager / accessor |
| Background work | 默认通过 bounded executor 提交，不再直接裸 `go func` |
| Health state | `ready / degraded / failed / disabled / stopped` |
| Startup failures | critical fail-fast，optional degrade-and-continue |

## Risk Register
### P0
- Lark WS upstream client无 `Stop()`，`Start()` 内部永久阻塞，进程级优雅退出仍受上游库限制。
  - 证据：`internal/runtime/lark_ws.go` 包装只能 best-effort；上游 `ws.Client.Start()` 无 graceful shutdown API。
  - 后果：停止流程仍有已知 goroutine 残留风险，依赖进程退出清理。

### P1
- `xhandler.RunParallelStages()` 仍按“每条消息一个 fetcher/operator goroutine”执行。
  - 证据：`pkg/xhandler/base.go`
  - 后果：入口背压已收敛，但单条消息内部 fanout 仍未纳入统一并发预算。

### P1
- 仍有少量 helper 级异步调用未接入 executor。
  - 证据：卡片动作审计、部分消息发送/反应 helper
  - 后果：观测和背压仍不完全统一。

### P2
- 部分 optional dependency 只有启动时静态健康判断，没有持续探测。
  - 证据：`opensearch/retriever/gotify/aktool/minio/ark`
  - 后果：运行中途降级需要依赖日志和业务错误暴露。

## Acceptance Checklist
- 启动从 `select {}` 迁移到 `App.Start + signal.NotifyContext + App.Stop`
- 有 `/livez`、`/readyz`、`/healthz`、`/statusz`
- ingress、recording、chunk merge、schedule execution 有显式 worker/queue/timeout
- critical module 启动失败立即中止
- optional module 启动失败进入 degraded/disabled
- 仓库内新增 ADR，记录生命周期、身份、配置、调度和降级语义
