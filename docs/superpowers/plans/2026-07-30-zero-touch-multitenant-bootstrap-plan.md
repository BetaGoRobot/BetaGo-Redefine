# Zero-Touch Multi-Tenant Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让每个 Bot 只靠 TOML 和重启即可自动获得隔离的 PostgreSQL schema、OpenSearch index/alias 和滚动评测 cohort。

**Architecture:** 启动根先构造不可伪造的 `Tenant`，再由内嵌 migration runner 和 OpenSearch provisioner 幂等准备基础设施。所有 repository、queue、callback 和 projection 都绑定 Tenant；Evaluation rollout 在首条符合配置的群消息上自动确保时间桶 cohort。

**Tech Stack:** Go 1.26、GORM/PostgreSQL advisory lock、OpenSearch Go v4 typed client、TOML、`go:embed`、现有 runtime module/health framework。

**Execution constraint:** 当前会话禁止子代理，使用 `superpowers:executing-plans` 在
`.worktrees/conversation-runtime` 顺序执行。

---

### Task 1: Define the tenant and rollout contracts

**Files:**
- Create: `internal/application/lark/tenant/tenant.go`
- Test: `internal/application/lark/tenant/tenant_test.go`
- Modify: `internal/infrastructure/config/configs.go`
- Modify: `internal/runtime/settings.go`
- Test: `internal/runtime/settings_test.go`
- Modify: `deploy/config.example.toml`

- [ ] **Step 1: Write failing tenant identity tests**

Cover:

```go
func TestNewTenantIsStableAndSeparatesBots(t *testing.T)
func TestNewTenantRejectsIncompleteIdentity(t *testing.T)
func TestTenantDocumentIDNamespacesDomainIDs(t *testing.T)
func TestTenantIndexAliasSanitizesAndSeparatesBots(t *testing.T)
```

The expected tenant ID format is `bot_` plus 24 lowercase hex characters. Two
different `bot_open_id` values must produce different IDs, aliases and document
IDs even when base names and domain IDs are identical.

- [ ] **Step 2: Run RED**

Run:

```bash
/root/.go/go1.26.1/bin/go test -tags custom_skip_vips \
  ./internal/application/lark/tenant ./internal/runtime
```

Expected: FAIL because the tenant package and Evaluation rollout settings do not
exist.

- [ ] **Step 3: Implement immutable tenant identity**

Define:

```go
type Tenant struct {
    ID        string
    AppID     string
    BotOpenID string
}

func New(appID, botOpenID string) (Tenant, error)
func (t Tenant) Validate() error
func (t Tenant) IndexAlias(base string) (string, error)
func (t Tenant) DocumentID(domainID string) (string, error)
```

Use SHA-256 over canonical `appID + "\x00" + botOpenID`. Validate OpenSearch
base names without accepting caller-provided tenant suffixes.

- [ ] **Step 4: Add fail-closed Evaluation settings**

Add RuntimeConfig fields:

```go
EvaluationMode                string
EvaluationChatIDs             []string
EvaluationCohortDurationHours int
```

Add `EvaluationRolloutSettings` with `off | allowlist | on`, default `off`.
Reject an empty allowlist and durations outside `1..168` hours. Provide:

```go
func (s EvaluationSettings) Enabled() bool
func (s EvaluationSettings) Allows(chatID string) bool
```

- [ ] **Step 5: Run GREEN and commit**

Run the Task 1 tests plus `git diff --check`, then commit:

```bash
git add internal/application/lark/tenant internal/infrastructure/config/configs.go \
  internal/runtime/settings.go internal/runtime/settings_test.go deploy/config.example.toml
git -c commit.gpgsign=false commit -m "feat: define tenant rollout contracts"
```

### Task 2: Add an embedded PostgreSQL migration runner

**Files:**
- Create: `script/sql/migrations.go`
- Create: `internal/infrastructure/schema/migrations.go`
- Create: `internal/infrastructure/schema/runner.go`
- Create: `internal/infrastructure/schema/tenant_backfill.go`
- Test: `internal/infrastructure/schema/runner_test.go`
- Create: `script/sql/20260730_runtime_tenant_prepare.sql`
- Create: `script/sql/20260730_runtime_tenant_constraints.sql`
- Modify: `cmd/larkrobot/bootstrap.go`
- Test: `cmd/larkrobot/bootstrap_test.go`

- [ ] **Step 1: Write failing migration contract tests**

Using the configured real PostgreSQL only in tests guarded by
`BETAGO_CONFIG_PATH`, create a temporary schema and verify:

```go
func TestRunnerBootstrapsEmptySchema(t *testing.T)
func TestRunnerIsIdempotent(t *testing.T)
func TestConcurrentRunnersApplyEachVersionOnce(t *testing.T)
func TestRunnerRejectsChecksumDrift(t *testing.T)
func TestRunnerDoesNotWrapConcurrentIndexInTransaction(t *testing.T)
```

The test must inspect `runtime_schema_migrations`, every required table, tenant
column, unique index and foreign key rather than relying only on runner return
values.

- [ ] **Step 2: Run RED**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  /root/.go/go1.26.1/bin/go test -tags custom_skip_vips \
  ./internal/infrastructure/schema
```

Expected: FAIL because the runner does not exist.

- [ ] **Step 3: Implement embedded migrations**

Define:

```go
type Migration struct {
    Version          string
    SQL              string
    NonTransactional bool
    Checksum         string
    Before           func(context.Context, *gorm.DB) error
}

type Runner struct {
    DB       *gorm.DB
    Schema   string
    Revision string
}

func (r *Runner) Apply(ctx context.Context) (Report, error)
```

Expose the existing raw files from `script/sql/migrations.go` using
`go:embed *.sql`, so the embedded source and human-readable SQL cannot drift.
Use a stable advisory-lock key and a ledger with
`version/checksum/applied_at/binary_revision`. Normalize legacy
`CREATE INDEX CONCURRENTLY` statements into explicitly non-transactional
migrations; do not execute multiple statements through GORM transaction APIs
when `NonTransactional` is true.

- [ ] **Step 4: Add tenant-hardening migration**

`20260730_runtime_tenant_prepare.sql` adds nullable tenant columns. A Go
`tenantBackfill` migration hook computes exactly the same SHA-256 tenant ID as
the application, updates roots, then propagates it through the foreign-key
graph. `20260730_runtime_tenant_constraints.sql` applies `NOT NULL`, tenant
uniques and composite foreign keys.

The sequence must:

- add and backfill `tenant_id` on every table listed in the design;
- derive root tenant IDs from `app_id + bot_open_id`;
- stop when orphan rows cannot be assigned;
- make tenant columns `NOT NULL`;
- add tenant-aware unique keys and composite foreign keys;
- preserve all existing IDs and rows;
- make every statement idempotent.

Do not require PostgreSQL extensions. The Go backfill selects distinct
`app_id/bot_open_id` roots, computes `tenant.New`, updates rows inside a
transaction, checks orphan counts, and only then lets the constraints migration
run.

- [ ] **Step 5: Register a critical schema module before repositories**

Add `runtime_schema` immediately after database initialization. Remove the
Evaluation “migration is not installed” probe. When any Agent/Card/Evaluation
feature is enabled, migration failure must stop startup.

- [ ] **Step 6: Run GREEN and commit**

Run schema tests, bootstrap ordering tests, `git diff --check`, then commit:

```bash
git add internal/infrastructure/schema cmd/larkrobot/bootstrap.go \
  cmd/larkrobot/bootstrap_test.go
git -c commit.gpgsign=false commit -m "feat: bootstrap runtime postgres schema"
```

### Task 3: Regenerate and tenant-scope PostgreSQL models

**Files:**
- Modify: `internal/infrastructure/db/model/agent_*.gen.go`
- Modify: `internal/infrastructure/db/model/evaluation_*.gen.go`
- Modify: `internal/infrastructure/db/query/agent_*.gen.go`
- Modify: `internal/infrastructure/db/query/evaluation_*.gen.go`
- Modify: `internal/infrastructure/db/query/gen.go`
- Modify: `cmd/generate/gorm-gen.go`
- Modify: `internal/application/lark/agentruntime/types.go`
- Modify: `internal/application/lark/conversationeval/types.go`
- Modify: `internal/application/lark/agentcard/surface.go`

- [ ] **Step 1: Apply migrations to the development PostgreSQL**

Use the application runner, not raw SQL:

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  /root/.go/go1.26.1/bin/go test -tags custom_skip_vips \
  ./internal/infrastructure/schema -run TestApplyConfiguredSchema -count=1
```

- [ ] **Step 2: Regenerate gorm-gen from canonical schema**

Make `cmd/generate/gorm-gen.go` read `BETAGO_CONFIG_PATH`, falling back to
`.dev/config.toml`. Run:

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  /root/.go/go1.26.1/bin/go run -tags custom_skip_vips \
  ./cmd/generate/gorm-gen.go
```

Do not hand-edit generated files. Verify every targeted model contains
`TenantID`.

- [ ] **Step 3: Add TenantID to domain contracts**

Domain objects that cross repository boundaries must include immutable
`TenantID`. Validation rejects missing tenant IDs and repository mappers compare
domain, row and bound repository tenant.

- [ ] **Step 4: Run compile checks and commit**

```bash
/root/.go/go1.26.1/bin/go test -run '^$' -tags custom_skip_vips \
  ./internal/infrastructure/db/... \
  ./internal/application/lark/agentruntime \
  ./internal/application/lark/conversationeval \
  ./internal/application/lark/agentcard
git diff --check
```

Commit generated and domain files together:

```bash
git -c commit.gpgsign=false commit -m "db: generate tenant runtime models"
```

### Task 4: Bind Agent Runtime and Agent Card repositories to tenants

**Files:**
- Modify: `internal/infrastructure/agentstore/repository.go`
- Modify: all repository files under `internal/infrastructure/agentstore/`
- Modify: `internal/infrastructure/agentcardstore/repository.go`
- Modify: all repository files under `internal/infrastructure/agentcardstore/`
- Modify: `cmd/larkrobot/bootstrap.go`
- Test: `internal/infrastructure/agentstore/tenant_test.go`
- Test: `internal/infrastructure/agentcardstore/tenant_test.go`

- [ ] **Step 1: Write failing two-tenant repository tests**

In one temporary PostgreSQL schema, create Tenant A and Tenant B with identical
logical session/run/step/surface/dedupe tokens. Verify:

- each repository only returns its own rows;
- queue claim and stale reclaim never take the other tenant’s work;
- callback token lookup cannot resolve across tenants;
- update/delete with another tenant’s ID returns the normal not-found error and
  never reveals that the ID exists for a different tenant;
- outbox claim is tenant-scoped.

- [ ] **Step 2: Run RED**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  /root/.go/go1.26.1/bin/go test -tags custom_skip_vips \
  ./internal/infrastructure/agentstore \
  ./internal/infrastructure/agentcardstore
```

- [ ] **Step 3: Change constructors and writes**

Replace:

```go
NewRepository(db)
```

with:

```go
NewRepository(db, tenant)
```

returning `(*Repository, error)`. Every create writes `tenant.ID`; callers cannot
supply a different value.

- [ ] **Step 4: Scope every read, mutation and lease**

Add tenant predicates to all GORM/gen operations, including raw SQL and
`FOR UPDATE SKIP LOCKED`. For child objects use both `tenant_id` and parent ID;
do not rely only on global UUID probability.

- [ ] **Step 5: Bind bootstrap services**

Construct Tenant once in `buildApp`, store it in `appComponents`, and inject it
into Agent/Card repositories, callback handlers and workers.

- [ ] **Step 6: Run GREEN, race-sensitive queue tests and commit**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  /root/.go/go1.26.1/bin/go test -tags custom_skip_vips \
  ./internal/infrastructure/agentstore \
  ./internal/infrastructure/agentcardstore \
  ./internal/application/lark/agentruntime \
  ./internal/application/lark/agentcard
```

Commit:

```bash
git -c commit.gpgsign=false commit -m "feat: isolate agent persistence by tenant"
```

### Task 5: Bind Evaluation persistence and automatic cohorts to tenants

**Files:**
- Modify: `internal/infrastructure/evaluationstore/repository.go`
- Modify: all repository files under `internal/infrastructure/evaluationstore/`
- Modify: `internal/application/lark/conversationeval/service.go`
- Create: `internal/application/lark/conversationeval/cohort_manager.go`
- Test: `internal/infrastructure/evaluationstore/tenant_test.go`
- Test: `internal/application/lark/conversationeval/cohort_manager_test.go`
- Modify: `internal/application/lark/messages/handler.go`
- Test: `internal/application/lark/messages/handler_test.go`

- [ ] **Step 1: Write failing tenant and cohort tests**

Cover:

```go
func TestEvaluationRepositoryRejectsCrossTenantEpisode(t *testing.T)
func TestCandidateClaimIsTenantScoped(t *testing.T)
func TestProjectionCursorIsTenantScoped(t *testing.T)
func TestEnsureCohortCreatesStableDailyBucket(t *testing.T)
func TestEnsureCohortIsConcurrentAndIdempotent(t *testing.T)
func TestAllowlistMessageCreatesEpisodeWithoutDynamicConfig(t *testing.T)
func TestOffModeCreatesNothing(t *testing.T)
```

- [ ] **Step 2: Run RED**

Run the Evaluation store, service and message handler packages with
`BETAGO_CONFIG_PATH`.

- [ ] **Step 3: Tenant-bind Evaluation repository**

Use the same constructor and invariant as Task 4. All cohort, episode, lane,
message, task, feedback, judgment, metrics, projection and work-queue SQL must
filter `tenant_id`.

- [ ] **Step 4: Implement automatic cohort manager**

Define:

```go
type CohortManager struct {
    Tenant   tenant.Tenant
    Store    CohortStore
    Settings runtime.EvaluationSettings
    Clock    func() time.Time
}

func (m *CohortManager) Ensure(
    ctx context.Context,
    chatID string,
    occurredAt time.Time,
) (*Cohort, error)
```

Use fixed UTC buckets, stable tenant IDs and `ON CONFLICT DO NOTHING`. Freeze
control/candidate version metadata at creation.

- [ ] **Step 5: Integrate rollout precedence**

Evaluate:

1. explicit dynamic false emergency override;
2. explicit dynamic true extension;
3. TOML mode/allowlist;
4. default off.

Only after a cohort is ensured may the service allocate pre-window context or
candidate work.

- [ ] **Step 6: Run GREEN and commit**

Commit:

```bash
git -c commit.gpgsign=false commit -m "feat: auto-manage tenant evaluation cohorts"
```

### Task 6: Auto-provision per-tenant OpenSearch indices

**Files:**
- Modify: `internal/infrastructure/opensearch/os.go`
- Create: `internal/infrastructure/opensearch/provisioner.go`
- Test: `internal/infrastructure/opensearch/provisioner_test.go`
- Modify: `script/opensearch/agent_conversation_events_v1.json`
- Modify: `script/opensearch/agent_conversation_evaluations_v1.json`
- Modify: `internal/infrastructure/conversationindex/store.go`
- Modify: `internal/infrastructure/conversationindex/writer.go`
- Test: `internal/infrastructure/conversationindex/store_test.go`
- Modify: `internal/infrastructure/evaluationindex/store.go`
- Test: `internal/infrastructure/evaluationindex/store_test.go`

- [ ] **Step 1: Write an HTTP-level fake OpenSearch test suite**

Use `httptest.Server` and a real v4 typed client. Cover:

- missing physical index and alias are created;
- repeated Ensure performs only reads;
- two tenants using the same base get distinct aliases;
- concurrent Ensure tolerates resource-already-exists;
- alias with multiple write indices fails;
- incompatible `_meta` or mapping fails;
- permission error fails;
- no dynamic auto-create request is relied upon.

- [ ] **Step 2: Run RED**

```bash
/root/.go/go1.26.1/bin/go test -tags custom_skip_vips \
  ./internal/infrastructure/opensearch \
  ./internal/infrastructure/conversationindex \
  ./internal/infrastructure/evaluationindex
```

- [ ] **Step 3: Expose the initialized typed client**

Replace package-global backend-only construction with an injected client
provider usable by the provisioner. Preserve existing read/write wrappers for
legacy callers.

- [ ] **Step 4: Implement provisioner**

Define schema descriptors with embedded mappings:

```go
type IndexSchema struct {
    Name    string
    Version int
    Base    string
    Mapping json.RawMessage
}

type ProvisionedIndex struct {
    TenantID string
    Alias    string
    Physical string
    Version  int
}

func (p *Provisioner) Ensure(
    ctx context.Context,
    tenant tenant.Tenant,
    schema IndexSchema,
) (ProvisionedIndex, error)
```

Use typed indices/alias APIs and validate `_meta`.

- [ ] **Step 5: Add tenant fields and scoped IDs/queries**

Both mapping files add keyword fields `tenant_id`, `app_id`, `bot_open_id` and:

```json
"_meta": {
  "schema_name": "agent_conversation_evaluations",
  "schema_version": 1
}
```

Conversation/Evaluation snapshots write tenant fields, namespace `_id`, and add
an immutable tenant term to every search.

- [ ] **Step 6: Run GREEN and commit**

Commit:

```bash
git -c commit.gpgsign=false commit -m "feat: provision tenant opensearch indices"
```

### Task 7: Make bootstrap zero-touch and fail closed

**Files:**
- Modify: `cmd/larkrobot/bootstrap.go`
- Modify: `cmd/larkrobot/bootstrap_test.go`
- Create: `cmd/larkrobot/bootstrap_e2e_test.go`
- Modify: `internal/runtime/health.go`
- Test: `internal/runtime/health_test.go`
- Modify: `docs/operations/conversation-callback-runtime.md`
- Modify: `docs/operations/conversation-parallel-evaluation.md`
- Modify: `docs/operations/agent-card-runtime.md`

- [ ] **Step 1: Write failing module-order and failure-policy tests**

Prove this order:

```text
database
runtime_schema
opensearch
tenant_indices
agent_runtime/card/evaluation repositories and workers
lark_ws/webui
```

When an enabled feature cannot migrate or provision, `App.Start` returns an
error and Lark ingress never starts. When all related modes are off, missing
OpenSearch remains disabled without touching indices.

- [ ] **Step 2: Add tenant bootstrap module**

`tenant_indices` provisions conversation events when Conversation Runtime/Card
is enabled and evaluation index when Evaluation is enabled. Store effective
aliases in `appComponents`; downstream code never reconstructs names.

- [ ] **Step 3: Make enabled persistence modules critical**

Criticality is configuration-derived. Remove all “migration missing → optional
degraded” behavior. Ready verifies workers and provisioned schema.

- [ ] **Step 4: Expose safe diagnostics**

Health stats include:

- tenant ID;
- latest PG migration version/checksum;
- effective aliases and physical indices;
- schema versions;
- cohort mode and allowlist count;
- last bootstrap success/error time.

Do not expose app secret, OpenSearch credentials or full Bot identifiers.

- [ ] **Step 5: Rewrite operations docs**

Delete required manual SQL/curl initialization. The only required operations are
TOML edit, deploy/restart and readiness check.

- [ ] **Step 6: Run GREEN and commit**

Commit:

```bash
git -c commit.gpgsign=false commit -m "feat: make tenant runtime bootstrap zero touch"
```

### Task 8: Prove production readiness

**Files:**
- Create: `internal/integration/zerotouch_bootstrap_test.go`

- [ ] **Step 1: Add fresh-environment end-to-end test**

Against a temporary PostgreSQL schema and HTTP fake OpenSearch:

1. start Bot A with only TOML-equivalent config;
2. verify migrations, tenant indices and readiness;
3. feed one allowlisted group message;
4. verify automatic cohort, Control/Candidate episode and projection;
5. start Bot B with the same base index names and logical IDs;
6. prove no PG/OpenSearch/API cross-tenant visibility;
7. restart both and prove idempotency.

- [ ] **Step 2: Run focused and real-infrastructure suites**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  /root/.go/go1.26.1/bin/go test -tags custom_skip_vips \
  ./internal/infrastructure/schema \
  ./internal/infrastructure/agentstore \
  ./internal/infrastructure/agentcardstore \
  ./internal/infrastructure/evaluationstore \
  ./internal/infrastructure/opensearch \
  ./internal/infrastructure/conversationindex \
  ./internal/infrastructure/evaluationindex \
  ./internal/application/lark/conversationeval \
  ./cmd/larkrobot
```

- [ ] **Step 3: Run mockey packages with required compiler flags**

```bash
/root/.go/go1.26.1/bin/go test -tags custom_skip_vips \
  -gcflags='all=-N -l' \
  ./internal/application/lark/agentcard \
  ./internal/interfaces/webui \
  ./cmd/larkrobot
```

Do not combine `-race` with mockey `-gcflags`.

- [ ] **Step 4: Run race, vet, build and user warmup**

```bash
/root/.go/go1.26.1/bin/go test -race -tags custom_skip_vips \
  ./internal/infrastructure/schema \
  ./internal/infrastructure/opensearch \
  ./internal/application/lark/conversationeval

/root/.go/go1.26.1/bin/go vet -tags custom_skip_vips \
  ./internal/infrastructure/schema \
  ./internal/infrastructure/opensearch \
  ./internal/infrastructure/agentstore \
  ./internal/infrastructure/agentcardstore \
  ./internal/infrastructure/evaluationstore \
  ./cmd/larkrobot

/root/.go/go1.26.1/bin/go build -tags custom_skip_vips \
  -o /tmp/betago-larkrobot-zerotouch ./cmd/larkrobot

/root/.go/go1.26.1/bin/go run \
  github.com/BetaGoRobot/go_utils/cmd/tools/warmup@master

git diff --check
```

- [ ] **Step 5: Audit every design requirement**

For each item in the design’s Verification section, link it to a passing test or
runtime inspection. Missing evidence is incomplete work.

- [ ] **Step 6: Commit final test/docs adjustments**

```bash
git -c commit.gpgsign=false commit -m "test: prove zero-touch tenant bootstrap"
```

### Task 9: Prepare delivery

**Files:**
- No production changes unless the completion audit finds a gap.

- [ ] **Step 1: Inspect branch state**

```bash
git status -sb
git log --oneline origin/feat/conversation-callback-runtime..HEAD
```

- [ ] **Step 2: Produce the final minimal production configuration**

The handoff must contain only TOML and readiness verification. It must not
contain required SQL or OpenSearch initialization commands.

- [ ] **Step 3: Push when credentials are available**

```bash
git push origin feat/conversation-callback-runtime
```

If authentication is unavailable, leave all commits intact and report the exact
unpublished commit range without claiming remote delivery.
