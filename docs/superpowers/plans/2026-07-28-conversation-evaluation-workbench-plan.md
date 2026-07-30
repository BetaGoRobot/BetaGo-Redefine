# Conversation Evaluation Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提供一个受保护的 WebUI 工作台，用时间段和群创建评测 cohort，浏览 Control/Candidate 前后向 episode、上下文差异、用户点评和 Judge 结果，并追加版本化人工评测。

**Architecture:** 后端在现有 `net/http + sonic` WebUI 模块中注入 EvaluationQueryService，不让 HTTP handler 直接拼 GORM。敏感评测 GET/POST 全部要求 Bearer token；未配置 token 时端点显式禁用。Vue 前端新增 cohort 列表、episode 列表和三栏详情视图，复用多 bot 代理和 Element Plus。

**Tech Stack:** Go 1.25, net/http, PostgreSQL query service, Vue 3, TypeScript, Pinia, Vue Router, Element Plus, Vite, Vitest/typecheck

---

## 0. Dependencies

- Requires `docs/superpowers/plans/2026-07-28-conversation-parallel-evaluation-plan.md`.
- No database schema changes belong in this plan.
- Evaluation endpoints expose complete conversation content; anonymous GET behavior used by ordinary dashboard endpoints must not apply.
- Frontend tasks must be tested at mobile, tablet, and desktop widths during execution.

## 1. File map

### Backend

- Create `internal/interfaces/webui/evaluation_service.go`: query/write interface and DTO mapping。
- Create `internal/interfaces/webui/handlers_evaluations.go`: cohort/episode/judgment handlers。
- Modify `internal/interfaces/webui/module.go`: inject EvaluationService。
- Modify `internal/interfaces/webui/server.go`: sensitive routes and auth guard。
- Modify `internal/interfaces/webui/types.go`: API DTOs。
- Modify `cmd/larkrobot/bootstrap.go`: inject evaluation store service。
- Test `internal/interfaces/webui/evaluation_service_test.go`
- Test `internal/interfaces/webui/server_test.go`

### Frontend

- Modify `webui/src/api/types.ts`: evaluation DTOs。
- Modify `webui/src/api/client.ts`: API methods。
- Modify `webui/src/router.ts`: evaluation routes。
- Modify `webui/src/App.vue`: navigation。
- Create `webui/src/views/EvaluationCohorts.vue`
- Create `webui/src/views/EvaluationEpisodes.vue`
- Create `webui/src/views/EvaluationEpisodeDetail.vue`
- Create `webui/src/components/evaluation/EpisodeTimeline.vue`
- Create `webui/src/components/evaluation/ContextDiff.vue`
- Create `webui/src/components/evaluation/LaneComparison.vue`
- Create `webui/src/components/evaluation/JudgmentForm.vue`

## Task 1: Add authenticated evaluation HTTP contracts

**Files:**
- Modify: `internal/interfaces/webui/types.go`
- Create: `internal/interfaces/webui/evaluation_service.go`
- Modify: `internal/interfaces/webui/module.go`
- Modify: `internal/interfaces/webui/server.go`
- Test: `internal/interfaces/webui/server_test.go`

- [ ] **Step 1: Write failing auth tests**

Cover:

- token not configured: `GET /api/evaluation-cohorts` returns 503;
- token configured but missing/invalid: returns 401 even though method is GET;
- valid token: request reaches service;
- normal `GET /api/chats` keeps existing behavior.

- [ ] **Step 2: Define the injected interface**

```go
type EvaluationService interface {
    CreateCohort(context.Context, CreateEvaluationCohortRequest) (EvaluationCohortView, error)
    ListCohorts(context.Context, EvaluationCohortFilter) (EvaluationCohortPage, error)
    GetCohort(context.Context, string) (EvaluationCohortView, error)
    ListEpisodes(context.Context, EvaluationEpisodeFilter) (EvaluationEpisodePage, error)
    GetEpisode(context.Context, string) (EvaluationEpisodeDetail, error)
    AppendHumanJudgment(context.Context, string, HumanJudgmentRequest) (EvaluationJudgmentView, error)
    RequestRejudge(context.Context, string) error
}
```

- [ ] **Step 3: Register routes**

```text
POST /api/evaluation-cohorts
GET  /api/evaluation-cohorts
GET  /api/evaluation-cohorts/{cohortID}
GET  /api/evaluation-episodes
GET  /api/evaluation-episodes/{episodeID}
POST /api/evaluation-episodes/{episodeID}/judgments
POST /api/evaluation-episodes/{episodeID}/rejudge
```

- [ ] **Step 4: Add `requireSensitiveAuth`**

The guard must return 503 when `authToken==""`, 401 for an invalid bearer, and call the handler only for a valid token. Apply it only to evaluation routes.

- [ ] **Step 5: Test and commit**

```bash
go test ./internal/interfaces/webui -run 'TestEvaluation|TestSensitiveAuth'
git add internal/interfaces/webui
git -c commit.gpgsign=false commit -m "feat: add protected evaluation api"
```

## Task 2: Implement cohort and episode query handlers

**Files:**
- Create: `internal/interfaces/webui/handlers_evaluations.go`
- Modify: `internal/interfaces/webui/evaluation_service.go`
- Modify: `internal/interfaces/webui/server_test.go`
- Modify: `cmd/larkrobot/bootstrap.go`

- [ ] **Step 1: Write failing request-validation tests**

Validate:

- `start_at < end_at`;
- at least one chat ID;
- serving lane `control|candidate`;
- cursor limit 20/50/100;
- RFC3339 time filters;
- judgment winner `A|B|tie`;
- dimension scores 0–100;
- missing cohort/episode returns 404;
- repository failure returns 500 without leaking SQL.

- [ ] **Step 2: Define response DTOs**

`EvaluationEpisodeDetail` must contain:

```go
type EvaluationEpisodeDetail struct {
    Episode   EvaluationEpisodeView   `json:"episode"`
    PreWindow []EvaluationMessageView `json:"pre_window"`
    Anchor    EvaluationMessageView   `json:"anchor"`
    PostWindow []EvaluationMessageView `json:"post_window"`
    Control   EvaluationLaneView      `json:"control"`
    Candidate EvaluationLaneView      `json:"candidate"`
    Feedback  []EvaluationFeedbackView `json:"feedback"`
    Judgments []EvaluationJudgmentView `json:"judgments"`
}
```

- [ ] **Step 3: Implement cursor pagination**

Use `(anchor_at, id)` cursor, not offset pagination. Encode cursor as base64url JSON and reject malformed cursors with 400.

- [ ] **Step 4: Wire the real service**

Construct it from `evaluationstore.Repository` and `evaluationindex.Store` in bootstrap. PostgreSQL is authoritative; OpenSearch may accelerate filtering but handler must fall back to PostgreSQL when unavailable.

- [ ] **Step 5: Run and commit**

```bash
BETAGO_CONFIG_PATH=.dev/config.toml go test ./internal/interfaces/webui ./cmd/larkrobot
git add internal/interfaces/webui cmd/larkrobot
git -c commit.gpgsign=false commit -m "feat: expose evaluation cohort and episode queries"
```

## Task 3: Add frontend API types and routes

**Files:**
- Modify: `webui/src/api/types.ts`
- Modify: `webui/src/api/client.ts`
- Modify: `webui/src/router.ts`
- Modify: `webui/src/App.vue`

- [ ] **Step 1: Add exact TypeScript DTOs**

Define `EvaluationCohort`, `EvaluationEpisodeSummary`, `EvaluationEpisodeDetail`, `EvaluationLane`, `EvaluationFeedback`, `EvaluationJudgment`, cursor page and filter types matching backend JSON.

- [ ] **Step 2: Add BotApi methods**

```ts
createEvaluationCohort(req)
listEvaluationCohorts(params)
getEvaluationCohort(id)
listEvaluationEpisodes(params)
getEvaluationEpisode(id)
appendHumanJudgment(id, req)
requestEvaluationRejudge(id)
```

All calls use the selected bot’s existing bearer token.

- [ ] **Step 3: Register lazy-loaded routes**

```ts
{ path: '/evaluations', name: 'evaluation-cohorts', component: () => import('./views/EvaluationCohorts.vue') }
{ path: '/evaluations/:cohortID', name: 'evaluation-episodes', component: () => import('./views/EvaluationEpisodes.vue') }
{ path: '/evaluations/:cohortID/episodes/:episodeID', name: 'evaluation-episode-detail', component: () => import('./views/EvaluationEpisodeDetail.vue') }
```

- [ ] **Step 4: Add the navigation entry and typecheck**

```bash
npm --prefix webui run build
```

Expected: TypeScript/Vite build passes.

- [ ] **Step 5: Commit**

```bash
git add webui/src/api webui/src/router.ts webui/src/App.vue
git -c commit.gpgsign=false commit -m "feat: add evaluation frontend routes"
```

## Task 4: Build cohort and episode list views

**Files:**
- Create: `webui/src/views/EvaluationCohorts.vue`
- Create: `webui/src/views/EvaluationEpisodes.vue`

- [ ] **Step 1: Build cohort creation and list**

The page must support:

- chat multi-select;
- RFC3339-local time range;
- serving lane;
- version and sampling summary;
- status chips;
- result version;
- pagination and empty/error/loading states.

- [ ] **Step 2: Build episode filters**

Filters:

```text
chat | time | join disagreement | topic disagreement |
context diff | feedback type | needs review | judge winner
```

Show anchor preview, Control/Candidate decisions, serving lane, explicit feedback count and latest Judge result.

- [ ] **Step 3: Make both pages responsive**

At `<768px`, collapse filters into a drawer and render rows as cards. At desktop widths use a table. All interactive targets must be at least 44px high.

- [ ] **Step 4: Build and visually inspect**

```bash
npm --prefix webui run build
```

Inspect at 390×844, 768×1024 and 1440×900. Expected: no horizontal page overflow; long message previews wrap.

- [ ] **Step 5: Commit**

```bash
git add webui/src/views/EvaluationCohorts.vue webui/src/views/EvaluationEpisodes.vue
git -c commit.gpgsign=false commit -m "feat: add evaluation cohort browser"
```

## Task 5: Build the episode timeline and context diff

**Files:**
- Create: `webui/src/views/EvaluationEpisodeDetail.vue`
- Create: `webui/src/components/evaluation/EpisodeTimeline.vue`
- Create: `webui/src/components/evaluation/ContextDiff.vue`
- Create: `webui/src/components/evaluation/LaneComparison.vue`

- [ ] **Step 1: Render the semantic timeline**

Display pre-window, highlighted anchor, and post-window. Mark direct replies, reactions, card actions, corrections, tool results and late feedback. Do not infer sentiment in the UI; show stored type/confidence.

- [ ] **Step 2: Render context differences**

Use message/document IDs to classify:

```text
selected by both | Control only | Candidate only | excluded by both
```

Show exclusion reason, retrieval score, token estimate and truncation marker. Provide a raw prompt drawer behind an explicit click.

- [ ] **Step 3: Render anonymous lane comparison**

Default to A/B labels. A reviewer may reveal Control/Candidate after saving a judgment. Show join/topic/tool plan, reply, latency/token/error, and serving/shadow badge.

- [ ] **Step 4: Add responsive layout**

Desktop: timeline left, comparison center, judgment right. Tablet: two columns. Mobile: stacked tabs preserving anchor visibility.

- [ ] **Step 5: Build and commit**

```bash
npm --prefix webui run build
git add webui/src/views/EvaluationEpisodeDetail.vue webui/src/components/evaluation
git -c commit.gpgsign=false commit -m "feat: visualize evaluation episodes"
```

## Task 6: Add versioned human judgment and Judge calibration

**Files:**
- Create: `webui/src/components/evaluation/JudgmentForm.vue`
- Modify: `webui/src/views/EvaluationEpisodeDetail.vue`
- Modify: `internal/interfaces/webui/server_test.go`

- [ ] **Step 1: Implement the form**

Fields:

- winner A/B/tie;
- eight 0–100 dimensions;
- multi-select problem tags;
- notes/rationale;
- “Judge 判断错误” calibration marker.

Submitting always appends a version; never edit an existing judgment.

- [ ] **Step 2: Add conflict visibility**

Show current human vs Judge winner, score deltas and `needs_review`. Rejudge is an explicit action requiring confirmation and bearer auth.

- [ ] **Step 3: Test error/retry behavior**

On 409 version conflict, reload judgments and preserve unsaved notes locally. On network failure, keep form data.

- [ ] **Step 4: Build and commit**

```bash
npm --prefix webui run build
go test ./internal/interfaces/webui
git add webui/src/components/evaluation/JudgmentForm.vue webui/src/views/EvaluationEpisodeDetail.vue internal/interfaces/webui/server_test.go
git -c commit.gpgsign=false commit -m "feat: add human evaluation workflow"
```

## Task 7: End-to-end verification and operations

**Files:**
- Create: `docs/operations/conversation-evaluation-workbench.md`
- Modify: `webui/README.md`

- [ ] **Step 1: Run backend tests**

```bash
go test -race ./internal/interfaces/webui ./internal/infrastructure/evaluationstore
```

Expected: PASS.

- [ ] **Step 2: Run frontend verification**

```bash
npm --prefix webui run build
```

Expected: PASS.

- [ ] **Step 3: Verify auth manually**

Confirm evaluation GET without token is 401 when configured and 503 when auth token is absent; confirm ordinary read-only dashboard remains available under its existing policy.

- [ ] **Step 4: Document reviewer workflow**

Document cohort creation, filters, anonymized review, context diff interpretation, late feedback, human versioning, rejudge and calibration export.

- [ ] **Step 5: Commit**

```bash
git add docs/operations/conversation-evaluation-workbench.md webui/README.md
git -c commit.gpgsign=false commit -m "docs: explain evaluation workbench"
```

## Completion gate

- A reviewer can create a chat/time cohort and inspect aligned episodes.
- Pre/anchor/post messages and late feedback are visible.
- Context selection differences and exclusion reasons are visible.
- A/B remains anonymous until review submission.
- Human judgments are append-only versions.
- Sensitive endpoints never become anonymous.
- Mobile, tablet and desktop layouts remain usable.
