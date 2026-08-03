# LLM Usage Business Taxonomy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add business-scene attribution and durable tool-call analytics to LLM usage records, then expose consistent business-first metrics across the WebUI without removing existing technical dimensions.

**Architecture:** Extend `llmusage.Scope` with explicit scene/operation attribution plus a deterministic legacy classifier. Persist one aggregate usage row per logical model turn and child tool-call rows in one transaction. Add additive business/tool aggregates to the WebUI API, centralize frontend taxonomy/aggregation helpers, and render business metrics before the retained technical drilldowns.

**Tech Stack:** Go 1.25, GORM/PostgreSQL, Volcengine Responses API, Vue 3.5, Pinia, ECharts 6, Element Plus, Vitest.

---

## File Structure

- `internal/infrastructure/llmusage/types.go`: stable scene, operation, attribution, and tool-call contracts plus legacy mapping.
- `internal/infrastructure/llmusage/recorder.go`: transactional persistence boundary and online metrics.
- `internal/infrastructure/llmusage/turn.go`: logical-turn usage/tool accumulator.
- `internal/infrastructure/ark_dal/responses.go`: feed completed response usage and real handler results into the accumulator.
- `internal/infrastructure/db/model/llm_*.gen.go`: generated-shape models for the new columns/table.
- `internal/infrastructure/db/query/llm_*.gen.go` and `gen.go`: generated query surface.
- `script/sql/20260803_llm_usage_business_taxonomy.sql`: embedded production migration and historical backfill.
- `internal/infrastructure/schema/migrations.go`: register the migration.
- Lark entry-point files: assign explicit business scenes and operations.
- `internal/interfaces/webui/token_stats_store.go`: safe allowlisted aggregation and tool summary queries.
- `webui/src/usage/taxonomy.ts`: shared labels, colors, provenance copy, and types.
- `webui/src/usage/aggregation.ts`: pure cross-chat/cross-bot merging.
- `webui/src/components/UsageBusinessOverview.vue`: reusable business KPI/chart/tool section.
- `webui/src/views/Dashboard.vue`, `ChatList.vue`, `ChatDetail.vue`: integrate business-first display and retained technical drilldown.

## Task 1: Business Attribution Contract

**Files:**
- Modify: `internal/infrastructure/llmusage/types.go`
- Create: `internal/infrastructure/llmusage/attribution_test.go`

- [ ] **Step 1: Write failing taxonomy tests**

Cover explicit attribution, the complete legacy source table, trimming, invalid enum fallback, and the known `source=chat` historical limitation:

```go
func TestNormalizeScopePrefersExplicitBusinessAttribution(t *testing.T) {
    got := NormalizeScope(Scope{
        SourceType: SourceTypeUser, Source: "chat",
        BusinessScene: SceneCommand, BusinessOperation: OperationCommandChat,
    })
    if got.BusinessScene != SceneCommand || got.AttributionMode != AttributionExplicit {
        t.Fatalf("normalized attribution = %+v", got)
    }
}

func TestLegacyAttributionMapsKnownSources(t *testing.T) {
    tests := []struct{ source string; scene BusinessScene; operation BusinessOperation }{
        {"chat", SceneConversation, OperationChatReply},
        {"intent", SceneRouting, OperationIntentRecognition},
        {"chunking", SceneBackground, OperationChunkMerge},
        {"conversation_evaluation_candidate", SceneEvaluation, OperationCandidateGeneration},
        {"agent_callback_continuation", SceneAgentRuntime, OperationCallbackContinuation},
    }
    // NormalizeScope must return AttributionLegacyMapping for each case.
}
```

- [ ] **Step 2: Run the red test**

Run:

```bash
go test ./internal/infrastructure/llmusage -run 'Test(NormalizeScopePrefersExplicitBusinessAttribution|LegacyAttributionMapsKnownSources)'
```

Expected: FAIL because business attribution types do not exist.

- [ ] **Step 3: Implement stable enums and classifier**

Add `BusinessScene`, `BusinessOperation`, and `AttributionMode`; extend `Scope`; implement an exhaustive `legacyAttribution(source)` switch; ensure invalid explicit values fall back to mapping and finally `unknown`.

- [ ] **Step 4: Run package tests**

Run `go test ./internal/infrastructure/llmusage`.

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infrastructure/llmusage/types.go internal/infrastructure/llmusage/attribution_test.go
git commit -m "feat: classify LLM usage by business scene"
```

## Task 2: Migration and Transactional Recorder

**Files:**
- Create: `script/sql/20260803_llm_usage_business_taxonomy.sql`
- Modify: `internal/infrastructure/schema/migrations.go`
- Modify: `internal/infrastructure/db/model/llm_token_usage_records.gen.go`
- Create: `internal/infrastructure/db/model/llm_tool_call_records.gen.go`
- Modify: `internal/infrastructure/db/query/llm_token_usage_records.gen.go`
- Create: `internal/infrastructure/db/query/llm_tool_call_records.gen.go`
- Modify: `internal/infrastructure/db/query/gen.go`
- Modify: `internal/infrastructure/llmusage/recorder.go`
- Modify: `internal/infrastructure/llmusage/recorder_test.go`
- Modify: `internal/infrastructure/schema/migrations_test.go`

- [ ] **Step 1: Write failing recorder and migration tests**

Define a store contract that persists a main row and tool rows atomically:

```go
type Store interface {
    CreateUsageTurn(context.Context, *UsageRecordRow, []ToolCallRecordRow) error
}

func TestRecorderWritesBusinessAttributionAndSanitizedToolCalls(t *testing.T) {
    // Record command_chat with one successful and one failed tool call.
    // Assert counts on the main row and safe child rows without args/output/raw errors.
}

func TestDefaultMigrationsIncludeLLMUsageBusinessTaxonomy(t *testing.T) {
    // Assert version 20260803_llm_usage_business_taxonomy is registered once.
}
```

- [ ] **Step 2: Verify red**

Run:

```bash
go test ./internal/infrastructure/llmusage ./internal/infrastructure/schema -run 'Test(RecorderWritesBusinessAttribution|DefaultMigrationsIncludeLLMUsageBusinessTaxonomy)'
```

Expected: FAIL on missing store method, fields, table, and migration.

- [ ] **Step 3: Add the idempotent SQL migration**

The SQL must:

- add scene/operation/attribution and tool-count columns with non-null defaults;
- create the two business aggregation indexes;
- create `llm_tool_call_records` and its three indexes;
- backfill the exact source mapping from the design;
- set `attribution_mode='legacy_mapping'` only for mapped historical rows;
- remain safe when replayed through the schema migration ledger.

- [ ] **Step 4: Update generated model/query shapes**

Update the generated LLM usage artifacts for the six new main-row fields and the new tool-call table. Include `LlmToolCallRecord` in `query.Q`, `SetDefault`, `Use`, cloning, and context query interfaces.

- [ ] **Step 5: Implement transactional persistence**

`GormStore.CreateUsageTurn` uses `db.Transaction`, creates the main row, copies its generated ID into each tool row, and bulk inserts tool rows. The fake store mirrors this contract. `Recorder.Record` derives summary counts from tool calls rather than trusting caller-supplied totals.

- [ ] **Step 6: Verify green**

Run:

```bash
go test ./internal/infrastructure/llmusage ./internal/infrastructure/schema
```

Expected: PASS where the host supports package compilation.

- [ ] **Step 7: Commit**

```bash
git add script/sql/20260803_llm_usage_business_taxonomy.sql internal/infrastructure/schema internal/infrastructure/db/model internal/infrastructure/db/query internal/infrastructure/llmusage
git commit -m "feat: persist LLM business and tool usage"
```

## Task 3: Logical Turn Usage and Tool Accumulation

**Files:**
- Create: `internal/infrastructure/llmusage/turn.go`
- Create: `internal/infrastructure/llmusage/turn_test.go`
- Modify: `internal/infrastructure/ark_dal/responses.go`
- Modify: `internal/infrastructure/ark_dal/responses_test.go`
- Modify: `internal/infrastructure/ark_dal/responses_manual.go`

- [ ] **Step 1: Write failing turn accumulator tests**

```go
func TestTurnAccumulatorSumsResponseLegsAndRecordsToolsOnce(t *testing.T) {
    turn := NewTurnAccumulator(scope, "ark", "model")
    turn.AddUsage("resp-plan", Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12})
    turn.AddToolCall(ToolCall{Name: "search_history", Status: ToolStatusSuccess})
    turn.AddUsage("resp-final", Usage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25})
    got := turn.Record(StatusSuccess)
    // Assert 30/7/37 tokens and exactly one tool call.
}

func TestTurnAccumulatorDeduplicatesResponseCompletedReplay(t *testing.T) {}
```

- [ ] **Step 2: Run red tests**

Run `go test ./internal/infrastructure/llmusage -run TestTurnAccumulator`.

Expected: FAIL because the accumulator is missing.

- [ ] **Step 3: Implement the accumulator**

Track response IDs to make completed-event replay idempotent. Store sanitized tool name/status/duration/error kind. Produce one immutable `llmusage.Record` at turn completion.

- [ ] **Step 4: Write failing Responses integration tests**

Use fake stream events for planning completion, two tool calls, synthesis completion, and EOF. Assert one observer record with summed usage and two tool calls. Add a failed-tool case and assert final user-visible stream items remain unchanged.

- [ ] **Step 5: Verify integration red**

Run `go test ./internal/infrastructure/ark_dal -run 'TestResponsesImpl.*TurnUsage'`.

Expected: FAIL because Responses still records per stream completion.

- [ ] **Step 6: Integrate turn accumulation**

Refactor the loop so same-leg tool calls are collected, the current response reaches `response.completed`, and only then is the continuation created. Sum every completed leg and emit one record when no continuation remains or on terminal error. Preserve capability traces and output ordering.

- [ ] **Step 7: Verify green**

Run:

```bash
go test ./internal/infrastructure/llmusage
go test ./internal/infrastructure/ark_dal -run 'TestResponsesImpl'
```

Expected: PASS, subject only to the documented host VIPS dependency.

- [ ] **Step 8: Commit**

```bash
git add internal/infrastructure/llmusage/turn* internal/infrastructure/ark_dal/responses*
git commit -m "feat: aggregate tool-loop usage by logical turn"
```

## Task 4: Explicit Attribution at Business Entry Points

**Files:**
- Modify: `internal/application/lark/messages/ops/chat_op.go`
- Modify: `internal/application/lark/messages/ops/reply_chat_op.go`
- Modify: `internal/application/lark/messages/ops/command_op.go`
- Modify: `internal/application/lark/messages/ops/intent_recognize_op.go`
- Modify: `internal/application/lark/handlers/chat_handler.go`
- Modify: `internal/application/lark/handlers/two_phase_chat.go`
- Modify: `internal/application/lark/handlers/history_search_handler.go`
- Modify: `internal/application/lark/agentruntime/generator.go`
- Modify: `internal/application/lark/handlers/tools.go`
- Modify: `internal/application/lark/messages/recording/service.go`
- Modify: `internal/infrastructure/lark_dal/larkmsg/record.go`
- Modify: `internal/infrastructure/retriever/retriver.go`
- Modify: `pkg/xchunk/chunking.go`
- Modify: `internal/tools/reindexembeddings/reindex.go`
- Modify: `internal/application/lark/messages/ops/intent_recognize_op_test.go`
- Modify: `internal/application/lark/handlers/chat_handler_test.go`
- Modify: `internal/application/lark/handlers/tools_test.go`
- Modify: `internal/application/lark/agentruntime/generator_test.go`
- Modify: `internal/application/lark/messages/recording/service_test.go`
- Modify: `internal/infrastructure/retriever/retriver_test.go`
- Create: `pkg/xchunk/chunking_test.go`
- Modify: `internal/tools/reindexembeddings/reindex_test.go`

- [ ] **Step 1: Add failing entry-point tests**

Test at least conversation, command, intent, tool planner, topic recall, callback, candidate, chunk merge, and debug scopes. The command test must prove `/bb` is `command/command_chat`, while ambient and mention paths are conversation operations.

- [ ] **Step 2: Verify red**

Run focused tests in `llmusage`, `messages/ops`, `handlers`, and `agentruntime`.

Expected: FAIL because callers still rely on source mapping.

- [ ] **Step 3: Add attribution context helpers**

Provide immutable context helpers in `llmusage` for entry-point attribution. `buildUserLLMUsageScope` reads the helper and explicit operation overrides are made for intent, planner, retrieval, and background work. Independent workers construct explicit scopes directly.

- [ ] **Step 4: Migrate every Ark/embedding caller**

Search all `llmusage.Scope{` and scope builders. Known sources must write `AttributionExplicit`; only compatibility tests may intentionally exercise legacy mapping.

- [ ] **Step 5: Verify green and coverage search**

Run focused package tests, then:

```bash
rg -n 'Source:\s*"(chat|intent|chunking|conversation_evaluation_candidate|agent_callback_continuation)' internal pkg
```

Inspect every result and confirm explicit scene/operation is present or inherited from an explicitly tested context.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark internal/infrastructure/lark_dal internal/infrastructure/retriever pkg/xchunk internal/tools/reindexembeddings internal/infrastructure/llmusage
git commit -m "feat: attribute LLM calls at business entry points"
```

## Task 5: WebUI Statistics API

**Files:**
- Modify: `internal/interfaces/webui/types.go`
- Modify: `internal/interfaces/webui/token_stats_store.go`
- Create: `internal/interfaces/webui/token_stats_store_test.go`
- Modify: `webui/src/api/types.ts`

- [ ] **Step 1: Write failing store aggregation tests**

Use a small SQLite-backed table or SQL mock matching the production schema. Insert conversation, command, background, one tool turn with two tool rows, and one no-tool turn. Assert:

- business scene/operation/source groups;
- additive tool calls and related tokens;
- `turns_with_tools` counts usage rows, not tool rows;
- `by_tool` reports calls/success/error/average latency;
- multi-tool joins do not duplicate Token totals;
- bot/chat/time filters remain enforced.

- [ ] **Step 2: Verify red**

Run `go test ./internal/interfaces/webui -run TestTokenStatsStoreBusiness`.

Expected: FAIL because API fields and queries are missing.

- [ ] **Step 3: Implement allowlisted aggregation**

Replace the raw `groupBy(column string)` contract with a private enum/allowlist. Add source, scene, and operation groups. Use main-row conditional sums for tool-related tokens and a separate child-table query for `by_tool`.

- [ ] **Step 4: Extend API types without breaking existing fields**

Add `ByBusinessScene`, `ByBusinessOperation`, `ByRawSource`, `ToolSummary`, and `ByTool`. Extend totals/groups/daily points only where fields remain additive.

- [ ] **Step 5: Verify green**

Run `go test ./internal/interfaces/webui -run 'TestTokenStatsStore|Test.*TokenStats'`.

Expected: PASS subject to host VIPS linkage.

- [ ] **Step 6: Commit**

```bash
git add internal/interfaces/webui webui/src/api/types.ts
git commit -m "feat: expose business and tool usage statistics"
```

## Task 6: Shared Frontend Taxonomy and Aggregation

**Files:**
- Create: `webui/src/usage/taxonomy.ts`
- Create: `webui/src/usage/aggregation.ts`
- Create: `webui/src/usage/usage.test.ts`
- Modify: `webui/src/stores/filter.ts`

- [ ] **Step 1: Write failing pure-function tests**

```ts
it('merges additive business and tool metrics across chats', () => {
  const merged = mergeUsageStats([first, second])
  expect(merged.toolSummary.tool_calls).toBe(3)
  expect(merged.toolSummary.tool_related_tokens).toBe(120)
})

it('uses consistent labels for explicit, legacy and unknown attribution', () => {
  expect(sceneLabel('conversation')).toBe('对话生成')
  expect(attributionLabel('legacy_mapping')).toBe('历史映射')
  expect(sceneLabel('new_value')).toBe('待归类')
})
```

- [ ] **Step 2: Verify red**

Run `npm test --prefix webui -- --run src/usage/usage.test.ts`.

Expected: FAIL because modules are missing.

- [ ] **Step 3: Implement taxonomy and aggregation**

Centralize labels/colors and pure additive merge helpers. Extend `MetricKey` with `tool_calls` and `tool_related_tokens`; extend `DimensionKey` with `business_scene`, `business_operation`, and `source`. Keep tool name as a dedicated tool dimension rather than coercing it into Token groups.

- [ ] **Step 4: Verify green**

Run the focused Vitest file and `npm run build --prefix webui`.

- [ ] **Step 5: Commit**

```bash
git add webui/src/usage webui/src/stores/filter.ts webui/src/api/types.ts
git commit -m "feat: add shared usage taxonomy to webui"
```

## Task 7: Business-First WebUI

**Files:**
- Create: `webui/src/components/UsageBusinessOverview.vue`
- Create: `webui/src/components/usage-business-overview.test.ts`
- Modify: `webui/src/composables/useChartOptions.ts`
- Modify: `webui/src/views/Dashboard.vue`
- Modify: `webui/src/views/ChatList.vue`
- Modify: `webui/src/views/ChatDetail.vue`
- Modify: `webui/src/styles/theme.css`

- [ ] **Step 1: Write failing component/source tests**

Mount `UsageBusinessOverview` with populated, empty, legacy, and unknown data. Assert KPI copy, scene labels, tool success/error copy, provenance badges, and empty state. Add source tests that require all three views to import the shared component/taxonomy and retain technical dimension keys.

- [ ] **Step 2: Verify red**

Run:

```bash
npm test --prefix webui -- --run src/components/usage-business-overview.test.ts src/views
```

Expected: FAIL because the component and integrations are missing.

- [ ] **Step 3: Build the reusable business overview**

Render five KPIs, business-scene doughnut, business-operation horizontal bar, and a tool ranking list with calls/success rate/average latency. Use ECharts responsive container patterns and existing `EChart` resize behavior. Do not show per-tool exclusive Token.

- [ ] **Step 4: Integrate Dashboard and ChatDetail**

Place the business overview before existing charts. Move model/kind/source_type/source/status charts under a clearly labeled technical section without deleting drilldowns. Wire business scene/action filters to existing breadcrumb behavior.

- [ ] **Step 5: Integrate ChatList**

Include business-scene filtering in single/multi-Bot summaries and preserve existing selections across filters. Do not fetch per-tool rows for every list item; list aggregation uses the stats response already fetched for top chats.

- [ ] **Step 6: Add mobile-first styles**

Default to one column; enhance at existing project breakpoints. Make tool rows compact cards below tablet width, allow legends/filters to wrap, and maintain 44px touch targets.

- [ ] **Step 7: Verify green**

Run all WebUI tests and production build:

```bash
npm test --prefix webui -- --run
npm run build --prefix webui
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add webui/src
git commit -m "feat: show business-first LLM usage analytics"
```

## Task 8: Completion Audit and Documentation

**Files:**
- Modify: `docs/observability/llm-token-usage-drilldown.md`

- [ ] **Step 1: Update operator documentation**

Document scene/operation meanings, explicit versus historical attribution, tool-related Token semantics, tool detail privacy, example SQL, and the non-additive per-tool Token caveat.

- [ ] **Step 2: Run formatters and generated-file checks**

```bash
gofmt -w internal/infrastructure/llmusage/*.go internal/infrastructure/ark_dal/*.go internal/infrastructure/schema/*.go internal/interfaces/webui/*.go internal/application/lark/messages/ops/*.go internal/application/lark/handlers/*.go internal/application/lark/agentruntime/*.go internal/application/lark/messages/recording/*.go internal/infrastructure/lark_dal/larkmsg/*.go internal/infrastructure/retriever/*.go pkg/xchunk/*.go internal/tools/reindexembeddings/*.go
git diff --check
```

- [ ] **Step 3: Run focused verification**

```bash
go test ./internal/infrastructure/llmusage
go test ./internal/infrastructure/schema
go test ./internal/infrastructure/ark_dal -run 'TestResponsesImpl|TestTurnAccumulator'
go test ./internal/interfaces/webui -run 'TestTokenStatsStore|Test.*TokenStats'
npm test --prefix webui -- --run
npm run build --prefix webui
```

If the host still lacks GLib/VIPS pkg-config metadata, record that exact environment error and run every unaffected pure-Go/frontend test; do not claim the blocked packages passed.

- [ ] **Step 4: Run requirement-by-requirement audit**

Verify all 14 design acceptance criteria against code, tests, migration SQL, API types, and rendered component sources. Search for unmapped known `source` values and confirm no tool args/outputs/raw errors are persisted.

- [ ] **Step 5: Final diff and commit**

```bash
git status --short
git diff --check
git diff --stat origin/master...HEAD
git add docs/observability/llm-token-usage-drilldown.md
git commit -m "docs: explain LLM business usage analytics"
```
