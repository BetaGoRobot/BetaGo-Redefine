# BetaGo_v2 Intent Decision Replay Design

## Scope

本文定义一套面向真实群聊样本的“决策层重放”能力，用于复盘某条消息在不同上下文增强策略下的输入与判定差异。

第一阶段只覆盖：

- 本地 CLI 入口。
- 输入 `chat_id + message_id`。
- `baseline` 与 `augmented` 双 case 对照。
- `dry-run` 与 `--live-model` 双模式。
- 文本报告与 JSON 双输出。

本文不覆盖：

- 真正发送消息。
- 真正执行工具调用或审批链路。
- 群内 `/debug` 命令入口。
- 批量样本回放平台。

Related docs:

- `docs/architecture/chatflow-user-context-augmentation-design.md`
- `docs/architecture/chatflow-user-context-augmentation-plan.md`
- `docs/architecture/conversation-replay-tui-design.md`

## Goal

让研发或产品可以从真实群聊环境抽取一条消息，回答下面几个问题：

1. 这条消息在线上会看到哪些上下文。
2. 开启上下文增强前后，传给 intent analyzer 的输入有什么差异。
3. 这些差异是否改变了 `intent_type`、`need_reply`、`interaction_mode`、`needs_history`、`needs_web` 等核心判定。
4. 最终会更偏向 `standard` 还是 `agentic`。

重点不是“证明增强一定更好”，而是把“增强到底改变了什么、为什么改变”显式暴露出来。

## Why Replay Instead Of Ad-Hoc Debug

现有调试能力分散在：

- `debug_handler.go` 的消息/会话查看。
- `chatflow` 的 prompt 组装。
- `intent` 的线上模型调用。

这些能力能帮助单点排查，但不能稳定回答“同一条消息在增强前后有什么决策差异”。主要问题：

- 无法固定同一条样本做 `baseline/augmented` AB 对照。
- 人工切配置容易污染真实群验证。
- prompt、intent 输入、路由结论没有统一收口。
- 后续难以沉淀样本集做回归。

因此需要一个独立的 replay 入口，把已有线上逻辑以只读方式重新调用，并输出结构化差异报告。

## Recommended Approach

推荐方案：**复用线上已有决策函数，新增一个本地 replay 入口做受控对照。**

不推荐重新实现一套平行规则引擎。原因很直接：

- 重放器的价值在于“接近线上真实行为”。
- 如果把 history/profile/prompt/route 自己再写一遍，长期一定漂移。
- 复用现有函数后，重放器更像“决策调用器 + 对照报告器”，可信度更高。

## Entry Point

第一阶段入口采用本地 CLI：

```bash
go run ./cmd/betago replay intent \
  --chat-id oc_xxx \
  --message-id om_xxx
```

选择本地 CLI 而不是群内 `/debug` 的原因：

- 不污染真实群。
- 更适合重复运行和落盘留样本。
- 更适合后续扩展成批量 replay。

当前实现状态：

- 已落地 `cmd/betago replay intent ...` 本地入口。
- 已在后续扩展中落地 `cmd/betago replay tui ...` 本地入口，用于批量样本 replay 和本地报告。
- 暂不接入群内 `/debug` 命令，避免把重放和生产消息链路耦合。

## Input Contract

第一阶段输入固定为：

- `chat_id`
- `message_id`

原因：

- 比仅传 `message_id` 更稳，符合当前仓库多数查询路径。
- 比 `chat_id + open_id + raw text` 更贴近真实线上样本。

后续如果有必要，再补：

- 只传 `message_id` 的自动反查。
- 纯文本人肉重放模式。

## Execution Modes

### Mode A: Dry Run

默认模式，不调用真实意图模型。

输出：

- 样本消息。
- runtime observation。
- `baseline/augmented` 的 analyzer 输入。
- 命中的 history/profile lines。
- 可静态推导的路由前置信息。

这个模式的目标是让人先确认“增强到底喂给模型什么”。

### Mode B: Live Model

通过 `--live-model` 开启。

额外输出：

- `intent_analysis`
- `route_decision`
- `diff summary`

这个模式用于验证真实模型下增强前后是否改变判定。它更贴近线上，但会受到模型波动影响。

## Comparison Cases

重放器固定构造两个 case：

1. `baseline`
   - 关闭 intent context augmentation。
   - history/profile 注入视为 0。
2. `augmented`
   - 开启 intent context augmentation。
   - 使用当前配置或命令行覆盖的 `history_limit/profile_limit`。

第一阶段不做更多 case 组合，避免输出过于发散。

如需更细对照，可通过参数临时覆盖：

- `--disable-history`
- `--disable-profile`
- `--history-limit`
- `--profile-limit`

## Report Schema

报告统一抽象为一份 `ReplayReport`，文本与 JSON 共用同一数据源。

建议核心字段：

```json
{
  "target": {
    "chat_id": "oc_xxx",
    "message_id": "om_xxx",
    "open_id": "ou_xxx",
    "chat_type": "group",
    "text": "原始消息"
  },
  "runtime_observation": {
    "mentioned": false,
    "reply_to_bot": false,
    "trigger_type": "ambient",
    "eligible_for_agentic": true
  },
  "cases": [
    {
      "name": "baseline",
      "intent_context_enabled": false,
      "history_limit": 0,
      "profile_limit": 0,
      "intent_input": "传给 analyzer 的完整输入",
      "intent_context": {
        "history_lines": [],
        "profile_lines": []
      },
      "intent_analysis": null,
      "route_decision": null
    },
    {
      "name": "augmented",
      "intent_context_enabled": true,
      "history_limit": 4,
      "profile_limit": 2,
      "intent_input": "传给 analyzer 的完整输入",
      "intent_context": {
        "history_lines": ["..."],
        "profile_lines": ["..."]
      },
      "intent_analysis": null,
      "route_decision": null
    }
  ],
  "diff": {
    "intent_input_changed": true,
    "interaction_mode_changed": false,
    "route_changed": false,
    "changed_fields": ["intent_input"]
  }
}
```

设计约束：

- `dry-run` 允许 `intent_analysis` 与 `route_decision` 为空。
- `live-model` 必须补全两者。
- `diff` 只关注真正变化的字段，不重复输出整份 case。

## Terminal Output

默认输出人读友好的文本报告，建议分为 4 个块：

1. `Target`
2. `Baseline`
3. `Augmented`
4. `Diff Summary`

每个块优先展示：

- 当前消息。
- 关键上下文 lines。
- analyzer 输入预览。
- 关键判定字段。

文本报告的目标是人工复盘；完整机器可读数据交给 `--json`。

## JSON Output

通过 `--json` 开启结构化输出。

建议支持：

- 直接打印到 stdout。
- `--output <path>` 落盘。

JSON 输出主要服务于：

- 批量样本统计。
- 后续回归集构建。
- 离线对比脚本。

## Manual Replay Workflow

建议按下面流程从真实群聊样本做单条重放：

1. 在目标群里发送 `/debug chatid`，记录当前 `chat_id`。
2. 对目标消息做引用回复后发送 `/debug msgid`，记录目标 `message_id`。
3. 本地先跑 dry-run，确认增强前后喂给 intent analyzer 的输入差异：

```bash
go run ./cmd/betago replay intent \
  --chat-id oc_xxx \
  --message-id om_xxx
```

4. 如果要看真实模型判定差异，再补 `--live-model`：

```bash
go run ./cmd/betago replay intent \
  --chat-id oc_xxx \
  --message-id om_xxx \
  --live-model
```

5. 如果要给后续脚本消费，改用 JSON 并落盘：

```bash
go run ./cmd/betago replay intent \
  --chat-id oc_xxx \
  --message-id om_xxx \
  --json \
  --output /tmp/intent-replay.json
```

6. 如果要拆解增强来源，可用：

- `--disable-history`
- `--disable-profile`
- `--history-limit N`
- `--profile-limit N`

解释口径：

- dry-run 重点看 `baseline/augmented` 的 `intent_input`、`history_lines`、`profile_lines`。
- live-model 重点看 `intent_analysis.interaction_mode`、`needs_history`、`needs_web`、`route_decision.final_mode`。
- 如果只有 `intent_input_changed=true`，说明增强改变了输入但暂未改变模型判定。
- 如果 `interaction_mode_changed=true` 或 `route_changed=true`，说明增强已经影响到后续执行链路选择。

## Data Sources

Replay 只读复用当前线上数据源：

- 消息与历史：`history.New(...)`
- thread/parent message：`larkmsg.GetMsgFullByID` / `larkmsg.GetAllParentMsg`
- intent context augmentation：`buildIntentAnalyzeInput(...)`
- 用户画像索引：`defaultIntentProfileLoader(...)`
- chatflow prompt 组装：`BuildStandardChatExecutionPlan` / `BuildAgenticChatExecutionPlan`
- runtime observation：现有 `observePotentialRuntimeMessage(...)`

原则：

- 不复制规则。
- 不新增业务语义分叉。
- 优先把现有内部逻辑抽成可复用只读函数。

当前实现备注：

- `GetMsgFullByID` 返回的 message detail 不含 `chat_type`。
- 第一阶段 replay 面向真实群聊样本，因此 target normalization 先固定写入 `chat_type=group`。
- 如果后续需要覆盖 p2p/topic_group，需要补独立 chat metadata 查询或显式 CLI override。

## Side-Effect Policy

Replay 必须是只读能力。

第一阶段禁止：

- 发送回复。
- 写入消息索引。
- 写入画像索引。
- 执行工具调用。
- 进入审批链路。

如果需要输出“可能会调用哪个工具”，应停留在计划层或意图层，绝不真的执行。

## Suggested CLI Flags

第一阶段建议支持：

- `--chat-id`
- `--message-id`
- `--json`
- `--output`
- `--live-model`
- `--history-limit`
- `--profile-limit`
- `--disable-history`
- `--disable-profile`

扩展参数留到后续再做，不要一次把批量目录、采样集、CSV 导出都塞进来。

## Validation Workflow

推荐人工验证流程：

1. 在真实群里挑一条怀疑“增强会改变判断”的消息。
2. 记录 `chat_id + message_id`。
3. 先跑 dry-run，确认增强前后 `intent_input` 和上下文 lines 差异是否符合预期。
4. 再跑 `--live-model`，观察 `interaction_mode / need_reply / reply_mode / needs_history / needs_web` 是否变化。
5. 人工判断这些变化是改善还是污染。
6. 把样本沉淀为 replay case，后续用于回归。

## Rollout

建议分三步：

1. **Step 1: 单样本 CLI**
   - 支持文本报告。
   - 支持 JSON 导出。
2. **Step 2: live-model 对照**
   - 接入真实 intent 调用。
   - 补齐 diff summary。
3. **Step 3: 样本集化**
   - 支持批量跑一组固定样本。
   - 用于评估 context augmentation 改动。

## Success Criteria

- 同一条消息可稳定输出 `baseline/augmented` 对照结果。
- 人工能快速看出“增强喂给模型什么”。
- `--live-model` 可解释增强前后决策差异。
- 输出可直接保存为后续回归样本。
- 全过程无副作用。
