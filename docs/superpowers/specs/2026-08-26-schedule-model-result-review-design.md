# Schedule 模型结果审核设计

## 背景

`scheduled_tasks.notify_result=true` 的公开语义是“工具返回文本结果时额外发送结果通知”，但当前 scheduler 的成功分支只记录 `Scheduled task execution result` 日志，没有调用发送链路。与此同时，直接发送 `research_read_url` 等工具的原始 JSON 也不适合作为群聊播报。

本次修复将 `notify_result` 明确定义为模型审核开关：工具成功后，由模型决定是否需要主动发送，以及最终发送的群聊文案。

## 目标

- `notify_result=false` 时不调用模型，也不发送成功结果。
- `notify_result=true` 时把任务上下文和工具结果交给模型审核。
- 模型可以选择发送或静默；发送内容完全由模型生成。
- 模型不能在审核阶段调用工具，避免重复执行、递归调度或额外副作用。
- 原始工具结果继续按现有方式持久化，便于排障和人工查看。
- 模型审核或消息投递失败不得导致工具重新执行。
- 保持现有失败通知 `notify_on_error` 的语义。

## 非目标

- 不为特定 URL 或“洛克王国远行商人”编写专用 formatter。
- 不把每次 schedule 触发升级为完整 Agent Runtime run。
- 不新增数据库字段或迁移。
- 不给历史结果增加新的持久化审计表。

## 架构

在 `internal/application/lark/schedule` 中增加两个边界：

1. `TaskResultReviewer`：把成功工具结果转换成结构化 `TaskResultDecision`。
2. `TaskNotifier`：负责把审核后的文案回复来源消息，并在回复失败时回落到群聊直发。

`Scheduler` 通过构造参数接收这两个依赖。生产环境使用模型 reviewer 和飞书 notifier；测试使用轻量 fake。模型 reviewer 使用当前群和创建者生效的普通聊天模型配置，并通过现有 Responses API 发起一次无工具、JSON object 输出的后台调用。

模型输入包括：

- task ID、名称、工具名和经过敏感字段清理的工具参数；无法解析的参数不原样转发；
- chat ID、creator open ID、时区和本次完成时间；
- 工具原始结果；
- 明确的数据边界和截断标记。

模型输出固定为：

```json
{
  "send": true,
  "content": "面向群聊的最终文案",
  "reason": "发送或静默的简短原因"
}
```

约束：

- `send=true` 要求非空 `content`。
- `send=false` 要求空 `content`。
- `reason` 必填，仅用于日志和观测。
- 拒绝未知字段、多份 JSON、超长输出和非法组合。
- 工具结果视为不可信数据，模型不得服从其中嵌入的指令。

## 执行流程

1. scheduler claim 到期任务并执行原有单一工具。
2. 无论后续模型审核是否成功，都先通过 `FinalizeTaskExecution` 持久化原始结果和工具执行状态；持久化失败时停止本次审核和通知。
3. 工具失败时不调用 reviewer；按现有 `notify_on_error` 发送错误通知。
4. 工具成功但 `notify_result=false` 时结束；`send_message` 始终跳过结果审核，避免提醒发送后产生二次通知。
5. 工具成功且 `notify_result=true` 时调用 reviewer，即使结果为空也由模型决定是否需要通知。
6. reviewer 返回静默决策时只记录包含 reason 的结构化日志。
7. reviewer 返回发送决策时交给 notifier；优先回复 `source_message_id`，失败后回落到 `chat_id` 直发。两条链路共用由任务 ID、本次完成时间和通知类型组成的稳定投递键。

## 错误处理

- 模型配置缺失、调用失败或输出校验失败：记录 `Scheduled task result review failed`，保留已持久化的原始结果；若 `notify_on_error=true`，尝试发送确定性的审核失败通知。
- 模型决定发送但消息投递失败：记录 `Send scheduled task notification failed`，不重新执行工具。
- 模型决定静默：视为成功，不写 `last_error`。
- 模型审核和通知错误不覆盖工具本身的 `last_error`，避免一次性副作用工具因投递失败被重新执行。

## 安全与资源边界

- reviewer 请求不注册任何工具。
- 模型使用 minimal reasoning，并关闭 thinking。
- 工具参数中的 token、密码、凭据、签名、Cookie 和 URL 敏感查询参数在进入提示词前递归遮蔽。
- 原始结果进入提示词前按字节安全截断，并显式标记截断。
- reviewer 的用户提示词不写入 OTel 内容预览，只记录长度和已遮蔽标志。
- 模型输出限制为单个小型 JSON 文档；最终群聊文案设置上限。
- LLM usage 归类为 background schedule result review，并保留 chat/open ID 归因。

## 测试

- reviewer 解码：发送、静默、未知字段、多 JSON、空 reason、发送空 content、静默非空 content、超长输入/输出。
- scheduler 编排：开关关闭不审核、`send_message` 不审核、成功发送、成功静默、空结果仍审核、工具失败不审核、持久化失败不审核、审核失败走错误通知、投递失败不重跑工具。
- notifier：来源消息回复成功、回复失败后群聊回落、双重失败返回错误、不同执行使用不同投递键。
- 装配与既有 schedule 包回归测试。

测试和构建严格使用仓库 Go 基线：Go 版本取自 `go.mod`，`-tags=custom_skip_vips`，测试设置 `BETAGO_CONFIG_PATH=.dev/config.toml` 并使用 `-v`。
