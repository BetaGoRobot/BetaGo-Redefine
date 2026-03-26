# Chat Passive Ratelimit V2 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rework passive chat reply policy so direct mentions/private chat always bypass blocking, passive replies and active interjections use willingness-aware rate limiting, and `search_history` supports richer metadata filters while remaining strictly scoped to the current `chat_id`.

**Architecture:** Split reply policy into three layers: intent policy classifies direct/passive/interject decisions and willingness metadata, ratelimit enforces passive/interject budgets with separate hard/soft accounting, and chat generation narrows default context while relying on explicit history tools for broader retrieval. `search_history` becomes the primary metadata-aware history tool, but the handler owns the `chat_id` boundary so tool calls cannot escape the current conversation.

**Tech Stack:** Go, OpenSearch, Redis, GORM/gen query layer, Lark handlers/operators, Ark Responses tool calling, Go test.

---

## Chunk 1: Metadata-Aware History Search

### Task 1: Extend `search_history` request model and tests

**Files:**
- Create: `internal/application/lark/history/search_test.go`
- Modify: `internal/application/lark/history/search.go`
- Modify: `internal/application/lark/handlers/history_search_handler.go`
- Modify: `internal/application/lark/handlers/tools_test.go`

- [x] **Step 1: Write failing history-search query builder tests**

Add table-driven tests that verify:
- `HybridSearchRequest.ChatID` is mandatory for tool usage.
- `user_id`, `user_name`, and `message_type` filters become exact filter clauses.
- keyword search still builds both keyword and vector clauses.
- invalid or missing `ChatID` never produces an unscoped query.

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/history -run 'TestHybridSearch' -count=1`
Expected: FAIL because metadata filtering and mandatory chat scoping are not implemented.

- [x] **Step 3: Write failing handler/tool tests**

Add tests for:
- `search_history` tool schema exposes metadata fields such as `user_name` and `message_type`.
- handler always injects `metaData.ChatID` into the search request.
- handler rejects empty `metaData.ChatID`.

- [x] **Step 4: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/handlers -run 'Test(SearchHistory|LarkTools)' -count=1`
Expected: FAIL because the tool does not yet expose the new metadata fields or enforce empty-chat rejection.

- [x] **Step 5: Implement minimal history search changes**

Implement:
- richer `HybridSearchRequest` metadata fields,
- a helper that builds the OpenSearch bool query with mandatory `chat_id`,
- handler-side enforcement that tool searches are scoped to the current chat only.

- [x] **Step 6: Run focused tests**

Run:
- `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/history -run 'TestHybridSearch' -count=1`
- `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/handlers -run 'Test(SearchHistory|LarkTools)' -count=1`
Expected: PASS.

## Chunk 2: Willingness-Aware Passive Reply Policy

### Task 2: Expand intent analysis payload and tests

**Files:**
- Modify: `internal/application/lark/intentmeta/types.go`
- Modify: `internal/application/lark/intent/recognizer.go`
- Modify: `internal/application/lark/intent/recognizer_test.go`
- Modify: `internal/application/lark/messages/ops/intent_recognize_op_test.go`

- [x] **Step 1: Write failing intent payload tests**

Add tests that cover:
- parsing/sanitizing `reply_mode`, `user_willingness`, `interrupt_risk`, `needs_history`, `needs_web`,
- conservative defaults for missing/invalid values,
- prompt text explicitly describing direct/passive/interject distinctions.

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/intent ./internal/application/lark/messages/ops -run 'Test(Intent|AnalyzeMessage|IntentRecognize)' -count=1`
Expected: FAIL because the new fields and prompt guidance do not exist yet.

- [x] **Step 3: Implement minimal intent model changes**

Implement:
- new enum/type definitions in `intentmeta`,
- updated recognizer system prompt and JSON parsing,
- sanitization rules that keep direct/private traffic conservative and passive/interject scores bounded to `0-100`.

- [x] **Step 4: Run focused tests**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/intent ./internal/application/lark/messages/ops -run 'Test(Intent|AnalyzeMessage|IntentRecognize)' -count=1`
Expected: PASS.

### Task 3: Rework passive reply decider and ratelimit accounting

**Files:**
- Modify: `internal/application/lark/ratelimit/rate_limiter.go`
- Modify: `internal/application/lark/ratelimit/integration.go`
- Modify: `internal/application/lark/ratelimit/rate_limiter_test.go`
- Modify: `internal/application/lark/messages/ops/chat_op.go`
- Modify: `internal/application/config/accessor.go`

- [x] **Step 1: Write failing decider/ratelimit tests**

Add tests that verify:
- direct mention traffic is never blocked by `Allow` because it bypasses the decider path,
- `TriggerTypeMention` contributes to soft load but not hard passive budget,
- `intent_reply_threshold` and `intent_fallback_rate` come from config accessor rather than hardcoded constants,
- active interjections cost less than passive replies.

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/ratelimit ./internal/application/lark/messages/ops -run 'Test(SmartRateLimiter|Decider|ChatMsgOperator)' -count=1`
Expected: FAIL because hard/soft budget separation and config-driven thresholds are not implemented.

- [x] **Step 3: Implement minimal passive-reply ratelimit changes**

Implement:
- separate hard/soft weights,
- willingness-aware score calculation in the decider,
- config-driven thresholds/fallback rates,
- chat operator mapping from intent reply mode to passive/interject trigger type.

- [x] **Step 4: Run focused tests**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/ratelimit ./internal/application/lark/messages/ops -run 'Test(SmartRateLimiter|Decider|ChatMsgOperator)' -count=1`
Expected: PASS.

## Chunk 3: Prompt Split And Context Narrowing

### Task 4: Split direct-response vs ambient-response prompt selection

**Files:**
- Modify: `internal/application/lark/agentruntime/chatflow/standard_plan.go`
- Modify: `internal/application/lark/agentruntime/chatflow/standard_plan_test.go`
- Modify: `internal/application/lark/handlers/chat_handler_test.go`
- Modify: `internal/application/lark/messages/ops/reply_chat_op.go`
- Modify: `internal/application/lark/messages/ops/chat_op.go`

- [x] **Step 1: Write failing plan-selection tests**

Add tests that verify:
- direct replies choose the direct-response prompt path,
- passive/interject replies choose the ambient prompt path,
- passive prompt path no longer requires broad preloaded history to function.

- [x] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/chatflow ./internal/application/lark/handlers -run 'Test(StandardPlan|GenerateChatSeq|ResolveChatExecutionMode)' -count=1`
Expected: FAIL because prompt selection is still single-path.

- [x] **Step 3: Implement minimal prompt/context split**

Implement:
- prompt variant selection in standard chat planning,
- minimal default context for passive/direct replies,
- continued use of `search_history` for broader recall instead of mandatory pre-spliced history blocks.

- [x] **Step 4: Run focused tests**

Run: `GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/agentruntime/chatflow ./internal/application/lark/handlers -run 'Test(StandardPlan|GenerateChatSeq|ResolveChatExecutionMode)' -count=1`
Expected: PASS.

## Chunk 4: Integrated Verification

### Task 5: Run regression suite for touched domains

**Files:**
- Modify: `docs/superpowers/plans/2026-03-26-chat-passive-ratelimit-v2.md`

- [x] **Step 1: Run the aggregated targeted suite**

Run:
`GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/history ./internal/application/lark/handlers ./internal/application/lark/intent ./internal/application/lark/messages/ops ./internal/application/lark/ratelimit ./internal/application/lark/agentruntime/chatflow -count=1`

Expected: PASS.

- [x] **Step 2: Update plan checklist and capture follow-up debt**

### Follow-up debt

- `prompt_template_args.prompt_id=5` 仍然存在遗留模板耦合；本次通过“强约束系统提示 + 缩窄上下文”压住了问题，但下一轮最好迁到命名化 prompt config，而不是继续依赖数字 prompt ID。
- `search_history` 已支持当前 chat 内的 metadata filter，但还没有进一步拆出 thread slice / recent slice 这类更低成本的专用读取工具。
- 主索引现在作为 bot 自我身份判断的事实来源：bot 自己发出的消息保留真实 `user_id`，展示名仍可渲染成“你”；历史搜索和历史行渲染对旧数据里的 `"你"` sender alias 做兼容映射，暂不要求 retriever 同步这套身份元数据。
- chat / agentic / continuation 三条 runtime user prompt 现在都会显式注入 `self_open_id` 与 `self_name`；`self_name` 通过带缓存的 `application/v6/applications/:app_id` 读取应用名，失败时回落到 `BaseInfo.RobotName`，便于模型在主索引历史和 mention 元数据里稳定识别“谁是自己”。
- 主索引 mention 解析不再把 mention 直接吞掉；历史搜索结果会把 mention 还原成 `@姓名`，命中 bot 自己时渲染成 `@你`，便于模型直接理解“谁在@谁”。
- 频控已经拆成 hard budget 和 soft load，但运营侧观测面板还看不到 direct/passive/interject 的拆分指标，后续应补 stats card 或 dashboard。
- standard chat 现在默认不预取大段历史/主题摘要，后续可以继续根据 `needs_history / needs_web` 做更细粒度的 runtime 提示或自动 tool bias。

Record any deferred work, especially:
- optional new history tools for exact recent chat slices/thread slices,
- migration from numeric prompt IDs to named prompt configs,
- stats panel exposure for passive-vs-direct budgets.

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/plans/2026-03-26-chat-passive-ratelimit-v2.md \
  internal/application/lark/history/search.go \
  internal/application/lark/history/search_test.go \
  internal/application/lark/handlers/history_search_handler.go \
  internal/application/lark/handlers/tools_test.go \
  internal/application/lark/intentmeta/types.go \
  internal/application/lark/intent/recognizer.go \
  internal/application/lark/intent/recognizer_test.go \
  internal/application/lark/messages/ops/chat_op.go \
  internal/application/lark/messages/ops/intent_recognize_op_test.go \
  internal/application/lark/ratelimit/integration.go \
  internal/application/lark/ratelimit/rate_limiter.go \
  internal/application/lark/ratelimit/rate_limiter_test.go \
  internal/application/lark/agentruntime/chatflow/standard_plan.go \
  internal/application/lark/agentruntime/chatflow/standard_plan_test.go \
  internal/application/lark/handlers/chat_handler_test.go
git commit -m "feat: add willingness-aware passive chat ratelimit"
```
