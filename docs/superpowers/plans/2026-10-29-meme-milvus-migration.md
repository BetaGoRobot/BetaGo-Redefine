# Meme retrieval on Milvus implementation plan

> For agentic workers: execute the approved migration in parallel with explicit file ownership; preserve unrelated workspace changes.

**Goal:** Import the complete Wiki and enable the same four-field retrieval service on MASTER and next using Milvus.

**Architecture:** PostgreSQL retains immutable sources, encoded cards, publication receipts and the Active generation. Milvus stores four generation-specific retrieval collections. Application query analysis, per-lane results, RRF fusion, final five references and adoption logging retain their existing contracts. FAISS is not a second application database.

**Approved constraints:** The user approved Milvus on 2026-10-29 and requires both old and new hosts. Existing OpenSearch candidates and their rollout are superseded. Full import must complete before production enablement. The requested early query is `蒋介石`, using the complete real retrieval pipeline and an explicitly identified corpus snapshot.

**Tech stack:** Go versions from each worktree, `custom_skip_vips`, PostgreSQL, Milvus 2.6.25 REST v2, existing Ark encoding and 2048-dimensional embedding adapters.

## 1. Shared Milvus adapter

- [ ] Implement `internal/infrastructure/mememilvus/`: publication, immutable resume, four collections, authenticated REST, actual BM25 and dense searches.
- [ ] Relation, emotion and keys use mixed Chinese/English analysis, BM25 sparse indices and L2 dense HNSW. Original description uses dense search only; retain UTF-8 chunking, mean/L2 policy and description hash reuse.
- [ ] Keep full records in PostgreSQL addressed by fingerprint, avoiding Milvus scalar-field length limits. Resolve and validate hits before returning them to the existing fusion function.
- [ ] Serialize publishers with a PostgreSQL generation lock. Verify ambiguous outcomes before retrying; certify the generation only after all expected rows and indices are queryable. Completed publications are read-only to the application publisher.
- [ ] Test cancellation, resume, conflicting/missing records, mixed-language BM25 and description vectors against actual Milvus, with isolated collection names.

## 2. MASTER and import integration

- [ ] Add the immutable `meme_projection_records` fact kind and a backend tag on generation manifests in `memestore`.
- [ ] Switch `cmd/larkrobot/bootstrap_meme_references.go` and `cmd/meme-sync` to Milvus. Resolve dedicated Milvus settings and environment-only import credentials.
- [ ] Retain the exact encoding contract/hash/profile and source cache so existing successful Wiki encoding is reused.
- [ ] Verify with Go 1.26.8, `BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml`, and `go test -tags=custom_skip_vips -v -race` on changed packages.

## 3. Next integration

- [ ] Switch native meme initialization to the same adapter and synchronized shared settings/store files.
- [ ] Preserve current quiet-chat source, existing non-meme environment and 1024-dimensional memory embeddings.
- [ ] Verify native initialization and configuration in models, worker and agent modes with Go 1.26.0 and the same documented tags/test flags.

## 4. Milvus service and query evidence

- [ ] Install a pinned standalone image on `homelab-v2` with real persistent paths, embedded etcd/local storage and authentication. Record image digest and health without secrets.
- [ ] Run an isolated snapshot publication and the actual `蒋介石` query analysis, each BM25/vector lane, RRF and final references. Record exact corpus coverage and timings; do not activate a partial snapshot or send Lark messages.

## 5. Full import and release

- [ ] Finish all Wiki sources and bounded retries, then publish/verify the full Milvus generation.
- [ ] Replace OpenSearch-specific deployment and readback checks with Milvus backend/schema/collection/receipt checks.
- [ ] Build frozen MASTER and next candidates, verify both real recall paths, then apply the previously authorized scoped deployment with rollback and fresh live-state guards.
- [ ] Record deployed image/source/config facts, service health and actual recall evidence separately from human acceptance.
