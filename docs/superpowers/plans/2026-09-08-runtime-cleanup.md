# Runtime Cleanup and Bootstrap Decomposition Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this approved plan task-by-task. Independent bootstrap work may run alongside runtime cleanup with separate file ownership.

**Goal:** Close startup resource leaks, preserve degradation behavior, and separate bootstrap responsibilities.

**Architecture:** Keep `runtime.App` as lifecycle owner and `cmd/larkrobot` as composition root. Track a successfully running module only after readiness handling; failure cleanup explicitly includes the current partially initialized module. Preserve module registration order and business behavior.

**Tech Stack:** Go 1.26.0, existing runtime Module/FuncModule and standard-library context/errors/log/testing. No dependency or schema changes.

Approved by the user on 2026-09-08 against architecture review section 6. Work proceeds on `codex/runtime-cleanup-20260908`; existing README and review edits are retained.

## Task 1: Reproduce missing lifecycle cleanup

Files: `internal/runtime/app_lifecycle_test.go` (new), existing `app_test.go`.

- [x] Add table-driven FuncModule failure scenarios at Init/Start/Ready. Record calls through per-test slices; assert current-module Stop precedes reverse rollback and every Stop occurs once.
- [x] Cover optional Init/Start failure cleanup, Ready degradation retained until normal Stop, and disabled Init versus Start/Ready resource ownership.
- [x] Cover cancellation with context values retained, deadline-bound cleanup, original and cleanup errors discoverable through `errors.Is`, and rolled-back registry state.
- [x] Run regression tests on original implementation and record expected assertion failures.

```sh
GOTOOLCHAIN=local BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.0/bin/go test -v -tags=custom_skip_vips ./internal/runtime -run 'TestApp(Cleans|Rolls|Disables|Cleanup|Preserves)'
```

## Task 2: Implement failure cleanup ownership

Files: `internal/runtime/app.go`, `internal/runtime/module.go`, `internal/runtime/app_test.go`; inspect all registered Module Stop paths for partial-initialization safety.

- [x] Add instance AppOptions via `NewAppWithOptions(options AppOptions, modules ...Module)` while retaining `NewApp(modules ...Module)` compatibility. Options contain cleanup timeout and error logger; default timeout is 30 seconds, default logger is `log.Default()`.
- [x] Use `context.WithTimeout(context.WithoutCancel(ctx), timeout)` for failure cleanup. No detached goroutine is used to pretend to bound an uncooperative Stop; Stop must observe its deadline.
- [x] Clean the current module on ordinary Init/Start failure; critical failure additionally rolls back prior modules. Optional Init/Start cleanup errors remain visible in degraded registry state and logging while startup continues.
- [x] Init ErrDisabled is resource-free; Start/Ready ErrDisabled cleans current resources immediately. Cleanup failure follows critical/optional failure policy and is not silently marked disabled.
- [x] Preserve optional Ready failure as a running degraded module. Update successful rollback states to stopped and failed cleanup states to failed. Aggregate errors with `errors.Join` and retain the initiating failure on the current module.
- [x] Replace the test's global log-function reassignment with an injected per-test logger. Document Stop's partial-initialization contract.
- [x] Re-run lifecycle regression and existing runtime tests to green.

## Task 3: Decompose composition root

Ownership: worker only changes `cmd/larkrobot`.

- [x] Establish `cmd/larkrobot` existing test baseline with the same Go/test settings.
- [x] Keep `bootstrap.go` as short buildApp orchestration; move component construction to `bootstrap_components.go`, infra setup to `bootstrap_infrastructure.go`, search provisioning to `bootstrap_search.go`, application wiring to `bootstrap_application.go`, and evaluation wiring to `bootstrap_evaluation.go`. Place helpers beside their consumer where possible.
- [x] Move `scheduler` into `appComponents`, retaining creation in the scheduler module Start callback and release in its Stop callback.
- [x] Preserve module names, order, critical flags, defaults, factory behavior and validation errors. Use existing topology and configuration tests; do not add tests mirroring file layout.
- [x] Format and run `cmd/larkrobot` tests. Report every moved function and any partial-init Stop issue to the parent.

## Task 4: Verify and review

- [x] Run runtime, xhandler, cmd/larkrobot and affected-module tests; run runtime race tests for lifecycle regressions.
- [x] Obtain independent code review, resolve substantive findings, and inspect `git diff --check`.
- [x] Update architecture report and ADR with actual implemented behavior and test results.
- [x] Deliver changes in the workspace with verification evidence. No deployment, real orders, external messages or schema mutation.

```sh
GOTOOLCHAIN=local BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.0/bin/go test -v -tags=custom_skip_vips ./internal/runtime ./pkg/xhandler ./cmd/larkrobot
GOTOOLCHAIN=local BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.0/bin/go test -v -race -tags=custom_skip_vips ./internal/runtime
git diff --check
```

## Independent requested audit

An audit agent owns `docs/architecture/2026-09-08-luckin-ordering-review.md`. It traces Luckin ordering flows and documents evidence, triggers, consequences, priority and design options. Luckin production changes are outside this batch.

## Execution evidence

- Core lifecycle regression cases failed on the original implementation (missing current-module Stop, stale registry states, canceled/unbounded cleanup context, missing cleanup error).
- Runtime, xhandler and cmd/larkrobot: 53 top-level tests passed with the declared baseline.
- Runtime race run: 29 top-level tests passed, no race report.
- Independent review found no blocking regression; bootstrap declaration comparison preserved behavior except the approved instance ownership and injection changes.
- Existing non-cooperative Stop methods remain a documented limitation; no deployment or external ordering action was performed.
