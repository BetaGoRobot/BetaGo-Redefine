# Reaction event provenance and diagnostics

The incident at 2026-10-30 15:56 Asia/Shanghai showed that a failed message lookup discarded the actor and action time before the statistics INSERT. The event preview was truncated before the event body, and metadata initialization created an unrelated trace. The user reviewed the proposed event journal, message locator and trace changes and asked to implement the improvement.

## Behavior

1. Record a reaction's event ID, message ID, actor type and IDs, reaction type, raw action timestamp, parsed UTC action time, receipt time and trace ID before resolving its group. Capture the complete recursively redacted event JSON, including original JSON fields when the SDK supplies a raw request. Keep these identity fields separately on logs and spans so payload size cannot hide them.
2. Keep a PostgreSQL message-to-chat locator scoped by application and bot identity. Observe inbound messages before background indexing and outbound messages as soon as their provider receipt is available. Respect private-mode exclusions. Store metadata, not message content. Recall events update the locator and have their own journal entry.
3. Resolve reaction metadata once, under the original context: locator first, then one bounded Feishu lookup. Successful lookups populate the locator. Preserve a known chat when the original message is no longer readable. Unknown messages remain explicitly unresolved; visibility errors never imply recall.
4. Both reaction stages use the resolved metadata. An unresolved event remains in the journal and logs, while group-dependent work is skipped. Existing successful interaction statistics and feedback remain intact.
5. Journal and locator writes use bounded child contexts, never include credentials, and report failures without blocking ordinary message handling. Journal records retain 30 days; locator metadata retains 90 days. Prune expired entries in bounded, occasional batches. Duplicate event IDs retain their original receipt and payload.
6. Additive schema migrations create only the two new tables and their indexes. No existing messages, statistics, indexes, dashboards, or unrelated containers are changed.

## Verification

Reproduce a message lookup returning 230050 and assert that actor, action time, message ID, event ID and the original trace are still available. Assert one remote lookup, no statistics write for an unresolved group, correct locally known group when remote visibility fails, application isolation, recall metadata capture, malformed-event handling and recursive credential redaction. Exercise real PostgreSQL persistence in an isolated temporary schema, including journal idempotence and locator updates. Use Go 1.26.8, `custom_skip_vips`, `-v`, and the existing main-workspace development configuration.

## Release boundary

Implement and build against the verified deployed master network-recovery snapshot (source digest `36fec7432a7b23997004ed938551a13ba6adb56202f5e90fe6496d418c5d1b1e`). Keep the change reviewable independently of existing live meme and network repairs. Deployment is a separate production action; verification fixtures must not send group messages or replay the old event to Lark.
