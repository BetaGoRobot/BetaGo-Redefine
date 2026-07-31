# Agentic Rollout WebUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add tenant-safe single-chat and batch WebUI controls for Conversation Runtime, callback continuation, parallel evaluation, and Agent Card, then refresh the existing chat list and detail pages with the approved warm Agent operations design.

**Architecture:** A new application-level `agenticrollout` service becomes the only effective-state resolver used by both runtime gates and WebUI handlers. It stores explicit chat/global overrides in the existing namespaced `dynamic_configs` table through an atomic Config Manager mutation API; absent rows mean inherit. Vue consumes dedicated typed endpoints, with single-bot batch operations, optimistic revisions, and reusable three-state components.

**Tech Stack:** Go 1.26.1, GORM/PostgreSQL, `net/http`, Sonic, Vue 3.5, TypeScript, Element Plus 2.x, Axios, Vitest 3.2, Vue Test Utils, happy-dom.

---

## File map

### Backend

- Create `internal/application/agenticrollout/types.go`: stable capability,
  override, state, request, result, and error types.
- Create `internal/application/agenticrollout/service.go`: source priority,
  readiness, revision, preview, and atomic apply behavior.
- Create `internal/application/agenticrollout/service_test.go`: resolver,
  revision, preset expansion, conflict, unavailable, and tenant-neutral service
  tests using injected fakes.
- Modify `internal/application/config/manager.go`: scoped reads, serialized
  atomic mutations, and post-commit cache updates.
- Modify `internal/application/config/manager_test.go`: explicit false,
  delete-as-inherit, rollback cache safety, and writer serialization tests.
- Modify `internal/application/config/definitions.go`: Agent Card key and
  rollout presentation metadata.
- Modify `internal/application/config/manager_test.go`: metadata compatibility
  assertions.
- Modify `internal/application/lark/agentruntime/runtime.go`: consume the
  shared callback gate without changing continuation behavior.
- Modify `internal/application/lark/messages/handler.go`: consume shared
  Runtime, Evaluation, and Agent Card gates.
- Modify `cmd/larkrobot/bootstrap.go`: construct one rollout service from
  normalized static settings and inject it into runtime and WebUI.
- Modify `cmd/larkrobot/bootstrap_test.go`: static readiness and runtime parity
  tests.
- Create `internal/interfaces/webui/handlers_agentic_rollout.go`: typed read,
  preview, and commit HTTP handlers.
- Create `internal/interfaces/webui/handlers_agentic_rollout_test.go`: API,
  auth, request limit, error mapping, and bot-bound response tests.
- Modify `internal/interfaces/webui/types.go`: rollout service interface and
  response aliases.
- Modify `internal/interfaces/webui/module.go`: rollout dependency option.
- Modify `internal/interfaces/webui/server.go`: dependency field, routes,
  metrics, and structured rollout errors.
- Modify `internal/interfaces/webui/handlers_config.go`: expose presentation
  metadata while retaining generic Config API compatibility.

### Frontend

- Modify `webui/package.json` and `webui/package-lock.json`: add Vitest 3.2.7,
  Vue Test Utils 2.4.11, and happy-dom 20.11.1.
- Create `webui/vitest.config.ts`: Vue plugin plus happy-dom test environment.
- Create `webui/src/api/agentic.ts`: rollout TypeScript types and pure state
  helpers.
- Create `webui/src/api/agentic.test.ts`: draft, summary, chunking, and preset
  tests.
- Modify `webui/src/api/types.ts`: export rollout API response types.
- Modify `webui/src/api/client.ts`: add typed BotApi rollout methods.
- Create `webui/src/components/AgenticStatusBadge.vue`: effective/source badge.
- Create `webui/src/components/AgenticCapabilityCard.vue`: accessible
  three-state control.
- Create `webui/src/components/AgenticRolloutPanel.vue`: single-chat draft and
  submit panel.
- Create `webui/src/components/AgenticBatchDrawer.vue`: dry-run and commit
  workflow.
- Create `webui/src/components/agentic-components.test.ts`: component behavior
  tests with Element Plus stubs.
- Modify `webui/src/views/ChatDetail.vue`: new rollout tab and approved visual
  treatment.
- Modify `webui/src/views/ChatList.vue`: single-bot selection, summaries,
  batch drawer, and approved visual treatment.

## Verification environment

Use the repository-defined toolchain for every Go command:

```bash
export BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml
export BETAGO_GO=/root/.go/go1.26.1/bin/go
```

All Go test commands include:

```bash
-count=1 -tags=custom_skip_vips -gcflags='all=-N -l'
```

Do not use the host-default Go 1.27rc2. Do not delete or flush the shared
development Redis to make unrelated baseline tests pass.

---

### Task 1: Add atomic dynamic-config mutations

**Files:**
- Modify: `internal/application/config/manager.go`
- Test: `internal/application/config/manager_test.go`

- [ ] **Step 1: Write failing scoped-read and cache-atomicity tests**

Add tests that use an injected persistence fake instead of package-level
function aliases:

```go
type mutationStoreFake struct {
	err      error
	applied  [][]persistedConfigMutation
	onApply  func()
}

func (f *mutationStoreFake) Apply(
	_ context.Context,
	mutations []persistedConfigMutation,
) error {
	copied := append([]persistedConfigMutation(nil), mutations...)
	f.applied = append(f.applied, copied)
	if f.onApply != nil {
		f.onApply()
	}
	return f.err
}

func TestGetScopedBoolOverrideDistinguishesAbsentAndFalse(t *testing.T) {
	manager := newManagerWithMutationStore(&mutationStoreFake{})
	manager.cache[buildConfigKey(
		ScopeChat, "oc_a", "", KeyConversationRuntimeEnabled,
	)] = "false"

	got, configured := manager.GetScopedBoolOverride(
		context.Background(),
		KeyConversationRuntimeEnabled,
		ScopeChat,
		"oc_a",
		"",
	)
	if !configured || got {
		t.Fatalf("got (%v, %v), want (false, true)", got, configured)
	}
}

func TestApplyConfigMutationsKeepsCacheUnchangedOnRollback(t *testing.T) {
	store := &mutationStoreFake{err: errors.New("write failed")}
	manager := newManagerWithMutationStore(store)
	value := "true"
	err := manager.ApplyConfigMutations(
		context.Background(),
		[]ConfigMutation{{
			Key: KeyConversationRuntimeEnabled, Scope: ScopeChat,
			ChatID: "oc_a", Value: &value,
		}},
		nil,
	)
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if _, ok := manager.cache[buildConfigKey(
		ScopeChat, "oc_a", "", KeyConversationRuntimeEnabled,
	)]; ok {
		t.Fatal("cache changed before transaction commit")
	}
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' ./internal/application/config \
  -run 'Test(GetScopedBoolOverride|ApplyConfigMutations)'
```

Expected: compile failure because `ConfigMutation`,
`newManagerWithMutationStore`, and the new methods do not exist.

- [ ] **Step 3: Implement the mutation primitive**

Add these public contracts and Manager fields:

```go
type ConfigMutation struct {
	Key    ConfigKey
	Scope  ConfigScope
	ChatID string
	OpenID string
	Value  *string // nil means delete/inherit
}

type persistedConfigMutation struct {
	FullKey string
	Value   *string
}

type configMutationStore interface {
	Apply(context.Context, []persistedConfigMutation) error
}

type Manager struct {
	cache           map[string]string
	mu              sync.RWMutex
	writeMu         sync.Mutex
	mutationStore   configMutationStore
	getFeaturesFunc func() []Feature
}
```

Implement:

```go
func (m *Manager) GetScopedBoolOverride(
	ctx context.Context,
	key ConfigKey,
	scope ConfigScope,
	chatID string,
	openID string,
) (bool, bool) {
	raw, ok := m.getConfig(ctx, scope, chatID, openID, key)
	if !ok {
		return false, false
	}
	value, err := strconv.ParseBool(raw)
	return value, err == nil
}

func (m *Manager) ApplyConfigMutations(
	ctx context.Context,
	mutations []ConfigMutation,
	guard func() error,
) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	if guard != nil {
		if err := guard(); err != nil {
			return err
		}
	}
	persisted, err := normalizeConfigMutations(mutations)
	if err != nil {
		return err
	}
	if err := m.mutationStore.Apply(ctx, persisted); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mutation := range persisted {
		if mutation.Value == nil {
			delete(m.cache, mutation.FullKey)
			continue
		}
		m.cache[mutation.FullKey] = *mutation.Value
	}
	return nil
}
```

The production store must perform all upserts and deletes inside one
`gorm.DB.Transaction`. Normalize and reject duplicate full keys before opening
the transaction. Refactor `SetString` and `DeleteConfig` to call this method
with one mutation so all Config Manager writers share `writeMu`.

- [ ] **Step 4: Add GREEN tests for delete, guard, and serialization**

Test that:

- a nil value removes the cache entry after successful persistence;
- a failing guard does not call the store;
- two concurrent calls never enter `mutationStoreFake.Apply` together;
- identical full keys in one batch return validation error.

- [ ] **Step 5: Run the config package**

Run:

```bash
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' ./internal/application/config
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/application/config/manager.go \
  internal/application/config/manager_test.go
git -c commit.gpgsign=false commit -m \
  "feat: apply dynamic config mutations atomically"
```

---

### Task 2: Build the shared Agentic rollout resolver

**Files:**
- Create: `internal/application/agenticrollout/types.go`
- Create: `internal/application/agenticrollout/service.go`
- Test: `internal/application/agenticrollout/service_test.go`

- [ ] **Step 1: Write resolver priority tests**

Define a fake implementing only scoped reads and guarded mutations. Cover:

```go
func TestResolveChatUsesChatOverrideBeforeGlobalAndStatic(t *testing.T) {
	store := newStoreFake()
	store.put(config.ScopeGlobal, "", config.KeyConversationParallelEvaluationEnabled, true)
	store.put(config.ScopeChat, "oc_a", config.KeyConversationParallelEvaluationEnabled, false)
	service := NewService(ServiceOptions{
		Store: store,
		Static: StaticPolicies{
			EvaluationAllows: func(string) bool { return true },
			AgentCardAllows:  func(string) bool { return false },
			AgentCardAvailable: true,
		},
	})

	state, err := service.ResolveChat(context.Background(), "oc_a")
	if err != nil {
		t.Fatal(err)
	}
	got := state.Capability(ParallelEvaluation)
	if got.Override != OverrideDisabled ||
		got.Effective ||
		got.Source != SourceChatOverride {
		t.Fatalf("unexpected state: %#v", got)
	}
}
```

Also cover default-off Runtime and callback, global source, TOML evaluation
allowlist, Agent Card off/shadow unavailability, and deterministic revision.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' ./internal/application/agenticrollout
```

Expected: package or symbols do not exist.

- [ ] **Step 3: Define stable domain types**

Use explicit string unions:

```go
type Capability string

const (
	ConversationRuntime Capability = "conversation_runtime"
	CallbackContinuation Capability = "callback_continuation"
	ParallelEvaluation Capability = "parallel_evaluation"
	AgentCard Capability = "agent_card"
)

type OverrideState string

const (
	OverrideInherit  OverrideState = "inherit"
	OverrideEnabled  OverrideState = "enabled"
	OverrideDisabled OverrideState = "disabled"
)

type Source string

const (
	SourceDefault        Source = "default"
	SourceTOML           Source = "toml"
	SourceGlobalConfig   Source = "global_config"
	SourceChatOverride   Source = "chat_override"
)

type CapabilityState struct {
	Key       Capability    `json:"key"`
	Label     string        `json:"label"`
	Override  OverrideState `json:"override"`
	Baseline  bool          `json:"baseline"`
	Effective bool          `json:"effective"`
	Source    Source        `json:"source"`
	Available bool          `json:"available"`
	Reason    string        `json:"reason,omitempty"`
}

type ChatState struct {
	ChatID       string            `json:"chat_id"`
	Revision     string            `json:"revision"`
	Capabilities []CapabilityState `json:"capabilities"`
}

type StaticPolicies struct {
	EvaluationAvailable      bool
	EvaluationAllows         func(string) bool
	AgentCardAvailable       bool
	AgentCardAllows          func(string) bool
	AgentCardUnavailableReason string
}
```

Add sentinel errors with stable codes: invalid request, stale revision,
unavailable capability, and persistence unavailable.

- [ ] **Step 4: Implement source resolution and revisions**

The service resolves each key in this order:

```text
chat explicit override
global explicit override
static policy
default false
```

Hash a canonical JSON payload containing chat ID and the four ordered
capability states with SHA-256. Do not hash labels or localized text.

- [ ] **Step 5: Write failing preview/apply tests**

Cover:

- partial changes preserve unlisted capabilities;
- `inherit` produces nil-value config mutations;
- Full Agentic is represented as four explicit changes;
- unavailable enabled state returns `ErrUnavailable`;
- stale expected revision returns `ErrStaleRevision`;
- a two-chat store failure leaves both states unchanged.

- [ ] **Step 6: Implement preview and apply**

Expose:

```go
type ChangeSet map[Capability]OverrideState

type BatchRequest struct {
	ChatIDs           []string
	ExpectedRevisions map[string]string
	Changes           ChangeSet
	DryRun            bool
}

func (s *Service) ResolveChat(context.Context, string) (ChatState, error)
func (s *Service) ResolveChats(context.Context, []string) ([]ChatState, error)
func (s *Service) Apply(context.Context, BatchRequest) (BatchResult, error)
func (s *Service) RuntimeEnabled(context.Context, string) bool
func (s *Service) CallbackContinuationEnabled(context.Context, string) bool
func (s *Service) EvaluationEnabled(context.Context, string) bool
func (s *Service) AgentCardEnabled(context.Context, string) bool
```

For commit, pass a guard to `ApplyConfigMutations`; inside the guard, recompute
all current revisions and availability. For dry-run, run the same validation
and return before persistence.

- [ ] **Step 7: Run package tests**

Run:

```bash
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' ./internal/application/agenticrollout
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/application/agenticrollout
git -c commit.gpgsign=false commit -m \
  "feat: resolve agentic rollout policy"
```

---

### Task 3: Add Agent Card metadata and wire runtime parity

**Files:**
- Modify: `internal/application/config/manager.go`
- Modify: `internal/application/config/definitions.go`
- Test: `internal/application/config/manager_test.go`
- Modify: `internal/application/lark/messages/handler.go`
- Modify: `internal/application/lark/agentruntime/runtime.go`
- Modify: `cmd/larkrobot/bootstrap.go`
- Test: `cmd/larkrobot/bootstrap_test.go`

- [ ] **Step 1: Write failing key and readiness tests**

Assert that:

```go
def, ok := config.GetConfigDefinition(config.KeyAgentCardEnabled)
if !ok || def.ValueType != "bool" ||
	def.ManagementSurface != config.ManagementSurfaceAgenticRollout {
	t.Fatalf("unexpected definition: %#v, %v", def, ok)
}
```

Bootstrap tests must prove:

- evaluation `off` is unavailable;
- evaluation `allowlist` with an empty list is available but defaults off;
- Agent Card `off` and `shadow` cannot be WebUI-promoted;
- Agent Card `allowlist` with an empty list is available but defaults off.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```bash
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' ./internal/application/config ./cmd/larkrobot \
  -run 'Test.*(AgentCardEnabled|RolloutService|RolloutReadiness)'
```

Expected: missing key, metadata, and bootstrap service construction.

- [ ] **Step 3: Add config metadata**

Add:

```go
const KeyAgentCardEnabled ConfigKey = "agent_card_enabled"

type ManagementSurface string

const (
	ManagementSurfaceGeneric ManagementSurface = ""
	ManagementSurfaceAgenticRollout ManagementSurface = "agentic_rollout"
)
```

Add `ManagementSurface` to `ConfigDefinition`. Mark the four rollout keys with
`ManagementSurfaceAgenticRollout`; preserve `GetAllConfigKeys` and generic API
visibility.

- [ ] **Step 4: Construct one rollout service in bootstrap**

Build `StaticPolicies` from normalized settings:

```go
Static: agenticrollout.StaticPolicies{
	EvaluationAvailable: evaluationSettings.Enabled(),
	EvaluationAllows:    evaluationSettings.Allows,
	AgentCardAvailable:  agentCardSettings.ToolsAvailable() &&
		!agentCardSettings.Shadow(),
	AgentCardAllows: agentCardSettings.CanSend,
	AgentCardUnavailableReason: agentCardUnavailableReason(agentCardSettings),
},
```

Store the service on `appComponents`. Pass its callback gate to
`agentruntime.NewRuntime`. Pass its Runtime, Evaluation, and Agent Card gates
to `messages.NewMessageProcessorWithOptions`.

Replace every live Agent Card send check with the same dynamic gate, including
the capability service construction:

```go
CanSend: func(chatID string) bool {
	return rollouts.AgentCardEnabled(context.Background(), chatID)
},
```

Keep evaluation lazy provisioning after `EvaluationEnabled` succeeds:

```go
EvaluationEnabled: func(ctx context.Context, chatID string) bool {
	if !rollouts.EvaluationEnabled(ctx, chatID) {
		return false
	}
	if err := ensureEvaluationSearchIndex(ctx, cfg, components); err != nil {
		logs.L().Ctx(ctx).Error("ensure evaluation search index failed", zap.Error(err))
		return false
	}
	return true
},
```

Set evaluation cohort admission to:

```go
EnsureCohortForChat: func(chatID string) bool {
	return rollouts.EvaluationEnabled(context.Background(), chatID)
},
```

Delete `evaluationRolloutAllows` only after every caller uses the shared
service.

- [ ] **Step 5: Run affected runtime tests**

Run:

```bash
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' \
  ./internal/application/config \
  ./internal/application/agenticrollout \
  ./internal/application/lark/messages \
  ./internal/application/lark/agentruntime \
  ./cmd/larkrobot
```

Expected: PASS with existing callback and evaluation behavior unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/application/config \
  internal/application/lark/messages/handler.go \
  internal/application/lark/agentruntime/runtime.go \
  cmd/larkrobot/bootstrap.go cmd/larkrobot/bootstrap_test.go
git -c commit.gpgsign=false commit -m \
  "feat: share rollout policy across runtime gates"
```

---

### Task 4: Expose tenant-bound rollout APIs

**Files:**
- Create: `internal/interfaces/webui/handlers_agentic_rollout.go`
- Create: `internal/interfaces/webui/handlers_agentic_rollout_test.go`
- Modify: `internal/interfaces/webui/types.go`
- Modify: `internal/interfaces/webui/module.go`
- Modify: `internal/interfaces/webui/server.go`
- Modify: `internal/interfaces/webui/handlers_config.go`
- Modify: `cmd/larkrobot/bootstrap.go`

- [ ] **Step 1: Write failing handler tests**

Use an injected `fakeAgenticRolloutService`. Test:

```go
func TestAgenticBatchRejectsCrossBotFieldsAndRequiresAuth(t *testing.T) {
	srv := NewServer(Options{
		Config: &infraConfig.WebUIConfig{AuthToken: "secret"},
		AgenticRollouts: &fakeAgenticRolloutService{},
		BotID: "bot-a",
	}, nil)
	body := `{
		"dry_run":false,
		"chat_ids":["oc_1"],
		"bot_id":"bot-b",
		"changes":{"conversation_runtime":"enabled"}
	}`
	req := httptest.NewRequest(
		http.MethodPost, "/api/agentic-rollouts/batch",
		strings.NewReader(body),
	)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
```

Also cover unknown JSON fields, more than 100 read IDs, more than 200 write
IDs, 409 mapping, 422 mapping, stable error codes, and response bot identity.

- [ ] **Step 2: Run handler tests and verify RED**

Run:

```bash
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' ./internal/interfaces/webui \
  -run 'TestAgentic'
```

Expected: missing routes and dependency.

- [ ] **Step 3: Add the service interface and routes**

Add to `webui.Options` and `Server`:

```go
type AgenticBotView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AgenticRolloutView struct {
	agenticrollout.ChatState
	Bot AgenticBotView `json:"bot"`
}

type AgenticRolloutService interface {
	ResolveChat(context.Context, string) (agenticrollout.ChatState, error)
	ResolveChats(context.Context, []string) ([]agenticrollout.ChatState, error)
	Apply(
		context.Context,
		agenticrollout.BatchRequest,
	) (agenticrollout.BatchResult, error)
}
```

Register:

```go
mux.HandleFunc(
	"GET /api/chats/{chatID}/agentic-rollout",
	s.handleGetAgenticRollout,
)
mux.HandleFunc(
	"PUT /api/chats/{chatID}/agentic-rollout",
	s.handlePutAgenticRollout,
)
mux.HandleFunc(
	"GET /api/agentic-rollouts",
	s.handleListAgenticRollouts,
)
mux.HandleFunc(
	"POST /api/agentic-rollouts/batch",
	s.handleBatchAgenticRollouts,
)
```

- [ ] **Step 4: Implement strict decode and error mapping**

Use Sonic's existing decoder unknown-field rejection:

```go
decoder := sonic.ConfigDefault.NewDecoder(r.Body)
decoder.DisallowUnknownFields()
if err := decoder.Decode(&request); err != nil {
	writeRolloutError(
		w,
		http.StatusBadRequest,
		"invalid_request",
		"invalid request body: "+err.Error(),
	)
	return
}
```

This rejects `bot_id`, `tenant_id`, `app_id`, `bot_open_id`, and every other
field outside the typed contract.

Return:

```json
{"code":"stale_revision","error":"rollout state changed; reload and retry"}
```

Map domain errors to 400, 409, 422, and 503. Do not accept `bot_id`,
`tenant_id`, `app_id`, or `bot_open_id`.

- [ ] **Step 5: Add rollout observability**

Record these stable metrics:

```text
betago_webui_agentic_rollout_reads_total{scope="single|batch"}
betago_webui_agentic_rollout_mutations_total{dry_run="true|false",status="ok|error"}
betago_webui_agentic_rollout_chats_total{operation="preview|commit"}
betago_webui_agentic_rollout_conflicts_total
betago_webui_agentic_rollout_unavailable_total{capability="..."}
```

Log request ID, hashed bot namespace, chat count, capability keys, and domain
error code with Zap. Do not log bearer tokens, App Secret, card bodies, or
message content.

- [ ] **Step 6: Add config presentation metadata**

Include `management_surface` in `ConfigView`. Keep all definitions in the
generic Config API response; frontend filtering happens by metadata, so
existing API consumers remain compatible.

- [ ] **Step 7: Inject the service from bootstrap and run tests**

Run:

```bash
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' \
  ./internal/interfaces/webui ./cmd/larkrobot
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/interfaces/webui cmd/larkrobot/bootstrap.go
git -c commit.gpgsign=false commit -m \
  "feat: expose agentic rollout webui api"
```

---

### Task 5: Establish the frontend test and typed API layer

**Files:**
- Modify: `webui/package.json`
- Modify: `webui/package-lock.json`
- Create: `webui/vitest.config.ts`
- Create: `webui/src/api/agentic.ts`
- Create: `webui/src/api/agentic.test.ts`
- Modify: `webui/src/api/types.ts`
- Modify: `webui/src/api/client.ts`

- [ ] **Step 1: Install the verified compatible test stack**

Run:

```bash
npm install --save-dev \
  vitest@3.2.7 @vue/test-utils@2.4.11 happy-dom@20.11.1
```

Add:

```json
{
  "scripts": {
    "test": "vitest run",
    "test:watch": "vitest"
  }
}
```

Create:

```ts
// webui/vitest.config.ts
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'happy-dom',
    restoreMocks: true,
  },
})
```

- [ ] **Step 2: Write failing pure helper tests**

```ts
import { describe, expect, it } from 'vitest'
import {
  buildFullAgenticChanges,
  chunkChatIDs,
  summarizeRollout,
  type AgenticCapabilityState,
} from './agentic'

const inheritedOffCapabilities = [
  'conversation_runtime',
  'callback_continuation',
  'parallel_evaluation',
  'agent_card',
].map((key) => ({
  key,
  label: key,
  override: 'inherit',
  baseline: false,
  effective: false,
  source: 'default',
  available: true,
})) as AgenticCapabilityState[]

describe('agentic rollout helpers', () => {
  it('restores all capabilities to inherit', () => {
    expect(buildFullAgenticChanges('inherit')).toEqual({
      conversation_runtime: 'inherit',
      callback_continuation: 'inherit',
      parallel_evaluation: 'inherit',
      agent_card: 'inherit',
    })
  })

  it('chunks rollout reads at 100 chats', () => {
    expect(chunkChatIDs(
      Array.from({ length: 201 }, (_, i) => `oc_${i}`),
    ).map((chunk) => chunk.length)).toEqual([100, 100, 1])
  })

  it('labels inherited disabled state explicitly', () => {
    expect(summarizeRollout({
      bot: { id: 'bot-a', name: 'Bot A' },
      chat_id: 'oc_1',
      revision: 'r1',
      capabilities: inheritedOffCapabilities,
    })).toBe('4 项继承关闭')
  })
})
```

- [ ] **Step 3: Run tests and verify RED**

Run:

```bash
npm test -- src/api/agentic.test.ts
```

Expected: missing module/types.

- [ ] **Step 4: Implement explicit TypeScript contracts and helpers**

Define:

```ts
export type AgenticCapabilityKey =
  | 'conversation_runtime'
  | 'callback_continuation'
  | 'parallel_evaluation'
  | 'agent_card'

export type AgenticOverride = 'inherit' | 'enabled' | 'disabled'
export type AgenticSource =
  | 'default'
  | 'toml'
  | 'global_config'
  | 'chat_override'

export interface AgenticCapabilityState {
  key: AgenticCapabilityKey
  label: string
  override: AgenticOverride
  baseline: boolean
  effective: boolean
  source: AgenticSource
  available: boolean
  reason?: string
}

export interface AgenticChatState {
  bot: {
    id: string
    name: string
  }
  chat_id: string
  revision: string
  capabilities: AgenticCapabilityState[]
}

export interface AgenticUpdateRequest {
  expected_revision: string
  changes: Partial<Record<AgenticCapabilityKey, AgenticOverride>>
}

export interface AgenticBatchRequest {
  dry_run: boolean
  chat_ids: string[]
  expected_revisions: Record<string, string>
  changes: Partial<Record<AgenticCapabilityKey, AgenticOverride>>
}

export interface AgenticBatchResult {
  dry_run: boolean
  items: Array<{
    chat_id: string
    before: AgenticChatState
    after: AgenticChatState
  }>
}
```

Implement immutable draft helpers, the four-key preset expansion, stable
summary text, and 100-ID chunking.

- [ ] **Step 5: Add BotApi methods**

Send read IDs as one comma-separated `chat_ids` query value using Axios'
standard `params` object:

```ts
async getAgenticRollout(chatID: string): Promise<AgenticChatState>
async getAgenticRollouts(chatIDs: string[]): Promise<ListResponse<AgenticChatState>>
async updateAgenticRollout(
  chatID: string,
  request: AgenticUpdateRequest,
): Promise<AgenticBatchResult>
async batchAgenticRollout(
  request: AgenticBatchRequest,
): Promise<AgenticBatchResult>
```

- [ ] **Step 6: Run tests and production build**

Run:

```bash
npm test
npm run build
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add webui/package.json webui/package-lock.json \
  webui/vitest.config.ts webui/src/api
git -c commit.gpgsign=false commit -m \
  "test: add typed agentic rollout frontend api"
```

---

### Task 6: Build the reusable three-state rollout UI

**Files:**
- Create: `webui/src/components/AgenticStatusBadge.vue`
- Create: `webui/src/components/AgenticCapabilityCard.vue`
- Create: `webui/src/components/AgenticRolloutPanel.vue`
- Create: `webui/src/components/agentic-components.test.ts`

- [ ] **Step 1: Write failing component tests**

Use Vue Test Utils and simple Element Plus stubs:

```ts
const state: AgenticCapabilityState = {
  key: 'conversation_runtime',
  label: 'Conversation Runtime',
  override: 'inherit',
  baseline: false,
  effective: false,
  source: 'default',
  available: true,
}

it('shows inherited effective state and emits an explicit override', async () => {
  const wrapper = mount(AgenticCapabilityCard, {
    props: {
      state,
      modelValue: 'inherit',
    },
    global: {
      stubs: {
        ElSegmented: {
          props: ['modelValue', 'options', 'disabled'],
          emits: ['update:modelValue'],
          template: `<button
            data-test="enable"
            @click="$emit('update:modelValue', 'enabled')"
          >enable</button>`,
        },
      },
    },
  })
  expect(wrapper.text()).toContain('继承')
  expect(wrapper.text()).toContain('当前关闭')
  await wrapper.get('[data-test="enable"]').trigger('click')
  expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['enabled'])
})
```

Also test unavailable controls, reason text, dirty state, Full Agentic preset,
restore-to-inherit, and successful reload after submit.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
npm test -- src/components/agentic-components.test.ts
```

Expected: missing components.

- [ ] **Step 3: Implement status and capability components**

`AgenticCapabilityCard` uses Element Plus Segmented with:

```vue
<el-segmented
  :model-value="modelValue"
  :options="overrideOptions"
  :disabled="saving || !state.available"
  block
  :aria-label="`${state.label} 灰度状态`"
  @update:model-value="emit('update:modelValue', $event)"
/>
```

Options are `继承`, `开启`, and `关闭`. Show effective state and source in
separate text; do not encode meaning by color alone.

- [ ] **Step 4: Implement the single-chat panel**

Props:

```ts
interface Props {
  bot: BotInstance
  chatID: string
}
```

The panel:

- loads authoritative state on mount and when bot/chat changes;
- stores a typed local draft;
- expands preset actions explicitly;
- sends only dirty keys with `expected_revision`;
- keeps the draft on 409, reloads server state, and shows a conflict message;
- reloads authoritative state after success.

- [ ] **Step 5: Run component tests and build**

Run:

```bash
npm test -- src/components/agentic-components.test.ts
npm run build
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add webui/src/components
git -c commit.gpgsign=false commit -m \
  "feat: add agentic rollout controls"
```

---

### Task 7: Add ChatDetail rollout tab and visual refresh

**Files:**
- Modify: `webui/src/views/ChatDetail.vue`
- Modify: `webui/src/api/types.ts`

- [ ] **Step 1: Add a failing view integration test**

Create `webui/src/views/chat-detail-agentic.test.ts` that shallow-mounts
ChatDetail with `AgenticRolloutPanel` stubbed and asserts:

- an `agentic` tab exists;
- selected bot and chat ID are passed to the panel;
- rollout-owned generic configs are filtered from the config table.

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
npm test -- src/views/chat-detail-agentic.test.ts
```

Expected: no Agentic tab and rollout configs remain visible.

- [ ] **Step 3: Add the tab and metadata filtering**

Add:

```vue
<el-tab-pane label="Agentic 灰度" name="agentic">
  <AgenticRolloutPanel
    v-if="bot"
    :bot="bot"
    :chat-id="props.chatID"
  />
</el-tab-pane>
```

Filter `management_surface === 'agentic_rollout'` from the generic config
table while retaining the raw API response for compatibility.

- [ ] **Step 4: Apply the approved warm visual system**

Introduce local CSS variables on `.chat-detail-ops`:

```css
.chat-detail-ops {
  --ops-pine-900: #143b36;
  --ops-pine-700: #25534d;
  --ops-lime: #d7ff73;
  --ops-canvas: #f8f7f3;
  --ops-surface: #ffffff;
  --ops-border: #e6e3da;
  --ops-muted: #737d78;
}
```

Replace inline header styling with semantic classes, use warm surfaces and
pine accents for the page header and tabs, and keep existing ECharts palette
and chart layout unchanged.

Add responsive CSS:

```css
@media (max-width: 767px) {
  .chat-detail-ops :deep(.el-tabs__nav-wrap) {
    overflow-x: auto;
  }
  .chat-detail-header {
    align-items: flex-start;
  }
}
```

- [ ] **Step 5: Run test and build**

Run:

```bash
npm test -- src/views/chat-detail-agentic.test.ts
npm run build
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add webui/src/views/ChatDetail.vue webui/src/api/types.ts \
  webui/src/views/chat-detail-agentic.test.ts
git -c commit.gpgsign=false commit -m \
  "feat: add rollout tab to chat detail"
```

---

### Task 8: Add single-bot batch rollout to ChatList

**Files:**
- Create: `webui/src/components/AgenticBatchDrawer.vue`
- Modify: `webui/src/components/agentic-components.test.ts`
- Modify: `webui/src/views/ChatList.vue`
- Create: `webui/src/views/chat-list-agentic.test.ts`

- [ ] **Step 1: Write failing batch drawer tests**

Test:

- Full Agentic produces four enabled changes;
- restore produces four inherit changes;
- opening submit always calls `dry_run: true` first;
- commit uses preview revisions and `dry_run: false`;
- 409 keeps the draft and emits `refresh`;
- unavailable preview blocks commit.

- [ ] **Step 2: Write failing ChatList safety tests**

Shallow-mount ChatList and assert:

- the all-bots view has no selection column and no batch action;
- one selected bot enables row selection;
- row key is `${bot_id}::${chat_id}`;
- clicking the selection column does not navigate;
- selected rows from different bots are rejected before drawer open.

- [ ] **Step 3: Run tests and verify RED**

Run:

```bash
npm test -- \
  src/components/agentic-components.test.ts \
  src/views/chat-list-agentic.test.ts
```

Expected: missing drawer and batch behavior.

- [ ] **Step 4: Implement async rollout summaries**

After the chat list loads:

- group rows by bot ID;
- chunk each group at 100 IDs;
- call that bot's `getAgenticRollouts`;
- store states under `${bot_id}::${chat_id}`;
- show a skeleton/status placeholder until each chunk resolves;
- keep chat analytics usable when rollout lookup fails.

- [ ] **Step 5: Implement safe table selection**

Element Plus requires a stable row key for reserved selection:

```vue
<el-table
  :data="filtered"
  :row-key="rowKey"
  @selection-change="handleSelectionChange"
  @row-click="handleRowClick"
>
  <el-table-column
    v-if="rolloutBotID"
    type="selection"
    reserve-selection
    width="48"
  />
</el-table>
```

`rolloutBotID` is non-empty only when the page is narrowed to one bot.
`handleRowClick` ignores selection cells.

- [ ] **Step 6: Implement the batch drawer workflow**

Pass the selected bot, selected chat states, and IDs to
`AgenticBatchDrawer`. Clear successful selections only after the commit
response reloads. Preserve selections across ordinary filters and show the
hidden-selection count.

- [ ] **Step 7: Run frontend tests and build**

Run:

```bash
npm test
npm run build
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add webui/src/components/AgenticBatchDrawer.vue \
  webui/src/components/agentic-components.test.ts \
  webui/src/views/ChatList.vue \
  webui/src/views/chat-list-agentic.test.ts
git -c commit.gpgsign=false commit -m \
  "feat: batch agentic rollout by bot"
```

---

### Task 9: Finish the ChatList visual and responsive refresh

**Files:**
- Modify: `webui/src/views/ChatList.vue`
- Modify: `webui/src/components/AgenticBatchDrawer.vue`

- [ ] **Step 1: Add structural assertions**

Extend the ChatList view test to assert semantic hooks:

```ts
expect(wrapper.find('.chat-ops-page').exists()).toBe(true)
expect(wrapper.find('.chat-ops-summary').exists()).toBe(true)
expect(wrapper.find('.chat-ops-toolbar').exists()).toBe(true)
```

Assert that batch drawer controls remain labelled and available without
hover-only interaction.

- [ ] **Step 2: Run test and verify RED**

Run:

```bash
npm test -- src/views/chat-list-agentic.test.ts
```

Expected: semantic visual wrappers do not exist.

- [ ] **Step 3: Apply the warm operations layout**

- Replace the four generic KPI cards with a cohesive summary band while
  preserving the same computed values.
- Move filter controls into a clear operations toolbar.
- Use pine/teal status surfaces and fluorescent lime only for primary rollout
  actions.
- Reduce inline styles by moving layout into scoped classes.
- Keep existing chart data and sorting behavior.
- Show rollout summary beside chat identity without widening the table beyond
  its current practical minimum.

- [ ] **Step 4: Add mobile/tablet behavior**

At less than 1024 px:

- stack summary and filter regions;
- allow the table to scroll horizontally;
- keep Bot/chat identity sticky only when it does not obscure selection.

At less than 768 px:

- switch the batch drawer to `size="100%"`;
- make toolbar controls full-width where needed;
- use 44 px minimum targets;
- keep status text visible instead of relying on tooltip.

- [ ] **Step 5: Run all frontend checks**

Run:

```bash
npm test
npm run build
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add webui/src/views/ChatList.vue \
  webui/src/components/AgenticBatchDrawer.vue \
  webui/src/views/chat-list-agentic.test.ts
git -c commit.gpgsign=false commit -m \
  "style: refresh chat operations webui"
```

---

### Task 10: Verify production behavior and documentation

**Files:**
- Modify: `deploy/config.example.toml`
- Modify: `docs/superpowers/specs/2026-07-31-agentic-rollout-webui-design.md`
- Create: `docs/operations/agentic-rollout-webui.md`

- [ ] **Step 1: Document the zero-init configuration**

Add:

```toml
[agent_card]
enabled = true
mode = "allowlist"
allow_chat_ids = []

[runtime_config]
evaluation_mode = "allowlist"
evaluation_chat_ids = []
```

Explain that Runtime and callback continuation default off, PostgreSQL schema
bootstrap remains automatic, and evaluation OpenSearch provisioning happens
on first effective enable.

- [ ] **Step 2: Run focused Go verification**

Run:

```bash
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' \
  ./internal/application/config \
  ./internal/application/agenticrollout \
  ./internal/application/lark/messages \
  ./internal/application/lark/agentruntime \
  ./internal/interfaces/webui \
  ./cmd/larkrobot
```

Expected: PASS.

- [ ] **Step 3: Run race tests that do not use mockey gcflags**

Run:

```bash
$BETAGO_GO test -race -count=1 -tags=custom_skip_vips \
  ./internal/application/config \
  ./internal/application/agenticrollout \
  ./internal/interfaces/webui
```

Expected: PASS. Do not combine `-race` with mockey `-gcflags`.

- [ ] **Step 4: Run frontend verification**

Run:

```bash
cd webui
npm test
npm run build
```

Expected: PASS.

- [ ] **Step 5: Run repository-wide Go baseline**

Run:

```bash
cd ..
$BETAGO_GO test -count=1 -tags=custom_skip_vips \
  -gcflags='all=-N -l' ./...
```

Expected: all affected packages pass. Record the two existing shared-Redis
isolation failures if they remain:

- `internal/infrastructure/geocode.TestCachedFallsBackAndCaches`;
- `internal/infrastructure/mcpstore.TestSessionStoreSeen`.

Do not mutate shared Redis to hide them.

- [ ] **Step 6: Build the production binary**

Run:

```bash
$BETAGO_GO build -tags=custom_skip_vips ./cmd/larkrobot
```

Expected: PASS.

- [ ] **Step 7: Inspect the WebUI at three widths**

Run:

```bash
cd webui
npm run dev -- --host 0.0.0.0
```

Verify ChatList and ChatDetail at 1440x900, 1024x768, and 390x844:

- all-bots mode is visibly read-only;
- single-bot selection and batch drawer work;
- inherited/effective state is legible;
- unavailable state includes reason text;
- no horizontal page overflow at mobile width;
- existing charts remain readable.

- [ ] **Step 8: Commit documentation**

```bash
git add deploy/config.example.toml \
  docs/operations/agentic-rollout-webui.md \
  docs/superpowers/specs/2026-07-31-agentic-rollout-webui-design.md
git -c commit.gpgsign=false commit -m \
  "docs: operate agentic rollout from webui"
```

- [ ] **Step 9: Final diff audit**

Run:

```bash
git diff --check origin/master...HEAD
git status --short
git log --oneline origin/master..HEAD
```

Expected: no whitespace errors; only `.superpowers/` remains untracked; commit
history contains focused design, backend, frontend, style, and operations
commits.
