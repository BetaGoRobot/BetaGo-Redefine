# LLM Token Usage Drilldown

## Business attribution

WebUI now treats business attribution as the primary cost view and keeps the existing technical dimensions for diagnosis.

| `business_scene` | Meaning |
| --- | --- |
| `conversation` | Ambient chat, mention replies, and P2P replies |
| `command` | Explicit command-triggered model work |
| `routing` | Intent recognition and tool planning |
| `retrieval` | History/topic recall, embeddings, and RAG answers |
| `agent_runtime` | Agent callback continuation |
| `evaluation` | Candidate generation and judging |
| `background` | Message embeddings, chunking, and reindexing |
| `debug` | Explicit debug calls |
| `unknown` | Calls that cannot be classified safely |

`business_operation` is the stable, more specific action within a scene. New online calls should set both fields explicitly at the business entry point. `attribution_mode` explains provenance:

- `explicit`: the active code path declared the scene and operation;
- `legacy_mapping`: a historical row was inferred from its technical `source`;
- `unknown`: no safe attribution exists.

Historical `source=chat` rows can only be mapped to `conversation/chat_reply`; they cannot be reconstructed as command, mention, or P2P calls. WebUI therefore labels them as historical mappings instead of presenting them as precise attribution.

## Tool-related usage semantics

One logical model turn may contain planning, one or more real tool handler executions, and a synthesis response. The recorder writes one aggregate token row for that complete turn and child rows for each tool execution.

`tool_related_tokens` is calculated once from main rows whose `tool_call_count > 0`:

```sql
SUM(CASE WHEN tool_call_count > 0 THEN total_tokens ELSE 0 END)
```

This value is additive by business scene and operation. It is deliberately not copied or divided across individual tools because several tools can share the same model context. Per-tool views therefore show calls, success/error counts, success rate, average duration, and P95 duration—not exclusive token cost. When several chat/Bot aggregates are merged in the browser, average duration remains call-weighted and the displayed P95 reference is the maximum child P95; it is not recomputed from raw samples.

Tool detail rows contain only a sanitized tool name, status, duration, controlled error kind, trace ID, and timestamps. Tool arguments, outputs, message content, and raw error strings are not persisted.

All WebUI queries filter by `bot_id`, `chat_id`, and time window. The temporary `bot_id=''` compatibility path applies only to historical rows during backfill.

## Business cost query

```sql
SELECT
  business_scene,
  business_operation,
  attribution_mode,
  COUNT(*) AS turns,
  SUM(total_tokens) AS total_tokens,
  SUM(tool_call_count) AS tool_calls,
  SUM(CASE WHEN tool_call_count > 0 THEN total_tokens ELSE 0 END) AS tool_related_tokens
FROM betago.llm_token_usage_records
WHERE bot_id = :bot_id
  AND chat_id = :chat_id
  AND created_at >= :since
GROUP BY business_scene, business_operation, attribution_mode
ORDER BY total_tokens DESC;
```

## Tool quality query

```sql
SELECT
  tool_name,
  COUNT(*) AS calls,
  COUNT(*) FILTER (WHERE status = 'success') AS successes,
  COUNT(*) FILTER (WHERE status = 'error') AS errors,
  AVG(duration_ms) AS average_duration_ms,
  PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms) AS p95_duration_ms
FROM betago.llm_tool_call_records
WHERE bot_id = :bot_id
  AND chat_id = :chat_id
  AND called_at >= :since
GROUP BY tool_name
ORDER BY calls DESC;
```

## Technical drilldown

The Grafana dashboard `deploy/grafana/betago-llm-token-usage.json` uses VictoriaMetrics for aggregate trends. Use it to answer:

- Is token usage high because QPS increased?
- Is token usage high because tokens/request increased?
- Which `model/source/status` combination caused the spike?

For error logs and trace drilldown, use the Postgres detail table. Do not add `trace_id` or `response_id` to Prometheus labels; they are high-cardinality identifiers.

## Recent Error Calls

```sql
SELECT
  created_at AT TIME ZONE 'Asia/Shanghai' AS created_at_cn,
  model,
  kind,
  source_type,
  source,
  status,
  chat_name,
  chat_id,
  user_name,
  open_id,
  prompt_tokens,
  completion_tokens,
  total_tokens,
  response_id,
  trace_id,
  error
FROM betago.llm_token_usage_records
WHERE created_at BETWEEN $__timeFrom() AND $__timeTo()
  AND status <> 'success'
  AND model ~ '${model:regex}'
  AND kind ~ '${kind:regex}'
  AND source_type ~ '${source_type:regex}'
  AND source ~ '${source:regex}'
  AND chat_id ~ '${chat_id:regex}'
  AND open_id ~ '${open_id:regex}'
ORDER BY created_at DESC
LIMIT 200;
```

Use `trace_id` to open the matching Jaeger trace. Application logs include the same `trace_id` via `logs.L().Ctx(ctx)`, so the same value also correlates to error logs.

## High-Cost Calls

```sql
SELECT
  created_at AT TIME ZONE 'Asia/Shanghai' AS created_at_cn,
  model,
  source,
  status,
  chat_name,
  user_name,
  total_tokens,
  prompt_tokens,
  completion_tokens,
  response_id,
  trace_id,
  error
FROM betago.llm_token_usage_records
WHERE created_at BETWEEN $__timeFrom() AND $__timeTo()
  AND model ~ '${model:regex}'
  AND source ~ '${source:regex}'
  AND chat_id ~ '${chat_id:regex}'
ORDER BY total_tokens DESC
LIMIT 200;
```
