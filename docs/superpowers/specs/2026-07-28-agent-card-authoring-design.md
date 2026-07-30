# Agent Card Authoring 设计

日期：2026-07-28  
状态：已确认  
适用范围：群聊 Conversation Runtime 中由 Agent 自主构造、发送和续接的飞书交互卡片

## 1. 背景

当前卡片能力主要由业务代码逐块拼装或绑定固定模板。它适合稳定面板，却不适合 Agent 在运行时根据任务内容决定：

- 需要展示哪些信息；
- 是否应该发卡而不是发文本；
- 应该使用按钮、选择器还是表单；
- 用户操作后是继续 Agent、执行已持久化 capability，还是只更新 UI；
- 卡片在提交、处理中、完成、取消、过期和失败时应该如何演进。

新能力的目标不是让模型直接生成任意飞书 JSON，而是向 Agent 提供一套有限、可发现、可校验、可审计的语义卡片语言。模型负责表达意图和信息结构；服务端负责注入可信运行时信封、执行安全策略、编译成飞书 schema 2.0 卡片并管理生命周期。

## 2. 目标

1. Agent 可以按任务需要自主选择文本回复或交互卡片。
2. Agent 可以使用标题、Markdown、事实、提示、分栏、按钮、选择器和表单等受控组件。
3. 所有会继续 Agent 或触发 capability 的 action 都绑定 Conversation Runtime interaction，而不是由模型生成可信执行参数。
4. 用户点击或提交产生标准 `ui_action` / continuation event，成为 Agent 可消费的输入。
5. callback 请求只做验证、claim、持久化和快速卡片反馈，不在请求内等待 LLM。
6. 原卡片按状态 patch，避免“点击一次又发一张新交互卡”的消息风暴。
7. 既有模板卡、业务手写卡和 legacy action handler 保持不变，可渐进迁移。
8. CardSpec、编译结果、interaction 时间线和前后文都可用于并轨评测。

## 3. 非目标

- 不允许 Agent 输出原始飞书卡片 JSON 并直接发送。
- 不把既有 schedule、config、permission、luckin 等业务卡一次性改写为 Agent Card。
- 不在模型上下文中暴露 callback token、capability 可信参数或权限判定结果。
- 不允许卡片表单采集密码、token、验证码、身份证号、银行卡号等敏感信息。
- 不承诺第一阶段覆盖飞书卡片 schema 2.0 的全部组件。
- 不在 callback HTTP 生命周期内执行 LLM turn 或不可控的慢 capability。

## 4. 总体架构

```text
Agent
  |
  | discover_card_components / compose_card
  v
Agent Card Application Service
  |-- Component Catalog
  |-- CardSpec Validator
  |-- Card Policy
  |-- Runtime Binder
  v
Bound CardSpec + persisted interaction
  |
  v
Lark Card Compiler
  |
  v
existing larkmsg schema 2.0 / CardKit helpers
  |
  v
Surface Manager
  |-- send
  |-- patch submitted / processing / resolved
  |-- fallback / reconciliation
```

### 4.1 `agentcard` 应用域

新增独立应用域，持有平台无关的：

- `CardSpec`、block、field、action 类型；
- 组件目录和使用说明；
- 结构与预算校验；
- 内容和 action policy；
- runtime binding 输入输出；
- 生命周期状态；
- 编译器、发送器和持久化接口。

应用域不能依赖飞书 SDK 的原始结构，也不能接受任意 `map[string]any` 作为组件定义。

### 4.2 Runtime Binder

Runtime Binder 在 `CardSpec` 校验通过后执行：

1. 为每个需要 callback 的 action 生成稳定 action ID。
2. 创建 interaction ID、revision、expiry 和高熵 token。
3. 将 token hash、可信 action descriptor、capability reference、actor policy 持久化到 wait step。
4. 只把运行时引用和明文一次性 token 注入编译阶段。
5. 为发送、callback、patch 和评测生成稳定关联 ID。

Agent 永远不能设置或覆盖：

- `run_id`
- `step_id`
- `interaction_id`
- `revision`
- `token`
- `continue_agent`
- capability 的可信参数
- 权限规则

### 4.3 Lark Compiler

编译器只接受已经绑定的 `BoundCardSpec`，输出仓库现有 `larkmsg.RawCard` / schema 2.0 JSON：

- 复用 `internal/infrastructure/lark_dal/larkmsg` 中的 card v2 helper；
- 复用 CardKit create/reply/patch；
- 将 DSL enum 映射为允许的飞书 tag、style 和 behavior；
- 禁止透传未知字段；
- 编译结果必须可以做 deterministic golden test。

### 4.4 Surface Manager

Surface Manager 管理卡片在消息表面的完整生命周期：

- 首次发送并记录 message ID；
- 用 revision CAS patch 原卡；
- 在 submitted / processing 阶段禁用或移除交互；
- resolved 后展示摘要、结果或下一步；
- patch 失败时记录可重试状态；
- 禁止因 patch 失败再次发送一张等价的可交互卡片；
- Agent 如有必要，可以在完成后另发一条简短总结，但默认不重复卡片内容。

## 5. Agent 工具与知识暴露

第一阶段向 Agent 暴露两个工具。

### 5.1 `discover_card_components`

返回当前版本可用的组件目录、字段、限制和简短示例。支持按 category 或 component name 筛选，避免每次把整个 schema 塞入上下文。

目录至少包含：

- layout：section、columns、divider；
- content：markdown、plain_text、facts、note；
- input：text_input、single_select、multi_select；
- action：button、submit、reset、cancel；
- lifecycle：interactive、submitted、processing、resolved、expired、failed。

目录返回的是语义知识，不返回内部 token、飞书 callback envelope 或 capability 参数。

### 5.2 `compose_card`

输入：

```json
{
  "purpose": "confirmation",
  "card": {
    "title": "确认修改 schedule",
    "blocks": []
  },
  "interaction": {
    "mode": "capability_confirm",
    "expires_in_seconds": 600
  }
}
```

输出是发送结果和公开引用，例如：

```json
{
  "card_ref": "card_surface_xxx",
  "message_id": "om_xxx",
  "interaction_id": "interaction_xxx",
  "revision": 3,
  "status": "sent"
}
```

工具内部顺序固定为：

```text
normalize
  -> validate
  -> policy
  -> bind runtime
  -> persist wait
  -> compile
  -> send
  -> persist surface reference
```

任一步失败都不能留下“数据库认为在等待、但卡片从未发出”的悬挂状态。发送与持久化不能组成跨系统原子事务，因此使用明确的 pending/sent/failed 状态和 reconciliation，而不是假装 exactly-once。

## 6. CardSpec DSL

### 6.1 顶层结构

```go
type CardSpec struct {
    Version string
    Title   string
    Theme   CardTheme
    Blocks  []Block
    Actions []Action
    Meta    PublicCardMeta
}
```

`Version` 从 `agent-card/v1` 开始。新增能力通过版本和组件目录演进，不复用字段改变旧语义。

### 6.2 内容组件

第一阶段支持：

- `markdown`：主体说明、列表和轻量格式；
- `plain_text`：短标签和不可解释为 Markdown 的文本；
- `facts`：键值摘要；
- `note`：提示、风险、来源或时间；
- `divider`：视觉分组；
- `columns`：最多三列的浅层布局；
- `section`：带标题的语义分组。

资源类内容不接受任意上传数据。图片、文件和其他资源只能引用服务端已登记的 asset reference。

### 6.3 输入组件

第一阶段支持：

- `text_input`
- `single_select`
- `multi_select`

每个输入必须定义稳定 `field_id`、label、required 和约束。选择项由 Agent 明确给出或引用服务端 catalog；不能在 callback 后根据显示文本猜真实值。

禁止：

- password/token/secret/credential/OTP 类字段；
- 以隐藏字符或“备注”名义收集敏感信息；
- 超出表单预算的大段自由文本；
- 未声明用途的人员或资源选择。

需要敏感输入时，卡片只提供跳转到个人临时会话或专用安全流程的说明和入口。

### 6.4 Action

```go
type Action struct {
    ID      string
    Label   string
    Style   ActionStyle
    Mode    ActionMode
    Intent  string
    FormRef string
}
```

支持三种执行模式：

1. `ui_action`
   - 普通按钮、选择器和表单提交。
   - callback 规范化为 Agent 输入。
   - Agent 根据用户动作和前后文决定下一步。

2. `capability_confirm`
   - 用于确认/取消已经由服务端持久化的 capability proposal。
   - callback 只提交 action 引用，服务端按可信 descriptor 执行。
   - 不让 Agent 在点击后重新构造高风险参数。

3. `server_action`
   - 纯 UI 行为，例如 reset、本地筛选、分页或关闭提示。
   - 由确定性 handler 执行，不启动 Agent continuation。

危险或不可逆 capability 必须使用 `capability_confirm`，并继续经过权限、actor、revision 和幂等检查。

## 7. Callback 与 Conversation Runtime

所有 callback payload 统一带受信 runtime envelope：

```text
action=agent.runtime.resume
run_id
step_id
interaction_id
revision
token
interaction_kind
continue_agent=true
action_id
```

callback 流程：

```text
Lark callback
  -> parse generic V2 action
  -> load wait step
  -> validate actor / token / revision / expiry
  -> claim interaction
  -> persist card_action / form outcome
  -> patch submitted or processing
  -> deterministic capability OR queue Agent continuation
  -> return quickly
```

表单值属于用户输入，必须：

- 按 `field_id` 白名单提取；
- 做类型、长度、选项和 required 校验；
- 作为 `ui_action` outcome 持久化；
- 不进入可信 capability 参数区；
- 不在错误、日志、trace 或 toast 中完整回显。

## 8. 生命周期与并发控制

生命周期：

```text
draft
  -> sent
  -> submitted
  -> processing
  -> resolved

draft/sent/submitted/processing
  -> cancelled | expired | failed
```

规则：

- 每次 patch 带期望 revision，使用 CAS；
- 第一个合法提交获得 interaction claim；
- 重复 callback 返回相同结果，不产生第二 continuation 或 capability execution；
- 非 owner、旧 revision、过期 token 返回安全 toast，不改变卡片事实；
- submitted 后立即禁用重复提交入口；
- processing 用于 deferred capability 或异步 continuation；
- resolved 展示结果摘要和必要的后续入口；
- cancel/expire/failed 都必须保留可理解的终态，不留下看似仍可点击的按钮；
- patch 失败写入 reconciliation queue，但不能重新发送等价交互卡；
- Agent 生成新的卡片必须创建新的 interaction/revision，而不是复用旧 token。

## 9. 安全策略与预算

### 9.1 结构预算

- 每张卡最多 20 个 block；
- 最多 10 个 input；
- 最多 5 个 action；
- columns 最多 3 列；
- 嵌套深度最多 3；
- 单个 form 的 callback 数据上限 8 KiB。

### 9.2 内容预算

- title 最多 80 个字符；
- 单个 Markdown 最多 4,000 个字符；
- 卡片文本总量最多 12,000 个字符；
- 单个 select 最多 50 个 option；
- field/action/component ID 必须 canonical 且长度受限。

### 9.3 URL 与资源

- 只允许 HTTPS；
- URL 必须通过用途和域名 allowlist；
- 不允许 `javascript:`、data URL、开放重定向或把 token 放入 query；
- 图片和附件只接受服务端 asset reference；
- 外部链接 action 不能伪装成 capability confirm。

### 9.4 Repair

Agent Card validation 失败时最多允许两次自动 repair：

1. 返回结构化错误路径、错误码和预算差值；
2. Agent 只修正 CardSpec；
3. 第二次仍失败则降级为文本或固定安全卡；
4. 不把原始飞书 SDK 错误或内部 policy 细节直接暴露给 Agent。

## 10. 兼容策略

1. 现有模板卡、`larkmsg.RawCard` 构卡函数和 action registry 保持原行为。
2. 新卡片使用独立 `agent.runtime.*` action 和 `agent-card/v1` spec。
3. generic V2 continuation dispatcher 只在完整 runtime envelope 存在且校验成功时接管；否则回落到既有 registry。
4. 既有业务卡可以逐张迁移为“固定 CardSpec builder”，但不是上线前置条件。
5. 编译器复用现有 schema 2.0 helper；底层发送、reply、patch 和 CardKit client 不重复实现。
6. carddebug/cardregression 增加 Agent Card 入口，但保留现有 scene/template/file 工作流。

## 11. 可观测性与审计

需要持久化或投影：

- 原始 `CardSpec`（脱敏）；
- validator/policy 版本和结果；
- 绑定后的公开结构，不含明文 token；
- 编译后的卡片 JSON（脱敏）；
- card surface message ID、revision 和 lifecycle；
- callback source ref、actor、action、表单字段名集合；
- submitted/processing/resolved 时间；
- patch attempt、错误分类和 reconciliation 状态；
- interaction 对应的前向、后向消息窗口。

日志和 trace 只记录稳定引用、计数、状态和错误码，不记录：

- token 或 token hash；
-完整表单值；
- capability 可信参数；
- 可能包含敏感数据的完整 CardSpec/compiled JSON。

## 12. 并轨评测

Agent Card 需要进入 Conversation Runtime 的双链路并轨评测。

### 12.1 自动采集

每个卡片样本关联：

- legacy/shadow/candidate 链路；
- run、topic、message 和 card surface；
- 发卡前消息窗口；
- CardSpec、编译结果和组件统计；
- 用户点击/提交时间线；
- 发卡后消息窗口；
- 用户显式点评、修改、撤回、重复点击和沉默；
- capability 或 continuation 结果；
- 卡片最终生命周期。

### 12.2 评测维度

- 是否应该发卡；
- 信息层级和可读性；
- 组件选择是否合适；
- 表单字段是否必要、明确且低负担；
- action 文案和风险表达；
- callback 后 continuation 是否理解用户动作；
- 卡片 patch 是否及时、是否产生重复消息；
- capability 结果是否正确；
- 用户前向/后向反馈；
- legacy 与 candidate 在同一时间段、同一话题上的差异。

评测必须同时保留前向和后向消息。用户往往不会直接点击“差评”，而会在后续消息里纠正、抱怨、重复提问或改口，这些都属于回复质量和卡片质量信号。

## 13. 测试策略

1. DSL validator table tests：合法/非法组件、预算、ID、URL、敏感字段。
2. Validator fuzz：未知 tag、深层嵌套、超长文本、非法 UTF-8、巨量 options。
3. Compiler golden：固定 CardSpec 对应稳定 schema 2.0 JSON。
4. Runtime Binder tests：可信 envelope 只能由服务端注入，token 不持久化明文。
5. Callback integration：actor/token/revision/expiry/dedupe/form validation。
6. Lifecycle CAS：双击、旧 revision、patch 重试、resolved 后重放。
7. Capability confirm：callback 参数不能覆盖 persisted descriptor。
8. Shadow/evaluation：CardSpec、compiled JSON、点击与前后文能完整关联。
9. carddebug dry-run：打印脱敏 CardSpec/compiled payload。
10. 人工验收：使用现有 lark-card-debug 向测试 chat_id/open_id 发卡，覆盖桌面端和移动端。

## 14. 分阶段落地

1. **Phase A：DSL、catalog、validator、compiler**
   - 只构建和 dry-run，不发生产卡。
2. **Phase B：Runtime Binder 与 generic callback**
   - 接入 interaction wait/resume、token/revision/actor 校验。
3. **Phase C：Surface lifecycle**
   - send/patch/reconciliation 和状态审计。
4. **Phase D：Agent tools**
   - 暴露 discovery/compose，启用两次 repair 和文本降级。
5. **Phase E：shadow 与并轨评测**
   - 先记录 Agent 会构造什么卡，不真实发送；评测稳定后灰度。
6. **Phase F：业务迁移**
   - 仅按收益迁移现有固定业务卡，legacy 长期可共存。

## 15. 已确认的关键决策

- 使用语义 `CardSpec` DSL，不允许 Agent 直接写飞书 JSON。
- 交互等级为完整表单/选择器/确认/取消能力。
- action 采用混合模式：`ui_action`、`capability_confirm`、`server_action`。
- callback 是 Agent 输入；callback 请求内不执行 LLM。
- 卡片优先 patch 原消息，Agent 可选发简短总结，默认不重复。
- 群聊卡片禁止收集敏感信息，转入个人临时或专用安全流程。
- 现有卡片能力保持兼容，新能力独立演进。
- 充分复用 schema 2.0 helper、CardKit、card action registry、carddebug、cardregression 和 Conversation Runtime。
