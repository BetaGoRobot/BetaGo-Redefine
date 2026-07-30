# Evaluation Workbench and Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the already-collected Control/Candidate episode timeline, context, outputs, feedback, and judgments through a bot-isolated, authenticated WebUI API, while making Agent Card patch reconciliation observable.

**Architecture:** PostgreSQL remains the evaluation fact source. A focused `EvaluationWorkbenchStore` reads complete episode bundles and appends versioned human judgments; WebUI handlers own HTTP validation and require configured Bearer authentication even for sensitive GETs. The existing Agent Card patch reconciler gains in-memory counters and dynamic health only—no new persistence or schema.

**Tech Stack:** Go 1.26, `net/http`, GORM/PostgreSQL, existing `conversationeval` contracts, VictoriaMetrics/runtime module stats.

---

### Task 1: Evaluation workbench read model

**Files:**
- Create: `internal/interfaces/webui/evaluation_workbench.go`
- Test: `internal/interfaces/webui/evaluation_workbench_test.go`

- [ ] **Step 1: Write failing PostgreSQL-backed tests**

Cover:

- list filtering by `app_id`, `bot_open_id`, chat, cohort, time, status, winner, and `needs_review`;
- stable `(anchor_at, id)` cursor pagination;
- detail loading pre/anchor/post messages in timeline order;
- both lane outputs with context/excluded/tool JSON preserved;
- all feedback and append-only judgments;
- cross-bot episode IDs returning not found.

- [ ] **Step 2: Run the RED tests**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
/root/.go/go1.26.1/bin/go test -run '^TestEvaluationWorkbench' -count=1 \
  -tags=custom_skip_vips ./internal/interfaces/webui
```

Expected: build failure because `EvaluationWorkbenchStore`, `EvaluationListQuery`, and `EvaluationEpisodeDetail` do not exist.

- [ ] **Step 3: Implement the bounded read contracts**

Define:

```go
type EvaluationListQuery struct {
    AppID, BotOpenID, ChatID, CohortID, Status, Winner string
    NeedsReview *bool
    From, To time.Time
    CursorAnchorAt time.Time
    CursorID string
    Limit int
}

type EvaluationEpisodeDetail struct {
    Episode EvaluationEpisodeView `json:"episode"`
    Messages []EvaluationMessageView `json:"messages"`
    Outputs []EvaluationLaneOutputView `json:"outputs"`
    Feedback []EvaluationFeedbackView `json:"feedback"`
    Judgments []EvaluationJudgmentView `json:"judgments"`
}
```

Use explicit SELECT lists, join `evaluation_cohorts` for bot ownership, cap the limit at 100, and decode JSON columns as `json.RawMessage` without flattening the stored evidence.

- [ ] **Step 4: Run the GREEN tests**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/interfaces/webui/evaluation_workbench.go \
  internal/interfaces/webui/evaluation_workbench_test.go
git -c commit.gpgsign=false commit -m "feat: query evaluation workbench episodes"
```

### Task 2: Sensitive WebUI list and detail APIs

**Files:**
- Create: `internal/interfaces/webui/handlers_evaluations.go`
- Modify: `internal/interfaces/webui/module.go`
- Modify: `internal/interfaces/webui/server.go`
- Modify: `internal/interfaces/webui/server_test.go`
- Modify: `cmd/larkrobot/bootstrap.go`

- [ ] **Step 1: Write failing handler tests**

Test:

- `GET /api/evaluations` rejects access when WebUI auth is not configured;
- missing/invalid Bearer tokens return 401;
- valid auth parses bounded RFC3339 time filters and cursor;
- `GET /api/evaluations/{episodeID}` returns the complete bundle;
- invalid limit/time/cursor returns 400;
- store unavailable returns 503;
- a missing or foreign episode returns 404.

- [ ] **Step 2: Run the RED tests**

```bash
/root/.go/go1.26.1/bin/go test -run 'TestEvaluation(List|Detail|SensitiveAuth)' \
  -count=1 -tags=custom_skip_vips ./internal/interfaces/webui
```

Expected: 404 because the routes are not registered.

- [ ] **Step 3: Implement sensitive-read authentication and routes**

Register:

```go
mux.HandleFunc("GET /api/evaluations", s.handleListEvaluations)
mux.HandleFunc("GET /api/evaluations/{episodeID}", s.handleGetEvaluation)
```

Each handler must call a dedicated guard:

```go
func (s *Server) requireSensitiveRead(w http.ResponseWriter, r *http.Request) bool {
    if s.authToken == "" {
        writeError(w, http.StatusServiceUnavailable, "sensitive reads require webui auth_token")
        return false
    }
    if !s.checkBearer(r) {
        writeError(w, http.StatusUnauthorized, "unauthorized")
        return false
    }
    return true
}
```

Inject `AppID` and `BotOpenID` from bootstrap; never accept either identity from query parameters.

- [ ] **Step 4: Run handler and package tests**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
/root/.go/go1.26.1/bin/go test -count=1 -tags=custom_skip_vips \
  ./internal/interfaces/webui ./cmd/larkrobot
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/interfaces/webui/handlers_evaluations.go \
  internal/interfaces/webui/module.go internal/interfaces/webui/server.go \
  internal/interfaces/webui/server_test.go cmd/larkrobot/bootstrap.go
git -c commit.gpgsign=false commit -m "feat: expose evaluation workbench api"
```

### Task 3: Append-only human quality judgments

**Files:**
- Modify: `internal/interfaces/webui/evaluation_workbench.go`
- Modify: `internal/interfaces/webui/evaluation_workbench_test.go`
- Modify: `internal/interfaces/webui/handlers_evaluations.go`
- Modify: `internal/interfaces/webui/server.go`
- Modify: `internal/interfaces/webui/server_test.go`

- [ ] **Step 1: Write failing versioning and API tests**

Test two sequential human submissions produce versions 1 and 2, version 2 supersedes version 1, concurrent submissions cannot create duplicate versions, evaluator identity is required, enums/scores/tags are validated, and a foreign episode cannot be judged.

- [ ] **Step 2: Run RED tests**

```bash
/root/.go/go1.26.1/bin/go test -run 'TestEvaluationHumanJudgment' \
  -count=1 -tags=custom_skip_vips ./internal/interfaces/webui
```

Expected: build failure because the append method and POST route do not exist.

- [ ] **Step 3: Implement transactional append and handler**

Register:

```go
mux.HandleFunc("POST /api/evaluations/{episodeID}/judgments", s.handleAppendEvaluationJudgment)
```

The store transaction must:

1. lock the bot-owned episode row;
2. read the latest `source='human'` judgment with `FOR UPDATE`;
3. build `version=1` or `version=previous+1`;
4. set `supersedes_id` to the prior human judgment ID;
5. call the existing append-only judgment contract with a UUID-backed ID.

Accept `winner` as `control|candidate|tie`, scores as a JSON object, problem tags as a bounded unique string list, rationale up to 4000 runes, confidence `0..100`, and `needs_review`.

- [ ] **Step 4: Run GREEN, race, and PostgreSQL tests**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
/root/.go/go1.26.1/bin/go test -count=1 -tags=custom_skip_vips \
  ./internal/interfaces/webui

/root/.go/go1.26.1/bin/go test -race -count=1 -tags=custom_skip_vips \
  ./internal/interfaces/webui
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/interfaces/webui/evaluation_workbench.go \
  internal/interfaces/webui/evaluation_workbench_test.go \
  internal/interfaces/webui/handlers_evaluations.go \
  internal/interfaces/webui/server.go internal/interfaces/webui/server_test.go
git -c commit.gpgsign=false commit -m "feat: append human evaluation judgments"
```

### Task 4: Patch reconciliation health and operational handoff

**Files:**
- Modify: `internal/application/lark/agentcard/reconciler.go`
- Modify: `internal/application/lark/agentcard/reconciler_test.go`
- Modify: `cmd/larkrobot/bootstrap.go`
- Modify: `docs/operations/conversation-parallel-evaluation.md`
- Create: `docs/operations/agent-card-runtime.md`

- [ ] **Step 1: Write failing reconciler stats tests**

Assert stats include `running`, `workers`, `scanned`, `completed`, `failed`, `last_success_at`, and bounded `last_error`; `DynamicHealth` degrades only after a catalog/processor error and returns ready after a later successful sweep.

- [ ] **Step 2: Run RED tests**

```bash
/root/.go/go1.26.1/bin/go test -run '^TestPatchReconciler.*(Stats|Health)' \
  -count=1 -tags=custom_skip_vips ./internal/application/lark/agentcard
```

Expected: build failure because `Stats` and `DynamicHealth` do not exist.

- [ ] **Step 3: Implement lock-free counters and module exposure**

Use atomics for counts/running state and a mutex only for the bounded last-error string/timestamps. Attach `Stats: components.agentCardPatchReconciler.Stats` to the runtime module; expose dynamic health by making the reconciler satisfy `runtime.DynamicHealthProvider` through an adapter if importing runtime would create a cycle.

- [ ] **Step 4: Document API and rollout operations**

Document:

- required WebUI Bearer auth for evaluation content;
- list/detail/judgment examples;
- bot isolation and maximum query bounds;
- Agent Card `off|shadow|allowlist|on`;
- patch worker lease/retry interpretation;
- rollback semantics: old cards remain actionable while new authoring is disabled.

- [ ] **Step 5: Final verification**

```bash
git diff --check

BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
/root/.go/go1.26.1/bin/go test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' \
  ./internal/application/lark/agentcard \
  ./internal/interfaces/webui \
  ./internal/infrastructure/evaluationstore \
  ./cmd/larkrobot

/root/.go/go1.26.1/bin/go test -race -count=1 -tags=custom_skip_vips \
  ./internal/application/lark/agentcard ./internal/interfaces/webui

/root/.go/go1.26.1/bin/go vet -tags=custom_skip_vips \
  ./internal/application/lark/agentcard ./internal/interfaces/webui \
  ./internal/infrastructure/evaluationstore ./cmd/larkrobot

/root/.go/go1.26.1/bin/go build -o /tmp/betago-larkrobot-observability \
  -tags=custom_skip_vips ./cmd/larkrobot
```

Expected: all commands pass.

- [ ] **Step 6: Commit**

```bash
git add internal/application/lark/agentcard/reconciler.go \
  internal/application/lark/agentcard/reconciler_test.go \
  cmd/larkrobot/bootstrap.go docs/operations/conversation-parallel-evaluation.md \
  docs/operations/agent-card-runtime.md
git -c commit.gpgsign=false commit -m "feat: observe agent evaluation runtime"
```
