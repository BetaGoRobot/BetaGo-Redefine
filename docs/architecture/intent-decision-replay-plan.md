# Intent Decision Replay Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提供一个本地 CLI replay 工具，输入 `chat_id + message_id`，输出某条真实消息在 `baseline/augmented` 两种上下文增强配置下的 intent 决策差异。

**Architecture:** 复用现有消息查询、intent 输入组装、runtime observation 与 chatflow 规划逻辑，在 replay 层做“只读调用 + 双 case 对照 + 文本/JSON 报告”。第一阶段只做决策层，不执行任何副作用。

**Tech Stack:** Go, existing `internal/application/lark/messages/ops`, `internal/application/lark/agentruntime/chatflow`, existing config/history/opensearch/lark DAL modules

---

## File Map

**Create:**

- `internal/application/lark/replay/intent_replay.go`
- `internal/application/lark/replay/intent_replay_test.go`
- `internal/application/lark/replay/report.go`
- `internal/application/lark/replay/report_test.go`
- `internal/application/lark/replay/cli.go`
- `internal/application/lark/replay/cli_test.go`
- `cmd/betago/main.go`

**Modify:**

- `internal/application/lark/messages/ops/intent_context.go`
- `internal/application/lark/messages/ops/intent_recognize_op.go`
- `docs/architecture/intent-decision-replay-design.md`

**Potentially modify if needed for reuse seams:**

- `internal/infrastructure/lark_dal/larkmsg/parse.go`
- `internal/application/lark/history/*`

---

## Chunk 1: Replay Domain And Report Model

### Task 1: Define report types

**Files:**

- Create: `internal/application/lark/replay/report.go`
- Test: `internal/application/lark/replay/report_test.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- `ReplayReport` text rendering includes `Target/Baseline/Augmented/Diff Summary`.
- `DiffSummary` only lists changed fields.
- `dry-run` report tolerates empty `intent_analysis`.

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestReplayReport'`

Expected: FAIL because report types/renderers do not exist yet.

- [x] **Step 3: Write minimal implementation**

Implement:

- `ReplayReport`
- `ReplayCase`
- `ReplayDiff`
- text renderer
- JSON marshal-safe field layout

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestReplayReport'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/replay/report.go internal/application/lark/replay/report_test.go
git commit -m "feat: add intent replay report model"
```

---

## Chunk 2: Read-Only Sample Loading

### Task 2: Load target message and replay input

**Files:**

- Create: `internal/application/lark/replay/intent_replay.go`
- Test: `internal/application/lark/replay/intent_replay_test.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- replay loader reads target message by `chat_id + message_id`
- loader normalizes `chat_id/open_id/chat_type/text`
- missing message returns explicit error

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestIntentReplayLoadTarget'`

Expected: FAIL because replay loader does not exist.

- [x] **Step 3: Write minimal implementation**

Implement:

- target lookup abstraction
- normalized target struct
- read-only runtime observation capture

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestIntentReplayLoadTarget'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/replay/intent_replay.go internal/application/lark/replay/intent_replay_test.go
git commit -m "feat: add replay target loading"
```

---

## Chunk 3: Baseline And Augmented Input Construction

### Task 3: Reuse intent context assembly in replay

**Files:**

- Modify: `internal/application/lark/messages/ops/intent_context.go`
- Create/Modify: `internal/application/lark/replay/intent_replay.go`
- Test: `internal/application/lark/replay/intent_replay_test.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- `baseline` case disables intent context injection
- `augmented` case uses configured or overridden history/profile limits
- `--disable-history` and `--disable-profile` suppress only the intended lines

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestIntentReplayBuildCases'`

Expected: FAIL because dual-case assembly is missing.

- [x] **Step 3: Write minimal implementation**

Add replay-scoped wrappers so replay can build:

- `baseline.intent_input`
- `augmented.intent_input`
- `history_lines`
- `profile_lines`

Do not duplicate context logic. Reuse existing loaders behind explicit config overrides.

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestIntentReplayBuildCases'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/messages/ops/intent_context.go internal/application/lark/replay/intent_replay.go internal/application/lark/replay/intent_replay_test.go
git commit -m "feat: add baseline and augmented replay inputs"
```

---

## Chunk 4: Optional Live Intent Analysis

### Task 4: Add `--live-model` intent comparison

**Files:**

- Modify: `internal/application/lark/replay/intent_replay.go`
- Test: `internal/application/lark/replay/intent_replay_test.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- `dry-run` leaves `intent_analysis` empty
- `live-model` populates `baseline` and `augmented` analyses
- diff marks changed intent fields correctly

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestIntentReplayReplay'`

Expected: FAIL because optional live comparison is missing.

- [x] **Step 3: Write minimal implementation**

Inject a replay-local analyzer function that reuses existing intent analysis entry points.

Constraints:

- default path remains dry-run
- no side effects
- clear error when model call fails

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestIntentReplayReplay'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/replay/intent_replay.go internal/application/lark/replay/intent_replay_test.go
git commit -m "feat: add live model replay comparison"
```

---

## Chunk 5: Local CLI Wiring

### Task 5: Expose replay command

**Files:**

- Create/Modify: `internal/application/lark/replay/cli.go`
- Create/Modify: `internal/application/lark/replay/cli_test.go`
- Create: `cmd/betago/main.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- local CLI parser accepts `replay intent --chat-id ... --message-id ...`
- `--json`, `--output`, `--live-model`, `--history-limit`, `--profile-limit`, `--disable-history`, `--disable-profile` parse correctly

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestParseCLIArgs'`

Expected: FAIL because replay CLI parser does not exist.

- [x] **Step 3: Write minimal implementation**

Wire a local `cmd/betago replay intent ...` entry to the replay service.

Keep first version local-only. Do not add group `/debug` handling yet.

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestParseCLIArgs'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/command/command.go
git commit -m "feat: add intent replay command"
```

---

## Chunk 6: Verification And Docs

### Task 6: End-to-end verification

**Files:**

- Modify: `docs/architecture/intent-decision-replay-design.md`
- Create or update any usage notes referenced by command help

- [x] **Step 1: Run focused replay package tests**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay`

Expected: PASS

- [x] **Step 2: Run affected intent/config tests**

Run:

- `BETAGO_CONFIG_PATH=/tmp/betago-test-config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/config -run 'TestGetBoolFallsBackToTomlForBusinessFlags|TestGetAllConfigKeysIncludesAccessorBackedKeys'`
- `BETAGO_CONFIG_PATH=/tmp/betago-test-config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops`

Expected: PASS

- [x] **Step 3: Run targeted chatflow compatibility tests**

Run: `BETAGO_CONFIG_PATH=/tmp/betago-test-config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/chatflow -run 'TestResolveChatflowProfileContextLines|TestBuildAgenticChatExecutionPlanExtendsToolBudgetForResearchRequests'`

Expected: PASS

- [x] **Step 4: Document manual replay workflow**

Add:

- sample commands
- dry-run vs live-model interpretation
- how to collect `chat_id + message_id`

- [ ] **Step 5: Commit**

```bash
git add docs/architecture/intent-decision-replay-design.md
git commit -m "docs: add intent replay usage notes"
```

---

## Notes For The Implementer

- Prefer extracting reuse seams over copying intent/chatflow rules.
- Keep replay read-only.
- Do not add tool execution to first version.
- Do not mix report formatting with data collection. One package collects, one renders.
- If command wiring turns out to be awkward, it is acceptable to expose replay through a developer-only CLI entry first and integrate with full command registry in a follow-up.
