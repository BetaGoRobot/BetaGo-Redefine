# Luckin Personal Checkout Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans and TDD. Domain ownership stays in `luckin`; action and repository ownership stays with the parent agent.

**Goal:** Self-service checkout must use the current user's own personal Luckin account throughout preview, submission and order tracking.

**Architecture:** Shared cart initiator and ordering-account owner are separate identities. Unified checkout uses the initiator; self-service checkout uses the operator. Persist the existing CredentialScope with the pending order and order record, and use it for every subsequent account operation. No schema change is needed.

**Tech Stack:** Existing Go 1.26, Luckin application services, MCP caller, repositories and native card builders.

User decision on 2026-09-08: “自我下单”使用用户各自自己的账号。

## Tasks

- [x] Add red tests for self-service choosing actor credentials and missing actor credentials refusing fallback; retain unified-mode initiator behavior.
- [x] Extract a small checkout credential-selection seam used by the actual handler; preserve the initiating user separately from the pending requester. Normalize empty form values after session fallback.
- [x] Add domain guard before confirmation side effects: personal credential scope must belong to pending requester, regardless of absent legacy CheckoutMode. Reject old mismatched pending cards with re-checkout guidance; cancellation remains available.
- [x] Authorize coupon revision using requester/chat/hash and the same credential-owner guard before token lookup or preview; retain the stored scope for revised drafts.
- [x] Add order lookup by app/bot/order ID using existing columns; manual status uses the persisted order scope after verifying chat, rather than the clicker's or initiator's account. Polling uses persisted scope too. Test via injected repository/caller/publisher without external requests.
- [x] Correct self-service card text and usage/audit documentation to reflect the chosen account semantics; validate cards locally only.
- [x] Run affected package tests, review diff and obtain independent review. Preserve earlier runtime changes; no deployment, real order, token or external message action.

Validation baseline: `GOTOOLCHAIN=local BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.0/bin/go test -v -tags=custom_skip_vips ./internal/application/lark/luckin ./internal/application/lark/luckinaction`. Repository tests must use isolated fakes or existing pure row-mapping tests; do not trigger real DB integration suites.

Existing state-machine/idempotency, checkout-batch and cache-consistency findings remain separately tracked in the audit; this change establishes account ownership only.

## Verification results

- RED tests reproduced initiator-account selection and unauthorized coupon revision before the fixes.
- Final baseline run: `luckin`, `luckinaction`, `mcpbridge`, `handlers`, and `cmd/larkrobot` all passed (190 top-level tests).
- Isolated `mcpstore.TestFindOrderUsesTenantAndOrderPredicates` passed (1 top-level test); no database integration suite was run.
- Independent review found two misleading card labels; both were fixed with passing regression tests.
- No real order, Lark message, schema change or deployment was performed.
