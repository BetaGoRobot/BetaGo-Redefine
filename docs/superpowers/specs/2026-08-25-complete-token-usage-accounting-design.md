# Complete Token Usage Accounting Design

## Goal

Make the WebUI Token totals represent every recorded model call owned by the
selected Bot, and preserve the usage detail returned by Ark instead of reducing
it to prompt, completion, and total values.

The design fixes two distinct problems:

1. The dashboard currently fetches stats for only the 20 highest-usage chats
   per Bot and labels their sum as the total. Calls from every other chat and
   calls without a `chat_id` are omitted.
2. Ark already returns cached-input, audio-input, audio-cached, reasoning, and
   embedding modality details, but the shared usage model drops them before
   aggregation and persistence.

## Confirmed Current Failures

- `webui/src/views/Dashboard.vue` limits stats loading to
  `MAX_CHATS_PER_BOT = 20`; every dashboard total and dimension is therefore a
  Top-20 sample rather than a Bot total.
- The only stats endpoint is chat-scoped. There is no server-side Bot-wide
  aggregate that can include background, system, debug, or reindex records
  whose `chat_id` is empty.
- `llmusage.Record`, `llmusage.Usage`, the database row, online metrics, API
  types, and frontend types retain only prompt, completion, and total tokens.
- Daily aggregation returns only total tokens, even though the dashboard lets
  callers choose prompt and completion metrics.
- Multimodal embedding conversion assigns `total_tokens` to
  `completion_tokens`, producing an internally inconsistent breakdown.
- Recorder errors are discarded at call sites. A failed database insert can
  remove a call from offline totals without an operational signal.

## Accounting Semantics

The existing public names remain for compatibility:

- `prompt_tokens` means Ark `input_tokens`.
- `completion_tokens` means Ark `output_tokens`.
- `total_tokens` is the canonical billed-token total for the recorded response.

New detail fields are:

- `cached_tokens`: cached input tokens; a subset of `prompt_tokens`.
- `reasoning_tokens`: reasoning output tokens; a subset of
  `completion_tokens`.
- `input_audio_tokens`: audio input tokens; a subset of `prompt_tokens`.
- `cached_audio_tokens`: cached audio input tokens; a subset of both
  `input_audio_tokens` and `cached_tokens`.
- `embedding_text_tokens`: text tokens reported for an embedding request.
- `embedding_image_tokens`: image tokens reported for an embedding request.

`uncached_prompt_tokens` is derived as
`max(prompt_tokens - cached_tokens, 0)` and is not persisted separately.
Cached and reasoning tokens are breakdowns, not extra consumption, so they are
never added to `total_tokens`.

For new records, canonical total is calculated as:

```text
max(provider_reported_total_tokens, prompt_tokens + completion_tokens)
```

The provider-reported value is also retained separately for audit. A mismatch
increments an anomaly metric; it is not silently hidden. This fallback prevents
a missing or partial provider total from understating consumption while
preserving a larger provider total if Ark introduces another billed category.

Embedding calls have prompt and total usage but no completion usage. Their
`completion_tokens` value is therefore zero. Text and image detail comes from
`MultimodalEmbeddingUsage.PromptTokensDetails`.

## Historical Detail Semantics

The new detail columns are nullable. `NULL` means the old row was written before
that detail was collected or the category is not applicable to that call; zero
means a detail-capable provider response returned a known zero. In particular,
embedding rows keep cache and reasoning fields `NULL` because those categories
are not part of embedding usage.

Cache ratios use only Responses rows where `cached_tokens IS NOT NULL`. API
aggregates include:

- `cache_observed_prompt_tokens`: prompt tokens from rows with known cache
  detail;
- `cache_observed_requests`: number of Responses rows with known cache detail;
- `cached_tokens` and derived `uncached_prompt_tokens` over that observed
  population.

This prevents historical rows from being presented as cache misses.

## Collection Architecture

`internal/infrastructure/llmusage` remains the single normalization and
recording boundary.

1. Add a complete `Usage` value shared by non-streaming responses, streaming
   responses, and embeddings.
2. Add one Ark Responses conversion helper that extracts input, cached, audio,
   output, reasoning, provider total, and canonical total.
3. Use that helper in both `recordResponseUsage` and every
   `response.completed` event.
4. Extend `TurnAccumulator` to add every detail while retaining response-ID
   deduplication across tool continuations.
5. Add one embedding conversion helper and use it from both normal embedding
   calls and the reindex batch path, eliminating the current hand-built partial
   records.
6. Audit direct Ark SDK calls so all billable Responses and embedding calls
   cross the same recorder boundary. Ark tokenization requests are excluded
   because they count input locally and do not produce model output usage.

An explicit `UsageObserved` state determines `usage_missing`; a zero total alone
does not prove usage was absent. If a stream ends without a completed usage
object, the call remains visible with `status=usage_missing` and zero recorded
tokens rather than fabricating a value.

## Persistence and Metrics

Create an idempotent migration under `script/sql/` that adds nullable detail
columns and a nullable `reported_total_tokens` column to
`betago.llm_token_usage_records`. Existing `total_tokens` remains the canonical
query and sorting column.

The online token counter gains these `token_type` values:

- `prompt`
- `prompt_cached`
- `prompt_uncached`
- `completion`
- `reasoning`
- `input_audio`
- `input_audio_cached`
- `embedding_text`
- `embedding_image`
- `total`

Recorder persistence failures are logged with safe structural fields only and
increment `betago_llm_usage_persistence_failures_total`. Prompt text, model
output, tool arguments, and tool output are never logged. Provider-total
mismatches increment `betago_llm_usage_total_mismatches_total`.

The main usage row must not be silently lost because an optional tool-detail
insert fails. Persistence keeps the usage row authoritative and reports any
tool-detail failure separately.

## Bot-Wide Stats API

Add `GET /api/stats?window=<Nd>` for a Bot-wide aggregate. The query:

- filters by the exact current `bot_id`;
- includes all `chat_id` values, including empty values;
- includes all statuses and source types in the requested time window;
- performs totals, dimensions, and daily aggregation in PostgreSQL;
- returns the complete token breakdown for totals, groups, and daily points.

The new endpoint does not use the historical `bot_id = ''` compatibility path.
Unattributed rows cannot safely be assigned when several Bots share a database,
and including them in every Bot response would double count them. Operators must
run the existing Bot-ID backfill for historical attribution. The response
reports the excluded unattributed row count and token total as completeness
metadata, but the dashboard does not add them to exact Bot totals.

The existing `GET /api/chats/{chatID}/stats` endpoint remains and uses the same
aggregation implementation with an additional chat filter. Existing JSON fields
remain backward compatible; new fields are additive.

## WebUI Data Flow

The dashboard makes one Bot-wide stats request per selected Bot and merges those
responses. It no longer fetches stats for the Top 20 chats to calculate totals,
trends, business attribution, model distribution, kind distribution, source
distribution, or status distribution.

The chat list remains responsible for chat ranking and discovery. A Top-N chat
limit may still be used for optional per-chat sparklines, but it cannot feed any
element labelled total.

The total card displays:

- canonical total;
- input, cached input, and uncached input;
- output and reasoning output;
- cache-detail coverage and usage-missing count.

Cache percentage is shown only when the cache-observed prompt denominator is
non-zero. Daily trend points and metric selectors receive the same complete
breakdown as totals and dimension groups.

## Error and Degradation Behavior

- A Bot-wide query failure is visible for that Bot and is not converted to a
  zero-valued successful result.
- Multi-Bot aggregation may show healthy Bots while naming failed Bot sources;
  failed sources are never silently included as zero.
- Database-unavailable stats retain the existing graceful degradation contract,
  but the response explicitly marks token stats unavailable.
- `usage_missing` records remain countable and visible even though their token
  amount is unknown.
- Historical rows with unknown detail remain part of canonical total while
  being excluded from cache-detail denominators.

## Testing Strategy

Implementation follows red-green-refactor.

Backend tests cover:

- Ark response conversion with cached, audio, reasoning, and mismatched total
  values;
- streaming aggregation across multiple completed response IDs;
- duplicate completed events not double counting any detail field;
- embedding text/image detail and zero completion tokens;
- recorder row mapping, metrics, persistence failure reporting, and preservation
  of the main usage row when tool-detail persistence fails;
- Bot-wide aggregation with more than 20 chats and records with empty
  `chat_id`;
- strict Bot isolation and explicit unattributed completeness metadata;
- complete totals, groups, and daily points;
- per-chat endpoint backward compatibility.

Frontend tests cover:

- one Bot-wide stats request per selected Bot;
- additive merging of every token field;
- no Top-20 sample contributing to total cards or global charts;
- cached/uncached and reasoning display semantics;
- visible partial failure and detail-coverage states.

Verification uses Go 1.26 with `-tags=custom_skip_vips`,
`BETAGO_CONFIG_PATH=.dev/config.toml`, and the active VS Code `-v` test flag.
WebUI verification runs the focused Vitest suite and production build.

## Database Change Gate

Repository policy requires the schema change to be staged first:

1. Add only the idempotent SQL file under `script/sql/`.
2. The user applies that SQL to the development database.
3. The user runs `go run ./cmd/generate` with the repository baseline.
4. After the generated model/query files are available, implementation and
   tests continue.

No generated GORM model is edited by hand before this gate is complete.

## Non-Goals

- Reconstructing cached or reasoning detail for historical rows.
- Estimating tokens for streams where Ark never returned usage.
- Adding cached or reasoning subsets to total and thereby double charging.
- Provider price or currency calculation.
- Assigning legacy empty-`bot_id` rows to a Bot without the explicit backfill.
