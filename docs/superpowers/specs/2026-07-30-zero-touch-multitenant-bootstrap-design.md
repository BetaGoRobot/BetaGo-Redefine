# Zero-Touch Multi-Tenant Bootstrap Design

**Date:** 2026-07-30

## Goal

Agent Runtime、Agent Card 和 Control/Candidate Evaluation 达到无痛上线标准：

- 每个 Bot 只需要增加 TOML 配置并重启；
- 应用自动、幂等地准备 PostgreSQL schema 和 OpenSearch index/alias；
- 不要求人工执行 SQL、OpenSearch API、动态配置或 cohort 初始化；
- 同一 PostgreSQL/OpenSearch 集群中的所有新增数据按 Bot 强制隔离；
- 初始化失败时 fail closed，不启动半可用链路。

本设计覆盖本次 Conversation Runtime 改造新增或直接依赖的所有持久化对象。
以后新增的 runtime/card/evaluation 表、队列、outbox 和索引也必须遵守相同
tenant invariant。

## Current Gaps

### PostgreSQL

- SQL migration 只作为仓库文件存在，应用启动时不会执行。
- Evaluation 模块只检查 `20260729` migration 是否已安装，缺失后退化为
  optional module，而不是完成初始化。
- `agent_sessions` 和 `evaluation_cohorts` 保存 Bot 身份，但下游
  run/step/card/outbox/episode/lane/feedback/judgment/task 表没有显式
  `tenant_id`。
- 多数 repository 直接按对象 ID 查询；调用方一旦传入另一个 Bot 的 ID，
  数据库查询本身没有统一 tenant guard。
- 手工填写的 cohort ID 是全局主键，多个 Bot 可发生命名冲突。

### OpenSearch

- `agent_conversation_events` 和 `agent_conversation_evaluations` 都假设 alias
  已由人工创建。
- 两类 mapping 都没有 `tenant_id`、`app_id`、`bot_open_id`。
- Evaluation 直接使用 `episode_id` 作为 OpenSearch `_id`。
- 搜索投影没有统一 tenant filter。
- 多个 Bot 配置同一个 alias 时会共享数据和写入命名空间。

### Rollout

- 创建 cohort 和打开 `conversation_parallel_evaluation_enabled` 仍需要外部
  操作，只有 TOML 不能开始采集。
- 健康状态无法明确展示当前 Bot 的有效 index、schema version 和 bootstrap
  结果。

## Tenant Identity

进程的租户身份由 `lark_config.app_id + lark_config.bot_open_id` 唯一确定。

规范化规则：

1. 两个字段分别去除首尾空白；
2. 任一字段为空时，所有需要持久化的 Agent/Card/Evaluation 功能 fail closed；
3. `tenant_id` 使用
   `bot_ + hex(sha256(app_id + "\x00" + bot_open_id))[:24]`；
4. 原始 `app_id` 和 `bot_open_id` 仍保存在事实记录中，便于审计；
5. API、repository、queue worker 和 projection worker 都从启动时绑定的
   `Tenant` 获取身份，不接受请求参数覆盖租户。

`tenant_id` 是稳定机器键，不把敏感身份直接暴露在 index 名或 worker 名中。

## Configuration Contract

保留现有 `runtime_config` 字段，新增以下配置：

```toml
[runtime_config]
evaluation_mode = "allowlist" # off | allowlist | on
evaluation_chat_ids = ["oc_xxx"]
evaluation_cohort_duration_hours = 24

conversation_event_index = "agent_conversation_events"
evaluation_index = "agent_conversation_evaluations"

evaluation_candidate_workers = 2
evaluation_candidate_lease_seconds = 600
evaluation_candidate_retry_seconds = 15
evaluation_candidate_poll_millis = 1000
evaluation_window_sweep_seconds = 5
evaluation_judge_workers = 1
evaluation_judge_poll_millis = 1000
evaluation_judge_model = ""
evaluation_judge_disabled = false
evaluation_projection_interval_seconds = 30
evaluation_projection_batch_size = 100
```

语义：

- `off`：不初始化 Evaluation 专属资源，也不创建 episode；
- `allowlist`：只为 `evaluation_chat_ids` 中的群自动维护 cohort；
- `on`：当前 Bot 所在的所有群均可进入自动 cohort；
- `allowlist` 且列表为空是配置错误；
- `conversation_event_index` 和 `evaluation_index` 是 base name，不是最终 alias；
- 原有动态开关继续兼容。显式动态 `false` 可以紧急关闭某个群，动态 `true`
  可以临时扩展 allowlist，但 TOML 本身已经足以完成上线。

Agent Card 保持已有 `off | shadow | allowlist | on` 配置。只要 Card 模式不是
`off`，其 PG schema 也由同一个 bootstrap 自动准备。

## PostgreSQL Bootstrap

### Migration runner

新增进程内 migration runner：

- migration SQL 使用 `go:embed` 编译进二进制；
- 建立 `betago.runtime_schema_migrations` ledger，主键为 migration version；
- 使用 PostgreSQL advisory lock 串行化多个 Bot/副本的并发启动；
- 每个 migration 记录 version、checksum、applied_at 和 binary revision；
- 已应用且 checksum 相同则跳过；
- version 相同但 checksum 不同则启动失败；
- transaction-safe migration 在单事务中执行；
- 必须非事务执行的步骤单独声明，不把
  `CREATE INDEX CONCURRENTLY` 包进 transaction；
- 所有 bootstrap SQL 幂等，支持空库、已有旧 schema 和并发冷启动。

runner 按依赖顺序覆盖：

1. `20260318_agent_runtime_tables.sql`
2. `20260325_agent_runtime_stale_run_recovery.sql`
3. `20260728_conversation_callback_runtime.sql`
4. `20260728_agent_card_surfaces.sql`
5. `20260728_conversation_parallel_evaluation.sql`
6. `20260729_conversation_evaluation_runtime.sql`
7. 新的 tenant-hardening migration

部署不再要求 DBA 手工执行这些文件。

### Tenant-hardening schema

以下表必须有不可为空的 `tenant_id`：

- `agent_sessions`
- `agent_runs`
- `agent_steps`
- `agent_capability_executions`
- `agent_projection_outbox`
- `agent_card_surfaces`
- `evaluation_cohorts`
- `evaluation_episodes`
- `evaluation_episode_messages`
- `evaluation_candidate_tasks`
- `evaluation_lane_outputs`
- `evaluation_feedback`
- `evaluation_judgments`

已有数据通过根对象的 `app_id + bot_open_id` 回填 tenant：

- Agent 链路沿 session → run → step/card/outbox 回填；
- Evaluation 链路沿 cohort → episode → message/task/lane/feedback/judgment 回填。

无法解析租户的历史行不允许静默归入当前 Bot。migration 报出计数并停止，
由运维决定修复或清理。

所有业务唯一约束包含 `tenant_id`。外键继续保留，同时增加可校验租户一致性的
复合唯一键和复合外键，防止下游行挂到另一租户的父对象。

全局 UUID/text 主键可以保留，业务生成器仍将 tenant 纳入确定性 ID：

```text
episode_<hash(tenant_id, cohort_bucket, anchor_event_id)>
cohort_<hash(tenant_id, chat_id, bucket_start)>
```

### Tenant-bound repositories

所有 repository 构造函数接收非空 `Tenant`：

```go
repo, err := agentstore.NewRepository(db, tenant)
repo, err := evaluationstore.NewRepository(db, tenant)
```

规则：

- INSERT 强制写入 repository 的 tenant；
- SELECT/UPDATE/DELETE/lease claim 必须包含 tenant predicate；
- 从数据库加载的行 tenant 不匹配时返回 `ErrTenantMismatch`；
- queue worker 只能 claim 当前 Bot 的任务；
- callback token、surface、run、episode 等按 ID 查询时仍必须带 tenant；
- WebUI 继续使用进程绑定租户，客户端不能传 `app_id`、`bot_open_id` 或
  `tenant_id`。

## OpenSearch Bootstrap

### Per-tenant names

所有 Bot 可以使用同一个 base name。有效名称自动派生：

```text
base:     agent_conversation_evaluations
alias:    agent_conversation_evaluations-<tenant_id>
physical: agent_conversation_evaluations-<tenant_id>-v1
```

Conversation Event index 使用相同规则。

名称经过 OpenSearch 合法字符和长度校验。用户配置已经包含版本后缀时仍视为
base name，避免不同 Bot 意外写入同一物理 index。

### Idempotent provisioner

OpenSearch provisioner 使用 v4 typed client，启动过程为：

1. 查询 tenant alias；
2. alias 已存在时确认只有一个 write index，并校验 `_meta.schema_name`、
   `_meta.schema_version` 和必要字段；
3. alias 不存在时查询 v1 physical index；
4. physical index 不存在则使用内嵌 mapping 创建；
5. 使用单次 `_aliases` 操作绑定 write alias；
6. 并发创建返回 already-exists 时重新读取并校验；
7. mapping 或 alias 不兼容时 fail closed，不自动覆盖生产数据。

应用不会依赖 OpenSearch 的动态 mapping 或 `action.auto_create_index`。

### Defense in depth

两类文档和 mapping 增加：

```json
{
  "tenant_id": "bot_xxx",
  "app_id": "cli_xxx",
  "bot_open_id": "ou_xxx"
}
```

OpenSearch `_id` 使用 `tenant_id + ":" + domain_object_id`。所有 search query
自动追加 `term tenant_id=<current tenant>`，调用方无法删除该过滤条件。

PG outbox 存储的是 base index 和 tenant object；最终 alias 只由 provisioner
解析，防止旧任务携带另一个 Bot 的完整 alias。

## Automatic Evaluation Cohorts

`evaluation_mode` 允许某个群时，消息进入 Evaluation service 前调用
`EnsureCohort(tenant, chat_id, occurred_at)`：

- 按 UTC 固定 bucket 计算 start/end，默认 24 小时；
- 使用稳定、租户化 cohort ID；
- `INSERT ... ON CONFLICT DO NOTHING`，支持多副本并发；
- 自动填入当前 control/candidate 代码版本和模型配置摘要；
- 到期 cohort 由现有 lifecycle worker 转入 late-feedback/finalized；
- 下一条消息自动创建下一 bucket，不需要定时人工维护。

已有手工 cohort 继续可读；只要 tenant/chat/time 匹配，优先复用，不重复采样。

## Startup Order and Failure Policy

启动顺序调整为：

1. 解析并验证 Tenant；
2. 初始化 PostgreSQL 连接；
3. 运行内嵌 PG migrations；
4. 初始化 OpenSearch client；
5. 根据启用模式幂等准备 tenant aliases；
6. 构造 tenant-bound repositories；
7. 启动 Agent/Card/Evaluation workers；
8. 开放 Lark WebSocket 和 WebUI。

当相关功能启用时：

- PG migration、tenant backfill、OpenSearch provisioning 任一步失败，进程
  readiness 失败并停止启动；
- 不允许 optional/degraded 状态继续消费消息；
- 健康接口暴露 tenant_id、migration version、effective aliases、schema
  version 和最后一次 bootstrap 结果，但不暴露凭据。

功能为 `off` 时只执行共享的 Agent Runtime migration；不会创建未启用功能的
OpenSearch index。

## Compatibility

- 现有 worker 数、lease、poll、judge 和 index base 配置继续有效；
- 现有动态 chat 开关继续支持紧急覆盖；
- 已有共享 OpenSearch alias 不会被删除或改写，新版本切换到 tenant alias；
- 已有 PG 数据在 tenant 可确定时自动回填；
- 旧二进制不应在 tenant-hardening migration 后继续运行，部署文档要求
  rolling deployment 先停旧版本再启动新版本；
- gorm-gen 文件由 migration 应用后的 canonical schema 重新生成并提交。

## Verification

完成标准必须包含以下自动化证据：

1. 空 PostgreSQL schema 冷启动自动创建所有表、列、约束和 ledger；
2. 已有 schema 升级自动回填 tenant，重复启动无变化；
3. 两个进程并发 migration 只应用一次；
4. transaction 和 non-transaction migration 均正确执行；
5. 空 OpenSearch 集群自动创建两个 tenant physical index 和 alias；
6. 两个 Bot 使用相同 base name 时获得不同 alias；
7. 两个 Bot 使用相同业务 ID 时文档和 PG 行互不覆盖；
8. repository、queue claim、callback、WebUI 查询无法跨租户读取或修改；
9. alias 已存在时启动幂等；
10. mapping 不兼容或权限不足时 readiness 失败；
11. 只有 TOML 配置时，首条 allowlist 群消息自动创建 cohort 和双 lane episode；
12. 不执行任何人工 SQL/curl 的全新环境端到端测试通过；
13. 用户提供的 warmup、目标包测试、race、vet 和生产构建全部通过。

## Deployment Contract

最终交付给运维的动作只有：

1. 部署包含本实现的二进制；
2. 在每个 Bot 的 TOML 中配置 rollout、worker 和两个 index base name；
3. 重启并观察 readiness。

不再提供“上线前手工执行 SQL/OpenSearch 初始化”作为必需步骤。
