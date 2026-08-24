# Ark Prefix Cache and OTel Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent short Ark prompts from repeatedly failing prefix-cache validation and restore process-wide OpenTelemetry trace correlation.

**Architecture:** Add an explicit cache opt-out and an error-specific direct-request fallback in the shared Ark adapter. Build the application OTel resource schemalessly, return resource errors instead of panicking, and log optional module degradation at the runtime boundary.

**Tech Stack:** Go 1.26, Volcengine Ark Responses SDK, Redis/miniredis, OpenTelemetry Go 1.45, standard `testing` package.

---

### Task 1: Direct Ark Path for Known Short Prompts

**Files:**
- Modify: `internal/infrastructure/ark_dal/responses_raw.go`
- Test: `internal/infrastructure/ark_dal/responses_raw_test.go`
- Modify/Test: `internal/application/lark/conversationeval/candidate_ark.go`
- Modify/Test: `internal/application/lark/conversationeval/judge.go`

- [ ] Write a failing test for `DisablePrefixCache: true` that expects one request with ordered system/user messages, no previous response ID, and no caching settings.
- [ ] Extend candidate and judge completion tests to require the opt-out flag.
- [ ] Run the focused tests and verify they fail because the flag/direct path is absent.
- [ ] Add `DisablePrefixCache bool`, reusable multi-message input construction, direct request execution, and shared response-text extraction.
- [ ] Set the flag for candidate and judge requests.
- [ ] Re-run the focused tests and verify they pass.

Focused command:

```bash
env BETAGO_CONFIG_PATH=.dev/config.toml /root/.go/go1.26.1/bin/go test -v -tags=custom_skip_vips \
  ./internal/infrastructure/ark_dal ./internal/application/lark/conversationeval \
  -run 'TestResponseTextWithCacheDisabledPrefixUsesDirectRequest|TestComplete(Candidate|Judge)JSONWithArk' -count=1
```

### Task 2: Error-Specific Prefix Cache Fallback

**Files:**
- Modify: `internal/infrastructure/ark_dal/responses_raw.go`
- Test: `internal/infrastructure/ark_dal/responses_raw_test.go`

- [ ] Write a failing test where the cache head returns `input tokens must be greater than 256 when using prefix cache` and a second direct request succeeds.
- [ ] Write a test proving an unrelated cache-head error is returned without a second request.
- [ ] Run both tests and verify the short-prefix case fails for the expected reason.
- [ ] Add a case-insensitive classifier requiring both `input tokens must be greater than` and `when using prefix cache`.
- [ ] Fall back exactly once through the direct helper only for that classification.
- [ ] Re-run both tests and verify they pass.

Focused command:

```bash
env BETAGO_CONFIG_PATH=.dev/config.toml /root/.go/go1.26.1/bin/go test -v -tags=custom_skip_vips \
  ./internal/infrastructure/ark_dal \
  -run 'TestResponseTextWithCache(FallsBackForShortPrefix|DoesNotFallbackForUnrelatedError)' -count=1
```

### Task 3: OTel Resource Recovery

**Files:**
- Modify: `internal/infrastructure/otel/otel.go`
- Test: `internal/infrastructure/otel/otel_test.go`

- [ ] Write a failing test that creates the application resource and expects no SchemaURL conflict.
- [ ] Write a failing initialization test that expects valid trace/span IDs and restores providers in cleanup.
- [ ] Run the tests and verify the existing `1.43.0` versus `1.41.0` conflict is observed.
- [ ] Use `resource.NewSchemaless`, return `(*resource.Resource, error)`, and propagate merge errors from `newTracerProvider`.
- [ ] Re-run the tests and verify valid IDs.

Focused command:

```bash
env BETAGO_CONFIG_PATH=.dev/config.toml /root/.go/go1.26.1/bin/go test -v -tags=custom_skip_vips \
  ./internal/infrastructure/otel \
  -run 'Test(NewResourceMergesWithDefault|InitCreatesValidSpan)' -count=1
```

### Task 4: Visible Optional Module Degradation

**Files:**
- Modify: `internal/runtime/app.go`
- Test: `internal/runtime/app_test.go`

- [ ] Write a failing test that injects a log function, starts a failing optional module, and expects module/stage/error fields while startup remains successful and degraded.
- [ ] Run the test and verify no lifecycle log is currently captured.
- [ ] Add a standard-library-backed package log function and invoke it only for non-critical module degradation.
- [ ] Re-run the test and verify it passes.

Focused command:

```bash
env BETAGO_CONFIG_PATH=.dev/config.toml /root/.go/go1.26.1/bin/go test -v -tags=custom_skip_vips \
  ./internal/runtime -run '^TestAppLogsOptionalModuleFailure$' -count=1
```

### Task 5: Regression and Build Verification

**Files:**
- Verify all modified Go files and this plan.

- [ ] Run `gofmt` on every modified Go file and `git diff --check`.
- [ ] Run all affected package tests with the repository baseline.
- [ ] Build `./cmd/larkrobot` with Go 1.26 and `custom_skip_vips`.
- [ ] Confirm no unrelated files changed and preserve the user's existing untracked files.

Verification commands:

```bash
env BETAGO_CONFIG_PATH=.dev/config.toml /root/.go/go1.26.1/bin/go test -v -tags=custom_skip_vips \
  ./internal/infrastructure/ark_dal \
  ./internal/application/lark/conversationeval \
  ./internal/infrastructure/otel \
  ./internal/runtime -count=1

env BETAGO_CONFIG_PATH=.dev/config.toml /root/.go/go1.26.1/bin/go build \
  -tags=custom_skip_vips ./cmd/larkrobot
```
