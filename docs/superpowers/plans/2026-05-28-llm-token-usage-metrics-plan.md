# LLM Token Usage Metrics Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add online and offline token usage statistics for every LLM call source, with explicit chat/user/source metadata.

**Architecture:** Introduce `internal/infrastructure/llmusage` as the token usage recording boundary. Change all `ark_dal` APIs to require an explicit `llmusage.Scope`, record VictoriaMetrics counters and Postgres detail rows in wrappers, and migrate every caller in one pass so old no-scope APIs no longer compile.

**Tech Stack:** Go 1.25, GORM/Postgres, VictoriaMetrics `metrics`, Volcengine Ark SDK, existing runtime `/metrics`.

---

## Chunk 1: Usage Recorder Foundation

### Task 1: Add Scope, Record, Buckets, And Sanitization

**Files:**
- Create: `internal/infrastructure/llmusage/types.go`
- Test: `internal/infrastructure/llmusage/types_test.go`

- [ ] **Step 1: Write failing tests**

Test that `NormalizeScope` trims fields, defaults blank `source_type` to `system`, defaults blank `source` to `unknown`, and keeps empty user fields valid for background/system calls.

- [ ] **Step 2: Run red test**

Run: `go test ./internal/infrastructure/llmusage`
Expected: FAIL because package does not exist.

- [ ] **Step 3: Implement minimal types**

Define `SourceType`, constants, `Scope`, `NormalizeScope`, label truncation, `Record`, `Status`, and `BucketTimes(createdAt time.Time)`.

- [ ] **Step 4: Run green test**

Run: `go test ./internal/infrastructure/llmusage`
Expected: PASS.

### Task 2: Add Recorder

**Files:**
- Create: `internal/infrastructure/llmusage/recorder.go`
- Test: `internal/infrastructure/llmusage/recorder_test.go`

- [ ] **Step 1: Write failing tests**

Use sqlite in-memory GORM for offline write tests. Verify a record writes `bucket_minute`, `bucket_hour`, `bucket_day`, chat fields, user fields, token fields, status, model, kind, response_id, trace_id, and error. Verify nil DB skips offline write without error. Verify metrics calls do not panic.

- [ ] **Step 2: Run red test**

Run: `go test ./internal/infrastructure/llmusage`
Expected: FAIL because recorder is missing.

- [ ] **Step 3: Implement minimal recorder**

Add `Recorder` with package global default, `SetDefaultRecorder`, `DefaultRecorder`, `Record(ctx, record) error`, GORM model struct, and VictoriaMetrics counters.

- [ ] **Step 4: Run green test**

Run: `go test ./internal/infrastructure/llmusage`
Expected: PASS.

### Task 3: Add Postgres Migration

**Files:**
- Create: `script/migrations/012_add_llm_token_usage_records.sql`

- [ ] **Step 1: Write migration**

Create `llm_token_usage_records` with the fields and indexes from the design doc.

- [ ] **Step 2: Validate SQL shape**

Run: `sed -n '1,240p' script/migrations/012_add_llm_token_usage_records.sql`
Expected: table and indexes are present.

## Chunk 2: Ark DAL API Contract

### Task 4: Require Scope In Non-Streaming Responses

**Files:**
- Modify: `internal/infrastructure/ark_dal/ark.go`
- Modify: `internal/infrastructure/ark_dal/responses_raw.go`
- Test: `internal/infrastructure/ark_dal/responses_raw_test.go`

- [ ] **Step 1: Write failing tests**

Update existing cache tests to call `ResponseTextWithCache(ctx, req, scope)`. Add test hook recorder and assert cache head plus continuation pass the same explicit scope.

- [ ] **Step 2: Run red test**

Run: `go test ./internal/infrastructure/ark_dal -run 'TestResponseTextWithCache'`
Expected: FAIL before implementation.

- [ ] **Step 3: Implement minimal API changes**

Change `CreateResponses(ctx, body, scope)` and `ResponseTextWithCache(ctx, req, scope)`. On success, extract response usage if available and record `kind=responses,status=success`; on error record `status=error`.

- [ ] **Step 4: Run green test**

Run: `go test ./internal/infrastructure/ark_dal -run 'TestResponseTextWithCache'`
Expected: PASS.

### Task 5: Require Scope In Embeddings

**Files:**
- Modify: `internal/infrastructure/ark_dal/embedding.go`
- Test: `internal/infrastructure/ark_dal/embedding_test.go`

- [ ] **Step 1: Write failing test**

Inject fake runtime or small helper around usage conversion and assert `EmbeddingText(ctx, input, scope)` records `kind=embedding` with prompt/total tokens.

- [ ] **Step 2: Run red test**

Run: `go test ./internal/infrastructure/ark_dal -run 'TestEmbedding.*Usage'`
Expected: FAIL because scope-aware recording is missing.

- [ ] **Step 3: Implement minimal embedding recording**

Change signature to `EmbeddingText(ctx, input, scope)` and record success/error.

- [ ] **Step 4: Run green test**

Run: `go test ./internal/infrastructure/ark_dal -run 'TestEmbedding.*Usage'`
Expected: PASS.

### Task 6: Require Scope In Streaming Responses

**Files:**
- Modify: `internal/infrastructure/ark_dal/responses.go`
- Modify: `internal/infrastructure/ark_dal/responses_manual.go`
- Test: `internal/infrastructure/ark_dal/responses_manual_test.go`

- [ ] **Step 1: Write failing tests**

Update `Do` and `StreamTurn` tests to pass explicit scope. Add a focused unit test for stream finalization behavior: if usage is missing, one `responses_stream` record with `status=usage_missing` is emitted.

- [ ] **Step 2: Run red test**

Run: `go test ./internal/infrastructure/ark_dal -run 'Test.*Stream|Test.*Do|Test.*Turn'`
Expected: FAIL before scope-aware stream recording.

- [ ] **Step 3: Implement minimal stream recording**

Change `CreateResponsesStream(ctx, body, scope)`, `Do(ctx, scope, ...)`, and `StreamTurn(ctx, scope, req)`. Thread scope into continuation calls from tool outputs. Record usage when SDK exposes it; otherwise record `usage_missing` once at stream end.

- [ ] **Step 4: Run green test**

Run: `go test ./internal/infrastructure/ark_dal -run 'Test.*Stream|Test.*Do|Test.*Turn'`
Expected: PASS.

## Chunk 3: Caller Migration

### Task 7: Add Scope Builders At Lark User Entry Points

**Files:**
- Modify: `internal/application/lark/handlers/chat_handler.go`
- Modify: `internal/application/lark/handlers/debug_handler.go`
- Modify: `internal/application/lark/intent/recognizer.go`
- Modify: `internal/application/lark/messages/recording/service.go`
- Modify: `internal/infrastructure/lark_dal/larkmsg/record.go`

- [ ] **Step 1: Write/update tests**

Update existing tests to pass or assert explicit scope where stubs capture model requests.

- [ ] **Step 2: Implement caller changes**

Construct scope from `chat_id`, `chat_name`, `open_id`, `user_name`, `source_type=user/debug`, and source names like `chat`, `debug_image`, `intent`, `message_recording`.

- [ ] **Step 3: Run package tests**

Run: `go test ./internal/application/lark/handlers ./internal/application/lark/intent ./internal/application/lark/messages/... ./internal/infrastructure/lark_dal/larkmsg`
Expected: PASS.

### Task 8: Add Scope Builders At Background/System Entry Points

**Files:**
- Modify: `pkg/xchunk/chunking.go`
- Modify: `internal/infrastructure/retriever/retriver.go`
- Modify: `cmd/reindex-embeddings/main.go`
- Modify: `internal/tools/reindexembeddings/reindex.go`

- [ ] **Step 1: Update tests**

Update tests and stubs for new embedding/response signatures.

- [ ] **Step 2: Implement caller changes**

Pass `source_type=background` for chunking/reindex and `source_type=system` for retriever calls unless a user scope is explicitly available.

- [ ] **Step 3: Run package tests**

Run: `go test ./pkg/xchunk ./internal/infrastructure/retriever ./internal/tools/reindexembeddings`
Expected: PASS.

### Task 9: Delete Old Signatures By Compilation

**Files:**
- All Go files that call `ark_dal`

- [ ] **Step 1: Search for old signatures**

Run: `rg -n "ResponseWithCache\\(|ResponseTextWithCache\\(|EmbeddingText\\(|CreateResponses\\(|CreateResponsesStream\\(|\\.Do\\(|StreamTurn\\(" internal pkg cmd -g '!*.gen.go'`
Expected: every relevant call includes explicit `llmusage.Scope`.

- [ ] **Step 2: Full compile/test**

Run: `go test ./...`
Expected: PASS or only documented pre-existing external integration failures.

## Chunk 4: Runtime Wiring And Verification

### Task 10: Wire Default Recorder

**Files:**
- Modify: `cmd/larkrobot/bootstrap.go`
- Modify: `internal/infrastructure/db/db.go` if needed for DB accessor safety

- [ ] **Step 1: Add test or bootstrap assertion**

Use existing bootstrap tests if possible to assert recorder wiring does not panic when DB is unavailable.

- [ ] **Step 2: Implement wiring**

After DB init, call `llmusage.SetDefaultRecorder(llmusage.NewRecorder(db.DB()))`. If DB disabled/unavailable in tests, use nil DB recorder.

- [ ] **Step 3: Run bootstrap tests**

Run: `go test ./cmd/larkrobot ./internal/runtime`
Expected: PASS.

### Task 11: Final Verification

**Files:**
- All changed files

- [ ] **Step 1: Run focused tests**

Run the focused package tests from chunks 1-4.

- [ ] **Step 2: Run full tests**

Run: `go test ./...`
Expected: PASS or documented existing environment-bound failures.

- [ ] **Step 3: Inspect git diff**

Run: `git diff --stat && git diff --check`
Expected: no whitespace errors; diff matches scope.
