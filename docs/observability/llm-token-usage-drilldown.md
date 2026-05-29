# LLM Token Usage Drilldown

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
