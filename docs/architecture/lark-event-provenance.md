# Lark reaction and recall diagnostics

Reaction events are journaled before queue admission or message lookup. Actor IDs, operator type, original action timestamp, event/message IDs and trace ID are separate columns and log/span attributes. The complete event JSON is also retained after recursive credential redaction; SDK raw event JSON is preferred when available so unmodeled fields survive. HTTP request headers are excluded.

`betago.lark_event_audits` is diagnostic provenance, distinct from the successful `interaction_stats` business INSERT. A lookup failure or queue rejection does not erase the original event. `status` describes reception/resolution (`received`, `resolved`, `unresolved`, `enqueue_failed`, or `recalled`); it does **not** claim that downstream statistics or reaction delivery succeeded.

`betago.lark_message_locations` maps a message to its chat within an application and bot scope. Inbound ingress and successful outbound provider receipts observe the mapping before optional indexing. Private-mode messages are excluded from this locator. Recall events retain their supplied chat ID and mark existing locations recalled. Later observations cannot clear that flag or replace a message with a different chat.

Reaction group resolution reads the locator without the legacy GORM query cache, then makes at most one three-second remote lookup. Both operators reuse this result. An unresolved group skips group-dependent statistics/follow work; it is not labeled recalled simply because Feishu returned `230050`.

## Find an event

Use the actual running application's and bot's IDs; `actor_open_id` is the person who added the reaction, not `bot_open_id`.

```sql
select event_id, event_type, message_id,
       operator_type, actor_open_id, actor_app_id,
       reaction_type, action_time,
       occurred_at at time zone 'Asia/Shanghai' as action_time_shanghai,
       received_at at time zone 'Asia/Shanghai' as receipt_time_shanghai,
       chat_id, status, resolution_source, error_code, error, trace_id, payload
from betago.lark_event_audits
where app_id = 'cli_your_application'
  and bot_open_id = 'ou_your_bot'
  and message_id = 'om_target_message'
order by received_at;
```

`action_time` is the unmodified provider timestamp. Invalid/missing values leave `occurred_at` NULL; receipt time is never substituted for action time. Stored timestamps are UTC; the query displays Asia/Shanghai (UTC+08:00).

Structured log searches can filter `message_id`, `event_id`, `actor_open_id`, `action_time`, or `trace_id`. Reaction processing spans expose `actor.open_id`, `operator.type`, `event.action_time`, `message.id` and `event.id` independently of payload length. `lark.reaction.resolve` records the group resolution and any provider error code under the same trace as ingress and statistics processing.

## Failure and retention boundaries

Database calls use one-second child budgets. A database outage is reported with `event.persisted=false` and a complete redacted reception log; ordinary processing remains available. Queue rejection is retained as `enqueue_failed`. Locator or journal failures do not turn an otherwise successful outbound send into an API error.

Expired audits (30 days) and locations (90 days) are removed in batches of at most 1,000 each, at most once per active application/bot scope per hour. Cleanup is driven by subsequent writes, so expiration does not imply immediate physical deletion while a scope is idle or a backlog remains. Existing message/statistics/search data is never pruned by this code.

This change starts capturing new events and message locations after deployment. It cannot reconstruct the actor/action time of the historical event whose original payload was already truncated, or infer a group for a never-observed message when Feishu also denies access.

## Validation

The contract test `TestPostgresProvenanceInIsolatedRolledBackSchema` creates a unique schema inside one PostgreSQL transaction and rolls it back. It checks migration execution, journal idempotence, scoped lookup, recall monotonicity, conflicting groups, resolution updates and scoped retention. Set `BETAGO_EVENTSTORE_TEST_DSN` only in the test process environment; never commit it. Regression tests cover event capture before queue admission, original trace preservation, failed statistics span attributes, receipt-before-indexing ordering, malformed timestamps and recursive redaction.
