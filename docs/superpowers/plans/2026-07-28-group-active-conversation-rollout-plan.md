# Group Active Conversation Rollout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在保持现有单聊、命令、@回复和静默态意图识别行为兼容的前提下，为群聊增加单一激活 conversation、相关性路由、三层上下文、15 分钟闲置过期和可灰度的高参与 Agent turn。

**Architecture:** 在现有 message processor 前置一个 `ConversationRouteOperator`，把每条消息归类为 `legacy / runtime / observe`。显式激活先创建或续接群级唯一 active run；激活态相关消息进入 durable inbox，由 Conversation Runtime 生成 `observe_only / reply / act / wait / close` 决策；不相关消息只记录而不触发回复。PostgreSQL保存当前态，OpenSearch回放历史事件，现有 message history/chunking 作为远期语义补充。所有新路径受 bot、群和百分比开关保护，关闭时严格回到原行为。

**Tech Stack:** Go 1.25, PostgreSQL, OpenSearch, Ark Responses API, existing `xhandler` message pipeline, existing `runtime.Executor`, VictoriaMetrics, Go testing

---

## 0. Dependencies and boundaries

- Requires `docs/superpowers/plans/2026-07-28-conversation-callback-runtime-plan.md`.
- Requires the capture contracts from `docs/superpowers/plans/2026-07-28-conversation-parallel-evaluation-plan.md` before enabling automatic activation; the WebUI workbench is recommended but not a code dependency.
- This plan has no database schema change. Reuse the runtime tables and event projection added by the callback plan.
- Only group chats enter the new route. P2P remains on the existing `ReplyChatOperator`.
- Initial serving rollout supports explicit activation only: mention, reply-to-bot, and `/bb`.
- Ordinary-message automatic activation starts in `observe` shadow mode and may serve only after evaluation gates pass.
- One group has at most one active run. A new explicit topic may supersede the old run atomically; late events remain attached to their original run.
- Related user input refreshes the 15-minute active TTL. Unrelated ambient messages do not. A run in `waiting_user` follows interaction expiry rather than active TTL.
- Existing commands other than `/bb`, existing V1 card actions, moderation restrictions, private mode, repeat/reaction/word-reply features and message recording retain their current behavior.
- Candidate/shadow turns must never send messages or execute side-effecting tools.

## 1. File map

### Conversation domain

- Create `internal/application/lark/agentruntime/activation.go`: explicit/automatic activation policy and run supersession.
- Create `internal/application/lark/agentruntime/relevance.go`: active-topic relevance contract and deterministic bypasses.
- Create `internal/application/lark/agentruntime/message_router.go`: `legacy / runtime / observe` route decision.
- Create `internal/application/lark/agentruntime/context_composer.go`: current-run, recent-event and semantic-history composition.
- Create `internal/application/lark/agentruntime/turn.go`: Agent turn input/output and decision validation.
- Create `internal/application/lark/agentruntime/message_processor.go`: durable message step processing.
- Create `internal/application/lark/agentruntime/expiry.go`: active/waiting expiry transition.
- Modify `internal/application/lark/agentruntime/types.go`: activation source, topic and message decision types.
- Modify `internal/application/lark/agentruntime/coordinator.go`: active run acquire/supersede/refresh operations.
- Modify `internal/application/lark/agentruntime/store.go`: active-run lookup and expiry scan contracts.

### Message integration

- Create `internal/application/lark/messages/ops/conversation_route_op.go`: early route stage.
- Modify `internal/application/lark/messages/handler.go`: inject router and submitter, register route stage.
- Modify `internal/application/lark/messages/policy.go`: preserve recording while suppressing duplicate legacy chat stages.
- Modify `internal/application/lark/messages/ops/reply_chat_op.go`: explicit mention/reply activation bridge.
- Modify `internal/application/lark/messages/ops/command_op.go`: `/bb` activation bridge while other commands remain legacy.
- Modify `internal/application/lark/messages/ops/chat_op.go`: ambient messages honor route metadata.
- Create `internal/application/lark/messages/conversation_routing_test.go`.

### Context, generation and delivery

- Modify `internal/infrastructure/conversationindex/store.go`: bounded run-event lookup and topic search.
- Create `internal/application/lark/agentruntime/history_source.go`: adapter around existing history/chunk retrieval.
- Modify `internal/application/lark/handlers/chat_handler.go`: expose shared prompt/tool planning without direct delivery.
- Modify `internal/application/lark/handlers/two_phase_chat.go`: accept runtime context and return a delivery-neutral result.
- Modify `internal/application/lark/handlers/tools.go`: runtime tool registry and approval metadata.
- Reuse the callback plan's Agent continuation delivery adapter for text/card responses.

### Runtime and rollout wiring

- Modify `internal/infrastructure/agentstore/repository.go`: active-run CAS, refresh and expiry scan.
- Modify `cmd/larkrobot/bootstrap.go`: router, context composer, message/expiry workers and delivery adapter.
- Modify `internal/runtime/settings.go`: conversation message and expiry executor settings.
- Modify `internal/infrastructure/config/configs.go`: runtime rollout defaults and model budgets.
- Modify `internal/application/config/definitions.go`, `manager.go`, `accessor.go`: bot/chat/percentage feature flags.
- Modify `deploy/config.example.toml`: rollout examples.
- Modify `cmd/larkrobot/chat_metrics.go`: activation, routing, decision, latency and fallback metrics.

## Task 1: Define activation, relevance and route contracts

**Files:**
- Modify: `internal/application/lark/agentruntime/types.go`
- Create: `internal/application/lark/agentruntime/activation.go`
- Create: `internal/application/lark/agentruntime/relevance.go`
- Create: `internal/application/lark/agentruntime/message_router.go`
- Test: `internal/application/lark/agentruntime/message_router_test.go`

- [ ] **Step 1: Write failing table-driven route tests**

Cover at least:

| Chat/input state | Expected route | Activation |
|---|---|---|
| P2P | `legacy` | none |
| group, runtime disabled | `legacy` | none |
| group mention, no active run | `runtime` | explicit |
| group reply-to-bot, no active run | `runtime` | explicit |
| group `/bb`, no active run | `runtime` | explicit |
| group ordinary message, no active run, auto shadow enabled | `observe` | candidate only |
| group ordinary message, active run, relevant | `runtime` | refresh |
| group ordinary message, active run, unrelated | `observe` | no refresh |
| group non-`/bb` command | `legacy` | none |
| restricted group | `legacy` | none |

Use pure fakes for `ActiveRunReader`, `RelevanceDecider`, `RolloutPolicy` and `Clock`.

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
go test ./internal/application/lark/agentruntime -run 'TestMessageRouter|TestActivationPolicy|TestRelevancePolicy'
```

Expected: FAIL because the contracts and router do not exist.

- [ ] **Step 3: Add versioned domain types**

Define:

```go
type MessageRoute string

const (
    MessageRouteLegacy  MessageRoute = "legacy"
    MessageRouteRuntime MessageRoute = "runtime"
    MessageRouteObserve MessageRoute = "observe"
)

type ActivationSource string

const (
    ActivationMention  ActivationSource = "mention"
    ActivationReplyBot ActivationSource = "reply_bot"
    ActivationCommand  ActivationSource = "bb_command"
    ActivationAuto     ActivationSource = "auto"
)

type RouteDecision struct {
    SchemaVersion    string
    Route            MessageRoute
    RunID            string
    ActivationSource ActivationSource
    Relevant         bool
    RefreshTTL       bool
    ReasonCode       string
}
```

Also define `ActivationDecision` and `RelevanceDecision` with stable reason codes. Reject unknown enum values at boundaries instead of silently treating them as reply.

- [ ] **Step 4: Implement deterministic policy before model calls**

Order the checks:

1. invalid/P2P/feature-off/restricted → `legacy`;
2. non-`/bb` command → `legacy`;
3. mention, reply-to-bot or `/bb` → explicit `runtime`;
4. active run + direct reply to a runtime message → relevant `runtime`;
5. active run + lexical/topic relevance decision → `runtime` or `observe`;
6. no active run + auto-activation shadow → `observe`;
7. otherwise → `legacy`.

The classifier may return `unknown`; unknown ambient input must be `observe`, never a serving reply.

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./internal/application/lark/agentruntime -run 'TestMessageRouter|TestActivationPolicy|TestRelevancePolicy'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/agentruntime
git -c commit.gpgsign=false commit -m "feat: define group conversation routing policy"
```

## Task 2: Compose exact three-layer conversation context

**Files:**
- Create: `internal/application/lark/agentruntime/context_composer.go`
- Create: `internal/application/lark/agentruntime/history_source.go`
- Modify: `internal/infrastructure/conversationindex/store.go`
- Test: `internal/application/lark/agentruntime/context_composer_test.go`
- Test: `internal/infrastructure/conversationindex/store_test.go`

- [ ] **Step 1: Write failing context selection tests**

Assert that:

- layer 1 always includes run goal, state, pending interaction, capability outcomes and messages selected in the current run;
- layer 2 reads bounded projected run events from OpenSearch and degrades to PostgreSQL event reads when the index is unavailable;
- layer 3 performs semantic/history lookup only with remaining token budget;
- every candidate item records `source`, `source_id`, `selected`, `exclude_reason`, `token_count` and rank;
- duplicate message IDs are emitted once;
- content after the anchor message is never selected;
- total selected tokens never exceed the configured budget;
- a source outage is visible in `ContextSnapshot.DegradedSources` but does not fail the turn.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/infrastructure/conversationindex -run 'TestContextComposer|TestRunEventQuery'
```

Expected: FAIL on missing composer/query behavior.

- [ ] **Step 3: Implement the context source interfaces**

Use narrow contracts:

```go
type CurrentRunSource interface {
    LoadRunContext(ctx context.Context, runID string, anchorAt time.Time) (RunContext, error)
}

type RecentEventSource interface {
    QueryRunEvents(ctx context.Context, runID string, before time.Time, limit int) ([]ConversationEvent, error)
}

type SemanticHistorySource interface {
    SearchBefore(ctx context.Context, chatID, query string, before time.Time, limit int) ([]ContextItem, error)
}
```

Do not pass GORM or OpenSearch clients into the composer.

- [ ] **Step 4: Implement deterministic budgeting**

Reserve budgets in this order:

1. system and policy;
2. current run;
3. recent events;
4. semantic history.

Persist both selected and excluded candidates in the `ContextSnapshot` contract from the evaluation plan. Hash normalized content for stable diffing, but keep the original content in the protected evaluation projection.

- [ ] **Step 5: Implement bounded OpenSearch query and fallback**

Query the exact `agent_conversation_events` alias (initially pointing to `agent_conversation_events_v1`) by `run_id`, `occurred_at < anchor`, descending, with an explicit `size`. Never query a wildcard date index. Treat index disabled/not found/timeouts as a typed degraded error so the composer can use PostgreSQL.

- [ ] **Step 6: Run tests**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/infrastructure/conversationindex -run 'TestContextComposer|TestRunEventQuery'
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/application/lark/agentruntime/context_composer.go \
  internal/application/lark/agentruntime/context_composer_test.go \
  internal/application/lark/agentruntime/history_source.go \
  internal/infrastructure/conversationindex
git -c commit.gpgsign=false commit -m "feat: compose versioned conversation context"
```

## Task 3: Acquire one active run from explicit group interactions

**Files:**
- Modify: `internal/application/lark/agentruntime/coordinator.go`
- Modify: `internal/application/lark/agentruntime/store.go`
- Modify: `internal/infrastructure/agentstore/repository.go`
- Test: `internal/application/lark/agentruntime/coordinator_test.go`
- Test: `internal/infrastructure/agentstore/repository_test.go`

- [ ] **Step 1: Write failing state transition tests**

Cover:

- first explicit activation creates an active run;
- repeated activation for the same topic reuses the run;
- concurrent activations leave exactly one active run;
- explicit activation with a distinct topic atomically closes the old run as `superseded` and creates the new run;
- old-run callback/async events still resolve against the old run and do not reactivate it;
- a failed create rolls back supersession;
- ordinary messages cannot supersede a run in the explicit-only phase.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/infrastructure/agentstore -run 'Test.*ActiveRun|Test.*Supersede'
```

Expected: FAIL on missing CAS and supersession behavior.

- [ ] **Step 3: Add repository transaction methods**

Implement a single transaction that:

1. locks the group session row;
2. reads the current active run;
3. reuses, supersedes or creates according to the coordinator decision;
4. appends activation/supersession events and projection outbox rows;
5. commits before returning the run ID.

Use the existing uniqueness/CAS constraint established in the callback plan. Do not emulate exclusivity with process-local mutexes.

- [ ] **Step 4: Implement topic matching for explicit activation**

Use a stable topic fingerprint from normalized explicit input plus reply root. A model-produced topic title is metadata only; it must not control transactional identity. Make supersession threshold/config injectable and default conservatively to reuse on uncertainty.

- [ ] **Step 5: Run repository and coordinator tests**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/infrastructure/agentstore -run 'Test.*ActiveRun|Test.*Supersede'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/agentruntime internal/infrastructure/agentstore
git -c commit.gpgsign=false commit -m "feat: acquire one active conversation per group"
```

## Task 4: Insert the conversation route into the message pipeline

**Files:**
- Create: `internal/application/lark/messages/ops/conversation_route_op.go`
- Modify: `internal/application/lark/messages/handler.go`
- Modify: `internal/application/lark/messages/policy.go`
- Modify: `internal/application/lark/messages/ops/reply_chat_op.go`
- Modify: `internal/application/lark/messages/ops/command_op.go`
- Modify: `internal/application/lark/messages/ops/chat_op.go`
- Create: `internal/application/lark/messages/conversation_routing_test.go`
- Modify: `internal/application/lark/messages/handler_test.go`
- Modify: `internal/application/lark/messages/policy_test.go`

- [ ] **Step 1: Write failing compatibility and duplicate-suppression tests**

Assert:

- route stage runs before `ReplyChatOperator`, `CommandOperator`, `ChatMsgOperator` and `IntentRecognizeOperator`;
- `RecordMsgOperator` and reaction collection still run for all routes;
- a `runtime` message is submitted exactly once and legacy chat/reply stages are skipped;
- an `observe` message is captured exactly once and sends nothing;
- a `legacy` message executes the same stages as before;
- P2P mention and all non-`/bb` commands stay legacy;
- feature disabled produces no new repository or model calls;
- moderation-restricted groups cannot activate;
- `/bb` does not execute both the old command chat handler and the runtime turn.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/application/lark/messages -run 'TestConversationRouting|TestMessageStagePolicy|TestNewMessageProcessor'
```

Expected: FAIL because the route stage is absent.

- [ ] **Step 3: Add route metadata helpers**

Store only small identifiers in `xhandler.BaseMetaData` extras:

```text
conversation.route
conversation.run_id
conversation.activation_source
conversation.reason
```

Do not store message content or full snapshots in metadata.

- [ ] **Step 4: Register the route stage and dependencies**

Construct `ConversationRouteOperator` with injected `MessageRouter`, runtime submitter and observe sink. Make the chat/reply/intent stages depend on it. Update the stage filter so:

- record/reaction remain allowed;
- runtime and observe suppress only duplicate response-producing stages;
- legacy delegates to the existing policy unchanged.

If route evaluation returns a transient error, record a degraded metric and use legacy only for explicit interactions; ambient messages fail closed to no reply.

- [ ] **Step 5: Bridge mention, reply-to-bot and `/bb`**

Remove direct `handlers.Chat` delivery only when metadata route is `runtime`. Keep the existing code reachable for `legacy`. Ensure `/bb` parsing remains owned by the command system, while the runtime bridge receives normalized user text and command provenance.

- [ ] **Step 6: Run focused and package tests**

Run:

```bash
go test ./internal/application/lark/messages/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/application/lark/messages
git -c commit.gpgsign=false commit -m "feat: route group messages into conversation runtime"
```

## Task 5: Execute a full Agent turn with delivery-neutral decisions

**Files:**
- Create: `internal/application/lark/agentruntime/turn.go`
- Create: `internal/application/lark/agentruntime/message_processor.go`
- Modify: `internal/application/lark/handlers/chat_handler.go`
- Modify: `internal/application/lark/handlers/two_phase_chat.go`
- Modify: `internal/application/lark/handlers/tools.go`
- Test: `internal/application/lark/agentruntime/message_processor_test.go`
- Modify: `internal/application/lark/handlers/chat_handler_test.go`

- [ ] **Step 1: Write failing decision tests**

Cover:

- `observe_only` appends a decision event and sends nothing;
- `reply` sends one reply after durable completion;
- `act` with a read-only tool may complete and reply;
- `act` with a side-effecting tool creates a durable approval wait rather than executing immediately;
- `wait` creates one pending interaction and sends the associated card;
- `close` records final output and closes the run;
- malformed/unknown decisions downgrade to `observe_only`;
- duplicate message steps do not call the model, tool or sender twice;
- a delivery retry reuses the stored output and does not regenerate;
- Candidate/shadow mode rejects all senders and side-effect tools in code.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/application/lark/handlers -run 'TestMessageProcessor|TestRuntimeTurn'
```

Expected: FAIL on the missing runtime turn.

- [ ] **Step 3: Extract planning from delivery**

Refactor the existing chat handler into:

```go
type TurnPlanner interface {
    Plan(ctx context.Context, input TurnInput) (TurnResult, error)
}

type TurnDeliverer interface {
    Deliver(ctx context.Context, output DeliveryOutput) (DeliveryReceipt, error)
}
```

Keep the old handler as an adapter that calls planner then deliverer, preserving legacy behavior. The runtime calls the planner directly and persists the result before delivery.

- [ ] **Step 4: Validate the decision and tool policy in code**

The model may propose, but code decides:

- only the five supported decisions are accepted;
- tool names must exist in the runtime registry;
- `toolmeta.SideEffectLevelOf(name)` controls approval;
- at most one blocking interaction exists per run;
- moderation/private-mode checks run again at delivery time;
- closed/superseded/expired runs cannot originate a new proactive message.

- [ ] **Step 5: Implement durable step processing**

For each claimed message step:

1. load run and anchor event;
2. compose and persist the exact context snapshot;
3. call the planner;
4. persist validated decision/output/capability state;
5. complete the step transaction;
6. deliver or enqueue the interaction;
7. append delivery outcome and evaluation capture.

On model timeout, mark retryable with bounded exponential backoff. On policy rejection, complete as `observe_only`. Use the queue lease/dedupe behavior from the callback plan.

- [ ] **Step 6: Run tests**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/application/lark/handlers
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/application/lark/agentruntime internal/application/lark/handlers
git -c commit.gpgsign=false commit -m "feat: process durable group conversation turns"
```

## Task 6: Expire active and waiting conversations safely

**Files:**
- Create: `internal/application/lark/agentruntime/expiry.go`
- Modify: `internal/application/lark/agentruntime/coordinator.go`
- Modify: `internal/application/lark/agentruntime/store.go`
- Modify: `internal/infrastructure/agentstore/repository.go`
- Test: `internal/application/lark/agentruntime/expiry_test.go`
- Test: `internal/infrastructure/agentstore/repository_test.go`

- [ ] **Step 1: Write failing clock-controlled expiry tests**

Assert:

- related runtime input refreshes `last_relevant_at`;
- unrelated observe input does not refresh it;
- an active run expires after 15 minutes with no related input;
- a run at 14m59s remains active;
- `waiting_user` does not expire through active TTL;
- expired interaction transitions the waiting run according to its timeout policy;
- concurrent expiry and callback resolution has one legal winner;
- expiry is idempotent and does not send duplicate close messages;
- late async/callback events are recorded on the expired run without reopening it.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/infrastructure/agentstore -run 'Test.*Expiry|Test.*Refresh'
```

Expected: FAIL on missing expiry service.

- [ ] **Step 3: Implement compare-and-set expiry**

Scan in bounded pages and update only rows whose state/version still matches. Append `run.expired` or `interaction.expired` events in the same transaction. Return claim counts so the worker can expose lag.

- [ ] **Step 4: Resume the Agent from timeout events with a deterministic fallback**

Append timeout as a durable Agent input. The continuation may choose only `reply` or `close`; it cannot execute the expired capability or create another approval for it. Persist the timeout policy with the wait so model failure has a deterministic fallback:

- approval/confirm timeout → cancel capability, optionally send one concise expiry notice, close or resume according to the stored continuation policy;
- information request timeout → close silently unless the initiating card promised a notification.

The capability is cancelled before the continuation runs, so the Agent decides communication only, never whether the expired action should still execute.

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/infrastructure/agentstore -run 'Test.*Expiry|Test.*Refresh'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/agentruntime internal/infrastructure/agentstore
git -c commit.gpgsign=false commit -m "feat: expire inactive group conversations"
```

## Task 7: Wire flags, workers, metrics and fail-safe defaults

**Files:**
- Modify: `cmd/larkrobot/bootstrap.go`
- Modify: `cmd/larkrobot/bootstrap_test.go`
- Modify: `cmd/larkrobot/chat_metrics.go`
- Modify: `internal/runtime/settings.go`
- Modify: `internal/infrastructure/config/configs.go`
- Modify: `internal/application/config/definitions.go`
- Modify: `internal/application/config/manager.go`
- Modify: `internal/application/config/accessor.go`
- Modify: `deploy/config.example.toml`
- Test: `internal/application/config/manager_test.go`

- [ ] **Step 1: Write failing configuration and lifecycle tests**

Cover:

- defaults disable all new serving behavior;
- enabled chats must also be under an enabled bot;
- explicit activation percentage is deterministic by `chat_id`;
- auto activation has separate shadow and serving percentages;
- run-sticky assignment does not change after configuration reload;
- missing OpenSearch leaves runtime available in degraded mode;
- missing PostgreSQL/runtime worker prevents activation and leaves legacy behavior;
- bootstrap starts/stops message and expiry workers cleanly.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/application/config ./cmd/larkrobot -run 'Test.*Conversation|Test.*Bootstrap'
```

Expected: FAIL on missing configuration/wiring.

- [ ] **Step 3: Add independent rollout controls**

Include at least:

```toml
[conversation_runtime]
enabled = false
explicit_activation_percent = 0
auto_activation_shadow_percent = 0
auto_activation_serving_percent = 0
active_ttl = "15m"
message_workers = 2
expiry_interval = "30s"
context_token_budget = 12000
recent_event_limit = 100
semantic_history_limit = 20
```

Support bot allowlist, chat allowlist and denylist through the existing config manager conventions. Denylist wins.

- [ ] **Step 4: Wire runtime dependencies explicitly**

Bootstrap should construct:

`agentstore → conversation index/history sources → context composer → planner/deliverer → message processor → router → message handler`.

Avoid new package globals. Keep the existing `NewMessageProcessor` compatibility constructor by delegating to a new options-based constructor with runtime disabled.

- [ ] **Step 5: Add operational metrics**

Record low-cardinality counters/histograms:

- activations by source/result;
- route decisions by route/reason;
- active runs and waiting runs;
- message queue age, processing latency and retry count;
- decisions by type;
- relevance/activation classifier latency and errors;
- context tokens and degraded sources;
- delivery/tool/approval outcomes;
- expirations and supersessions;
- legacy fallback count.

Never use `chat_id`, `run_id`, message text or model output as metric labels.

- [ ] **Step 6: Run configuration and bootstrap tests**

Run:

```bash
go test ./internal/application/config ./cmd/larkrobot
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/larkrobot internal/runtime internal/infrastructure/config \
  internal/application/config deploy/config.example.toml
git -c commit.gpgsign=false commit -m "feat: wire conversation runtime rollout controls"
```

## Task 8: Capture automatic activation in shadow, then gate serving

**Files:**
- Modify: `internal/application/lark/agentruntime/activation.go`
- Modify: `internal/application/lark/agentruntime/message_router.go`
- Modify: `internal/application/lark/conversationeval/control_capture.go`
- Modify: `internal/application/lark/conversationeval/candidate.go`
- Modify: `internal/application/lark/conversationeval/service.go`
- Test: `internal/application/lark/agentruntime/auto_activation_test.go`
- Test: `internal/application/lark/conversationeval/service_test.go`

- [ ] **Step 1: Write failing shadow-safety and assignment tests**

Assert:

- auto activation shadow evaluates eligible ambient messages but never creates an active serving run;
- shadow records activation probability/reason, selected context, proposed decision and reply;
- serving eligibility is assigned once per cohort/run key and remains sticky;
- explicit activation always overrides auto assignment when explicit rollout is enabled;
- side-effect tools and delivery sinks are inaccessible in auto shadow;
- an auto-serving run is created only when all local rollout gates pass;
- disabling serving prevents new auto runs without terminating already active explicit runs.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/application/lark/conversationeval -run 'TestAutoActivation|Test.*Shadow.*Activation|Test.*Sticky'
```

Expected: FAIL on missing auto-activation capture.

- [ ] **Step 3: Reuse evaluation episodes**

For each sampled ambient anchor, capture:

- Control: current silent-state intent decision and actual output;
- Candidate: auto-activation decision, topic, context snapshot, Agent decision and proposed output;
- pre-window: 20 messages;
- post-window: topic boundary or 15 minutes or 50 messages;
- late feedback: 24 hours;
- actual serving lane and immutable assignment key.

Do not create a second evaluation format for activation.

- [ ] **Step 4: Enforce serving gates in code**

Auto activation may serve only when:

- global, bot and chat flags allow it;
- cohort assignment is serving and sticky;
- no active/waiting run exists;
- moderation/private-mode policy allows it;
- activation confidence exceeds the configured threshold;
- the decision is not `unknown`;
- system health gates are green.

Any failed gate becomes `observe` with a reason code.

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/application/lark/agentruntime ./internal/application/lark/conversationeval
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/agentruntime internal/application/lark/conversationeval
git -c commit.gpgsign=false commit -m "feat: evaluate automatic group activation in shadow"
```

## Task 9: Verify compatibility, safety and staged rollout

**Files:**
- Create: `internal/application/lark/agentruntime/integration_test.go`
- Modify: `internal/application/lark/messages/conversation_routing_test.go`
- Modify: `cmd/larkrobot/bootstrap_test.go`
- Modify: `docs/superpowers/specs/2026-07-28-group-conversation-runtime-design.md` only if implementation reveals a confirmed contract correction

- [ ] **Step 1: Add an end-to-end in-memory integration harness**

Exercise:

1. group mention activates a run;
2. relevant follow-up enters the same run without another mention;
3. unrelated ambient message is recorded but receives no reply;
4. a side-effect schedule edit reaches `waiting_user`;
5. callback continuation resumes and replies exactly once;
6. a later distinct explicit topic supersedes the run;
7. late callback remains on the old run;
8. inactivity expires the current run;
9. the next ambient message returns to legacy/silent behavior.

Run the same input with runtime disabled and assert existing legacy sender/tool call counts.

- [ ] **Step 2: Run targeted race and duplicate-delivery tests**

Run:

```bash
go test -race ./internal/application/lark/agentruntime ./internal/application/lark/messages ./internal/infrastructure/agentstore
```

Expected: PASS with no races.

- [ ] **Step 3: Run the repository verification suite**

Run:

```bash
go test ./internal/application/lark/... ./internal/infrastructure/agentstore/... ./internal/infrastructure/conversationindex/... ./cmd/larkrobot/...
go test ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 4: Perform a manual Lark smoke test in explicit-only mode**

With one allowlisted test group and `auto_activation_serving_percent = 0`, verify:

- mention and `/bb` activate;
- follow-up relevance behavior;
- unrelated-message silence;
- schedule confirmation callback continuation;
- 15-minute expiry using a temporarily shorter test-only config;
- current legacy commands, P2P, cards and moderation behavior.

Record run IDs and evaluation episode IDs in the release checklist, not in source control.

- [ ] **Step 5: Roll out through explicit gates**

Advance only one dimension at a time:

1. runtime installed, all percentages 0;
2. explicit activation 1% in allowlisted test chats;
3. explicit activation 10%, then 50%, then 100% after error/duplicate/latency checks;
4. automatic activation shadow 10%, then 100%;
5. review the parallel evaluation workbench for insertion quality, context quality, response quality and negative user feedback;
6. automatic serving 1% only after the agreed Judge/human thresholds pass;
7. expand serving with a documented rollback owner.

Rollback is setting the relevant serving percentage to 0. Existing queued callbacks continue to resolve, but no new active serving runs are created.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/agentruntime/integration_test.go \
  internal/application/lark/messages/conversation_routing_test.go \
  cmd/larkrobot/bootstrap_test.go
git -c commit.gpgsign=false commit -m "test: verify active conversation rollout safety"
```

## Completion criteria

- Feature-off behavior is byte-for-byte compatible at the routing boundary and equivalent in sender/tool call counts.
- P2P, non-`/bb` commands, V1 cards and restricted groups remain on legacy paths.
- A group has at most one active run under race.
- Explicit interactions activate reliably; related follow-ups participate without repeated mentions.
- Unrelated active-state messages are observable but do not provoke a reply or refresh TTL.
- Context snapshots identify every selected and excluded source item and fit the token budget.
- Every serving reply/action is attached to a durable step and can be evaluated against Control/Candidate.
- Side effects always pass through capability idempotency and approval policy.
- Callback and async results are Agent-consumed inputs, including after supersession/expiry, without reopening old runs.
- Automatic activation is measurable in shadow before it can serve.
- Rollback disables new serving immediately without discarding durable callback work.
