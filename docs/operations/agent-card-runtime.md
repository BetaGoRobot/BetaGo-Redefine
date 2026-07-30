# Agent Card Runtime 运维手册

## 灰度配置

Agent 自主卡片默认关闭：

```toml
[agent_card]
enabled = false
mode = "off" # off | shadow | allowlist | on
allow_chat_ids = []
max_repair_attempts = 2
default_expiry_seconds = 600
patch_worker_count = 1
patch_lease_seconds = 30
```

- `off`：不向模型暴露 `discover_card_components` / `compose_card`。
- `shadow`：暴露工具，执行严格校验与 Lark JSON 编译，但不落库、不发送；
  工具结果会要求 Agent 继续用文本完成回复。
- `allowlist`：只在 `allow_chat_ids` 指定的 chat 暴露工具并允许发送。
- `on`：全量开启。

推荐按 `off -> shadow -> allowlist -> on` 推进。关闭 authoring 不会禁用旧卡片
的回调消费；已经发出的 waiting interaction 仍可完成、取消或过期。

上线只需加入配置并重启，不执行 SQL。Agent Card 所需表、tenant 列、复合
外键和索引由应用启动时的内嵌 migration 幂等创建或升级。surface、callback、
run、step 和 capability execution 全部绑定由当前 `app_id + bot_open_id`
派生的 `tenant_id`；两个 Bot 即使使用相同业务 ID 也不能互相读取、claim
或完成回调。无法解析租户的历史数据会使 migration fail closed。

## 安全边界

- Agent 只能提交语义 Card DSL，不能提交原始飞书 JSON、runtime token 或可信
  capability 参数。
- token 和 capability input 由服务端绑定，数据库只保存 token hash 和脱敏
  编译产物。
- 公共 authoring 暂不接受 `capability_confirm`；该模式必须先经过服务端可信
  capability adapter。
- 卡片输入禁止密码、token、OTP、身份和支付信息。

## Patch 补偿

回调会立即返回终态卡片；异步 capability 完成等路径把待更新 surface 标为
`pending`。`agent_card_patch_reconciler` 从 PostgreSQL 查询 due surface，
使用 worker ID、attempt count 和 lease fencing 抢占，失败后保留同一 message
重试，不发送替代卡片。

管理面 `/healthz` 的组件 stats 包含：

- `running` / `workers`
- `scanned` / `completed` / `skipped` / `failed`
- `last_success_at` / `last_failure_at` / `last_error`

多 worker 同时看到同一 surface 时产生的 not-found/conflict 属于正常竞争，
计入 `skipped`，不会让组件降级。数据库或飞书 patch 错误会进入 `failed` 并将
组件标记为 degraded；后续一次无错误 sweep 会恢复 ready。

## 回滚

1. 把 `enabled=false`、`mode="off"` 后重启，停止创建新 Agent Card。
2. 保持 callback handler 和 patch reconciler 运行，让旧卡片收敛。
3. 不删除 surface/run/step；这些记录是幂等重放、反馈归因和审计依据。
4. OpenSearch 故障不阻塞 PostgreSQL 回调状态；恢复后 projection 会继续重试。
