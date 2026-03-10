# Lark Card V2 Builder

## 目标

为后续大量 schema v2 卡片提供一层通用 primitive，避免各业务包重复手写：

- raw card 外壳
- `markdown`
- `plain_text`
- `div`
- `hr`
- `column`
- `column_set`
- `button`
- callback behaviors

当前主要使用方：
- `internal/application/config/card_view.go`
- `internal/application/lark/ratelimit/stats_card.go`

## 分层建议

推荐分层：

1. 业务快照层
- 聚合真实数据
- 处理多 key / 多来源读取

2. 展示策略层
- 颜色
- 风险等级
- 文案映射

3. ViewModel 层
- 组装适合卡片渲染的数据结构

4. Primitive 渲染层
- 仅负责拼 schema v2 组件
- 不负责业务判断

`larkmsg/card_v2.go` 只属于第 4 层。

## 当前 Primitive

入口：
- `NewCardV2`

文本：
- `PlainText`
- `TextDiv`
- `Markdown`
- `HintMarkdown`
- `Divider`

布局：
- `Column`
- `ColumnSet`
- `ButtonRow`

交互：
- `Button`
- `CallbackBehaviors`

## 使用约束

1. 不要把业务阈值写进 primitive 层
- 例如拒绝率、活跃度、频控风险等级
- 这些应放在业务 policy 层

2. primitive 层保持 map 级别薄封装
- 目标是减少重复结构，而不是造一套复杂 DSL

3. 卡片组件预算仍由业务层负责
- Feishu Card JSON 2.0 单卡最多 200 组件
- primitive 层不自动做裁剪

4. 如果要做 callback
- 统一走 `ButtonOptions.Payload`
- 业务侧自己决定 action 协议

## 新增卡片 SOP

1. 先定义 snapshot / data / policy
2. 再用 `larkmsg.NewCardV2` 拼壳
3. 优先复用：
- `Column`
- `ColumnSet`
- `TextDiv`
- `Markdown`
- `Button`
- `ButtonRow`
4. 如果发现多个卡片还在重复新的结构：
- 先判断是否真的是通用 primitive
- 是的话，再下沉到 `larkmsg/card_v2.go`

## 当前未覆盖

当前没有下沉的内容：

- `form`
- `input`
- 更复杂的富交互容器
- 图片 / 媒体 / tag 等更专门的 schema v2 组件

这些组件只有在至少两个业务场景重复出现时，再考虑继续通用化。
