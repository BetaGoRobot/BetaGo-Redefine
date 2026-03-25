# Agent Runtime Abstraction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把当前 agentic runtime 从“入口、状态推进、模型工具循环、输出路由、排队治理混在一起”的实现，重构为边界清晰、可复用、可渐进迁移的运行时骨架。

**Architecture:** 先冻结现有行为，补齐状态投影与协议测试，再抽取三条横切骨架：`TurnLoop`、`OutputRouter`、`ReplyProtocol`。在不改变对外行为的前提下，把 `ContinuationProcessor` 收缩为 runtime driver，把 reply target 推导、模型工具循环、Lark 输出分发逐步迁出。

**Tech Stack:** Go, agentruntime, Lark card/message DAL, Ark stream turn/tool-call loop, Redis coordination store, DB-backed run/step repositories

---

## Assumptions

- 这次目标是“结构性重构计划”，不是功能扩张。
- 对外可观察行为必须保持一致：root card patch/reply 规则、approval 路由、pending queue 提示、resume 语义都不能回退。
- 允许先引入新抽象并让旧实现做适配，不要求一轮内删除旧代码。

## File Map

### Core runtime state
- Modify: `internal/application/lark/agentruntime/types.go`
- Modify: `internal/application/lark/agentruntime/coordinator.go`
- Modify: `internal/application/lark/agentruntime/reply_completion.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor.go`

### Model/tool loop
- Modify: `internal/application/lark/agentruntime/default_chat_generation_executor.go`
- Modify: `internal/application/lark/agentruntime/default_capability_reply_turn_executor.go`
- Modify: `internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go`
- Create: `internal/application/lark/agentruntime/turn_loop.go`
- Create: `internal/application/lark/agentruntime/turn_loop_test.go`

### Reply/output routing
- Modify: `internal/application/lark/agentruntime/reply_emitter.go`
- Modify: `internal/application/lark/agentruntime/initial_reply_executor.go`
- Modify: `internal/application/lark/agentruntime/runtimecutover/runtime_output.go`
- Create: `internal/application/lark/agentruntime/output_router.go`
- Create: `internal/application/lark/agentruntime/output_router_test.go`

### Projection / read model
- Create: `internal/application/lark/agentruntime/run_projection.go`
- Create: `internal/application/lark/agentruntime/run_projection_test.go`

### Runtime driver split
- Create: `internal/application/lark/agentruntime/run_driver.go`
- Create: `internal/application/lark/agentruntime/initial_run_driver.go`
- Create: `internal/application/lark/agentruntime/resume_run_driver.go`
- Modify: `internal/application/lark/agentruntime/runtimewire/runtimewire.go`

### Queue / pending governance
- Modify: `internal/application/lark/agentruntime/initial_run_queue.go`
- Modify: `internal/application/lark/agentruntime/initial_run_worker.go`
- Modify: `internal/application/lark/agentruntime/pending_scope_sweeper.go`

### Contract tests
- Modify: `internal/application/lark/agentruntime/planner_test.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor_test.go`
- Modify: `internal/application/lark/agentruntime/reply_emitter_test.go`
- Modify: `internal/application/lark/agentruntime/initial_reply_executor_test.go`
- Modify: `internal/infrastructure/lark_dal/larkmsg/streaming_agentic_test.go`

---

## Sprint 1: Freeze Runtime Semantics

**Goal:** 先把现有 runtime 的核心协议变成明确测试，避免后续抽象时改出行为漂移。

**Demo/Validation:**
- 能列出并跑通 root reply、approval、capability continuation、pending queue 的关键行为测试。
- 现有 runtime 在重构前后，对外行为快照一致。

### Task 1.1: 补 root model reply 路由契约测试
- **Location**: `internal/application/lark/agentruntime/continuation_processor_test.go`, `internal/application/lark/agentruntime/reply_emitter_test.go`
- **Description**: 固化下列行为：
  - model reply 优先 patch root agentic card
  - 无 root card 时 reply 当前消息
  - side effect 输出不允许 patch root model card
- **Dependencies**: 无
- **Acceptance Criteria**:
  - 覆盖 `create/reply/patch` 三种 delivery mode
  - 覆盖 `root target`、`latest target`、`superseded target` 三种 target 推导
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Reply.*'`

### Task 1.2: 补 capability 生命周期测试
- **Location**: `internal/application/lark/agentruntime/continuation_processor_test.go`, `internal/application/lark/agentruntime/reply_completion_internal_test.go`
- **Description**: 固化 `capability_call -> observe -> plan -> reply -> complete/continue` 协议，以及 `queue tail`、`pending capability`、`previous_response_id` 保留规则。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - 有覆盖 queued tail 串行推进
  - 有覆盖 capability 后 reply planner fallback
  - 有覆盖 continuation turn 再次产生 pending capability
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Capability.*|Test.*QueuedReplyPlan.*'`

### Task 1.3: 补 approval / resume 契约测试
- **Location**: `internal/application/lark/agentruntime/coordinator_test.go`, `internal/application/lark/agentruntime/approval_test.go`, `internal/application/lark/agentruntime/approval_reject_test.go`
- **Description**: 固化 `running -> waiting_approval -> queued -> resume` 路径，以及 reservation/reject 的分支。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - 覆盖 token mismatch、expired、reservation approved/rejected
  - 覆盖 resume 后 capability replay 路径
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Approval.*|Test.*Resume.*'`

### Task 1.4: 补 pending initial queue 契约测试
- **Location**: `internal/application/lark/agentruntime/initial_run_worker_test.go`, `internal/application/lark/agentruntime/pending_scope_sweeper_test.go`, `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`
- **Description**: 固化 “slot 被占用时先发 pending 卡，再入队；slot 释放后自动开始；队列满时 patch 提示”。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - 覆盖 lock skip、requeue、scope clear、retry notify
  - 覆盖 root target 在 pending 期间保留
- **Validation**:
  - `go test ./internal/application/lark/agentruntime/... -run 'Test.*Pending.*|Test.*Queue.*'`

---

## Sprint 2: 抽 RunProjection 只读层

**Goal:** 先把“扫描 step log 推导当前视图”的逻辑从执行器里拿出来，降低 `ContinuationProcessor` 体积与认知负担。

**Demo/Validation:**
- `ContinuationProcessor` 不再自己扫描 step list 推导 root/latest reply target。
- 所有 target / previous-step / replayable-capability 推导都通过 projection 完成。

### Task 2.1: 定义 projection 接口与只读结果对象
- **Location**: `internal/application/lark/agentruntime/run_projection.go`
- **Description**: 定义 `RunProjection`，至少包含：
  - `CurrentStep`
  - `PreviousStep`
  - `RootReplyTarget`
  - `LatestReplyTarget`
  - `LatestModelReplyTarget`
  - `ReplayableCapabilityStep`
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - 只依赖 `AgentRun + []AgentStep`
  - 不产生副作用
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'TestRunProjection'`

### Task 2.2: 迁移 target 推导逻辑
- **Location**: `internal/application/lark/agentruntime/continuation_processor.go`, `internal/application/lark/agentruntime/run_projection.go`
- **Description**: 把 `resolveRootReplyTarget`、`resolveReplyTarget`、`resolveLatestModelReplyTarget` 背后的扫描逻辑迁到 projection。
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - `ContinuationProcessor` 不再直接遍历 step slice 做 target 推导
  - superseded reply 过滤规则保持不变
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*ReplyTarget.*|TestRunProjection'`

### Task 2.3: 迁移 continuation 上下文推导逻辑
- **Location**: `internal/application/lark/agentruntime/continuation_processor.go`, `internal/application/lark/agentruntime/run_projection.go`
- **Description**: 把 previous step title、latest reply target、replayable capability step 的推导迁出。
- **Dependencies**: Task 2.2
- **Acceptance Criteria**:
  - `buildContinuationContext` 仅消费 projection，不做原始扫描
  - approval/wait 标题提取规则不变
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Continuation.*|TestRunProjection'`

---

## Sprint 3: 抽统一 TurnLoop

**Goal:** 合并首轮聊天、capability continuation、resume continuation 三套同构的模型工具循环。

**Demo/Validation:**
- 三条路径都通过统一 `TurnLoop` 驱动。
- prompt/context 差异留在 strategy 层，循环本身只保留一份。

### Task 3.1: 定义 TurnLoop 协议对象
- **Location**: `internal/application/lark/agentruntime/turn_loop.go`
- **Description**: 定义：
  - `TurnLoopRequest`
  - `TurnLoopSeed`
  - `TurnLoopStrategy`
  - `TurnLoopResult`
  - completed/pending capability capture contract
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - 能表达首轮 prompt 驱动
  - 能表达 previous response + tool output 驱动
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'TestTurnLoop'`

### Task 3.2: 把首轮聊天切到 TurnLoop
- **Location**: `internal/application/lark/agentruntime/default_chat_generation_executor.go`, `internal/application/lark/agentruntime/initial_reply_executor.go`, `internal/application/lark/agentruntime/turn_loop.go`
- **Description**: 让 standard/agentic initial chat 通过统一 `TurnLoop` 执行，保留当前 `planBuilder/finalizer/toolTurns` 变体。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - `generateChatPlanWithVariant` 不再手写 turn for-loop
  - deferred collector / tool description decoration 行为不变
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Initial.*|TestTurnLoop'`

### Task 3.3: 把 capability reply turn 切到 TurnLoop
- **Location**: `internal/application/lark/agentruntime/default_capability_reply_turn_executor.go`, `internal/application/lark/agentruntime/turn_loop.go`
- **Description**: 用统一 loop 替代当前 continuation previous-response replay 实现。
- **Dependencies**: Task 3.2
- **Acceptance Criteria**:
  - `previous_response_id` 和 `call_id` 绑定保持不变
  - pending capability queue tail 合并逻辑保持不变
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*CapabilityReplyTurn.*|TestTurnLoop'`

### Task 3.4: 把 continuation reply turn 切到 TurnLoop
- **Location**: `internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go`, `internal/application/lark/agentruntime/turn_loop.go`
- **Description**: 统一恢复阶段的模型工具循环，只保留 prompt builder 和 request builder 差异。
- **Dependencies**: Task 3.3
- **Acceptance Criteria**:
  - research tool turn limit 保持原逻辑
  - fallback reply/thought 行为保持不变
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*ContinuationReplyTurn.*|TestTurnLoop'`

---

## Sprint 4: 抽统一 OutputRouter

**Goal:** 合并 initial emitter 与 continuation reply emitter，统一 agentic/standard 的 `create/reply/patch` 路由。

**Demo/Validation:**
- initial path 和 durable path 都复用一套输出路由。
- root card patch / thread reply / standard text patch 的路由决策只有一份。

### Task 4.1: 定义 OutputRouter 抽象
- **Location**: `internal/application/lark/agentruntime/output_router.go`
- **Description**: 抽象统一输入：
  - output kind
  - payload(thought/reply)
  - target(message/card/thread)
  - delivery fallback policy
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 能覆盖 initial output 和 post-run output
  - 不关心 run lifecycle，只关心传输
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'TestOutputRouter'`

### Task 4.2: 让 `reply_emitter.go` 变成 router adapter
- **Location**: `internal/application/lark/agentruntime/reply_emitter.go`, `internal/application/lark/agentruntime/output_router.go`
- **Description**: 把 `LarkReplyEmitter` 收缩为基于 router 的薄适配层。
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - `EmitReply` 不再重复编码 agentic/standard 三路分发
  - runtimecontext active target 更新保持原语义
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*ReplyEmitter.*|TestOutputRouter'`

### Task 4.3: 让 `runtime_output.go` 复用同一路由器
- **Location**: `internal/application/lark/agentruntime/runtimecutover/runtime_output.go`, `internal/application/lark/agentruntime/output_router.go`
- **Description**: 去掉 initial path 的重复 patch/reply/create 分支。
- **Dependencies**: Task 4.2
- **Acceptance Criteria**:
  - `replyOrchestrator.emit` 仅保留 capture/snapshot 与 initial result mapping
  - 路由决策代码从双份收敛为单份
- **Validation**:
  - `go test ./internal/application/lark/agentruntime/runtimecutover -run 'Test.*RuntimeOutput.*'`

---

## Sprint 5: 抽 ReplyProtocol

**Goal:** 统一 “plan -> emit -> append reply step -> complete/continue run” 这一条 reply 完成协议。

**Demo/Validation:**
- initial run、capability run、resume continuation 都通过同一协议服务完成 reply 收尾。
- `RunCoordinator` 继续持有 durable mutation，但 protocol glue 只保留一份。

### Task 5.1: 定义 ReplyProtocol service
- **Location**: `internal/application/lark/agentruntime/reply_protocol.go`
- **Description**: 抽出统一方法：
  - `QueuePlan`
  - `EmitModelReply`
  - `CompleteWithReply`
  - `ContinueWithReply`
- **Dependencies**: Sprint 4
- **Acceptance Criteria**:
  - 输入同时支持 completed capability calls 和 pending capability
  - 输出保留 target step link 信息
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*ReplyProtocol.*'`

### Task 5.2: capability / continuation 改走 ReplyProtocol
- **Location**: `internal/application/lark/agentruntime/continuation_processor.go`, `internal/application/lark/agentruntime/reply_protocol.go`
- **Description**: 用协议服务替代当前 `queueReplyPlanStep + emit*Reply + completeQueuedReplyPlan + continueQueuedReplyPlan` 拼装代码。
- **Dependencies**: Task 5.1
- **Acceptance Criteria**:
  - capability 和 continuation 不再各自手工 glue reply completion
  - 回复 supersede link 逻辑仍保留
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*QueuedReplyPlan.*|Test.*Continuation.*|Test.*Capability.*'`

### Task 5.3: initial path 接入 ReplyProtocol
- **Location**: `internal/application/lark/agentruntime/initial_reply_executor.go`, `internal/application/lark/agentruntime/run_processor.go`, `internal/application/lark/agentruntime/reply_protocol.go`
- **Description**: 让首轮 reply 也通过同一套 protocol 完成 durable 收尾。
- **Dependencies**: Task 5.2
- **Acceptance Criteria**:
  - initial / continuation 共享同一 reply completion 协议
  - initial path 仍能记录 immediate pending approval / capability trace
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*InitialReply.*|Test.*ReplyProtocol.*'`

---

## Sprint 6: 拆 Runtime Driver

**Goal:** 把当前臃肿的 `ContinuationProcessor` 拆成更小的 driver / executor / ports 组合。

**Demo/Validation:**
- `ContinuationProcessor` 退化为兼容 façade，核心执行职责迁入新 driver。
- 初始 run 和 resume run 入口独立，但共享底层 service。

### Task 6.1: 定义 driver 边界
- **Location**: `internal/application/lark/agentruntime/run_driver.go`, `internal/application/lark/agentruntime/initial_run_driver.go`, `internal/application/lark/agentruntime/resume_run_driver.go`
- **Description**: 抽出：
  - `InitialRunDriver`
  - `ResumeRunDriver`
  - `CapabilityStepExecutor`
  - `RuntimeSideEffectPorts`
- **Dependencies**: Sprint 5
- **Acceptance Criteria**:
  - 一个 driver 只关心一类入口
  - approval sender / reply router / projection / coordinator 通过 ports 注入
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*RunDriver.*'`

### Task 6.2: 迁移 initial run 流程
- **Location**: `internal/application/lark/agentruntime/run_processor.go`, `internal/application/lark/agentruntime/initial_run_driver.go`
- **Description**: 把 `processInitialRun` 迁入 initial driver，保留旧接口做 façade。
- **Dependencies**: Task 6.1
- **Acceptance Criteria**:
  - `RunProcessorInput{Initial: ...}` 行为不变
  - execution lease / root target / pending approval trace 保持原有时序
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*InitialRun.*'`

### Task 6.3: 迁移 resume 流程
- **Location**: `internal/application/lark/agentruntime/continuation_processor.go`, `internal/application/lark/agentruntime/resume_run_driver.go`
- **Description**: 把 `ProcessResume` 的主链路迁入 resume driver。
- **Dependencies**: Task 6.2
- **Acceptance Criteria**:
  - `ContinuationProcessor` 只做兼容入口与依赖组装
  - lease / approval defer / replayable capability 行为不变
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Resume.*|Test.*Continuation.*'`

### Task 6.4: runtimewire 切到新装配图
- **Location**: `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- **Description**: 用新 driver/service/ports 装配 runtime，而不是直接 new 一个“全能 ContinuationProcessor”。
- **Dependencies**: Task 6.3
- **Acceptance Criteria**:
  - 依赖图变得可读：coordinator / projection / turn loop / output router / protocol / drivers
  - 不影响 worker、dispatcher、cutover handler 的调用面
- **Validation**:
  - `go test ./internal/application/lark/agentruntime/...`

---

## Sprint 7: 清理 Queue / Governance 边界

**Goal:** 让 pending queue FSM 和 durable run FSM 边界更清楚，避免“排队态”和“run waiting 态”继续串味。

**Demo/Validation:**
- pending initial 阶段只负责“未开始的请求”。
- 一旦 durable run 创建成功，排队系统不再承担 run 语义。

### Task 7.1: 明确 pending item 的最小职责
- **Location**: `internal/application/lark/agentruntime/initial_run_queue.go`
- **Description**: 文档化并收紧 `PendingInitialRun` 的字段，明确只保存启动所需最小上下文和 root target。
- **Dependencies**: Sprint 6
- **Acceptance Criteria**:
  - 不把 durable lifecycle 字段泄漏到 pending item
  - root target 保留逻辑仍可用
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*PendingInitial.*'`

### Task 7.2: worker/sweeper 改成纯 queue 组件
- **Location**: `internal/application/lark/agentruntime/initial_run_worker.go`, `internal/application/lark/agentruntime/pending_scope_sweeper.go`
- **Description**: 保证 worker/sweeper 不依赖 durable run 内部语义，只依赖 slot occupancy 与 queue payload。
- **Dependencies**: Task 7.1
- **Acceptance Criteria**:
  - queue worker 不推导 reply target，不推导 run step
  - sweeper 只关心 backlog 与 execution lease 数
- **Validation**:
  - `go test ./internal/application/lark/agentruntime -run 'Test.*Pending.*|Test.*Sweep.*'`

---

## Testing Strategy

- 先补协议测试，再抽象实现，避免边改边猜。
- 每个 Sprint 至少保留一组“行为不变”回归测试：
  - root reply patch/reply 规则
  - approval request / reject / resume 规则
  - capability queue tail / continuation turn 规则
  - pending queue enqueue / patch / auto-start 规则
- 每个新抽象都要有独立单测：
  - `RunProjection`
  - `TurnLoop`
  - `OutputRouter`
  - `ReplyProtocol`
  - `RunDriver`
- Sprint 4 之后，每轮至少跑：
  - `go test ./internal/application/lark/agentruntime/...`
  - `go test ./internal/application/lark/agentruntime/runtimecutover/...`
  - `go test ./internal/infrastructure/lark_dal/larkmsg/...`

## Parallelization Plan

- 可并行 A: Sprint 1 的 reply/approval/pending 三组测试补强
- 可并行 B: Sprint 2 的 projection 抽取 与 Sprint 3 的 TurnLoop 设计
- 可并行 C: Sprint 4 的 OutputRouter 与 Sprint 5 的 ReplyProtocol 草拟
- 串行关键路径:
  - Sprint 1 -> Sprint 2 -> Sprint 4 -> Sprint 5 -> Sprint 6
  - 因为 reply target / output routing / reply completion 是后续 driver 拆分的前置依赖

## Potential Risks & Gotchas

- 最大风险不是编译错误，而是 reply target 语义悄悄变掉。
- `initial` 与 `continuation` 的“半统一”状态很危险，重构时很容易保留双实现。
- `OutputRouter` 如果抽象过头，可能把业务层的 model reply / side effect 区分抹平。
- `TurnLoop` 如果只做语法统一，不明确 previous-response/tool-output contract，会变成更隐蔽的重复。
- approval reservation 和 replayable capability 是最容易在 driver 拆分时漏掉的边角逻辑。

## Rollback Plan

- 每个 Sprint 独立提交，禁止跨 Sprint 大批量混改。
- 新抽象先通过 adapter 接入，保留旧入口 façade，确认行为稳定后再删旧实现。
- 若某个 Sprint 引入语义漂移：
  - 回滚到上一个 Sprint 的提交
  - 保留新增测试
  - 在原行为基础上重新调整抽象边界

