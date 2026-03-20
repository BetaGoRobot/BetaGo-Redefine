# Agent Runtime Durable Shadow Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把当前“只记录 decision 的 shadow mode”推进为“可持久化的 durable shadow runtime”，在不改变用户可见回复的前提下落地 `session -> run -> step`、Redis coordination 和 coordinator。

**Architecture:** 先做 durable state 和 coordination contract，再实现 `RunCoordinator`，最后让 `AgentShadowOperator` 调 coordinator 创建 shadow run。旧 `ReplyChatOperator` / `ChatMsgOperator` 继续负责真实回复，直到 cutover 条件成熟。

**Tech Stack:** Go, GORM Gen, Redis, existing `xhandler`, existing `xcommand`, Lark SDK, Go tests.

## Status Update · 2026-03-18

- 已完成本计划的前三十七个执行块：
  - durable repository
  - Redis coordination helper
  - `RunCoordinator`
  - `AgentShadowOperator` 的 shadow persistence wiring
  - `AgentShadowOperator` 的默认 durable coordinator 懒加载装配
  - run-aware card callback ingress
  - resume queue consumer + schedule continuation ingress
  - continuation processor skeleton
  - approval contract and waiting state
  - approval card sender and reject path
  - approval callback resolved-card UX
  - capability-aware continuation planner slice
  - agentic chat runtime cutover entry
  - runtime message/card reference tracking
  - real chat capability trace persistence
  - standard chat runtime cutover entry
  - runtime-owned output orchestration
  - continuation-driven reply engine
  - generic continuation reply contract
  - business-grade generic continuation semantics
  - payload-aware generic continuation context
  - agentic continuation existing-card patch path
  - standard continuation existing-message patch path
  - reply delivery metadata persistence
  - patched continuation payload-aware reply body semantics
  - reply-step patch lineage
  - reverse patch lineage on prior reply step
  - reply lifecycle state + non-destructive patch backfill
  - reply-step supersede transition for new terminal replies
  - active-only reply target selection
  - approval-title-aware generic continuation reply
  - wait-title-aware callback/schedule continuation reply
  - schedule write tools deferred approval
  - todo write tools deferred approval
  - revert_message deferred approval + image replay noise suppression
  - permission_manage deferred approval + selective compatible output replay
  - oneword_get/music_search deferred approval + selective compatible output replay
  - shared tool runtime behavior registry + capability requires_approval metadata unification
  - unified RunProcessor owner for initial progression + resume progression
  - chat handler no longer pre-generates stream before runtime cutover; initial generation is processor-triggered
- 当前 durable repository 已切到生成的 `model/query`，前置依赖是：
  - 建表 SQL 已写入 `script/sql/20260318_agent_runtime_tables.sql`
  - 用户已执行 SQL
  - 用户已执行 `go run ./cmd/generate`
- 当前 `ContinuationProcessor` 已经可以：
  - 直接执行 queued `capability_call`
  - 对 `RequiresApproval=true` capability 发起 approval gate
  - approval 通过后 replay 原 `capability_call`
  - 继续兼容原有 `resume -> observe -> complete` fallback
  - generic fallback 写出 payload-aware `observe` payload
  - generic fallback 在 agentic reply emitter 中带出 thought + reply
  - generic / capability continuation 在存在历史 reply refs 时复用旧 agentic card 做 patch
  - standard continuation 在存在历史 reply message ref 时 patch 原文本消息
  - continuation / runtime cutover 的 `reply` step 已经持久化 `create / reply / patch` metadata
  - patched continuation 的正文 reply 已经能表达“已更新原消息”
  - patched continuation 的 `reply` step 已经记录被更新的旧 `reply step id`
  - 被 patch 的旧 `reply step` 也会反向记录 `patched_by_step_id`
  - runtime / continuation 的 `reply` step 已经显式区分 `active / superseded` lifecycle state
  - patch 回写旧 reply step 时会保留原有 `thought_text / reply_text` 等 output payload
  - create / reply 产生新的 terminal reply 时，旧 `reply step` 也会被显式 supersede
  - reply target resolver 已经优先选择最近的 active reply，而不是盲取最后一条 reply
  - approval generic continuation 已经能把 `approval_request.title` 带入 reply/thought/observe
  - callback / schedule generic continuation 已经能在存在 `wait.title` 时带出业务标题
  - queued capability input 只要带 `approval` spec，就会进入 approval gate，并真实发送 approval card
- 当前 `chat_handler` 在 agentic 模式下已经可以在 cutover flag 命中时：
  - 创建 durable run
  - 复用现有 streaming card 输出
  - 在流式完成后把最终结果收敛成 terminal `reply` step
  - 在 `send_message` 场景下把 inline tool call 改写成：
    - 首轮 reply 先输出“已发起审批”
    - durable 持久化 active reply
    - queued `capability_call`
    - continuation processor 推进到 `waiting_approval`
    - approval 通过后 patch 原 agentic card
  - 在以下 side-effect tool 场景下复用同样的 deferred approval contract：
    - `config_set`
    - `config_delete`
    - `feature_block`
    - `feature_unblock`
    - `mute_robot`
    - `word_add`
    - `reply_add`
    - `image_add`
    - `image_delete`
    - `create_schedule`
    - `delete_schedule`
    - `pause_schedule`
    - `resume_schedule`
    - `create_todo`
    - `update_todo`
    - `delete_todo`
    - `revert_message`
    - `permission_manage`
    - `oneword_get`
    - `music_search`
- 当前 runtime 已经能追踪：
  - `run.last_response_id = message_id`
  - terminal `reply.external_ref = card_id`
- 当前 tool runtime 行为已经统一收口到 `internal/application/lark/toolmeta`：
  - shared side effect level
  - shared requires approval
  - shared compatible output policy
  - shared default approval placeholder/title/result key
- 当前 `runtime_chat.go / runtime_text.go` 已经退化成 output adapter：
  - 首轮 reply 通过 `ProduceReply(...)` 交给 unified `RunProcessor`
  - `ResumeWorker` 也已经统一调用 `ProcessRun(resume)`
- 当前 `chat_handler.go` 进入 cutover 后也不再先执行 `GenerateChatSeq(...)`：
  - 首轮 generation 已改为由 runtime cutover / processor 按需触发
  - cutover request 已从 ad hoc callback 收口为显式 `ChatGenerationPlan`
  - `RunProcessor` 的 initial contract 已从匿名 `ProduceReply` 收口为显式 `InitialReplyExecutor`
  - prompt build / Ark execution 的真实实现已进入 base `agentruntime`，`handlers.GenerateChatSeq(...)` 只剩兼容 wrapper
  - `ChatGenerationPlan` 类型所有权也已进入 base `agentruntime`
  - `Runtime*CutoverRequest` 与 cutover handler interface 也已进入 base `agentruntime`
  - `ChatGenerationPlanExecutor` contract 也已进入 base `agentruntime`
  - runtime 生产代码已不再静态依赖 `handlers`，默认 executor/provider 改为由 bootstrap 显式装配
- 2026-03-19 追加完成：
  - active run ownership 已接入真实消息入口，而不再只存在于 shadow policy：
    - `/bb` command bridge
    - `@bot / p2p`
    - `follow_up / reply_to_bot`
  - `ChatEntryHandler -> ChatResponseRequest -> Runtime*CutoverRequest -> StartShadowRunRequest` 已有显式 `InitialRunOwnership` contract
  - `follow_up / reply_to_bot` 在真实消息路径上已经会跳过旧 random/intent 分支，直接进入 runtime
  - attach 到 active run 时，初始 output 已能复用已有 reply target：
    - agentic patch 原 card
    - standard patch 原消息
  - waiting run 已允许被 follow-up 唤醒回 `running`
  - attach follow-up 现在会在同一 run 内追加新的 durable `decide` step，而不是直接复用旧 waiting step
  - attach 对同一条消息已具备幂等保护，不会重复长 step
  - 默认 `ChatGenerationPlanExecutor` 已不再直接依赖 `GenerateInitialChatSeq(...)`
  - 首轮默认生成已经显式拆成三段：
    - `BuildInitialChatExecutionPlan(...)`
    - `ExecuteInitialChatExecutionPlan(...)`
    - `FinalizeInitialChatStream(...)`
  - `GenerateInitialChatSeq(...)` 现在只剩兼容 wrapper 角色
  - 默认 `InitialReplyExecutor` 与 emission contract 已进入 base `agentruntime`
  - `runtimecutover` 现在只保留 output emitter adapter，而不再持有默认 initial reply executor 实现
  - `InitialRunInput` 已从 `Start + concrete executor` 收口为 declarative initial request：
    - `Start`
    - `Event`
    - `Plan`
    - `OutputMode`
  - `ProcessInitial` 已经会在内部调用 `InitialRunInput.BuildExecutor(...)` 装配默认 executor
  - initial reply emitter 已不再经由 `InitialRunInput` 透传，而是改成 processor dependency：
    - `ContinuationProcessor.initialReplyEmitter`
    - `WithInitialReplyEmitter(...)`
    - `runtimecutover.processorBuilder(ctx, emitter)`
  - 首轮 tool flywheel 已不再由 `responses.go` 内联 `handle tools -> recursive PreviousResponseId` 拥有：
    - `ResponsesImpl.StreamTurn(...)` 现在只负责单轮 streaming 和 function call intent 解析
    - `defaultChatGenerationPlanExecutor` 现在负责首轮 multi-turn tool loop
    - 首轮 capability execution / approval placeholder / tool output re-entry 现在都由 runtime 决定
  - 首轮 chat generation 现在真正使用 `ChatGenerationPlan.ModelID`
  - pending capability 恢复现在已经会保留 `PreviousResponseID`
  - capability completion 默认会优先 re-enter model continuation，而不是直接吐 summary
  - 默认 continuation executor 也已能 own follow-up tool loop，并把 nested capability / pending capability 再写回当前 run
  - initial / continuation follow-up tool loop 里的 completed capability trace 已改为即时 durable 落 `capability_call + observe`
  - runtime recorder 在即时录 step 后也会同步推进 `run.current_step_index`
  - resume 事件消费后如果 reply-turn 出错，`ProcessResume(...)` 也会把 lingering `running` run 收敛到 `failed`
  - agentic chat 首轮 prompt 已与 standard chat 分叉，agentic mode 不再复用旧模板式单轮 builder
  - agentic / standard 首轮 generation executor 入口也已显式分轨
- 仍未完成的关键缺口：
  - 还没有把更多 side-effect tool 接入同样的 `defer -> wait/approval -> resume -> patch original reply` 协议，当前主要覆盖：
    - `send_message`
    - `config_set`
    - `config_delete`
    - `feature_block`
    - `feature_unblock`
    - `mute_robot`
    - `word_add`
    - `reply_add`
    - `image_add`
    - `image_delete`
    - `create_schedule`
    - `delete_schedule`
    - `pause_schedule`
    - `resume_schedule`
    - `create_todo`
    - `update_todo`
    - `delete_todo`
    - `revert_message`
    - `permission_manage`
    - `oneword_get`
    - `music_search`
  - 首轮 / continuation 的 completed capability trace 虽然已经即时 durable 落 step，run cursor 也会跟进，但 multi-turn loop 里的 model turn 本身还不是显式 durable step
  - agentic mode 虽然已经有独立 prompt builder 和独立执行器入口，但底层 transport 仍然复用 `StreamTurn(...)`
  - initial output delivery dependency 仍然是由 `runtimecutover` 在 build processor 时注入，还没有完全沉到 base runtime 的默认装配层
  - capability resume / callback / schedule 虽然都已经能重新进模型并继续 follow-up tool loop，但仍然不是“每一轮 model turn 都 durable 落 step 再继续”的 full durable reasoning loop
  - callback / schedule 已经有统一的 `summary / payload_json` resume payload protocol，但真实 producer 目前仍主要是 scheduler internal tool
- 下一优先级已经前移到 `把 multi-turn loop 里的 model turn / plan 自身 durable 化并并入统一 step loop -> 把 initial output emitter 的默认装配继续下沉 -> 扩展 resume payload producer 与真实 waiting_schedule/waiting_callback 入口 -> 扩展剩余 side-effect tool -> 把 selective compatible output replay 扩到更多合适的 message/card capability`。

---

## File Structure

- Modify: `cmd/generate/gorm-gen.go`
  - 注册 `agent_sessions`、`agent_runs`、`agent_steps` 的生成定义
- Create: `internal/infrastructure/db/model/agent_sessions.gen.go`
- Create: `internal/infrastructure/db/model/agent_runs.gen.go`
- Create: `internal/infrastructure/db/model/agent_steps.gen.go`
- Create: `internal/infrastructure/db/query/agent_sessions.gen.go`
- Create: `internal/infrastructure/db/query/agent_runs.gen.go`
- Create: `internal/infrastructure/db/query/agent_steps.gen.go`
- Create: `internal/infrastructure/agentstore/session_repo.go`
- Create: `internal/infrastructure/agentstore/run_repo.go`
- Create: `internal/infrastructure/agentstore/step_repo.go`
- Create: `internal/infrastructure/agentstore/repository_test.go`
- Create: `internal/infrastructure/redis/agentruntime.go`
- Create: `internal/infrastructure/redis/agentruntime_test.go`
- Create: `internal/application/lark/agentruntime/coordinator.go`
- Create: `internal/application/lark/agentruntime/coordinator_test.go`
- Modify: `internal/application/lark/agentruntime/shadow.go`
- Modify: `internal/application/lark/messages/ops/agent_op.go`
- Create: `internal/application/lark/messages/ops/agent_op_persist_test.go`
- Optional create after coordinator stabilizes: `internal/application/lark/agentruntime/resume_event.go`

## Chunk 1: Durable State

### Task 1: Add Agent DB Models And Repository

**Files:**
- Modify: `cmd/generate/gorm-gen.go`
- Create: `internal/infrastructure/agentstore/session_repo.go`
- Create: `internal/infrastructure/agentstore/run_repo.go`
- Create: `internal/infrastructure/agentstore/step_repo.go`
- Test: `internal/infrastructure/agentstore/repository_test.go`

- [x] **Step 1: Write the failing repository tests**

Cover these behaviors:
- create session by chat scope
- create run under session
- append step under run
- update run status with revision check
- reject stale revision update

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/infrastructure/agentstore -run 'Test(AgentSessionRepository|AgentRunRepository|AgentStepRepository)'
```

Expected: FAIL with missing repository or generated model symbols.

- [x] **Step 3: Add minimal repository implementation**

Requirements:
- 当前先不改 `gorm-gen`
- 使用手写 row struct + `AutoMigrate(sqlite)` 锁定 durable contract
- repository API must be application-facing and hide gorm details

Suggested repository surface:

```go
type SessionRepository interface {
    FindOrCreateChatSession(ctx context.Context, appID, botOpenID, chatID string) (*agentruntime.AgentSession, error)
}

type RunRepository interface {
    Create(ctx context.Context, run *agentruntime.AgentRun) error
    UpdateStatus(ctx context.Context, runID string, fromRevision int64, mutate func(*agentruntime.AgentRun) error) (*agentruntime.AgentRun, error)
}

type StepRepository interface {
    Append(ctx context.Context, step *agentruntime.AgentStep) error
    ListByRun(ctx context.Context, runID string) ([]*agentruntime.AgentStep, error)
}
```

- [x] **Step 4: Run test to verify it passes**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/infrastructure/agentstore -run 'Test(AgentSessionRepository|AgentRunRepository|AgentStepRepository)'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/generate/gorm-gen.go internal/infrastructure/db/model internal/infrastructure/db/query internal/infrastructure/agentstore
git commit -m "feat: add agent runtime durable repositories"
```

### Task 2: Add Redis Coordination Helpers

**Files:**
- Create: `internal/infrastructure/redis/agentruntime.go`
- Test: `internal/infrastructure/redis/agentruntime_test.go`

- [x] **Step 1: Write the failing Redis helper tests**

Cover these behaviors:
- acquire and release run lock
- active chat slot compare-and-swap
- cancel generation increment
- enqueue and dequeue resume event

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/infrastructure/redis -run 'Test(AgentRuntimeRunLock|AgentRuntimeActiveChatSlot|AgentRuntimeResumeQueue)'
```

Expected: FAIL with missing helper symbols.

- [x] **Step 3: Implement minimal Redis helpers**

Requirements:
- all keys must be namespaced by current bot identity
- resume queue payload must carry `run_id`, `revision`, `reason`
- lock API must expose explicit ownership token, not just boolean

- [x] **Step 4: Run test to verify it passes**

Run the same command from Step 2.

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infrastructure/redis/agentruntime.go internal/infrastructure/redis/agentruntime_test.go
git commit -m "feat: add agent runtime redis coordination"
```

## Chunk 2: Runtime Coordination

### Task 3: Implement RunCoordinator

**Files:**
- Create: `internal/application/lark/agentruntime/coordinator.go`
- Test: `internal/application/lark/agentruntime/coordinator_test.go`

- [x] **Step 1: Write the failing coordinator tests**

Cover these behaviors:
- start shadow run for a chat session
- create initial `decide` step
- reject stale revision resume
- cancel active run when superseded

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestRunCoordinator'
```

Expected: FAIL with undefined coordinator types or methods.

- [x] **Step 3: Implement minimal coordinator**

Required methods:

```go
type RunCoordinator interface {
    StartShadowRun(ctx context.Context, req StartShadowRunRequest) (*AgentRun, error)
    CancelRun(ctx context.Context, runID, reason string) error
}
```

Rules:
- `StartShadowRun` must be idempotent per message id when possible
- create session before run
- create run before first step
- when superseding, cancel previous active run before setting new active slot

- [x] **Step 4: Run test to verify it passes**

Run the same command from Step 2.

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/agentruntime/coordinator.go internal/application/lark/agentruntime/coordinator_test.go
git commit -m "feat: add agent runtime run coordinator"
```

## Chunk 3: Shadow Persistence Integration

### Task 4: Let AgentShadowOperator Persist Durable Shadow Runs

**Files:**
- Modify: `internal/application/lark/agentruntime/shadow.go`
- Modify: `internal/application/lark/messages/ops/agent_op.go`
- Test: `internal/application/lark/messages/ops/agent_op_persist_test.go`

- [x] **Step 1: Write the failing integration-style tests**

Cover these behaviors:
- eligible mention creates shadow run through coordinator
- ineligible message only records decision and does not create run
- created `run_id` and `session_id` are written into `meta.Extra`
- shadow mode still does not send user-visible replies

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/messages/ops -run 'TestAgentShadowOperator.*Persist'
```

Expected: FAIL with missing coordinator wiring or missing meta fields.

- [x] **Step 3: Implement coordinator wiring**

Requirements:
- `AgentShadowOperator` should depend on an injected coordinator interface
- no new user-visible message send
- `meta.Extra` should include:
  - `agent_runtime.shadow.run_id`
  - `agent_runtime.shadow.session_id`
  - existing decision fields

- [x] **Step 4: Run test to verify it passes**

Run the same command from Step 2.

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/agentruntime/shadow.go internal/application/lark/messages/ops/agent_op.go internal/application/lark/messages/ops/agent_op_persist_test.go
git commit -m "feat: persist agent runtime shadow runs"
```

### Task 5: Prepare Resume Event Contract For Callback And Async Continuation

**Files:**
- Create: `internal/application/lark/agentruntime/resume_event.go`
- Modify: `internal/application/lark/agentruntime/coordinator.go`
- Test: `internal/application/lark/agentruntime/coordinator_test.go`

- [x] **Step 1: Write the failing resume contract tests**

Cover these behaviors:
- validate resume event payload
- reject revision mismatch
- reject cancelled run resume
- accept queued callback/schedule resume event

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'Test(RunCoordinatorResume|ResumeEvent)'
```

Expected: FAIL with missing resume DTO or methods.

- [x] **Step 3: Implement minimal resume contract**

Rules:
- DTO must not carry raw Lark SDK payload
- keep callback/schedule specific fields outside the core run model
- this task stops at contract and coordinator entry; it does not yet wire card callback ingress

- [x] **Step 4: Run test to verify it passes**

Run the same command from Step 2.

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/agentruntime/resume_event.go internal/application/lark/agentruntime/coordinator.go internal/application/lark/agentruntime/coordinator_test.go
git commit -m "feat: add agent runtime resume event contract"
```

## Final Verification

- [x] Run the durable-state package tests

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/infrastructure/agentstore ./internal/infrastructure/redis ./internal/application/lark/agentruntime ./internal/application/lark/messages ./internal/application/lark/messages/ops
```

- [x] Run the existing packages that must remain green

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/config ./internal/application/lark/handlers ./internal/infrastructure/lark_dal/larkmsg
```

- [ ] Manual verification in test chat

Check:
- `agent_runtime_enabled=true`
- `agent_runtime_shadow_only=true`
- mention bot once
- confirm no新增用户可见回复
- confirm shadow log / meta / durable run record exist

## Next Sprint · Runtime Chat Cutover

### Task 6: Replay Approved Capability Instead Of Terminally Completing On Resume

Status: completed on 2026-03-18.

**Files:**
- Modify: `internal/application/lark/agentruntime/continuation_processor.go`
- Modify: `internal/application/lark/agentruntime/coordinator.go`
- Test: `internal/application/lark/agentruntime/planner_test.go`

- [x] **Step 1: Write the failing replay tests**

Cover these behaviors:
- approval approve 后，worker 不直接走 `resume -> observe -> complete`
- runtime 能从当前 approval/resume 轨迹回溯到待执行 `capability_call`
- capability 真正执行后写出 `observe` / `reply`

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessor.*Approval.*'
```

Expected: FAIL with missing replay logic.

- [x] **Step 3: Implement approval replay**

Requirements:
- `ResumeSourceApproval` 恢复后，processor 需要识别“当前 `resume` step 只是 continuation marker”
- 从 step 链回溯最近一个未完成的 `capability_call`
- 在审批通过后真正执行该 capability，而不是直接把 run 终结

- [x] **Step 4: Run test to verify it passes**

Run the same command from Step 2.

Expected: PASS.

### Task 7: Cut Chat Handler Into Runtime Entry And Durable Planner Output

Status: partially completed on 2026-03-18.

**Files:**
- Modify: `internal/application/lark/handlers/chat_handler.go`
- Modify: `internal/application/lark/messages/ops/agent_op.go`
- Create or modify: `internal/application/lark/agentruntime/*planner*`
- Test: `internal/application/lark/handlers/chat_handler_test.go`

- [x] **Step 1: Write the failing cutover tests**

Cover these behaviors:
- 当 `agent_runtime_enabled=true` 且切到 runtime chat mode 时，聊天主链不再直接调用旧 reply path
- 会创建或推进 durable run
- planner 输出至少包含可执行的 `capability_call` 或 terminal `reply`

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/application/lark/handlers -run 'TestChatHandler.*AgentRuntime'
```

Expected: FAIL with missing runtime cutover wiring.

- [x] **Step 3: Implement minimal chat cutover**

Requirements:
- 先只切 agentic 模式，不影响 standard chat
- 旧 chat path 继续作为 fallback
- runtime planner 输出先追求最小闭环，不在第一轮引入复杂多步 reasoning protocol

Current landed scope:
- agentic 模式在 cutover flag 命中时进入 runtime-aware adapter
- 会创建 durable run 并在流式结束后写 `capability_call + reply` durable step
- 当前 `capability_call` 来自真实已执行的 tool trace，尚不是独立 planner protocol
- standard 模式也已进入 runtime-aware adapter，但仍然只在流结束后发送一次文本回复
- agentic/standard 两条输出路径已经由 runtimecutover 内部统一 orchestrator 收口
- `handlers.ChatHandlerInner(...)` 已不再持有首轮 chat 编排细节；chat mode 判定、mute guard、图片收集、model 选择、plan 组装、agentic/standard 分流 已进入 `agentruntime.ChatEntryHandler`
- 默认 responder dispatch / cutover builder / fallback sender 也已进入 `agentruntime.chat_response.go`
- 默认 `ChatGenerationPlanExecutor` 已进入 `agentruntime.default_chat_generation_executor.go`
- `AgentShadowOperator` 已开始把 active run snapshot 注入 `ShadowObserver`，follow-up attach / mention supersede 不再只是 policy 测试语义，而是会真实写入 `StartShadowRunRequest.AttachToRunID / SupersedeRunID`
- `handlers/chat_handler.go` 当前只剩：
  - CLI 参数解析
  - event adapter
- `handlers` 当前保留的 runtime 兼容点主要只剩：
  - `BuildLarkTools()` 默认工具集合提供
- `GenerateChatSeq(...)` 兼容 wrapper
- `chat_generation_plan.go` 的轻量兼容别名/转发
- capability continuation 完成后，已经会真实发出 terminal reply，并把 refs 写回 durable state
- generic callback / schedule continuation 完成后，也已经会真实发出 terminal reply，并把 refs 写回 durable state
- generic callback / schedule continuation 的默认 reply 已经换成用户可见的中文语义提示
- generic callback / schedule continuation 已经会向 agentic emitter 传递 thought + reply，并把 payload-aware 上下文写入 `observe`

- [x] **Step 4: Run test to verify it passes**

Run the same command from Step 2.

Expected: PASS.

### Task 8: Runtime Output To Agentic Streaming Card

Status: partially completed on 2026-03-18 via legacy sender reuse.

**Files:**
- Modify: `internal/infrastructure/lark_dal/larkmsg/streaming_agentic.go`
- Modify: `internal/application/lark/handlers/chat_handler.go`
- Test: `internal/infrastructure/lark_dal/larkmsg/streaming_agentic_test.go`

- [ ] **Step 1: Write the failing output tests**

Cover these behaviors:
- runtime `reply` step 能映射到现有 agentic streaming card
- thought / body 仍按“折叠 thought 在前、正文在后”布局输出
- runtime continuation 更新不会破坏现有流式 element patch contract

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp go test ./internal/infrastructure/lark_dal/larkmsg -run 'TestStreamingAgentic'
```

Expected: FAIL with missing runtime-to-card mapping.

- [ ] **Step 3: Implement minimal runtime output mapping**

Requirements:
- 复用已有 card entity / streaming element update 能力
- 不重新发明第二套 agentic UI
- runtime state 和 streaming card message id/reference 需要可追踪

Current landed scope:
- 已复用现有 `SendAndUpdateStreamingCard()` 作为 cutover 输出通道
- 已把 message id / card entity reference 写回 runtime state
- continuation 在已有 reply card refs 时，已经会 patch 原 card entity，而不是新发一张 agentic 卡
- continuation 在 standard 模式且已有 reply message ref 时，已经会 patch 原文本消息，而不是新发 reply
- runtime reply output 已经持久化 delivery mode 与 patch target refs

- [ ] **Step 4: Run test to verify it passes**

Run the same command from Step 2.

Expected: PASS.
