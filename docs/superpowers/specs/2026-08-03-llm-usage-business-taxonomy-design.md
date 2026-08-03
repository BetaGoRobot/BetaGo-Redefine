# LLM 用量业务分类与工具归因设计

## 1. 背景

当前 LLM 用量记录已经包含 `kind`、`source_type` 和 `source`：

- `kind` 描述 Ark API 形态，例如 `responses`、`responses_stream`、`embedding`；
- `source_type` 描述调用主体，例如 `user`、`background`、`system`、`debug`；
- `source` 描述较细的代码来源，例如 `chat`、`intent`、`chunking`。

这些字段适合排障，但不能直接回答以下业务问题：

- Token 花在对话、命令、检索、后台加工还是评测上；
- 哪些业务场景最常进入工具循环；
- 工具实际调用了多少次、成功率和耗时如何；
- 有多少 Token 来自包含工具调用的模型轮次。

WebUI 目前只聚合展示 `kind` 和 `source_type`，没有展示原始 `source`，也没有稳定的业务分类或通用工具执行指标。

## 2. 目标

本设计为每条 LLM 用量记录增加稳定的业务归因，同时建立与模型轮次关联的工具调用明细，使 WebUI 可以：

1. 按业务场景和业务动作统计请求数与 Token；
2. 统计工具调用次数、成功率、耗时和工具相关 Token；
3. 保留现有全部技术维度用于高级下钻；
4. 对历史数据提供可解释的兼容映射；
5. 在 Dashboard、会话列表和会话详情中使用一致口径。

## 3. 非目标

- 不建设通用分布式事件账本；
- 不保存工具参数、工具输出或消息原文；
- 不把一个模型轮次的 Token 人为平均分摊给多个工具；
- 不修改现有业务回复、命令或工具的用户可见语义；
- 不删除或重定义 `kind`、`source_type`、`source`、`status` 等技术字段。

## 4. 分类模型

业务归因由两个层级构成，工具执行作为正交维度记录。

### 4.1 一级业务场景 `business_scene`

| 值 | WebUI 名称 | 范围 |
| --- | --- | --- |
| `conversation` | 对话生成 | 普通群聊、@回复、单聊及直接对话生成 |
| `command` | 命令处理 | `/bb` 等显式命令触发的 LLM 调用 |
| `routing` | 意图与路由 | 意图识别、工具规划、激活和相关性判断 |
| `retrieval` | 检索与召回 | 历史搜索、话题召回、向量检索和检索式回答 |
| `agent_runtime` | Agent 运行 | callback continuation、等待后续接等运行时调用 |
| `evaluation` | 评测与 Shadow | Candidate、Judge 及其他离线/并轨评测 |
| `background` | 后台加工 | 消息向量化、Chunk 合并、索引重建等后台任务 |
| `debug` | 运维调试 | 显式调试和运维调用 |
| `unknown` | 待归类 | 无法安全判断的历史记录或新增未声明来源 |

`business_scene` 是唯一成本归属维度：每条 LLM 用量记录只属于一个一级场景。

### 4.2 二级业务动作 `business_operation`

`business_operation` 描述具体动作，并允许后续新增稳定枚举。首批动作包括：

| 场景 | 动作示例 |
| --- | --- |
| `conversation` | `chat_reply`、`mention_reply`、`p2p_reply` |
| `command` | `command_chat`、`command_handler` |
| `routing` | `intent_recognition`、`tool_planning`、`activation`、`relevance` |
| `retrieval` | `history_search`、`topic_recall`、`retriever_embedding`、`retriever_answer` |
| `agent_runtime` | `callback_continuation` |
| `evaluation` | `candidate_generation`、`judge` |
| `background` | `message_embedding`、`outbound_embedding`、`chunk_merge`、`chunk_embedding`、`reindex_embedding` |
| `debug` | `debug_image`、`debug_conversation` |
| `unknown` | `unknown` |

动作值是低基数、可枚举字段，不允许直接写入用户输入、命令参数或动态工具名。命令名和工具名分别进入受控的命令/工具维度。

### 4.3 归因来源 `attribution_mode`

| 值 | 含义 |
| --- | --- |
| `explicit` | 新代码在调用入口明确提供业务归因 |
| `legacy_mapping` | 历史数据或兼容调用根据 `source` 推断 |
| `unknown` | 无法安全归因 |

WebUI 在详情和 tooltip 中展示归因来源。历史映射不能伪装成精确的显式归因。

### 4.4 工具执行维度

工具调用不作为一级业务场景。一次命令调用工具仍归属 `command`，普通对话调用工具仍归属 `conversation`。

每个逻辑模型轮次保存：

- `tool_call_count`；
- `tool_success_count`；
- `tool_error_count`。

每次真实工具 handler 执行保存独立明细：

- 工具名称；
- 成功或失败状态；
- 调用耗时；
- 精简、受控的错误类型；
- 调用时间。

不保存参数、返回值、消息内容或原始错误文本。

## 5. Token 归因口径

### 5.1 逻辑模型轮次

一个用户或后台动作可能经历多段模型响应：

```text
model planning -> tool calls -> model synthesis -> final reply
```

这些阶段共同组成一个逻辑模型轮次。用量记录在轮次结束时写入一次，Token 为轮次内所有模型阶段 usage 的总和。

### 5.2 工具相关 Token

当 `tool_call_count > 0` 时，该轮次的 `total_tokens` 定义为工具相关 Token。统计时使用：

```text
SUM(total_tokens) FILTER (WHERE tool_call_count > 0)
```

同时提供 `turns_with_tools`，用于区分“调用工具的模型轮次数”和“真实工具调用次数”。

多个工具共享同一轮模型上下文时，不把轮次 Token 复制到每个工具，也不做缺乏事实依据的平均分配。因此：

- 业务场景和业务动作可展示可加总的工具相关 Token；
- 单工具排名展示调用次数、成功率、错误次数和耗时；
- 单工具不展示可加总的独占 Token。

### 5.3 Responses 工具循环累加

当前流式工具循环需要增加轮次累加器：

1. 收集每个 `response.completed` 的 usage；
2. 收集每次真实 handler 执行结果；
3. 工具输出续接后继续累加下一段模型 usage；
4. 最终回复完成或轮次异常终止时，写入一条轮次用量记录；
5. 用量主记录和工具明细在同一数据库事务写入。

实现必须保留现有流式输出、工具 handler 和最终回复顺序。对于并行工具调用，应先收集同一模型阶段产生的工具调用，再续接模型，避免遗漏该阶段的 completed usage。

## 6. 数据模型

### 6.1 `llm_token_usage_records` 扩展

新增字段：

```text
business_scene       TEXT NOT NULL DEFAULT 'unknown'
business_operation   TEXT NOT NULL DEFAULT 'unknown'
attribution_mode     TEXT NOT NULL DEFAULT 'unknown'
tool_call_count      BIGINT NOT NULL DEFAULT 0
tool_success_count   BIGINT NOT NULL DEFAULT 0
tool_error_count     BIGINT NOT NULL DEFAULT 0
```

增加面向时间窗口聚合的组合索引：

```text
(bot_id, chat_id, created_at, business_scene)
(bot_id, chat_id, created_at, business_operation)
```

保留现有 `kind`、`source_type`、`source`、`status` 和 Token 字段。

### 6.2 `llm_tool_call_records`

新表字段：

```text
id                    BIGSERIAL PRIMARY KEY
usage_record_id       BIGINT NOT NULL
bot_id                TEXT NOT NULL
chat_id               TEXT NOT NULL DEFAULT ''
business_scene        TEXT NOT NULL
business_operation    TEXT NOT NULL
tool_name             TEXT NOT NULL
status                TEXT NOT NULL
duration_ms            BIGINT NOT NULL DEFAULT 0
error_kind            TEXT NOT NULL DEFAULT ''
trace_id              TEXT NOT NULL DEFAULT ''
called_at             TIMESTAMPTZ NOT NULL
created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
```

`usage_record_id` 关联主用量记录。查询必须同时带当前 Bot 的 `bot_id`，延续现有多 Bot 隔离边界。

索引：

```text
(bot_id, chat_id, called_at)
(bot_id, chat_id, tool_name, called_at)
(usage_record_id)
```

### 6.3 写入契约

`llmusage.Scope` 增加显式 `BusinessScene` 和 `BusinessOperation`。`llmusage.Record` 增加 `AttributionMode` 和工具执行摘要/明细。

规范化顺序：

1. 使用调用方提供的显式业务归因；
2. 显式字段为空时，使用受控 `source -> scene/operation` 映射；
3. 无映射时写入 `unknown`；
4. 永远保留原始 `source_type` 和 `source`。

Recorder 先继续记录 VictoriaMetrics 在线指标，再用数据库事务写入主记录和工具明细。数据库失败返回错误但不得中断用户回复、命令或工具执行。

## 7. 历史兼容映射

首批映射：

| `source` | 场景 | 动作 |
| --- | --- | --- |
| `chat` | `conversation` | `chat_reply` |
| `intent` | `routing` | `intent_recognition` |
| `history_search` | `retrieval` | `history_search` |
| `topic_recall` | `retrieval` | `topic_recall` |
| `retriever_embedding`、`retriever_recall` | `retrieval` | 对应检索动作 |
| `retriever_answer` | `retrieval` | `retriever_answer` |
| `message_recording` | `background` | `message_embedding` |
| `outbound_message_recording` | `background` | `outbound_embedding` |
| `chunking` | `background` | `chunk_merge` |
| `chunking_embedding` | `background` | `chunk_embedding` |
| `reindex_embeddings` | `background` | `reindex_embedding` |
| `conversation_evaluation_candidate` | `evaluation` | `candidate_generation` |
| `agent_callback_continuation` | `agent_runtime` | `callback_continuation` |
| `debug_image` | `debug` | `debug_image` |

历史 `source=chat` 无法区分显式 `/bb` 命令和普通对话，因此统一映射为 `conversation/chat_reply` 并标记 `legacy_mapping`。新记录必须在命令入口显式写入 `command`。

迁移脚本批量回填已知来源；未知记录保持 `unknown`。运行时保留同一映射作为兼容兜底，确保迁移前后和新旧部署混合期间口径一致。

## 8. 调用入口归因

归因应尽量在业务入口确定，而不是由底层 Ark DAL 猜测。

- 普通消息、@回复、单聊：`conversation`；
- `CommandOperator` 执行链：`command`，动作携带受控命令类别；
- 意图识别和二阶段工具规划：`routing`；
- 历史搜索、话题召回和 retriever：`retrieval`；
- Conversation Runtime callback：`agent_runtime`；
- Candidate/Judge worker：`evaluation`；
- 消息记录、Chunk、reindex worker：`background`；
- debug handler：`debug`。

上下文可以携带入口归因供下游 scope builder 读取，但每个独立后台任务必须创建自己的显式 scope，不能依赖用户请求上下文残留。

工具内部再次调用 LLM 时，应继承原始业务场景，并使用能够表明工具内部动作的 `business_operation`。无法安全继承时使用该工具自身的稳定业务动作，而不是写动态参数。

## 9. WebUI API

现有 Token Stats 响应只新增字段，不删除或改名：

```text
by_business_scene
by_business_operation
by_source
tool_summary
by_tool
```

`tool_summary` 包含：

```text
tool_calls
tool_successes
tool_errors
turns_with_tools
tool_related_tokens
```

`by_tool` 包含：

```text
tool_name
calls
successes
errors
success_rate
average_duration_ms
```

通用分组结果增加 `tool_calls` 和 `tool_related_tokens`，使 Dashboard 能按业务场景和动作合并多个 Bot/会话。工具名称使用单独的工具分组结构，不伪装成 Token 分组。

所有动态分组列必须来自后端固定 allowlist，不能把请求参数直接拼入 SQL。

## 10. WebUI 信息架构

### 10.1 默认业务视图

Dashboard 和会话详情优先展示：

- 总 Token；
- 请求数；
- 工具调用次数；
- 工具相关 Token；
- 工具错误率；
- 业务场景分布；
- 业务动作 Top 排名；
- 工具调用排名与成功率。

业务场景、业务动作和工具名进入下钻与过滤体系。Dashboard、会话列表、会话详情共用一份枚举标签、颜色和 fallback 文案。

### 10.2 技术维度

现有指标集中到“技术维度”区域并全部保留：

- 模型；
- Ark API 类型 `kind`；
- 触发来源 `source_type`；
- 原始来源 `source`；
- 状态。

技术维度默认次于业务视图，但不隐藏排障能力。

### 10.3 历史与未知数据

- `legacy_mapping` 在 tooltip 或详情中标记“历史映射”；
- `unknown` 显示“待归类”；
- 空数据展示明确的空状态，不渲染误导性的 0% 图表；
- 业务总量必须与相同过滤条件下的现有总量一致。

### 10.4 响应式与可访问性

- 移动端业务卡片单列；
- 工具明细表在窄屏转为紧凑列表；
- 图例和筛选器允许换行；
- 交互目标至少 44×44px；
- 颜色不是区分成功、失败和归因来源的唯一方式。

## 11. 异常与安全

- 指标落库失败不影响业务响应；
- 用量主记录和工具明细在同一事务写入；
- 工具错误只保存受控错误类别，不保存原始错误文本；
- 工具参数和输出不进入统计数据库、日志标签或 VictoriaMetrics label；
- 新增 label 保持低基数，工具名仅来自注册表中的稳定名称；
- 多 Bot API 与查询继续按服务端绑定的 `bot_id` 隔离；
- 新增未知场景或动作时 fail open 到 `unknown`，同时增加计数和日志提示，便于补充映射。

## 12. 测试策略

### 12.1 Go 单元与集成测试

- 分类规范化和全部历史映射；
- 显式归因优先于兼容映射；
- 普通对话与命令写入不同业务场景；
- routing、retrieval、runtime、evaluation、background、debug 入口归因；
- 单工具、多工具、并行工具和工具失败；
- 多段 response usage 累加后只写一条逻辑轮次记录；
- 主记录与工具明细事务一致性；
- 工具相关 Token 不因多工具明细重复加总；
- 按业务场景、业务动作、原始 source 和工具名聚合；
- bot_id、chat_id 和时间窗口隔离；
- 迁移幂等性和历史回填结果。

### 12.2 WebUI 测试

- API 类型与新增响应字段；
- 多 Bot/多会话业务分组合并；
- KPI 的工具调用次数、相关 Token 和错误率；
- 业务场景、动作和工具过滤；
- 历史映射与 unknown 文案；
- 空数据和部分 Bot 请求失败；
- Dashboard、会话列表、会话详情标签一致；
- 移动端紧凑布局和可操作性。

### 12.3 完成验证

- 运行受影响 Go 包测试；
- 运行数据库 migration/schema 测试；
- 运行 WebUI 单测、类型检查和生产构建；
- 运行 `git diff --check`；
- 通过固定样本核对业务分类总量等于原始总量，工具相关 Token 不重复计算。

## 13. 上线与回滚

上线顺序：

1. 应用数据库迁移并回填历史分类；
2. 部署结构化写入和工具轮次累加；
3. 部署新增 API 聚合；
4. 部署 WebUI 业务视图；
5. 观察 unknown 比例、工具记录失败和业务总量一致性。

旧 WebUI 可以继续消费新增字段后的 API。若需回滚应用代码，新增列和工具明细表可以保留，不影响旧读取；不执行破坏性 down migration。

## 14. 验收标准

1. 新产生的已知 LLM 调用均具有非 unknown 的显式业务场景和动作；
2. 普通对话与显式命令在 WebUI 中可分开统计；
3. Chunk 合并、向量化、评测和 Agent callback 可分别识别；
4. 工具调用次数与真实 handler 执行次数一致；
5. 包含工具的多阶段模型轮次 Token 完整且只计一次；
6. 多工具轮次不会在跨工具汇总时重复累计 Token；
7. 历史分类明确标记为映射值，无法归因的记录显示待归类；
8. 现有模型、kind、source_type、source 和 status 技术下钻继续可用；
9. Dashboard、会话列表和会话详情使用一致业务口径；
10. 指标写入故障不会阻断线上对话、命令、工具或后台任务。
