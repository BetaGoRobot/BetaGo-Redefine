# Conversation Run Observability and Evaluation Design

**Date:** 2026-07-21  
**Status:** Awaiting written-spec review

## 1. Background

The current conversation path has OpenTelemetry spans and a `msg_trace_logs` mapping from a Lark message ID to a trace ID. The `/trace` debug command can return Jaeger links for a quoted message or thread. This is useful for latency and error diagnosis, but it does not provide a durable, complete record of recent conversation inputs, outputs, and intermediate decisions.

In particular:

- LLM-related spans generally store only short previews rather than complete prompts and responses.
- Routing, context assembly, model turns, tool calls, and the final Lark delivery are not represented by one stable, queryable data model.
- Trace retention and sampling are observability concerns and cannot guarantee an evaluation dataset.
- There is no direct window for reviewing the latest N runs, applying human labels, or attaching future Judge results.

This design adds a dedicated conversation run ledger. The ledger is the source of truth for replayable observation and evaluation data. OpenTelemetry remains the source of truth for distributed timing and operational diagnosis; the two systems link through trace and span IDs.

## 2. Scope

### 2.1 Goals

- Treat one inbound user message as one conversation run.
- Preserve complete original text for input, assembled context, prompts, model output, tool arguments, tool results, and final delivered output.
- Reconstruct the ordered process from message receipt through routing, LLM turns, tool execution, and Lark delivery.
- Query the latest 20, 50, or 100 runs with useful filters.
- Provide a clear WebUI timeline for inspecting one run.
- Support versioned human evaluation immediately.
- Reserve a compatible structure for future automated Judge evaluations.
- Keep recorder failures from interrupting the user-facing message path.

### 2.2 Non-goals for the first release

- Robot-initiated messages and scheduled tasks.
- Prompt experiments, A/B testing, or dataset campaign management.
- Running an automated Judge.
- Full-text search across all stored prompts and outputs.
- Persisting individual streaming token chunks.
- Replacing Jaeger, OpenTelemetry, or existing message history storage.

## 3. Architectural Decision

Use a PostgreSQL-backed, append-oriented run ledger with three primary records:

1. `conversation_runs`: one row per inbound user-triggered execution.
2. `conversation_steps`: ordered process records belonging to a run.
3. `conversation_evaluations`: versioned human or automated evaluations belonging to a run.

The implementation is separated into three components:

- **Recorder:** creates and completes runs and appends steps from business boundaries.
- **Query service:** retrieves recent runs and assembles a detailed timeline.
- **Evaluation service:** writes human labels now and automated Judge results later.

OpenTelemetry IDs are correlation fields, not the primary storage mechanism. A WebUI detail view can navigate from a recorded run to Jaeger, while Jaeger attributes can carry `conversation.run_id` for navigation in the other direction.

## 4. Data Model

The exact PostgreSQL DDL will be delivered as an idempotent SQL file under `script/sql/` during implementation. The following model defines required semantics.

### 4.1 Conversation run

A run is created at the Lark message entry after the event has passed basic structural validation. It is finalized exactly once with a terminal status.

Required fields:

- Identity: `id`, `trace_id`, `source_message_id`, `chat_id`, `user_open_id`, and bot identity.
- Classification: source type fixed to inbound user message in the first release, chat type, selected route, and primary model.
- State: `running`, `succeeded`, `failed`, `skipped`, `timed_out`, or `abandoned`.
- Summary: complete original user input, complete final generated output, complete actual delivered output, step count, total duration, and error summary.
- Capture health: `capture_incomplete` plus a short capture error summary.
- Retention: `pinned` to exempt a run from automatic cleanup.
- Time: started, completed, and created/updated timestamps.

The generated output and delivered output are distinct. A model may succeed while Lark delivery fails, and the ledger must preserve that distinction.

### 4.2 Conversation step

Steps are append-oriented and have a stable order within a run.

Required fields:

- Identity and relation: `id`, `run_id`, optional `parent_step_id`, and `sequence_no`.
- Classification: `kind`, stable `name`, and status.
- Payload: JSONB `input`, JSONB `output`, and JSONB `metadata`.
- Correlation: `trace_id` and `span_id` where available.
- Capture metadata: truncation flags and original byte sizes for bounded fields.
- Timing: started, completed, duration, and created timestamp.
- Failure: error type and complete error message/structured error payload.

Initial step kinds are:

- `input`
- `routing`
- `context_build`
- `llm_request`
- `llm_response`
- `tool_call`
- `tool_result`
- `final_output`
- `delivery`
- `error`

Kinds are deliberately coarse and stable. The `name` and metadata fields carry implementation-specific detail without requiring schema changes.

Each LLM turn is represented separately. Tool calls and results are children of the turn that requested them. If another LLM turn follows a tool result, it receives its own request and response steps. Concurrent steps may overlap in wall-clock time but always have unique, monotonically allocated sequence numbers.

### 4.3 Conversation evaluation

Evaluations are immutable versions rather than an overwritten current value.

Required fields:

- Identity: `id`, `run_id`, evaluation version, and creation time.
- Source: `human` or `judge`.
- Human actor or Judge model/rule identity and version.
- Overall result: `good`, `acceptable`, or `bad`, with an optional numeric score for future Judge results.
- Multi-select problem tags.
- Structured dimension scores and complete rationale.
- Free-form notes.
- Supersession metadata to identify the current evaluation while retaining prior versions.

Initial human problem tags are:

- context error
- intent/routing error
- tool selection error
- tool result error
- factual error
- incomplete task
- format or tone issue
- system error

Tags use stable machine values and localized display labels.

### 4.4 Indexes

Indexes serve recent-run queries rather than full-text content search:

- run start time
- chat ID plus start time
- user open ID plus start time
- status plus start time
- primary model plus start time
- run ID plus step sequence number, unique within a run
- run ID plus evaluation creation time

Foreign keys use cascading deletion from a run to its steps and evaluations. Automatic retention always deletes an entire run, never isolated steps.

## 5. Capture Lifecycle

### 5.1 Run creation and context propagation

The Lark message entry creates the run before business routing and places a lightweight run session in `context.Context`. The session contains the run ID and recorder interface, while storage implementation details remain outside application code. Structurally invalid events, stale redeliveries rejected by the transport boundary, and messages sent by the bot itself are excluded because they are not user-triggered runs.

A run must also be recorded when processing ends through mute, rate limit, command routing, or another deliberate skip after run creation. The terminal status and skip reason distinguish an intentional decision from missing data.

Events that fail basic structural validation before a user/message identity can be established are logged operationally and do not create malformed run rows.

### 5.2 Instrumentation boundaries

Instrumentation is concentrated at stable boundaries:

- Message entry and message processor: raw event-derived input and routing decisions.
- Context builder: selected history, excluded/cut-off history, system prompt, persona, and final assembled request context.
- ARK DAL: the actual outbound model request and the aggregated response for every turn, including token usage and model identifiers.
- Tool dispatcher: tool name, complete arguments, complete result, duration, and error.
- Lark delivery layer: the actual text or card payload sent and the delivery response/error.

Business handlers do not construct database models directly. They depend on a narrow recorder API such as start/finish/fail step operations.

### 5.3 Streaming and large payloads

Streaming deltas are accumulated in memory by the existing response path and stored once per completed model turn. This avoids a row per token while retaining the complete response visible to the application.

Complete original content is allowed and is the default. A configurable hard byte limit, defaulting to 4 MiB per JSON payload field, protects PostgreSQL from pathological payloads. When a field exceeds the limit, truncation must be explicit: the row records the truncation flag and original byte size. Silent truncation is prohibited.

### 5.4 Completion and recovery

Run completion writes terminal status, generated output, delivered output, duration, primary model, step count, and error summary. Completion is idempotent so deferred error handling and normal completion cannot produce inconsistent terminal state.

A periodic sweeper marks `running` rows older than 15 minutes as `abandoned`. The threshold is configurable. This identifies process crashes and capture gaps instead of leaving ambiguous permanent in-flight records.

## 6. Failure Isolation and Observability

Recorder persistence is best-effort from the perspective of the user-facing path:

- Failure to create a run does not block message processing.
- Failure to append or finish a step does not block LLM/tool/delivery work.
- A successful run row is marked `capture_incomplete` when later capture operations fail.
- Recorder errors emit structured logs and metrics with run ID, operation, step kind, and error class.
- Recorder calls use bounded timeouts and do not launch unbounded goroutines.

Required metrics include run creation/finalization counts, step append failures, capture-incomplete counts, abandoned-run counts, recorder latency, and run status totals.

The run ID is attached to relevant OTel spans as `conversation.run_id`. Stored steps retain trace/span IDs when present.

## 7. Query and Evaluation API

The WebUI backend adds these endpoints:

- `GET /api/conversation-runs`
- `GET /api/conversation-runs/{runID}`
- `POST /api/conversation-runs/{runID}/evaluations`
- `PUT /api/conversation-runs/{runID}/pin`

Required behavior is:

- List recent runs with limit 20, 50, or 100 and cursor-based pagination.
- Filter by chat, user, bot, model, time, terminal status, presence of error, and evaluation result/state.
- Return run summaries without scanning all step payloads.
- Load a run with ordered steps and the current plus historical evaluations.
- Create a new human evaluation version without mutating history.
- Pin or unpin a run for retention.

Invalid limits and filters return clear 4xx errors. A missing run returns 404. Evaluation writes use existing WebUI bearer authentication.

Because complete conversation content is sensitive, these new read endpoints also require a configured bearer token. If no WebUI auth token is configured, the conversation observation endpoints remain disabled rather than inheriting the existing anonymous-GET behavior.

## 8. WebUI Observation Window

Add a dedicated conversation-runs view with a master/detail layout.

### 8.1 Recent runs list

The list defaults to the latest 50 runs and allows 20, 50, or 100. Each row shows:

- time, chat, and user
- input/output preview
- run status and capture-incomplete indicator
- model, duration, and step count
- human result and automated-evaluation state

Filters cover the query dimensions described above. Pagination retains filters in the URL so a review view can be shared and revisited.

### 8.2 Run detail

The detail header keeps the complete user input and actual delivered output visible with status, duration, and a Jaeger link. The process appears as an ordered timeline. Every step can expand to show:

- complete input and output
- formatted and raw JSON views
- start/end/duration
- parent-child relationship
- error data
- trace/span correlation
- explicit truncation metadata

The layout must remain readable on desktop and mobile. On small screens, the master/detail panes become a list-to-detail navigation flow and interactive targets remain touch-friendly.

### 8.3 Evaluation panel

The first release supports human overall result, multiple problem tags, and notes. It displays evaluator identity, timestamp, and historical versions. A reserved automated-evaluation section can later display total score, dimension scores, rationale, Judge identity, and version without changing the base page model.

## 9. Retention and Access Control

- Default retention is 30 days and is configurable.
- A scheduled cleanup deletes expired unpinned runs in batches of at most 500 by default; the batch size is configurable.
- Pinned runs and all of their steps/evaluations are retained.
- Cleanup emits deleted-run counts, failures, and duration metrics.
- Full conversation read access requires the WebUI bearer token.
- Evaluation and pin writes require the same token.
- Tokens and credentials must never be copied into recorder metadata by infrastructure wrappers; tool payloads are otherwise stored as provided because complete original data is an explicit requirement.

## 10. Testing and Acceptance Criteria

### 10.1 Unit and integration coverage

- Recorder lifecycle: create, append, finish, fail, idempotent completion, and capture-incomplete handling.
- Stable sequence allocation and parent-child relationships, including concurrent steps.
- Ordinary chat, multiple LLM/tool turns, command routing, deliberate skip, model failure, tool failure, and delivery failure.
- Complete aggregated streaming output with no per-token rows.
- Recorder storage failures do not change the user-facing business result.
- Stale running rows become abandoned.
- Recent-N queries, every filter, cursor pagination, ordered detail, and evaluation history.
- Retention cleanup respects pinned runs and batch limits.
- Read and write access control for complete conversation content.
- Desktop and mobile UI behavior for list, detail timeline, JSON payloads, and evaluation editing.

Tests follow repository rules: dependencies use explicit interfaces/fakes rather than mutable package-level function aliases.

### 10.2 Acceptance criteria

An operator can open the WebUI, select the latest 20, 50, or 100 inbound user runs, filter them, and inspect one complete process as:

`user input -> routing -> context -> LLM request -> LLM response -> tool calls/results -> subsequent turns -> final output -> Lark delivery`

The operator can label the run and later view prior label versions. The run links to its Jaeger trace. A recorder outage or individual persistence error does not prevent the bot from responding, and the system exposes the resulting capture gap through metrics/logs or `capture_incomplete`.

## 11. Delivery Sequence and Database Gate

Implementation must follow `script/AGENT_DB_CHANGE_SOP.md`:

1. Produce one idempotent SQL file under `script/sql/` containing all tables, indexes, constraints, and foreign keys for this feature.
2. Stop and ask the user to execute the SQL.
3. Ask the user to run `go run ./cmd/generate`.
4. Wait for confirmation that both steps completed.
5. Only then implement repositories, services, handlers, instrumentation, and UI against the generated model/query code.

This feature should be delivered incrementally after that gate: storage and recorder, capture boundaries, query/evaluation backend, WebUI, retention/recovery, then end-to-end verification.
