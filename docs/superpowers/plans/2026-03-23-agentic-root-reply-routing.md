# Agentic Root Reply Routing Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 agentic 模式下的 `reply_content` 始终汇聚到 root 卡片并走 patch，而审批卡、工具卡、兼容发送卡等侧向输出统一走 root 话题 reply；同时 root agentic 卡在创建时 `@` 触发用户，确保话题自动进入消息列表。

**Architecture:** 这次改造不再以“当前拿到了什么 target / target mode”决定 patch 还是 reply，而是先判定“这条输出属于模型 reply 还是侧向输出”，再由统一路由层选择 root patch 或 root thread reply。root 卡片是唯一可变的用户主视图；话题下回复是审批、工具、副产物的追加视图；工具卡如果后续有刷新，只 patch 自己，不 patch root。另一个独立约束是：root agentic 卡要带一个静态 `@触发用户` 区块，这个区块属于 root 卡壳层，不属于模型 `reply_content`，因此后续 patch reply 时不能丢失。

**Tech Stack:** Go, Lark card/message APIs, agent runtime state machine, runtimecontext, existing reply/approval emitters

---

### Task 1: 明确输出语义模型，停止用 target-mode 代替业务语义

**Files:**
- Modify: `internal/application/lark/agentruntime/initial_reply_target.go`
- Modify: `internal/application/lark/agentruntime/initial_reply_executor.go`
- Modify: `internal/application/lark/agentruntime/reply_emitter.go`
- Modify: `internal/application/lark/agentruntime/runtimecutover/runtime_output.go`
- Test: `internal/application/lark/agentruntime/initial_reply_executor_test.go`
- Test: `internal/application/lark/agentruntime/reply_emitter_test.go`
- Test: `internal/application/lark/agentruntime/runtimecutover/runtime_output_test.go`

- [ ] **Step 1: 先定义新的输出语义枚举**

引入显式语义，例如：

```go
type AgenticOutputKind string

const (
	AgenticOutputKindModelReply AgenticOutputKind = "model_reply"
	AgenticOutputKindSideEffect AgenticOutputKind = "side_effect"
)
```

要求：
- `model_reply` 只用于模型本轮最终 `reply_content`
- `side_effect` 用于审批卡、工具卡、兼容输出、发送类卡片
- `TargetMode` 保留为低层传输细节，不再直接承载业务含义

- [ ] **Step 2: 给 initial / continuation reply request 加上 output kind**

要求：
- `InitialReplyEmissionRequest`
- `ReplyEmissionRequest`

都能显式传入 `OutputKind`

- [ ] **Step 3: 先写失败测试，固化“语义优先于 target-mode”**

补测试覆盖：
- `model_reply` 命中 root 时优先 patch，即使之前代码默认 reply
- `side_effect` 命中 root 时必须 reply thread，不得 patch root

Run:

```bash
go test ./internal/application/lark/agentruntime/... -run 'TestDefaultInitialReplyExecutor|TestLarkReplyEmitter|TestReplyOrchestrator' -count=1
```

Expected:
- 旧断言会失败，暴露出当前实现仍然依赖 `TargetMode`/`ReplyInThread`

- [ ] **Step 4: 在 emitter/orchestrator 内按 output kind 做统一分发**

实现约束：
- `model_reply`:
  - 有 root card 时 patch root
  - 无 root 时回退到首张 root 卡的 reply/create 逻辑
- `side_effect`:
  - 有 root message 时 reply root thread
  - 无 root 时回退到现有 trigger reply/create

- [ ] **Step 5: 跑本任务测试，确认低层分发 contract 固化**

Run:

```bash
go test ./internal/application/lark/agentruntime/... -run 'TestDefaultInitialReplyExecutor|TestLarkReplyEmitter|TestReplyOrchestrator' -count=1
```

Expected:
- 所有 reply emitter / runtime output 相关测试通过

### Task 2: 改 initial 路径，让首轮 reply 创建 root，后续模型 reply patch root

**Files:**
- Modify: `internal/application/lark/agentruntime/run_processor.go`
- Modify: `internal/application/lark/agentruntime/initial_reply_executor.go`
- Modify: `internal/infrastructure/lark_dal/larkmsg/send.go`
- Modify: `internal/infrastructure/lark_dal/larkmsg/streaming_agentic.go`
- Test: `internal/application/lark/agentruntime/run_processor_test.go`
- Test: `internal/infrastructure/lark_dal/larkmsg/streaming_agentic_test.go`

- [ ] **Step 1: 先写失败测试，区分“首张 root 卡”与“已有 root 卡”**

新增/改造测试矩阵：
- 首轮 agentic，尚无 root: 应回复触发消息创建 root
- 已有 root: initial model reply 应 patch root，不再 reply root thread
- 线程消息触发但已有 root: 仍应 patch root，而不是继续在线程里新发一张 reply_content 卡

Run:

```bash
go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessorProcessRun' -count=1
```

Expected:
- 现有用例里关于 `InitialReplyTargetModeReply` 的断言会有一部分需要改成 patch

- [ ] **Step 2: 重写 `resolveInitialReplyTarget` 的判定顺序**

目标：
- 先判断是否已有 root agentic card
- 如果已有 root:
  - 返回 root message/card
  - 语义为 `model_reply`
  - 低层走 patch
- 如果没有 root:
  - 沿用当前事件上下文决定 reply trigger message / thread 的首张 root 卡创建方式

- [ ] **Step 3: 保持 root anchor 只在首张卡创建后建立**

要求：
- 首轮创建 root 后要继续写回 runtimecontext / step store
- 后续模型 reply 只能复用 root refs
- 不允许把最新 side-effect 卡写成新的 root

- [ ] **Step 4: 跑 initial 路径测试**

Run:

```bash
go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessorProcessRun|TestDefaultInitialReplyExecutor' -count=1
```

Expected:
- 初始运行路径测试通过
- root 建立与 root patch 的语义分离清晰

- [ ] **Step 5: 给 root agentic 卡补静态 `@触发用户` 能力**

要求：
- 只 root agentic 卡带 `@`，审批卡/ephemeral 卡仍然不能 `@`
- `@` 不拼进模型 reply 文本
- `@` 作为 root 卡固定元素存在，后续 patch reply content 时不被覆盖
- 需要从 runtime 上层把 `ActorOpenID` 或等价字段一路传到 streaming card 创建层

建议实现：

```go
type AgentStreamingCardOptions struct {
	MentionOpenID string
}
```

由 root create/reply 路径注入，patch 路径只复用已有卡，不重新决定 mention 语义

- [ ] **Step 6: 写失败测试并验证 `@` 只落在 root agentic 卡**

补测试覆盖：
- root card create/reply 时带 trigger user mention
- root card patch 不会移除 mention block
- approval card 不带 mention markup
- side-effect thread replies 不带 root mention block

Run:

```bash
go test ./internal/infrastructure/lark_dal/larkmsg ./internal/application/lark/agentruntime -run 'Test.*Streaming|TestContinuationProcessorProcessRun' -count=1
```

Expected:
- root agentic card mention 行为稳定
- 审批卡和 side-effect 卡不会误带 `@`

### Task 3: 改 continuation/capability 路径，让所有模型最终 reply 回到 root patch

**Files:**
- Modify: `internal/application/lark/agentruntime/continuation_processor.go`
- Modify: `internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go`
- Modify: `internal/application/lark/agentruntime/default_capability_reply_turn_executor.go`
- Modify: `internal/application/lark/agentruntime/reply_completion.go`
- Test: `internal/application/lark/agentruntime/continuation_processor_test.go`
- Test: `internal/application/lark/agentruntime/planner_test.go`

- [ ] **Step 1: 先写失败测试，锁住“单次 agentic 执行的模型 reply 必须 patch root”**

测试至少覆盖：
- `ContinuationReplyTurnExecutor` 给出最终 reply 时 patch root
- `CapabilityReplyTurnExecutor` 给出最终 reply 时 patch root
- 审批通过后恢复执行，最终模型 reply patch root
- 如果本轮只产生 pending approval / tool side-effect，没有最终 reply，则不 patch root

Run:

```bash
go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessor|TestPlanner' -count=1
```

Expected:
- 当前 follow-up reply 相关断言会失败，因为现在默认还是 thread reply

- [ ] **Step 2: 把 `emitCapabilityReply` / `emitContinuationReply` 从 follow-up thread target 切到 root model reply target**

重构建议：
- 新增 `resolveModelReplyTarget(ctx, run)`
- 新增 `resolveSideEffectThreadTarget(ctx, run)`
- `emitCapabilityReply` 更名为 `emitModelReply` 或同等语义名字

要求：
- 模型最终 reply 只走 `resolveModelReplyTarget`
- side-effect 输出只走 `resolveSideEffectThreadTarget`

- [ ] **Step 3: 保持 reply step 生命周期与 root patch 语义一致**

要求：
- patch root 的 reply step 仍然新建 `StepKindReply`
- 被覆盖的旧 root reply step 应打上 `lifecycle_state=superseded`
- 如果这次是 patch，要写 `patched_by_step_id`
- 如果这次是 thread append，要写 `superseded_by_step_id`

- [ ] **Step 4: 确认 root target 的恢复逻辑不会被 tool card 污染**

要求：
- `resolveRootReplyTarget` 只认主 reply card
- `resolveReplyTarget` 或其替代逻辑不得把 side-effect 卡当成新的 root

- [ ] **Step 5: 跑 continuation/capability 路径测试**

Run:

```bash
go test ./internal/application/lark/agentruntime -run 'TestContinuationProcessor|TestPlanner' -count=1
```

Expected:
- continuation、capability、planner 相关测试通过

### Task 4: 固定侧向输出语义，审批卡/工具卡/兼容发送统一 reply root thread，工具卡只 patch 自己

**Files:**
- Modify: `internal/application/lark/agentruntime/initial_pending_approval_dispatcher.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor.go`
- Modify: `internal/application/lark/handlers/schedule_compat.go`
- Test: `internal/application/lark/agentruntime/run_processor_test.go`
- Test: `internal/application/lark/agentruntime/continuation_processor_test.go`
- Test: `internal/application/lark/handlers/schedule_compat_test.go`

- [ ] **Step 1: 审批发送路径只使用 side-effect thread target**

要求：
- 审批卡永远 reply 到 root 话题
- 不参与 root patch
- 不带 `@` mention
- 审批卡后续撤回/删除仍只处理审批卡自己

- [ ] **Step 2: 兼容发送路径显式区分“新发 side-effect 卡”与“刷新已有工具卡”**

要求：
- `sendCompatibleText/Card/CardJSON/RawCard` 初次发送时，如果存在 root，则 reply root thread
- `meta.Refresh` 只 patch 当前工具卡自身
- 不允许 refresh 把 root card 当 patch target

- [ ] **Step 3: 写测试固化 tool/self-patch 规则**

补测试覆盖：
- root 存在时 compatible text/card/json/raw card 都 reply root thread
- refresh 时 patch 的是当前 tool card，不是 root
- side-effect 卡发送后不改变 root anchor

- [ ] **Step 4: 跑 side-effect 相关测试**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/application/lark/handlers -run 'Test.*Approval|TestSendCompatible' -count=1
```

Expected:
- 审批/兼容输出路径测试通过

### Task 5: 回归与防回退，补全测试矩阵和日志

**Files:**
- Modify: `internal/application/lark/agentruntime/run_processor_test.go`
- Modify: `internal/application/lark/agentruntime/continuation_processor_test.go`
- Modify: `internal/application/lark/agentruntime/reply_emitter_test.go`
- Modify: `internal/application/lark/agentruntime/runtimecutover/runtime_output_test.go`
- Modify: `internal/application/lark/handlers/schedule_compat_test.go`

- [ ] **Step 1: 建一张完整语义矩阵**

最少覆盖这些 case：
- initial 首轮模型 reply: create/reply 首张 root
- initial 有 root 时模型 reply: patch root
- continuation 模型 reply: patch root
- capability turn 模型 reply: patch root
- approval card: reply root thread
- compatible card/text/json/raw card: reply root thread
- compatible refresh: patch tool card itself
- root 缺失时的降级行为: reply trigger / create

- [ ] **Step 2: 给关键路由点补最小可观测日志**

建议日志字段：
- `run_id`
- `output_kind`
- `delivery_mode`
- `target_message_id`
- `target_card_id`
- `reply_in_thread`

- [ ] **Step 3: 跑最小回归集**

Run:

```bash
go test ./internal/application/lark/agentruntime/... ./internal/application/lark/handlers/... -count=1
```

Expected:
- 全部通过

- [ ] **Step 4: 跑全量相关包测试**

Run:

```bash
go test ./... -count=1
```

Expected:
- 无新增回归
- 如全量太慢，至少保留 agentruntime + handlers 全通过作为合入门槛

## Implementation Notes

- root 卡片是用户主视图，thread 是副视图；两者职责不能再混用。
- root 卡的 `@触发用户` 是卡壳层静态能力，不是模型 reply 文本的一部分。
- “是否有 target card/message” 是传输条件，不是业务语义。
- 真正应该 patch root 的只有模型本轮最终 `reply_content`。
- 审批卡、工具卡、兼容发送卡都是 side-effect；它们可以 reply thread，也可以后续 patch 自己，但不能抢占 root。
- side-effect 卡不继承 root 的 `@` 逻辑；否则会把整个话题都变成噪音提醒。
- 命名上建议避免继续使用 `followUpReplyTarget` 这种模糊名字，改成 `modelReplyTarget` / `sideEffectThreadTarget` 之类更直白的语义名。
