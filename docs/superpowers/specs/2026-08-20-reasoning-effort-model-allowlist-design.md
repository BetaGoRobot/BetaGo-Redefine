# Reasoning Effort 模型白名单设计

## 背景

当前 Responses API 的多个调用点会设置 `reasoning.effort`，并且
`ark_dal.ResponsesImpl` 默认注入 `medium`。部分模型或 Endpoint 不支持该字段，
导致请求失败。逐调用点删除容易遗漏，也无法让明确支持该字段的模型继续使用它。

## 目标

- 默认不向任何模型发送 `reasoning.effort`。
- 仅当请求的精确模型 ID 位于配置白名单时发送调用方指定的 effort。
- 保留内部 `ReasoningEffort` 元数据及其策略用途；只治理 Ark 请求载荷。
- 统一覆盖普通对话、工具续轮、缓存调用、意图识别、Agent Runtime、Candidate 和
  Judge。

## 配置

在 `[ark_config]` 中增加：

```toml
reasoning_effort_models = [
    "明确支持 reasoning.effort 的模型或 Endpoint ID",
]
```

字段未配置或列表为空时，所有请求均省略 `reasoning.effort`。模型 ID 去除首尾空格
后精确匹配；空值忽略，重复项等价于单项。通过 WebUI 动态选择的模型也遵循同一
规则。

## 请求治理

治理逻辑位于 `ark_dal` 的最终 Responses 请求出口：

1. 根据请求中的 `Model` 和当前 `ArkConfig` 计算有效 `Reasoning`。
2. 模型不在白名单时，将请求副本的 `Reasoning` 置空。
3. 模型在白名单时，原样保留调用方给出的 `minimal`、`low`、`medium` 或 `high`。
4. 同步和流式出口执行相同策略，形成最后一道一致性边界。

请求治理不得修改调用方传入的请求对象，以免影响重用、日志或测试。缓存请求在
生成缓存键和构造 cache head/continuation 前使用同一有效值，保证缓存身份与实际
发送载荷一致。

`thinking` 参数不属于本次修改范围。

## 错误与观测

不在收到“不支持参数”错误后自动重试，以免产生重复计费、重复工具调用或其他
副作用。请求日志和 trace 记录过滤后的实际 effort；被屏蔽时为空。

## 测试

采用测试先行，覆盖：

- 未配置和空白名单时移除 `Reasoning`；
- 精确命中白名单时保留 `Reasoning`；
- 非精确匹配和空模型 ID 不启用；
- 输入请求对象不被修改；
- 同步与流式出口使用同一策略；
- 缓存键和 cache head/continuation 使用过滤后的有效值；
- 工具续轮和手动 turn 最终也经过统一出口治理。

现有内部 reasoning effort 的解析、规范化和策略测试保持不变。
