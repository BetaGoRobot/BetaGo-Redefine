# Reaction observability implementation plan

> Execute inline with the existing reviewed design. Use test-first regression coverage and review the final diff against the deployed-source baseline.

**Goal:** A failed reaction lookup must preserve the actor, action time and original trace, and retain any independently known group.

**Architecture:** Add a small PostgreSQL event journal and message locator in `internal/infrastructure/larkeventstore`. Capture metadata at ingress and provider receipt boundaries. Resolve reaction metadata once in the processor's context-aware pre-run phase and reuse it in both operators.

**Tech stack:** Existing Go 1.26.8, Feishu SDK v3.12.2, GORM/PostgreSQL, Zap and OpenTelemetry; no new dependencies.

## Tasks

- [x] Run the existing reaction package baseline using the documented workspace test settings.
- [x] Add regression tests for event capture, untruncated actor/time attributes, malformed events, redaction, original trace preservation and independent lookup failures. Verify the tests fail before implementation.
- [x] Implement `larkeventstore` event records, PostgreSQL persistence, scoped location lookup and bounded retention. Add `script/sql/20261030_lark_event_provenance.sql` to the existing migration registry.
- [x] Capture inbound message locations and reaction journal records in `internal/interfaces/lark/handler.go`. Handle recall events instead of discarding them. Capture outbound locations synchronously at the provider receipt boundary in `internal/infrastructure/lark_dal/larkmsg/outbound.go`, before scheduling optional recording/indexing.
- [x] Replace reaction metadata's background-context lookup with a single context-aware pre-run resolver. Reuse the resulting chat ID in `record_reaction.go` and `follow_react.go`. Add structured provenance to both stages and message-lookup failure logs; preserve existing success behavior.
- [x] Run focused regressions and race tests for event storage, reaction processors, ingress, outbound recording, schema and bootstrap wiring. Exercise the additive migration and idempotent writes in an isolated PostgreSQL schema. Do not mutate production event/statistics tables during tests.
- [x] Review the diff, build the master binary with its existing native toolchain and produce a reviewable patch and verification evidence. Record precisely whether deployment occurred.

Acceptance: after a simulated 230050, one can query the event ID and recover its actor, action and receipt times, target message, error and trace; a previously observed message still resolves to its chat. No test sends a Lark message.

Verification: 86 focused test cases passed across six packages with `-race -count=1`; the PostgreSQL-only case skipped in that run was executed separately against an isolated schema and passed, with all 12 event-store cases passing and no race reports. Native master build completed using Go 1.26.8 in the existing musl builder. Review blockers were fixed and rechecked. Production was not deployed.
