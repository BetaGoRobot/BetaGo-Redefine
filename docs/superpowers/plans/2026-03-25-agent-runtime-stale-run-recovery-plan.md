# Agent Runtime Stale Run Recovery Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `agentruntime` 补齐前序 run 卡死后的自愈能力，避免 stale active run 长时间阻塞 pending initial queue 和新请求进入。

**Architecture:** 先引入 run 级存活语义，让系统能区分“状态仍是 active，但执行已经死亡”的 run；再增加 stale-run sweeper 做自动 repair；最后给 pending queue 增加最大等待时间和失败治理，补齐指标、告警和灰度开关。修复路径尽量复用现有 `RunCoordinator`、`ResumeWorker`、`PendingScopeSweeper`、Redis lease 与 DB repo，不推倒重做。

**Tech Stack:** Go, agentruntime, DB-backed run/step repositories, Redis coordination store, pending initial worker/sweeper, application runtime metrics

---

## Scope

### In Scope

- 为 run 增加可判活的 lease/heartbeat 语义
- 定期识别 stale active run 并做 repair
- 避免 stale run 持续占用 `CountActiveBySessionActor`
- 给 pending initial queue 增加最大等待时间和失败治理
- 增加指标、日志、管理查询与灰度开关

### Out of Scope

- 重构整个 runtime 编排结构
- 修改 capability / approval 主业务语义
- 设计新的外部管理后台
- 一次性解决所有历史异常数据，只做渐进治理能力

## Assumptions

- 当前 `execution lease` 仍保留，用于执行临界区互斥；新方案补的是“run 生命周期判活”，不是替换 execution lease。
- 失败修复的默认策略优先保守：把 stale active run 标记为 `failed`，并释放阻塞；不自动重放业务 capability。
- `pending initial` 的过期治理优先只处理队列项，不直接改用户消息格式；消息提示可以通过现有 patch/reply 能力实现。
- 涉及 `script/sql/` 的改动只生成脚本与执行说明，不由 agent 自动执行；数据库变更需要你在目标环境中手动落库。

## SQL Handoff Constraint

- 所有 schema 变更都必须拆成“脚本产出”和“人工执行”两个阶段。
- agent 负责：
  - 生成 `script/sql/*.sql`
  - 在代码中适配新字段的读写与兼容逻辑
  - 给出执行前检查、执行顺序、回滚建议
- 你负责：
  - 在目标数据库中手动执行 SQL
  - 确认执行结果、索引构建耗时、锁影响和回滚窗口
- 在 SQL 未执行前，相关代码变更不得假定新列已经存在；实现阶段要么放在 migration 之后合并，要么加显式 feature flag / backward-compatible 保护。

## File Map

### Core runtime state
- Modify: `internal/application/lark/agentruntime/types.go`
- Modify: `internal/application/lark/agentruntime/coordinator.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor_support.go`
- Modify: `internal/application/lark/agentruntime/reply_completion.go`

### Persistence / repository
- Modify: `internal/infrastructure/agentstore/repository.go`
- Modify: `script/sql/20260318_agent_runtime_tables.sql` (manual apply by user)
- Create: `script/sql/20260325_agent_runtime_stale_run_recovery.sql` (manual apply by user)

### Redis coordination
- Modify: `internal/infrastructure/redis/agentruntime.go`

### Pending queue governance
- Modify: `internal/application/lark/agentruntime/initial/pending_run.go`
- Modify: `internal/application/lark/agentruntime/initial/pending_worker.go`
- Modify: `internal/application/lark/agentruntime/initial/pending_scope_sweeper.go`
- Modify: `internal/application/lark/agentruntime/initial/metrics.go`
- Modify: `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`

### Wiring / background jobs / metrics
- Modify: `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- Create: `internal/application/lark/agentruntime/stale_run_sweeper.go`
- Create: `internal/application/lark/agentruntime/stale_run_sweeper_test.go`

### Tests
- Modify: `internal/application/lark/agentruntime/coordinator_test.go`
- Modify: `internal/application/lark/agentruntime/resume_worker_test.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor_test.go`
- Modify: `internal/application/lark/agentruntime/initial/pending_worker_test.go`
- Modify: `internal/application/lark/agentruntime/initial/pending_scope_sweeper_test.go`
- Modify: `internal/infrastructure/redis/agentruntime_test.go`

---

## Sprint 1: 建立 Run 判活语义

**Goal:** 系统必须先具备“判断某条 active run 还活着还是已经 stale”的基础能力，否则后续 repair 只能靠猜。

**Demo/Validation:**
- 能在 DB 中看到 run 的 lease/heartbeat 信息。
- initial/resume/capability lane 在执行期间会定期刷新 heartbeat。
- 进程停止续约后，run 会在存活字段上表现为过期。

### Task 1.1: 设计并落库 run liveness 字段
- **Location**: `script/sql/20260325_agent_runtime_stale_run_recovery.sql`, `internal/infrastructure/agentstore/repository.go`, `internal/application/lark/agentruntime/types.go`
- **Description**: 为 `agent_runs` 增加最小必需字段：
  - `worker_id`
  - `heartbeat_at`
  - `lease_expires_at`
  - 可选 `repair_attempts`
- **Dependencies**: 无
- **Acceptance Criteria**:
  - `AgentRun` 能承载这些字段
  - repo 读写 map 能正确映射
  - migration 可重复执行
  - 产出明确的“需你手动执行”的 SQL 说明
- **Validation**:
  - `go test ./internal/infrastructure/agentstore -run 'Test.*Run.*'`
  - 人工执行前检查 migration SQL 的幂等性、默认值和回滚注释

### Task 1.2: 定义 run lease/heartbeat 协议对象
- **Location**: `internal/application/lark/agentruntime/types.go`
- **Description**: 新增 run 判活相关类型与辅助函数，例如：
  - `RunLivenessState`
  - `RunLeasePolicy`
  - `IsLeaseExpired(now)` / `NeedsHeartbeat(now)`
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - 不把 lease 判活逻辑散落在各调用点
  - 有单点函数表达 stale 判断
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Liveness.*'`

### Task 1.3: initial/resume/capability lane 进入执行时写入 worker_id 和 lease
- **Location**: `internal/application/lark/agentruntime/continuation_processor.go`, `internal/application/lark/agentruntime/continuation_processor_support.go`, `internal/application/lark/agentruntime/coordinator.go`
- **Description**: 在 run 真正进入执行态时写入：
  - `worker_id`
  - `heartbeat_at = now`
  - `lease_expires_at = now + leaseTTL`
- **Dependencies**: Task 1.2
- **Acceptance Criteria**:
  - `queued -> running` 时必写 liveness
  - 恢复执行的 `ResumeRun` 不直接写 heartbeat，真正执行时才写
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*MoveRunToRunning.*|Test.*Resume.*'`

### Task 1.4: 为长期执行链路增加 heartbeat 续约
- **Location**: `internal/application/lark/agentruntime/continuation_processor.go`
- **Description**: 仿照 execution lease renew，增加 run heartbeat renew goroutine 或统一 refresh 点，在执行过程中刷新 `heartbeat_at/lease_expires_at`。
- **Dependencies**: Task 1.3
- **Acceptance Criteria**:
  - 执行超出一个 lease window 时不会被误判 stale
  - 退出执行时停止续约
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Heartbeat.*|Test.*Lease.*'`

---

## Sprint 2: 增加 Stale Run Repair

**Goal:** 当 active run 已经不再存活时，系统要能自动检测并释放阻塞。

**Demo/Validation:**
- 可以构造一条 `status in active` 且 `lease_expires_at` 已过期的 run。
- sweeper 扫描后会把它 repair 到终态，并释放 active slot。
- pending initial scope 会在 repair 后被重新唤醒。

### Task 2.1: 定义 stale-run repair 策略
- **Location**: `internal/application/lark/agentruntime/stale_run_sweeper.go`
- **Description**: 明确不同 run 状态的 repair 策略：
  - `running -> failed`
  - `queued -> failed` 或 `cancelled`，并记录 `stale_run_timeout`
  - `waiting_approval`/`waiting_schedule`/`waiting_callback` 的策略是否区分
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - repair 策略可配置，不硬编码在 sweeper 逻辑中
  - 每种 active 状态都有明确定义
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*StaleRunPolicy.*'`

### Task 2.2: 为 repository 增加 stale active run 查询能力
- **Location**: `internal/infrastructure/agentstore/repository.go`
- **Description**: 增加可分页查询接口，筛出：
  - active status
  - `lease_expires_at < now` 或 `heartbeat_at` 超时
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 查询不影响现有 active count 逻辑
  - 支持 limit/cursor 或按 `updated_at` 扫描
- **Validation**:
  - `go test ./internal/infrastructure/agentstore -run 'Test.*Stale.*Run.*'`

### Task 2.3: 实现 stale-run sweeper
- **Location**: `internal/application/lark/agentruntime/stale_run_sweeper.go`, `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- **Description**: 新增后台 sweeper，周期扫描 stale active run，并调用 coordinator/repository 完成 repair。
- **Dependencies**: Task 2.2
- **Acceptance Criteria**:
  - 支持 `Start/Stop/Stats`
  - repair 后会清 session active、清 actor active slot、notify pending scope
  - repair 不依赖人工触发
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*StaleRunSweeper.*'`

### Task 2.4: 将 repair 结果纳入 active count 语义
- **Location**: `internal/infrastructure/agentstore/repository.go`, `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- **Description**: 保证 stale run 一旦 repair，不再被 `CountActiveBySessionActor` 和 `FindLatestActiveBySessionActor` 继续视为 active。
- **Dependencies**: Task 2.3
- **Acceptance Criteria**:
  - outstanding counter 结果会随 repair 下降
  - shadow policy 的 `ActiveRunSnapshot` 不会继续吃到 stale run
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*ActiveRunSnapshot.*|Test.*Outstanding.*'`

---

## Sprint 3: Pending Queue 治理

**Goal:** 就算前序 run 修复失败或修复滞后，pending queue 也不能无限等待。

**Demo/Validation:**
- pending run 超过最大等待时间后，会被摘除并对用户回写超时提示。
- scope sweeper 不会反复唤醒已经超时的 pending item。
- 队列项支持 attempts/expired 统计。

### Task 3.1: 扩展 PendingRun 元数据
- **Location**: `internal/application/lark/agentruntime/initial/pending_run.go`
- **Description**: 为 `PendingRun` 增加：
  - `AttemptCount`
  - `LastAttemptAt`
  - 保留并正式治理 `RequestedAt`
- **Dependencies**: 无
- **Acceptance Criteria**:
  - 编解码兼容旧 payload
  - 老数据缺失字段时有默认值
- **Validation**:
  - `go test ./internal/application/lark/agentruntime/initial -run 'Test.*PendingRun.*'`

### Task 3.2: worker 处理超时与 attempts 上限
- **Location**: `internal/application/lark/agentruntime/initialcore/worker.go`, `internal/application/lark/agentruntime/initial/pending_worker.go`
- **Description**: 在 worker 消费时加入策略：
  - `max_queue_age`
  - `max_attempts`
  - 超出后不再 `PrependPendingInitialRun`
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - `ErrRunSlotOccupied` 仍可重试
  - 超时/超次的 item 会被终止而不是永久回队
- **Validation**:
  - `go test ./internal/application/lark/agentruntime/initial -run 'Test.*PendingWorker.*'`

### Task 3.3: 为过期 pending item 增加用户侧提示
- **Location**: `internal/application/lark/agentruntime/initial/pending_worker.go`, `internal/application/lark/agentruntime/initial/lark_emitter.go`, `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
- **Description**: 如果 pending item 过期，优先 patch 原排队卡片；没有卡片时退化为 reply/create 提示。
- **Dependencies**: Task 3.2
- **Acceptance Criteria**:
  - 复用现有 root target / reply target
  - 文案与指标都能区分“排队超时”
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Pending.*Expired.*'`

### Task 3.4: 让 pending scope sweeper 跳过已不可恢复 scope
- **Location**: `internal/application/lark/agentruntime/initial/pending_scope_sweeper.go`
- **Description**: sweeper 只负责唤醒可恢复 scope；对已经超时清空的 scope 不再反复 reschedule。
- **Dependencies**: Task 3.2
- **Acceptance Criteria**:
  - `stale_cleared` 指标和 `expired` 指标分离
  - scope clear 行为与队列空、队列过期两种情况可区分
- **Validation**:
  - `go test ./internal/application/lark/agentruntime/initial -run 'Test.*PendingScopeSweeper.*'`

---

## Sprint 4: 指标、告警、管理能力

**Goal:** 修复机制上线前后，都要能看见系统当前到底有多少 stale run、多少 queue timeout、修了多少条 run。

**Demo/Validation:**
- health/metrics 能看到 stale-run 和 queue-timeout 指标。
- sweeper 和 repair 的统计可读。
- 可以从 runtime stats 中确认最近一次 repair 的对象和结果。

### Task 4.1: 增加 stale-run / queue-timeout 指标
- **Location**: `internal/application/lark/agentruntime/initial/metrics.go`, `internal/application/lark/agentruntime/stale_run_sweeper.go`, `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- **Description**: 增加指标：
  - `stale_runs_found`
  - `stale_runs_repaired`
  - `stale_runs_repair_failed`
  - `pending_runs_expired`
  - `pending_runs_dropped_by_attempts`
- **Dependencies**: Sprint 2, Sprint 3
- **Acceptance Criteria**:
  - metrics provider 可导出
  - 统计口径清晰，不与现有 enqueue/requeue 指标混淆
- **Validation**:
  - `go test ./internal/application/lark/agentruntime/... -run 'Test.*Metrics.*'`

### Task 4.2: 增加结构化日志和 stats 输出
- **Location**: `internal/application/lark/agentruntime/stale_run_sweeper.go`, `internal/application/lark/agentruntime/initial/pending_worker.go`, `internal/application/lark/agentruntime/resume.go`
- **Description**: 在 repair、drop、expire 路径输出结构化日志，并在 `Stats()` 中增加最近处理对象。
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - 日志包含 `run_id/session_id/chat_id/actor_open_id/status/decision`
  - `Stats()` 可用于 health/debug
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Stats.*'`

### Task 4.3: 增加灰度开关
- **Location**: `internal/application/lark/agentruntime/runtimewire/runtimewire.go`, 相关 config 接入点
- **Description**: 增加 feature flag：
  - `enable_run_heartbeat`
  - `enable_stale_run_sweeper`
  - `enable_pending_expiry`
- **Dependencies**: Sprint 1-3
- **Acceptance Criteria**:
  - 可以单独打开某一块能力
  - 关闭开关时旧逻辑不变
- **Validation**:
  - `go test ./internal/application/lark/agentruntime/...`

---

## Sprint 5: 上线与回滚准备

**Goal:** 让这套修复能力可以安全上线，不把正常 run 误杀。

**Demo/Validation:**
- 先只观测不修复，再开启 repair，再开启 queue expiry。
- 每一步都有明确回滚手段。

### Task 5.1: 上线顺序与保护阈值
- **Location**: plan-only / config rollout notes
- **Description**: 上线顺序：
  1. 你手动执行 SQL migration，并确认 schema 已落库
  2. 只写 heartbeat，不 repair
  3. 开 stale-run 探测与指标
  4. 开 repair，但只处理 `running/queued`
  5. 最后再开 pending expiry
- **Dependencies**: Sprint 1-4
- **Acceptance Criteria**:
  - 每一步都有开关
  - 每一步都有回滚动作
  - SQL 执行与代码发布之间有明确检查点
- **Validation**:
  - staging 演练 checklist

### Task 5.2: 补管理脚本/查询
- **Location**: 可放 `script/sql/` 或 `script/ops/`
- **Description**: 提供最小运维查询：
  - 列出 active 超时 run
  - 列出 pending backlog 最长队列
  - 手动标记/回滚 repair
- **Dependencies**: Sprint 2, Sprint 3
- **Acceptance Criteria**:
  - 出问题时无需手工拼 SQL 才能排查
- **Validation**:
  - staging 手动演练

---

## Testing Strategy

- 单元测试优先冻结：
  - run heartbeat 刷新
  - stale-run 判定
  - repair 状态推进
  - pending expiry / attempts 上限
- 集成测试重点覆盖：
  - 前序 run 卡死后，新任务不再永久阻塞
  - approval / callback / schedule waiting run 不被误杀
  - pending 卡片 patch/reply 在过期场景仍正确
- 演练用例至少包含：
  - `running` run 心跳停止
  - `queued` run 长期未推进
  - `waiting_approval` 超时
  - pending queue 连续回队

## Potential Risks & Gotchas

- 最大风险是误判正常长任务为 stale，因此 `lease_expires_at` 和 heartbeat 刷新窗口必须保守。
- `waiting_approval`/`waiting_schedule`/`waiting_callback` 不一定都应该直接 `failed`，策略要分状态设计。
- 旧数据没有新字段，migration 与默认值必须兼容。
- pending item 过期后回写消息可能失败，不能阻塞队列摘除。
- repair 必须通过现有 coordinator/cleanup 路径完成，不能绕过 active slot 清理逻辑。

## Rollback Plan

- 关闭 `enable_pending_expiry`，恢复旧的 pending queue 行为。
- 关闭 `enable_stale_run_sweeper`，保留 heartbeat 但停止自动 repair。
- 关闭 `enable_run_heartbeat`，恢复旧执行路径；保留字段不回删。
- 如 repair 误伤，提供脚本把指定 run 从 `failed/cancelled` 手动恢复到人工指定状态，仅限运维介入使用。

---

## Chunk 1: Sprint 1 执行拆分

### Slice 1A: SQL 迁移与 `AgentRun` 字段映射

**Files:**
- Modify: `script/sql/20260318_agent_runtime_tables.sql`
- Create: `script/sql/20260325_agent_runtime_stale_run_recovery.sql`
- Modify: `internal/application/lark/agentruntime/types.go`
- Modify: `internal/infrastructure/agentstore/repository.go`
- Test: `internal/infrastructure/agentstore/repository.go`

- [ ] **Step 1: 盘点 `agent_runs` 的现有 schema 与 repo 映射点**
  Run: `rg -n "agent_runs|AgentRun|Scan|INSERT INTO agent_runs|UPDATE agent_runs" script/sql internal/infrastructure/agentstore/repository.go internal/application/lark/agentruntime/types.go`
  Expected: 找到 schema、scan、insert、update 的唯一修改入口。
- [ ] **Step 2: 产出 migration SQL，不自动执行**
  内容至少包含：`worker_id`、`heartbeat_at`、`lease_expires_at`、可选 `repair_attempts`，以及必要索引和回滚注释。
- [ ] **Step 3: 在计划和交付说明中加人工落库 Gate**
  Gate: 在你手动执行 SQL 并确认成功前，不继续提交依赖新列存在的代码路径。
- [ ] **Step 4: 更新 `AgentRun` 结构体与序列化字段**
  目标：让新字段在代码里可读可写，同时对旧数据保持零值兼容。
- [ ] **Step 5: 更新 repository 的 scan / insert / update 映射**
  目标：新增字段读写集中在 repo，避免调用层手写 SQL 片段。
- [ ] **Step 6: 补 repository 侧测试，覆盖空值和非空值**
  Run: `go test ./internal/infrastructure/agentstore -run 'Test.*Run.*'`
  Expected: PASS
- [ ] **Step 7: 向你交付 SQL 执行清单**
  内容：执行顺序、预检查、回滚建议、是否需要低峰期执行。

### Slice 1B: 判活协议对象与统一判断入口

**Files:**
- Modify: `internal/application/lark/agentruntime/types.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor_support.go`
- Test: `internal/application/lark/agentruntime/continuation_processor_test.go`

- [ ] **Step 1: 定义 `RunLeasePolicy` 和 `RunLivenessState`**
  目标：把 lease window、heartbeat interval、stale threshold 放到一个集中对象里。
- [ ] **Step 2: 为 `AgentRun` 增加统一判断函数**
  例如：`IsRunLeaseExpired(now)`、`NeedsRunHeartbeat(now)`、`ClassifyRunLiveness(now, policy)`。
- [ ] **Step 3: 清理散落在 processor/support 中的时间判断**
  目标：避免后续 sweeper、processor、worker 各自复制 stale 判定。
- [ ] **Step 4: 写纯单元测试冻结边界**
  用例至少包括：未初始化、刚进入 running、接近过期、已过期、长任务续约后恢复健康。
- [ ] **Step 5: 运行判活相关测试**
  Run: `go test ./internal/application/lark/agentruntime -run 'Test.*Liveness.*|Test.*Lease.*'`
  Expected: PASS

### Slice 1C: 进入执行态时写入 liveness

**Files:**
- Modify: `internal/application/lark/agentruntime/coordinator.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor_support.go`
- Test: `internal/application/lark/agentruntime/coordinator_test.go`
- Test: `internal/application/lark/agentruntime/continuation_processor_test.go`
- Test: `internal/application/lark/agentruntime/resume_worker_test.go`

- [ ] **Step 1: 找出所有 run 真正开始执行的入口**
  重点确认 initial、resume、capability continuation 三条 lane 是否共用同一推进点。
- [ ] **Step 2: 在 `queued -> running` 的 durable 转换里写入初始 liveness**
  字段：`worker_id`、`heartbeat_at`、`lease_expires_at`。
- [ ] **Step 3: 保证仅在“真正开始执行”时写入**
  约束：不要在只做 schedule/resume 排队时提前刷新 heartbeat。
- [ ] **Step 4: 让 coordinator 暴露一个显式的“启动 lease”能力**
  目标：写入逻辑集中，避免 processor 多处直接写 repo。
- [ ] **Step 5: 补充状态迁移测试**
  用例：正常启动、重复启动、resume 只排队不续约、并发竞争时 CAS 拒绝。
- [ ] **Step 6: 跑相关测试**
  Run: `go test ./internal/application/lark/agentruntime -run 'Test.*MoveRunToRunning.*|Test.*Resume.*|Test.*Coordinator.*'`
  Expected: PASS

### Slice 1D: 执行期 heartbeat 续约

**Files:**
- Modify: `internal/application/lark/agentruntime/continuation_processor.go`
- Modify: `internal/application/lark/agentruntime/coordinator.go`
- Test: `internal/application/lark/agentruntime/continuation_processor_test.go`

- [ ] **Step 1: 复用现有 execution lease renew 结构，选定 run heartbeat 刷新点**
  优先复用已有 ticker/goroutine 生命周期，避免再造一套并发控制。
- [ ] **Step 2: 实现续约函数**
  目标：只刷新当前 worker 持有的 run，并更新 `heartbeat_at/lease_expires_at`。
- [ ] **Step 3: 确保 processor 退出时能停止续约**
  覆盖正常返回、错误返回、context cancel 三类退出。
- [ ] **Step 4: 增加长执行链路测试**
  用例：运行跨过一个 lease window 不误判 stale；退出后 lease 自然过期。
- [ ] **Step 5: 跑 heartbeat 测试**
  Run: `go test ./internal/application/lark/agentruntime -run 'Test.*Heartbeat.*|Test.*Lease.*'`
  Expected: PASS
- [ ] **Step 6: 提交一个只包含 Sprint 1 的可回滚变更**
  Commit suggestion: `feat(agentruntime): add run liveness lease fields and heartbeat`

## Chunk 2: Sprint 2 执行拆分

### Slice 2A: 明确 stale repair 策略表

**Files:**
- Create: `internal/application/lark/agentruntime/stale_run_sweeper.go`
- Modify: `internal/application/lark/agentruntime/types.go`
- Test: `internal/application/lark/agentruntime/stale_run_sweeper_test.go`

- [ ] **Step 1: 列出所有 active run 状态**
  目标：覆盖 `queued`、`running`、`waiting_approval`、`waiting_schedule`、`waiting_callback`。
- [ ] **Step 2: 为每种状态定义 repair 决策**
  至少明确：终态、错误码/原因、是否允许重入、是否通知 pending scope。
- [ ] **Step 3: 把 repair 决策抽成可测试函数**
  例如：`DecideStaleRunRepair(run, now, policy)`。
- [ ] **Step 4: 补状态策略测试**
  Run: `go test ./internal/application/lark/agentruntime -run 'Test.*StaleRunPolicy.*'`
  Expected: PASS

### Slice 2B: 仓储侧 stale 查询能力

**Files:**
- Modify: `internal/infrastructure/agentstore/repository.go`
- Test: `internal/infrastructure/agentstore/repository.go`

- [ ] **Step 1: 设计 stale 查询接口**
  目标：支持 active status 过滤、按过期时间排序、limit 分页。
- [ ] **Step 2: 实现 SQL 查询**
  条件至少包含：`status in active` 且 `lease_expires_at < now`，必要时兼容 `heartbeat_at` 兜底。
- [ ] **Step 3: 校验查询不会污染现有 active count 逻辑**
  约束：查询接口新增，不直接偷改 `CountActiveBySessionActor` 语义。
- [ ] **Step 4: 写 repo 测试覆盖分页和排序**
  Run: `go test ./internal/infrastructure/agentstore -run 'Test.*Stale.*Run.*'`
  Expected: PASS

### Slice 2C: stale-run sweeper 与 runtime wiring

**Files:**
- Create: `internal/application/lark/agentruntime/stale_run_sweeper.go`
- Modify: `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- Test: `internal/application/lark/agentruntime/stale_run_sweeper_test.go`

- [ ] **Step 1: 定义 sweeper 生命周期接口**
  至少包含：`Start`、`Stop`、`Stats`、扫描周期、单轮 limit。
- [ ] **Step 2: 在单轮扫描中串起“查 stale -> 决策 -> repair -> 记录结果”**
  要求：单条失败不影响本轮后续处理。
- [ ] **Step 3: 接入 `runtimewire`**
  目标：服务启动时注册，关闭时优雅停止，受 feature flag 控制。
- [ ] **Step 4: 写 sweeper 级测试**
  用例：找到 stale run、忽略健康 run、repair 失败计数、stop 后不再扫描。
- [ ] **Step 5: 跑 sweeper 测试**
  Run: `go test ./internal/application/lark/agentruntime -run 'Test.*StaleRunSweeper.*'`
  Expected: PASS

### Slice 2D: repair 后释放 active slot

**Files:**
- Modify: `internal/application/lark/agentruntime/coordinator.go`
- Modify: `internal/application/lark/agentruntime/reply_completion.go`
- Modify: `internal/infrastructure/agentstore/repository.go`
- Test: `internal/application/lark/agentruntime/coordinator_test.go`
- Test: `internal/application/lark/agentruntime/continuation_processor_test.go`

- [ ] **Step 1: 复核当前 active slot 清理路径**
  重点检查 session `ActiveRunID`、actor active count、pending scope notify 的清理顺序。
- [ ] **Step 2: 为 stale repair 复用现有 cleanup 路径**
  约束：不能让 sweeper 直接绕过 coordinator 手改 DB 状态。
- [ ] **Step 3: 补 active count 语义测试**
  用例：repair 前被视为 active，repair 后不再阻塞新 run。
- [ ] **Step 4: 跑 active snapshot / outstanding 相关测试**
  Run: `go test ./internal/application/lark/agentruntime -run 'Test.*ActiveRunSnapshot.*|Test.*Outstanding.*|Test.*Coordinator.*'`
  Expected: PASS
- [ ] **Step 5: 提交一个只包含 stale repair 的可回滚变更**
  Commit suggestion: `feat(agentruntime): repair stale active runs via sweeper`

## Chunk 3: Sprint 3 执行拆分

### Slice 3A: `PendingRun` 元数据扩展

**Files:**
- Modify: `internal/application/lark/agentruntime/initial/pending_run.go`
- Test: `internal/application/lark/agentruntime/initial/pending_worker_test.go`

- [ ] **Step 1: 盘点 `PendingRun` 的编码/解码入口**
  目标：确认 Redis queue payload 与历史兼容需求。
- [ ] **Step 2: 增加 `AttemptCount`、`LastAttemptAt`、标准化 `RequestedAt`**
  要求：旧 payload 缺字段时使用默认值。
- [ ] **Step 3: 写兼容性测试**
  用例：老 payload、新 payload、缺失时间字段、attempt 自增后回队。
- [ ] **Step 4: 跑 pending model 测试**
  Run: `go test ./internal/application/lark/agentruntime/initial -run 'Test.*PendingRun.*'`
  Expected: PASS

### Slice 3B: worker 超时和 attempts 上限

**Files:**
- Modify: `internal/application/lark/agentruntime/initialcore/worker.go`
- Modify: `internal/application/lark/agentruntime/initial/pending_worker.go`
- Test: `internal/application/lark/agentruntime/initial/pending_worker_test.go`

- [ ] **Step 1: 定义 `max_queue_age` 与 `max_attempts` 配置**
  目标：支持默认值和 feature flag 关闭。
- [ ] **Step 2: 在消费路径中区分“可重试占位冲突”和“不可恢复超时/超次”**
  约束：`ErrRunSlotOccupied` 仍允许正常回队。
- [ ] **Step 3: 为过期和超次路径增加终止决策**
  目标：不要再 `PrependPendingInitialRun` 形成永久回队。
- [ ] **Step 4: 补 worker 测试**
  Run: `go test ./internal/application/lark/agentruntime/initial -run 'Test.*PendingWorker.*'`
  Expected: PASS

### Slice 3C: pending 过期后的用户提示

**Files:**
- Modify: `internal/application/lark/agentruntime/initial/pending_worker.go`
- Modify: `internal/application/lark/agentruntime/initial/lark_emitter.go`
- Modify: `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
- Test: `internal/application/lark/agentruntime/initial/pending_worker_test.go`

- [ ] **Step 1: 确认过期场景的回复目标来源**
  目标：优先 patch 原排队卡片，没有卡片再退化到 reply/create。
- [ ] **Step 2: 定义统一的“排队超时”消息语义**
  至少区分：队列超时、重试超限、系统稍后重试建议。
- [ ] **Step 3: 接入发消息路径，但不阻塞队列摘除**
  约束：消息发送失败只能记日志和指标，不能让 pending item 留在队列里。
- [ ] **Step 4: 补用户侧提示测试**
  Run: `go test ./internal/application/lark/agentruntime -run 'Test.*Pending.*Expired.*'`
  Expected: PASS

### Slice 3D: pending scope sweeper 治理

**Files:**
- Modify: `internal/application/lark/agentruntime/initial/pending_scope_sweeper.go`
- Modify: `internal/application/lark/agentruntime/initial/metrics.go`
- Test: `internal/application/lark/agentruntime/initial/pending_scope_sweeper_test.go`

- [ ] **Step 1: 明确 sweeper 只唤醒“仍可恢复”的 scope**
  目标：与“已过期清空”的 scope 分流。
- [ ] **Step 2: 增加 expired / stale_cleared 区分指标**
  约束：不要和现有 requeue 指标混用。
- [ ] **Step 3: 补 sweeper 测试**
  用例：空队列清理、可恢复 scope 唤醒、过期 scope 跳过、重复扫描不抖动。
- [ ] **Step 4: 运行 scope sweeper 测试**
  Run: `go test ./internal/application/lark/agentruntime/initial -run 'Test.*PendingScopeSweeper.*'`
  Expected: PASS
- [ ] **Step 5: 提交一个只包含 pending 治理的可回滚变更**
  Commit suggestion: `feat(agentruntime): expire unrecoverable pending initial runs`

## Chunk 4: Sprint 4 执行拆分

### Slice 4A: metrics 指标补齐

**Files:**
- Modify: `internal/application/lark/agentruntime/initial/metrics.go`
- Modify: `internal/application/lark/agentruntime/stale_run_sweeper.go`
- Modify: `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- Test: `internal/application/lark/agentruntime/stale_run_sweeper_test.go`

- [ ] **Step 1: 为 stale repair 和 pending expiry 定义统一指标名**
  指标至少包括：`stale_runs_found`、`stale_runs_repaired`、`stale_runs_repair_failed`、`pending_runs_expired`、`pending_runs_dropped_by_attempts`。
- [ ] **Step 2: 在成功、失败、跳过三类路径分别记数**
  目标：上线后能区分“没扫到”与“扫到了但修不了”。
- [ ] **Step 3: 补指标测试或 stats 断言**
  Run: `go test ./internal/application/lark/agentruntime/... -run 'Test.*Metrics.*'`
  Expected: PASS

### Slice 4B: 结构化日志与 `Stats()`

**Files:**
- Modify: `internal/application/lark/agentruntime/stale_run_sweeper.go`
- Modify: `internal/application/lark/agentruntime/initial/pending_worker.go`
- Modify: `internal/application/lark/agentruntime/resume.go`
- Test: `internal/application/lark/agentruntime/stale_run_sweeper_test.go`

- [ ] **Step 1: 为 repair / expire / drop 设计统一日志字段**
  至少包含：`run_id`、`session_id`、`chat_id`、`actor_open_id`、`status`、`decision`。
- [ ] **Step 2: 在 `Stats()` 中加入最近处理对象和计数摘要**
  目标：方便健康检查和 staging 排障。
- [ ] **Step 3: 跑 stats/log 相关测试**
  Run: `go test ./internal/application/lark/agentruntime -run 'Test.*Stats.*'`
  Expected: PASS

### Slice 4C: feature flag 与兼容发布

**Files:**
- Modify: `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- Modify: 相关 config 接入点
- Test: `internal/application/lark/agentruntime/runtimewire/runtimewire_test.go`

- [ ] **Step 1: 增加 `enable_run_heartbeat`、`enable_stale_run_sweeper`、`enable_pending_expiry`**
  目标：三块能力独立开关，便于灰度。
- [ ] **Step 2: 确认关闭开关时旧逻辑完全不变**
  约束：不因为新字段存在就改变老路径语义。
- [ ] **Step 3: 补 wiring/config 测试**
  Run: `go test ./internal/application/lark/agentruntime/runtimewire -run 'Test.*'`
  Expected: PASS
- [ ] **Step 4: 提交观测与开关变更**
  Commit suggestion: `feat(agentruntime): add stale-run metrics and rollout flags`

## Chunk 5: Sprint 5 执行拆分

### Slice 5A: SQL 手工执行与检查清单

**Files:**
- Modify: `script/sql/20260325_agent_runtime_stale_run_recovery.sql`
- Create or Modify: `script/ops/` 下的辅助查询脚本（如需要）

- [ ] **Step 1: 输出 SQL 执行前检查项**
  内容：目标库版本、表规模、索引构建方式、是否需要锁表窗口。
- [ ] **Step 2: 输出手工执行顺序**
  顺序：先 schema，再代码发布，再 heartbeat，再 sweeper，再 pending expiry。
- [ ] **Step 3: 输出失败回滚建议**
  内容：如何回退代码开关、如何停用 sweeper、SQL 是否只做前向兼容。

### Slice 5B: 运维查询与手工 repair 工具

**Files:**
- Create or Modify: `script/sql/*.sql`
- Create or Modify: `script/ops/*`

- [ ] **Step 1: 提供 stale active run 查询 SQL**
  目标：无需临时手拼 SQL 就能看出最老的阻塞 run。
- [ ] **Step 2: 提供 pending backlog 查询**
  目标：能按 scope / actor 看最长等待队列。
- [ ] **Step 3: 提供受控的手工 repair / 回滚说明**
  约束：仅运维介入使用，不替代自动修复主流程。

### Slice 5C: staging 演练和灰度顺序

**Files:**
- Modify: 当前计划文件

- [ ] **Step 1: staging 只开启 `enable_run_heartbeat`**
  验证：heartbeat 字段在跑，正常任务不误杀。
- [ ] **Step 2: staging 开启 stale-run 探测，不开启 repair**
  验证：指标能观测到 stale 候选。
- [ ] **Step 3: staging 开启 repair，但先只处理 `running/queued`**
  验证：stale run 被释放，新请求不再永久阻塞。
- [ ] **Step 4: staging 最后开启 `enable_pending_expiry`**
  验证：pending queue 超时能摘除并提示用户。
- [ ] **Step 5: 每一步都留回滚窗口**
  约束：任一步异常，先关 feature flag，再分析日志和指标。
- [ ] **Step 6: 汇总最终发布 checklist**
  Commit suggestion: `docs(agentruntime): add stale-run rollout and ops checklist`
