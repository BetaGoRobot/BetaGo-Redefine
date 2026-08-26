# 工具失败安全反馈设计

## 背景

`create_schedule` 收到只有 `name` 和 `type=once` 的调用时，会因缺少动作以及执行时间而失败。Responses 工具循环会继续向模型提交 function-call output，但 `gresult.Err[string]` 的 value 是空字符串，当前实现只编码 value，因此模型实际收到 `""`，看不到失败原因，无法修正参数或向用户追问信息。

## 目标

- 信息不足时不猜测提醒时间、消息内容或工具目标。
- 让模型知道工具调用失败，并决定修正参数或向用户提问。
- 只向模型暴露明确标记为安全的输入反馈；内部错误不得原样进入模型上下文。
- 不改变成功工具调用的输出格式、现有日志和 usage 归因。

## 方案

采用三层防护：

1. `create_schedule` 的工具描述明确要求信息不足时先询问用户，禁止只传 `name/type`。
2. `createScheduleHandler.ParseTool` 在执行前校验动作和调度字段组合：
   - `message` 与 `tool_name` 必须且只能提供一个；
   - `once` 必须提供 `run_at`；
   - `cron` 必须提供 `cron_expr`。
3. 工具错误续传统一生成结构化 JSON：
   - 被标记为安全反馈的输入错误包含可纠正原因和“修正或询问用户”的指令；
   - 其他错误只包含通用失败文案，不暴露底层错误内容。

安全反馈错误由 `pkg/xerror` 提供小型包装类型，保留原始 cause 供日志和 `errors.Is/As` 使用，同时单独暴露给模型的反馈字符串。Ark Responses 层只依赖这个公共错误边界。

## 数据流

1. 模型调用 `create_schedule`。
2. typed handler 解析并校验参数组合。
3. 参数不完整时返回带安全反馈的错误，工具不执行、不写数据库。
4. Responses 层记录原始错误日志，但向模型续传结构化安全反馈。
5. 模型根据反馈补齐已知参数；若用户没有提供必要信息，则先向用户询问。

## 测试

- Ark Responses：失败 handler 的原始 value 为空时，续传 output 仍包含安全反馈；未标记的内部错误不得泄露。
- Schedule：只有 `name/type=once` 时在 ParseTool 阶段失败，反馈同时指出缺少动作与 `run_at`；合法 once/cron 参数继续通过。
- 回归：相关包测试、竞态检查、vet 和 `cmd/larkrobot` 构建。
