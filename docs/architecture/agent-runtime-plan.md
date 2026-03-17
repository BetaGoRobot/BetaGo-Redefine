# Plan: BetaGo_v2 Agent Runtime Incremental Rollout

**Generated**: 2026-03-17
**Estimated Complexity**: High

## Overview

这份计划对应 `docs/architecture/agent-runtime-design.md`，目标是在不推翻当前 Lark runtime、typed command/tool、schedule、card callback 的前提下，渐进式引入一个可持续、可恢复、可审批、可观测的 `Agent Runtime`。

总原则：

- 先收敛 runtime contract，再切真实流量。
- 先做 shadow mode，再做 user-visible cutover。
- 先把 callback / schedule 变成 run-aware continuation，再谈更复杂的 memory / specialist handoff。

## Success Criteria

- 非命令式聊天存在统一 `AgentOperator`，而不是继续分散在 ad-hoc handler 里。
- 至少一条真实群聊链路具备 `session -> run -> step` durable state。
- callback 和异步恢复不依赖“必须是同一进程”这一假设。
- 工具调用具备 capability metadata，可做 approval / timeout / scope gate。
- 群聊场景有明确的 active run、follow-up window、supersede 规则。
- 整套能力可通过 feature flag 完整回退到旧链路。

## Non-Goals

- 本轮不直接上多 agent 协同。
- 本轮不拆 sidecar agent service。
- 本轮不替换现有 typed command framework。
- 本轮不要求所有工具都立即支持审批和等待。

## Sprint 1: 建立 Agent Runtime Contract

**Goal**: 先把核心抽象和能力边界建起来，让后续实现不再继续往旧 chat handler 里塞语义。  
**Demo/Validation**:

- 仓库内出现独立的 `internal/application/lark/agentruntime` 包。
- capability metadata、session/run/step types、group policy、resume contract 有单元测试。
- 旧代码仍然不受影响。

### Task 1.1: 新增 runtime domain model

- **Location**: `internal/application/lark/agentruntime/types.go`
- **Description**: 定义 `AgentSession`、`AgentRun`、`AgentStep`、状态枚举、step kind、trigger type、waiting reason。
- **Dependencies**: none
- **Acceptance Criteria**:
  - 类型不依赖具体 Lark SDK payload。
  - 状态和 step kind 足以覆盖审批、等待、恢复、取消、完成。
- **Validation**:
  - 增加状态迁移与 DTO 编解码测试。

### Task 1.2: 定义 capability metadata 与 adapter interface

- **Location**: `internal/application/lark/agentruntime/capability.go`
- **Description**: 定义 `CapabilityMeta`、`Capability`、`CapabilityRegistry`，允许现有 command/tool 被包成 runtime 可调度能力。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - metadata 至少包含 `side_effect_level`、`requires_approval`、`supports_async`、`allowed_scopes`、`default_timeout`。
  - registry 支持按名字查 capability，并输出 list 供 trace/log 使用。
- **Validation**:
  - capability registry 单测覆盖重复注册、scope gate、metadata 读取。

### Task 1.3: 定义 group policy contract

- **Location**: `internal/application/lark/agentruntime/policy.go`
- **Description**: 把 `trigger policy`、`ownership policy`、`follow-up window`、`supersede policy` 抽象为显式策略接口。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - 可以明确判断某条消息是否进入 agent runtime。
  - 可以明确判断新输入是否 supersede 当前 run。
- **Validation**:
  - 针对 `@bot`、reply-to-bot、普通消息、follow-up window 超时、supersede 场景补测试。

## Sprint 2: 接出现有 Tool / Command 能力，但先不切流

**Goal**: 让 Agent Runtime 能用现有能力做事，但仍处于 shadow mode。  
**Demo/Validation**:

- runtime 能成功枚举现有 capability。
- shadow mode 下会创建 run/step，但不实际替换用户可见回复。

### Task 2.1: 为现有 tool registry 增加 capability adapter

- **Location**: `internal/application/lark/handlers/tools.go`, `internal/application/lark/agentruntime/capability_tools.go`
- **Description**: 把当前 tool registry 封成 runtime capability backend，先覆盖常用 read-only / low-risk 工具。
- **Dependencies**: Sprint 1 complete
- **Acceptance Criteria**:
  - tool capability 能复用现有 handler 实现。
  - capability metadata 与原 tool name 一一对应。
- **Validation**:
  - 用 fake tool executor 验证入参、超时和输出透传。

### Task 2.2: 为少量 typed commands 增加 bridge adapter

- **Location**: `internal/application/lark/command/command.go`, `internal/application/lark/agentruntime/capability_commands.go`
- **Description**: 为 `/bb` 及少量适合桥接的 typed command 提供 runtime adapter，不要求全量命令全部接入。
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 至少 `/bb` 或等价 chat entry 可被当成 capability 调起。
  - adapter 不破坏现有命令直连执行路径。
- **Validation**:
  - 增加命令桥接测试，确认 raw command 和 typed args 的兼容行为。

### Task 2.3: 引入 AgentOperator shadow mode

- **Location**: `internal/application/lark/messages/ops/agent_op.go`, `internal/application/lark/messages/handler.go`
- **Description**: 新增 `AgentOperator`，只做 trigger/policy/run creation/trace，不做实际 reply，先旁路观察真实消息流。
- **Dependencies**: Task 2.1, Task 2.2
- **Acceptance Criteria**:
  - 可通过 feature flag 开关 shadow mode。
  - shadow mode 不改变用户可见结果。
  - shadow mode 能记录为什么某条消息没有进入 agent runtime。
- **Validation**:
  - operator 单测 + 手工在测试 chat 中观察日志/trace。

## Sprint 3: 做出 Durable Store 与跨进程协调

**Goal**: 让 session/run/step 从内存语义变成可恢复语义。  
**Demo/Validation**:

- run/step 会落盘。
- callback/恢复不依赖同进程。
- 同一个 run 有分布式互斥执行语义。

### Task 3.1: 增加 agent DB model 与 repository

- **Location**: `internal/infrastructure/db/model/agent_*.go`, `internal/infrastructure/agentstore/*.go`
- **Description**: 新增 `agent_sessions`、`agent_runs`、`agent_steps` 对应 model/repository。
- **Dependencies**: Sprint 2 complete
- **Acceptance Criteria**:
  - repository 支持按 chat scope 查 session、按 run 查 steps、更新 run status/revision。
  - 状态更新支持 optimistic concurrency 或 revision 检查。
- **Validation**:
  - repository 测试覆盖创建、状态迁移、revision conflict。

### Task 3.2: 增加 Redis coordination helpers

- **Location**: `internal/infrastructure/redis/agentruntime.go`
- **Description**: 增加 `run lock`、`cancel generation`、`active chat slot`、`resume queue` 操作封装。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 同一个 run 只能被一个 worker 持有执行权。
  - 新 run 能对同 chat active run 执行 supersede。
  - resume queue 可跨进程消费。
- **Validation**:
  - Redis integration test 或最小 fake test 覆盖 lock/release/supersede/enqueue。

### Task 3.3: 实现 RunCoordinator

- **Location**: `internal/application/lark/agentruntime/coordinator.go`
- **Description**: 统一负责 `StartRun`、`ResumeRun`、`CancelRun`、`AdvanceStep`，并把 DB + Redis 拼成一个真正的 runtime。
- **Dependencies**: Task 3.1, Task 3.2
- **Acceptance Criteria**:
  - 能创建 run 并推进至少一轮 step。
  - revision mismatch、lock conflict、cancel generation mismatch 有清晰错误语义。
- **Validation**:
  - coordinator 单测覆盖 start/resume/cancel/conflict。

## Sprint 4: 接入 Callback / Async Resume / Approval

**Goal**: 把“用户点击卡片”和“异步后续执行”变成 agent runtime 的 continuation。  
**Demo/Validation**:

- callback 可以恢复 run。
- 需要审批的 capability 可以真正等待批准再继续。
- 不同进程接 callback 也能继续执行。

### Task 4.1: 定义 approval gateway 和审批卡片协议

- **Location**: `internal/application/lark/agentruntime/approval.go`, `pkg/cardaction/action.go`
- **Description**: 定义 approval DTO、token/revision 约束、审批卡 payload 规范。
- **Dependencies**: Sprint 3 complete
- **Acceptance Criteria**:
  - approval request 带 `run_id`、`step_id`、`token`、`revision`。
  - 过期审批和重复审批都能被拒绝。
- **Validation**:
  - DTO/validator 测试覆盖正常、过期、重放、revision mismatch。

### Task 4.2: 为 card callback 增加 run-aware adapter

- **Location**: `internal/application/lark/cardaction/registry.go`, `internal/application/lark/cardaction/builtin.go`, `internal/interfaces/lark/handler.go`
- **Description**: callback 入口先尝试解析 agent callback，命中则写状态并投递 resume event，未命中再走旧 registry。
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - callback 处理不再依赖原始进程内存。
  - agent callback 与既有 callback 可以并存。
- **Validation**:
  - callback integration test 覆盖命中 agent / fallback old registry 两类路径。

### Task 4.3: 实现 immediate resume worker

- **Location**: `internal/application/lark/agentruntime/resume.go`, `cmd/larkrobot/bootstrap.go`
- **Description**: 增加基于 Redis queue 的 resume consumer，把 callback / async result 转成可恢复 run。
- **Dependencies**: Task 4.2
- **Acceptance Criteria**:
  - worker 能从 queue 消费并安全推进 run。
  - run lock 丢失时不会并发推进同一 run。
- **Validation**:
  - resume worker 测试覆盖重复事件、并发消费、取消后恢复被拒绝。

### Task 4.4: 将 scheduler 接成 delayed continuation substrate

- **Location**: `internal/application/lark/schedule/service.go`, `internal/application/lark/schedule/scheduler.go`, `internal/application/lark/agentruntime/resume_schedule.go`
- **Description**: 对需要未来某时刻再继续的 run，落一个 delayed continuation，而不是要求 callback/请求线程一直活着。
- **Dependencies**: Task 4.3
- **Acceptance Criteria**:
  - agent wait-until 能在未来时刻被恢复。
  - delayed continuation 不破坏现有用户 schedule 语义。
- **Validation**:
  - 增加 wait-until -> resume 的集成测试。

## Sprint 5: 切入真实聊天流量

**Goal**: 从 shadow mode 进入小流量真实回复，但保留硬回退。  
**Demo/Validation**:

- `/bb` 或 `@bot` 的一条真实链路由 Agent Runtime 完成。
- 群聊里 active run / supersede / follow-up window 有肉眼可见效果。

### Task 5.1: 让 `/bb` 走 AgentOperator 主链路

- **Location**: `internal/application/lark/messages/ops/chat_op.go`, `internal/application/lark/handlers/chat_handler.go`, `internal/application/lark/agentruntime/operator.go`
- **Description**: 把 `/bb` 或其等价入口桥接到 Agent Runtime，让旧 chat handler 退化为 capability backend 或 fallback。
- **Dependencies**: Sprint 4 complete
- **Acceptance Criteria**:
  - feature flag 可控制新旧链路切换。
  - 至少单轮 tool call + reply 能成功跑通。
- **Validation**:
  - 在测试 chat 中做人工回归，确认 reply、approval、resume 都可工作。

### Task 5.2: 接管 `@bot` 非命令消息

- **Location**: `internal/application/lark/messages/ops/agent_op.go`, `internal/application/lark/messages/ops/chat_op.go`
- **Description**: 让 `@bot` 的自然语言消息进入 Agent Runtime，由 group policy 决定 follow-up / supersede。
- **Dependencies**: Task 5.1
- **Acceptance Criteria**:
  - 普通群消息不会无约束进入 agent runtime。
  - `@bot` 可触发 active run。
  - 新一轮显式提及可 supersede 旧 run。
- **Validation**:
  - 群聊回归测试覆盖 active run、follow-up、supersede 三个场景。

### Task 5.3: 补充用户可见的进度与等待反馈

- **Location**: `internal/application/lark/agentruntime/reply.go`, `internal/infrastructure/lark_dal/larkmsg/*`
- **Description**: 在关键状态下发送或 patch 统一风格的“进行中 / 等待审批 / 已恢复 / 已取消”卡片或文本。
- **Dependencies**: Task 5.1
- **Acceptance Criteria**:
  - 用户能知道当前 run 在做什么、为什么没立刻结束。
  - patch/reply 行为带 run_id trace 关联。
- **Validation**:
  - 人工回归卡片与日志。

## Sprint 6: 记忆、观测与 specialist handoff

**Goal**: 在主链路稳定后，补足真正提升 agent 质量的长期能力。  
**Demo/Validation**:

- runtime 能输出 run 级 trace 和状态。
- memory composer 能把现有 history/retriever 接进来。
- specialist handoff 有统一 contract，但不强制全量落地。

### Task 6.1: MemoryComposer 接入现有 history / retriever / chunking

- **Location**: `internal/application/lark/agentruntime/memory.go`, `internal/application/lark/history/*`, `internal/infrastructure/retriever/*`
- **Description**: 复用现有检索链路，形成 short-term + mid-term + retrieval context 组合器。
- **Dependencies**: Sprint 5 complete
- **Acceptance Criteria**:
  - memory 组合不再散落在旧 chat handler 内。
  - context 大小与来源可观测。
- **Validation**:
  - 组合器单测 + 上下文裁剪测试。

### Task 6.2: 增加 run-level metrics / trace / management status

- **Location**: `internal/application/lark/agentruntime/*`, `internal/runtime/health.go`, `internal/runtime/health_http.go`
- **Description**: 增加 active sessions、waiting runs、resume backlog、step duration 等指标与状态面。
- **Dependencies**: Task 6.1
- **Acceptance Criteria**:
  - `/healthz` 或同级状态面可反映 agent runtime backlog / degradation。
  - trace 中可串起 run -> step -> outbound message。
- **Validation**:
  - metrics/health snapshot 测试 + 手工查看状态输出。

### Task 6.3: 定义 specialist/handoff contract，但先少量接入

- **Location**: `internal/application/lark/agentruntime/handoff.go`
- **Description**: 定义手工可控的 specialist runtime contract，例如 search-heavy、schedule-heavy、doc-heavy specialist，但不立即做复杂多 agent 编排。
- **Dependencies**: Task 6.2
- **Acceptance Criteria**:
  - handoff contract 不破坏单 orchestrator 模型。
  - specialist 只是 capability 选择和上下文裁剪层，不引入新的 run source of truth。
- **Validation**:
  - contract 层单测即可，暂不要求大规模业务接入。

## Suggested Execution Order

推荐顺序：

1. Sprint 1 建抽象。
2. Sprint 2 先接能力，但只 shadow。
3. Sprint 3 把 durable store 和 Redis coordination 做实。
4. Sprint 4 打通 callback/resume/approval。
5. Sprint 5 再切真实聊天流量。
6. Sprint 6 补记忆、观测和 handoff。

适合并行的部分：

- Task 1.2 与 Task 1.3 可并行。
- Task 3.1 与 Task 3.2 可并行。
- Task 4.1 与 Task 4.3 可在 DTO 定义稳定后并行。
- Task 6.1 与 Task 6.2 可并行。

不适合并行的关键路径：

- `RunCoordinator` 必须建立在 DB + Redis contract 稳定后。
- 真实流量切换必须晚于 callback/resume 打通。

## Testing Strategy

- **Unit first**: 先锁定 type、policy、registry、coordinator、Redis helper。
- **Shadow verification**: 在 shadow mode 里验证 trigger 与 state machine，不改用户可见行为。
- **Cross-process simulation**: 至少做一次“callback 由另一个 worker 接收”的恢复测试。
- **Canary rollout**: 只对 `/bb` 和 `@bot` 小流量启用。
- **Card regression**: 对审批卡、等待卡、恢复卡接入现有 card regression/debug 流程。

## Risks & Gotchas

- `xhandler` 当前是并行 operator 模型，Agent Runtime 是单 run orchestrator 模型，两者不能强行揉成一个抽象；需要清晰边界。
- Redis coordination 如果只做锁不做 cancel generation，会留下“旧 worker 继续跑”的隐患。
- callback 如果仍然直接执行业务逻辑，会重新引入“必须同进程”的假设。
- scheduler 的 30s tick 粒度不适合 immediate continuation，所以必须与 Redis resume queue 分工。
- 如果第一阶段就试图接管所有自然群聊消息，噪声和风险都会失控。

## Rollback Plan

- 所有入口都保留 feature flag：`agent_runtime_enabled`、`agent_runtime_shadow_only`、`agent_runtime_chat_cutover`。
- 任一阶段发现 run/callback/resume 语义不稳定，立即回退到旧 `ChatMsgOperator` 与旧 callback path。
- DB/Redis 新增的数据结构应允许“停用但保留”，避免切换时需要 destructive rollback。
