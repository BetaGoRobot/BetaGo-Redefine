# 群聊 Conversation Runtime 与交互续接设计

**日期：** 2026-07-28
**状态：** 已完成对话设计确认，等待书面 Spec 审阅

## 1. 背景

当前 Chat 流程只把飞书消息视为 Agent 输入。工具可以发送确认卡片，但模型在卡片发出后会立即收到“等待用户确认”这一工具结果并结束本轮生成。用户之后点击卡片时，事件进入：

```text
CardActionHandler
  -> cardaction.Dispatch
  -> 具体业务 handler
  -> toast / card response
```

该路径可以直接完成 schedule 修改等业务动作，但点击行为和执行结果不会重新进入 Chat，Agent 因而无法感知“用户确认了什么、业务执行结果是什么、下一步是否需要继续”。现有 `edit_schedule` 正是这一问题的代表：

```text
edit_schedule tool
  -> 保存进程内 PendingEdit
  -> 发送确认卡
  -> Chat 回复“请确认”

用户点击
  -> schedule.edit_confirm
  -> 直接 UpdateTask
  -> 返回 toast / 新卡片
  -> Chat 不再继续
```

更大的问题是，群聊没有稳定的 Agent 会话生命周期。当前普通消息基本都是独立进行意图识别和回复决策，缺少“一个群正在与 Agent 共同推进某个目标”的激活态。卡片点击、异步结果、schedule 触发也没有统一的 continuation 入口。

本设计新增一个轻量、事件驱动的群聊 Conversation Runtime。它不恢复历史上已经删除的整套重型 agentic runtime，也不替换现有 Chat、tool registry、card handler 或 scheduler；它在这些能力之外提供统一事件入口、群级激活状态、durable waiting/continuation 以及可检索的会话记忆。

## 2. 已确认的产品决策

### 2.1 群聊作用域

- 每个群同一时刻最多存在一个 active conversation。
- active conversation 对群内所有成员共享，不按发起人隔离，也不按 thread 隔离。
- 任何群成员的相关消息都可以推进当前 conversation。
- 显式新话题可以结束或替换当前 conversation，防止上下文串线。

### 2.2 激活方式

- `@机器人`、`/bb`、回复机器人等显式入口可以直接激活。
- 静默态下，意图识别可以在高置信度判断为持续求助、协作任务或多步任务时自动激活。
- 一次性简单问答可以正常回复，但不要求创建 active conversation。

### 2.3 活跃窗口

- 普通 active conversation 的默认无活动窗口为 15 分钟。
- 只有相关的用户输入刷新活跃期限。
- Agent 自己的输出不刷新期限，也不能再次触发输入入口。
- waiting 状态不受 15 分钟无活动窗口影响；它由 interaction 到期时间或显式取消控制。

### 2.4 激活态消息消费

- 激活态下，每条用户消息都被记录为 conversation event。
- 普通消息先经过轻量相关性门控；只有相关消息进入完整 Agent turn。
- 无关消息不污染当前会话上下文，也不刷新活跃期限。
- 卡片 interaction、异步结果和 schedule continuation 通过稳定 ID 精确关联，直接进入完整 Agent，不经过相关性猜测。

### 2.5 Agent 输出语义

完整 Agent turn 不等于每次必须发言。它可以产生：

```text
observe_only | reply | act | wait | close
```

“高强度参与”表示 Agent 持续感知相关输入并推进目标，而不是机械地回复每一条群消息。

## 3. 目标与非目标

### 3.1 目标

- 让消息、卡片操作、capability 结果、异步结果和 schedule 事件都能成为 Agent 输入。
- 建立群级 silent/active/waiting 生命周期。
- 支持发卡后 durable 等待，用户操作后恢复同一个 conversation。
- 保持现有 card action、Chat、command、tool 和 scheduler 路径兼容。
- 支持重复 callback、多实例消费、进程重启和基础故障恢复。
- 利用 PostgreSQL 保存强一致状态，利用 OpenSearch 提供 conversation timeline 和语义召回。
- 允许按群、按能力逐步灰度，并可随时回退旧路径。

### 3.2 第一阶段非目标

- 不恢复历史上完整的 planner/capability/approval/runtime 实现。
- 不支持同一个 run 同时等待多个阻塞 interaction。
- 不要求迁移全部现有卡片。
- 不把翻页、筛选、刷新、音乐播放等纯 UI 操作强制送入完整 Agent turn。
- 不用 OpenSearch 承担锁、队列、幂等或状态机 source of truth。
- 不在第一阶段引入 Redis resume queue。
- 不让 Agent 在无 active conversation、无明确结果交付义务时自行反复发言。

## 4. 总体架构

```text
Lark message / card callback / scheduler / async completion
                         |
                         v
                 Conversation Ingress
                         |
                 normalize + dedupe
                         |
                         v
              Conversation Coordinator
              /          |            \
      activation     relevance       interaction
       policy          gate          validation
              \          |            /
                         v
                  Durable Agent Step
                         |
                  runtime.Executor
                         |
                         v
                   Turn Processor
             /             |             \
      legacy/capability   LLM turn      wait/close
             \             |             /
                         v
                  reply / card / no-op
                         |
                         v
             PostgreSQL state + OS projection
```

核心组件如下：

1. **Conversation Ingress**
   - 将不同来源标准化为 `ConversationEvent`。
   - 生成稳定 dedupe key。
   - 拒绝机器人自身输出形成的回环。

2. **Conversation Coordinator**
   - 加载群级 session 和 active run。
   - 执行激活、相关性、新话题和 supersede 策略。
   - 验证 waiting interaction 的 token、revision 和 actor。
   - 使用事务维护一个群只能有一个 active run。

3. **Turn Processor**
   - 消费 queued step。
   - 执行 capability 或完整 LLM turn。
   - 追加 observation、reply、wait 或 terminal step。
   - 不直接依赖飞书 callback 请求生命周期。

4. **Context Composer**
   - 组合 PostgreSQL 当前强一致状态、当前 run 事件以及群聊长期召回。

5. **Projection Worker**
   - 将已提交的 conversation step 投影到 OpenSearch。
   - 失败可重试，不阻塞用户主链。

## 5. 生命周期与存储模型

### 5.1 逻辑状态

产品层状态为：

- `silent`：群级 session 没有 active run。普通消息只走现有意图识别和激活判断。
- `active`：存在一个可接收相关消息的 run。
- `waiting`：active run 正等待用户 interaction、异步结果或 schedule。
- `closed`：一次 run 已完成、失败、取消、超时或被新话题替换。

`silent` 不是 `agent_runs` 的一条状态，而是 `agent_sessions.active_run_id` 为空。`closed` 对应 run 的多个终态。

### 5.2 复用现有表

#### `agent_sessions`

一个群和机器人身份对应一个长期存在的容器：

- 唯一作用域保持 `app_id + bot_open_id + scope_type + scope_id`。
- 群级场景使用 `scope_type=chat`、`scope_id=chat_id`。
- `active_run_id` 指向当前唯一 active/waiting run。
- `last_message_id` 和 `last_actor_open_id` 用于诊断，不作为上下文 source of truth。

#### `agent_runs`

一条 run 表示一次激活周期：

- `queued/running`：正在处理。
- `waiting_approval/waiting_callback/waiting_schedule`：不同等待原因。
- `completed/failed/cancelled`：终态。
- `goal` 保存当前目标摘要。
- `revision` 用于 interaction 乐观并发控制。
- `waiting_token` 只保存 opaque token 的 hash。
- `last_response_id` 可以作为模型 provider continuation 优化，但不是恢复所需的唯一状态。
- `lease_expires_at/heartbeat_at/worker_id` 用于执行存活和 stale repair。

#### `agent_steps`

按 run 内严格递增 index 保存：

- 输入事件；
- activation/relevance 决策；
- model turn；
- capability call/result；
- wait/resume；
- reply/close。

queued step 同时作为第一阶段的 durable inbox。实现时需扩展：

- `dedupe_key`：非空时在 run 内唯一。
- `attempt_count`。
- `worker_id`。
- `lease_expires_at`。
- 可选 `retry_of_step_id`，用于审计重试关系。

stale running step 可以在 lease 过期后重新回到 queued。已经产生外部副作用的 capability 必须通过幂等键返回原结果，不能因为 step 重试再次执行。

### 5.3 投影 Outbox

新增轻量 durable outbox，避免 PostgreSQL提交成功但 OpenSearch 写入丢失。每个需要投影的 step 在同一数据库事务中写入一条 outbox 记录：

- `id`
- `step_id`，唯一
- `index_alias`
- `document_id`
- `payload_json`
- `status`
- `attempt_count`
- `next_attempt_at`
- `worker_id`
- `lease_expires_at`
- `last_error`
- timestamps

Projection Worker 使用 claim/lease 模式消费。OpenSearch 写入以稳定 `document_id` upsert，天然幂等。

## 6. 统一事件协议

```go
type EventType string

const (
    EventMessage          EventType = "message"
    EventCardAction       EventType = "card_action"
    EventCapabilityResult EventType = "capability_result"
    EventSchedule         EventType = "schedule"
    EventAsyncResult      EventType = "async_result"
    EventTimeout          EventType = "timeout"
)

type ConversationEvent struct {
    ID            string
    Type          EventType
    ChatID        string
    ActorOpenID   string
    RunID         string
    InteractionID string
    SourceRef     string
    OccurredAt    time.Time
    Payload       json.RawMessage
}
```

要求：

- `ID` 是 runtime 内部稳定 ID。
- `SourceRef` 保存平台 event ID、message ID 或 schedule execution ID。
- callback dedupe key 使用 `interaction_id + revision + action`。
- message dedupe key 优先使用平台 event ID，缺失时使用 message ID。
- Payload 必须是有版本的结构化对象，不以自然语言作为唯一事实载体。

## 7. 激活、相关性与新话题策略

### 7.1 静默态

静默态保留现有消息流程，并在意图识别之后增加：

```go
type ActivationDecision struct {
    ShouldActivate bool
    Confidence     int
    GoalSummary    string
    Reason         string
}
```

规则：

- 显式 mention、`/bb`、reply-to-bot 直接激活。
- 普通消息只有高置信度多步任务或持续协作意图才自动激活。
- 简单问答允许旧 Chat 回复，但不必创建 run。
- 自动激活先以 shadow 方式记录和评估，再开放真实行为。

### 7.2 激活态

每条用户消息先写 event，再运行：

```go
type RelevanceDecision struct {
    Relation string // related, new_topic, unrelated
    NeedTurn bool
    Reason   string
}
```

规则：

- `related`：进入完整 Agent turn，并刷新 active expiry。
- `unrelated`：保留事件，不进入当前 run 的模型上下文，不刷新 expiry。
- `new_topic + 显式触发`：取消旧 run，原因记为 `superseded_by_new_topic`，再创建新 run。
- `new_topic + 普通消息`：重新应用自动激活门槛；未达到门槛时不替换旧 run。
- interaction、async result、schedule event 按 run/step ID 直接路由。

### 7.3 超时

- active run 连续 15 分钟没有相关用户输入时关闭。
- waiting run 使用 interaction 自己的 `expires_at`。
- interaction 到期追加 timeout event，由 Agent决定给出超时提示或静默关闭。
- run 终态后必须以事务 compare-and-swap 清理 `session.active_run_id`。

## 8. Interaction 与 Callback Continuation

### 8.1 卡片信封

Runtime 发出的 continuation 卡片携带：

```text
run_id
step_id
interaction_id
revision
token
interaction_kind
continue_agent=true
```

token 是高熵 opaque 值，数据库只保存 hash。Payload 不包含待执行 capability 的完整可信参数；可信参数保存在 wait step 中，callback 只携带引用和用户选择。

### 8.2 两类事件

一次 interaction 产生两个不同事实：

1. `card_action.received`
   - 用户做了什么。
   - 记录 actor、action、selection、callback source ref。

2. `capability_result`
   - 确定性业务执行结果。
   - 记录 capability、target、status、structured output 和 error。

Agent continuation 主要消费第二类事件，同时可以从上下文看到用户选择。

### 8.3 确认不是二次决策

用户点击“确认”已经构成明确授权。模型不能在 callback 后重新决定是否执行同一个修改。正确流程是：

```text
validate interaction
  -> execute deterministic capability
  -> persist result
  -> resume Agent
  -> Agent decides follow-up only
```

### 8.4 执行模式

V2 action 可以声明：

- `inline_transactional`
  - 适合本地、短耗时数据库操作。
  - callback 内完成业务事务、capability result 和 outbox 写入后返回。
  - `edit_schedule` 第一阶段可以采用该模式。

- `deferred`
  - 适合外部 API 或慢操作。
  - callback 只完成 validation、claim 和 queued capability step，然后立即返回 accepted toast/card。
  - worker 完成后产生 capability result。

两种模式都不能在 callback 请求内等待 LLM。

### 8.5 幂等与权限

- interaction 必须同时校验 run、wait step、revision、token、actor 和业务权限。
- `interaction_id + revision` 是 capability execution idempotency key。
- 重复 callback 返回“操作已处理”，并返回已有 outcome 的用户可见摘要。
- callback actor 不必是最初发起人；是否允许其他群成员确认由具体 capability policy 决定。
- schedule 修改继续沿用现有创建者/管理员权限判断。

## 9. Card Action 兼容层

现有 registry 和 handler 签名保留。新增可选 V2 contract：

```go
type ActionResult struct {
    Response *callback.CardActionTriggerResponse
    Outcome  *ConversationEvent
    Continue bool
}
```

分发优先级：

1. Parse action。
2. 如果没有 runtime envelope，走原 handler。
3. 如果是 runtime envelope 但属于 UI-only action，走原 handler并可选记录审计。
4. 如果 `continue_agent=true`，走 V2 interaction ingress。
5. V2 执行成功后持久化 outcome 并触发 worker。

第一批 V2 迁移：

- `schedule.edit_confirm`
- `schedule.edit_cancel`
- Agent 发起的副作用审批
- Agent 发起的补充表单
- 异步任务完成回调

第一阶段保持 V1：

- schedule list/query/view
- 翻页、筛选、刷新
- 音乐播放和详情
- 普通配置/权限管理卡
- 非 Agent 发起的历史卡片

旧卡片没有 runtime envelope，因此行为完全不变。

## 10. Schedule 修改端到端流程

### 10.1 发起

```text
Agent calls edit_schedule
  -> validate task and permission
  -> build trusted pending capability payload
  -> append wait step
  -> run = waiting_approval
  -> revision++
  -> send confirmation card with references
  -> reply decision = wait
```

`PendingEdit` 不再只保存在进程内 map。可信修改内容保存在 durable wait step，进程重启后仍可恢复。

### 10.2 确认

```text
Lark callback
  -> parse runtime envelope
  -> lock run/wait step
  -> validate token hash, revision, actor, expiry, task permission
  -> claim interaction
  -> UpdateTask with idempotency key
  -> append card_action + capability_result
  -> run waiting_approval -> queued
  -> commit + return resolved card/toast
  -> executor processes Agent continuation
  -> reply / act / wait / close
```

### 10.3 取消

取消不执行 capability，追加：

```json
{
  "capability": "edit_schedule",
  "status": "cancelled_by_user",
  "target_id": "schedule-id",
  "interaction_id": "..."
}
```

随后恢复 Agent，由其自然结束任务或询问替代方案。

## 11. Context Composer 与 OpenSearch

### 11.1 三层上下文

#### 强一致当前状态

从 PostgreSQL读取：

- run goal/status/revision；
- 当前 wait；
- 最近 capability result；
- 当前未完成 action；
- 最近 reply reference。

#### 当前 conversation

从新 OpenSearch index 按 `run_id` 召回：

- related user messages；
- card choices；
- capability calls/results；
- Agent replies；
- schedule/async events。

OpenSearch 不可用时，回退读取 PostgreSQL最近 agent steps。

#### 群聊长期上下文

复用现有：

- Lark message index；
- history/chunk/retriever；
- 群成员和群画像能力；
- correction/persona/extra context。

长期召回围绕当前 `run.goal` 进行，而不是无差别注入整个群历史。

### 11.2 新索引

使用 alias：

```text
agent_conversation_events
  -> agent_conversation_events_v1
```

核心字段：

- `event_id`
- `chat_id`
- `run_id`
- `step_id`
- `event_type`
- `actor_open_id`
- `content`
- `structured_payload`
- `relevance`
- `capability_name`
- `status`
- `source_message_id`
- `occurred_at`
- `embedding`，后续可选

mapping 按版本演进，通过 alias 切换，不修改现有 Lark 消息 index。

### 11.3 一致性边界

- PostgreSQL是状态、幂等和执行结果 source of truth。
- OpenSearch是检索投影，可以最终一致。
- 投影失败不能导致 callback、业务动作或 Agent continuation 失败。
- Projection Worker 恢复后补写 outbox。
- Context Composer 必须暴露降级标记，用于日志和 metrics。

## 12. 执行、并发与故障恢复

### 12.1 Worker

- 使用现有 `runtime.Executor` 承载 turn 和 projection 任务。
- 不新增无界 goroutine。
- PostgreSQL claim 使用 `FOR UPDATE SKIP LOCKED`。
- 同一个 run 同时最多一个 turn worker 持有有效 lease。

### 12.2 Active run 互斥

创建或替换 active run 时：

1. 锁定群级 session。
2. 检查 `active_run_id`。
3. 根据 activation/new-topic policy 保留、拒绝或取消旧 run。
4. 创建新 run。
5. CAS 更新 `active_run_id`。
6. 提交后再启动执行。

### 12.3 重试语义

- event persistence 失败：callback 返回失败，不假装已接收。
- capability 失败：写入 failed outcome，Agent可以解释或选择重试。
- LLM 失败：重试 model step，不重放已经完成的 capability。
- reply delivery 失败：保留生成结果，单独重试 delivery。
- stale worker：lease sweeper 把未完成 step 重新入队。
- OpenSearch 失败：只重试 projection outbox。

### 12.4 外部副作用

本地数据库 capability 应尽量将 idempotency record、业务修改和 outcome 放在同一事务。外部系统无法共享事务时：

- 传递稳定 idempotency key；
- 在本地记录 started/succeeded/failed；
- 超时后先查询外部结果，再决定是否重试；
- 不允许仅凭网络超时盲目重复副作用。

## 13. 可观测性

所有相关 span/log 携带：

- `conversation.session_id`
- `conversation.run_id`
- `conversation.step_id`
- `conversation.event_id`
- `conversation.interaction_id`
- `conversation.event_type`
- `conversation.run_status`

指标至少包括：

- activation decision 数量和命中率；
- auto activation 数量、关闭原因和人工负反馈；
- relevance decision 分布；
- active/waiting run 数量；
- callback validation failure；
- interaction duplicate；
- capability execution duration/status；
- queued step backlog 和 lease recovery；
- continuation latency；
- OpenSearch projection backlog/failure；
- Context Composer PostgreSQL fallback 次数。

日志中不得记录原始 interaction token。

## 14. Control/Candidate 并轨评测

### 14.1 目的

灰度期同时运行：

- **Control**：当前意图识别、上下文拼装、工具循环和 Chat 回复链路。
- **Candidate**：新的 activation、relevance、Conversation Context Composer 和 Agent turn 链路。

两条链路消费同一批群消息和外部 observation，但同一时刻只有一条 serving lane 可以向真实群聊输出。初始阶段 Control serving、Candidate shadow；后续可以 Candidate serving、Control shadow。

评测不只比较最终回复，还要比较：

- 机器人是否应该参与当前话题；
- 选择哪条消息作为切入锚点；
- 当前消息属于旧话题、新话题还是无关消息；
- 两条链路实际选择了哪些上下文；
- 回复质量和任务推进质量；
- 回复后的用户反馈与话题走向。

### 14.2 Evaluation Cohort

一次评测任务定义为一个时间段 cohort：

```go
type EvaluationCohort struct {
    ID              string
    ChatIDs         []string
    StartAt         time.Time
    EndAt           time.Time
    ServingLane     string // control | candidate
    ControlVersion  string
    CandidateVersion string
    JudgeConfig     string
    SamplingPolicy  string
}
```

cohort 固定两条链路的配置、prompt、模型、代码版本和 serving lane，防止一个时间段内配置漂移后仍被当作同一实验。

支持：

- 指定群和时间范围直接创建 cohort；
- 对持续灰度流量按天自动滚动 cohort；
- 通过 chat、时间、lane、话题决策、回复决策和反馈类型筛选。

cohort 生命周期为 `collecting -> waiting_late_feedback -> finalized`。到达 `EndAt` 后继续接收 24 小时 late feedback，再冻结该版本的聚合结果；冻结后到达的反馈仍可追加，但会生成新的结果版本，不静默改写旧报告。

### 14.3 Evaluation Episode

评测单元不是孤立的一问一答，而是以一次“是否参与/如何参与”的决策为锚点的 episode：

```text
pre-window
  -> decision anchor
  -> lane outputs
  -> post-window
  -> feedback
  -> judge/human evaluation
```

每个 episode 保存：

- `anchor_event_id` 和 anchor message；
- 话题 ID 或推导出的 topic boundary；
- serving lane；
- pre/post 窗口边界；
- Control/Candidate lane output；
- 直接和间接用户反馈；
- 自动 Judge 结果；
- 人工评测版本。

episode 初始采集策略：

- **前向窗口**：最近 20 条原始群消息。
- **后向窗口**：直到话题边界、15 分钟或 50 条消息，任一先到。
- **Late feedback**：用户明确回复、reaction、纠正或卡片操作可以在 24 小时内追加。

窗口数字是 cohort 配置，默认值与上述一致。

### 14.4 Lane Output

两条链路都必须保存当时真实使用的输入和决策，不允许事后仅靠当前索引重建：

```go
type EvaluationLaneOutput struct {
    Lane               string // control | candidate
    ActivationDecision any
    RelevanceDecision  any
    JoinDecision       string // join | skip
    TopicRelation      string // related | new_topic | unrelated
    SelectedContext    ContextSnapshot
    ExcludedContext    []ExcludedContextItem
    ToolPlan           any
    Reply              string
    OutputMode         string // actual | shadow
    Latency            time.Duration
    TokenUsage         any
    Error              any
}
```

`ContextSnapshot` 至少保存：

- 选中的消息 ID、顺序和完整内容快照；
- 被召回的 chunk/document ID、score 和内容快照；
- card action、capability result 和 schedule event；
- session goal、wait 和最近 step；
- system prompt、persona、correction 和额外上下文版本；
- 总字符数、token 估算和截断信息。

`ExcludedContextItem` 保存消息 ID 和排除原因，例如：

- `outside_window`
- `unrelated`
- `topic_boundary`
- `cutoff`
- `token_budget`
- `deduplicated`

这使评测可以回答“回复不好是生成问题，还是上下文选择问题”。

### 14.5 采样

完整保存以下高价值 episode：

- 两条链路 `join/skip` 不一致；
- topic relation 不一致；
- selected context 差异超过阈值；
- 任一链路调用工具或进入 waiting；
- 发生新话题 supersede；
- 用户有明确回复、reaction、纠正、重复提问或卡片操作；
- 任一链路报错；
- serving reply 收到明显负反馈。

两条链路一致且都 skip 的普通样本只做比例采样，避免评测集被大量无事件消息淹没。采样率必须写入 cohort，聚合指标按采样权重校正。

### 14.6 前向和后向消息

前向消息用于判断：

- 当前讨论的主题和参与者；
- 机器人是否被邀请或是否有合理插话机会；
- 两条链路是否漏选或错选上下文；
- 回复是否引用了过期话题。

后向消息用于识别：

- 用户直接评价：“不是这个意思”“对，就是这样”等；
- 用户重复同一需求；
- 用户继续补充细节；
- 用户是否采用 Agent 建议；
- 用户是否点击确认、完成表单或继续 capability；
- 群聊是否自然继续、被机器人打断或转移话题。

“回复后群聊暂时沉默”不能单独视为负反馈，必须结合语义和群聊节奏判断。

### 14.7 Feedback 归因

反馈记录包含：

- `episode_id`
- `target_lane`
- `target_message_id`
- `feedback_event_id`
- `feedback_type`
- `explicitness`
- `content`
- `occurred_at`
- `attribution_confidence`

归因优先级：

1. 用户直接 reply 某条机器人消息；
2. 对该消息的 reaction；
3. 点击该消息所含卡片；
4. 同 thread/root 的明确纠正；
5. 时间和语义关联推断。

只有实际发送的 serving reply 才能获得线上行为反馈。Shadow reply 没有真实 message ID，不能把随后用户行为直接归因给它。

### 14.8 自动 Judge

Judge 输入包含：

- episode pre-window；
- 两条链路的 topic/join/context 决策；
- 匿名、随机顺序的 A/B 回复；
- capability/tool observation；
- serving lane 的 post-window 和显式反馈；
- 数据完整性和降级标记。

Judge 执行两层评分：

#### 决策与上下文

- 参与时机是否合适；
- 切入锚点是否正确；
- topic relation 是否正确；
- 是否漏掉关键上下文；
- 是否引入无关或过期上下文；
- tool plan 是否合理。

#### 回复质量

- 是否真正回应当前问题；
- 上下文事实是否使用正确；
- 是否遗漏关键约束；
- 是否推动任务向前；
- 是否与工具结果一致；
- 事实是否可靠；
- 群聊语气、篇幅和打扰程度是否合适。

Judge 同时输出：

- A/B winner 或 tie；
- 两条链路的绝对维度分；
- 问题标签；
- 简短证据和理由；
- 置信度；
- 是否需要人工复核。

如果一条链路选择 skip、另一条选择 reply，Judge 直接比较 `skip vs reply` 的合理性，不强迫 skip lane 生成反事实回复。

### 14.9 结果反馈与因果限制

Shadow cohort 可以公平比较：

- join/topic/context 决策；
- 两条回复本身的离线质量；
- latency、token 和错误率。

但 post-window 的真实用户行为只由 serving lane 的真实输出引起，不能作为 Candidate shadow reply 的因果效果。报告必须区分：

- `offline_pairwise_quality`
- `served_lane_outcome`
- `shadow_counterfactual_estimate`

只有后续进入随机 serving 实验，才能比较真实用户反馈和任务完成率。随机 serving 建议按 `chat_id + run_id` 粘性分配，保证同一个 active conversation 不在两条链路之间来回切换。

### 14.10 Shadow 安全与公平

- Candidate 文本、卡片和 reaction 全部进入 shadow sink，不发送给真实群聊。
- 只读工具可以执行，但结果必须被捕获为 observation snapshot。
- 为降低外部数据时间漂移，两条链路优先复用同一份只读工具结果。
- 副作用工具只有 serving lane 可以执行；shadow lane 只生成 tool plan。
- serving lane 已产生的业务结果可以回放给 shadow lane，但必须标记 `replayed_from_control` 或 `replayed_from_candidate`。
- Judge 使用调用时保存的外部结果，不在评测时重新查询时间敏感数据。
- Shadow 执行设置独立 token、并发和超时预算，不能挤占线上 serving worker。

### 14.11 存储与索引

PostgreSQL保存 cohort、episode、lane output 元数据、Judge 结果和人工评测版本。完整消息/context 快照可以通过稳定 document ID 投影到独立索引：

```text
agent_conversation_evaluations
  -> agent_conversation_evaluations_v1
```

建议逻辑实体：

- `evaluation_cohorts`
- `evaluation_episodes`
- `evaluation_lane_outputs`
- `evaluation_feedback`
- `evaluation_judgments`

人工评测和 Judge 结果采用 append-only version，不覆盖历史。

### 14.12 最小人工抽检工作台

WebUI 至少提供：

- 按 cohort、群、时间、分歧类型、反馈和 Judge 结果筛选；
- 一屏展示 pre-window、anchor 和 post-window；
- Control/Candidate 上下文选择差异；
- 匿名 A/B 回复；
- tool plan 和 observation；
- 用户明确反馈的 message-level 高亮；
- 人工 winner、维度分、问题标签和备注；
- 标记 Judge 错误并进入 calibration 集。

首期优先展示：

- join/skip 分歧；
- new-topic 分歧；
- context 选择差异；
- 用户直接点评；
- Judge 低置信度或人工/Judge 冲突。

### 14.13 核心聚合指标

- join decision agreement；
- topic relation agreement；
- Candidate 相对 Control 的 missed-opportunity / unwanted-interruption；
- context precision/recall 的人工与 Judge 代理分；
- pairwise win/tie/loss；
- served reply 显式正负反馈率；
- 重复提问率；
- capability 继续率和任务完成率；
- token、latency 和错误率；
- Judge/人工一致率；
- 按 chat、话题类型、显式/自动激活拆分的指标。

## 15. 配置与灰度

增加独立开关，支持全局默认和按群覆盖：

```text
conversation_runtime_enabled
conversation_callback_continuation_enabled
conversation_active_mode_enabled
conversation_auto_activation_enabled
conversation_active_ttl
conversation_parallel_evaluation_enabled
conversation_evaluation_serving_lane
```

默认 `conversation_active_ttl=15m`。

灰度阶段：

1. **Shadow activation/relevance**
   - 只记录决策，不改变回复和路由。

2. **Schedule callback continuation**
   - 只迁移 `edit_schedule`。
   - 验证 durable wait、确认、取消、重复点击和 Agent续接。

3. **通用 interaction**
   - 接审批、补充表单、async result、schedule continuation。

4. **显式激活态**
   - mention、`/bb`、reply-to-bot 创建 active run。
   - 静默态保留旧 Chat。

5. **自动激活**
   - 在 shadow 样本和指标稳定后按群开启。

关闭任意开关时：

- 旧卡片继续 V1 handler。
- 新卡片已存在的 waiting interaction 仍允许完成或取消，避免产生不可操作的悬挂卡片。
- 禁止创建新的 runtime interaction。
- 完成现有 waiting 后关闭对应 run。

## 16. 测试策略

### 16.1 单元测试

- run 状态迁移。
- active TTL 与 waiting expiry。
- activation/relevance policy。
- new topic supersede。
- token hash、revision、actor 和权限校验。
- callback dedupe。
- capability idempotency。
- V1/V2 card dispatch。
- Context Composer 分层与降级。

### 16.2 PostgreSQL 集成测试

- session active run CAS。
- 多 worker `SKIP LOCKED` claim。
- stale lease recovery。
- interaction claim exactly-once。
- capability transaction 与 outcome 原子性。
- step/outbox 同事务写入。

### 16.3 OpenSearch 集成测试

- alias 和 mapping。
- outbox 重试与文档 upsert。
- run 过滤。
- relevance 过滤。
- capability/card event 检索。
- index 不可用时 PostgreSQL fallback。

### 16.4 端到端场景

1. 用户要求修改 schedule。
2. Agent 调用 `edit_schedule` 并进入 waiting。
3. 用户点击确认。
4. schedule 只修改一次。
5. callback 立即返回 resolved UX。
6. Agent消费 capability result 并继续回复。

同时覆盖：

- 取消；
- 重复点击；
- callback 重投；
- callback 后进程重启；
- LLM 暂时失败；
- OpenSearch 暂时失败；
- waiting 超时；
- 新话题替换旧 run；
- 无关群聊不污染 active context；
- 旧 schedule 卡片继续原行为。

### 16.5 并轨评测测试

- 同一个 anchor 只创建一个 episode。
- Control/Candidate 输入 message set 一致，lane-specific context 选择可独立保存。
- 匿名 A/B 顺序稳定随机，Judge 不看到 lane 名称。
- Candidate shadow 不产生任何 Lark 或外部副作用。
- 只读 tool observation snapshot 可以被两条链路复用。
- 前向 20 条和后向 topic/15 分钟/50 条边界正确。
- 24 小时 late feedback 可以追加且不会修改旧 Judge 版本。
- reply/reaction/card callback 能按 message ID 精确归因。
- Shadow lane 的 post-window 不被错误标记为因果结果。
- cohort 配置和版本冻结。
- OpenSearch 暂停后，评测 outbox 恢复可补齐完整 episode。

## 17. 安全与数据治理

- interaction token 仅向用户卡片暴露一次，服务端只保存 hash。
- callback 必须重新执行当前业务权限校验，不能只信任发卡时结果。
- structured payload 中的可信 capability 参数保存在服务端，不接受客户端回传覆盖。
- OpenSearch index 按现有群聊数据安全等级管理访问。
- 对 conversation event 设置可配置 retention，并支持按 chat/run 删除。
- 日志和 metrics 仅记录 ID、状态和大小，不记录敏感完整 payload。
- Judge 和人工工作台按现有 WebUI 敏感会话数据等级鉴权。
- Candidate shadow 禁止调用副作用 capability 的约束必须在执行层强制，而不是依赖 prompt。
- 评测导出默认脱敏用户标识，并记录导出审计。

## 18. 实施边界与推荐切片

实施应拆为三个可独立验收但协议一致的切片：

### 切片 A：Conversation 与 callback 闭环

1. 扩展 agentstore 的 active run、step queue、lease 和 dedupe contract。
2. 增加 Conversation Event/Coordinator/Worker 骨架。
3. 增加 runtime card envelope 与 V2 dispatch。
4. 把 `edit_schedule` 的进程内 PendingEdit 迁为 durable wait step。
5. 实现 schedule confirm/cancel outcome 与 Agent continuation。
6. 增加 OpenSearch v1 index、outbox 和 PostgreSQL fallback。
7. 增加 shadow activation/relevance 记录，但暂不开放自动激活。

### 切片 B：并轨采集与自动 Judge

1. Cohort/episode/lane output/feedback/judgment contract。
2. Control/Candidate fan-out 和 shadow sink。
3. 精确 Context Snapshot。
4. 前向/后向窗口 collector 和 late feedback。
5. 自动 Judge、版本化结果和聚合 API。
6. 新 evaluation index 和投影 outbox。

### 切片 C：人工工作台与激活态灰度

1. 最小人工抽检页面。
2. Judge calibration workflow。
3. 显式 active mode。
4. Shadow 指标稳定后开放自动激活。
5. 条件成熟后增加 run-sticky randomized serving。

三个切片必须先定义共享 event/run/context snapshot 版本协议。切片 A 和 B 的存储基础可以并行准备，但 Candidate 不得在 callback 闭环和 shadow sink 验证前执行真实副作用。

## 19. 验收标准

- Agent 发出的 schedule 修改确认卡可以在点击后恢复原 run。
- 点击和实际执行结果都以结构化 event 进入 Agent上下文。
- 同一 interaction 无论重复点击、callback 重投或 worker 重试，schedule 最多修改一次。
- 进程重启不会丢失 waiting 修改内容。
- callback 不等待 LLM。
- 旧卡片和未迁移 handler 行为保持不变。
- PostgreSQL是可独立恢复的状态 source of truth。
- OpenSearch 故障不会阻塞业务；恢复后可补齐 conversation event。
- 每个群最多一个 active/waiting run。
- waiting 不受 15 分钟 active TTL 影响。
- 无关群聊消息不进入 active run 的模型上下文。
- 所有灰度开关关闭后，系统可以安全回退到旧 Chat/card 路径。
- 可以按群和时间段直接创建 Control/Candidate evaluation cohort。
- 每个高价值 anchor 可以查看前向消息、两条链路上下文、决策、回复和后向消息。
- 用户 reply、reaction、纠正和卡片操作可以归因到实际 serving reply。
- 自动 Judge 对匿名 A/B 输出决策、上下文和回复质量评分。
- Candidate shadow 不产生真实消息或副作用。
- 报告明确区分离线 pairwise 质量、serving lane 真实结果和 shadow 反事实估计。
