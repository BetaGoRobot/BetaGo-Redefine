# Agentic 灰度 WebUI 运维手册

## 上线结果

上线只需配置并重启。无需手工执行 SQL、创建 PostgreSQL 表、创建
OpenSearch index/alias，或为每个 Bot 预生成任何数据。

推荐配置：

```toml
[runtime_config]
evaluation_mode = "allowlist"
evaluation_chat_ids = []

[agent_card]
enabled = true
mode = "allowlist"
allow_chat_ids = []

[webui_config]
addr = ":8090"
auth_token = "请替换为高熵随机值"
```

空 allowlist 的含义是“能力基础设施 ready，但没有 chat 默认启用”：

- Conversation Runtime 默认继承关闭；
- callback continuation 默认继承关闭；
- 并轨评测 ready，但默认继承关闭；
- Agent Card authoring/sender ready，但默认继承关闭。

因此首次打开页面时，正常状态是四项均显示“继承（当前关闭）”，不是部署失败。
操作员随后在 WebUI 对当前 Bot + 当前 chat 单独开启。

应用启动继续幂等执行内嵌 PostgreSQL migration。Evaluation 的当前 Bot 专属
OpenSearch physical index/write alias 在首次实际启用时幂等准备；不存在就创建，
已存在就校验兼容性。任何初始化错误都会 fail closed，不会把请求落到其他 Bot
的索引。

## 页面操作

### 单个会话

进入“会话详情 → Agentic 灰度”。每项都有三个状态：

- `继承`：删除 chat override，重新采用 Bot 全局/TOML/默认基线；
- `开启`：为当前 Bot + chat 写显式 `true`；
- `关闭`：为当前 Bot + chat 写显式 `false`。

“Full Agentic”会把四项草稿全部设为开启；“全部恢复继承”会把四项草稿全部设为
继承。预设只改本地草稿，仍需点击保存。

保存携带最后一次读取到的 revision。若别人已修改，服务端返回 `409`；页面会
刷新服务端状态、保留本地草稿，并要求操作员重新核对，不会静默覆盖。

### 批量会话

会话列表在以下任一条件满足时进入单 Bot 操作态：

- 当前只选择了一个 Bot；
- Bot 过滤器明确选中了一个 Bot；
- 下钻路径已绑定一个 Bot。

全部 Bot 视图只读，不显示选择列。单 Bot 操作态可勾选会话并打开“批量 Agentic
灰度”。普通过滤不会丢失已保留的选择，页面会提示被过滤器隐藏的选择数量。

批量操作固定分两步：

1. 服务端 `dry_run=true`，对所有 chat 做 revision、可用性和变更校验；
2. 预览通过后才允许 `dry_run=false` 原子提交。

提交使用预览中每个 chat 的 `before.revision` 再次校验。任一 chat 冲突或能力
不可用时，整批不写入。单次读取最多 100 个 chat（前端自动分块），单次写入最多
200 个 chat。

## 多 Bot 与安全边界

每个 larkrobot 进程的 rollout service 在启动时绑定当前
`app_id + bot_open_id` 派生出的 tenant namespace。WebUI 请求只接受 chat ID、
revision 和四项 capability change，不接受以下字段：

- `bot_id`
- `tenant_id`
- `app_id`
- `bot_open_id`

出现这些字段或其他未知字段时返回 `400 invalid_request`。返回体里的 Bot 身份
仅用于展示，不能作为写入目标。

多个 Bot 可以共用 PostgreSQL、Redis、OpenSearch base name 和同一个 WebUI
前端。浏览器仍通过 `/bot/<id>/api/*` 访问各 Bot 独立后端；每个 Bot 使用自己的
WebUI token 和服务端绑定 namespace。批量抽屉不会接受跨 Bot 行。

生产环境应配置高熵 `webui_config.auth_token`。为保持历史兼容，token 为空时
写请求仍会放行，因此只适用于可信内网。

## 生效优先级

每项能力按以下顺序解析：

1. 当前 chat 显式 override；
2. Bot 全局动态配置；
3. TOML baseline（Evaluation/Agent Card）；
4. 默认关闭。

最终 `effective` 还受能力可用性约束。即使显式开启，当部署没有初始化对应能力
时仍 fail closed，并返回 `422 capability_unavailable`。常见原因：

- `parallel_evaluation_not_initialized`：`evaluation_mode=off`；
- `agent_card_off`：`agent_card.enabled=false` 或 `mode=off`；
- `agent_card_shadow_mode`：shadow 只编译，不允许实时投递。

## API 与错误码

```text
GET  /api/chats/{chatID}/agentic-rollout
PUT  /api/chats/{chatID}/agentic-rollout
GET  /api/agentic-rollouts?chat_ids=oc_1,oc_2
POST /api/agentic-rollouts/batch
```

稳定错误码：

- `invalid_request`：未知字段、状态、capability、空目标或数量超限；
- `stale_revision`：状态已变化，刷新后重试；
- `capability_unavailable`：当前部署没有 live 能力；
- `persistence_unavailable`：动态配置事务不可用；
- `internal_error`：非预期错误。

## 观测与排障

Prometheus 指标：

```text
betago_webui_agentic_rollout_reads_total
betago_webui_agentic_rollout_mutations_total
betago_webui_agentic_rollout_chats_total
betago_webui_agentic_rollout_conflicts_total
betago_webui_agentic_rollout_unavailable_total
```

日志包含 request ID、Bot namespace hash、chat 数量、capability 名称、操作类型和
错误码；不会记录 Bearer token、App Secret、消息原文或完整卡片内容。

排障顺序：

1. 查看页面能力的 `available` 与 `reason`；
2. 查看 `/healthz` 中 runtime/evaluation/Agent Card 组件状态；
3. 核对当前请求是否走到了正确的 `/bot/<id>/api`；
4. 检查 `409` 是否来自并发操作，并重新读取 revision；
5. 检查 PostgreSQL migration 与当前 tenant OpenSearch bootstrap 结果。

回滚时，在 WebUI 将 chat 恢复继承或显式关闭即可。若需全局停用，把
`evaluation_mode="off"`、`agent_card.enabled=false` 后重启；已有 callback、
run、step 和评测事实记录保留用于幂等收敛与审计。
