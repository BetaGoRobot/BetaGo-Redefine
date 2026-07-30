# Conversation Parallel Evaluation and Judge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 对同一时间段、同一批群消息并行采集 Control/Candidate 的话题切入、上下文选择、回复决策和回复内容，并结合前后向消息生成可版本化的自动 Judge 结果。

**Architecture:** Serving lane 保持唯一，shadow lane 通过独立 executor 和无副作用 sink 运行。每个决策锚点形成 Evaluation Episode，在线保存两条链路当时的 Context Snapshot，随后收集 topic/15 分钟/50 条后向窗口及 24 小时 late feedback。PostgreSQL保存 cohort/episode/版本状态，OpenSearch保存完整可检索快照。

**Tech Stack:** Go 1.25, PostgreSQL, GORM Gen, OpenSearch, Ark Responses API, existing message/reaction/card ingress, VictoriaMetrics, Go testing

---

## 0. Dependencies and boundaries

- Requires completion of `docs/superpowers/plans/2026-07-28-conversation-callback-runtime-plan.md`.
- The shared event/run/context version is `conversation.v1`.
- Initial serving lane is `control`; Candidate output is never sent to Lark.
- Candidate may call explicitly allowlisted read-only tools only. `toolmeta.SideEffectLevelOf(name) != none` is blocked in code.
- Post-window feedback is causal evidence only for the actual serving message.
- Task 1 is a mandatory database gate; stop after SQL until the user runs SQL and `go run ./cmd/generate`.

## 1. File map

- Create `script/sql/20260728_conversation_parallel_evaluation.sql`: cohort/episode/lane/feedback/judgment tables。
- Create `internal/application/lark/conversationeval/types.go`: versioned evaluation contracts。
- Create `internal/application/lark/conversationeval/service.go`: cohort and episode orchestration。
- Create `internal/application/lark/conversationeval/context_snapshot.go`: exact selected/excluded context。
- Create `internal/application/lark/conversationeval/control_capture.go`: serving control hooks。
- Create `internal/application/lark/conversationeval/candidate.go`: shadow candidate runner。
- Create `internal/application/lark/conversationeval/window.go`: pre/post/late feedback collection。
- Create `internal/application/lark/conversationeval/judge.go`: blind A/B Judge。
- Create `internal/application/lark/conversationeval/worker.go`: candidate/window/judge workers。
- Create `internal/infrastructure/evaluationstore/repository.go`: PostgreSQL repository。
- Create `internal/infrastructure/evaluationindex/store.go`: OpenSearch evaluation projection/query。
- Create `script/opensearch/agent_conversation_evaluations_v1.json`: mapping。
- Modify `internal/application/lark/handlers/chat_handler.go`: split plan building from execution and emit capture hooks。
- Modify `internal/application/lark/handlers/two_phase_chat.go`: same capture contract。
- Modify `internal/application/lark/handlers/tools.go`: explicit shadow read-only registry。
- Modify `internal/application/lark/messages/handler.go`: attach evaluation recorder。
- Modify `internal/application/lark/reaction/record_reaction.go`: feedback sink。
- Modify `internal/interfaces/lark/handler.go`: card feedback sink。
- Modify `cmd/larkrobot/bootstrap.go`, `internal/runtime/settings.go`, `internal/infrastructure/config/configs.go`: evaluation executors and lifecycle。

## Task 1: Add evaluation schema and stop at the DB gate

**Files:**
- Create: `script/sql/20260728_conversation_parallel_evaluation.sql`

- [ ] **Step 1: Write the complete migration**

Create these tables:

```sql
create table if not exists betago.evaluation_cohorts (
    id text primary key,
    app_id text not null,
    bot_open_id text not null,
    chat_ids jsonb not null default '[]'::jsonb,
    start_at timestamptz not null,
    end_at timestamptz not null,
    status text not null,
    serving_lane text not null,
    control_version text not null,
    candidate_version text not null,
    judge_config_json jsonb not null default '{}'::jsonb,
    sampling_policy_json jsonb not null default '{}'::jsonb,
    result_version bigint not null default 0,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists betago.evaluation_episodes (
    id text primary key,
    cohort_id text not null references betago.evaluation_cohorts(id) on delete cascade,
    chat_id text not null,
    run_id text not null default '',
    anchor_event_id text not null,
    anchor_message_id text not null,
    topic_id text not null default '',
    serving_lane text not null,
    status text not null,
    pre_window_start timestamptz not null,
    anchor_at timestamptz not null,
    post_window_end timestamptz null,
    late_feedback_until timestamptz not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (cohort_id, anchor_event_id)
);

create table if not exists betago.evaluation_lane_outputs (
    id text primary key,
    episode_id text not null references betago.evaluation_episodes(id) on delete cascade,
    lane text not null,
    output_mode text not null,
    activation_json jsonb not null default '{}'::jsonb,
    relevance_json jsonb not null default '{}'::jsonb,
    join_decision text not null,
    topic_relation text not null,
    context_snapshot_json jsonb not null default '{}'::jsonb,
    excluded_context_json jsonb not null default '[]'::jsonb,
    tool_plan_json jsonb not null default '{}'::jsonb,
    reply_text text not null default '',
    latency_ms bigint not null default 0,
    token_usage_json jsonb not null default '{}'::jsonb,
    error_json jsonb not null default '{}'::jsonb,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (episode_id, lane)
);

create table if not exists betago.evaluation_feedback (
    id text primary key,
    episode_id text not null references betago.evaluation_episodes(id) on delete cascade,
    target_lane text not null,
    target_message_id text not null default '',
    feedback_event_id text not null,
    feedback_type text not null,
    explicitness text not null,
    content_json jsonb not null default '{}'::jsonb,
    attribution_confidence integer not null,
    occurred_at timestamptz not null,
    created_at timestamptz not null default now(),
    unique (episode_id, feedback_event_id)
);

create table if not exists betago.evaluation_judgments (
    id text primary key,
    episode_id text not null references betago.evaluation_episodes(id) on delete cascade,
    version bigint not null,
    source text not null,
    evaluator_id text not null,
    winner text not null,
    scores_json jsonb not null default '{}'::jsonb,
    problem_tags_json jsonb not null default '[]'::jsonb,
    rationale text not null default '',
    confidence integer not null default 0,
    needs_review boolean not null default false,
    supersedes_id text not null default '',
    created_at timestamptz not null default now(),
    unique (episode_id, source, version)
);

create index if not exists idx_eval_cohort_time on betago.evaluation_cohorts (start_at, end_at);
create index if not exists idx_eval_episode_filter on betago.evaluation_episodes (cohort_id, chat_id, anchor_at desc);
create index if not exists idx_eval_episode_status on betago.evaluation_episodes (status, post_window_end);
create index if not exists idx_eval_feedback_message on betago.evaluation_feedback (target_message_id, occurred_at);
create index if not exists idx_eval_judgment_episode on betago.evaluation_judgments (episode_id, created_at desc);
```

- [ ] **Step 2: Validate and commit the SQL**

```bash
git diff --check -- script/sql/20260728_conversation_parallel_evaluation.sql
git add script/sql/20260728_conversation_parallel_evaluation.sql
git -c commit.gpgsign=false commit -m "db: add conversation evaluation schema"
```

- [ ] **Step 3: Stop**

Request that the user executes the SQL and then `go run ./cmd/generate`. Continue only after confirmation.

## Task 2: Define evaluation contracts and repository

**Files:**
- Create: `internal/application/lark/conversationeval/types.go`
- Create: `internal/application/lark/conversationeval/store.go`
- Create: `internal/infrastructure/evaluationstore/repository.go`
- Test: `internal/application/lark/conversationeval/types_test.go`
- Test: `internal/infrastructure/evaluationstore/repository_test.go`

- [ ] **Step 1: Write RED tests for lifecycle and versioning**

Test:

- cohort `collecting -> waiting_late_feedback -> finalized`;
- invalid reverse transition rejected;
- episode uniqueness by cohort + anchor;
- one lane output per lane;
- judgment version is append-only;
- feedback dedupes by episode + event.

- [ ] **Step 2: Define stable enums and snapshots**

```go
const SchemaVersion = "conversation.v1"

type Lane string
const (
    LaneControl Lane = "control"
    LaneCandidate Lane = "candidate"
)

type ContextItem struct {
    ID            string          `json:"id"`
    Source        string          `json:"source"`
    SourceID      string          `json:"source_id"`
    Kind          string          `json:"kind"`
    Content       string          `json:"content"`
    ContentHash   string          `json:"content_hash"`
    Score         float64         `json:"score,omitempty"`
    Rank          int             `json:"rank"`
    TokenCount    int             `json:"token_count"`
    Selected      bool            `json:"selected"`
    ExcludeReason string          `json:"exclude_reason,omitempty"`
    Metadata      json.RawMessage `json:"metadata,omitempty"`
}

type ExcludedContextItem struct {
    ContextItem
}

type ContextSnapshot struct {
    SchemaVersion   string        `json:"schema_version"`
    AnchorEventID   string        `json:"anchor_event_id"`
    Messages        []ContextItem `json:"messages"`
    Retrieved       []ContextItem `json:"retrieved"`
    Events          []ContextItem `json:"events"`
    SystemPrompt    string        `json:"system_prompt"`
    UserPrompt      string        `json:"user_prompt"`
    TokenEstimate   int           `json:"token_estimate"`
    TokenBudget     int           `json:"token_budget"`
    Truncated       bool          `json:"truncated"`
    DegradedSources []string      `json:"degraded_sources,omitempty"`
}
```

Validate that selected item token counts fit `TokenBudget`, excluded items carry a non-empty reason, IDs are unique per source, and all items precede or equal the anchor. This shared shape is used by both the existing Control capture and the later Conversation Context Composer.

- [ ] **Step 3: Implement repository methods**

Required interface:

```go
type Store interface {
    CreateCohort(context.Context, Cohort) error
    ActiveCohorts(context.Context, string, time.Time) ([]Cohort, error)
    GetOrCreateEpisode(context.Context, Episode) (*Episode, error)
    UpsertLaneOutput(context.Context, LaneOutput) error
    AppendFeedback(context.Context, Feedback) error
    AppendJudgment(context.Context, Judgment) error
    EpisodesReadyForJudge(context.Context, time.Time, int) ([]Episode, error)
    TransitionCohorts(context.Context, time.Time) (int64, error)
}
```

- [ ] **Step 4: Run and commit**

```bash
go test ./internal/application/lark/conversationeval ./internal/infrastructure/evaluationstore
git add internal/application/lark/conversationeval internal/infrastructure/evaluationstore
git -c commit.gpgsign=false commit -m "feat: add evaluation cohort and episode store"
```

## Task 3: Capture the exact Control context and output

**Files:**
- Create: `internal/application/lark/conversationeval/control_capture.go`
- Create: `internal/application/lark/conversationeval/context.go`
- Modify: `internal/application/lark/handlers/chat_handler.go`
- Modify: `internal/application/lark/handlers/two_phase_chat.go`
- Test: `internal/application/lark/handlers/chat_handler_test.go`
- Test: `internal/application/lark/conversationeval/control_capture_test.go`

- [ ] **Step 1: Write a failing context-capture test**

Assert the captured snapshot contains exact ordered history, retrieved chunk IDs/content, system/user prompt, excluded message IDs/reasons, final decision/reply, latency and token usage.

- [ ] **Step 2: Introduce a request-scoped capture interface**

```go
type Capture interface {
    RecordIntent(context.Context, any)
    RecordContext(context.Context, ContextSnapshot, []ExcludedContextItem)
    RecordToolPlan(context.Context, any)
    RecordOutput(context.Context, Output)
    RecordDelivery(context.Context, string)
}
```

Provide `WithCapture` and `FromContext`; nil capture is a no-op.

- [ ] **Step 3: Split plan building from model execution**

Create a concrete value in `handlers/chat_handler.go`:

```go
type StandardChatPlan struct {
    ModelID        string
    SystemPrompt   string
    UserPrompt     string
    HistoryItems   []conversationeval.ContextItem
    RetrievedItems []conversationeval.ContextItem
    ExcludedItems  []conversationeval.ExcludedContextItem
    Files          []string
}
```

`buildStandardChatPlan` performs current context work. `executeStandardChatPlan` performs the current Ark call. Preserve output behavior byte-for-byte when no capture exists.

- [ ] **Step 4: Capture Control at stable boundaries**

- intent operator records sanitized intent;
- plan builder records exact context before Ark;
- tool calls record name/arguments/output source;
- final stream aggregation records `reply|skip`;
- delivery records actual Lark message ID.

- [ ] **Step 5: Run regression and commit**

```bash
go test ./internal/application/lark/handlers ./internal/application/lark/messages/ops ./internal/application/lark/conversationeval
git add internal/application/lark/handlers internal/application/lark/messages/ops internal/application/lark/conversationeval
git -c commit.gpgsign=false commit -m "feat: capture control conversation artifacts"
```

## Task 4: Run Candidate in a side-effect-free shadow sink

**Files:**
- Create: `internal/application/lark/conversationeval/candidate.go`
- Create: `internal/application/lark/conversationeval/shadow_tools.go`
- Modify: `internal/application/lark/handlers/tools.go`
- Test: `internal/application/lark/conversationeval/candidate_test.go`
- Test: `internal/application/lark/handlers/tools_test.go`

- [ ] **Step 1: Write safety tests first**

Iterate every Candidate tool and assert:

```go
if toolmeta.SideEffectLevelOf(name) != toolmeta.SideEffectLevelNone {
    t.Fatalf("shadow tool %q has side effects", name)
}
```

Also inject fake Lark sender/external writer and assert zero calls.

- [ ] **Step 2: Build an explicit allowlist**

The first Candidate registry contains only read-only capabilities whose handlers return data rather than cards/messages:

```text
search_history
finance_tool_discover
finance_market_data
finance_news
economy_indicator
get_chat_members
get_recent_active_members
```

If any currently reports a write side effect, exclude it until its read-only contract is corrected; never override metadata to force inclusion.

- [ ] **Step 3: Implement CandidateRunner**

```go
type CandidateRunner interface {
    Run(context.Context, CandidateRequest) (LaneOutput, error)
}
```

It receives the same anchor/pre-window snapshot, applies activation/relevance/context composition, calls Ark with the safe registry, returns a draft, and never calls Lark delivery.

- [ ] **Step 4: Reuse time-sensitive observations**

Add an `ObservationCache` keyed by episode + tool name + canonical args. Control writes captured read-only outputs; Candidate reads them first and marks `replayed_from_control=true`.

- [ ] **Step 5: Run and commit**

```bash
go test -race ./internal/application/lark/conversationeval ./internal/application/lark/handlers
git add internal/application/lark/conversationeval internal/application/lark/handlers
git -c commit.gpgsign=false commit -m "feat: run side-effect-free candidate lane"
```

## Task 5: Create episodes and collect pre/post windows

**Files:**
- Create: `internal/application/lark/conversationeval/service.go`
- Create: `internal/application/lark/conversationeval/window.go`
- Create: `internal/application/lark/conversationeval/sampling.go`
- Test: `internal/application/lark/conversationeval/window_test.go`
- Test: `internal/application/lark/conversationeval/sampling_test.go`

- [ ] **Step 1: Write table-driven boundary tests**

Cover:

- pre-window exactly last 20 messages;
- post closes at topic boundary;
- otherwise closes at 15 minutes;
- otherwise closes at 50 messages;
- explicit feedback attaches until 24 hours;
- ordinary feedback after 24 hours is rejected;
- later feedback creates a new aggregate result version after cohort finalization.

- [ ] **Step 2: Implement high-value sampling**

Always keep disagreements, context diff threshold breaches, tool/wait, supersede, feedback and errors. Hash `(cohort_id, anchor_event_id)` for deterministic sampling of agree+skip traffic.

- [ ] **Step 3: Implement episode orchestration**

For every active cohort:

1. get/create episode;
2. store Control output;
3. submit Candidate task;
4. append subsequent messages until a close condition;
5. mark `ready_for_judge` when both lanes and post-window exist.

- [ ] **Step 4: Run and commit**

```bash
go test ./internal/application/lark/conversationeval
git add internal/application/lark/conversationeval
git -c commit.gpgsign=false commit -m "feat: collect evaluation episodes"
```

## Task 6: Attach direct replies, reactions, corrections, and card feedback

**Files:**
- Create: `internal/application/lark/conversationeval/feedback.go`
- Modify: `internal/application/lark/messages/handler.go`
- Modify: `internal/application/lark/reaction/record_reaction.go`
- Modify: `internal/interfaces/lark/handler.go`
- Test: `internal/application/lark/conversationeval/feedback_test.go`
- Test: `internal/application/lark/reaction/base_test.go`
- Test: `internal/interfaces/lark/handler_test.go`

- [ ] **Step 1: Write attribution-priority tests**

Priority must be direct reply, reaction, card click, same-thread explicit correction, semantic/time inference.

- [ ] **Step 2: Add an injected FeedbackSink**

```go
type FeedbackSink interface {
    ObserveMessage(context.Context, MessageFeedback) error
    ObserveReaction(context.Context, ReactionFeedback) error
    ObserveCardAction(context.Context, CardFeedback) error
}
```

Inject through constructors; do not create mutable global aliases.

- [ ] **Step 3: Preserve serving-only causality**

Resolve `target_message_id` against Control/Candidate lane outputs. If it has no actual delivery ID, store feedback as episode context only and leave `target_lane` empty.

- [ ] **Step 4: Run and commit**

```bash
go test ./internal/application/lark/conversationeval ./internal/application/lark/reaction ./internal/interfaces/lark
git add internal/application/lark/conversationeval internal/application/lark/messages internal/application/lark/reaction internal/interfaces/lark
git -c commit.gpgsign=false commit -m "feat: attribute user feedback to evaluation episodes"
```

## Task 7: Implement blind pairwise Judge

**Files:**
- Create: `internal/application/lark/conversationeval/judge.go`
- Create: `internal/application/lark/conversationeval/judge_prompt.go`
- Test: `internal/application/lark/conversationeval/judge_test.go`

- [ ] **Step 1: Write RED tests**

Assert:

- A/B order is deterministic-random by episode/version;
- prompt contains no `control` or `candidate` labels;
- skip vs reply is represented without a fake reply;
- post-window is labeled as serving-lane outcome only;
- malformed Judge JSON fails without writing a judgment.

- [ ] **Step 2: Define Judge output**

```go
type JudgeResult struct {
    Winner      string         `json:"winner"` // A|B|tie
    ScoresA     DimensionScore `json:"scores_a"`
    ScoresB     DimensionScore `json:"scores_b"`
    ProblemTags []string       `json:"problem_tags"`
    Rationale   string         `json:"rationale"`
    Confidence  int            `json:"confidence"`
    NeedsReview bool           `json:"needs_review"`
}
```

Dimensions: participation timing, topic relation, context correctness, response relevance, task progress, factual/tool consistency, group tone, disturbance.

- [ ] **Step 3: Call Ark with strict JSON output**

Use a dedicated source label `conversation_evaluation_judge`, minimal stored prompt previews in logs, complete result in PostgreSQL, and configurable model ID.

- [ ] **Step 4: Append versioned judgments**

Never update an old judgment. Increment version and set `supersedes_id`.

- [ ] **Step 5: Run and commit**

```bash
go test ./internal/application/lark/conversationeval -run 'TestJudge'
git add internal/application/lark/conversationeval
git -c commit.gpgsign=false commit -m "feat: judge control and candidate replies"
```

## Task 8: Project searchable evaluation snapshots to OpenSearch

**Files:**
- Create: `script/opensearch/agent_conversation_evaluations_v1.json`
- Create: `internal/infrastructure/evaluationindex/store.go`
- Test: `internal/infrastructure/evaluationindex/store_test.go`
- Modify: `deploy/README.md`

- [ ] **Step 1: Add mapping**

Use keyword IDs/lane/status/topic relation, dates for window boundaries, text fields for messages/replies/rationale, and disabled objects for full snapshots.

- [ ] **Step 2: Implement exact upsert/query**

Use `opensearch.UpsertData` from Plan A. Document ID is episode ID; one document contains both lane snapshots and latest judgments.

- [ ] **Step 3: Add time/chat/cohort filters**

Repository query must support:

```go
type EpisodeFilter struct {
    CohortID string
    ChatID string
    From, To time.Time
    Disagreement string
    FeedbackType string
    NeedsReview *bool
}
```

- [ ] **Step 4: Test, document index setup, and commit**

```bash
go test ./internal/infrastructure/evaluationindex
git add script/opensearch internal/infrastructure/evaluationindex deploy/README.md
git -c commit.gpgsign=false commit -m "feat: index conversation evaluations"
```

## Task 9: Wire workers, budgets, flags, and metrics

**Files:**
- Modify: `internal/infrastructure/config/configs.go`
- Modify: `internal/runtime/settings.go`
- Modify: `internal/application/config/manager.go`
- Modify: `internal/application/config/definitions.go`
- Modify: `internal/application/config/accessor.go`
- Modify: `cmd/larkrobot/bootstrap.go`
- Modify: `deploy/config.example.toml`
- Test: `internal/runtime/settings_test.go`
- Test: `cmd/larkrobot/bootstrap_test.go`

- [ ] **Step 1: Add defaults**

```text
candidate executor: 2 workers, queue 64, timeout 2m
judge executor: 1 worker, queue 64, timeout 2m
window collector: every 30s
parallel evaluation flag: false
serving lane: control
pre=20, post=50, post TTL=15m, late=24h
```

- [ ] **Step 2: Add metrics**

Expose cohort/episode totals, lane error/latency/token, join/topic agreement, shadow safety blocks, Judge backlog, win/tie/loss, late feedback and projection backlog.

- [ ] **Step 3: Wire observer and workers**

Attach capture context before message processing; submit Candidate only after the anchor snapshot is durable; start window/Judge workers as non-critical runtime modules.

- [ ] **Step 4: Run and commit**

```bash
BETAGO_CONFIG_PATH=.dev/config.toml go test ./internal/runtime ./internal/application/config ./cmd/larkrobot
git add internal/infrastructure/config internal/runtime internal/application/config cmd/larkrobot deploy/config.example.toml
git -c commit.gpgsign=false commit -m "feat: wire parallel conversation evaluation"
```

## Task 10: Verify shadow safety and evaluation completeness

**Files:**
- Create: `internal/application/lark/conversationeval/e2e_test.go`
- Create: `docs/operations/conversation-parallel-evaluation.md`

- [ ] **Step 1: Add an end-to-end evaluation test**

Drive 25 pre messages, one anchor, two lane outputs, 10 post messages, a direct correction and reaction. Assert correct 20-message pre-window, both snapshots, serving-only feedback attribution and one blind judgment.

- [ ] **Step 2: Add a hard shadow-safety test**

Inject panicking Lark/external writers and run Candidate through every registered tool plan. The test passes only if no writer is invoked.

- [ ] **Step 3: Run verification**

```bash
go test -race \
  ./internal/application/lark/conversationeval \
  ./internal/infrastructure/evaluationstore \
  ./internal/infrastructure/evaluationindex \
  ./internal/application/lark/handlers \
  ./internal/application/lark/reaction \
  ./internal/interfaces/lark \
  ./cmd/larkrobot
```

Expected: PASS.

- [ ] **Step 4: Document cohort operation**

Document creation by chat/time, transition states, 24-hour finalization, rejudge versioning, serving/shadow causal labels and emergency flag disable.

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/conversationeval/e2e_test.go docs/operations/conversation-parallel-evaluation.md
git -c commit.gpgsign=false commit -m "test: verify parallel conversation evaluation"
```

## Completion gate

- A time/chat cohort produces aligned Control/Candidate episodes.
- Exact selected/excluded contexts are inspectable.
- Pre/post/late windows follow configured boundaries.
- Direct user comments and reactions attach to the delivered serving reply.
- Judge output is blind, versioned, and distinguishes offline quality from serving outcome.
- Candidate makes zero real writes or Lark deliveries.
- Evaluation overload cannot block serving message workers.
