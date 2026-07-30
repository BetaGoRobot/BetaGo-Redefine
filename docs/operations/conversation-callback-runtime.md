# Conversation Callback Runtime 上线与回滚指南

## 适用范围

当前 Conversation Callback Runtime 已用于 `edit_schedule` 的确认/取消卡片。新链路会把 run、wait step、可信编辑参数和 projection outbox 写入 PostgreSQL；卡片只携带运行时 envelope，不携带可信编辑参数。

运行边界如下：

- `conversation_runtime_enabled` 控制当前群聊是否创建新的 runtime interaction。
- `conversation_callback_continuation_enabled` 只控制回调完成后是否运行 LLM 续接；关闭时仍会消费 durable continuation，但使用确定性的 `observe_only`，不会调用模型。
- 两个开关只按 chat/global 解析，user 或 chat-user 配置不能激活该链路。
- runtime envelope 卡片由 continuation dispatcher 处理；没有 runtime 字段的旧 Schedule 卡片继续走 V1 handler。
- PostgreSQL 是 run、step、幂等执行和 projection outbox 的事实来源。OpenSearch 只是可重建的查询投影。
- 回调同步完成鉴权、token/revision/expiry 校验和 Schedule mutation；LLM decision、回复投递与 OpenSearch projection 在 durable step/outbox 基础上异步执行。

## 上线前预算

默认预算和启动约束为：

- interaction wait TTL：30 分钟；
- conversation executor timeout：120 秒，必须小于 continuation lease 180 秒；
- OpenSearch 单次 write timeout：30 秒；
- projection executor timeout：60 秒，必须处于 write timeout 30 秒与 projection lease 120 秒之间。

进程启动时会拒绝不满足上述 executor/lease 关系的配置。修改 `[runtime_config]` 时不要让执行超时触及或超过 lease。

## 上线流程

### 1. 配置并启动

不需要人工执行 SQL、重新生成 GORM 文件或创建 OpenSearch index/alias。应用
启动时会幂等执行内嵌 PostgreSQL migration，并为当前 Bot 自动创建租户专属
的会话事件 physical index 和 write alias。

`[runtime_config].conversation_event_index` 是 base name。多个 Bot 可以使用
相同值，最终 alias 会自动追加由 `app_id + bot_open_id` 派生的租户后缀。
初始化失败时 `runtime_schema` 或 `tenant_search_schema` 会令启动失败，不会
静默进入共享或半初始化状态。

启动后的 `/healthz` 会暴露脱敏后的 `tenant_id`、当前 migration
version/checksum、实际 conversation alias/physical index、schema version
以及最近一次 bootstrap 结果。alias 已存在时，应用仍会校验 alias 指向、
mapping `_meta` 和租户字段；指向其他 Bot、mapping 不兼容或缺少
OpenSearch 建索引/读 mapping/维护 alias 权限都会 fail closed。

### 两个动态开关均为 false 时部署

两个开关代码默认值都是 `false`。部署前还应确认全局值为 false，且没有遗留 chat=true override。可用现有配置命令设置全局值：

```text
/config set --key=conversation_runtime_enabled --value=false --scope=global
/config set --key=conversation_callback_continuation_enabled --value=false --scope=global
```

通过 `/config list --scope=chat` 检查测试群和历史灰度群；不要用 user scope 启用这两个开关。

部署后验证：

- 旧 Schedule 确认/取消卡片仍走 V1 fallback；
- 新进程正常启动；
- 如果配置了 `[management_http_config].addr`，`/healthz` 中 `conversation_runtime_worker` 和 `conversation_projection_worker` 可见；
- 启动 bootstrap 完成后发生的短暂 OpenSearch 故障只使 projection worker
  降级并保留 outbox 重试；启动时无法验证租户 alias/mapping 则 readiness
  失败。

### 只为一个测试群启用 runtime

在目标测试群执行：

```text
/config set --key=conversation_runtime_enabled --value=true --scope=chat
```

保持 `conversation_callback_continuation_enabled=false`。发起一次 Schedule 编辑，确认新卡片包含 runtime envelope，并在 PostgreSQL 中出现 `waiting_approval` run、completed wait step 和 pending projection outbox。

该开关按 chat ID 解析。即使同一用户在其他群或私聊配置了 user=true，也不会激活目标群。

### 再为同一测试群启用 callback continuation

在同一目标群执行：

```text
/config set --key=conversation_callback_continuation_enabled --value=true --scope=chat
```

这一步只改变 callback mutation 完成后的续接策略。确认按钮的鉴权、幂等 mutation 和 durable event 写入不依赖 LLM 开关。

### 验证重复回调、续接、projection 和 worker health

完成一次人工验收：

1. 发起 Schedule 编辑并取得确认卡；
2. 对同一确认动作重复触发两次；
3. 确认 Schedule 只更新一次；
4. 确认最多投递一条 frozen reply；
5. 确认 run 最终为 `completed`；
6. 确认 outbox 被投影，或在 OpenSearch 故障时保持 `pending` 并设置下一次重试时间。

PostgreSQL 检查示例：

```sql
-- 当前群最近的 runtime run
select r.id, r.status, r.waiting_reason, r.revision, r.error_text,
       r.created_at, r.updated_at, r.finished_at
from betago.agent_runs r
join betago.agent_sessions s on s.id = r.session_id
where s.chat_id = '<chat_id>'
  and r.tenant_id = '<tenant_id>'
  and s.tenant_id = '<tenant_id>'
order by r.created_at desc
limit 20;

-- 单个 run 的 durable step、lease 和 retry 状态
select id, "index", kind, status, dedupe_key, attempt_count,
       worker_id, lease_expires_at, error_text, external_ref
from betago.agent_steps
where run_id = '<run_id>'
  and tenant_id = '<tenant_id>'
order by "index";

-- duplicate callback 应复用同一 idempotency_key；同一个主键只有一条执行记录
select idempotency_key, capability_name, status, count(*)
from betago.agent_capability_executions
where run_id = '<run_id>'
  and tenant_id = '<tenant_id>'
group by idempotency_key, capability_name, status;

-- projection 成功或 durable retry 状态
select id, step_id, index_alias, document_id, status, attempt_count,
       next_attempt_at, worker_id, lease_expires_at, last_error
from betago.agent_projection_outbox
where step_id in (
  select id from betago.agent_steps
  where run_id = '<run_id>' and tenant_id = '<tenant_id>'
)
and tenant_id = '<tenant_id>'
order by created_at;
```

如果启用了 management HTTP：

```bash
curl -fsS "${MANAGEMENT_URL}/healthz"
curl -fsS "${MANAGEMENT_URL}/metrics" |
  grep 'betago_runtime_component_state'
```

仓库当前存在的运行时指标名只有：

- `betago_runtime_live`
- `betago_runtime_ready`
- `betago_runtime_degraded`
- `betago_runtime_component_state`

`/healthz` 的组件 stats 可检查：

- `conversation_runtime_worker`：`running`、`iterations`、`consecutive_errors`、`last_submitted`、`last_expired`、`last_success_at`、`last_error`、`terminal_error`；
- `conversation_projection_worker`：同一组 worker 字段，其中 `last_submitted` 表示本轮提交的 projection 数；
- `conversation_executor` / `conversation_projection_executor`：`queue_depth`、`running`、`completed`、`failed`、`rejected`、`last_error`。

worker 连续三次执行错误会显示为 `degraded`，成功一轮后恢复为 `ready`。目前没有独立命名的 duplicate-callback 或 continuation 业务 counter；应使用上面的 PostgreSQL 幂等记录、step/outbox 状态和最终用户行为检查，不要假设不存在的 metric。

### 回滚：先禁止新 interaction，再排空已有 waiting 卡

在所有已灰度群先关闭新 interaction 创建：

```text
/config set --key=conversation_runtime_enabled --value=false --scope=chat
```

随后关闭 LLM continuation：

```text
/config set --key=conversation_callback_continuation_enabled --value=false --scope=chat
```

这一顺序的效果是：

- 后续 `edit_schedule` 不再创建新的 runtime wait；
- 已经发出的 runtime 卡仍由 continuation dispatcher 解析，仍可完成一次幂等 Schedule mutation；
- 已有 durable continuation 由 disabled processor 收口为 `observe_only`，不会继续调用模型；
- 未点击的 wait 最多保留 30 分钟，expiry worker 会写入 timeout step/outbox，把 run 置为 `cancelled` 并释放 session；
- OpenSearch 故障不影响上述 PostgreSQL resolve/expiry，outbox 会保留重试状态。

不要先回滚到不认识 runtime envelope 的旧二进制，也不要删除表、索引或 alias；那会让已发出的 waiting 卡失去 resolve 路径。等待 `waiting_approval` run 已处理或过期后，再决定是否回滚应用版本。数据库 schema 和 outbox 数据应保留。

## 故障恢复与排查

### 回调失败或重复更新

- 检查 `agent_runs.revision`、wait step 的 `input_json`、callback token 是否匹配；wrong token、stale revision、expired wait 都会拒绝 mutation。
- 检查操作者是否通过 Schedule capability 的 chat/actor 权限校验。
- 对同一 run 检查 `agent_capability_executions`。相同 interaction/revision 的确认必须只有一个幂等执行记录。
- completed callback 重放只返回已持久化 outcome，不应再次执行 Schedule updater。

### 回调成功但没有续接回复

- 确认目标群的 `conversation_callback_continuation_enabled` 值；false 时按设计不会生成 LLM 回复。
- 检查 `agent_steps` 中 `decide` / `reply` 的 `status`、`attempt_count`、`lease_expires_at` 和 `error_text`。
- 检查 `conversation_runtime_worker` 的 `last_submitted`、`consecutive_errors` 和 executor 的 `rejected`。
- generator 或 Lark delivery 失败会把当前 durable step 放回 queued，并设置下一次重试时间；进程重启后 worker 会从 PostgreSQL 再次发现它。
- Ark model 为空不会阻止两个开关均关闭时部署；只有启用 LLM continuation 并实际消费 decide step 时才会产生可重试错误。

### projection 堆积或 OpenSearch 不可用

- 检查 `agent_projection_outbox.status='pending'`、`attempt_count`、`next_attempt_at` 和 `last_error`。
- 检查 alias 是否存在且有 `is_write_index=true` 的物理索引。
- projection 使用独立 executor、lease 和指数退避。OpenSearch 写失败不会回滚已经提交的 callback mutation、continuation decision 或回复投递。
- OpenSearch 恢复后让 `conversation_projection_worker` 继续消费；不要手工把 PostgreSQL run 改回 waiting。

### 过期与重启

- wait 到期后，expiry worker 会写 `interaction_expiry` resume step 和 projection outbox，并把 run 置为 `cancelled`。
- callback、continuation 和 projection 状态均持久化在 PostgreSQL。进程崩溃或发布重启后，新 Runtime 会扫描 queued 或 lease 已过期的记录继续处理。
- `running` step/outbox 只有 lease 过期后才能被其他 worker reclaim；不要在 lease 尚有效时直接清空 worker 字段。
