# Agent Runtime Pending Sweep And Metrics Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `pending initial run` execution correct under missed/early-consumed wakeups by adding a level-driven sweep backstop, and add observability so operators can see queue health, wakeup flow, retry behavior, and stuck scopes.

**Architecture:** Keep the current Redis-backed per-scope FIFO as the source of truth for pending work. Preserve the existing `BLPop` wakeup queue for low-latency response, but add a background sweep worker that periodically scans scopes with pending work and reschedules eligible scopes when wakeups are missed or consumed too early. Observability is split into two layers: lightweight structured `Stats()` fields surfaced in `/statusz`, and scrape-friendly time-series metrics exposed from the management HTTP server.

**Tech Stack:** Go 1.25, Redis (`go-redis/v9`), existing runtime health/status HTTP module, agent runtime coordinator/worker infrastructure, optional Prometheus text exposition on management HTTP.

---

## Overview

Current behavior is edge-triggered:

- enqueue pending run -> `NotifyPendingInitialRun(...)`
- active slot released -> `NotifyPendingInitialRun(...)`
- worker `BLPop`s one scope and tries once

This is not sufficient for correctness. A wakeup can be consumed while the scope still has an active slot, or a worker can consume a wakeup and exit before work becomes runnable. The pending list still contains the source-of-truth item, but no future event is guaranteed to arrive. The system then appears stuck even though the slot is eventually free.

The fix is to move correctness to a level-driven model:

- the Redis pending run list remains the only source of truth
- wakeup queue remains only a fast-path hint
- a scope index tracks which `chat + actor` pairs still have pending work
- a background sweep periodically scans indexed scopes and reschedules runnable scopes

Observability must answer:

- how many pending scopes and pending runs exist right now
- how often wakeups are emitted, consumed, retried, or swept
- how often scopes are skipped because a slot is still occupied
- how long a pending run waits before it actually starts
- whether any scope is stuck with pending work but no recent progress

## File Map

**Existing files likely to modify**

- `internal/infrastructure/redis/agentruntime.go`
  Responsibility: Redis persistence for active slots, pending initial FIFO, wakeup queue, locks, and new scope index helpers.
- `internal/infrastructure/redis/agentruntime_test.go`
  Responsibility: Redis-level tests for pending scope index, scope scheduling, and queue visibility.
- `internal/application/lark/agentruntime/initial_run_queue.go`
  Responsibility: pending run payload shape; add fields needed for queue latency metrics if missing.
- `internal/application/lark/agentruntime/initial_run_worker.go`
  Responsibility: wakeup-driven scope processing; extend to cooperate with scope index and metrics.
- `internal/application/lark/agentruntime/initial_run_worker_test.go`
  Responsibility: worker behavior tests for wakeup, retry, and sweep interactions.
- `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
  Responsibility: build/store adapters and worker construction; wire a new sweep worker and shared metrics surface.
- `cmd/larkrobot/bootstrap.go`
  Responsibility: process lifecycle registration; start/stop stats reporting for the new sweep worker and metrics exporter.
- `internal/runtime/health_http.go`
  Responsibility: management HTTP server; add `/metrics` if this phase includes scrape output.

**New files recommended**

- `internal/application/lark/agentruntime/pending_scope_sweeper.go`
  Responsibility: background level-driven sweeper over indexed pending scopes.
- `internal/application/lark/agentruntime/pending_scope_sweeper_test.go`
  Responsibility: sweep loop tests, especially “wakeup lost but sweep eventually schedules”.
- `internal/application/lark/agentruntime/metrics.go`
  Responsibility: small, explicit runtime metrics collector for pending queue and worker lifecycle counters.
- `internal/application/lark/agentruntime/metrics_test.go`
  Responsibility: unit tests for metric counters/snapshots if a shared collector struct is introduced.
- `docs/architecture/agent-runtime-progress.md`
  Responsibility: architecture record for “wakeup is fast-path only, sweep is correctness backstop” and metric semantics.

## Sprint 1: Correctness Backstop For Pending Initial Runs

**Goal:** Pending runs eventually start once their actor slot is free, even if wakeups are missed, consumed too early, or worker timing races occur.

**Demo/Validation:**

- Start one active run for `chat + actor`.
- Queue a second pending run for the same `chat + actor`.
- Let the active run finish without sending any extra user message.
- Verify the pending run starts automatically.
- Simulate lost or early-consumed wakeup and verify the sweep still starts the pending run.

### Task 1: Add Redis Scope Index As Sweep Source Of Truth

**Files:**

- Modify: `internal/infrastructure/redis/agentruntime.go`
- Test: `internal/infrastructure/redis/agentruntime_test.go`

- [ ] **Step 1: Write failing Redis tests for indexed pending scopes**

Add tests that cover:

- enqueue pending run -> scope appears in a pending scope index
- consuming the last pending run -> scope can be removed from the index
- scope can be listed for sweep without scanning all chats
- repeated notifications do not affect FIFO correctness

- [ ] **Step 2: Run Redis tests to verify they fail**

Run:

```bash
env GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml go test ./internal/infrastructure/redis -run 'TestAgentRuntimePending.*' -count=1
```

Expected: missing methods / failing assertions for pending scope index behavior.

- [ ] **Step 3: Implement pending scope index helpers**

Add Redis helpers such as:

- `MarkPendingInitialScope(ctx, chatID, actorOpenID) error`
- `ClearPendingInitialScopeIfEmpty(ctx, chatID, actorOpenID) error`
- `ListPendingInitialScopes(ctx, cursor, count) ([]PendingInitialScope, uint64, error)`
- `PendingInitialRunCount(ctx, chatID, actorOpenID) (int64, error)`

Design rules:

- enqueue path marks the scope index unconditionally after successful list push
- worker clears the scope only when the scope FIFO is empty
- index key is bot-scoped, same as existing Redis keys

- [ ] **Step 4: Run Redis tests to verify they pass**

Run the same command from Step 2.

- [ ] **Step 5: Commit**

```bash
git add internal/infrastructure/redis/agentruntime.go internal/infrastructure/redis/agentruntime_test.go
git commit -m "feat: index pending initial scopes in redis"
```

### Task 2: Refactor Wakeup Semantics To Use Indexed Scopes

**Files:**

- Modify: `internal/application/lark/agentruntime/initial_run_worker.go`
- Modify: `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- Test: `internal/application/lark/agentruntime/initial_run_worker_test.go`

- [ ] **Step 1: Write failing worker tests for wakeup semantics**

Add tests for:

- wakeup consumed while slot is still busy does not lose the pending scope forever
- worker clears indexed scope only when the pending FIFO is empty
- repeated wakeups for the same scope do not duplicate run execution

- [ ] **Step 2: Run worker tests to verify they fail**

Run:

```bash
env GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'TestPendingInitialRunWorker.*' -count=1
```

Expected: failures around missing scope index coordination or lost wakeup recovery.

- [ ] **Step 3: Update worker/store interfaces**

Extend the pending initial store interface with explicit index-aware methods needed by the worker:

- `PendingInitialRunCount(...)`
- `MarkPendingInitialScope(...)`
- `ClearPendingInitialScopeIfEmpty(...)`

Keep `NotifyPendingInitialRun(...)` as the fast-path wakeup emitter.

- [ ] **Step 4: Implement index-aware worker behavior**

Adjust `handleScope(...)` so that:

- if slot is still occupied, it leaves the scope indexed and simply returns
- if no pending item remains, it clears the scope index
- if a pending item is requeued, the scope stays indexed
- successful processing of one item reschedules the scope if more items remain

- [ ] **Step 5: Run worker tests to verify they pass**

Run the same command from Step 2.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/agentruntime/initial_run_worker.go internal/application/lark/agentruntime/initial_run_worker_test.go internal/application/lark/agentruntime/runtimewire/runtimewire.go
git commit -m "refactor: make pending initial worker scope-index aware"
```

### Task 3: Add Background Pending Scope Sweep Worker

**Files:**

- Create: `internal/application/lark/agentruntime/pending_scope_sweeper.go`
- Create: `internal/application/lark/agentruntime/pending_scope_sweeper_test.go`
- Modify: `internal/application/lark/agentruntime/runtimewire/runtimewire.go`
- Modify: `cmd/larkrobot/bootstrap.go`

- [ ] **Step 1: Write failing sweep tests**

Add tests that cover:

- pending scope remains indexed but has no wakeup -> sweep reschedules it
- active slot busy -> sweep skips the scope without removing it
- empty pending FIFO -> sweep removes stale scope index entry
- multiple sweeps on same scope do not start duplicate runs because worker lock/slot guard still applies

- [ ] **Step 2: Run sweep tests to verify they fail**

Run:

```bash
env GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'TestPendingScopeSweeper.*' -count=1
```

Expected: missing sweep worker implementation.

- [ ] **Step 3: Implement the sweep worker**

Create a small background worker that:

- ticks on a configurable interval, e.g. `1s` default
- pages through indexed pending scopes from Redis
- checks `ActiveActorChatRun(...)`
- if slot is free and pending count > 0, emits `NotifyPendingInitialRun(...)`
- if pending count == 0, clears stale scope index entry

Do not execute runs directly in the sweeper. The sweeper should only reschedule scopes into the existing wakeup queue so the actual run start path remains centralized in `PendingInitialRunWorker`.

- [ ] **Step 4: Wire the sweeper into runtime bootstrap**

Add:

- builder in `runtimewire`
- lifecycle module in `cmd/larkrobot/bootstrap.go`
- `Stats()` exposure on the sweeper

- [ ] **Step 5: Run sweep tests to verify they pass**

Run the same command from Step 2.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/agentruntime/pending_scope_sweeper.go internal/application/lark/agentruntime/pending_scope_sweeper_test.go internal/application/lark/agentruntime/runtimewire/runtimewire.go cmd/larkrobot/bootstrap.go
git commit -m "feat: add pending initial scope sweep worker"
```

## Sprint 2: Structured Status Surface For Debugging

**Goal:** Make `/statusz` and worker stats sufficient for first-line debugging without logs or Redis CLI.

**Demo/Validation:**

- Hit `/statusz`
- See pending worker stats, sweep worker stats, and queue/index counters
- Confirm an operator can tell whether a scope is blocked, idle, or stuck

### Task 4: Define Shared Pending Runtime Stats Collector

**Files:**

- Create: `internal/application/lark/agentruntime/metrics.go`
- Test: `internal/application/lark/agentruntime/metrics_test.go`
- Modify: `internal/application/lark/agentruntime/initial_run_worker.go`
- Modify: `internal/application/lark/agentruntime/pending_scope_sweeper.go`

- [ ] **Step 1: Write failing tests for metrics collector snapshots**

Cover:

- counters increment correctly
- queue depth / indexed scope count can be snapshotted
- last error / last scope / last sweep time are preserved

- [ ] **Step 2: Run metrics collector tests to verify they fail**

Run:

```bash
env GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'TestPending.*Metrics.*' -count=1
```

Expected: missing collector implementation.

- [ ] **Step 3: Implement a shared in-process collector**

Recommended counters/gauges:

- `pending_runs_enqueued_total`
- `pending_runs_started_total`
- `pending_runs_requeued_total`
- `pending_scope_wakeup_total`
- `pending_scope_wakeup_consumed_total`
- `pending_scope_sweep_total`
- `pending_scope_sweep_rescheduled_total`
- `pending_scope_busy_skip_total`
- `pending_scope_empty_cleanup_total`
- `pending_run_wait_seconds` histogram-like bucket summary or rolling min/max/avg

Also track status-oriented fields:

- `last_scope_chat_id`
- `last_scope_actor_open_id`
- `last_started_run_id`
- `last_pending_trigger_message_id`
- `last_error`
- `last_sweep_at`

- [ ] **Step 4: Hook collector into worker/sweeper**

Increment counters at the exact points where:

- enqueue succeeds
- wakeup is emitted
- wakeup is dequeued
- scope skipped because slot busy
- run actually starts
- item requeued
- stale scope cleaned up
- sweep tick executes

- [ ] **Step 5: Run metrics collector tests to verify they pass**

Run the same command from Step 2.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/agentruntime/metrics.go internal/application/lark/agentruntime/metrics_test.go internal/application/lark/agentruntime/initial_run_worker.go internal/application/lark/agentruntime/pending_scope_sweeper.go
git commit -m "feat: add pending runtime stats collector"
```

### Task 5: Expose Queue And Sweep Stats Through Existing Status Plane

**Files:**

- Modify: `internal/application/lark/agentruntime/initial_run_worker.go`
- Modify: `internal/application/lark/agentruntime/pending_scope_sweeper.go`
- Modify: `cmd/larkrobot/bootstrap.go`

- [ ] **Step 1: Write failing status assertions**

Add tests that assert worker/sweeper `Stats()` include:

- processed / skipped / retried counts
- current indexed scope count
- last sweep cursor/time
- last queue depth snapshot

- [ ] **Step 2: Run status tests to verify they fail**

Use focused package tests or bootstrap tests depending on where assertions land.

- [ ] **Step 3: Extend `Stats()` output**

Surface structured fields from the shared collector and direct worker internals so `/statusz` contains enough detail to debug:

- active queue depth
- indexed scope count
- number of pending scopes rescheduled by sweep
- number of scopes skipped due to active slot
- last successful start time
- last rescheduled scope

- [ ] **Step 4: Run tests to verify they pass**

Re-run the focused test command from Step 2.

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/agentruntime/initial_run_worker.go internal/application/lark/agentruntime/pending_scope_sweeper.go cmd/larkrobot/bootstrap.go
git commit -m "chore: surface pending runtime stats in status output"
```

## Sprint 3: Scrape-Friendly Metrics Export

**Goal:** Expose time-series metrics that can be scraped and alerted on, not just viewed in `/statusz`.

**Demo/Validation:**

- Hit `/metrics`
- See counters/gauges for pending queue lifecycle and sweep outcomes
- Verify the values change during a local integration scenario

### Task 6: Decide Metrics Export Format

**Files:**

- Modify: `docs/architecture/agent-runtime-progress.md`
- Optionally modify: `go.mod`

- [ ] **Step 1: Choose exporter strategy**

Decision point:

- preferred now: Prometheus text exposition on existing management HTTP
- defer OpenTelemetry metrics until the repo has a real metric provider, exporter config, and naming conventions

Document this explicitly before coding so the implementation does not half-adopt two systems.

- [ ] **Step 2: If adding a dependency, fetch current docs before implementation**

If using `prometheus/client_golang`, fetch current official docs first. Do not rely on memory.

- [ ] **Step 3: Commit docs-only decision if needed**

```bash
git add docs/architecture/agent-runtime-progress.md go.mod go.sum
git commit -m "docs: record pending runtime metrics export strategy"
```

### Task 7: Add `/metrics` To Management HTTP

**Files:**

- Modify: `internal/runtime/health_http.go`
- Modify: `cmd/larkrobot/bootstrap.go`
- Test: `internal/runtime/health_http_test.go` or create a new focused test

- [ ] **Step 1: Write failing management HTTP tests**

Cover:

- `/metrics` route exists
- route returns `200`
- route includes pending runtime metric names

- [ ] **Step 2: Run management HTTP tests to verify they fail**

Run:

```bash
env GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml go test ./internal/runtime -run 'TestHealthHTTP.*Metrics.*' -count=1
```

Expected: missing route/metrics output.

- [ ] **Step 3: Implement `/metrics` output**

Expose at least:

- `betago_agent_runtime_pending_runs_enqueued_total`
- `betago_agent_runtime_pending_runs_started_total`
- `betago_agent_runtime_pending_runs_requeued_total`
- `betago_agent_runtime_pending_scope_wakeup_total`
- `betago_agent_runtime_pending_scope_wakeup_consumed_total`
- `betago_agent_runtime_pending_scope_busy_skip_total`
- `betago_agent_runtime_pending_scope_sweep_total`
- `betago_agent_runtime_pending_scope_sweep_rescheduled_total`
- `betago_agent_runtime_pending_scope_empty_cleanup_total`
- `betago_agent_runtime_pending_scope_indexed`
- `betago_agent_runtime_pending_run_queue_depth`
- `betago_agent_runtime_pending_run_wait_seconds`

Label guidance:

- avoid high-cardinality labels like raw `chat_id` / `actor_open_id`
- keep labels limited to result buckets such as `source=wakeup|sweep`, `outcome=started|busy|empty|error`

- [ ] **Step 4: Run management HTTP tests to verify they pass**

Run the same command from Step 2.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/health_http.go internal/runtime/health_http_test.go cmd/larkrobot/bootstrap.go
git commit -m "feat: expose agent runtime pending metrics on management http"
```

## Sprint 4: End-To-End Verification And Runbooks

**Goal:** Prove the new scheduler is correct under race conditions and document how to debug it in production.

**Demo/Validation:**

- Run local integration tests
- Trigger manual scenario with two same-actor tasks
- Observe `/statusz` and `/metrics`
- Confirm no user interaction is required after slot release

### Task 8: Add End-To-End Regression Coverage

**Files:**

- Modify: `internal/application/lark/agentruntime/coordinator_test.go`
- Modify: `internal/application/lark/agentruntime/initial_run_worker_test.go`
- Modify: `internal/application/lark/agentruntime/pending_scope_sweeper_test.go`

- [ ] **Step 1: Add end-to-end regression tests**

Required scenarios:

- active run completes -> pending starts automatically
- wakeup consumed too early -> sweep later starts pending
- repeated wakeups do not start duplicate runs
- pending scope index cleaned after last pending item drains

- [ ] **Step 2: Run focused regression tests**

Run:

```bash
env GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml go test ./internal/application/lark/agentruntime -run 'Test(RunCoordinatorCompleteRunWithReplyNotifiesPendingInitialWorker|PendingInitialRunWorker.*|PendingScopeSweeper.*)' -count=1
```

- [ ] **Step 3: Commit**

```bash
git add internal/application/lark/agentruntime/coordinator_test.go internal/application/lark/agentruntime/initial_run_worker_test.go internal/application/lark/agentruntime/pending_scope_sweeper_test.go
git commit -m "test: cover pending scheduler end-to-end regressions"
```

### Task 9: Update Architecture And Operations Notes

**Files:**

- Modify: `docs/architecture/agent-runtime-progress.md`

- [ ] **Step 1: Document final scheduler semantics**

Record:

- pending FIFO is source of truth
- wakeup queue is fast-path only
- sweep is correctness backstop
- scope index drives sweep
- metrics semantics and alert intent

- [ ] **Step 2: Add operator debugging checklist**

Include:

- what to check in `/statusz`
- what to check in `/metrics`
- how to recognize stuck indexed scope vs busy slot vs empty cleanup

- [ ] **Step 3: Commit**

```bash
git add docs/architecture/agent-runtime-progress.md
git commit -m "docs: record pending scheduler and metrics runbook"
```

## Testing Strategy

- Redis-level tests first, because sweep correctness depends on explicit scope index semantics.
- Worker tests second, because they must keep all current slot/lock guarantees intact.
- Management HTTP tests only after the collector shape stabilizes.
- Full package regression after each sprint:

```bash
env GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml go test ./internal/application/lark/agentruntime/... ./internal/infrastructure/redis/... ./internal/runtime/... ./internal/infrastructure/lark_dal/larkmsg -count=1
```

- Manual validation after Sprint 3:
  - fire two identical same-actor agentic tasks
  - let the first finish
  - verify the second starts without any extra user action
  - inspect `/statusz` and `/metrics` during the transition

## Metrics Plan

### Layer 1: Status-Oriented Fields

These go into existing `Stats()` maps and therefore `/statusz`:

- current queue depth for the most recently processed scope
- total indexed scope count
- processed / retried / skipped-busy counts
- last started run id
- last pending trigger message id
- last error
- last wakeup time
- last sweep time

These are for ad hoc debugging and incident response.

### Layer 2: Time-Series Metrics

These go into `/metrics` and are intended for scraping and alerts:

- enqueue counter
- start counter
- requeue counter
- wakeup emitted counter
- wakeup consumed counter
- sweep tick counter
- sweep reschedule counter
- busy skip counter
- stale scope cleanup counter
- indexed scope gauge
- aggregate pending queue depth gauge
- pending wait duration histogram

### Metrics Anti-Patterns To Avoid

- do not label metrics with raw `chat_id`, `message_id`, `run_id`, or `actor_open_id`
- do not emit one metric series per scope
- do not mix status strings into labels if a bounded enum is enough
- do not make sweep directly execute runs; metrics would become ambiguous and concurrency riskier

## Potential Risks & Gotchas

- If the scope index is not cleared only when FIFO is empty, sweep can keep waking dead scopes forever.
- If the worker both requeues and immediately reschedules without releasing the scope lock first, it can self-thrash.
- If sweep executes runs directly instead of only scheduling scopes, it will duplicate concurrency logic already centralized in `PendingInitialRunWorker`.
- If `/metrics` exposes high-cardinality labels, the fix creates an observability outage of its own.
- If wait duration is recorded only on success, failed or repeatedly retried items may look artificially healthy.
- If Redis is unavailable, the current pending queue feature is already degraded; document clearly that sweep/metrics share the same dependency.

## Rollback Plan

- Disable the sweep worker module in `cmd/larkrobot/bootstrap.go` while keeping the existing wakeup worker path intact.
- Keep the pending scope index keys unused but harmless if the sweep is rolled back.
- If `/metrics` export causes issues, leave the shared collector in place and continue exposing only `/statusz` stats until exporter issues are resolved.

## Recommended Delivery Order

1. Redis scope index
2. Worker refactor
3. Sweep worker
4. Shared collector + `/statusz`
5. `/metrics`
6. Docs and runbook
