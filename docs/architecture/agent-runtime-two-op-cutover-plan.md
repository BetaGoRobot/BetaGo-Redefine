# Agent Runtime Two-Op Cutover Plan

## Goal

把当前被 runtime 语义污染的聊天主链路重新切开：

- `standard chat` 保持接近 `HEAD(master)` 的原始行为
- `agentic chat` 作为独立入口和独立回复模式继续推进
- 两条链路由 `chat_mode` 互斥选择
- 不再让标准 chat 经过 agentic 的 pending / approval / durable-run 预处理

## Current Problem

当前问题不是某个工具 handler 坏了，而是入口边界坏了：

1. `messages/ops/chat_op.go` 和 `reply_chat_op.go` 已经把标准聊天入口改成统一走 runtime/chat entry。
2. `handlers/chat_handler.go` 不再保留原始标准聊天生成逻辑，而是统一委托给 `agentruntime.ChatEntryHandler`。
3. `agentruntime/default_chat_generation_executor.go` 会在工具真正执行前，把 `RequiresApproval` 工具转成 `pending`。
4. 结果是标准 chat 也失去了原有的直接副作用能力，例如兼容路径里的发卡、发消息类能力。

这属于架构层串味，不应该继续在共享 executor 里补条件修。

## Cutover Rules

本轮改造遵循以下硬约束：

1. 标准 chat 的可见行为优先回到 `HEAD(master)` 原语义。
2. agentic 与 standard 在消息入口层互斥，不共享同一条前门 routing。
3. `chat_mode=standard` 时，不允许用户可见主链路走 runtime pending 语义。
4. `chat_mode=agentic` 时，才允许 mention / follow-up / reply-to-bot 等消息进入 agentic 路径。
5. 旧的标准 handler 不为兼容 agentic 做额外侵入式改造。
6. 分流尽量前置到 handler / processor 入口；进入具体 op 后不再做 standard / agentic mode 判断。
7. 避免用 resolver、alias、浅包装把分流往后拖；优先使用两套直接入口。

## Execution Plan

### Phase 1: 切开前门入口

目标：先止血，恢复标准 chat。

涉及文件：

- `internal/application/lark/handlers/chat_handler.go`
- `internal/application/lark/messages/ops/chat_op.go`
- `internal/application/lark/messages/ops/reply_chat_op.go`
- `internal/application/lark/messages/ops/runtime_route.go`
- `internal/application/lark/messages/handler.go`

执行项：

1. 在 `handlers/chat_handler.go` 恢复标准聊天的原始生成逻辑。
2. 新增 agentic 专用 chat handler，保留独立入口，禁止共享一个带 mode 字段的 chat handler。
3. `/bb` 拆成 standard / agentic 两套 root command 与两套 command op，不再在同一个 command op 里按 mode 二次分流。
4. 消息入口改为一个前置 router + 两套 processor：
   - `standard processor`
   - `agentic processor`
5. `ChatMsgOperator` / `ReplyChatOperator` / `Agentic*` op 不再包含 mode guard；进入各自 processor 后直接执行。

完成标准：

- `chat_mode=standard` 下，标准聊天不再经过 runtime pending 语义。
- `chat_mode=agentic` 下，仍可通过现有 runtime/continuation 机制进入 agentic 链路。
- standard 与 agentic 不再共享同一个 chat handler，也不再共享同一个消息 processor。

### Phase 1 Status

当前已完成：

1. `handlers.Chat` / `handlers.AgenticChat` 已拆成两个独立 handler。
2. `command.LarkRootCommand` / `command.AgenticLarkRootCommand` 已拆开，`bb` 绑定到各自 handler。
3. `messages.NewMessageProcessor(...)` 已改成前置 router，内部持有两套独立 processor。
4. `StandardCommandOperator` / `AgenticCommandOperator` 已分离。
5. chat / reply chat op 已移除 mode guard。

当前仍待继续：

1. 继续清理 agentic 侧残留的 shared cutover / runtime adapter，让 initial run 与 resume 彻底收口到同一个 processor。
2. 补更多端到端验证，覆盖标准链路不会误入 runtime、agentic 链路会正确持有 ownership。

### Phase 2: 收缩共享 chat entry 的职责

目标：让 agentic 继续独立演进，但不反向影响标准模式。

涉及文件：

- `internal/application/lark/agentruntime/chat_entry.go`
- `internal/application/lark/agentruntime/chat_response.go`
- `internal/application/lark/agentruntime/default_chat_generation_executor.go`

执行项：

1. 明确 `ChatEntryHandler` 只服务 agentic 入口。
2. 标准模式不再依赖 `ChatGenerationPlan` 的 runtime executor。
3. agentic prompt / tool loop / approval 继续单独收敛。

### Phase 3: 统一 agentic initial run 与 resume

目标：补齐用户指出的“前半截 loop”。

涉及文件：

- `internal/application/lark/agentruntime/runtime_chat.go`
- `internal/application/lark/agentruntime/continuation_processor.go`
- `internal/application/lark/agentruntime/resume_worker.go`
- 后续新增 `RunProcessor` 相关实现

执行项：

1. 初次触发与 resume 恢复走同一个 run processor。
2. 将 `decide -> reply/capability_call/waiting_* -> resume -> complete` 串成统一 durable flow。
3. 旧 chat path 仅保留标准模式，不再承担 agentic 控制权。

## This Round Scope

当前回合只执行 `Phase 1`，理由很直接：

- 先恢复标准 chat，能立刻解决副作用工具被误 pending 的回归问题。
- 同时保住 agentic 已有 runtime 资产，不做大拆大迁。
- 之后再继续把 agentic 前后半 loop 收口到同一个 processor。

## Verification

本轮至少补以下验证：

1. 标准 reply chat 在 `chat_mode=standard` 时走标准 invoker。
2. agentic reply chat 在 `chat_mode=agentic` 时携带 runtime ownership 并走 agentic invoker。
3. agentic follow-up 在 `chat_mode=agentic` 时仍可 attach/supersede active run。
4. `handlers/chat_handler.go` 能按 mode 选择标准或 agentic handler。
