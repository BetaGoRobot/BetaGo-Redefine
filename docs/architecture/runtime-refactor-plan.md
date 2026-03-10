# Plan: BetaGo_v2 Runtime 下一阶段重构

**Generated**: 2026-03-11
**Estimated Complexity**: High

## Overview

这份计划基于以下文档继续推进运行时治理改造：

- `docs/adr/0001-runtime-lifecycle.md`
- `docs/adr/0002-identity-model.md`
- `docs/adr/0003-config-access.md`
- `docs/adr/0004-scheduler-ha.md`
- `docs/adr/0005-health-and-degradation.md`
- `docs/architecture/runtime-governance.md`
- `docs/architecture/runtime-refactor-design.md`

当前仓库已经完成了第一轮 runtime container、health registry、bounded executor 和 management HTTP 的收敛；下一阶段的重点不再是“把启动流程收口”，而是把剩余的并发、身份、配置和共享状态也纳入同一套治理语义。

## Success Criteria

- 所有生产路径上的后台任务都要么显式同步执行，要么显式提交到受管执行器。
- 用户身份统一以 `OpenID` 为主键，`UserId` 仅保留在兼容层，且必须可审计。
- 业务运行时配置读取统一通过 accessor / manager，不再直接依赖 `config.Get()` 的 TOML struct。
- 关键服务与可选依赖的健康状态从“启动时一次性判定”推进到“可持续观察、可解释降级”。
- 现有包级全局状态进一步收敛，`cmd/larkrobot` 的装配关系可被模块化测试验证。

## Non-Goals

- 本轮不直接实现多实例 scheduler，只做单实例到 lease/claim 模型的演进准备。
- 本轮不替换上游 Lark websocket SDK，只隔离其不可优雅停止的限制。
- 本轮不做全仓库级大规模业务重写，优先覆盖 runtime 边界和高风险链路。

## Baseline Gaps

截至 2026-03-11，现状与文档约束之间还有这些主要差距：

- `pkg/xhandler/base.go` 仍在单条消息内部直接派生 fetcher / operator goroutine，未纳入统一并发预算。
- `internal/interfaces/lark/handler.go`、`internal/application/lark/messages/handler.go`、`internal/application/lark/messages/recording/service.go`、`pkg/xchunk/chunking.go` 仍保留少量 fire-and-forget 路径。
- 仓库里仍有大量 `config.Get()` 直接读取，尤其集中在 handler、chunking、history、基础设施适配层。
- `internal/application/lark/messages/handler.go`、`internal/application/lark/messages/ops/common.go`、`internal/application/lark/reaction/follow_react.go`、`internal/application/lark/handlers/schedule_compat.go` 仍保留 `UserId` fallback 兼容逻辑。
- 包级共享状态仍存在于 `messages.Handler`、`reaction.Handler`、`appconfig.GetManager()`、`db.globalDB`、`redis.RedisClient`、`larkchunking.M`、`scheduleapp` registry。
- optional dependency 的健康状态主要靠启动时 `Ready()` 判断，缺少持续探测和状态转换原因。

## Sprint 1: 收口剩余并发预算

**Goal**: 让 runtime 的“bounded execution”从入口扩大到内部 fan-out 和 helper 异步路径，关闭当前最明显的治理缺口。  
**Demo/Validation**:

- 消息、reaction、recording、chunk merge、schedule 之外，不再出现生产路径裸 `go func()` 作为默认分支。
- 执行器队列打满时，`/healthz` 能看到拒绝计数与最近错误。
- 单条消息内部的并发预算可配置、可测试、可观测。

### Task 1.1: 给 `xhandler` 增加受控并发预算

- **Location**: `pkg/xhandler/base.go`, `cmd/larkrobot/bootstrap.go`, `internal/runtime/executor.go`
- **Description**: 为 fetcher/operator 执行阶段引入显式并发预算机制，优先选择可注入的 semaphore / stage runner，而不是每个 stage 直接 `go func()`。
- **Dependencies**: none
- **Acceptance Criteria**:
  - `RunParallelStages()` 不再无限制地为每个 fetcher/operator 派生 goroutine。
  - runtime 装配层可以给 message / reaction processor 注入预算参数。
  - 队列饱和、超时和拒绝会进入统一日志与状态面。
- **Validation**:
  - 补充 `pkg/xhandler` 的并发与依赖顺序测试。
  - 针对 budget=1、budget>1、dependency failure 三种场景做回归测试。

### Task 1.2: 清理 helper 级 fire-and-forget 异步

- **Location**: `internal/interfaces/lark/handler.go`, `internal/application/lark/messages/handler.go`, `internal/application/lark/messages/recording/service.go`, `pkg/xchunk/chunking.go`
- **Description**: 把卡片动作审计、trace 落库、recording fallback、chunk merge fallback 等残留异步统一接到现有 executor，或明确降级为同步执行。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - 生产装配路径下，handler 和消息处理链路不再依赖匿名 goroutine 兜底。
  - 允许保留测试/兼容 fallback，但必须只在显式 nil injector 或测试场景触发。
  - 每条剩余同步例外都在文档中有理由说明。
- **Validation**:
  - 扩充 `internal/interfaces/lark` 和 `internal/application/lark/messages` 的单测。
  - 用执行器 stub 验证任务名称、超时和错误传播。

### Task 1.3: 给 runtime 暴露更细的执行器治理指标

- **Location**: `internal/runtime/executor.go`, `internal/runtime/health.go`, `internal/runtime/health_http.go`
- **Description**: 在现有计数基础上补齐 timeout、last_transition、queue saturation 等运维字段，让并发治理不仅“存在”，还可被排障和告警消费。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - `/healthz` 能区分 rejected、failed、timeout、running backlog。
  - 组件状态信息含最近一次状态变化时间和原因。
- **Validation**:
  - 补充 health snapshot / HTTP 响应测试。
  - 手工验证 management HTTP 输出字段稳定。

## Sprint 2: 落实身份与配置边界

**Goal**: 把 ADR `0002` 和 `0003` 从“设计约束”推进为“代码默认路径”，减少隐式 fallback 和跨租户风险。  
**Demo/Validation**:

- 消息和 reaction 主链路默认从 typed identity 读取 `OpenID`、`AppID`、`BotOpenID`。
- 业务包不再直接读取 TOML struct 来决定功能开关和动态配置。
- 新增 guardrail 能阻止未来继续引入违规读取。

### Task 2.1: 收敛消息/反应链路的身份抽取

- **Location**: `internal/application/lark/botidentity/identity.go`, `internal/application/lark/messages/handler.go`, `internal/application/lark/messages/ops/common.go`, `internal/application/lark/reaction/follow_react.go`, `internal/application/lark/handlers/schedule_compat.go`
- **Description**: 提供统一 identity extractor / resolver，明确 `OpenID` 优先、`UserId` 仅兼容 fallback，并对 fallback 命中打点或记录日志。
- **Dependencies**: Sprint 1 complete
- **Acceptance Criteria**:
  - 主处理路径默认只消费 `OpenID`。
  - `UserId` fallback 被集中到兼容层，不再散落在多个 handler / op 中。
  - bot identity mismatch 会 fail-closed，而不是静默回退。
- **Validation**:
  - 补充 identity extractor 与 handler 测试。
  - 覆盖 `OpenID` 存在、仅 `UserId` 存在、身份缺失三类用例。

### Task 2.2: 迁移业务包到 accessor / manager

- **Location**: `internal/application/config/accessor.go`, `internal/application/lark/handlers/*.go`, `internal/application/lark/chunking/*.go`, `internal/application/lark/history/*.go`, `internal/application/lark/ratelimit/rate_limiter.go`
- **Description**: 先覆盖高频业务链路，把功能开关、概率配置、阈值配置等读取全部迁移到 accessor；`config.Get()` 只保留在基础设施初始化和 bot identity bootstrap。
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - handler / message / chunking 主路径不再直接依赖 `config.Get()`。
  - accessor 能表达 bot namespace + scope priority 语义。
  - 关键配置读取具备可注入测试替身。
- **Validation**:
  - 为 accessor 和高频 handler 增加针对 chat/user/global 优先级的测试。
  - 用 `rg "config\\.Get\\("` 生成剩余名单，确保只剩基础设施允许区。

### Task 2.3: 增加治理检查，防止回归

- **Location**: `internal/application/config`, `st/` 或现有测试脚本目录
- **Description**: 增加轻量静态检查或测试，约束业务层直接 `config.Get()`、散落的 `UserId` 主路径读取、新增裸 goroutine。
- **Dependencies**: Task 2.1, Task 2.2
- **Acceptance Criteria**:
  - 至少有一条自动化检查能覆盖本轮治理规则。
  - 规则以 allowlist 方式记录现存兼容例外，避免“一刀切”阻塞迭代。
- **Validation**:
  - 在本地测试中验证违规样例会失败。
  - 更新开发文档说明例外登记方式。

## Sprint 3: 继续收敛包级共享状态

**Goal**: 把“runtime 装配是唯一事实来源”落实到 service wiring，降低隐藏依赖和测试耦合。  
**Demo/Validation**:

- `buildApp()` 不再依赖消息处理器、配置管理器、调度器的隐式全局单例。
- 应用服务和基础设施可以通过显式依赖构造并在模块测试中独立替换。
- `application_services` 模块职责更清晰，不再承载过多 side effect。

### Task 3.1: 去掉消息与反应处理器的包级默认实例

- **Location**: `internal/application/lark/messages/handler.go`, `internal/application/lark/reaction/base.go`, `internal/interfaces/lark/handler.go`, `cmd/larkrobot/bootstrap.go`
- **Description**: 将 `messages.Handler`、`reaction.Handler`、`ConfigManager` 等全局变量收敛为 runtime 注入对象；默认 wrapper 仅保留过渡用途。
- **Dependencies**: Sprint 2 complete
- **Acceptance Criteria**:
  - 新代码路径通过构造函数装配 processor。
  - 默认 wrapper 只负责转发到已注入的 handler set。
  - 业务测试可以不触发 package `init()` 就完成装配。
- **Validation**:
  - 增加 bootstrap / handler wiring 测试。
  - 清点全局变量引用点并确认缩减。

### Task 3.2: 收敛 DB / Redis / Chunking / Schedule 全局注册表

- **Location**: `internal/infrastructure/db/db.go`, `internal/infrastructure/redis/redis.go`, `internal/application/lark/chunking/chunking.go`, `internal/application/lark/schedule/*.go`, `cmd/larkrobot/bootstrap.go`
- **Description**: 为 DB、Redis、chunking management、schedule service / scheduler 提供显式 provider 或 module struct，逐步替代 `globalDB`、`RedisClient`、`larkchunking.M`、`scheduleapp.GetService()`。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - `scheduler` 不再通过包级变量暴露生命周期句柄。
  - `application_services` 不再依赖隐藏的 registry side effect。
  - 关键依赖可在测试中以 fake provider 替换。
- **Validation**:
  - 增加服务构造与 runtime module 测试。
  - 检查 `rg "GetService\\(|globalDB|RedisClient|var scheduler"` 的剩余结果。

### Task 3.3: 把 `application_services` 拆成更小的 runtime modules

- **Location**: `cmd/larkrobot/bootstrap.go`, `cmd/larkrobot/module_helpers.go`, `internal/runtime/module.go`
- **Description**: 将当前过于宽泛的 `application_services` 模块拆成 `config_manager`、`handler_wiring`、`todo_service`、`schedule_service`、`chunking_manager` 等更细粒度模块，便于 readiness 与失败域隔离。
- **Dependencies**: Task 3.2
- **Acceptance Criteria**:
  - readiness 输出能区分是哪一层应用服务降级。
  - 单个 optional service 降级不会污染无关组件状态。
  - bootstrap 中模块顺序更接近真实依赖图。
- **Validation**:
  - 增加模块顺序和 rollback 测试。
  - 手工验证 `/healthz` 的组件颗粒度变细。

## Sprint 4: 健康探测与调度演进准备

**Goal**: 让健康状态从“启动即定格”演进为“运行时持续可解释”，同时为 ADR `0004` 的下一轮 scheduler HA 做好边界隔离。  
**Demo/Validation**:

- optional dependency 运行中断连或不可用时，状态面能反映降级原因。
- scheduler 生命周期完全由 runtime module 托管，不再依赖包级句柄。
- websocket 与 scheduler 的已知限制有明确隔离层和后续替换点。

### Task 4.1: 为 optional dependency 引入持续探测接口

- **Location**: `internal/runtime/module.go`, `internal/runtime/app.go`, `internal/runtime/health.go`, `cmd/larkrobot/bootstrap.go`
- **Description**: 为模块增加可选 probe / refresh 能力，周期性刷新 `opensearch`、`retriever`、`gotify`、`aktool`、`minio`、`ark_runtime` 等状态，而不是只在启动时执行一次 `Ready()`。
- **Dependencies**: Sprint 3 complete
- **Acceptance Criteria**:
  - optional module 运行中断连能从 `ready` 转为 `degraded`。
  - health registry 能保留最近 probe 时间和失败原因。
  - probe 不会阻塞主启动链路。
- **Validation**:
  - 增加 registry/probe 的状态转换测试。
  - 为至少一个 optional adapter 增加 fake probe 集成测试。

### Task 4.2: 把 scheduler 提升为独立 module 结构体

- **Location**: `internal/application/lark/schedule/scheduler.go`, `cmd/larkrobot/bootstrap.go`, `internal/runtime/module.go`
- **Description**: 用专门的 runtime module 包住 scheduler 的 start/stop/stats，而不是继续通过包级 `scheduler` 句柄桥接；同时显式保留 claim-before-execute 语义。
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - scheduler 生命周期完整纳入 runtime 模块语义。
  - stats 中能看到队列、claim、执行结果等关键计数。
  - 单实例模型与未来 lease/claim 演进的边界清晰。
- **Validation**:
  - 扩充 scheduler start/stop 和 executor 协同测试。
  - 验证 shutdown 顺序先停 scheduler 再停 DB/Redis。

### Task 4.3: 隔离上游 websocket 限制并补充运维文档

- **Location**: `internal/runtime/lark_ws.go`, `docs/architecture/runtime-governance.md`, `docs/adr/0004-scheduler-ha.md`
- **Description**: 维持当前 SDK 不替换，但抽象出 transport limitation 边界，明确 graceful stop 缺失、进程退出清理和未来替换策略。
- **Dependencies**: Task 4.2
- **Acceptance Criteria**:
  - websocket limitation 在代码和文档中都有单一归档点。
  - 后续如果切换 SDK 或增加外层 transport supervisor，不需要重写整个 runtime lifecycle。
- **Validation**:
  - 检查 `/healthz` 和文档能定位该限制。
  - 人工 review 代码边界是否足够清晰。

## Suggested Execution Order

推荐按下面的顺序推进，并允许小范围并行：

1. 先完成 Sprint 1，优先消除运行时最不受控的 goroutine 和内部 fan-out。
2. Sprint 2 与 Sprint 3 可以交错推进，但身份/配置边界应先于大规模 service wiring 收敛。
3. Sprint 4 必须建立在 Sprint 3 之后，否则 probe 和 scheduler module 仍会被旧的全局状态限制。

适合并行的子任务：

- Task 2.1 和 Task 2.2 可以分别由“identity”与“config accessor”两个子流并行。
- Task 3.1 和 Task 3.2 可以拆成“handler wiring”和“infra providers”两条支线。
- Task 4.1 的 probe 框架与 Task 4.2 的 scheduler module 可在接口定义稳定后并行推进。

## Testing Strategy

- **单元测试优先**：先覆盖 `runtime`, `xhandler`, `handler`, `config`, `schedule` 的可注入边界。
- **治理回归测试**：对 `go func()`、`config.Get()`、`UserId` fallback、全局注册表引用建立 allowlist 检查。
- **管理面验证**：每个 sprint 都至少验证一次 `/livez`、`/readyz`、`/healthz`、`/statusz` 输出。
- **故障演练**：对 queue full、optional adapter down、identity 缺失、scheduler service unavailable 做回归测试。

## Potential Risks & Gotchas

- `pkg/xhandler` 已被消息主链路深度依赖，过快改动执行模型容易引入顺序和时序回归；Sprint 1 必须先用测试锁住行为。
- `config.Get()` 迁移不能简单全局替换，基础设施初始化和 bot identity bootstrap 仍需要底层配置入口。
- `UserId` fallback 仍可能被老数据、老事件结构依赖，迁移时需要保留可观测兼容层，而不是直接删除。
- scheduler 和 websocket 都带有“当前实现不是最终形态”的性质，本轮的目标是隔离限制，不是一次性解完全部演进问题。
- 仓库当前存在大量未提交改动，落地时应避免跨文件大面积重排，优先做可验证的局部收敛。

## Rollback Plan

- 每个 sprint 都保留薄兼容层：例如 handler default wrapper、config accessor fallback、scheduler bridge，避免一次切断老路径。
- 新增 runtime/probe 接口时优先通过适配器接入，必要时可回退到当前 `FuncModule` 模式。
- 治理检查采用 allowlist 渐进收紧，避免因为历史包袱导致整个分支无法继续迭代。
- 如果某个子流回归风险过高，优先回退到“保留显式文档化例外”的状态，而不是重新引入匿名全局 side effect。
