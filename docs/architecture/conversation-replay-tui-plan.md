# Conversation Replay TUI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提供一个本地 TUI，支持按群聊名选群、筛选近 X 天内的 Y 条消息、执行只读对话生成 replay，并把批次结果落为本地明细报告。

**Architecture:** 在现有 `internal/application/lark/replay` 单条 intent replay 能力上，新增群聊发现、样本筛选、conversation replay、批次执行、报告写入和 TUI 展示六个模块。执行层坚持“只读 replay”，允许 `dry-run` 和 `live-model`，但统一禁止真实工具执行、真实发消息和审批/schedule continuation。

**Tech Stack:** Go, existing replay/history/chatflow/ark_dal modules, OpenSearch-backed history index, Bubble Tea/Bubbles/Lipgloss for TUI

---

## Execution Status

- 已完成：Chunk 1 `chat catalog`
- 已完成：Chunk 2 `sample selector`
- 已完成：Chunk 3 `conversation replay extension`（含 replay 包单测回归）
- 已完成：Chunk 4 `batch runner and report writer`
- 已完成：Chunk 5 `tui app`
- 已完成：Chunk 6 `cli wiring`
- 进行中：Chunk 7 `verification and docs`

---

## File Map

**Create:**

- `internal/application/lark/replay/chat_catalog.go`
- `internal/application/lark/replay/chat_catalog_test.go`
- `internal/application/lark/replay/sample_selector.go`
- `internal/application/lark/replay/sample_selector_test.go`
- `internal/application/lark/replay/conversation_replay.go`
- `internal/application/lark/replay/conversation_replay_test.go`
- `internal/application/lark/replay/batch_report.go`
- `internal/application/lark/replay/batch_report_test.go`
- `internal/application/lark/replay/batch_runner.go`
- `internal/application/lark/replay/batch_runner_test.go`
- `internal/application/lark/replay/tui/model.go`
- `internal/application/lark/replay/tui/model_test.go`
- `internal/application/lark/replay/tui/view.go`
- `internal/application/lark/replay/tui/state.go`
- `internal/application/lark/replay/tui/types.go`

**Modify:**

- `internal/application/lark/replay/intent_replay.go`
- `internal/application/lark/replay/report.go`
- `internal/application/lark/replay/cli.go`
- `cmd/betago/main.go`
- `docs/architecture/conversation-replay-tui-design.md`
- `docs/architecture/intent-decision-replay-design.md`

**Potentially modify if needed for reuse seams:**

- `internal/application/lark/agentruntime/chatflow/turn.go`
- `internal/application/lark/agentruntime/chatflow/standard_plan.go`
- `internal/application/lark/agentruntime/chatflow/agentic_plan.go`
- `internal/application/lark/handlers/chat_handler.go`

---

## Chunk 1: Chat Discovery By Group Name

### Task 1: Add chat catalog query over history index

**Files:**

- Create: `internal/application/lark/replay/chat_catalog.go`
- Test: `internal/application/lark/replay/chat_catalog_test.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- search by `chat_name` keyword returns grouped chat candidates
- candidate includes `chat_id/chat_name/message_count_in_window/last_message_at`
- empty keyword returns a bounded recent list instead of all chats

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestChatCatalog'`

Expected: FAIL because chat catalog does not exist.

- [x] **Step 3: Write minimal implementation**

Implement:

- `ChatCandidate`
- `ChatCatalogQuery`
- history/OpenSearch backed grouped search by `chat_name`
- sane fallback when `chat_name` is missing

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestChatCatalog'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/replay/chat_catalog.go internal/application/lark/replay/chat_catalog_test.go
git commit -m "feat: add replay chat catalog"
```

---

## Chunk 2: Sample Filtering And Selection

### Task 2: Add message-shape and content-feature filters

**Files:**

- Create: `internal/application/lark/replay/sample_selector.go`
- Test: `internal/application/lark/replay/sample_selector_test.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- `mention/reply_to_bot/command/ambient_group_message` classification
- `question/long_message/has_link/has_attachment/keyword_contains` filtering
- window + filters + top-Y trimming returns expected samples in reverse chronological order

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestSampleSelector'`

Expected: FAIL because sample selector does not exist.

- [x] **Step 3: Write minimal implementation**

Implement:

- `ReplaySample`
- `SampleFilterOptions`
- message-shape classifier
- content-feature classifier
- top-Y reverse-chronological sampler

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestSampleSelector'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/replay/sample_selector.go internal/application/lark/replay/sample_selector_test.go
git commit -m "feat: add replay sample selector"
```

---

## Chunk 3: Conversation Replay Extension

### Task 3: Extend replay from intent-only to conversation generation

**Files:**

- Create: `internal/application/lark/replay/conversation_replay.go`
- Test: `internal/application/lark/replay/conversation_replay_test.go`
- Modify: `internal/application/lark/replay/intent_replay.go`
- Modify: `internal/application/lark/replay/report.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- standard replay captures prompt, user input, final `decision/reply/reference_*`
- agentic replay captures first tool intent without executing tools
- `dry-run` preserves build artifacts while leaving model outputs empty
- generation diff flags `decision/reply/tool intent` changes between baseline and augmented

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestConversationReplay'`

Expected: FAIL because conversation replay does not exist.

- [x] **Step 3: Write minimal implementation**

Implement:

- `ReplayConversationCase`
- standard plan builder replay
- agentic initial turn replay with tool-intent capture and no side effects
- integration into per-case report model

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestConversationReplay'`

Expected: PASS

- [x] **Step 5: Run compatibility tests**

Run:

- `BETAGO_CONFIG_PATH=/tmp/betago-test-config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/chatflow -run 'TestResolveChatflowProfileContextLines|TestBuildAgenticChatExecutionPlanExtendsToolBudgetForResearchRequests'`
- `BETAGO_CONFIG_PATH=/tmp/betago-test-config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/replay/conversation_replay.go internal/application/lark/replay/conversation_replay_test.go internal/application/lark/replay/intent_replay.go internal/application/lark/replay/report.go
git commit -m "feat: add conversation replay"
```

---

## Chunk 4: Batch Runner And Report Writer

### Task 4: Add batch execution and artifact writing

**Files:**

- Create: `internal/application/lark/replay/batch_runner.go`
- Test: `internal/application/lark/replay/batch_runner_test.go`
- Create: `internal/application/lark/replay/batch_report.go`
- Test: `internal/application/lark/replay/batch_report_test.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- batch runner continues after per-sample failures
- summary counts `success/partial/failed`
- artifact writer creates `summary.json/summary.md/filters.json/samples.json/cases/*.json/cases/*.md`
- batch summary aggregates route/generation/tool-intent change counts

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestReplayBatch'`

Expected: FAIL because batch runner/report writer do not exist.

- [x] **Step 3: Write minimal implementation**

Implement:

- `ReplayBatchRequest`
- `ReplayBatchResult`
- per-case status/error model
- local artifact directory layout under `artifacts/replay-batches/...`

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestReplayBatch'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/replay/batch_runner.go internal/application/lark/replay/batch_runner_test.go internal/application/lark/replay/batch_report.go internal/application/lark/replay/batch_report_test.go
git commit -m "feat: add replay batch runner and reports"
```

---

## Chunk 5: TUI App

### Task 5: Add Bubble Tea based replay TUI

**Files:**

- Create: `internal/application/lark/replay/tui/types.go`
- Create: `internal/application/lark/replay/tui/state.go`
- Create: `internal/application/lark/replay/tui/model.go`
- Create: `internal/application/lark/replay/tui/view.go`
- Test: `internal/application/lark/replay/tui/model_test.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- state transitions `chat picker -> filter builder -> sample preview -> runner -> report viewer`
- selection toggles and “select all” behavior
- runner progress updates from batch events
- report viewer can switch between summary and case detail

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay/tui -run 'TestReplayTUI'`

Expected: FAIL because TUI app does not exist.

- [x] **Step 3: Write minimal implementation**

Implement:

- Bubble Tea model/update/view
- list/table/progress/detail states
- no business logic in view layer; call replay services through injected interfaces

- [x] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay/tui -run 'TestReplayTUI'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/replay/tui
git commit -m "feat: add replay tui"
```

---

## Chunk 6: CLI Wiring

### Task 6: Expose `go run ./cmd/betago replay tui`

**Files:**

- Modify: `cmd/betago/main.go`
- Modify: `internal/application/lark/replay/cli.go`
- Test: `internal/application/lark/replay/cli_test.go`

- [x] **Step 1: Write the failing test**

Add tests for:

- parser accepts `replay tui`
- `replay tui` supports `--days`, `--limit`, optional `--live-model`, optional `--output-dir`
- `replay intent` existing path still works unchanged

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay -run 'TestParseCLIArgs|TestParseReplayTUIArgs'`

Expected: FAIL because TUI CLI entry does not exist.

- [x] **Step 3: Write minimal implementation**

Implement:

- subcommand split between `replay intent` and `replay tui`
- bootstrap required infra for TUI path
- start Bubble Tea program from `cmd/betago`

- [x] **Step 4: Run test to verify it passes**

Run:

- `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay`
- `GOCACHE=/tmp/betago-gocache go test ./cmd/betago`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/betago/main.go internal/application/lark/replay/cli.go internal/application/lark/replay/cli_test.go
git commit -m "feat: expose replay tui command"
```

---

## Chunk 7: Verification And Docs

### Task 7: End-to-end verification and usage notes

**Files:**

- Modify: `docs/architecture/conversation-replay-tui-design.md`
- Modify: `docs/architecture/intent-decision-replay-design.md`

- [x] **Step 1: Run focused replay tests**

Run:

- `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/replay/...`
- `GOCACHE=/tmp/betago-gocache go test ./cmd/betago`

Expected: PASS

- [x] **Step 2: Run affected compatibility tests**

Run:

- `BETAGO_CONFIG_PATH=/tmp/betago-test-config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/config -run 'TestGetBoolFallsBackToTomlForBusinessFlags|TestGetAllConfigKeysIncludesAccessorBackedKeys'`
- `BETAGO_CONFIG_PATH=/tmp/betago-test-config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops`
- `BETAGO_CONFIG_PATH=/tmp/betago-test-config.toml GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/chatflow -run 'TestResolveChatflowProfileContextLines|TestBuildAgenticChatExecutionPlanExtendsToolBudgetForResearchRequests'`

Expected: PASS

- [ ] **Step 3: Run a manual smoke flow**

Run:

- `go run ./cmd/betago replay tui`
- search one chat by name
- configure `days=3`, `limit=5`
- select a small dry-run batch
- verify artifacts are written under `artifacts/replay-batches/...`

Expected: TUI is interactive, dry-run batch completes, artifact directory exists.

- [ ] **Step 4: Document manual workflow**

Add:

- how to search group by name
- recommended dry-run first workflow
- when to switch to `live-model`
- artifact directory layout

- [ ] **Step 5: Commit**

```bash
git add docs/architecture/conversation-replay-tui-design.md docs/architecture/intent-decision-replay-design.md
git commit -m "docs: add conversation replay tui usage notes"
```

---

## Notes For The Implementer

- Reuse existing replay/chatflow builders wherever possible; do not fork prompt logic.
- Keep TUI as a thin shell over replay services.
- First version should favor bounded, inspectable behavior over fancy UX.
- `dry-run` remains the default path everywhere.
