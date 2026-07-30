# 群聊 Control/Candidate 并轨评测运维手册

## 目标与安全边界

并轨评测在同一条群消息上保存两条对齐链路：

- `control` 是现网决策；只有 serving lane 可以发送消息、写外部系统或触发卡片。
- `candidate` 是 counterfactual shadow；只能使用显式只读工具白名单，不能发送飞书消息或执行外部写操作。
- 前向窗口固定保留 anchor 之前最近 20 条消息。
- 后向窗口最多 50 条，在话题边界、15 分钟或消息上限任一条件满足时关闭。
- 显式反馈最多在 anchor 后 24 小时归因；推断反馈只能落在因果后向窗口内。
- 用户回复、reaction、卡片点击只有在目标 message ID 精确匹配实际投递消息时，才标记为 serving-lane feedback。shadow 输出没有可供用户交互的投递身份。

PostgreSQL 是事实源；OpenSearch
`agent_conversation_evaluations` 只是可重建的检索投影。

## 上线前检查

1. 执行以下迁移：

   - `script/sql/20260728_conversation_parallel_evaluation.sql`
   - `script/sql/20260729_conversation_evaluation_runtime.sql`

2. 用 `script/opensearch/agent_conversation_evaluations_v1.json`
   创建物理 index，并把 alias
   `agent_conversation_evaluations` 指向该 index。

3. 配置 `[runtime_config]`。推荐起点：

```toml
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
evaluation_index = "agent_conversation_evaluations"
```

`evaluation_judge_model` 为空时优先使用
`ark_config.reasoning_model`，其次使用 `normal_model`。没有可用 Judge
模型时，窗口和双 lane 数据仍会采集，自动 Judge 暂停。

4. 在配置管理中按 chat 打开
   `conversation_parallel_evaluation_enabled`。该开关默认关闭；只创建 cohort
   而不打开开关不会产生新的 episode。

## 创建评测 cohort

时间使用带时区的绝对时间。`serving_lane` 首轮必须保持 `control`，避免把
shadow 误当现网输出。

```sql
insert into betago.evaluation_cohorts (
    id,
    app_id,
    bot_open_id,
    chat_ids,
    start_at,
    end_at,
    status,
    serving_lane,
    control_version,
    candidate_version,
    judge_config_json,
    sampling_policy_json,
    result_version
) values (
    'cohort_20260730_chat_a',
    'cli_xxx',
    'ou_xxx',
    '["oc_xxx"]'::jsonb,
    '2026-07-30 10:00:00+08',
    '2026-07-30 12:00:00+08',
    'collecting',
    'control',
    'control_20260730',
    'candidate_20260730',
    '{"dimensions_version":"v1"}'::jsonb,
    '{"sample_rate":1}'::jsonb,
    0
);
```

同一时间段可以建立多个 cohort。每个 cohort 独立归因和出结果，因此同一条
用户反馈可以出现在多个 cohort 的对齐 episode 中。

## 生命周期

```text
collecting
  └─ end_at 到达
      → waiting_late_feedback
          └─ end_at + 24h 到达
              → finalized
```

episode 生命周期：

```text
collecting
  └─ 后向窗口关闭，并且 Control/Candidate 均已保存
      → ready_for_judge
          └─ Judge 严格 JSON 校验并成功追加
              → judged
```

Judge 结果是 append-only：

- 首次结果为 `version=1`。
- judged episode 收到更新反馈后，会生成 `version=2`，并通过
  `supersedes_id` 指向 v1。
- 旧版本不会更新或删除。
- Judge 返回 malformed JSON 时不写 judgment，worker 退避后重试。

## 查询与核对

### WebUI 抽检 API

评测数据包含群聊原文、上下文和用户反馈，属于敏感读取。必须先配置
`[webui_config].auth_token`；即使是 GET，也必须携带同一个 Bearer token。
未配置 token 时，评测 API 返回 `503`，不会退化成匿名读取。

```bash
curl -H 'Authorization: Bearer <token>' \
  'http://127.0.0.1:8090/api/evaluations?chat_id=oc_xxx&from=2026-07-30T00:00:00%2B08:00&to=2026-07-31T00:00:00%2B08:00&limit=50'

curl -H 'Authorization: Bearer <token>' \
  'http://127.0.0.1:8090/api/evaluations/<episode_id>'
```

列表支持 `chat_id`、`cohort_id`、`status`、`winner`、
`needs_review`、`from`、`to` 和 `cursor`。单次最多 100 条，时间范围最多
31 天。服务端固定使用当前进程的 `app_id + bot_open_id` 过滤，调用方不能
覆盖 Bot 身份。

详情一次返回：

- pre/anchor/post 消息时间线；
- Control/Candidate 的 join/topic、上下文、排除上下文和 tool plan；
- 两条回复、延迟、token 和错误；
- 用户直接回复、reaction、纠正和卡片反馈；
- 自动 Judge 与人工判断的完整版本链。

追加人工判断：

```bash
curl -X POST \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{
    "evaluator_id":"reviewer-1",
    "winner":"candidate",
    "scores":{"response_relevance":9,"context_correctness":8},
    "problem_tags":["control_missed_context"],
    "rationale":"Candidate 使用了前文约束。",
    "confidence":90,
    "needs_review":false
  }' \
  'http://127.0.0.1:8090/api/evaluations/<episode_id>/judgments'
```

人工结果使用 `source=human` 独立追加版本；并发提交由 episode 行锁串行化，
不会覆盖自动 Judge 或旧人工版本。

查看 cohort/episode 状态：

```sql
select status, count(*)
from betago.evaluation_cohorts
group by status
order by status;

select status, count(*)
from betago.evaluation_episodes
group by status
order by status;
```

查看 Judge backlog：

```sql
select count(*) as judge_backlog
from betago.evaluation_episodes e
where e.status = 'ready_for_judge'
  and e.post_window_end is not null
  and e.post_window_end <= now()
  and (
      select count(distinct lane)
      from betago.evaluation_lane_outputs o
      where o.episode_id = e.id
        and o.lane in ('control', 'candidate')
  ) = 2;
```

查看双 lane、上下文和实际投递身份：

```sql
select
    e.id as episode_id,
    e.anchor_message_id,
    o.lane,
    o.output_mode,
    o.join_decision,
    o.topic_relation,
    o.reply_text,
    o.context_snapshot_json,
    o.excluded_context_json,
    o.tool_plan_json->>'delivery_message_id' as delivery_message_id,
    o.error_json
from betago.evaluation_episodes e
join betago.evaluation_lane_outputs o on o.episode_id = e.id
where e.cohort_id = 'cohort_20260730_chat_a'
order by e.anchor_at, e.id, o.lane;
```

查看用户前向/后向反馈及版本链：

```sql
select
    f.episode_id,
    f.feedback_type,
    f.explicitness,
    f.target_lane,
    f.target_message_id,
    f.content_json,
    f.occurred_at
from betago.evaluation_feedback f
join betago.evaluation_episodes e on e.id = f.episode_id
where e.cohort_id = 'cohort_20260730_chat_a'
order by f.occurred_at, f.id;

select
    j.episode_id,
    j.version,
    j.winner,
    j.confidence,
    j.needs_review,
    j.problem_tags_json,
    j.rationale,
    j.supersedes_id,
    j.created_at
from betago.evaluation_judgments j
join betago.evaluation_episodes e on e.id = j.episode_id
where e.cohort_id = 'cohort_20260730_chat_a'
order by j.episode_id, j.source, j.version;
```

运行时模块 `conversation_evaluation` 的 stats 暴露：

- cohort/episode 状态总量；
- 每条 lane 的总量、错误数、平均延迟和平均 token；
- join/topic agreement；
- shadow safety block；
- Judge backlog 和最新 win/tie/loss；
- feedback/late feedback；
- OpenSearch projection backlog、cursor 和最近错误。

## 降级与紧急停用

1. 先按 chat 关闭 `conversation_parallel_evaluation_enabled`。这会停止创建新
   episode，不影响正常聊天。
2. 已存在的 candidate、窗口、Judge 和投影任务仍会继续收敛。
3. 如需立即停 Judge，设置 `evaluation_judge_disabled = true` 后重启；采集链路
   和正常聊天仍保留。
4. OpenSearch 故障不会影响 PostgreSQL 事实数据或 serving 回复。恢复 alias
   后重启进程，projection worker 会从头幂等重扫。
5. 不要把 `serving_lane` 改成 `candidate` 作为临时灰度手段；候选链路尚不拥有
   外部写权限。正式切流应走独立发布和权限评审。

## 验证命令

```bash
BETAGO_CONFIG_PATH=.dev/config.toml \
go test -count=1 -tags=custom_skip_vips \
  ./internal/application/lark/conversationeval \
  ./internal/infrastructure/evaluationstore \
  ./internal/infrastructure/evaluationindex \
  ./cmd/larkrobot

BETAGO_CONFIG_PATH=.dev/config.toml \
go test -race -count=1 -tags=custom_skip_vips \
  ./internal/application/lark/conversationeval \
  ./internal/infrastructure/evaluationstore \
  ./internal/infrastructure/evaluationindex
```
