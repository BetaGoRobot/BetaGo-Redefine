# Agent Runtime Progress

持续记录 `docs/architecture/agent-runtime-design.md` 与 `docs/architecture/agent-runtime-plan.md` 的落地结果，避免“设计在文档里、实现散落在提交里”。

## 2026-03-25 · Milestone X · Run Liveness / Stale Repair / Pending Expiry

### 目标

- 把之前“前序 run 卡死时排队链没有自愈”的架构缺口补成真实运行机制。
- 让 `heartbeat_at / lease_expires_at` 不再只是 schema 字段，而是能被创建、续约、扫描和修复路径消费。
- 给 pending initial queue 增加最保守可用的最大等待时间兜底，避免无限排队。

### 已落地的运行语义

- `AgentRun` 现在正式承载 run liveness 字段：
  - `worker_id`
  - `heartbeat_at`
  - `lease_expires_at`
  - `repair_attempts`
- `queued` run 在以下路径都会至少写入一次 liveness：
  - `StartShadowRun`
  - attach 回到 `queued`
  - `ResumeRun`
  - continuation 再次把 run 推回 `queued`
- 真正进入执行临界区后：
  - `startRunExecution(...)` 会补上 `worker_id`
  - `renewRunExecutionHeartbeat(...)` 会周期续约 run heartbeat
- 进入 `waiting_*` 或终态时，会清掉旧 liveness，避免把“暂停/已结束”误当成仍在执行。

### Stale Run Repair

- 新增后台 [`StaleRunSweeper`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/stale_run_sweeper.go)
  - 周期扫描 stale `queued/running` run
  - 判定优先用 `lease_expires_at`
  - 老数据没有 lease 时回退到 `updated_at` cutoff
- repair 通过 [`RunCoordinator.RepairStaleRun`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/stale_run_sweeper.go) 完成
  - `queued/running -> failed`
  - `error_text = stale_run_timeout`
  - 清 `worker_id / heartbeat_at / lease_expires_at`
  - 清 session active / actor slot
  - `NotifyPendingInitialRun(...)` 唤醒后续 scope
- 当前仍然刻意不自动处理：
  - `waiting_approval`
  - `waiting_schedule`
  - `waiting_callback`

### Pending Queue 治理

- pending worker 现在会检查 `PendingRun.RequestedAt`
- 仅当该字段存在，且排队超过 `48h` 时：
  - patch 原排队卡片为“排队超时”
  - 停止继续排队
  - 让 scope 清理链自然收尾
- `ErrRunSlotOccupied` 仍然保留为 retryable，不会因为这次治理把正常短时拥塞误判为失败。

### 文档修正点

- [`ARCHITECTURE.md`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/ARCHITECTURE.md)
  - 第 25 节已从“缺口审计”改成“当前自愈设计”
  - 补充 `run_liveness.go` / `stale_run_sweeper.go` 到模块图、分层表和阅读顺序
- 这轮文档明确了当前边界：
  - execution lease 负责并发互斥
  - run heartbeat + stale sweeper 负责 DB active run 自愈
  - pending expiry 只是 fallback，不是完整的队列 SLA

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOMODCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gomodcache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime/...`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOMODCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gomodcache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./cmd/larkrobot ./internal/infrastructure/agentstore`

## 2026-03-24 · Milestone W · Agent Runtime 入口收口与状态机治理

### 目标

- 优先做 agent runtime 的架构治理，而不是继续在碎文件和局部状态机里补洞。
- 把当前实现收口成少数几个稳定入口文件，让后续修改时能先看入口，再顺着 lane 往下读。
- 去掉测试里靠 package-level `default_xx` / `SetXxx(...)` 替换的“函数锚点”做法，改为显式依赖注入。

### 当前骨架

当前 `internal/application/lark/agentruntime` 的核心入口已经进一步收口到下列 4 个主文件：

- [initial.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial.go)
  - initial lane 的 plan builder
  - initial turn request/result
  - initial turn 执行与 stream finalizer
  - initial reply executor / emitter
  - initial pending approval dispatch
  - initial capability trace record
  - pending initial queue item、marshal/unmarshal、worker 消费
- [reply_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/reply_turn.go)
  - default runtime executor deps
  - default chat generation entry
  - capability / continuation reply turn 请求类型
  - capability / continuation reply turn 默认执行器
  - runtime tool decoration 与 reply-turn runtime 装配
  - shared initial/reply turn loop
- [run_projection.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_projection.go)
  - run + steps 的只读投影
  - current/previous step
  - root/latest/latest-model reply target
  - replayable capability step
  - continuation context
### 现在的 4 条 Lane

1. `initial lane`
   - `InitialRunInput -> BuildExecutor -> InitialReplyExecutor`
   - 负责首轮模型 turn、首轮工具循环、首轮消息发送/patch/reply
2. `pending-initial queue lane`
   - `PendingInitialRun -> PendingInitialRunWorker`
   - 负责 slot 被占用时的排队、重试、root target 保留
3. `reply-turn lane`
   - `CapabilityReplyTurnExecutor / ContinuationReplyTurnExecutor -> ExecuteReplyTurnLoop`
   - 负责 capability 后续写和 resume 后续写
4. `continuation projection lane`
   - `RunProjection`
   - 负责把 durable step log 转成执行器需要的只读上下文

### 已完成的结构治理

- 新增 [run_projection.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_projection.go)
  - 把 step 扫描读逻辑从 `ContinuationProcessor` 里迁出。
- 新增 [turn_loop.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/turn_loop.go)
  - 把 initial/reply turn 的同构 loop 收成共享骨架。
- 收口默认执行器到 [default_reply_turn_executors.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_reply_turn_executors.go)
  - 删除旧的：
    - `default_capability_reply_turn_executor.go`
    - `default_chat_generation_executor.go`
    - `default_continuation_reply_turn_executor.go`
- 把薄文件并回主入口：
  - 并回 [run_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor.go)
    - `initial_reply_target.go`
    - `initial_run_ownership.go`
  - 并回 [initial_reply_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor.go)
    - `initial_capability_trace_recorder.go`
    - `initial_pending_approval_dispatcher.go`
    - `initial_reply_lark.go`
  - 并回 [initial_run_worker.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_run_worker.go)
    - `initial_run_queue.go`
  - 并回 [initial_chat_generation.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_chat_generation.go)
    - `initial_chat_turn.go`
  - 并回 [default_reply_turn_executors.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_reply_turn_executors.go)
    - `capability_reply_turn.go`
    - `continuation_reply_turn.go`
- 二次聚合 lane 主入口：
  - 收口到 [initial.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial.go)
    - `initial_chat_generation.go`
    - `initial_reply_executor.go`
    - `initial_run_worker.go`
  - 收口到 [reply_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/reply_turn.go)
    - `default_reply_turn_executors.go`
    - `turn_loop.go`

### 已完成的测试治理

- default / initial executor 测试已从“替换包级默认函数”改为显式依赖注入：
  - [default_chat_generation_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor_test.go)
  - [initial_reply_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor_test.go)
  - [default_capability_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor_test.go)
  - [default_continuation_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor_test.go)
- `run_processor` 增加 `InitialReplyExecutorFactory` 注入点：
  - [run_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor.go)
  - [run_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor_test.go)
  - 不再通过 `SetAgenticInitialReplyStreamGenerator(...)` 篡改全局默认流生成器。

### 当前高层抽象复用方案

当前代码已经适合围绕以下抽象继续复用，而不是再长出新的 `default_xx` / `initial_xx` 文件：

- `RunProjection`
  - 统一读取 durable state
  - 后续所有 reply target / previous step / replayable capability 推导优先放这里
- `reply_turn.go` 内的 turn loop 骨架
  - 统一模型 turn + tool turn 的循环骨架
  - 后续新增 lane 优先复用 loop，而不是复制 `for turn := 0; ...`
- `defaultRuntimeExecutorDeps`
  - 统一默认执行器的依赖装配
  - 测试和 runtime wiring 都优先走 deps / factory 注入
- `InitialReplyExecutorFactory`
  - 统一首轮 executor 的测试替身和后续可插拔实现
  - 避免再出现测试通过全局函数替换来控制行为

### 后续治理约束

- 不再新增纯壳文件来放：
  - 单个 request/result struct
  - 单个 option helper
  - 单个 default executor 包装
- 新逻辑优先落到对应 lane 的主入口文件：
  - initial / pending-initial queue -> `initial.go`
  - reply turn / shared loop -> `reply_turn.go`
  - projection -> `run_projection.go`
  - 其他 runtime 汇总入口 -> `run_processor.go`
- 若未来 `initial.go` 或 `reply_turn.go` 再继续增大，优先做“文件内分层整理”：
  - 先加清晰 section 和 lane 内部抽象
  - 不优先重新拆回多个 `default_*` / `initial_*` 文件

### 验证

- `env GOCACHE=/tmp/go-build-cache BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime/... ./internal/application/lark/agentruntime/runtimecutover/... -run 'TestLarkInitialReplyEmitter.*|TestPendingInitialRunWorker.*|TestDefaultChatGenerationPlanExecutor.*|TestDefaultInitialReplyExecutor.*|TestGenerateAgenticInitialReplyStream.*|TestRunProjection|Test.*Reply.*|Test.*Approval.*|Test.*Resume.*|Test.*Pending.*|Test.*Queue.*|Test.*Continuation.*|Test.*CapabilityReplyTurn.*|TestContinuationProcessorProcessRun.*|TestExecuteInitialChatTurn.*|TestBuildInitialChatExecutionPlan.*'`

## 2026-03-20 · Milestone V · Chat / Agentic 前门硬分流

### 方案

- 按最新 cutover 约束，把 standard chat 和 agentic chat 的分流前移到真正入口：
  - 不再共享一个带 mode 字段的 `chatHandler`
  - 不再共享一个同时挂 standard / agentic op 的 message processor
  - 不再在 chat op / reply chat op 里靠 mode guard 跳过另一条链
- 这轮只保留一层前置路由：
  - `messages.MessageHandler.Run(...)` 读一次 `chat_mode`
  - 然后直接进入 `standard processor` 或 `agentic processor`
  - 进入 processor 后不再做 standard / agentic 判定

### 修改

- 修改 [chat_handler.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/chat_handler.go)
  - `handlers.Chat` / `handlers.AgenticChat` 改成两个独立 handler
  - 标准 handler 直接走 `runStandardChat(...)`
  - agentic handler 直接走 `runAgenticChat(...)`
- 修改 [command.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/command/command.go)
  - 新增 `AgenticLarkRootCommand`
  - `bb` 分别绑定到 standard / agentic chat handler
- 修改 [handler.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/handler.go)
  - `NewMessageProcessor(...)` 现在返回一个前置 message router
  - router 内部分别持有：
    - standard processor
    - agentic processor
- 修改 [command_op.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/ops/command_op.go)
  - 拆成：
    - `StandardCommandOperator`
    - `AgenticCommandOperator`
  - agentic `/bb` 在 command 入口补 runtime ownership，再走 agentic root command
- 修改：
  - [chat_op.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/ops/chat_op.go)
  - [reply_chat_op.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/ops/reply_chat_op.go)
  - [agentic_chat_op.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/ops/agentic_chat_op.go)
  - [agentic_reply_chat_op.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/ops/agentic_reply_chat_op.go)
  - 移除 mode guard，避免 op 内再区分 standard / agentic
- 修改 [handler.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/lark/handler.go)
  - 传输层入口不再依赖单个 `*xhandler.Processor`
  - message 入口直接接受可运行的 message router

### 决策

- 当前边界调整成：
  - handler 层分 chat / agentic handler
  - message 入口分 standard / agentic processor
  - command 入口分 standard / agentic root command
- 共用的只保留最底层基础设施和少量命令解析能力，不再共用聊天主控制流。

### 验证

- `printf '' >/tmp/betago-empty.toml && GOCACHE=/tmp/go-build-cache GOTESTCACHE=/tmp/go-test-cache BETAGO_CONFIG_PATH=/tmp/betago-empty.toml go test ./internal/application/lark/command ./internal/application/lark/handlers ./internal/application/lark/messages ./internal/application/lark/messages/ops ./internal/interfaces/lark ./internal/infrastructure/lark_dal/larkmsg -run '^$'`

## 2026-03-20 · Milestone T · Continuation / Capability Tool Turn 也 Durable 成 Plan Step

### 方案

- 把 continuation / capability reply turn 里的中间 model turn 从“只在内存里出现一下”推进到真正 durable：
  - 以前只有最终收尾那条 `plan` 会落库
  - 如果某一轮先规划了一个 tool call，再继续下一轮
  - 这条中间规划不会留下独立 step
- 本轮改成：
  - tool turn 一旦产出 `function call`
  - 先 durable 写入一条 `StepKindPlan(status=completed)`
  - plan 里记录：
    - `thought_text`
    - `reply_text`
    - `pending_capability(call_id / capability_name / arguments)`
  - 然后再继续执行 capability / approval queue

### 修改

- 修改 [initial_capability_trace_recorder.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_capability_trace_recorder.go)
  - 新增 `ReplyTurnPlanRecorder`
  - 让现有 run-scoped recorder 同时负责：
    - completed capability trace
    - intermediate reply-turn plan durable
  - plan / capability / observe 共用同一个 `nextIndex` 和锁，避免 step index 撞车
- 修改：
  - [capability_reply_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/capability_reply_turn.go)
  - [continuation_reply_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_reply_turn.go)
  - request 新增 `PlanRecorder`
- 修改：
  - [default_capability_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor.go)
  - [default_continuation_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go)
  - 在每次 `snapshot.ToolCall != nil` 时，先记录 intermediate `plan`，再执行 tool call
- 修改 [continuation_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor.go)
  - continuation / capability 两条链现在都会把同一个 run recorder 同时注入：
    - `Recorder`
    - `PlanRecorder`

### 测试

- 新增/增强测试：
  - [default_capability_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor_test.go)
  - [default_continuation_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor_test.go)
  - [continuation_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor_test.go)
- 覆盖：
  - capability reply turn 会记录中间 `plan`
  - continuation reply turn 会记录中间 `plan`
  - processor 注入的 reply-turn recorder 同时具备 trace / plan 两种能力

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'TestDefault(Capability|Continuation)ReplyTurnExecutorRecordsIntermediatePlanTurn|TestContinuationProcessor(CompletesQueuedCapabilityRunWithDurablyRecordedCapabilityTraceAndReplyTurnExecutor|QueuesPendingCapabilityAfterDurablyRecordingContinuationNestedCapabilityTrace)'`

## 2026-03-20 · Milestone U · 审批卡优先按 Actor 定向投递

### 方案

- 修正当前 approval delivery 对群聊的打扰边界：
  - 以前 approval card 默认优先 reply 到群里的 trigger message
  - 对 runtime 来说，这会把本来只和发起人相关的审批卡刷到群里
- 本轮先把 sender target 收口成明确语义：
  - `VisibleOpenID`
  - sender 优先级变成：
    - `VisibleOpenID`
    - `ReplyToMessageID`
    - `ChatID`
- 当前落地方式是：
  - sender 优先按 `open_id` 定向发卡
  - 先把“只打扰相关人”这件事做对
  - 若 actor 定向发卡失败，会回退到原 reply / chat 路径，避免审批直接丢失
  - 后续若切成真正的群内临时可见卡，只需要替换 sender 底层实现

### 修改

- 修改 [approval_sender.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/approval_sender.go)
  - `ApprovalCardTarget` 新增 `VisibleOpenID`
  - `LarkApprovalSender` 改为基于 `CreateCardJSONByReceiveID(...)`
  - actor visible target 存在时，优先按 `open_id` 发卡
- 修改 [continuation_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor.go)
  - request approval 时把 `run.ActorOpenID` 写入 target

### 测试

- 新增/增强测试：
  - [approval_sender_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/approval_sender_test.go)
  - [continuation_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor_test.go)
- 覆盖：
  - actor visible target 存在时，sender 不再走群 reply
  - actor visible target 发送失败时，会回退到 reply 路径
  - runtime 发起 approval 时会把 actor open_id 传给 sender

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'TestLarkApprovalSender(Create|Replies|FallsBack)|TestContinuationProcessorRequestsApprovalAndSendsApprovalCardForQueuedCapability'`

## 2026-03-20 · Milestone R · 模型侧明确 Pending / Approval 触发语义

### 方案

- 修正一个直接影响 agentic 体感的问题：
  - runtime 已经支持很多 side-effect tool 的 deferred approval
  - 但模型看到的 prompt 与 tool description 里，没有明确写出：
    - 哪些请求应优先调用只读工具
    - 哪些动作不能口头说“已完成”，而必须先进入 approval / waiting
- 结果就是模型容易：
  - 对本该走工具的事实类请求直接口头回答
  - 对本该走审批的副作用动作直接一轮收尾

### 修改

- 修改 [agentic_chat_generation.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/agentic_chat_generation.go)
  - agentic system prompt 现在显式要求：
    - 实时数据、行情、历史检索、资料查找优先调读工具
    - 发送消息、发卡、改配置、增删素材、schedule/todo、权限操作等副作用动作必须走 tool，让 runtime 进入审批或等待流程
- 修改 [default_chat_generation_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor.go)
  - 新增 model-facing tool description decoration
  - `requires_approval` tool 会在 description 中明确标出：
    - 先进入审批等待
    - 不会立刻执行
    - 仅在用户明确要求动作时使用
  - 只读查询 tool 会在 description 中明确标出：
    - 不会修改群聊、配置或共享状态
    - 事实类请求应优先使用
- 同步修改：
  - [default_continuation_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go)
  - [default_capability_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor.go)
  - [initial_reply_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor.go)
  - 保证初始 turn、continuation turn、capability continuation turn 都使用同一套 runtime-aware tool schema

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/agentruntime/runtimewire ./internal/infrastructure/lark_dal/larkmsg ./internal/infrastructure/agentstore`

## 2026-03-20 · Milestone Q · Multi-Pending Capability Queue 串起来

### 方案

- 修正当前 agentic multi-turn loop 里的一个实际断点：
  - 模型在一轮 pending capability 之后，如果下一轮继续规划新的 pending capability
  - 默认 executor 以前会因为 `awaitingPostPendingReply` 提前中断
  - 结果就是：
    - 只能吃到第一条 pending capability
    - 后续 pending 不会进入 runtime queue
    - agentic 的“连续规划多个待执行动作”会直接断掉
- 本轮把这条链统一改成：
  - 第一条 pending 作为 queue root
  - 后续 pending 进入 `Input.QueueTail`
  - continuation processor 再按已有 queue tail 逻辑串行推进

### 修改

- 修改 [default_continuation_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go)
  - 去掉 pending 后“下一轮直接中断”的旧限制
  - 多个 pending capability 现在会聚合成：
    - `PendingCapability`
    - `PendingCapability.Input.QueueTail`
- 修改 [default_capability_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor.go)
  - 同样支持 multi-pending queue accumulation
  - 新增 `appendQueuedPendingCapability(...)`
- 修改 [default_chat_generation_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor.go)
  - 首轮通用 chat tool loop 不再在第一条 pending 后中断
  - 因此旧 standard/compat path 也能继续把后续 pending trace 流出来
- 修改 [initial_reply_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor.go)
  - agentic 初始 reply stream 同样支持多条 pending trace 连续产出

### 测试

- 新增/补齐测试：
  - [default_continuation_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor_test.go)
  - [default_capability_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor_test.go)
  - [default_chat_generation_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor_test.go)
  - [initial_reply_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor_test.go)
- 覆盖：
  - continuation reply turn 能把两个 pending capability 串成 root + queue tail
  - capability reply turn 能把两个 pending capability 串成 root + queue tail
  - standard/default chat tool loop 能连续流出两条 pending trace 再收尾
  - agentic initial tool loop 能连续流出两条 pending trace 再收尾

### 决策

- 这一步仍然坚持当前 runtime 的边界：
  - 每个 model turn 只接受一个 function call
  - 多能力不是单 turn 并发，而是跨 turn 串成 durable queue
- 当前 queue 语义已经从“只能有一个 pending capability”推进到：
  - “首个 pending 为 root”
  - “其余 pending 挂到 queue tail”
  - “由 continuation processor 串行消费”

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'Test(DefaultContinuationReplyTurnExecutorChainsMultiplePendingCapabilities|DefaultCapabilityReplyTurnExecutorChainsMultiplePendingCapabilities|DefaultChatGenerationPlanExecutorChainsMultiplePendingCapabilities|GenerateAgenticInitialReplyStreamChainsMultiplePendingCapabilities)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/infrastructure/lark_dal/larkmsg ./internal/infrastructure/agentstore ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimewire ./internal/application/lark/agentruntime/runtimecutover`

## 2026-03-20 · Milestone P · 首轮 Plan Durable 化并接入 Reply-Scoped Context

### 方案

- 把“reply 主要看被回复消息及其父消息的 plan”从近似实现推进到真实 durable 语义：
  - 首轮 initial run 在 `decide` 之后，不再直接落 `reply`
  - 先 durable 写入一条 `plan` step
  - 再进入 `reply` 或 `queued capability`
- 同时把 reply-scoped context 对 runtime 的读取升级为：
  - 不只看 `run.status / goal / latest reply`
  - 还会读最近一条 durable `plan` step
  - 并把这条 plan 带进 recall query

### 修改

- 修改 [reply_completion.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/reply_completion.go)
  - 新增 `QueuePlanStep(...)`
  - `plan` step 以 `InputJSON` durable 保存：
    - `thought_text`
    - `reply_text`
    - `pending_capability`
- 修改 [run_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor.go)
  - `processInitialRun(...)` 在首轮 emitter 产出结果后，先插入 `plan` step
  - 然后再走：
    - `CompleteRunWithReply(...)`
    - 或 `ContinueRunWithReply(...)`
  - 这样首轮链路现在变成：
    - `decide -> plan -> reply`
    - 或 `decide -> plan -> reply -> capability_call`
    - 若首轮已即时 durable 了 tool trace，则会变成：
      - `decide -> capability_call -> observe -> plan -> reply`
- 修改 [agentic_chat_generation.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/agentic_chat_generation.go)
  - reply-scoped runtime context 现在会读取最近一条 `StepKindPlan`
  - context lines 新增：
    - `最近一轮计划: ...`
  - recall query 也会补入这条 plan，而不只依赖 parent message 文本
- 修改测试
  - [run_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor_test.go)
  - [agentic_chat_generation_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/agentic_chat_generation_test.go)
  - 覆盖：
    - initial complete path 会先落 `plan`
    - initial pending capability path 会先落 `plan`
    - initial durable trace path 会在 `observe` 后再落 `plan`
    - reply-scoped loader 会读出 durable `plan` 并进入 recall query

### 决策

- 当前最小正确心智模型已经从：
  - “reply 时猜一下 run goal / latest reply”
  - 推进到
  - “reply 时直接读取这条 runtime 已 durable 的最近 plan”
- 这一步先只把 **首轮 initial turn** 接入 durable `plan`。
- continuation 链路里的 `plan` durable 化仍是下一步，但 reply-scoped context 已经不再完全依赖临时拼接。

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessorProcessRun(CompletesInitialReply|ExecutesQueuedCapabilityAfterInitialReply|DurablyRecordsInitialCapabilityTraceBeforeReply)|TestDefaultAgenticChatReplyScopeLoaderIncludesParentChainAndRuntimeState|TestBuildAgenticChatPromptContextUsesReplyScopedContext|TestBuildInitialChatExecutionPlanUsesReplyScopedContext'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./cmd/larkrobot ./internal/interfaces/lark ./internal/application/lark/cardaction ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/messages/ops`

## 2026-03-20 · Milestone N · Reply-Scoped Context 收窄

### 方案

- 直接修正当前多轮 reply 场景里最影响 agentic 体验的噪音来源：
  - 用户选择性回复某条消息时，真正需要的是：
    - 被回复消息
    - 它的父消息
    - 与这条 reply 链直接关联的 runtime plan / run status
  - 而不是默认把整个 chat 最近历史重新塞进 prompt
- 本轮把这条规则同时落到：
  - agentic 首轮 prompt builder
  - standard 首轮 prompt builder
- reply 场景现在会优先构造：
  - parent chain message list
  - related run thought / reply / status / plan context
  - 面向这条 reply chain 的 recall query

### 修改

- 修改 [agentic_chat_generation.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/agentic_chat_generation.go)
  - 新增 reply-scoped context loader
  - reply 场景不再先拉全 chat recent history
  - 会优先读取：
    - `ParentId` 对应消息
    - 它的父消息
    - 若 parent message 对应 runtime reply，则补入：
      - `run.status`
      - `run.goal / input_text`
      - 最近 active reply 的 `thought_text / reply_text`
  - recall query 也改成围绕 reply chain，而不是只拿当前这句模糊 follow-up
- 修改 [initial_chat_generation.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_chat_generation.go)
  - standard 首轮也复用同一套 reply-scoped 边界
  - prompt template / user name / chunk index 读取被抽成可注入依赖，方便测试和后续继续收口
- 新增/修改测试
  - [agentic_chat_generation_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/agentic_chat_generation_test.go)
  - [initial_chat_generation_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_chat_generation_test.go)
  - 覆盖：
    - agentic reply-scoped context 不再走全量 chat history
    - parent chain + runtime state 会进入 prompt context
    - standard 首轮也遵守同样的 reply-scoped 规则

### 决策

- 这一步的本质不是“再调 prompt”，而是先把上下文选择权收窄到真正 relevant 的链路。
- 当前在选择性 reply 场景里，runtime 的上下文心智已经变成：
  - current request
  - replied message
  - replied message 的 parent
  - related plan / waiting status / latest reply thought
- 这样后面继续做 durable model turn 时，不会再建立在“首轮 prompt 先被全 chat 噪音污染”的坏基础上。

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/messages/ops`

## 2026-03-20 · Milestone O · Approval Resume Worker 启动条件修正

### 方案

- 修正一个真实阻断 agentic 连续性的运行时装配问题：
  - 首轮 agentic chat 可以在 chat 级配置下运行
  - 但 `resume worker` 以前只看全局 `agent_runtime_enabled`
  - 结果就是：
    - approval callback 能成功把 run 置回 queued
    - resume event 能成功入 Redis queue
    - 但 worker 没启动，后续没人消费 queue
- 同时补一层入口可观测性：
  - card action dispatch 出错不再静默吞掉
  - 会同步返回错误 toast，避免“点了没反应”

### 修改

- 修改 [bootstrap.go](/mnt/RapidPool/workspace/BetaGo_v2/cmd/larkrobot/bootstrap.go)
  - `agent_runtime_resume_worker` 不再依赖全局 runtime 开关才启动
  - 只要依赖齐全，就常驻待命消费 resume queue
  - 抽出 `startAgentRuntimeResumeWorker(...)` 方便单测
- 新增 [bootstrap_test.go](/mnt/RapidPool/workspace/BetaGo_v2/cmd/larkrobot/bootstrap_test.go)
  - 覆盖 resume worker 可用时会启动
  - 不可用时返回 disabled
- 修改 [handler.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/lark/handler.go)
  - card action dispatch 失败时，改为返回 error toast
- 修改 [handler_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/lark/handler_test.go)
  - 覆盖 card action dispatch 失败时会返回错误提示

### 决策

- `resume worker` 属于基础运行时后台组件，不应该绑定某个具体 chat 的开关可见性。
- 对 agentic 来说，它应该像 scheduler 一样常驻，只在真正收到 event 时工作。
- 这一步修完后，approval/callback/schedule 的“入队后无人续跑”类问题会少一大块。

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./cmd/larkrobot ./internal/interfaces/lark ./internal/application/lark/cardaction ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/messages/ops`

## 2026-03-20 · Milestone M · Agentic Chat Prompt 与 Standard Chat Prompt 分叉

### 方案

- 直接处理当前 agentic 体验里最明显的错位：
  - runtime loop 已经越来越 agentic
  - 但首轮 prompt builder 仍然复用旧的模板库与单轮聊天心智
- 本轮把 agentic chat 的首轮 prompt contract 独立出来：
  - agentic mode 走专用 builder
  - standard mode 继续保留原 builder
  - `ChatGenerationPlan` 显式带 `Mode`
  - `chat_entry -> chat_response -> runtimecutover -> run_processor` 整条链都会保留这个 mode

### 修改

- 修改 [chat_generation_plan.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/chat_generation_plan.go)
  - `ChatGenerationPlan` 新增 `Mode`
- 修改 [chat_entry.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/chat_entry.go)
  - build plan 时显式写入 `agentic / standard`
- 修改 [chat_response.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/chat_response.go)
  - agentic / standard responder fallback path 也会强制补齐 mode，避免 plan 脱落
- 修改 [default_chat_generation_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor.go)
  - 新增 `defaultAgenticInitialChatPlanBuilder`
  - 新增 `defaultAgenticChatGenerationExecutor / defaultStandardChatGenerationExecutor`
  - agentic / standard 现在不仅是 builder 分叉，执行器入口也显式分轨
  - agentic mode 现在不再走旧 `BuildInitialChatExecutionPlan(...)`
- 新增 [agentic_chat_generation.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/agentic_chat_generation.go)
  - 新的 `BuildAgenticChatExecutionPlan(...)`
  - 新的 `agenticChatSystemPrompt()`
  - 新的 `buildAgenticChatUserPrompt(...)`
  - prompt 现在围绕 durable agent / runtime-owned tool loop / JSON `thought + reply` 输出约束构建
  - 不再依赖旧 prompt template 表达“单轮聊天”
- 新增/修改测试
  - [agentic_chat_generation_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/agentic_chat_generation_test.go)
  - [default_chat_generation_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor_test.go)
  - [chat_entry_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/chat_entry_test.go)
  - [chat_response_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/chat_response_test.go)

### 决策

- 当前 agentic chat 的差异已经不再只是：
  - 用 reasoning model
  - 用 agentic card 输出
- 而是进一步变成：
  - 首轮 prompt contract 独立
  - mode 在 plan 层显式透传
  - 执行器入口也已按 `agentic / standard` 分开
  - runtime loop 与 prompt 心智终于对齐
- 当前仍未完成的是：
  - agentic mode 虽然已有独立 prompt builder 和独立执行器入口，但底层 transport 仍然复用 `StreamTurn(...)`
  - multi-turn loop 里的 model turn 本身还不是 durable step

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'Test(DefaultChatGenerationPlanExecutorUsesAgenticPlanBuilderForAgenticMode|HandleAgenticChatResponseForcesAgenticPlanModeOnFallbackPath|AgenticChatSystemPromptEmphasizesRuntimeOwnedAgentLoop|BuildAgenticChatUserPromptIncludesRuntimeContextSections|ChatEntryHandlerRoutesAgenticReasonModeWithReasoningModel)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/schedule ./internal/application/lark/cardaction ./internal/infrastructure/ark_dal ./internal/infrastructure/redis ./cmd/larkrobot`

## 2026-03-20 · Milestone L · Continuation Tool Loop 即时 Durable Trace 与 Run Cursor 推进

### 方案

- 在首轮已经具备即时 durable trace 的基础上，继续把同样的语义扩到 continuation：
  - capability resume follow-up tool loop
  - callback / schedule generic continuation tool loop
- 同时补上一个会影响 durable 恢复精度的缺口：
  - recorder 以前只 append `capability_call / observe` step
  - 现在 recorder 也会同步推进 `run.current_step_index`
  - 这样即便 reply / pending capability 收尾前中断，run cursor 也不会停在旧位置

### 修改

- 修改
  - [capability_reply_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/capability_reply_turn.go)
  - [continuation_reply_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_reply_turn.go)
  - continuation reply turn request 新增 `Recorder`
- 修改
  - [default_capability_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor.go)
  - [default_continuation_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go)
  - nested tool loop 产出的 completed capability trace 现在会优先即时录入 durable ledger，而不是只在 turn result 里暂存
- 修改 [initial_capability_trace_recorder.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_capability_trace_recorder.go)
  - 抽出可复用的 `newRunCapabilityTraceRecorder(...)`
  - recorder 在录入 capability / observe step 后会同步推进 `run.current_step_index`
- 修改 [continuation_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor.go)
  - capability continuation 与 generic continuation 现在都会注入 recorder
  - `reply / queued capability` 改为基于 `nextAvailableStepIndex(...)` 续排，避免与即时录入的 nested step 撞位
  - completion 前会刷新最新 run revision，兼容 recorder 已推进 run cursor 的场景
  - `ProcessResume(...)` 增加 fail-safe：
    - 如果 resume 事件已消费，但 run 仍停在 `running` 且本次处理返回错误
    - 会把 run 收敛到 `failed`，而不是留下 zombie running run
- 修改 [run_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor.go)
  - 首轮 reply completion 前会刷新一次最新 run，避免 recorder 已推进 revision 后仍用旧 revision 收尾
- 新增/修改测试
  - [default_capability_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor_test.go)
  - [default_continuation_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor_test.go)
  - [continuation_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor_test.go)
  - [run_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor_test.go)

### 决策

- 当前 initial / continuation 两条 runtime-owned tool loop 都已经具备：
  - completed capability trace 即时 durable `capability_call + observe`
  - reply / pending capability 在真实最后一个已录 step 之后续排
  - run cursor 跟随即时录制推进
  - resume reply-turn 出错时也会把 running run 收敛到 failed，而不是悬空
- 当前剩余关键缺口继续收窄为：
  - multi-turn loop 里的 model turn 本身仍未 durable step 化
  - callback / schedule 的真实 resume payload producer 仍偏少
  - initial output emitter 默认装配仍留在 `runtimecutover`

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'Test(DefaultCapabilityReplyTurnExecutorRecordsCompletedCapabilityTraceWithRecorder|DefaultContinuationReplyTurnExecutorRecordsCompletedCapabilityTraceWithRecorder|ContinuationProcessorCompletesCapabilityReplyTurnAfterDurablyRecordingNestedCapabilityTrace|ContinuationProcessorQueuesPendingCapabilityAfterDurablyRecordingContinuationNestedCapabilityTrace|ContinuationProcessorProcessRunAdvancesRunCursorWhenInitialTraceRecordedBeforeFailure)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/schedule ./internal/application/lark/cardaction ./internal/infrastructure/ark_dal ./internal/infrastructure/redis ./cmd/larkrobot`

## 2026-03-20 · Milestone K · Pending Capability 恢复后重新进模型并继续 Tool Loop

### 方案

- 继续收掉“看起来还不够 agentic”的核心缺口：
  - pending capability 以前恢复后虽然已经 durable，但默认只会走 capability result summary / planner-style 收尾。
  - 现在改成：
    - 首轮 pending trace 显式保留触发 tool call 的 `previous_response_id`
    - queued capability input 持久化 continuation metadata
    - capability 真正执行完成后，runtime 优先把真实 tool output 回填到原 responses 链路
    - 默认 continuation executor 也能像首轮一样继续 own 一段 follow-up tool loop
    - 如果 follow-up 里再次遇到 pending capability，processor 会继续把：
      - nested completed capability calls
      - 新 reply
      - 新 queued capability
      写回 durable step ledger

### 修改

- 修改 [responses.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/ark_dal/responses.go)
  - `CapabilityCallTrace` 新增 `PreviousResponseID`
- 修改
  - [default_chat_generation_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor.go)
  - 首轮 pending capability trace 现在会带出触发它的 `snapshot.ResponseID`
  - 抽出通用 `executeChatToolCall(...)`，供首轮和 continuation 复用
- 修改 [planner.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/planner.go)
  - `CapabilityCallInput` 新增 `Continuation`
  - continuation metadata 现在属于 persisted capability input contract
- 新增
  - [capability_reply_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/capability_reply_turn.go)
  - [default_capability_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor.go)
  - 定义 `CapabilityReplyTurnExecutor`
  - 默认实现现在会：
    - 选模型
    - 用真实 tool output 回填 `previous_response_id`
    - 继续跑 follow-up tool loop
    - 产出 `plan + completed capability calls + pending capability`
- 修改 [continuation_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor.go)
  - capability completion 后现在优先尝试 `CapabilityReplyTurnExecutor`
  - 如果 continuation result 里带出 nested capability calls / pending capability：
    - 会继续 append durable `capability_call / observe / reply`
    - 并在需要时再 queue 下一条 `capability_call`
  - planner fallback 退回为真正的 fallback，而不再抢前置控制权
- 修改 [initial_chat_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_chat_turn.go)
  - `PreviousResponseID != ""` 的 continuation turn 现在允许没有 event，只要求：
    - `ModelID`
    - `ChatID`
    - `OpenID`
    - `Tools`
- 修改
  - [initial_reply_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor.go)
  - [runtime_output.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_output.go)
  - [runtime_chat.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_chat.go)
  - pending capability 从 cutover capture 到 queued capability 持久化这条链路，已经会保留 `PreviousResponseID`
- 修改 [runtimewire.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimewire/runtimewire.go)
  - 默认装配新增 `WithCapabilityReplyTurnExecutor(NewDefaultCapabilityReplyTurnExecutor())`
- 新增/修改测试
  - [default_capability_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor_test.go)
  - [planner_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/planner_test.go)
  - [initial_reply_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor_test.go)
  - [runtime_chat_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go)

### 决策

- 这一步之后，pending capability 的默认恢复路径已经不再只是：
  - 执行 capability
  - 写一条 summary reply
- 而是优先变成：
  - 执行 capability
  - 把真实 output 回填进原模型链路
  - 如有需要，继续 follow-up tool loop
  - 再把 continuation 的结果 durable 化
- 当前剩余关键缺口进一步收窄为：
  - continuation follow-up tool loop 虽然已 runtime-owned，但 model turn 本身仍然不是显式 durable step
  - callback / schedule 恢复仍主要走 generic continuation 语义，尚未像 capability resume 一样完整 re-enter model turn loop
  - initial output emitter 的默认装配仍然由 `runtimecutover` build processor 时注入

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'Test(DefaultCapabilityReplyTurnExecutor|ContinuationProcessorUsesCapabilityReplyTurnExecutorWhenContinuationStatePresent|ContinuationProcessorQueuesFollowUpPendingCapabilityFromReplyTurnExecutor)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/infrastructure/ark_dal ./cmd/larkrobot`

## 2026-03-19 · Milestone J · 首轮 Tool Flywheel 改成 Runtime-Owned Loop

### 方案

- 直接处理此前最大的真实缺口：
  - `responses.go` 不再在 runtime 首轮链路里负责 `handle tools -> function_call_output -> PreviousResponseId` 的递归推进。
- 新的首轮路径改为：
  - model turn 只负责产出 text delta / reasoning delta / function call intent
  - `defaultChatGenerationPlanExecutor` 负责截获 tool intent
  - runtime 基于 capability metadata 决定：
    - 直接执行 capability
    - 产出 pending approval placeholder
    - 把 tool output 再作为下一轮 model input
  - 循环直到得到最终 reply，或在 pending capability 后产出用户可见确认回复

### 修改

- 新增 [responses_manual.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/ark_dal/responses_manual.go)
  - 为 `ResponsesImpl` 增加单轮 `StreamTurn(...)`
  - 只解析：
    - reasoning delta
    - output text delta
    - function call intent
  - 不再在这里执行 handler，也不再递归发下一轮 `ResponsesRequest`
- 修改 [responses.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/ark_dal/responses.go)
  - 增加 `ResponseTurnRequest / ToolCallIntent / ResponseTurnSnapshot`
  - 把 tool continuation 所需 request/snapshot contract 显式化
- 修改 [initial_chat_generation.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_chat_generation.go)
  - `InitialChatExecutionPlan` 显式携带 `ModelID`
  - `ExecuteInitialChatExecutionPlan(...)` 改为复用新的 single-turn executor
- 新增 [initial_chat_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_chat_turn.go)
  - 定义首轮 model turn request/result/snapshot
  - 把 `agentruntime -> ark_dal.StreamTurn(...)` 的桥接收口
- 修改 [default_chat_generation_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor.go)
  - 默认 executor 不再直接依赖 `ExecuteInitialChatExecutionPlan(...)` 的单轮结果
  - 改为 runtime-owned multi-turn loop：
    - turn 1 读 model intent
    - runtime 执行 capability 或生成 pending approval placeholder
    - turn N 持续喂回 `function_call_output`
  - 首轮 capability 现在会统一走 `BuildToolCapabilities(...)` + `CapabilityRegistry`
  - 首轮 approval gate 现在会优先尊重 capability metadata，而不再依赖 `responses` 内联 handler 自己决定
  - 首轮 model request 现在真正使用 `plan.ModelID`，不再隐式掉回默认 normal model
- 修改 [default_chat_generation_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor_test.go)
  - 覆盖默认 executor 的 multi-turn loop
  - 覆盖 completed capability trace
  - 覆盖 pending approval trace

### 决策

- 这一步之后，真实首轮 flywheel 的 owner 已经从 `ark responses inline recursion` 移到 `agentruntime.defaultChatGenerationPlanExecutor`。
- `responses` 现在只是：
  - 单轮 streaming transport
  - function call intent parser
- runtime 现在真正拥有：
  - capability execution decision
  - approval placeholder decision
  - tool output 再入模
  - turn-to-turn continuation
- 当前剩余缺口已经进一步收窄为：
  - 首轮 multi-turn 虽已 runtime-owned，但 durable step 仍然是在首轮完成后批量写入 completed capability steps，而不是 turn-by-turn 即时落 step
  - pending capability 之后的恢复仍然是 `ContinuationProcessor` 通用续跑，而不是“模型继续 observe 前序 capability result 再决定下一步”的 full durable step loop

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'TestDefaultChatGenerationPlanExecutor'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/infrastructure/ark_dal ./cmd/larkrobot`

## 2026-03-19 · Milestone I · Initial Reply Emitter 下沉到 Processor Dependency

### 方案

- 在上一轮 declarative initial request 的基础上继续收口。
- 之前虽然 concrete executor 已经从 `InitialRunInput` 拿掉，但 input 里仍然带着 `Emitter`。
- 本轮继续把 initial delivery adapter 从 input contract 拿掉，改为 processor dependency：
  - `ContinuationProcessor` 增加 `initialReplyEmitter`
  - `WithInitialReplyEmitter(...)`
  - `ProcessInitial` 通过 processor dependency 装配默认 executor
  - `runtimecutover` 改为在 build processor 时注入 emitter，而不是塞进 `InitialRunInput`

### 修改

- 修改 [continuation_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor.go)
  - 增加 `initialReplyEmitter` 依赖
- 修改 [initial_reply_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor.go)
  - 新增 `WithInitialReplyEmitter(...)`
- 修改 [run_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor.go)
  - `InitialRunInput` 不再携带 `Emitter`
  - `BuildExecutor(...)` 改为显式吃 processor 注入的 emitter
  - `processInitialRun(...)` 改为使用 `p.initialReplyEmitter`
- 修改 [runtimewire.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimewire/runtimewire.go)
  - `BuildRunProcessor(...)` 改为显式接收 initial reply emitter
  - `ResumeWorker` 路径继续传 `nil`
- 修改
  - [runtime_chat.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_chat.go)
  - [runtime_text.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_text.go)
  - handler 的 `processorBuilder` 改为接收 emitter
  - fallback 直发路径仍复用本地 output adapter
- 修改
  - [run_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor_test.go)
  - [runtime_chat_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go)
  - [runtime_text_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_text_test.go)
  - 测试改为通过 processor dependency 注入 initial reply emitter
- 修改 [resume_worker_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/resume_worker_test.go)
  - 等待 `ReleaseRunLock()` 真正发生后再断言，消除全量回归下的时序抖动

### 决策

- 现在 `InitialRunInput` 已经是更干净的 declarative contract：
  - `Start`
  - `Event`
  - `Plan`
  - `OutputMode`
- initial delivery adapter 已经不再从 cutover request 直接塞进 `RunProcessorInput.Initial`。
- 当前更窄的剩余缺口是：
  - `runtimecutover` 仍然在 build processor 时注入 initial reply emitter
  - 也就是说 initial output delivery dependency 仍未完全沉到 base runtime 的默认装配层

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover -run 'TestContinuationProcessorProcessRun(CompletesInitialReply|ExecutesQueuedCapabilityAfterInitialReply|AttachCarriesActiveReplyTargetIntoInitialExecutor)|TestHandler(StartsRunStreamsCardAndCompletesReply|CapturesCapabilityCallTraceForCompletion|DelegatesPendingCapabilityThroughSingleInitialRunProcessorCall)|TestStandardHandlerStartsRunRepliesTextAndCompletesReply'`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./cmd/larkrobot`

## 2026-03-19 · Milestone H · Initial Run Input 改成 Declarative Request

### 方案

- 在上一轮“默认 initial reply executor 下沉到 base runtime”的基础上继续收口。
- 之前虽然默认 executor 已在 base `agentruntime`，但 `Runtime*CutoverHandler` 仍然会先构造 concrete `InitialReplyExecutor`，再塞进 `RunProcessorInput.Initial`。
- 本轮把 `InitialRunInput` 改成 declarative request：
  - `Start`
  - `Event`
  - `Plan`
  - `OutputMode`
- `ProcessInitial` 内部再调用 `BuildExecutor()` 去组装默认 executor。

### 修改

- 修改 [run_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor.go)
  - `InitialRunInput` 从 `Start + Executor` 改为 declarative request
  - 新增 `BuildExecutor()`
  - `processInitialRun(...)` 内部自行构造默认 executor
- 修改
  - [runtime_chat.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_chat.go)
  - [runtime_text.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_text.go)
  - cutover handler 改为只组装 declarative initial request
  - fallback 直发路径也复用 `InitialRunInput.BuildExecutor()`
- 修改
  - [run_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor_test.go)
  - [runtime_chat_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go)
  - 测试改为基于 declarative initial request 驱动

### 决策

- 现在 `Runtime*CutoverHandler` 已经不再持有 concrete `InitialReplyExecutor`。
- `RunProcessorInput.Initial` 也已经从“可执行对象”收口成“首轮生成请求”。
- 当时剩下的更窄缺口是：
  - `InitialRunInput` 还携带 `Emitter`
  - 也就是说 output delivery adapter 仍由 cutover 侧注入，而不是 processor 依赖

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover -run 'TestContinuationProcessorProcessRun(CompletesInitialReply|ExecutesQueuedCapabilityAfterInitialReply|AttachCarriesActiveReplyTargetIntoInitialExecutor)|TestHandler(StartsRunStreamsCardAndCompletesReply|CapturesCapabilityCallTraceForCompletion|DelegatesPendingCapabilityThroughSingleInitialRunProcessorCall)|TestStandardHandlerStartsRunRepliesTextAndCompletesReply'`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./cmd/larkrobot`

## 2026-03-19 · Milestone G · Default Initial Reply Executor 下沉到 Base Runtime

### 方案

- 在上一轮“默认首轮生成 executor 去单体化”的基础上继续前推。
- 之前即便 `BuildInitialChatExecutionPlan / ExecuteInitialChatExecutionPlan / FinalizeInitialChatStream` 已进入 base `agentruntime`，首轮 reply executor 仍然实现在 `runtimecutover` 包里。
- 本轮继续收口：
  - base `agentruntime` 持有默认 `InitialReplyExecutor`
  - `runtimecutover` 只保留 output emitter adapter
  - handler 不再构造本地 executor，而是调用 base runtime 的默认实现

### 修改

- 新增 [initial_reply_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor.go)
  - 定义 `InitialReplyOutputMode`
  - 定义 `InitialReplyEmitter`
  - 定义 `InitialReplyEmissionRequest / Result`
  - 定义 `CapturedInitialReply / PendingCapability`
  - 提供 `NewDefaultInitialReplyExecutor(...)`
  - 在 base runtime 内完成：
    - `plan.Generate(...)`
    - 读取 `InitialReplyTarget`
    - 发送到 emitter
    - 转换为 `InitialReplyResult`
    - pending capability -> queued capability call
- 删除 [runtimecutover/initial_reply_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/initial_reply_executor.go)
- 修改 [runtime_output.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_output.go)
  - `replyOrchestrator` 实现 `InitialReplyEmitter`
  - 负责把 base emission contract 映射回现有 agentic/standard 输出适配
- 修改
  - [runtime_chat.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_chat.go)
  - [runtime_text.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_text.go)
  - 改为直接使用 `agentruntime.NewDefaultInitialReplyExecutor(...)`
- 新增测试 [initial_reply_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_reply_executor_test.go)
  - 覆盖 emitter 调用
  - 覆盖 active reply target 透传
  - 覆盖 pending capability 转 queued call

### 决策

- 现在首轮 default executor 已经不只是“生成三段式”在 base runtime。
- 连默认 initial reply executor 本体和 emission contract 也已经进入 base `agentruntime`。
- `runtimecutover` 进一步退化为：
  - trigger / input text 解析
  - output emitter adapter
- 当前还没做到的点变得更窄了：
  - `RunProcessorInput.Initial` 仍然携带 output emitter，而不是完全 processor-owned 的 delivery dependency

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover -run 'TestDefaultInitialReplyExecutor(ProducesReplyViaEmitter|BuildsQueuedPendingCapability)|TestHandler(StartsRunStreamsCardAndCompletesReply|CapturesCapabilityCallTraceForCompletion|DelegatesPendingCapabilityThroughSingleInitialRunProcessorCall)|TestStandardHandlerStartsRunRepliesTextAndCompletesReply'`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./cmd/larkrobot`

## 2026-03-19 · Milestone F · 默认首轮生成 Executor 去单体化

### 方案

- 继续优先推进“理想 agentic 功能态”，不为拆包而拆包。
- 当前首轮 runtime 虽然已经由 `RunProcessor.ProcessRun(initial)` 决定何时触发，但默认 `ChatGenerationPlanExecutor` 仍然整块调用 `GenerateInitialChatSeq(...)`，首轮 prompt build / model execute / stream finalize 还是一个单体 helper。
- 本轮先切最小一刀：
  - `BuildInitialChatExecutionPlan(...)`
  - `ExecuteInitialChatExecutionPlan(...)`
  - `FinalizeInitialChatStream(...)`
- `GenerateInitialChatSeq(...)` 退化为兼容 wrapper，runtime 默认 executor 直接编排这三段。

### 修改

- 修改 [initial_chat_generation.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_chat_generation.go)
  - 新增 `InitialChatExecutionPlan`
  - 新增 `BuildInitialChatExecutionPlan(...)`
  - 新增 `ExecuteInitialChatExecutionPlan(...)`
  - 新增 `FinalizeInitialChatStream(...)`
  - `GenerateInitialChatSeq(...)` 改为兼容 wrapper
- 修改 [default_chat_generation_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor.go)
  - 默认 executor 改为显式走 planner / model executor / finalizer
  - 不再直接依赖 `GenerateInitialChatSeq(...)`
- 修改 [default_chat_generation_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_chat_generation_executor_test.go)
  - 新增对三段式边界的断言
  - 验证 deferred collector、tool provider、plan 传递与 finalizer 调用

### 决策

- 现在 runtime 默认首轮生成已经不再是“直接调用旧 monolithic helper”。
- 首轮生成的核心边界已经在 base `agentruntime` 内变成显式 contract：
  - plan builder
  - model executor
  - stream finalizer
- 这一步仍然不是 full runtime-owned initial loop 的终点：
  - `ProcessInitial` 还是通过 `runtimecutover.initialReplyExecutor` 间接触发生成
  - `RunProcessor` 还没有直接持有初始 planner/executor 依赖
  - Ark responses 的 tool interception 也仍然是现有 inline 机制

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime -run 'TestDefaultChatGenerationPlanExecutor(UsesConfiguredToolProvider|ReturnsErrorWithoutToolProvider)'`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./cmd/larkrobot`

## 2026-03-19 · Milestone E · Active Run Ownership 接入真实消息入口

### 方案

- 把此前只存在于 `shadow` 观察层的 ownership decision，真正接到实际聊天入口。
- 不再让 `/bb`、`@bot`、follow-up 在真实消息路径里各自重新“裸调用 chat handler”。
- 增加一个轻量 ownership contract：
  - `ops` 负责观察当前消息应该 `attach` 还是 `supersede`
  - `chat entry -> chat response -> runtime cutover` 负责原样透传
  - `StartShadowRunRequest` 负责落到真正的 durable run 启动参数

### 修改

- 新增 `internal/application/lark/agentruntime/initial_run_ownership.go`
  - 定义 `InitialRunOwnership`
  - 提供 `WithInitialRunOwnership / InitialRunOwnershipFromContext`
- 修改
  - `internal/application/lark/agentruntime/chat_entry.go`
  - `internal/application/lark/agentruntime/chat_response.go`
  - `internal/application/lark/agentruntime/cutover_contract.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text.go`
  - 将 ownership 从 chat entry 一路透传到 `StartShadowRunRequest`
- 新增 `internal/application/lark/messages/ops/runtime_route.go`
  - 统一运行时 observation
  - 统一 runtime chat invoke seam
  - 统一 `/bb` root command execute seam
- 新增 `internal/application/lark/agentruntime/initial_reply_target.go`
  - 定义 attach 场景下的初始 reply target context contract
- 修改
  - `internal/application/lark/messages/ops/reply_chat_op.go`
  - `internal/application/lark/messages/ops/chat_op.go`
  - `internal/application/lark/messages/ops/command_op.go`
  - 使以下真实入口会把 ownership 带入 runtime：
    - `/bb`
    - `@bot / p2p`
    - `follow_up / reply_to_bot`
- 新增测试
  - `internal/application/lark/messages/ops/runtime_route_test.go`
  - `internal/application/lark/agentruntime/chat_entry_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`

### 决策

- 现在 active run continuity 已经不再只存在于 `AgentShadowOperator` 的日志和持久化里。
- 真实消息路径的行为变为：
  - `/bb` 命中 command bridge 时会把 `supersede/attach` ownership 带进 runtime
  - `@bot / p2p` 回复会把 ownership 带进 runtime
  - `follow_up / reply_to_bot` 会跳过旧随机/意图分支，直接进入 runtime
- `ops` 侧 observation 只在以下条件满足时启用：
  - `agent_runtime_enabled = true`
  - `agent_runtime_chat_cutover = true`
- 这轮只给 `bb` 做 command bridge ownership 接入，不把其它管理命令混进 ambient runtime。
- attach 到已有 active run 时，初始 reply 现在会优先复用已有 active reply target：
  - agentic 模式 patch 原 card
  - standard 模式 patch 原文本消息
- waiting run 现在允许被新的 follow-up / reply-to-bot 唤醒回 `running`，不再卡死在 `waiting_* -> running` 的状态机缺口上。
- attach 到已有 run 不再只是“拿旧 run 继续算”：
  - 会在同一 run 内新增一个 durable `decide` step
  - 会清掉旧 `waiting_reason / waiting_token`
  - 会刷新 session `last_message_id / last_actor_open_id`
  - 会刷新 active chat slot TTL
  - 对同一条 attach 消息重复投递时保持幂等，不会重复追加 step

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./cmd/larkrobot`

## 2026-03-17 · Milestone A · Sprint 1 Contract 落地

### 方案

- 在应用层新增独立的 `internal/application/lark/agentruntime` 包，先把 runtime 的核心语义从旧 `chat_handler.go` 中拆出来。
- 第一阶段只建立 contract，不切真实流量：
  - `session / run / step` 类型
  - capability registry
  - group policy
  - command/tool adapter
- 所有类型都避免直接依赖具体的 Lark callback/message payload，先把 runtime 语义收口为纯应用层 DTO。

### 修改

- 新增 `internal/application/lark/agentruntime/types.go`
  - 定义 `AgentSession`、`AgentRun`、`AgentStep`
  - 定义 `RunStatus`、`StepStatus`、`TriggerType`、`WaitingReason`
  - 增加状态迁移校验
- 新增 `internal/application/lark/agentruntime/capability.go`
  - 定义 `CapabilityMeta`
  - 定义 `CapabilityRegistry`
  - 收口 side effect / scope / timeout 等 metadata
- 新增 `internal/application/lark/agentruntime/policy.go`
  - 定义 `TriggerPolicy`、`OwnershipPolicy`、`GroupPolicy`
  - 提供 `DefaultGroupPolicy`
- 新增 `internal/application/lark/agentruntime/capability_tools.go`
  - 把现有 tool registry 包成 runtime capability
  - 预置 side effect / scope 推导
- 新增 `internal/application/lark/agentruntime/capability_commands.go`
  - 定义 command bridge capability
  - 允许 `/bb` 这类 typed command 以 capability 方式接入
- 修改 `internal/application/lark/handlers/tools.go`
  - 暴露 `BuildLarkTools()`，供 runtime adapter 直接复用

### 决策

- capability metadata 先以“治理需要的最小集合”为主，不在第一轮引入复杂 planner 协议。
- `/bb` 先作为 command bridge capability 存在，不替换原命令执行链。
- group policy 第一阶段只承认以下触发：
  - `@bot`
  - `reply_to_bot`
  - `/bb`
  - `p2p`
  - follow-up window 内的同 actor 连续消息

### 验证

- `go test ./internal/application/lark/agentruntime`
- `go test ./internal/application/lark/handlers -run 'Test(BuildSchedulableToolsContainsStandardToolset|BuildSchedulableToolsRestrictsSendMessageChatOverride|LarkToolsExposeTypedConfigAndFeatureEnums)$'`

## 2026-03-17 · Milestone B · Agentic 流式卡片与聊天模式切换

### 方案

- 先把用户可见的 agentic 回复载体做出来，再推进真正的 runtime orchestration。
- 采用 Feishu `card entity + streaming element update` 方案：
  - 卡片实体先创建
  - 开启 `streaming_mode`
  - 分 element 流式更新“思考过程”和“正文”
- 思考过程必须放在卡片开头，并放进折叠块；正文在后面单独流式更新。
- 同时保留标准 chat 与 agentic chat 两种回复模式，通过配置切换。

### 修改

- 新增 `internal/infrastructure/lark_dal/larkmsg/streaming_agentic.go`
  - 创建 agentic streaming card entity
  - 流式更新 thought / reply element
- 新增
  - `internal/infrastructure/lark_dal/larkmsg/streaming_agentic_test.go`
  - `internal/infrastructure/lark_dal/larkmsg/streaming_agentic_manual_test.go`
- 修改
  - `internal/infrastructure/lark_dal/larkmsg/send.go`
  - `internal/infrastructure/lark_dal/larkmsg/larkcard/streaming_msg.go`
- 新增 `internal/application/config/chat_mode.go`
- 修改
  - `internal/application/config/manager.go`
  - `internal/application/config/definitions.go`
  - `internal/application/config/accessor.go`
  - `internal/application/lark/handlers/chat_handler.go`
  - `internal/application/lark/handlers/chat_handler_test.go`

### 决策

- `reason` 路径固定走 agentic 卡片，不受 `chat_mode` 影响。
- `normal` 路径由 `chat_mode` 控制：
  - `standard`：维持原文本回复
  - `agentic`：走流式卡片
- agentic 卡片布局固定为：
  - 折叠的 thought panel 在前
  - divider
  - reply markdown 在后
- 保留手工发卡测试入口，作为后续 agentic 交互回归的真实链路验收工具。

### 验证

- `go test ./internal/infrastructure/lark_dal/larkmsg`
- `go test ./internal/application/config`
- `go test ./internal/application/lark/handlers`
- 已对测试会话做过真实发卡验收，确认：
  - 思考折叠块在前
  - 正文在后
  - element streaming 正常工作

## 2026-03-18 · Milestone C · Shadow Mode 接入消息主链

### 方案

- 按设计文档的 rollout 原则，先接 shadow mode，不改变用户可见行为。
- 在消息主链新增 `AgentShadowOperator`：
  - 读取 runtime flag
  - 归一化消息 signal
  - 调用 `ShadowObserver`
  - 将 decision 写入日志和 `meta.Extra`
- 当前不创建 durable run，不发送新消息，不拦截旧 operator。

### 修改

- 修改 `internal/application/config/manager.go`
  - 新增配置键：
    - `agent_runtime_enabled`
    - `agent_runtime_shadow_only`
    - `agent_runtime_chat_cutover`
- 修改 `internal/application/config/definitions.go`
  - 为上述 flag 增加配置定义
- 修改 `internal/application/config/accessor.go`
  - 增加 runtime flag accessor
- 新增 `internal/application/lark/agentruntime/shadow.go`
  - 定义 `ShadowObserveInput`
  - 定义 `ShadowObservation`
  - 实现 `ShadowObserver`
- 修改 `internal/application/lark/agentruntime/capability_commands.go`
  - 提供默认 `/bb` command bridge capability 列表
- 新增 `internal/application/lark/messages/ops/agent_op.go`
  - 实现 `AgentShadowOperator`
  - 识别 mention / reply-to-bot / command / p2p
  - 记录 shadow decision 到 `meta.Extra`
- 修改 `internal/application/lark/messages/handler.go`
  - 把 `AgentShadowOperator` 挂入消息 processor

### 决策

- `agent_runtime_enabled + agent_runtime_shadow_only` 为当前真正生效的组合。
- `agent_runtime_chat_cutover` 先只建立配置 contract，不在本轮接管主链路。
- shadow mode 当前只记录：
  - 是否应该进入 runtime
  - trigger type
  - reason
  - scope
  - candidate capability
- candidate capability 当前只输出 `bb`，先不做更复杂的 planner。
- `reply_to_bot` 判断通过 parent message sender 与当前 bot identity 对比完成，不引入额外中间缓存。

### 验证

- `go test ./internal/application/config`
- `go test ./internal/application/lark/agentruntime`
- `go test ./internal/application/lark/messages`
- `go test ./internal/application/lark/handlers`

## 2026-03-18 · Milestone D · Durable Shadow Runtime Skeleton

### 方案

- 在给出建表 SQL、等待用户执行建表并完成 `go run ./cmd/generate` 之后，把 durable shadow skeleton 切到真实 schema contract。
- 本轮交付一套“可测试、可恢复设计成立、默认不破坏线上行为”的 durable shadow skeleton：
  - 基于 `gorm-gen model/query` 的 `session -> run -> step` repository
  - Redis coordination store
  - `RunCoordinator`
  - `AgentShadowOperator` 的 durable shadow run 持久化接入
- 这一阶段先把持久化 contract 锁定住，用户可见回复链路仍由旧 chat pipeline 负责。

### 修改

- 新增 `internal/infrastructure/agentstore/repository.go`
  - 改为基于生成的 `model.AgentSession` / `model.AgentRun` / `model.AgentStep`
  - 提供 `AutoMigrate`
  - 提供 `SessionRepository` / `RunRepository` / `StepRepository`
  - 支持 run revision conflict 校验
- 新增 `internal/infrastructure/redis/agentruntime.go`
  - 提供 run lock
  - 提供 active chat slot compare-and-swap
  - 提供 cancel generation
  - 提供 resume queue enqueue / dequeue
- 新增 `internal/application/lark/agentruntime/coordinator.go`
  - 定义 `StartShadowRunRequest`
  - 实现 `RunCoordinator`
  - 支持 `StartShadowRun` / `CancelRun`
  - 启动时创建初始 `decide` step
- 修改 `internal/application/lark/messages/ops/agent_op.go`
  - 增加可选 coordinator 注入点
  - 在 durable shadow run 创建成功后，把 `run_id` / `session_id` 写入 `meta.Extra`
- 新增测试
  - `internal/infrastructure/agentstore/repository_test.go`
  - `internal/infrastructure/redis/agentruntime_test.go`
  - `internal/application/lark/agentruntime/coordinator_test.go`
  - `internal/application/lark/messages/ops/agent_op_persist_test.go`
- 新增 `script/sql/20260318_agent_runtime_tables.sql`
- 新增 `script/AGENT_DB_CHANGE_SOP.md`

### 决策

- 数据库变更流程固定为：
  - 先把建表 / 改表 SQL 写入 `script/sql/`
  - 等待用户执行 SQL
  - 等待用户执行 `go run ./cmd/generate`
  - 之后代码只消费生成的 `model/query`
- shadow persistence 仍然是“只落 durable 状态，不改变用户可见回复”。
- `StartShadowRun` 当前按 `session_id + trigger_message_id` 做幂等，避免同一条消息重复创建 shadow run。
- `CancelRun` 当前先满足 shadow 阶段最小语义：
  - 标记 run cancelled
  - 递增 cancel generation
  - 清理 active chat slot / session active run

### 验证

- `go test ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/application/lark/agentruntime ./internal/application/lark/messages/ops -run 'Test(AgentSessionRepository|AgentRunRepository|AgentStepRepository|AgentRuntimeRunLockAcquireAndRelease|AgentRuntimeActiveChatSlotCompareAndSwap|AgentRuntimeResumeQueueAndCancelGeneration|RunCoordinator|AgentShadowOperatorPersistsRunIDsInMetaWhenCoordinatorIsPresent)'`
- `go test ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/application/lark/agentruntime ./internal/application/lark/messages ./internal/application/lark/messages/ops ./internal/application/lark/handlers ./internal/application/config`

## 2026-03-18 · Milestone E · Default Durable Coordinator Wiring

### 方案

- 不改 `messages.NewMessageProcessor(...)` 和 `cmd/larkrobot/bootstrap.go` 的创建时序，避免为了 coordinator 注入把现有装配根撕开。
- 在 `AgentShadowOperator` 内增加懒加载 coordinator loader：
  - 显式注入 coordinator 时，沿用显式注入
  - 未显式注入时，运行期从 `db + redis + botidentity + agentstore` 组装真实 `RunCoordinator`
  - 任一关键依赖缺失时，只降级为 observation，不中断消息主链

### 修改

- 修改 `internal/application/lark/messages/ops/agent_op.go`
  - 增加 `coordinatorLoader`
  - 增加线程安全的懒加载缓存
  - `persistShadowRun()` 改为优先取运行期 coordinator
- 新增 `internal/application/lark/messages/ops/agent_op_runtime.go`
  - 统一默认 durable coordinator 组装逻辑
  - 从 `db.DBWithoutQueryCache()`、`redis.GetRedisClient()`、`botidentity.Current()` 构造真实 `RunCoordinator`
- 修改 `internal/application/lark/messages/ops/agent_op_persist_test.go`
  - 覆盖 loader 路径
  - 锁住默认构造下存在 coordinator loader

### 决策

- 采用 operator 内懒加载，而不是 bootstrap 显式传参，原因是消息 processor 的创建早于基础设施模块的 `Start/Ready` 阶段。
- 只缓存成功构造出的 coordinator；依赖暂时不可用时不缓存 nil，避免把启动阶段的短暂未就绪固化成永久降级。
- 默认降级策略仍然是：
  - 继续记录 shadow decision
  - 不创建用户可见新回复
  - 不因为 durable 依赖暂时不可用而阻断消息处理

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/messages/ops -run 'Test(AgentShadowOperatorPersistsRunIDsInMetaWhenCoordinatorIsPresent|AgentShadowOperatorPersistsRunIDsViaCoordinatorLoader|NewAgentShadowOperatorProvidesDefaultCoordinatorLoader)$'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/messages ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/infrastructure/agentstore`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./cmd/larkrobot`

## 2026-03-18 · Milestone F · Resume Event Contract

### 方案

- 先把 callback / schedule continuation 所需的应用层恢复协议立住，但暂时不接具体 ingress。
- `ResumeEvent` 保持纯 runtime DTO：
  - 不携带原始 Lark SDK payload
  - 只表达 `run_id / revision / source / token / actor / occurred_at`
- `RunCoordinator` 提供 `ResumeRun()`：
  - 校验 source 对应的 waiting state
  - 校验 revision / token
  - 把 run 从 `waiting_*` 重新排回 `queued`
  - 追加一条 `resume` step，给后续 continuation 留出可观测轨迹

### 修改

- 新增 `internal/application/lark/agentruntime/resume_event.go`
  - 定义 `ResumeSource`
  - 定义 `ResumeEvent`
  - 定义 `ErrResumeStateConflict` / `ErrResumeTokenMismatch`
  - 提供 waiting reason / trigger type / external ref 映射
- 修改 `internal/application/lark/agentruntime/coordinator.go`
  - 新增 `ResumeRun(ctx, event ResumeEvent)`
  - 成功恢复后写入 `StepKindResume`
- 修改 `internal/application/lark/agentruntime/coordinator_test.go`
  - 覆盖 payload 校验
  - 覆盖 revision mismatch
  - 覆盖 cancelled run 拒绝恢复
  - 覆盖 callback / schedule 恢复成功

### 决策

- application 层 `ResumeEvent` 与 infra 层 Redis queue payload 先解耦，避免把 transport / queue 细节直接泄漏进 runtime contract。
- approval / callback 恢复要求 token；schedule 恢复不要求 token。
- `ResumeRun()` 当前只完成 contract + 状态推进，不在本轮直接接 card callback registry 或 scheduler ingress。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ResumeEventValidate|RunCoordinatorResumeRun)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/messages ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/infrastructure/agentstore ./cmd/larkrobot`

## 2026-03-18 · Milestone G · Run-Aware Card Callback Ingress

### 方案

- 先把 card callback 这条 continuation ingress 接起来，不等真正的 runtime worker。
- 新增一个应用层 `ResumeDispatcher`：
  - 先调用 `RunCoordinator.ResumeRun()` 把 waiting run 写回 `queued`
  - 再把恢复事件写入 Redis resume queue，给后续 worker 留 continuation 信号
- `cardaction.Dispatch()` 增加 run-aware adapter：
  - 命中 agent runtime resume action 时，不再走 builtin registry
  - 解析 payload 为 `ResumeEvent`
  - 调默认 runtime dispatcher
  - 未命中时继续回退现有 cardaction registry

### 修改

- 新增 `internal/application/lark/agentruntime/resume_dispatcher.go`
  - 定义 `ResumeDispatcher`
  - 定义 `resume -> enqueue` 的最小调度语义
- 新增 `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  - 统一默认 coordinator / resume dispatcher 的基础设施装配
- 新增 `internal/application/lark/cardaction/runtime_resume.go`
  - 解析 runtime callback payload
  - 构造 `ResumeEvent`
  - 调默认 dispatcher
- 修改 `internal/application/lark/cardaction/registry.go`
  - 在默认 registry lookup 前增加 run-aware callback 分流
- 修改 `pkg/cardaction/action.go`
  - 增加 `agent.runtime.resume`
  - 增加 `run_id / step_id / revision / source / token` payload field
- 修改 `internal/infrastructure/redis/agentruntime.go`
  - 扩展 resume queue payload，承载完整 runtime continuation 信息

### 决策

- Redis queue payload 继续保持 infra 自有 DTO，而不是直接复用 application 层 `ResumeEvent`，避免引入包依赖环。
- card callback ingress 当前只负责：
  - 校验并写回 run 状态
  - 发布 continuation 事件
  - 不直接在 callback 请求内执行后续 agent step
- `agent.runtime.resume` 作为标准 card action 协议字段落地，后续 approval / callback 卡片统一复用这套 payload。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/cardaction -run 'Test(ResumeDispatcherDispatch|DispatchRoutesAgentRuntimeResumeAction)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/cardaction ./internal/interfaces/lark ./internal/application/lark/agentruntime ./internal/application/lark/messages/ops ./internal/infrastructure/redis ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./pkg/cardaction`

## 2026-03-18 · Milestone H · Resume Queue Consumer And Schedule Continuation

### 方案

- 继续把 continuation substrate 往前补齐：
  - 新增进程内 `ResumeWorker`，持续消费 Redis resume queue
  - worker 处理时先抢 run lock，再调用可插拔 processor
  - 当前默认 processor 先支持 `schedule` 来源的 `ResumeRun()`，其他来源只做占位确认日志
- 同时给 scheduler 增加内部专用的 `agent_runtime_resume` tool：
  - 只注册到 `BuildSchedulableTools()`
  - 不暴露给普通聊天工具集
  - 到点后只负责向 resume queue 写标准 payload

### 修改

- 新增 `internal/application/lark/agentruntime/resume_worker.go`
  - 定义 `ResumeWorker`
  - 定义 `ResumeProcessor`
  - 提供 `Start / Stop / Stats`
- 新增 `internal/application/lark/agentruntime/resume_worker_test.go`
  - 覆盖消费队列
  - 覆盖 run lock 跳过
- 修改 `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  - 增加默认 `BuildResumeWorker()`
  - 增加 Redis queue adapter
  - 默认 processor 对 `schedule` 事件执行 `ResumeRun()`
- 修改 `cmd/larkrobot/bootstrap.go`
  - 增加 `agent_runtime_resume_worker` runtime module
  - 在 `agent_runtime_enabled=true` 时默认启动
- 修改 `internal/application/lark/schedule/func_call_tools.go`
  - 新增内部 tool `agent_runtime_resume`
  - 把 schedule continuation 写入 Redis resume queue
- 修改 `internal/application/lark/handlers/tools.go`
  - `BuildSchedulableTools()` 额外注册 runtime-only schedulable tool

### 决策

- `agent_runtime_resume` 只进入 schedulable tool registry，不进入普通 `BuildLarkTools()`，避免被聊天路径直接暴露给模型。
- schedule 路径当前采用“到点入队 -> worker 执行 `ResumeRun()`”的形式，和 callback 侧“先 `ResumeRun()` 再入队”暂时并存；统一化留到真正 continuation planner 落地时再收口。
- 默认 worker processor 当前只把 `schedule` 来源做成真正的状态推进；其他来源先只确认和打点，不在本轮强行补一个半成品 continuation executor。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestResumeWorker'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/handlers ./internal/application/lark/schedule -run 'Test(BuildSchedulableToolsIncludesAgentRuntimeResumeOnlyForScheduler|AgentRuntimeResumeHandleEnqueuesResumeEvent)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/cardaction ./internal/application/lark/schedule ./internal/application/lark/handlers ./internal/application/lark/messages/ops ./internal/interfaces/lark ./internal/infrastructure/redis ./cmd/larkrobot ./pkg/cardaction`

## 2026-03-18 · Milestone I · Continuation Processor Skeleton

### 方案

- 把 `ResumeWorker` 从“只确认 resume event / 只对 schedule 做状态推进”推进到“真正消费 resumed run”。
- 新增最小 continuation processor：
  - 如果 run 仍停留在 `waiting_*`，worker 侧先补一次 `ResumeRun()`
  - 如果 callback 路径已经在 dispatcher 侧把 run 写回 `queued`，worker 直接继续执行
  - 把 run 从 `queued` 推到 `running`
  - 把当前 `resume` step 从 `queued`/`running` 消费到 `completed`
  - 追加一条 `observe` step 记录 continuation observation
  - 把 run 收敛到 `completed`，并释放 session active run / active chat slot
- 本轮仍然不接 capability planner 和用户可见回复，只先锁定“跨进程恢复后 durable 状态能闭环”。

### 修改

- 新增 `internal/application/lark/agentruntime/continuation_processor.go`
  - 定义 `ContinuationProcessor`
  - 统一 callback / schedule continuation 的最小执行语义
- 新增 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 覆盖 queued callback run continuation
  - 覆盖 waiting schedule run continuation
- 修改 `internal/infrastructure/agentstore/repository.go`
  - 为 `StepRepository` 增加 step status update 能力
- 修改 `internal/application/lark/agentruntime/coordinator.go`
  - 扩展 `stepRepository` contract，允许 continuation processor 读取并更新 step
- 修改 `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  - 默认 `ResumeWorker` 改为使用 `ContinuationProcessor`

### 决策

- 先交付 terminal continuation skeleton，而不是一次性接 capability planner，避免在 cutover 前把 worker 演进成另一套半成品 chat loop。
- worker 层开始收口 callback / schedule 的恢复语义：
  - waiting run 可以由 worker 自己补做 `ResumeRun()`
  - already queued run 直接继续执行
- continuation 完成后立即释放 active run slot，避免 completed run 长时间占住 follow-up / supersede 判断基础。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessor'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/infrastructure/agentstore ./internal/application/lark/agentruntime ./internal/application/lark/cardaction ./internal/application/lark/schedule ./internal/application/lark/handlers ./internal/application/lark/messages/ops ./internal/interfaces/lark ./internal/infrastructure/redis ./cmd/larkrobot ./pkg/cardaction`

## 2026-03-18 · Milestone J · Approval Contract And Waiting State

### 方案

- 把 approval 从文档设计推进成可落地的 runtime contract：
  - 定义 `ApprovalRequest`
  - 增加 `RunCoordinator.RequestApproval()`
  - 把运行中的 run 持久化到 `waiting_approval`
  - 为审批卡片提供标准 approve callback payload
- approval callback 仍复用现有 `agent.runtime.resume` 动作：
  - `source=approval`
  - `run_id / step_id / revision / token`
- 在 runtime 侧补充过期校验：
  - approval request 的 `expires_at` 写入 `approval_request` step
  - `ResumeRun()` 处理 approval source 时读取当前 approval step
  - 过期审批直接拒绝，不把 run 重新排回 `queued`

### 修改

- 新增 `internal/application/lark/agentruntime/approval.go`
  - 定义 `RequestApprovalInput`
  - 定义 `ApprovalRequest`
  - 提供 `Validate()` / `ApprovePayload()`
  - 提供 approval step state 编解码
- 修改 `internal/application/lark/agentruntime/coordinator.go`
  - 新增 `RequestApproval()`
  - `ResumeRun()` 增加 approval step expiry / step_id 校验
- 新增 `internal/application/lark/agentruntime/approval_test.go`
  - 覆盖 approval DTO 校验
  - 覆盖 running -> waiting_approval 状态推进
  - 覆盖 expired approval resume 拒绝
  - 覆盖 valid approval resume 进入 queued

### 决策

- approval card 仍沿用通用 `agent.runtime.resume` callback 协议，不再为 approve path 额外发明 action 名称。
- approval 过期信息不单独建表，先写入 `approval_request` step 的 `input_json`，由 runtime 在恢复时读取。
- 当前只实现 approve path；reject/cancel 按钮和 approval UI 发送器留到下一轮。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ApprovalRequest|RunCoordinatorRequestApproval|RunCoordinatorResumeRunRejectsExpiredApproval|RunCoordinatorResumeRunQueuesWaitingApprovalRun)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./pkg/cardaction`

## 2026-03-18 · Milestone K · Approval Card Sender And Reject Path

### 方案

- 在已有 `waiting_approval` contract 基础上补齐用户可交互的最小闭环：
  - 生成标准审批卡
  - 卡片内同时提供 approve / reject 两个 runtime action payload
  - reject 回调命中后，把 run 从 `waiting_approval` 正式推进到 `cancelled`
- 继续复用通用 callback ingress：
  - approve 走 `agent.runtime.resume`
  - reject 走 `agent.runtime.reject`
- 本轮仍然不做 callback 内联 patch resolved card，先把“可发送、可拒绝、状态正确”做实。

### 修改

- 修改 `pkg/cardaction/action.go`
  - 新增 `agent.runtime.reject`
- 修改 `internal/application/lark/agentruntime/approval.go`
  - `ApprovalRequest` 增加 `RejectPayload()`
- 新增 `internal/application/lark/agentruntime/approval_sender.go`
  - 定义 `ApprovalCardTarget`
  - 定义 `LarkApprovalSender`
  - 定义 `BuildApprovalCard()`
  - 支持 reply-to-trigger-message / create-to-chat 两条发送路径
- 修改 `internal/application/lark/agentruntime/coordinator.go`
  - 新增 `RejectApproval()`
  - reject 时校验 approval token / step_id / expiry
  - reject 成功后释放 active run / active chat slot
- 修改 `internal/application/lark/cardaction/runtime_resume.go`
  - run-aware callback 分流新增 reject path
- 新增测试
  - `internal/application/lark/agentruntime/approval_sender_test.go`
  - `internal/application/lark/agentruntime/approval_reject_test.go`
  - `internal/application/lark/cardaction/registry_test.go` reject routing case

### 决策

- approval card sender 先作为独立 sender 能力落地，不强行在本轮接 planner。
- reject path 不走 resume queue，直接在 callback 请求内完成状态拒绝与取消清理，因为它不会触发后续 continuation。
- resolved card 的 inline patch 继续留到下一轮；当前 callback UX 只保证动作生效，不保证卡面即时回写。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/cardaction -run 'Test(LarkApprovalSender|RunCoordinatorRejectApproval|DispatchRoutesAgentRuntimeRejectAction)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction`

## 2026-03-18 · Milestone L · Approval Callback Resolved Card UX

### 方案

- 在 approval approve / reject 动作已经生效的基础上，把 callback 响应补成“状态成功 + 卡面即时回写”：
  - approve 后返回 approved card
  - reject 后返回 rejected card
- resolved card 不重新拼装临时文案，而是从 durable `approval_request` step 中恢复原始审批信息，再以新的 visual state 重建卡片。
- 如果卡片回写信息加载失败，不影响主动作成败，仍以 run state 为准。

### 修改

- 修改 `internal/application/lark/agentruntime/coordinator.go`
  - 新增 `LoadApprovalRequest()`
  - 从 approval step 的 `input_json` + `external_ref` 恢复 `ApprovalRequest`
- 修改 `internal/application/lark/cardaction/runtime_resume.go`
  - callback 分流新增 approval request loader
  - approve / reject 成功后返回 resolved raw card payload
- 修改 `internal/application/lark/cardaction/registry_test.go`
  - 增加 approval approve resolved-card case
  - reject case 改为校验返回 resolved card

### 决策

- callback 内联回写卡面属于 UX 增强，不应反向影响状态推进；因此 loader 失败时静默降级为“只完成动作，不更新卡面”。
- resolved card 沿用同一张 approval card 模板，只切 visual state，避免 approve/reject 后出现两套不一致 UI。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/cardaction ./internal/application/lark/agentruntime -run 'Test(DispatchApprovalResumeReturnsResolvedCard|DispatchRoutesAgentRuntimeRejectAction|RunCoordinatorRejectApproval|LarkApprovalSender)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction`

## 2026-03-18 · Milestone M · Capability-Aware Continuation Planner Slice

### 方案

- 在不直接切真实 chat orchestration 的前提下，把 `ContinuationProcessor` 从“只会消费 `resume` step 的 terminal executor”推进到“能理解 `capability_call` step 的最小 planner slice”。
- 本轮只解决两类 durable continuation：
  - queued `capability_call` 直接查 registry 执行
  - `RequiresApproval=true` 的 capability 不执行，转成正式 `waiting_approval`
- 继续保持原有 `resume -> observe -> complete` fallback 不动，避免在 cutover 前把 worker 路径改成另一套大而杂的 chat loop。

### 修改

- 新增 `internal/application/lark/agentruntime/planner.go`
  - 定义 `CapabilityCallInput`
  - 定义 `CapabilityApprovalSpec`
  - 定义 `ContinuationProcessorOption`
  - 提供 `WithCapabilityRegistry(...)`
  - 提供 capability call input / result 编解码与 approval spec 归一化
- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `NewContinuationProcessor(...)` 支持 option 注入
  - 增加 capability registry 字段
  - 当前 step 为 `capability_call` 时：
    - hydrate `CapabilityRequest`
    - 按 scope 查 capability registry
    - 直接执行 capability，写回 `capability_call -> observe -> reply`
    - 或进入 `RequestApproval()`，把 run 推到 `waiting_approval`
  - capability 执行失败时，run / step 会收敛到 failed terminal state，并释放 active slot
- 新增 `internal/application/lark/agentruntime/planner_test.go`
  - 覆盖 queued capability call 直接执行
  - 覆盖 protected capability 触发 approval gate
- 修改 `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  - 默认 `ResumeWorker` 注入 Lark tool capability registry
  - 让真实 worker 具备 planner slice 所需的默认能力表

### 决策

- planner slice 先只消费 durable `capability_call` step，不在本轮直接生成新的 planner 协议。
- 默认 registry 先只注入 `BuildDefaultLarkToolCapabilities()`，不把 command bridge executor 强行接进来。
- approval gate 当前只完成“挂起等待审批”的 durable contract；
  - approve 后如何回到待执行 capability 并真正执行，留到下一轮 continuation replay 补齐。
- `reply` step 先作为 durable output trace 落地，仍不代表用户可见回复已经由 runtime 接管。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessor(ExecutesQueuedCapabilityCall|RequestsApprovalForProtectedCapability)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`

## 2026-03-18 · Milestone N · Approval Replay To Pending Capability

### 方案

- 在 Milestone M 已经具备 `approval gate` 的基础上，补齐 approve 之后的真正 continuation：
  - approval callback 恢复 run
  - 当前 `resume` step 只作为 continuation marker 被消费
  - runtime 回溯最近一个待执行 `capability_call`
  - 执行原 capability，而不是直接 terminal complete
- 这样 approval 才从“状态能挂起”推进到“挂起后能继续完成真实动作”。

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `ProcessResume(...)` 增加 approval replay 检测
  - approval source 命中 `resume` step 时，会回溯待执行 `capability_call`
  - 先完成 `resume` marker，再执行 capability，并在 `resume` 之后追加 `observe -> reply`
- 修改 `internal/application/lark/agentruntime/planner_test.go`
  - 新增 protected capability approve 后 replay 执行用例

### 决策

- approval replay 不重新请求审批；一旦 approve callback 已通过 token / expiry / revision 校验，就直接执行原 capability。
- `resume` step 仍然保留并落到 completed，用于 durable trace，而不是被 capability replay 吞掉。
- replay 后 `observe` / `reply` 的 index 以 `resume` step 之后的位置继续递增，保留完整轨迹：
  - `capability_call`
  - `approval_request`
  - `resume`
  - `observe`
  - `reply`

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessorReplaysProtectedCapabilityAfterApproval'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`

## 2026-03-18 · Milestone O · Agentic Chat Runtime Cutover Entry

### 方案

- 先把 `chat_handler` 的 agentic 路径切成 runtime-aware，而不是一次性把 output engine 全部重写进 runtime。
- cutover 命中时：
  - 创建 durable run
  - 继续复用现有 streaming card 发给用户
  - 在流式结束后把最终 thought / reply 收敛成 terminal `reply` step
- 这样可以先让真实聊天链路进入 runtime，再继续把 planner/output reference 往内收。

### 修改

- 新增 `internal/application/lark/agentruntime/reply_completion.go`
  - 定义 `CompleteRunWithReplyInput`
  - 新增 `RunCoordinator.CompleteRunWithReply()`
  - 负责完成当前 `decide` step、追加 terminal `reply` step、完成 run、清理 active slot
- 新增 `internal/application/lark/handlers/chat_runtime_cutover.go`
  - 定义 `RuntimeAgenticCutoverRequest`
  - 定义 `RuntimeAgenticCutoverHandler`
  - 定义 `shouldUseRuntimeChatCutover(...)`
  - 为 `chat_handler` 增加 builder 注入点和 cutover 分流
- 修改 `internal/application/lark/handlers/chat_handler.go`
  - agentic 模式下改经 `handleAgenticChatResponse(...)`
  - cutover flag 命中时委派到 runtime adapter
  - 未命中时继续 fallback 到原 `SendAndUpdateStreamingCard(...)`
- 新增 `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - 默认 runtime chat cutover adapter
  - 创建 run
  - tee 流式结果
  - 复用现有 streaming card sender
  - 流式结束后回写 terminal `reply`
- 修改 `cmd/larkrobot/bootstrap.go`
  - 启动时注入默认 runtime cutover builder

### 决策

- 本轮 cutover 先只覆盖 agentic 模式，不碰 standard chat。
- 当前用户可见输出仍由既有 `GenerateChatSeq + SendAndUpdateStreamingCard()` 完成；
  - runtime 负责 entry / durable state
  - 还不负责持有完整 streaming 生命周期
- 当前真实 chat cutover 落地的是 terminal `reply` durable output；
  - 还没有让真实 chat planner 产出 `capability_call`
  - 也还没有把 card message / card entity reference 写回 runtime

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestRunCoordinatorCompleteRunWithReplyAppendsReplyStepAndClearsActiveSlot'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/handlers -run 'Test(ShouldUseRuntimeChatCutover|HandleAgenticChatResponseDelegatesToRuntimeCutover)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime/runtimecutover -run 'TestHandlerStartsRunStreamsCardAndCompletesReply'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone P · Runtime Message/Card Reference Tracking

### 方案

- 在 cutover 已经能把 terminal `reply` 落库的基础上，把用户可见 agentic 卡片的引用也纳入 runtime state。
- 目标不是立即 patch 这张卡，而是先保证 runtime 至少知道：
  - 这次回复对应的 message id
  - 这次 streaming card 对应的 card entity id
- 这样后续 planner/output engine 和 callback 补写才有 durable reference 可用。

### 修改

- 修改 `internal/infrastructure/lark_dal/larkmsg/streaming_agentic.go`
  - 定义 `AgentStreamingCardRefs`
  - `sendAgentStreamingCreateCard(...)` / reply variant 返回 `message_id + card_id`
- 修改 `internal/infrastructure/lark_dal/larkmsg/send.go`
  - 新增 `SendAndUpdateStreamingCardWithRefs(...)`
  - 旧 `SendAndUpdateStreamingCard(...)` 改为兼容包装
- 修改 `internal/application/lark/agentruntime/reply_completion.go`
  - `CompleteRunWithReplyInput` 增加 `response_message_id / response_card_id`
  - `run.last_response_id` 写 message id
  - terminal `reply` step `external_ref` 写 card id
  - `reply` step `output_json` 增加 response refs
- 修改 `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - cutover adapter 改为消费 sender 返回的 refs
  - refs 透传到 `CompleteRunWithReply()`
- 修改测试
  - `internal/application/lark/agentruntime/coordinator_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`
  - `internal/infrastructure/lark_dal/larkmsg/streaming_agentic_test.go`

### 决策

- 不做 schema 变更，先复用现有 durable 字段：
  - `run.last_response_id` 保存 message id
  - terminal `reply` step `external_ref` 保存 card id
- 旧 sender API 保持不破坏，新的 ref-aware API 以增量方式提供。
- 当前只追踪 terminal chat reply 的 refs；
  - 还没有把中间 planner step 和 card patch 生命周期完全纳入 runtime

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestRunCoordinatorCompleteRunWithReplyAppendsReplyStepAndClearsActiveSlot'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime/runtimecutover -run 'TestHandlerStartsRunStreamsCardAndCompletesReply'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/infrastructure/lark_dal/larkmsg -run 'TestSendAndUpdateStreamingCardPreservesRefsFromWithRefsVariant'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone Q · Real Chat Capability Trace Persistence

### 方案

- 在 agentic chat cutover 已经能创建 durable run、落 terminal `reply` 的基础上，把真实聊天链路里已经执行过的 tool/capability 调用也纳入 durable step。
- 本轮不引入新的 planner schema，也不改卡片发送链路；只把当前 Ark responses 流里真实发生的 function call trace 暴露出来，并在 cutover 完成时落成 completed 的 `capability_call` step。
- 这样 runtime 至少能够还原：
  - 这次 agentic 回复执行过哪些 capability
  - 每个 capability 的 `call_id`
  - 输入参数
  - 输出结果

### 修改

- 修改 `internal/infrastructure/ark_dal/responses.go`
  - 新增 `CapabilityCallTrace`
  - `ModelStreamRespReasoning` 增加 `capability_call`
  - `OnCallArgs(...)` 在真实 handler 执行后记录 pending trace
  - 流式输出改为 drain delta + capability trace，避免把内部状态只困在 `ResponsesImpl`
- 新增 `internal/infrastructure/ark_dal/responses_test.go`
  - 锁住 delta 与 capability trace 的 drain 行为
- 修改 `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - cutover adapter 在 tee 流时收集 capability trace
  - `CompleteRunWithReplyInput` 透传 `capability_calls`
- 修改 `internal/application/lark/agentruntime/reply_completion.go`
  - `CompleteRunWithReplyInput` 增加 `capability_calls`
  - terminal `reply` 前追加 completed `capability_call` step
  - step `external_ref` 复用 `call_id`
  - step `output_json` 复用 `encodeCapabilityResult(...)`
- 修改测试
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`
  - `internal/application/lark/agentruntime/coordinator_test.go`

### 决策

- 不做 schema 变更，继续复用现有 `agent_steps`：
  - `kind = capability_call`
  - `capability_name` 写 tool/function name
  - `external_ref` 写 `call_id`
- completed trace step 先表示“真实已执行的 capability 历史”，不直接复用到 approval/resume planner 输入。
- `input_json` 仍沿用 `CapabilityCallInput` envelope；
  - 当前只填 `request.payload_json`
- `output_json` 对齐现有 capability result 结构；
  - 文本结果走 `output_text`
  - 合法 JSON 结果走 `output_json`
- 当前只覆盖 agentic chat cutover；
  - standard chat 仍未纳入 runtime

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/infrastructure/ark_dal -run 'TestResponsesImplDrainPendingStreamItemsEmitsDeltaAndCapabilityTrace'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime/runtimecutover -run 'TestHandler(CapturesCapabilityCallTraceForCompletion|StartsRunStreamsCardAndCompletesReply)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestRunCoordinatorCompleteRunWithReply(AppendsCapabilityCallStepsBeforeReply|AppendsReplyStepAndClearsActiveSlot)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone R · Standard Chat Runtime Cutover Entry

### 方案

- 在 agentic chat 已经接入 runtime entry 的基础上，把 standard chat 也纳入同一套 durable run 起点。
- 本轮仍然不改 standard chat 的用户可见形态：
  - 继续等待模型流结束
  - 继续发送普通文本回复
- runtime 侧负责：
  - 创建 durable run
  - 收集真实 capability trace
  - 记录 terminal `reply`
  - 写回普通文本回复的 `message_id`

### 修改

- 修改 `internal/application/lark/handlers/chat_runtime_cutover.go`
  - 新增 `RuntimeStandardCutoverRequest`
  - 新增 `RuntimeStandardCutoverHandler`
  - 新增 standard cutover builder / fallback reply sender
  - 抽出 `handleStandardChatResponse(...)`
- 修改 `internal/application/lark/handlers/chat_handler.go`
  - standard 模式不再直接在 `ChatHandlerInner(...)` 里内联 drain + reply
  - 改为复用 `handleStandardChatResponse(...)`
- 修改 `cmd/larkrobot/bootstrap.go`
  - 启动时注册默认 standard runtime cutover builder
- 新增 `internal/application/lark/agentruntime/runtimecutover/runtime_text.go`
  - 标准文本 runtime cutover handler
  - 复用现有 run start / stream capture / terminal completion contract
  - 发送普通文本回复后把 `message_id` 回写 runtime
- 修改测试
  - `internal/application/lark/handlers/chat_handler_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text_test.go`

### 决策

- cutover flag 现在同时覆盖：
  - `agentic` 回复模式
  - `standard` 回复模式
- standard cutover 仍然不是 runtime-owned streaming output：
  - 只在流结束后发送一次文本回复
  - 不在本轮引入文本流式 patch/编辑协议
- standard 文本回复没有 card entity：
  - `run.last_response_id` 记录 reply message id
  - terminal `reply.external_ref` 退化复用 reply message id
- 当最终 reply 为空时，standard runtime cutover 不发送文本消息，但仍会完成 durable run。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/handlers -run 'Test(ShouldUseRuntimeChatCutover|HandleStandardChatResponseDelegatesToRuntimeCutover|HandleAgenticChatResponseDelegatesToRuntimeCutover)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime/runtimecutover -run 'TestStandardHandler(StartsRunRepliesTextAndCompletesReply|SkipsSendingWhenReplyIsEmpty)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone S · Runtime-Owned Output Orchestration

### 方案

- 把当前 agentic/standard 两条 cutover 里各自持有的“输出发送 + stream capture + ref 回传”逻辑收束到 `runtimecutover` 包内的统一 orchestrator。
- 这一步的目标不是改用户行为，而是把 reply 发送生命周期正式收回 runtime 包，为后续 continuation-driven reply engine 留出稳定入口。
- 统一 orchestrator 当前负责：
  - 基于原始 stream 采集 thought/reply/capability trace
  - 按输出模式调用对应 sender
  - 返回 `message/card refs + captured reply snapshot`

### 修改

- 新增 `internal/application/lark/agentruntime/runtimecutover/runtime_output.go`
  - 定义 `replyOutputMode`
  - 定义 `replyOrchestrator`
  - 定义统一 `emit(...)` 流程
- 新增 `internal/application/lark/agentruntime/runtimecutover/runtime_output_test.go`
  - 锁住：
    - agentic 模式透传 stream + 返回 refs/snapshot
    - standard 模式发送文本 + 返回 message ref
    - empty reply 时不发送 standard 文本
- 修改
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text.go`
  - agentic/standard handler 均改走统一 orchestrator

### 决策

- orchestrator 目前仍然只支持两种模式：
  - `agentic`
  - `standard`
- sender 仍然复用现有基础设施能力：
  - agentic 复用 streaming card sender
  - standard 复用文本 reply sender
- 本轮不引入 step-driven patch/update 协议；
  - orchestrator 只是把“如何把当前 reply 发出去”从 handler 里收回来
- `captureRuntimeReplyStream(...)` 继续作为统一的 stream 观察入口；
  - capability trace 去重、thought/reply 聚合都在这里完成

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime/runtimecutover -run 'Test(ReplyOrchestratorEmit(AgenticStreamsCardAndReturnsSnapshot|StandardRepliesTextAndReturnsMessageRef|StandardSkipsEmptyReply)|Handler(StartsRunStreamsCardAndCompletesReply|CapturesCapabilityCallTraceForCompletion|DeduplicatesCapabilityCallTraceByCallID)|StandardHandler(StartsRunRepliesTextAndCompletesReply|SkipsSendingWhenReplyIsEmpty))'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone T · Continuation-Driven Reply Engine

### 方案

- 在 runtime cutover 已经统一输出 orchestration 的基础上，把 continuation 执行出的 terminal `reply` 也接入真实发送链路，而不是只落 durable step。
- 当前先覆盖最有价值的一条链：
  - queued `capability_call`
  - capability 执行完成
  - 生成 terminal `reply`
  - 真实发出 reply
  - 把 message/card refs 写回 run / reply step
- 这样 approval/schedule/callback 恢复后的能力执行，就不再只是“库里完成”，而是能真正把结果发回会话。

### 修改

- 新增 `internal/application/lark/agentruntime/reply_emitter.go`
  - 定义 `ReplyEmitter`
  - 定义 `ReplyEmissionRequest / ReplyEmissionResult`
  - 提供默认 `LarkReplyEmitter`
  - 按配置的 `chat_mode` 选择：
    - `agentic` -> 发送 agentic card
    - `standard` -> 回复原消息；若没有 trigger message，则直接在 chat 新发文本
- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `ContinuationProcessor` 增加 `replyEmitter`
  - capability 执行完成后调用 emitter
  - terminal `reply` step `output_json` 增加 response refs
  - terminal `reply` step `external_ref` 写 card id 或 message id
  - `run.last_response_id` 写 message id
- 修改 `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  - 默认 resume worker 构造 `ContinuationProcessor` 时注入 `NewLarkReplyEmitter()`
- 新增测试
  - `internal/application/lark/agentruntime/reply_emitter_test.go`
  - `internal/application/lark/agentruntime/planner_test.go`

### 决策

- continuation reply engine 目前只覆盖 capability continuation；
  - 还没有把 generic `resume -> observe -> complete` fallback 变成用户可见回复
- emitter 使用当前 chat 配置决定输出形态；
  - 不额外引入 run 级别 mode 字段
- standard continuation 默认优先 reply 到原 trigger message；
  - trigger 缺失时退化成在 chat 内新发文本
- agentic continuation 当前仍然是“一次性发最终卡片”；
  - 还不是 step-driven patch/update

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorEmitsCapabilityReplyAndPersistsReplyRefs|LarkReplyEmitterUsesAgenticSenderInAgenticMode|LarkReplyEmitterUsesTextReplyInStandardMode)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone U · Generic Continuation Reply Contract

### 方案

- 把上一轮只覆盖 `capability_call` continuation 的 reply engine，继续推广到 generic continuation fallback。
- 当前 generic continuation 的典型路径是：
  - waiting callback / waiting schedule
  - `ResumeRun`
  - `resume -> observe -> complete`
- 这条路径之前只会完成 durable run，不会对用户产生可见回复；本轮把它补成：
  - 产出 terminal `reply`
  - 调用统一 reply emitter
  - 把 refs 写回 run / reply step

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `continuationPlan` 增加 `reply_text`
  - generic `execute(...)` 路径新增：
    - `emitContinuationReply(...)`
    - `newContinuationReplyStep(...)`
  - generic continuation 完成后：
    - 追加 terminal `reply` step
    - `run.last_response_id` 写入 emitted message id
    - `reply` step `output_json / external_ref` 写入 refs
- 修改测试
  - `internal/application/lark/agentruntime/continuation_processor_test.go`
  - generic callback / schedule continuation 现在都断言 `observe + reply` 两个 terminal step
  - 新增 generic continuation reply emitter + refs 持久化用例

### 决策

- generic continuation 的默认 reply text 先直接复用 continuation summary：
  - `agent runtime continuation processed via <source>`
- 这条文案当前是 runtime 内部闭环占位语义；
  - 后续仍应被更明确的业务化 reply contract 替换
- generic continuation 与 capability continuation 现在都共享同一个 `ReplyEmitter` contract；
  - 但二者各自构造 reply text 的方式仍不同

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorProcessesQueuedCallbackRun|ContinuationProcessorResumesAndProcessesWaitingScheduleRun|ContinuationProcessorEmitsGenericContinuationReplyAndPersistsReplyRefs)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone V · Business-Grade Generic Continuation Semantics

### 方案

- 在 generic continuation 已经能对外发 terminal reply 的基础上，把占位式 runtime 文案替换成可直接面向用户的中文提示。
- 当前先覆盖已经落地的两个 generic source：
  - `callback`
  - `schedule`
- 目标不是一步到位做复杂 NLG，而是先让 runtime 的默认 reply 至少不再暴露内部实现细节。

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - 新增 `resolveContinuationReplyText(...)`
  - generic continuation 的 `reply_text` 不再复用 `result_summary`
  - 当前映射为：
    - `callback` -> `已收到回调，继续处理完成。`
    - `schedule` -> `定时任务已恢复执行并完成。`
    - `approval` -> `审批已处理，继续执行完成。`
    - fallback -> `已继续处理并完成。`
- 修改 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 锁住 callback / schedule generic continuation 的用户提示语义

### 决策

- `result_summary` 仍保留 runtime 视角，便于内部观测；
  - 用户真正收到的 reply text 走独立语义函数
- 本轮先不把 generic continuation 和具体业务 payload 深度绑定；
  - 仍然按 `ResumeSource` 做语义分流
- 这一步是“去内部味”，不是终态智能回复；
  - 后续仍可以把 reply text 进一步与 capability / callback payload 绑定

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorEmitsGenericContinuationReplyAndPersistsReplyRefs|ContinuationProcessorUsesUserFacingScheduleReplyText)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone W · Payload-Aware Generic Continuation Context

### 方案

- 在不改 schema 的前提下，把 generic continuation 从“只知道 `ResumeSource`”推进到“还能感知当前 resume step、前置 step 和原始请求上下文”。
- 用户可见的正文 reply 先保持稳定，避免在 cutover 过程中让外部语义频繁抖动；
  - payload-aware 信息优先进入 durable `observe` payload 和 agentic thought 区域。
- 这样 continuation 在 agentic 模式下也能继续满足既有卡片结构：
  - thought 在前、折叠展示
  - 正文 reply 在后

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `continuationObservation` 新增：
    - `waiting_reason`
    - `trigger_type`
    - `resume_step_id`
    - `resume_step_external_ref`
    - `previous_step_kind`
    - `previous_step_external_ref`
  - `continuationPlan` 新增 `thought_text`
  - generic continuation 新增 context resolver：
    - 从 `run + step chain + resume event` 推导 continuation 上下文
    - 生成 payload-aware thought 文案
    - 保留稳定的中文正文 reply
  - generic continuation 发 reply 时，开始把 `thought_text` 交给 `ReplyEmitter`
- 修改 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 新增 callback generic continuation 的 payload-aware thought / observe 断言
  - 新增 schedule generic continuation 的前置 step context 断言

### 决策

- 本轮坚持 schema-free：
  - 不新增表字段
  - 不引入 callback/schedule 专用业务 payload 持久化
  - 先最大化利用现有 `run / step / resume event`
- payload-aware 信息先进入 thought 和 `observe.output_json`；
  - 正文 reply 继续保持 source-level 中文提示，避免回归风险
- thought 只展示对用户有意义的前置 step：
  - `wait`
  - `capability_call`
  - `approval_request`
  - `plan`
  - `observe`
  - 不展示 `decide` 这类噪音步骤

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorEmitsGenericContinuationReplyAndPersistsReplyRefs|ContinuationProcessorUsesUserFacingScheduleReplyText|ContinuationProcessorEmitsPayloadAwareContinuationThoughtAndObservation|ContinuationProcessorIncludesPreviousStepContextInThought)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone X · Agentic Continuation Existing-Card Patch Path

### 方案

- 在不重做整套 step-driven lifecycle 的前提下，先落一个最小但真实有价值的 patch slice：
  - 如果 continuation 之前已经产出过 agentic reply card
  - 且 runtime 还能从历史 `reply` step 中拿到 `message_id / card_id`
  - 那么 continuation 不再新发一张卡，而是复用原 card entity 做 streaming patch
- 这一步先只覆盖 agentic 路径；
  - standard text reply 仍保持原有 send/reply 行为

### 修改

- 修改 `internal/application/lark/agentruntime/reply_emitter.go`
  - `ReplyEmissionRequest` 新增：
    - `target_message_id`
    - `target_card_id`
  - `LarkReplyEmitter` 新增 agentic patch 分支：
    - 有 `target_card_id` 时优先 patch 既有 card
    - 无 target 时回退创建新 card
- 修改 `internal/infrastructure/lark_dal/larkmsg/streaming_agentic.go`
  - 新增 `PatchAgentStreamingCardWithRefs(...)`
  - 复用既有 `streamAgentCardContent(...)`，对同一张 card entity 重新开启 streaming 并更新 element
- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - continuation 发 reply 前，会从历史 step 链回溯最近一个 `reply` step
  - 解析其中的 `response_message_id / response_card_id`
  - 把 refs 作为 patch target 传给 reply emitter
- 修改测试
  - `internal/application/lark/agentruntime/reply_emitter_test.go`
    - 新增“有 target card 时走 patch，不走 create”断言
  - `internal/application/lark/agentruntime/continuation_processor_test.go`
    - 新增 continuation 会把最近 reply refs 作为 patch target 传出的断言

### 决策

- 当前只做 agentic existing-card patch，不扩到 standard text patch；
  - 先把最有用户感知价值的卡片续写闭环打通
- patch target 先从 durable `reply` step 反推，不额外加表字段；
  - 仍然遵守现有 schema-free 演进策略
- 只要缺失 `card_id`，就自动回退到创建新 reply；
  - 不因为 patch 条件不满足而中断 continuation

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(LarkReplyEmitterPatchesExistingAgenticCardWhenTargetCardProvided|ContinuationProcessorTargetsLatestReplyRefsForAgenticPatch)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone Y · Standard Continuation Existing-Message Patch Path

### 方案

- 在 agentic existing-card patch 已经打通的基础上，把同一套“优先复用旧回复载体”的策略补到 standard text 路径。
- 当 continuation 能从历史 `reply` step 解析出既有 `message_id` 且当前 chat mode 为 standard 时：
  - 不再新 reply 一条文本
  - 改为直接 patch 旧消息正文

### 修改

- 修改 `internal/application/lark/agentruntime/reply_emitter.go`
  - `LarkReplyEmitter` 新增 `patchText` 分支
  - standard 模式下：
    - 有 `target_message_id` 时优先 patch 旧文本消息
    - 无 target 时保持原有 `reply/create` fallback
- 修改 `internal/infrastructure/lark_dal/larkmsg/reply.go`
  - 新增 `PatchTextMessage(...)`
  - 复用 Feishu `Message.Patch` 能力更新 text content
- 修改 `internal/application/lark/agentruntime/reply_emitter_test.go`
  - 新增 standard mode 命中 target message 时走 patch 的断言

### 决策

- continuation 的复用策略现在按载体分流：
  - agentic -> patch 既有 card entity
  - standard -> patch 既有 text message
- 仍然坚持“有 target 才 patch，没有就 fallback send”；
  - 不把 continuation 绑死在 patch-only 语义上
- 这一步解决的是“复用旧消息载体”，不是完整的 step-owned 多阶段 patch 协议；
  - runtime 仍然没有接管更细粒度的中间态更新 lifecycle

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestLarkReplyEmitterPatchesExistingStandardMessageWhenTargetMessageProvided'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone Z · Reply Delivery Metadata Persistence

### 方案

- 在 runtime 已经会复用旧 card / 旧消息 patch 的基础上，把“这次 reply 是 create、reply 还是 patch”收口为 durable metadata。
- 目标不是再扩一套新表，而是让现有 `reply` step 的 `output_json` 足够表达 output lifecycle：
  - 发送模式
  - patch target refs
  - 最终 response refs

### 修改

- 修改 `internal/application/lark/agentruntime/reply_emitter.go`
  - 新增 `ReplyDeliveryMode`
    - `create`
    - `reply`
    - `patch`
  - `ReplyEmissionResult` 新增：
    - `delivery_mode`
    - `target_message_id`
    - `target_card_id`
- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - continuation / capability 的 terminal `reply` step output 现在会持久化 delivery metadata
- 修改 `internal/application/lark/agentruntime/reply_completion.go`
  - `CompleteRunWithReplyInput` / `replyCompletionOutput` 新增 delivery metadata 字段
- 修改 `internal/application/lark/agentruntime/runtimecutover/runtime_output.go`
  - agentic 首次输出标记为 `create`
  - standard 首次文本 reply 标记为 `reply`
- 修改
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text.go`
  - runtime cutover 现在会把 delivery mode 透传到 `CompleteRunWithReply(...)`
- 修改测试
  - `internal/application/lark/agentruntime/reply_emitter_test.go`
  - `internal/application/lark/agentruntime/continuation_processor_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_output_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text_test.go`

### 决策

- delivery metadata 先落在 step output JSON，不做 schema 变更；
  - 保持 runtime schema-free 演进速度
- `response_*` 表示这次执行后最终生效的回复 refs；
  - `target_*` 只在 patch 语义下补充“复用了谁”
- 这一步把“reply 载体复用”从行为层推进到了 durable contract 层；
  - 但仍未进入更细粒度的多阶段 step-owned patch/update protocol

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover -run 'Test(LarkReplyEmitter|ReplyOrchestrator|HandlerStartsRunStreamsCardAndCompletesReply|StandardHandlerStartsRunRepliesTextAndCompletesReply|ContinuationProcessorTargetsLatestReplyRefsForAgenticPatch)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone AA · Payload-Aware Reply Body Semantics For Patched Continuations

### 方案

- 在 generic continuation 已经能识别 reply target 并复用旧消息载体的基础上，继续把用户可见正文从“只看 source”推进到“识别这次是否在更新原消息”。
- 当前先覆盖最直接、风险最低的一层：
  - 命中既有 reply target 的 continuation
  - 正文 reply 改为“已更新原消息”语义
- 这样用户看到的不再只是“继续处理完成”，而是知道系统是在续写/更新之前那条结果

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `continuationContext` 新增 latest reply target 识别
  - generic continuation 新增 `findLatestReplyTargetBeforeIndex(...)`
  - `resolveContinuationReplyText(...)` 改为感知 context：
    - `callback + reply target` -> `已收到回调，并已更新原消息。`
    - `schedule + reply target` -> `定时任务已执行，并已更新原消息。`
    - `approval + reply target` -> `审批已处理，并已更新原消息。`
  - 未命中 reply target 时，继续沿用现有 source-level 中文文案
- 修改 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 锁住 callback continuation 在 patch 原消息场景下的正文语义

### 决策

- 先只做“patch 原消息”这个最确定的业务语义增强；
  - 不在本轮把 callback / schedule 的业务 payload 文案扩展成开放式模板
- reply body 是否进入“更新原消息”语义，依赖 durable step 链中是否存在可用 reply target；
  - 不依赖额外 schema
- 仍保持保守 fallback：
  - 没有 target -> 继续使用原 source-level reply 文案

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorTargetsLatestReplyRefsForAgenticPatch|ContinuationProcessorEmitsGenericContinuationReplyAndPersistsReplyRefs|ContinuationProcessorUsesUserFacingScheduleReplyText)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone AB · Reply-Step Patch Lineage

### 方案

- 在 runtime 已经能 durable 表达 `create / reply / patch` 之后，继续把 patch 的“来源 reply step”补齐。
- 目标是让一个 patched reply step 不只是知道自己 patch 了哪个 `message/card`，还知道自己在 durable step 链上更新的是哪一个旧 `reply` step。

### 修改

- 修改 `internal/application/lark/agentruntime/reply_emitter.go`
  - `ReplyEmissionResult` 新增 `target_step_id`
- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `replyTarget` 新增 `step_id`
  - `resolveReplyTarget(...)` / `findLatestReplyTargetBeforeIndex(...)` 返回最新 reply target 时同时带出旧 `reply step id`
  - continuation / capability 新产生的 terminal `reply` step output 现在会持久化 `target_step_id`
- 修改 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 锁住 patched continuation reply step 的 `target_step_id`

### 决策

- `target_step_id` 仍然只放在 reply output JSON，不做 schema 变更；
  - 继续遵守当前 schema-free 演进方式
- 这一步建立的是 reply-step lineage，不是通用 step graph；
  - 先把 output lifecycle 中最关键的 patch 关系表达清楚
- 现在 patched reply 至少可以回答三件事：
  - 更新了哪条消息
  - 更新了哪张卡
  - 更新的是哪一个旧 `reply` step

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessorTargetsLatestReplyRefsForAgenticPatch'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone AC · Reverse Patch Lineage On Prior Reply Step

### 方案

- 在 patched reply step 已经能指向旧 `reply step` 的基础上，继续补齐反向关系。
- 当 continuation 通过 patch 更新旧回复时：
  - 新 reply step 会记录 `target_step_id`
  - 旧 reply step 也会写入 `patched_by_step_id`
- 这样 runtime 的 reply lineage 从单向引用推进到双向可追踪。

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `capabilityReply` 新增 `patched_by_step_id`
  - 新增 `linkPatchedReplyStep(...)`
  - patch 场景在 append 新 reply step 后，会回写目标旧 reply step 的 output JSON
- 修改 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 锁住旧 `reply step` 的 `patched_by_step_id`

### 决策

- 继续沿用 schema-free 方式，把 reverse lineage 放在 `reply` step output JSON；
  - 不额外建 relation 表
- 反向回写只在 `delivery_mode=patch` 且 `target_step_id` 存在时执行；
  - create / reply 路径保持无副作用
- 这一步让 reply lifecycle 至少具备最小的“before/after”可追踪能力；
  - 但仍不是通用 step graph

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessorTargetsLatestReplyRefsForAgenticPatch'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone AD · Reply Lifecycle State And Non-Destructive Patch Backfill

### 方案

- 在 reply step 已经具备正反向 patch lineage 的基础上，继续把 lifecycle 语义显式化：
  - 新产生的 terminal `reply` step 明确标记为 `active`
  - 被后续 patch 覆盖的旧 `reply` step 明确标记为 `superseded`
- 同时收口一个真实风险：
  - continuation patch 旧 reply step 时，不能把 runtime cutover 产出的 `thought_text / reply_text` 等既有 output 字段覆盖丢失

### 修改

- 修改 `internal/application/lark/agentruntime/reply_emitter.go`
  - 新增 `ReplyLifecycleState`
    - `active`
    - `superseded`
- 修改 `internal/application/lark/agentruntime/reply_completion.go`
  - runtime cutover 产出的 `reply` step output 新增 `lifecycle_state=active`
- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - continuation / capability 新产生的 `reply` step output 明确写入 `lifecycle_state=active`
  - `linkPatchedReplyStep(...)` 回写旧 reply step 时：
    - 增加 `patched_by_step_id`
    - 增加 `lifecycle_state=superseded`
    - 改为保留原 output JSON 后再追加字段，避免丢失已有 `thought_text / reply_text`
- 修改测试
  - `internal/application/lark/agentruntime/coordinator_test.go`
  - `internal/application/lark/agentruntime/continuation_processor_test.go`

### 决策

- lifecycle state 继续走 schema-free 路线，仍然只写入 `reply` step `output_json`；
  - 不因为这层状态语义额外引入新列
- `active / superseded` 当前只覆盖 reply lifecycle；
  - 还不是通用 step lifecycle machine
- patch 回写必须是 non-destructive；
  - lineage 字段是增量附加，不允许覆盖已有 reply payload

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorTargetsLatestReplyRefsForAgenticPatch|RunCoordinatorCompleteRunWithReply(AppendsCapabilityCallStepsBeforeReply|AppendsReplyStepAndClearsActiveSlot))'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime/runtimecutover -run 'Test(ReplyOrchestrator|HandlerStartsRunStreamsCardAndCompletesReply|StandardHandlerStartsRunRepliesTextAndCompletesReply)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone AE · Reply Step Supersede Transition For New Terminal Replies

### 方案

- 把 reply lifecycle 从“只有 patch 才会回写旧 reply step”推进到“任何新的 terminal reply 都会显式 supersede 上一条 reply step”。
- 这样 runtime 的 lifecycle 不再只覆盖 patch 场景：
  - `patch` -> 旧 reply step 写 `patched_by_step_id`
  - `create / reply` -> 旧 reply step 写 `superseded_by_step_id`
- 核心目标是给后续 active-only target 选择和更完整的 continuation output lifecycle 打稳定基础。

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `capabilityReply` 新增 `superseded_by_step_id`
  - `linkPatchedReplyStep(...)` 升级为通用的旧 reply supersede 回写逻辑
  - continuation / capability 在 append 新 reply step 后：
    - `patch` 场景回写 `patched_by_step_id`
    - `create / reply` 场景回写 `superseded_by_step_id`
    - 两类场景都会把旧 reply step 标记为 `lifecycle_state=superseded`
- 修改 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 新增 continuation 走 `reply/create` 时 supersede 旧 reply step 的断言
  - 锁住旧 runtime reply payload 在 supersede 回写后仍保留

### 决策

- `patched_by_step_id` 与 `superseded_by_step_id` 分开保留：
  - patch 表示“复用并更新原载体”
  - supersede 表示“出现了新的 terminal reply，因此旧 reply 退役”
- 当前只回写最新目标 reply step；
  - 暂不回溯批量清理更早历史 reply step
- 这一步把 reply lifecycle 从 patch-only 扩展到更通用的 terminal transition；
  - 但还没有做 active-only target resolver

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorTargetsLatestReplyRefsForAgenticPatch|ContinuationProcessorSupersedesPriorReplyStepWhenContinuationCreatesNewReply)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone AF · Active-Only Reply Target Selection

### 方案

- 既然 reply lifecycle 已经显式标出 `active / superseded`，target 解析就不该继续简单取“最后一个 reply step”。
- 新策略改为：
  - 优先选择最近的 `active` reply target
  - 如果历史数据没有 lifecycle 字段或当前链路里找不到 active target，再回退到 legacy 的“最近 reply target”
- 目标是避免 continuation 把已经 superseded 的旧 reply 当成 patch / supersede 目标。

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - 新增 `replyLifecycleState(...)`
  - `findLatestReplyTargetBeforeIndex(...)` 改为优先 active-only 解析
  - `resolveReplyTarget(...)` 改为优先 active-only 解析
  - 保留 legacy fallback，避免旧数据因缺少 lifecycle 字段而失效
- 修改 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 新增 superseded reply 不会被误选为 target 的断言

### 决策

- lifecycle state 现在开始真正参与 runtime 行为，而不只是审计 metadata；
  - 但仍保持 legacy fallback，避免一次性把历史 reply step 判为不可用
- active-only 选择只作用在 reply target resolver；
  - 不改变已有 step append / resume state machine
- 这一步为后续更完整的 output lifecycle 奠定了“当前有效 reply”判定基础。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorTargetsLatestReplyRefsForAgenticPatch|ContinuationProcessorSupersedesPriorReplyStepWhenContinuationCreatesNewReply|ContinuationProcessorPrefersLatestActiveReplyTarget)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone AG · Approval Title Aware Generic Continuation Reply

### 方案

- 在 generic continuation 已经具备 source-aware 与 patch-aware reply 语义之后，继续把业务 payload 信息带入正文。
- 第一层先选择最稳定且用户可见价值最高的 payload：
  - `approval_request.title`
- 目标是让 approval generic continuation 不再只说“审批已处理”，而是能直接告诉用户“哪个审批事项已通过”。

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `continuationContext` / `continuationObservation` 新增 `previous_step_title`
  - 新增 `continuationPreviousStepTitle(...)`
    - 从 `approval_request` step output 提取 title
  - `resolveContinuationThoughtText(...)`
    - approval 场景追加 `审批事项：<title>`
  - `resolveContinuationReplyText(...)`
    - `approval + title + patch target` -> `审批「<title>」已通过，并已更新原消息。`
    - `approval + title` -> `审批「<title>」已通过，继续执行完成。`
- 修改 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 新增 approval title 在 reply/thought/observe payload 中透出的断言

### 决策

- richer payload-aware reply body 先只吃已有 durable step payload；
  - 不新增 schema
  - 不扩 `ResumeEvent`
- 先覆盖 approval title，是因为它天然用户可见且表达稳定；
  - callback / schedule 的业务 payload 仍留待下一轮
- thought 与 observe payload 同步带标题，保持 agentic UI 与 durable 审计一致。

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorUsesApprovalTitleInGenericReplyText|ContinuationProcessorPrefersLatestActiveReplyTarget|ContinuationProcessorSupersedesPriorReplyStepWhenContinuationCreatesNewReply|ContinuationProcessorTargetsLatestReplyRefsForAgenticPatch)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone AH · Wait-Title-Aware Callback And Schedule Continuation Reply

### 方案

- 在 approval generic continuation 已经能使用 `approval_request.title` 的基础上，把同样的 payload-aware 语义扩到 callback / schedule。
- 当前不扩 `ResumeEvent`，也不新增 schema；
  - 直接消费前置 `wait` step 的 `output_json.title`
- 目标是让 callback / schedule generic continuation 不再只输出来源级提示，而是在已有 wait payload 时带出业务标题。

### 修改

- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - `continuationPreviousStepTitle(...)` 现在同时支持：
    - `approval_request.title`
    - `wait.title`
  - `resolveContinuationThoughtText(...)`
    - `wait` 场景改为 `等待事项：<title>`
  - `resolveContinuationReplyText(...)`
    - `callback + wait.title` -> `回调「<title>」已收到，继续处理完成。`
    - `callback + wait.title + patch target` -> `回调「<title>」已收到，并已更新原消息。`
    - `schedule + wait.title` -> `定时任务「<title>」已执行完成。`
    - `schedule + wait.title + patch target` -> `定时任务「<title>」已执行，并已更新原消息。`
  - `observe.output_json` 继续带出 `previous_step_title`
- 修改 `internal/application/lark/agentruntime/continuation_processor_test.go`
  - 新增 callback wait title 语义断言
  - 新增 schedule wait title 语义断言
  - 保留无 title 的旧 source-level reply 文案断言，确保回退路径稳定

### 决策

- callback / schedule 的 richer reply body 先只依赖现有 `wait` step payload；
  - 这一步不是完整 resume payload protocol
- 有 `wait.title` 才升级正文语义；
  - 没有 title 时继续保持旧文案，避免历史数据回归
- `wait.title` 同时进入 thought / observe / reply 三层；
  - 保持 agentic UI 与 durable 审计一致

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorUsesWaitTitleInCallbackGenericReplyText|ContinuationProcessorUsesWaitTitleInScheduleGenericReplyText|ContinuationProcessorUsesUserFacingScheduleReplyText|ContinuationProcessorUsesApprovalTitleInGenericReplyText)'`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/infrastructure/lark_dal/larkmsg ./pkg/cardaction ./internal/interfaces/lark ./cmd/larkrobot ./internal/application/config ./internal/application/lark/messages`

## 2026-03-18 · Milestone AI · First Real Chat-To-Approval Agentic Loop

### 方案

- 不再只停留在 durable metadata 和 continuation fallback，而是先打通一条真实用户可见的 agentic 主链：
  - 普通 agentic chat 中，`send_message` 不再直接 inline 执行
  - 首轮 reply 先输出“已发起审批，等待确认”
  - runtime 持久化这条 reply，但不结束 run
  - 紧接着把 `send_message` 作为 queued `capability_call` 推入 continuation processor
  - continuation processor 发审批卡
  - 用户批准后 replay 原 capability，并 patch 最初那张 agentic 卡
- 目标不是一次性把所有 tool runtime 化，而是先让“patch 原卡片”在真实 chat 中可观测。

### 修改

- 新增 `internal/application/lark/runtimecontext/deferred_tool_call.go`
  - 提供 request-scoped deferred tool collector
  - 允许 tool handler 声明“本次调用延后执行”
- 修改 `internal/application/lark/handlers/chat_handler.go`
  - 仅在 `agentic + runtime cutover` 路径下注入 deferred tool collector
- 修改 `internal/application/lark/handlers/send_message_handler.go`
  - 命中 deferred collector 时不再直接发消息
  - 返回占位结果：`已发起审批，等待确认后发送消息。`
  - 同时写出 approval title / summary / expiry
- 修改 `internal/infrastructure/ark_dal/responses.go`
  - function-call trace 新增 pending approval metadata
  - 把 deferred `send_message` 作为 `pending capability trace` 暴露给 runtime cutover
- 修改 `internal/application/lark/agentruntime/reply_completion.go`
  - 新增 `ContinueRunWithReply(...)`
  - 支持“首条 reply 已发出，但 run 继续保活，并排入 queued capability_call”
- 修改 `internal/application/lark/agentruntime/continuation_processor.go`
  - capability call 现在在 `input.approval != nil` 时也会进入 approval gate
  - `RequestApproval(...)` 后会真实发送 approval card
- 修改 `internal/application/lark/agentruntime/approval_sender.go`
  - 暴露 `ApprovalSender` interface，供 continuation processor 注入
- 修改 `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  - 新增默认 `BuildContinuationProcessor(...)`
  - 默认注入 capability registry、reply emitter、approval sender
- 修改 `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - 识别 pending capability trace
  - pending capability 存在时走 `ContinueRunWithReply(...)`
  - 随后立刻触发 continuation processor，把 run 推进到 `waiting_approval`
- 修改测试
  - `internal/application/lark/agentruntime/coordinator_test.go`
  - `internal/application/lark/agentruntime/continuation_processor_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`
  - `internal/application/lark/agentruntime/types_test.go`

### 决策

- 第一条真实 runtime-owned side-effect 路径先收敛到 `send_message`；
  - 这是最直观、最容易让用户感知“不是传统 inline tool call”的入口
- approval gate 不再只依赖 capability meta；
  - 只要 queued capability input 自带 `approval` spec，就进入审批
  - 这样不会把所有默认 tool capability 一次性强制改成审批模式
- 初始 reply step 必须先持久化为 active reply，再排入 queued capability；
  - 这样 approval 通过后 continuation 才能稳定 patch 原 agentic 卡
- 当前一轮只保证单个 pending capability 的主链闭环；
  - 多 pending side-effect orchestration 仍留待后续 planner/runtime 化

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/infrastructure/ark_dal ./internal/application/lark/handlers ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/cardaction ./internal/infrastructure/agentstore ./internal/infrastructure/redis`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./cmd/larkrobot ./internal/application/lark/messages ./internal/application/lark/messages/ops`

## 2026-03-18 · Milestone AJ · Expand Deferred Approval To More Side-Effect Tools

### 方案

- 在 `send_message` 已经跑通真实 `reply -> approval -> resume -> patch` 主链后，把“延后执行 + 审批元数据”抽成 handlers 侧的通用 helper。
- 第二批先覆盖治理类 side-effect tool：
  - `config_set`
  - `config_delete`
  - `feature_block`
  - `feature_unblock`
  - `mute_robot`
- 同时把一批资料/素材类 side-effect tool 也接入同样的 deferred approval contract：
  - `word_add`
  - `reply_add`
  - `image_add`
  - `image_delete`
- 目标是让 agentic 模式下不再只有“发消息”能进入真实审批闭环，而是一批常见的 side-effect tool 都能进入同样的 runtime 主链。

### 修改

- 新增 `internal/application/lark/handlers/agentic_defer.go`
  - 提供 `tryDeferAgenticApproval(...)`
  - 统一写入 placeholder output / approval metadata / result extra
- 修改 `internal/application/lark/handlers/send_message_handler.go`
  - 改为复用通用 defer helper
- 修改 `internal/application/lark/handlers/config_handler.go`
  - `config_set` / `config_delete` / `feature_block` / `feature_unblock`
    - 命中 deferred collector 时不再直接执行副作用
    - 返回明确占位结果
    - 写出 approval title / summary
  - 同时补充 tool result encoder，使模型能拿到更具体的执行结果文本
- 修改 `internal/application/lark/handlers/mute_handler.go`
  - `mute_robot` 进入通用 deferred approval 协议
  - 区分“设置禁言 / 取消禁言”的 approval title
- 修改
  - `internal/application/lark/handlers/word_handler.go`
  - `internal/application/lark/handlers/reply_handler.go`
  - `internal/application/lark/handlers/image_handler.go`
  - `word_add / reply_add / image_add / image_delete`
    - 进入通用 deferred approval 协议
    - 补充 result encoder，让 deferred placeholder 与执行后结果都能稳定回流给模型
- 修改 `internal/application/lark/handlers/tools_test.go`
  - 新增 selected side-effect tools 的 deferred collector 行为测试

### 决策

- 第二批优先选“治理类 side-effect tool”，而不是继续扩 schedule；
  - schedule 还带有更长链路的 async/wait 语义，后续单独推进更合适
- config / feature 这些 tool 现在补了明确 result encoder；
  - 这样无论是 deferred placeholder，还是真正执行后的结果，都能稳定回流给模型
- 当前仍然采用“按工具声明 deferred approval”而不是全局强制所有 side-effect tool 一律审批；
  - 这样 rollout 风险更低，也方便逐个观察用户体感

### 验证

- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/handlers ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/infrastructure/ark_dal ./internal/application/lark/cardaction ./internal/application/lark/messages ./internal/application/lark/messages/ops ./internal/infrastructure/agentstore ./internal/infrastructure/redis`
- `env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./cmd/larkrobot`

## 2026-03-18 · Milestone AK · Schedule 写工具接入 Deferred Approval

### 方案

- 在治理类 side-effect tool 已经进入真实审批闭环之后，把 `schedule` 的写操作也接到同一条 agentic 主链上：
  - 首轮 reply 先输出“已发起审批”
  - capability 进入 runtime queue
  - approval 通过后 replay capability
  - 最终结果 patch 回原 agentic 卡片
- 这一轮只处理“创建 / 删除 / 暂停 / 恢复 schedule”的写入口，不把 scheduler 到点执行语义混进来。

### 修改

- 修改 `internal/application/lark/schedule/func_call_tools.go`
  - 新增 `tryDeferScheduleAgenticApproval(...)`
  - `create_schedule`
  - `delete_schedule`
  - `pause_schedule`
  - `resume_schedule`
    - 命中 deferred collector 时不再直接访问 schedule service
    - 返回明确 placeholder output
    - 写出 approval title / summary
- 修改 `internal/application/lark/schedule/func_call_tools_test.go`
  - 新增 schedule 写工具的 deferred collector 行为测试

### 决策

- schedule 写工具先只做“入口 defer + 审批元数据 + placeholder 回流”；
  - 不在本轮把定时执行后的 wait payload / richer schedule callback 语义一起展开
- `create_schedule` 的 approval summary 先锁定最小可读语义；
  - 当前按 once/cron 区分“单次 / 周期 schedule”
- 继续沿用“按工具显式声明 deferred approval”策略，而不是把所有 tool 一刀切进审批。

### 验证

- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/schedule -run TestScheduleWriteToolsDeferApprovalWhenCollectorPresent -count=1`
- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/schedule ./internal/application/lark/handlers`

## 2026-03-18 · Milestone AL · Todo 写工具接入 Deferred Approval

### 方案

- 沿用 `send_message / config / schedule` 已经跑通的 runtime defer contract，把 todo 写入口接入同一条 agentic 审批闭环。
- 范围先收敛在：
  - `create_todo`
  - `update_todo`
  - `delete_todo`
- `list_todos` 保持查询工具，不进入审批。

### 修改

- 修改 `internal/application/lark/todo/func_call_tools.go`
  - 新增 `tryDeferTodoAgenticApproval(...)`
  - `create_todo`
  - `update_todo`
  - `delete_todo`
    - 命中 deferred collector 时不再直接访问 todo service / 用户信息查询
    - 返回明确 placeholder output
    - 写出 approval title / summary
- 修改 `internal/application/lark/todo/func_call_tools_test.go`
  - 新增 todo 写工具的 deferred collector 行为测试

### 决策

- `update_todo` 的 approval summary 先只区分“完成待办”和“更新待办”；
  - 不在本轮展开字段级 diff 文案
- todo 写工具继续复用现有 runtime collector，不新增独立 planner protocol。

### 验证

- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/todo -run TestTodoWriteToolsDeferApprovalWhenCollectorPresent -count=1`
- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/schedule ./internal/application/lark/handlers ./internal/application/lark/todo`

## 2026-03-19 · Milestone AM · Tool Runtime Behavior Registry 收口

### 方案

- 把当前分散在：
  - handler defer helper
  - schedule/todo 局部 helper
  - capability metadata 默认值
  里的 tool runtime 真值收口到一份共享 registry。
- 本轮 registry 统一承载以下信息：
  - side effect level
  - requires approval
  - allow compatible output
  - 默认 approval result key / placeholder / title
- 不在本轮把参数相关的 summary/title 生成完全抽象化；
  - 动态 summary 仍留在各 tool handler 内生成
  - 少量动态 title（如 `mute_robot`）仍允许局部 override

### 修改

- 新增 `internal/application/lark/toolmeta/runtime_behavior.go`
  - 定义共享 `RuntimeBehavior` registry
  - 覆盖当前已接入 agentic deferred approval 的工具
- 新增 `internal/application/lark/toolmeta/deferred_approval.go`
  - 提供共享 `TryRecordDeferredApproval(...)`
- 修改 `internal/application/lark/handlers/agentic_defer.go`
  - 支持按 `tool_name` 取默认 defer approval 行为
  - 保留旧字段兜底，避免切换过程中断
- 修改
  - `internal/application/lark/handlers/*.go`
  - `internal/application/lark/schedule/func_call_tools.go`
  - `internal/application/lark/todo/func_call_tools.go`
  - 改为按 tool name 复用 registry 默认 placeholder/title/result key
- 修改 `internal/application/lark/agentruntime/capability_tools.go`
  - `defaultToolSideEffectLevel(...)`
  - `defaultToolAllowCompatibleOutput(...)`
  - 新增 `defaultToolRequiresApproval(...)`
  - `BuildToolCapabilities(...)` 现在会把 `RequiresApproval` 真值带进 capability metadata
- 修改 `internal/application/lark/agentruntime/capability_adapters_test.go`
- 新增 `internal/application/lark/toolmeta/runtime_behavior_test.go`

### 决策

- `toolmeta` 现在是 agentic tool runtime 行为的单一真值来源；
  - handler 不再重复维护 placeholder/title/result key
  - capability metadata 也不再手写平铺 switch
- 先只收口“已进入 agentic approval/runtime 协议”的工具；
  - 不把所有 query/read-only tool 一次性塞进 registry
- `RequiresApproval` 虽然当前主要依赖 queued capability 的 approval payload 驱动，但 capability meta 也必须反映真实运行语义，避免后续 planner/processor 判断分叉。

### 验证

- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/toolmeta`
- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/agentruntime ./internal/application/lark/handlers`
- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/schedule ./internal/application/lark/todo`

## 2026-03-19 · Milestone AN · 首轮与恢复收口到统一 RunProcessor

### 方案

- 解决当前 runtime “两条半截 loop”的核心割裂：
  - 首轮启动在 `runtime_chat.go / runtime_text.go`
  - 恢复续跑在 `resume_worker.go / continuation_processor.go`
- 本轮不直接把模型调用挪进 runtime；
  - 先把 **run 的 owner** 收口到同一个 processor
  - cutover handler 退化为 output adapter，只负责提供 `ProduceReply(...)`
- 统一入口 contract：
  - `ProcessRun(initial)`
  - `ProcessRun(resume)`
- `ResumeWorker` 不再知道 continuation 细节，只知道 `RunProcessor`

### 修改

- 新增 `internal/application/lark/agentruntime/run_processor.go`
  - 定义：
    - `InitialRunInput`
    - `InitialReplyResult`
    - `RunProcessorInput`
    - `RunProcessor`
  - 由 `ContinuationProcessor` 实现 `ProcessRun(...)`
- 修改 `internal/application/lark/agentruntime/resume_worker.go`
  - 改为依赖 `RunProcessor`
  - 消费 resume queue 时统一调用 `ProcessRun(resume)`
- 修改
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text.go`
  - 不再自己持有 `coordinator + continuation processor` 双 builder
  - 改为只持有单一 `processorBuilder`
  - 首轮流式输出通过 `ProduceReply(...)` 回调交给 processor
- 修改 `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  - 新增 `BuildRunProcessor(...)`
  - `BuildResumeWorker(...)` 改为注入 unified processor
- 新增 / 修改测试：
  - `internal/application/lark/agentruntime/run_processor_test.go`
  - `internal/application/lark/agentruntime/resume_worker_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text_test.go`

### 决策

- 这一步的目标是 **统一状态机 owner**，不是一步到位做成 full runtime-owned planner。
- 当前已经做到：
  - 首轮 reply 完成后，由同一个 `RunProcessor` 决定：
    - complete
    - continue with queued capability
    - 立即推进 queued capability 到 approval/wait/complete
  - 恢复事件也由同一个 `RunProcessor` 接手
- 当前仍未做到：
  - 首轮 LLM/tool streaming 仍在旧 chat generation path 里发生
  - `RunProcessor` 还不是“自己发起模型调用”的真正前半段 loop
- 也就是说，本轮是：
  - 从 `chat-first + runtime-after-reply`
  - 前进到 `chat-produces-reply, runtime-owns-run-progression`

### 验证

- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorProcessRunCompletesInitialReply|ContinuationProcessorProcessRunExecutesQueuedCapabilityAfterInitialReply|ResumeWorkerProcessesQueuedEventWithRunLock|ResumeWorkerSkipsWhenRunLockIsHeld)$'`
- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/agentruntime/runtimecutover -run 'Test(HandlerStartsRunStreamsCardAndCompletesReply|HandlerCapturesCapabilityCallTraceForCompletion|HandlerDeduplicatesCapabilityCallTraceByCallID|HandlerDelegatesPendingCapabilityThroughSingleInitialRunProcessorCall|StandardHandlerStartsRunRepliesTextAndCompletesReply|StandardHandlerSkipsSendingWhenReplyIsEmpty)$'`
- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/agentruntime/... ./internal/application/lark/handlers`

## 2026-03-19 · Milestone AO · 首轮模型调用延后到 Processor 驱动

### 方案

- 在上一轮 unified `RunProcessor` owner 的基础上，继续收口首轮前半段：
  - `chat_handler.go` 不再在进入 runtime cutover 前先执行 `GenerateChatSeq(...)`
  - cutover request 改为携带 `Generate(...)` callback
  - 由 cutover handler 内部、并最终由 `RunProcessor.ProcessRun(initial)` 触发首轮生成
- 这样首轮模型调用的时机已经不再由旧 chat path 先行决定，而是由 runtime owner 控制。
- 本轮仍不改 prompt 拼装和 Ark responses 内部执行机制；
  - 只是把“什么时候开始生成”收回 runtime 一侧

### 修改

- 修改 `internal/application/lark/handlers/chat_runtime_cutover.go`
  - `RuntimeAgenticCutoverRequest`
  - `RuntimeStandardCutoverRequest`
  - 从携带 `Stream` 改为携带 `Generate(...)`
- 修改 `internal/application/lark/handlers/chat_handler.go`
  - cutover 命中时不再预先生成 stream
  - 只把 generation callback 交给 runtime cutover
- 修改
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text.go`
  - fallback 直发路径和 unified processor 路径都改为按需调用 `req.Generate(ctx)`
- 修改测试：
  - `internal/application/lark/handlers/chat_handler_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text_test.go`
  - 新增“只有 runtime handler 消费 generator 时才会触发生成”的断言

### 决策

- 现在已经做到：
  - 进入 runtime cutover 后，`chat_handler` 不再先做首轮 LLM 生成
  - 首轮 generation callback 的执行时机由 runtime cutover / processor 决定
- 但还没有做到：
  - prompt 构建、Ark responses tool orchestration、stream capture 完全搬入 `RunProcessor`
  - 当前仍然是“旧 chat generation logic 作为 callback 被 runtime 调用”
- 所以当前状态应理解为：
  - `RunProcessor` 已经拥有首轮 generation 的触发权
  - 但还没拥有首轮 generation 的全部内部实现

### 验证

- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/handlers -run 'Test(HandleAgenticChatResponseDelegatesToRuntimeCutover|HandleStandardChatResponseDelegatesToRuntimeCutover|HandleAgenticChatResponseDefersGenerationUntilRuntimeHandlerConsumesIt|HandleStandardChatResponseDefersGenerationUntilRuntimeHandlerConsumesIt)$'`
- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/agentruntime/runtimecutover`
- `BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/application/lark/agentruntime/... ./internal/application/lark/handlers`

## 2026-03-19 · Milestone AP · Cutover Request 改为显式 Generation Plan

### 方案

- 在 AO 的基础上继续收口首轮启动接口：
  - 不再把旧 chat generation 藏在临时 `Generate(...)` callback 里
  - cutover request 改为携带显式 `ChatGenerationPlan`
  - runtime cutover / processor 统一执行 `plan.Generate(ctx, event)`
- 本轮仍然复用旧 `GenerateChatSeq(...)` 的 prompt build 和 Ark execution；
  - 只是把“生成所需配置”从匿名 closure 提升为显式 DTO

### 修改

- 新增 `internal/application/lark/handlers/chat_generation_plan.go`
  - 定义 `ChatGenerationPlan`
  - 定义 `ChatGenerationPlanExecutor`
  - 提供默认 executor 和测试替换入口
- 修改 `internal/application/lark/handlers/chat_handler.go`
  - 提取 `buildChatGenerationPlan(...)`
  - cutover 前只构造 plan，不再传递 ad hoc callback
- 修改 `internal/application/lark/handlers/chat_runtime_cutover.go`
  - `RuntimeAgenticCutoverRequest`
  - `RuntimeStandardCutoverRequest`
  - 从 `Generate(...)` 切到 `Plan`
- 修改
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text.go`
  - fallback 直发路径和 unified processor 路径都统一执行 `req.Plan.Generate(ctx, req.Event)`
- 修改测试：
  - `internal/application/lark/handlers/chat_handler_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text_test.go`
  - 新增 plan laziness / executor 替换断言

### 决策

- cutover 接口现在已经显式表达了：
  - model
  - history size
  - image files
  - args
  - deferred tool collector 开关
- 当前状态应理解为：
  - `RunProcessor` 已经拥有“何时开始首轮生成”的控制权
  - `ChatGenerationPlan` 已经拥有“首轮生成需要什么配置”的显式 contract
  - 但“如何生成”内部仍复用旧 `GenerateChatSeq(...)`

### 验证

- `env GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/handlers -run 'Test(HandleAgenticChatResponseDelegatesToRuntimeCutover|HandleStandardChatResponseDelegatesToRuntimeCutover|HandleAgenticChatResponseDefersGenerationUntilRuntimeHandlerConsumesIt|HandleStandardChatResponseDefersGenerationUntilRuntimeHandlerConsumesIt)$'`
- `env GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/runtimecutover`

## 2026-03-19 · Milestone AQ · Initial Reply Contract 改为显式 Executor

### 方案

- 在 AP 的基础上继续去掉 runtime 首轮路径里的匿名执行块：
  - `RunProcessor` 不再依赖 `ProduceReply` 闭包
  - 改为依赖显式 `InitialReplyExecutor`
  - `runtimecutover` 内新增命名执行器，统一封装：
    - `plan.Generate(...)`
    - `output.emit(...)`
    - `buildInitialReplyResult(...)`
- 这样首轮 initial step 的执行 contract 已经从“匿名 callback”进一步收口为“runtime 可识别的命名对象”。

### 修改

- 修改 `internal/application/lark/agentruntime/run_processor.go`
  - 新增 `InitialReplyExecutor`
  - `InitialRunInput` 从 `ProduceReply` 切到 `Executor`
- 新增 `internal/application/lark/agentruntime/runtimecutover/initial_reply_executor.go`
  - 封装 plan-driven initial reply 执行器
  - 同时提供 direct emit 与 `ProduceInitialReply(...)`
- 修改
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_text.go`
  - 不再内联匿名 `ProduceReply`
  - 统一改为构造 `initialReplyExecutor`
- 修改测试：
  - `internal/application/lark/agentruntime/run_processor_test.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_chat_test.go`

### 决策

- 当前首轮 initial path 已经完成两次收口：
  - 先从“chat handler 预执行”改为“runtime 触发”
  - 再从“匿名 callback”改为“显式 plan + 显式 executor”
- 这仍然不是最终形态：
  - prompt build
  - Ark responses execution
  - tool orchestration interception
  仍然没有进入 `agentruntime` 包内部
- 但现在下一步要把这些逻辑继续下沉时，已经有稳定挂点，不需要先拆 handler 里的 closure。

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessorProcessRun(CompletesInitialReply|ExecutesQueuedCapabilityAfterInitialReply)$'`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/runtimecutover`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/... ./internal/application/lark/handlers`

## 2026-03-19 · Milestone AR · Prompt Build 与 Ark Execution 脱离 Handlers 主文件

### 方案

- 在 AQ 的基础上继续把首轮生成的“真实实现”从 `handlers/chat_handler.go` 挪走：
  - 新增 `agentruntime.InitialChatGenerationRequest`
  - 新增 `agentruntime.GenerateInitialChatSeq(...)`
  - `handlers.GenerateChatSeq(...)` 退化成兼容 wrapper
  - `defaultChatGenerationPlanExecutor` 直接调用 `agentruntime.GenerateInitialChatSeq(...)`
- 同时把 `BuildDefaultLarkToolCapabilities()` 从 base `agentruntime` 包挪到 `runtimewire`：
  - 去掉 base `agentruntime -> handlers` 依赖
  - 为后续继续把 plan / cutover contract 下沉到 runtime 清掉循环依赖障碍

### 修改

- 新增 `internal/application/lark/agentruntime/initial_chat_generation.go`
  - 承载 prompt template 构造
  - 承载 history / retriever / topic 补全
  - 承载 Ark responses 调用
  - 承载最终 JSON repair / mention rewrite 收尾
- 修改 `internal/application/lark/handlers/chat_handler.go`
  - `GenerateChatSeq(...)` 退化为 request builder + delegator
- 修改 `internal/application/lark/handlers/chat_generation_plan.go`
  - 默认 plan executor 直接走 `agentruntime.GenerateInitialChatSeq(...)`
- 修改 `internal/application/lark/agentruntime/capability_tools.go`
  - 去掉默认 Lark tool capability 构建
- 修改 `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  - 在 wiring 层接管默认 Lark tool capability 装配
- 新增测试：
  - `internal/application/lark/agentruntime/runtimewire/runtimewire_test.go`
  - `internal/application/lark/handlers/chat_handler_test.go` 中新增 `GenerateChatSeq` 委托断言

### 决策

- 当前首轮 initial path 又向 runtime core 收了一步：
  - `handlers` 已不再拥有 prompt build / Ark execution 的主实现
  - `defaultChatGenerationPlanExecutor` 也不再经由 `handlers.GenerateChatSeq(...)`
  - `ChatGenerationPlan` 类型所有权也已下沉到 base `agentruntime`，`handlers` 仅保留兼容别名与 executor 注册
  - `Runtime*CutoverRequest` 与 cutover handler interface 也已下沉到 base `agentruntime`
  - `ChatGenerationPlanExecutor` contract 也已进入 base `agentruntime`
- 但还没完全完成的点仍然是：
  - `runtimecutover` 生产代码虽然已不再依赖 `handlers`，但默认 generator 注册仍通过 `handlers` 兼容层衔接
- 所以现在的状态应理解为：
  - 首轮生成实现已经进入 base `agentruntime`
  - `ChatGenerationPlan` 也已经进入 base `agentruntime`
  - cutover request contract 也已经进入 base `agentruntime`
  - 但默认 generator 装配仍没有完全脱离 `handlers`

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/runtimewire -run TestBuildDefaultLarkToolCapabilitiesIncludesUserFacingToolsOnly`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/handlers -run 'TestGenerateChatSeqDelegatesToAgentRuntimeGenerator|Test(HandleAgenticChatResponseDelegatesToRuntimeCutover|HandleStandardChatResponseDelegatesToRuntimeCutover)$'`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/... ./internal/application/lark/handlers`

## 2026-03-19 · Milestone AS · Runtime Wiring 回到组合根

### 方案

- 在 AR 的基础上继续清理隐藏耦合：
  - `runtimewire` 不再静态 import `handlers.BuildLarkTools()`
  - 默认 capability provider 改为由组合根显式注入
  - 默认 `ChatGenerationPlanExecutor` 也不再靠 `handlers.init()` 偷偷注册
  - 改为由 `cmd/larkrobot/bootstrap.go` 显式 wiring
- 这样 runtime 生产代码与 handlers 的关系收敛为：
  - 组合根可以选择用 handlers 里的默认实现来装配 runtime
  - 但 `agentruntime` / `runtimecutover` / `runtimewire` 本身不再静态依赖 handlers

### 修改

- 修改 `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  - 新增 `SetDefaultCapabilityProvider(...)`
  - `buildDefaultCapabilityRegistry()` 改为消费注入 provider
- 修改 `internal/application/lark/agentruntime/runtimewire/runtimewire_test.go`
  - 改为校验 injected provider 行为
- 修改 `internal/application/lark/agentruntime/chat_generation_plan.go`
  - `ChatGenerationPlanExecutor` contract 进入 base runtime
- 修改 `internal/application/lark/handlers/chat_generation_plan.go`
  - 去掉 `init()` 默认注册
  - 新增 `NewDefaultChatGenerationPlanExecutor()`
  - `SetChatGenerationPlanExecutor(nil)` 不再偷偷恢复默认实现
- 修改 `cmd/larkrobot/bootstrap.go`
  - 显式设置：
    - runtime cutover builders
    - default chat generation executor
    - default capability provider

### 决策

- 当前 runtime 生产代码已经达成：
  - `agentruntime` 基础包不静态依赖 `handlers`
  - `runtimecutover` 不静态依赖 `handlers`
  - `runtimewire` 不静态依赖 `handlers`
- `handlers` 现在主要承担：
  - 用户命令 / tool 实现
  - chat entry 兼容层
  - 默认 generation executor 实现
- 这意味着当前最大的剩余“runtime 还没完全自治”的点，已经从 contract/ownership 问题，缩小为：
  - 默认 toolset 是否要继续从 handlers 中抽成更独立的能力装配模块
  - chat entry wrapper 是否还要继续下沉

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/handlers -run 'TestChatGenerationPlanGenerateReturnsNotConfiguredWithoutRegisteredExecutor|TestGenerateChatSeqDelegatesToAgentRuntimeGenerator|Test(HandleAgenticChatResponseDelegatesToRuntimeCutover|HandleStandardChatResponseDelegatesToRuntimeCutover)$'`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/runtimewire ./internal/application/lark/agentruntime/... ./internal/application/lark/handlers`

## 2026-03-19 · Milestone K · 首轮 Chat Entry 编排下沉到 Runtime

### 方案

- 沿着“不要再保留第三条半截 loop”的思路，继续把首轮 chat 入口从 `handlers` 往 `agentruntime` 收口。
- 这一轮不动已有 cutover/output adapter，也不改 tool execution 行为，只抽走首轮入口里原本散在 `chat_handler.go` 的编排职责：
  - chat mode 判定
  - mute guard
  - message / quote image 收集
  - model 选择
  - `ChatGenerationPlan` 组装
  - agentic / standard responder 分流
- `handlers` 保留成兼容层：
  - CLI 参数解析
  - event adapter
  - 默认 responder wrapper

### 修改

- 新增 `internal/application/lark/agentruntime/chat_entry.go`
  - 定义 `ChatEntryHandler`
  - 定义 `ChatResponseRequest`
  - 将首轮 chat 编排迁入 runtime
- 新增 `internal/application/lark/agentruntime/chat_entry_test.go`
  - 锁住 response mode 判定
  - 锁住 mute skip 语义
  - 锁住 mention 绕过 mute 的语义
  - 锁住 reasoning model / deferred collector / responder 分流
- 新增 `internal/application/lark/mutestate/mute.go`
  - 提供中性 `RedisKey(chatID)` helper，避免 runtime 反向依赖 `handlers`
- 修改 `internal/application/lark/handlers/chat_handler.go`
  - `defaultRuntimeChatHandler()` 退化为对 `agentruntime.ChatEntryHandler` 的调用
- 修改 `internal/application/lark/handlers/chat_handler_test.go`
  - 去掉对 handler 内部 response mode 实现细节的假设
- 修改 `internal/application/lark/handlers/mute_handler.go`
  - `MuteRedisKey()` 改为复用 `mutestate.RedisKey()`

### 决策

- 这一轮的目标不是“把所有默认 chat 行为都完全从 handlers 拔掉”，而是先把首轮入口编排所有权移动到 runtime。
- 为了降低切分风险，agentic / standard 的实际输出发送仍沿用现有 responder wrapper：
  - `handleAgenticChatResponse(...)`
  - `handleStandardChatResponse(...)`
- 这样得到的新边界是：
  - `handlers.ChatHandlerInner(...)` 只负责输入适配
  - `agentruntime.ChatEntryHandler` 负责首轮编排
  - runtimecutover / continuation 继续负责 durable run 与后续推进
- 剩余待收口点进一步缩小为：
  - 默认 toolset 装配是否继续从 `handlers/tools.go` 抽离

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime ./internal/application/lark/handlers -run 'Test(ResolveChatResponseMode|ChatEntryHandler|ChatHandlerInnerDelegatesToRuntimeChatHandler|ShouldUseRuntimeChatCutover|HandleAgenticChatResponse|HandleStandardChatResponse|GenerateChatSeqDelegatesToAgentRuntimeGenerator|ChatGenerationPlanGenerateReturnsNotConfiguredWithoutRegisteredExecutor)$'`

## 2026-03-19 · Milestone L · 默认 Responder 与 Default Generator 所有权继续回收到 Runtime

### 方案

- 在 Milestone K 的基础上继续收口，把 `handlers` 中剩余的默认 chat responder dispatch 和 default generator 所有权都迁到 `agentruntime`。
- 目标是让 runtime 自己拥有：
  - agentic / standard responder dispatch
  - cutover builder wiring 点
  - fallback sender
  - default `ChatGenerationPlanExecutor`
- `handlers` 保留的只是兼容入口：
  - `ChatHandlerInner(...)`
  - `GenerateChatSeq(...)` 兼容 wrapper
  - `BuildLarkTools()` 默认工具集合提供者

### 修改

- 新增 `internal/application/lark/agentruntime/chat_response.go`
  - 将 `shouldUseRuntimeChatCutover(...)`
  - `handleAgenticChatResponse(...)`
  - `handleStandardChatResponse(...)`
  - cutover builder / fallback sender 默认 wiring
    全部迁入 runtime
- 新增 `internal/application/lark/agentruntime/chat_response_test.go`
  - 锁住 runtime cutover delegation
  - 锁住 generation laziness
  - 锁住 agentic / standard fallback 语义
- 新增 `internal/application/lark/agentruntime/default_chat_generation_executor.go`
  - 提供 runtime-owned `NewDefaultChatGenerationPlanExecutor()`
  - 提供 `SetDefaultChatToolProvider(...)`
- 新增 `internal/application/lark/agentruntime/default_chat_generation_executor_test.go`
  - 锁住 default tool provider 注入
  - 锁住 deferred collector 初始化语义
- 修改 `internal/application/lark/handlers/chat_handler.go`
  - `defaultChatEntryHandler` 改为直接复用 `agentruntime.NewDefaultChatEntryHandler()`
- 删除 `internal/application/lark/handlers/chat_runtime_cutover.go`
  - 旧 responder dispatch 不再留在 handlers
- 修改 `internal/application/lark/handlers/chat_handler_test.go`
  - 去掉已迁往 runtime 的 responder/cutover 相关测试
- 修改 `internal/application/lark/handlers/chat_generation_plan.go`
  - `NewDefaultChatGenerationPlanExecutor()` 退化为 runtime wrapper
- 修改 `cmd/larkrobot/bootstrap.go`
  - 改为直接设置：
    - `agentruntime.SetRuntimeAgenticCutoverBuilder(...)`
    - `agentruntime.SetRuntimeStandardCutoverBuilder(...)`
    - `agentruntime.SetDefaultChatToolProvider(handlers.BuildLarkTools)`
    - `agentruntime.SetChatGenerationPlanExecutor(agentruntime.NewDefaultChatGenerationPlanExecutor())`

### 决策

- 现在 runtime 已经拥有“首轮消息从入口到默认 responder/default generator”的主要控制权。
- 组合根仍然负责装配默认 tool provider，因为默认工具集合本身还在 `handlers/tools.go`。
- 这样新的边界进一步收紧为：
  - `handlers`：
    - 输入适配
    - 默认工具集合兼容提供
    - 少量兼容 wrapper
  - `agentruntime`：
    - 首轮 chat 编排
    - responder dispatch
    - cutover/fallback output 入口
    - default generation executor
    - durable run / continuation / resume
- 当前最大剩余的“理想 agentic 架构”缺口，已经更明确地集中在：
  - 默认 toolset 是否继续从 `handlers/tools.go` 抽到更中性的模块
  - Ark responses inline tool execution 是否升级为更统一的 runtime interception/planning loop
  - richer wait payload protocol

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime ./internal/application/lark/handlers -run 'Test(DefaultChatGenerationPlanExecutor|ResolveChatResponseMode|ChatEntryHandler|ShouldUseRuntimeChatCutover|HandleAgenticChatResponse|HandleStandardChatResponse|ChatHandlerInnerDelegatesToRuntimeChatHandler|GenerateChatSeqDelegatesToAgentRuntimeGenerator|ChatGenerationPlanGenerateReturnsNotConfiguredWithoutRegisteredExecutor)$'`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/... ./internal/application/lark/handlers ./internal/application/lark/messages/ops`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./cmd/larkrobot`

## 2026-03-19 · Milestone M · Active Run Ownership 真正接入 Shadow Policy

### 方案

- 继续优先推进“理想 agentic 功能态”，不再把重点放在继续拆包。
- 当前 policy 其实早已定义：
  - follow-up window 内 attach 到 active run
  - mention / `/bb` supersede active run
- 但之前 `AgentShadowOperator` 创建 `ShadowObserver` 时没有提供 active run snapshot，导致这套 ownership 语义在运行时实际上没有生效。
- 本轮把 active run snapshot lookup 真正接入 observer，让：
  - `follow_up -> AttachToRunID`
  - `mention -> SupersedeRunID`
  从“policy 设计”变成“真实落到 `StartShadowRunRequest` 的行为”。

### 修改

- 修改 `internal/application/lark/messages/ops/agent_op.go`
  - `NewAgentShadowOperator()` 改为把 `activeRunSnapshot()` closure 注入 `ShadowObserver`
  - 增加 active run snapshot provider detection
  - 从 runtime coordinator 获取当前 chat 的 active run snapshot
- 修改 `internal/application/lark/agentruntime/coordinator.go`
  - 新增 `ActiveRunSnapshot(ctx, chatID)`，基于 active chat slot + run repo 返回：
    - run id
    - actor open id
    - run status
    - last active time
- 修改 `internal/application/lark/messages/ops/agent_op_persist_test.go`
  - 新增 follow-up attach 用例
  - 新增 mention supersede 用例

### 决策

- 这一步先不强推消息主链全面 cutover，只先把“active run ownership”这条最关键的 runtime 语义打通。
- 这样即便首轮/恢复链路已经 durable，policy 也终于开始真正识别“当前对话里有没有一个正在活着的 run”。
- 这对后续两个方向都是必要前置：
  - 真正的 ambient group-chat runtime trigger
  - 同一轮 agentic interaction 的 follow-up / supersede 行为

### 验证

- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops -run 'TestNewAgentShadowOperator(AttachesFollowUpToActiveRun|SupersedesActiveRunOnMention)'`
- `env BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/handlers`

## 2026-03-20 · Milestone N · Generic Continuation Re-Enter And Resume Payload Protocol

### 方案

- 把 callback / schedule 的 generic continuation 继续往“理想 agentic”推进，而不是停留在 template-like terminal reply：
  - generic continuation 也通过默认 `ContinuationReplyTurnExecutor` 重新进入模型 turn
  - continuation turn 内允许继续 tool loop
  - 如有需要，可以在 continuation 中追加 completed capability steps，或者再排一个新的 pending capability
- 同时补上更完整的 resume payload protocol：
  - `ResumeEvent` / Redis resume queue / scheduler internal tool `agent_runtime_resume` 支持 `summary + payload_json`
  - continuation observe step 与 continuation reply turn prompt 都能消费这份恢复上下文

### 修改

- 修改 [resume_event.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/resume_event.go)
  - `ResumeEvent` 增加 `summary` / `payload_json`
- 修改 [continuation_reply_turn.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_reply_turn.go)
  - `ContinuationReplyTurnRequest` 增加 resume summary / payload
- 修改 [default_continuation_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go)
  - continuation prompt 现在显式携带恢复摘要与恢复 payload
- 修改 [continuation_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor.go)
  - generic continuation observe step 现在持久化 `summary` / `payload_json`
  - generic continuation thought / result summary 也能消费 resume summary
  - 执行 continuation reply turn 时，会把 resume summary / payload 透传给模型层
- 修改 [runtimewire.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimewire/runtimewire.go)
  - runtime/app 与 Redis DTO 之间映射新的 resume payload 字段
- 修改 [func_call_tools.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/schedule/func_call_tools.go)
  - `agent_runtime_resume` tool 增加 `summary` / `payload_json`
- 修改 [agentruntime.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/redis/agentruntime.go)
  - Redis resume queue DTO 增加 `summary` / `payload_json`
- 新增/修改测试
  - [default_continuation_reply_turn_executor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor_test.go)
  - [continuation_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor_test.go)
  - [func_call_tools_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/schedule/func_call_tools_test.go)
  - [resume_dispatcher_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/resume_dispatcher_test.go)
  - [resume_worker_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/resume_worker_test.go)

### 决策

- 这一步继续不扩 schema；resume payload 只存在于应用层 event / queue / step observe 中。
- scheduler internal tool 是第一批 payload producer；
  - callback/card action 侧后续如果有更丰富恢复上下文，可以复用同一协议
- generic continuation 现在已经不是“只能 source-level fallback”的 terminal slice；
  - 它也能真正 re-enter 模型、继续 tool loop、并把后续结果 durable 化
- 当前剩余的关键缺口进一步收窄为：
  - multi-turn tool loop 仍然是在单次 initial/resume invocation 内内聚执行，不是每一轮都 durable 落 step 再继续
  - richer wait payload protocol 虽然已经有了统一 event contract，但真实 producer 还主要是 scheduler internal tool
  - runtime 自己发起 `waiting_schedule / waiting_callback` 的入口仍然没有完全产品化

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime ./internal/application/lark/schedule -run 'Test(BuildContinuationReplyTurnUserPromptIncludesResumeSummaryAndPayload|ContinuationProcessorPassesResumeSummaryAndPayloadToContinuationReplyTurnExecutor|AgentRuntimeResumeHandleEnqueuesResumeEvent|ResumeDispatcherDispatchResumesRunAndEnqueuesEvent|ResumeWorkerProcessesQueuedEventWithRunLock)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/schedule ./internal/application/lark/cardaction ./internal/infrastructure/ark_dal ./internal/infrastructure/redis ./cmd/larkrobot`

## 2026-03-20 · Milestone O · Durable Capability Trace 补齐 PreviousResponseID

### 方案

- 在 full durable reasoning loop 之前，先把 completed capability trace 的 response-chain 信息补齐。
- 之前只有 pending capability 会把 `previous_response_id` 保存在 queued capability input 里；
  - completed capability step 丢失了这段链路信息
- 这会让后续想把 initial/continuation 的 multi-turn tool loop 真正拆成 per-turn durable step 时，缺少最关键的模型续写锚点。

### 修改

- 修改 [reply_completion.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/reply_completion.go)
  - `CompletedCapabilityCall` 增加 `previous_response_id`
  - `newCompletedCapabilityStep(...)` 在有 `previous_response_id` 时，会把它写进 `capability_call.input.continuation.previous_response_id`
- 修改 [runtime_chat.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_chat.go)
  - 首轮 cutover stream capture 现在会把 completed capability 的 `previous_response_id` 一并带回 runtime
- 修改 [default_capability_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_capability_reply_turn_executor.go)
  - capability continuation re-enter 时，nested completed capability 也保留 `previous_response_id`
- 修改 [default_continuation_reply_turn_executor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go)
  - generic continuation re-enter 时，nested completed capability 也保留 `previous_response_id`
- 新增/修改测试
  - [run_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/run_processor_test.go)
  - [continuation_processor_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor_test.go)

### 决策

- 这一步仍然不等于“每轮都 durable 落 step 再继续”，但它把后续真正拆 loop 所需的链路信息补齐了。
- 现在 initial reply、capability continuation、generic continuation 三条路径上的 completed capability step，都不再丢失原模型链的 `previous_response_id`。
- 当前剩余关键缺口更明确了：
  - 还没有在 tool turn 发生当下就持久化 step
  - 但持久化后的 step 已经开始具备后续恢复/重放所需的 response-chain 信息

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'Test(ContinuationProcessorProcessRunPersistsCompletedCapabilityPreviousResponseID|ContinuationProcessorQueuesFollowUpPendingCapabilityFromContinuationReplyTurnExecutor)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/handlers ./internal/application/lark/schedule ./internal/application/lark/cardaction ./internal/infrastructure/ark_dal ./internal/infrastructure/redis ./cmd/larkrobot`

## 2026-03-24 · Milestone P · `chat + actor` 并发隔离与 pending 初始队列

### 方案

- 现有 runtime 的 active ownership 是 `chat` 单槽：
  - 同一群里不同用户会互相 cancel / supersede
  - 同一用户连续触发第二个 agentic 任务时，也会直接打断第一个 run
  - 审批 reservation 在 run 被抢占后会命中 `run unavailable for reservation`
- 这一轮改为：
  - active slot 下沉到 `chat_id + actor_open_id`
  - `mention` / `/bb` 不再默认 supersede 当前 active run
  - 同一用户在同一 chat 触发第二个 agentic 任务时，改走 pending 队列
  - slot 释放后由独立 worker 自动拉起 queued initial run

### 修改

- 修改 [policy.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/policy.go)
  - `mention` / `command_bridge` 不再返回 `supersede_active_run`
  - follow-up / reply-to-bot 仍然 attach 到同 actor 的 active run
- 修改 [shadow.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/shadow.go)
- 修改 [agent_op.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/ops/agent_op.go)
- 修改 [runtime_route.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/ops/runtime_route.go)
  - active snapshot provider 改为 actor-scoped，而不是 chat-scoped
- 修改 [coordinator.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/coordinator.go)
- 修改 [reply_completion.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/reply_completion.go)
- 修改 [continuation_processor.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/continuation_processor.go)
  - `RunCoordinator` 改为抢占/刷新 `chat + actor` slot
  - 同 actor 的第二个 active run 直接返回 `ErrRunSlotOccupied`
  - run 完成 / 取消 /清理 active slot 后，会通知 pending initial queue
- 修改 [repository.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/agentstore/repository.go)
  - `RunRepository` 新增 `FindLatestActiveBySessionActor(...)`
  - 在 Redis 不可用时，DB fallback 也能按 actor 维度找 active run
- 修改 [agentruntime.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/redis/agentruntime.go)
  - 新增 actor-scoped active slot
  - 新增 pending initial run list
  - 新增 pending initial scope wakeup queue
  - 新增 pending initial scope lock
- 新增 [initial_run_queue.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_run_queue.go)
  - 定义 queued initial run payload、root target 与 event snapshot
- 新增 [initial_run_worker.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_run_worker.go)
  - 持续消费 pending initial scope queue
  - 在 slot 空闲时恢复 queued initial run，并 seed pending root card 作为 root target
- 修改 [runtime_chat.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimecutover/runtime_chat.go)
  - busy actor slot 时，不再直接报错
  - 先 reply 一张紧凑 pending root agentic 卡
  - 再把 initial request 入队
  - 如果命中队列上限，会 patch 同一张 root 卡提示“队列已满”
- 修改 [runtimewire.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/runtimewire/runtimewire.go)
- 修改 [bootstrap.go](/mnt/RapidPool/workspace/BetaGo_v2/cmd/larkrobot/bootstrap.go)
  - runtimewire 新增 pending queue adapter 与 worker 装配
  - 进程启动时额外拉起 `agent_runtime_pending_initial_worker`

### 决策

- 当前仍然限制为：
  - 每个 `chat + actor` 最多 `1` 个 active run
  - 额外 `3` 个 pending initial run
- pending 队列的 user-visible root 语义是：
  - queued 时立即 reply 一张 root agentic 卡
  - 真正开始执行后 patch 这张 root 卡，而不是新发 root
- 这一步没有引入更重的 DB schema 变更；
  - actor-scoped active ownership 主要靠 Redis
  - DB fallback 只补 actor 维度的 active run 查询
- agentic streaming card 的 `sequence` 约束是按同一张 `card_id` 生效，不是整个 chat / run / 会话共享，也不该按 `message_id` 理解：
  - 同一个 root agentic 卡如果后续继续 patch，必须沿用同一条单调递增 sequence
  - 不同 `card_id` 之间的 sequence 彼此独立
  - 因此“queued 时先发 pending root，slot 释放后再 patch 同一张 root 卡”的路径，必须把 sequence state 也视为 root card 的持久状态之一

### 补充修复

- 在 pending initial run 恢复链路里，出现过 Feishu `300317 sequence number compare failed`：
  - 现象上看像是 pending 队列没有恢复
  - 实际上是 queued run 已经恢复，但在 patch 既有 root agentic 卡时，把 card streaming sequence 又从 `1` 开始发送
  - 由于 Feishu 对同一 `card_id` 的 streaming update 要求 sequence 单调递增，第二轮 patch 会被直接拒绝
- 修复落在 [streaming_agentic.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkmsg/streaming_agentic.go)：
  - `streamAgentCardContent(...)` 不再对每轮 patch 固定从 `1` 起步
  - 改为按 `card_id` 分配连续 sequence
  - 优先使用 Redis 维护 card-scoped counter
  - Redis 不可用时退回进程内 card-scoped counter
- 这条修复的边界是：
  - 目标不是让整个 runtime 共用一条 sequence
  - 而是保证“同一张被复用的 root card”在多轮 patch 之间 sequence 连续
- 另一个会让用户感觉“slot 明明释放了，但 pending 还是没自动开始”的竞态在 pending initial worker：
  - worker 收到 scope wakeup 后，如果 `ProcessRun(...)` 再次命中 `ErrRunSlotOccupied`
  - 旧逻辑只会把 pending item `prepend` 回队头
  - 但不会再次发送 scope wakeup
  - 结果就是这条 pending item 仍在 list 里，却没有新的 worker 消费机会
- 修复落在 [initial_run_worker.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_run_worker.go)：
  - `ErrRunSlotOccupied` 分支在 requeue 后不会直接丢回 idle
  - 而是在释放 scope lock 之后重新 `NotifyPendingInitialRun(chat_id, actor_open_id)`
  - 这样后续 slot 真正可用时，worker 能再次自动拉起这条 pending run
- 继续向前收口后，pending initial queue 的正确性语义也从“纯事件驱动”改成“list + scope index 的 level-driven backstop”：
  - 旧模型里 `pending_initial_scope_queue` 只是一个 wakeup hint
  - 一旦 wakeup 被过早消费，或者 worker 消费时 scope 仍 busy，就可能出现 list 里还有 pending item，但之后再也没人触发恢复
  - 用户侧感知就是“slot 释放了，但 queued run 没自动开始”
- 本轮新增的事实来源与恢复路径是：
  - [agentruntime.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/redis/agentruntime.go)
    - 新增 `pending_initial_scope_index`
    - enqueue / prepend pending item 时，会同时把 `chat_id + actor_open_id` 写入 scope index
    - worker / sweeper 在确认 scope queue 为空时，才会清理这条 index
  - [initial_run_worker.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/initial_run_worker.go)
    - 成功消费最后一条 pending item 后，会清理 scope index
    - 收到陈旧 wakeup 且 scope queue 已空时，也会清理陈旧 index
  - [pending_scope_sweeper.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/pending_scope_sweeper.go)
    - 新增后台 sweeper，周期性扫描 indexed scope
    - `pending_initial_scope_queue` 仍保留为低时延 fast path
    - sweep 只负责：
      - queue 空了就清陈旧 index
      - slot 仍 busy 就跳过
      - slot 空闲且 queue 非空就重新 `NotifyPendingInitialRun(...)`
    - sweep 不直接 `ProcessRun(...)`，因此不会绕过既有 scope lock / slot guard
- 这一步之后，pending initial 恢复的边界明确成：
  - `pending_initial_run_list` 才是 source of truth
  - `BLPop pending_initial_scope_queue` 只是低时延 hint
  - scope index + periodic sweep 才是“slot 释放后最终一定会恢复”的 correctness backstop
- 同时补了最小可观测性：
  - [metrics.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/agentruntime/metrics.go)
    - 记录 enqueue / wakeup / worker / sweep 计数
    - 暴露 pending scope 数、pending run 数，以及 worker wait time 汇总
  - [health_http.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/health_http.go)
    - 管理面新增 `/metrics`
    - 除了 runtime live / ready / degraded 外，也会输出 pending initial metrics
  - [bootstrap.go](/mnt/RapidPool/workspace/BetaGo_v2/cmd/larkrobot/bootstrap.go)
    - 进程启动时额外拉起 `agent_runtime_pending_scope_sweeper`

### 验证

- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'Test(RunCoordinatorStartShadowRunAllowsDifferentActorsInSameChat|RunCoordinatorStartShadowRunRejectsSecondActiveRunForSameActor|PendingInitialRunWorkerProcessesQueuedInitialRun|PendingInitialRunWorkerRequeuesWhenProcessorReportsSlotOccupied)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'Test(PendingInitialRunWorkerRetriesAfterSlotBecomesAvailable|RunCoordinatorCompleteRunWithReplyNotifiesPendingInitialWorker)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/runtime ./internal/application/lark/agentruntime -run 'Test(HealthHTTPModuleHandleMetrics.*|PendingScopeSweeper.*|PendingInitialMetricsProviderPrometheusMetricsIncludesCountersAndBacklog)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/infrastructure/redis ./internal/application/lark/agentruntime ./internal/application/lark/agentruntime/runtimecutover ./internal/application/lark/messages/ops ./internal/application/lark/agentruntime/runtimewire -run 'Test(AgentRuntimeActiveActorChatSlotIsIsolatedByActor|AgentRuntimePendingInitialRunQueueIsScopedAndFIFO|RunCoordinatorStartShadowRunAllowsDifferentActorsInSameChat|RunCoordinatorStartShadowRunRejectsSecondActiveRunForSameActor|HandlerQueuesInitialRunWhenSameActorSlotIsBusy)'`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml go test ./internal/application/lark/agentruntime/... ./internal/application/lark/messages/ops/... ./internal/infrastructure/redis/... ./cmd/larkrobot/...`
- `env GOCACHE=/mnt/RapidPool/workspace/BetaGo_v2/.codex-gocache GOTMPDIR=/mnt/RapidPool/workspace/BetaGo_v2/.cache/gotmp BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml MOCKEY_CHECK_GCFLAGS=false go test ./internal/infrastructure/lark_dal/larkmsg -count=1`

## 当前边界

当前已经落地的是“agent runtime contract + agentic reply surface + shadow ingress + durable shadow skeleton + 默认 durable coordinator 装配 + resume contract + run-aware callback ingress + resume worker + schedule continuation ingress + terminal continuation processor skeleton + approval waiting contract + approval card sender/reject path + approval resolved-card callback UX + capability-aware continuation planner slice + approval replay to pending capability + agentic chat runtime cutover entry + runtime message/card reference tracking + real chat capability trace persistence + standard chat runtime cutover entry + runtime-owned output orchestration + continuation-driven reply engine + generic continuation reply contract + business-grade generic continuation semantics + payload-aware generic continuation context + agentic continuation existing-card patch path + standard continuation existing-message patch path + reply delivery metadata persistence + patched continuation payload-aware reply body semantics + reply-step patch lineage + reverse patch lineage on prior reply step + reply lifecycle state + non-destructive patch backfill + reply-step supersede transition for new terminal replies + active-only reply target selection + approval-title-aware generic continuation reply + wait-title-aware callback/schedule continuation reply”，还没有落地的是：

- generic continuation 已经能重新进入模型 turn，callback / schedule 也已经有统一的 `summary / payload_json` resume payload 协议，但真实 producer 目前仍主要来自 scheduler internal tool
- 当前已经能 durable 表达 reply 的 `create / reply / patch` 语义、`active / superseded` lifecycle、active target 选择与 patched/superseded lineage，但更完整的 runtime-owned continuation update lifecycle 仍未落地
- cutover request 已经从 ad hoc generation callback 收口为显式 `ChatGenerationPlan`，默认 executor 也不再直接依赖 `GenerateInitialChatSeq(...)`
- 首轮 prompt build / model execute / stream finalize 已拆成 base `agentruntime` 内的显式三段 contract
- 默认 `InitialReplyExecutor` 与 emission contract 也已进入 base `agentruntime`
- `RunProcessor` 的 initial contract 已经从匿名 `ProduceReply` 闭包收口为显式 `InitialReplyExecutor`，但 `RunProcessorInput.Initial` 仍然携带 concrete executor
- `ChatGenerationPlan`、`Runtime*CutoverRequest`、`ChatGenerationPlanExecutor` 都已进入 base `agentruntime`
- runtime 生产代码已经不再静态依赖 `handlers`，默认 capability provider 和 default executor 都由 bootstrap 显式装配

也就是说，当前系统已经具备：

- agentic UI 载体
- runtime 语义 contract
- shadow 观察入口
- durable shadow state contract
- durable shadow run 默认装配链路
- callback / schedule / approval 的恢复协议
- run-aware card callback ingress
- resume queue consumer
- schedule continuation ingress
- callback / schedule continuation 的最小 durable terminal executor
- queued capability call 的最小 durable executor
- approval waiting state、expiry 校验、approve callback payload contract
- approval card sender
- approval reject callback path
- approval approve/reject resolved-card callback UX
- capability-aware continuation planner slice
- approval replay to pending capability
- agentic chat runtime cutover entry
- agentic 真实聊天链路的 terminal reply durable 落库
- runtime message/card reference tracking
- agentic 真实聊天链路的 completed capability_call durable 落库
- standard 真实聊天链路的 runtime entry、terminal reply durable 落库、message ref 回写
- runtimecutover 内统一的 output orchestrator
- capability continuation 的真实 reply 发送与 refs 持久化
- callback / schedule generic continuation 的真实 reply 发送与 refs 持久化
- callback / schedule generic continuation 的用户可见中文语义回复
- callback / schedule generic continuation 的 payload-aware thought/observe 上下文
- agentic continuation 在已有 reply card refs 时会 patch 原 card entity，而不是新发卡
- standard continuation 在已有 reply message ref 时会 patch 原文本消息，而不是新发 reply
- runtime `reply` step 已经会持久化 delivery mode 与 patch target refs
- patched continuation 的正文 reply 已经能明确表达“已更新原消息”
- patched continuation 的 `reply` step 已经能指向被它更新的旧 `reply step`
- 被 patch 的旧 `reply step` 也已经会反向记录 `patched_by_step_id`

但还没有进入“全量 durable runtime + real cutover”阶段。

当前已经第一次进入“真实 chat 存在 user-visible agentic wait/approval/resume”的阶段，具体表现为：

- agentic chat 中，`send_message` 不再只是 inline tool call + 最终总结
- `config_set / config_delete / feature_block / feature_unblock / mute_robot` 也已经能进入同样的 deferred approval 协议
- `word_add / reply_add / image_add / image_delete` 也已经能进入同样的 deferred approval 协议
- `create_schedule / delete_schedule / pause_schedule / resume_schedule` 也已经能进入同样的 deferred approval 协议
- `create_todo / update_todo / delete_todo` 也已经能进入同样的 deferred approval 协议
- `revert_message` 也已经能进入同样的 deferred approval 协议
- `permission_manage` 也已经能进入 deferred approval，并在 approval replay 时真实发送权限管理卡片
- `oneword_get / music_search` 也已经能进入 deferred approval，并在 approval replay 时真实发送文本/音乐卡片
- 首轮 reply 会明确告诉用户“已发起审批，等待确认后继续发送”
- run 不会在首轮 reply 后直接 completed，而是保留 active reply + queued capability
- continuation processor 会立即把 run 推到 `waiting_approval`
- approval 通过后，最终结果会 patch 回最初那张 agentic 卡
- `image_add / image_delete` 在 approval replay 后不再额外发 reaction 回执，降低了 runtime replay 噪音
- runtime execution context 已支持按 capability 区分是否 suppress compatible output，为后续接入更多“发卡/发消息类” tool 打开了口子
- callback / schedule continuation 已经不再只停留在 generic fallback；默认路径也能 re-enter 模型、继续 tool loop，并把恢复摘要/payload 传进 observe 与 prompt

当前仍然没完成的是：

- 当前真实主链虽然已经扩到治理类、素材类、schedule 写工具、todo 写工具、`revert_message`、`permission_manage`、`oneword_get` 和 `music_search`，但还没有覆盖所有 side-effect tool
- 还没有把更多 side-effect tool 接到同样的 runtime defer / wait / approval / resume 协议
- full durable reasoning loop 还没完成；当前首轮与 continuation 的 multi-turn tool loop 仍然是在单次 invocation 内收敛，而不是每一轮都 durable 落 step 再继续

## 下一份文档

- 后续推进步骤见 `docs/architecture/agent-runtime-next-steps-plan.md`
