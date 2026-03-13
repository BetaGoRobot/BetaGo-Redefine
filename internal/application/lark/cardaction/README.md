# Card Action

相关文档：
- [Checklist](./CHECKLIST.md)
- [Checklist: 新增或修改回调前](./CHECKLIST.md#新增或修改回调前)

## 设计与架构

这一层的目标是把飞书卡片回调拆成三段：

1. 协议解析
`pkg/cardaction` 负责把飞书回调里的 `action.value`、`action.form_value` 解析成统一协议，优先走标准字段 `action`，同时兼容少量历史 payload。

2. 注册与调度
`internal/application/lark/cardaction/registry.go` 负责注册 action 和调度执行。这里显式区分两类 handler：
- `RegisterAsync`
- `RegisterSync`

3. 业务实现
`internal/application/lark/cardaction/builtin.go` 只负责把 action 映射到业务逻辑。

## 执行逻辑

一次卡片点击的执行链路如下：

1. 飞书把 `card.action.trigger` 回调发到服务端。
2. `internal/interfaces/lark/handler.go` 进入 `CardActionHandler`。
3. `Dispatch` 先通过 `pkg/cardaction.Parse(...)` 解析统一 action。
4. 根据注册模式执行：
- `sync`: 直接在 callback 请求内执行，允许返回 `toast` / `card`
- `async`: 先做轻量参数校验，返回一个 `AsyncTask`，由 `Dispatch` 统一 `go task(ctx)` 异步执行，并立即 `return nil, nil`

这样做的目的是把“是否异步”从业务函数内部拿出来，放到注册层显式声明。

## 当前约定

- 高开销、可能超时、需要二次更新卡片的动作，一律注册成 `async`
- 低开销、可在毫秒级完成、适合直接回 toast 或刷新卡片的动作，注册成 `sync`

当前默认分类：

- `async`
  - `music.play`
  - `music.album`
  - `music.lyrics`
  - `music.refresh`
  - `music.list_page`
  - `card.withdraw`
  - `command.refresh`
  - `command.submit_form`
  - `command.submit_time_range`
- `sync`
  - `command.open_form`
  - `feature.*`
  - `config.*`
  - `permission.*`
  - `ratelimit.view`
  - `schedule.view/pause/resume/delete`
  - `wordcount.chunks.view`
  - `wordcount.chunk.detail`

## 发卡与回调的联动

新增一个卡片回调时，发卡侧和回调侧必须一起设计，不能只改其中一边。

统一原则：

1. 发卡时写入标准 action payload
- 使用 `pkg/cardaction.New(actionName)`
- 通过 `WithValue(...)` / `WithID(...)` / `WithCommand(...)` / `WithFormField(...)` 填充回调入参
- 最终通过 `Payload()` 输出给卡片组件

2. 回调时只从统一协议层取值
- 不要在业务 handler 里直接依赖飞书原始 JSON 结构
- 一律先经过 `pkg/cardaction.Parse(...)`

3. 表单卡片要显式约定字段名
- 表单组件的 `name`
- 业务 payload 里的 `form_field`
- 解析函数里的取值逻辑

这三者必须一致，否则表单提交时会出现“按钮点了但值取不到”的问题。

常见写法：

- 普通按钮
  - 发卡：`cardaction.New(ActionXxx).WithValue(...).Payload()`
  - 回调：`parsed.RequiredString(...)`

- 输入框直接回调
  - 发卡：组件自身 `behaviors.callback.value = payload`
  - 回调：优先读 `parsed.InputValue`

- 表单提交
  - 发卡：payload 里带 `form_field`
  - 回调：优先按 `form_field` 去 `parsed.FormValue` 取值

配置卡片就是这个模式：

- 发卡侧在 `internal/application/config/card_view.go`
- payload builder 在 `internal/application/config/card_action.go`
- 回调解析也在 `internal/application/config/card_action.go`

命令帮助卡和命令表单卡同样走这一套：

- 帮助卡主按钮写 `command.open_form`
- 帮助卡里的子命令快捷入口也写 `command.open_form`
- 参数表单提交写 `command.submit_form`
- 回调只负责恢复 raw command，真正执行仍回到标准命令链路

## 新增或修改回调的 SOP

### SOP 1：先定义 action 协议

1. 在 `pkg/cardaction/action.go` 增加 action 常量
2. 如果需要新的通用字段，也在这里加 field 常量
3. 不要优先加 legacy alias，除非必须兼容旧卡片

### SOP 2：确定发送侧入参

1. 确定这次卡片点击需要哪些业务参数
- 例如 `id`
- 例如 `key/value/scope`
- 例如 `feature/chat_id/user_id`

2. 决定参数来自哪里
- 按钮固定值：写进 payload
- 输入框即时值：走 `input_value`
- 表单提交值：走 `form_value`

3. 给发送侧补 builder
- 优先在对应业务目录增加 `BuildXxxActionValue(...)`
- 不要在卡片 JSON 里手写字符串 key

### SOP 3：决定注册模式

1. 用 `RegisterAsync`
- 远程接口调用重
- 需要异步 patch/重发卡片
- 回调内做不完，或者有明显超时风险

2. 用 `RegisterSync`
- 本地校验和轻量写操作
- 可以直接返回 toast
- 可以直接返回一张刷新后的卡片

### SOP 4：实现 handler

1. `async` handler
- 签名：`func(ctx context.Context, actionCtx *Context) (AsyncTask, error)`
- 只做轻量解析和校验
- 返回 `AsyncTask`
- 不要在 handler 里自己写 `go`

2. `sync` handler
- 签名：`func(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error)`
- 直接返回 `toast` / `card`
- 不要塞重 IO

### SOP 5：接回发卡侧

1. 在卡片 builder 里把 payload 接到按钮/表单组件
2. 确认按钮、输入框、表单 `name` 与 payload 约定一致
3. 如果是 Card JSON v2：
- 发送侧可以用 `card_json`
- callback response 仍然只能回 `raw` / `template`

### SOP 6：补测试

至少补下面几类：

1. payload builder 测试
- 确认 action 名正确
- 确认字段齐全

2. 解析测试
- 普通按钮值
- `input_value`
- `form_value`

3. 注册模式测试
- `async` 不内联返回响应，但任务会执行
- `sync` 会直接返回响应

4. callback 卡片类型测试
- 即使 payload 是 Card JSON v2，callback response 的 `card.type` 也必须还是 `raw`

5. template 卡异步 patch 覆盖测试
- 如果 handler 会在 callback 返回后继续异步 patch 当前卡片，确认不会再被旧的同步 `resp.card.content` 覆盖回去

## 修改或新增回调时的注意点

1. 先决定注册模式，不要先写代码再想是否异步。
- 如果动作里会查远端接口、跑较重逻辑、二次 patch 消息、或者存在超时风险，用 `RegisterAsync`
- 如果动作只是本地校验、改一条配置、返回 toast、重绘一张小卡片，用 `RegisterSync`

2. `async` handler 不要自己再写 `go ...`
- 统一返回 `AsyncTask`
- 由 `Dispatch` 负责启动 goroutine
- 这样才能保证异步策略集中、可测试、可审计

3. `sync` handler 只做低开销逻辑
- 不要把慢 IO、复杂聚合、长链路外部调用塞进 callback 直返路径

4. 卡片回调响应里的 `card.type` 只能用 `raw` 或 `template`
- 即使 payload 本身是 Card JSON v2，也是在 callback 响应里作为 `raw` body 返回
- `card_json` 只属于发消息 / 建卡实体 / patch 消息这条链路

5. 发送侧和回调侧要分开理解
- 发送侧：可以 `CreateCard(type=card_json)`，再发送 `card_id`
- 回调侧：只能返回 `raw` / `template`

6. 新 action 一律优先走标准协议字段
- 使用 `pkg/cardaction.New(...)`
- 不要再新增新的历史别名或散落的 `type=xxx`

7. 表单类回调优先从统一解析层取值
- 普通点击值看 `action.value`
- 表单提交值看 `action.form_value`
- 不要在业务 handler 里直接手搓飞书原始字段

8. 修改完一定补测试
- 至少覆盖注册模式
- 至少覆盖 payload 解析
- 如果返回卡片，确认 callback response 里的 type 仍然是 `raw`

9. template 卡如果走异步 patch，优先避免“同步返回旧内容 + 异步再 patch”这个组合
- 这类场景常见于分页、流式刷新、命令执行后重绘
- 如果同步响应里携带旧 `content`，飞书可能会在异步 patch 后又把卡片立即改回旧内容
- 处理方式通常有两种：
  - 纯同步：直接返回最终卡片
  - 纯异步：callback 只做轻响应，最终内容由 patch 写入
