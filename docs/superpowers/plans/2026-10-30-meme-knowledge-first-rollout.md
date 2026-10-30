# Knowledge-first meme retrieval implementation and release

> For agentic workers: use superpowers:subagent-driven-development, with spec review before quality review. User approved implementing the design, pushing with gh without local GPG, and publishing both master and next. Continue in the background and notify after completion.

**Goal:** Ship source-grounded literal knowledge before planning, bounded hypothesis expansion and persistent reply context on both deployed chains.

**Architecture:** One shared pure knowledge compiler/scanner and versioned context; immutable PG knowledge sidecars bound to the actual corpus manifest; a grounded reference service that preserves priors across planning/expansion failures; host-specific formatting and existing delivery behavior. No new model provider, search database, reply scoring model or public test messages.

**Tech stack:** existing Go modules (development source go1.26.8; verify next release overlay on its go1.26.0 too), PG and Milvus, existing completion interfaces and gh API.

## Baseline and ownership

Development is isolated in `.worktrees/meme-knowledge-first`. Parent imports the exact deployed master reaction-observability snapshot and next network/cover snapshot; unrelated worktrees remain untouched. Release builds overlay only feature files onto each separately frozen production source. Local commits may use `git -c commit.gpgsign=false` as the user explicitly allows; remote publication uses gh API.

All Go commands read root VS Code settings and use custom_skip_vips, test -v, existing absolute BETAGO_CONFIG_PATH and declared toolchain. No config copies.

## Task 1: Pure knowledge compiler and observations (implementer ownership)

Create `internal/application/lark/memes/knowledge_snapshot.go`, `knowledge_observations.go` and their focused tests. Define the typed binding/snapshot/entry/alias/sense/anchor/selection API provided by the parent. Compile detached, deterministic entries from immutable IndexRecord data; retain all use IDs and fingerprints; mark source explanations and inferred aliases accurately. Scan original rune spans with stable ordering and mapped normalization, preserve short terms and ambiguity, cap selected anchors at six, report omission. Validate scope and every entry binding, not just snapshot header.

- [x] Run existing memes package baseline.
- [x] Write missing-feature tests first: source/alias provenance, deterministic fingerprints, raw-span roundtrip, same-name ambiguity, short terms, two known anchors, normalization, dictionary order, six-anchor omission, generation mismatch.
- [x] Observe RED, implement the public API, run focused tests/race and self-review.
- [x] Commit only owned files; parent requests separate spec and quality review before accepting.

## Task 2: Bound knowledge persistence and publication (parent ownership)

Create `internal/infrastructure/memestore/knowledge.go` with PG tests; extend migration only for its new fact kind. Publish and read immutable manifests/chunks by generation+corpus/projection hashes with fingerprints, count limits and no mixing. Determine exact record membership from the existing publication ledger or matching canonical corpus artifacts before publishing. Add an operator command with explicit dry-run/publish, no source rewrite or implicit corpus activation.

- [x] Test missing/mismatched/tampered/chunked snapshots and isolated PG publication before implementation.
- [x] Reuse the current active corpus; do not rebuild embeddings or replace existing card versions.
- [x] Capture current active manifest before and after publication under the existing publication lock.

## Task 3: Grounded planner, query validation and bounded expansion

Create separate grounded contracts/prompt/schema/service files in memes; preserve V1 APIs. Add ContextV2 to ReferenceResult with independent statuses, selected raw anchors, priors, concise hypotheses and sources. Literal queries compile from frozen observation refs; knowledge_link requires knowledge refs; hypothesis_probe remains explicitly derived. Reject invalid individual queries without destroying valid priors. Keep old lexical anchors independent of semantic budgets; count the complete request against seven logical searches and the existing 12-second deadline, ordinary planner one call. Do not demand two interpretations.

- [x] Regression for H03 key loss and model self-supplied literal_source.
- [x] Negative cases for explicit game/history questions, invented IDs, stale generation, zero/failed expansion and source changes.
- [x] Actual role-model probes retain all first responses and failures; no model self-score as acceptance.

## Task 4: Runtime and both hosts

Extend memeruntime with explicit mode and generation-bound knowledge reads, caching immutable snapshots. Adapt next `cmd/lark-agent/meme_references.go` and engine scene, and master `handlers/meme_reference.go` plus bootstrap. Preserve delivered-card attribution, visible-output filters, ordinary questions, approvals, tools and action ledgers. Shared BuildContext must keep prior knowledge even when cards are empty and validate supplied references. Complete source-backed use metadata is rendered as an optional view, never a newly approved corpus record.

- [x] Full-context tests for prior-only fallback, six-anchor stability, no internal identifiers leaked and independent V1 behavior.
- [x] Native next model replay plus MASTER actual initialization/knowledge/policy readback; exact role settings and serious controls recorded. Human humor evaluation remains separate.

## Task 5: Verification, publication and release

Run scoped race/integration checks using disposable fixtures. Existing four baseline test failures are compared explicitly, never called passing. Read actual live master/next again before building. A late source/config drift requires rebasing the scoped overlay, not overwriting a concurrent release.

- [x] Publish design and complete self-contained source through gh, verify remote file hashes and expected branch head; include an exact reviewable change list.
- [x] Build each host with its own frozen module/toolchain, image user, dictionaries and prior labels.
- [x] Offline actual-config checks and real model/PG/Milvus read-only probes before runtime changes.
- [x] Publish verified knowledge sidecar and activate the explicit mode on the approved existing scopes only.
- [x] Release master and the three next apps separately with --no-deps --pull never; prove protected services and private config remain unchanged except declared mode settings.
- [x] Verify live health, zero restart loops, MASTER natural grounded planner activity, Next actual prior-on-timeout behavior and preserved rollback references. Do not send public messages without a specified test destination.
- [x] Report technical acceptance versus pending human humor evaluation. The user explicitly requests production publication; offline/deployment acceptance is not a claimed 80-window human evaluation.

## Completion

User asked to keep running in the background and notify in this conversation after implementation, remote push and both releases. Record every source/image/corpus/knowledge digest and all limitations. Do not mark completion for a partial design-only push.
