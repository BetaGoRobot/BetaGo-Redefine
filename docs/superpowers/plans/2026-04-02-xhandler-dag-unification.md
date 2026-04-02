# XHandler DAG Unification Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current `fetcher -> handler` split in `pkg/xhandler` with a unified dependency-driven stage model that supports chained dependencies and DAG-based concurrent execution.

**Architecture:** Introduce a single `Stage` abstraction with `Depends()` returning peer stages, compile the registered roots plus all transitive dependencies into a DAG before execution, then run every stage once with dependency-aware concurrency. Existing message ops stay as business stages, while the former intent "fetcher" becomes a normal dependency stage.

**Tech Stack:** Go, `pkg/xhandler`, Lark message ops, Go test.

---

## Chunk 1: Lock The DAG Behavior With Tests

### Task 1: Rewrite `pkg/xhandler` dependency tests around unified stages

**Files:**
- Modify: `pkg/xhandler/dependency_test.go`

- [ ] **Step 1: Write failing chain-dependency test**

Add a test where `root` depends on `mid`, and `mid` depends on `leaf`, then assert `root` waits for both upstream stages.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./pkg/xhandler -run 'TestRunParallelStages_' -count=1`
Expected: FAIL because the current implementation only flattens one dependency layer.

- [ ] **Step 3: Write failing cycle-detection test**

Add a test that builds `a -> b -> a` and assert `RunParallelStages()` returns an error instead of deadlocking.

- [ ] **Step 4: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./pkg/xhandler -run 'TestRunParallelStages_' -count=1`
Expected: FAIL because the current implementation has no DAG compile / cycle detection.

## Chunk 2: Unify `pkg/xhandler`

### Task 2: Replace split fetcher/operator plumbing with stage DAG execution

**Files:**
- Modify: `pkg/xhandler/base.go`
- Modify: `pkg/xhandler/tools.go`

- [ ] **Step 1: Introduce unified `Stage` abstraction**

Replace `Fetcher` and `Operator` usage in the processor with a single stage concept and make `Depends()` return peer stages.

- [ ] **Step 2: Compile roots into a DAG before execution**

Add recursive collection, duplicate-name conflict detection, and cycle detection.

- [ ] **Step 3: Run stages with dependency-aware concurrency**

Execute every stage once, let shared dependencies fan out to multiple downstream stages, and preserve `ErrStageSkip` / feature-gating semantics.

- [ ] **Step 4: Run focused tests**

Run: `GOCACHE=/tmp/betago-gocache go test ./pkg/xhandler -count=1`
Expected: PASS.

## Chunk 3: Migrate Real Usage

### Task 3: Convert message ops to unified stage dependencies

**Files:**
- Modify: `internal/application/lark/messages/ops/common.go`
- Modify: `internal/application/lark/messages/ops/chat_op.go`
- Modify: `internal/application/lark/messages/ops/command_op.go`
- Modify: `internal/application/lark/messages/ops/reply_chat_op.go`
- Modify: `internal/application/lark/messages/ops/intent_recognize_op.go`
- Modify: `internal/application/lark/messages/ops/intent_recognize_op_test.go`

- [ ] **Step 1: Replace old dependency signatures with stage dependencies**

Update the message ops aliases and `Depends()` return types to use the unified stage abstraction.

- [ ] **Step 2: Turn intent recognition into a normal dependency stage**

Move the old fetch logic onto the stage execution path so it can serve both root execution and transitive dependencies.

- [ ] **Step 3: Run focused tests**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/messages/ops -count=1`
Expected: PASS.

## Chunk 4: Integrated Verification

### Task 4: Verify the touched packages together

**Files:**
- Modify: `docs/superpowers/plans/2026-04-02-xhandler-dag-unification.md`

- [ ] **Step 1: Run aggregated verification**

Run:
`GOCACHE=/tmp/betago-gocache go test ./pkg/xhandler ./internal/application/lark/messages/ops ./internal/application/lark/messages -count=1`

Expected: PASS.

- [ ] **Step 2: Update checklist state in this plan if needed**
