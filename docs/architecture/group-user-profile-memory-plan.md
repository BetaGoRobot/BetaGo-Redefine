# Group User Profile Memory Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add chat-scoped user profile memory with isolated OpenSearch storage, cold-start backfill, and gated prompt integration without regressing existing chunk/topic recall.

**Architecture:** Keep `pkg/xchunk` as the existing topic-memory producer, harden it with deterministic speaker evidence, and fan out chunk documents into a new `profilememory` pipeline. Persist `chat_user` profile docs in a dedicated OpenSearch index, backfill them from historical chunk documents, and inject only targeted profile facts into prompt assembly behind positive dynamic-config gates.

**Tech Stack:** Go, OpenSearch, existing `xchunk` pipeline, Ark model APIs, dynamic config manager, GORM Gen, `go test`

---

Execution notes:

- Use `@superpowers:test-driven-development` for every task.
- Use `@superpowers:verification-before-completion` before claiming implementation is done.
- Keep rollout control on dynamic config booleans, not `feature_block`, because the existing feature system is “default enabled, selectively blocked” and does not support whitelist enablement.
- Keep plan and design doc adjacent in `docs/architecture` to match the user’s documentation preference.

## File Structure

### Modify

- `internal/infrastructure/config/configs.go`
  - Add `OpensearchConfig.LarkUserProfileIndex`.
- `internal/application/config/manager.go`
  - Add config keys for profile index and positive rollout gates.
- `internal/application/config/definitions.go`
  - Register new keys so config cards expose them.
- `internal/application/config/accessor.go`
  - Add typed accessors for profile index and read/write/backfill booleans.
- `internal/application/config/manager_test.go`
  - Cover fallback/default behavior and key enumeration.
- `internal/xmodel/models.go`
  - Extend `MessageChunkLogV3` with deterministic speaker evidence and `PromptTemplateArg` with `UserProfiles`.
- `internal/application/lark/chunking/common.go`
  - Normalize sender identity for chunk evidence.
- `internal/application/lark/chunking/msg.go`
  - Export author evidence for inbound messages.
- `internal/application/lark/chunking/reply.go`
  - Export author evidence for reply-created messages.
- `internal/application/lark/chunking/create.go`
  - Export author evidence for send-created messages.
- `pkg/xchunk/chunking.go`
  - Carry speaker evidence through `StandardMsg`, persist it into chunk docs, and support a post-persist observer hook.
- `cmd/larkrobot/bootstrap.go`
  - Wire the profile memory service into application startup and chunk post-processing.
- `internal/application/lark/handlers/chat_handler.go`
  - Resolve targeted profile facts and append them to prompt data when read-gate is enabled.
- `internal/application/lark/agentruntime/initial_chat_generation.go`
  - Reuse the same targeted profile resolver for the agent-runtime prompt path.
- `internal/application/lark/agentruntime/initial_chat_generation_test.go`
  - Verify prompt generation includes user profile facts only when targeted and enabled.
- `cmd/larkrobot/bootstrap_test.go`
  - Verify startup wiring keeps building cleanly after profile service is attached.

### Create

- `internal/xmodel/group_user_profile.go`
  - Define user profile docs, candidates, facet enums, and backfill report rows.
- `pkg/xchunk/chunking_test.go`
  - Add unit tests for speaker evidence capture, participant aggregation, and observer invocation.
- `internal/application/lark/profilememory/types.go`
  - Shared service-layer types and service interfaces.
- `internal/application/lark/profilememory/extractor.go`
  - Chunk-to-candidate extraction orchestration.
- `internal/application/lark/profilememory/gate.go`
  - Facet whitelist, disallowed-signal filtering, and base confidence assignment.
- `internal/application/lark/profilememory/merge.go`
  - Candidate/doc merge rules, conflict handling, and state transitions.
- `internal/application/lark/profilememory/service.go`
  - High-level service for online write path and online read path.
- `internal/application/lark/profilememory/read_context.go`
  - Resolve target users from messages and format prompt snippets.
- `internal/application/lark/profilememory/backfill.go`
  - Historical chunk scan, dry-run/write modes, and report generation.
- `internal/application/lark/profilememory/extractor_test.go`
  - Extraction and gating tests.
- `internal/application/lark/profilememory/merge_test.go`
  - Merge/conflict/decay tests.
- `internal/application/lark/profilememory/service_test.go`
  - Online writer/reader orchestration tests.
- `internal/application/lark/profilememory/read_context_test.go`
  - Target resolution and snippet-selection tests.
- `internal/application/lark/profilememory/backfill_test.go`
  - Backfill windowing and report tests.
- `internal/infrastructure/profilememory/repository.go`
  - OpenSearch-backed query/upsert implementation with injectable search/insert functions.
- `internal/infrastructure/profilememory/repository_test.go`
  - Query/upsert tests using fake search/insert callbacks.
- `cmd/profile-memory-backfill/main.go`
  - Dry-run/write CLI for cold-start backfill and evaluation output.
- `script/sql/20260323_group_user_profile_prompt_template.sql`
  - Idempotent SQL to update prompt template `prompt_id = 5` with a `UserProfiles` section.

## Chunk 1: Foundations

### Task 1: Add config plumbing and positive rollout gates

**Files:**
- Modify: `internal/infrastructure/config/configs.go`
- Modify: `internal/application/config/manager.go`
- Modify: `internal/application/config/definitions.go`
- Modify: `internal/application/config/accessor.go`
- Test: `internal/application/config/manager_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestGetStringFallsBackToTomlForUserProfileIndex(t *testing.T) {
	oldConfig := currentBaseConfig
	currentBaseConfig = func() *infraConfig.BaseConfig {
		return &infraConfig.BaseConfig{
			OpensearchConfig: &infraConfig.OpensearchConfig{
				LarkUserProfileIndex: "group_user_profile_index",
			},
		}
	}
	defer func() { currentBaseConfig = oldConfig }()

	manager := NewManager()
	if got := manager.GetString(context.Background(), KeyLarkUserProfileIndex, "", ""); got != "group_user_profile_index" {
		t.Fatalf("GetString(profile index) = %q", got)
	}
}

func TestGetBoolDefaultsForGroupUserProfileGates(t *testing.T) {
	manager := NewManager()
	if manager.GetBool(context.Background(), KeyGroupUserProfileReadEnabled, "", "") {
		t.Fatal("read gate should default to false")
	}
	if manager.GetBool(context.Background(), KeyGroupUserProfileWriteEnabled, "", "") {
		t.Fatal("write gate should default to false")
	}
	if manager.GetBool(context.Background(), KeyGroupUserProfileBackfillEnabled, "", "") {
		t.Fatal("backfill gate should default to false")
	}
}

func TestGetAllConfigKeysIncludesUserProfileKeys(t *testing.T) {
	keys := GetAllConfigKeys()
	set := make(map[ConfigKey]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	expected := []ConfigKey{
		KeyLarkUserProfileIndex,
		KeyGroupUserProfileReadEnabled,
		KeyGroupUserProfileWriteEnabled,
		KeyGroupUserProfileBackfillEnabled,
	}
	for _, key := range expected {
		if _, ok := set[key]; !ok {
			t.Fatalf("missing config key %q", key)
		}
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./internal/application/config -run 'Test(GetStringFallsBackToTomlForUserProfileIndex|GetBoolDefaultsForGroupUserProfileGates|GetAllConfigKeysIncludesUserProfileKeys)$'
```

Expected:

- FAIL because the new config keys and accessors do not exist yet.

- [ ] **Step 3: Implement the minimal config plumbing**

```go
// internal/infrastructure/config/configs.go
type OpensearchConfig struct {
	LarkCardActionIndex  string `toml:"lark_card_action_index"`
	LarkChunkIndex       string `toml:"lark_chunk_index"`
	LarkMsgIndex         string `toml:"lark_msg_index"`
	LarkUserProfileIndex string `toml:"lark_user_profile_index"`
}

// internal/application/config/manager.go
const (
	KeyLarkUserProfileIndex            ConfigKey = "lark_user_profile_index"
	KeyGroupUserProfileReadEnabled     ConfigKey = "group_user_profile_read_enabled"
	KeyGroupUserProfileWriteEnabled    ConfigKey = "group_user_profile_write_enabled"
	KeyGroupUserProfileBackfillEnabled ConfigKey = "group_user_profile_backfill_enabled"
)

func (m *Manager) getStringFromToml(key ConfigKey) string {
	switch key {
	case KeyLarkUserProfileIndex:
		if cfg.OpensearchConfig == nil {
			return m.getDefaultString(key)
		}
		return cfg.OpensearchConfig.LarkUserProfileIndex
	}
	return m.getDefaultString(key)
}

func (m *Manager) getDefaultBool(key ConfigKey) bool {
	switch key {
	case KeyGroupUserProfileReadEnabled, KeyGroupUserProfileWriteEnabled, KeyGroupUserProfileBackfillEnabled:
		return false
	}
	return false
}

// internal/application/config/accessor.go
func (a *Accessor) LarkUserProfileIndex() string {
	return a.manager.GetString(a.ctx, KeyLarkUserProfileIndex, a.chatID, a.openID)
}

func (a *Accessor) GroupUserProfileReadEnabled() bool {
	return a.manager.GetBool(a.ctx, KeyGroupUserProfileReadEnabled, a.chatID, a.openID)
}
```

Implementation notes:

- Add all four keys to `configDefinitions` with clear Chinese descriptions.
- Use dynamic-config booleans for rollout, not `feature_block`.
- Keep default booleans `false`; enabling is a positive opt-in by scope.

- [ ] **Step 4: Run the config tests again**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./internal/application/config -run 'Test(GetStringFallsBackToTomlForUserProfileIndex|GetBoolDefaultsForGroupUserProfileGates|GetAllConfigKeysIncludesUserProfileKeys)$'
```

Expected:

- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/infrastructure/config/configs.go \
  internal/application/config/manager.go \
  internal/application/config/definitions.go \
  internal/application/config/accessor.go \
  internal/application/config/manager_test.go
git commit -m "feat: add user profile memory config plumbing"
```

### Task 2: Harden chunk docs with deterministic speaker evidence

**Files:**
- Modify: `internal/xmodel/models.go`
- Modify: `internal/application/lark/chunking/common.go`
- Modify: `internal/application/lark/chunking/msg.go`
- Modify: `internal/application/lark/chunking/reply.go`
- Modify: `internal/application/lark/chunking/create.go`
- Modify: `pkg/xchunk/chunking.go`
- Test: `pkg/xchunk/chunking_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestBuildStdMsgCapturesAuthorEvidence(t *testing.T) {
	msg := fakeGenericMsg{
		groupID: "oc_chat",
		msgID:   "om_1",
		ts:      1710000000000,
		line:    "[2026-03-23 10:00:00](ou_alice) <Alice>: 我来负责发布",
		author:  ChunkAuthor{OpenID: "ou_alice", Name: "Alice"},
	}

	got, ok := BuildStdMsg(msg)
	if !ok {
		t.Fatal("expected BuildStdMsg to keep valid message")
	}
	if got.Author.OpenID != "ou_alice" || got.Author.Name != "Alice" {
		t.Fatalf("unexpected author evidence: %+v", got.Author)
	}
}

func TestObservedParticipantsDedupesAndCountsAuthors(t *testing.T) {
	messages := []StandardMsg{
		{MsgID_: "om_1", Author: ChunkAuthor{OpenID: "ou_alice", Name: "Alice"}},
		{MsgID_: "om_2", Author: ChunkAuthor{OpenID: "ou_bob", Name: "Bob"}},
		{MsgID_: "om_3", Author: ChunkAuthor{OpenID: "ou_alice", Name: "Alice"}},
	}

	participants := BuildObservedParticipants(messages)
	if len(participants) != 2 {
		t.Fatalf("len(participants) = %d", len(participants))
	}
	if participants[0].OpenID != "ou_alice" || participants[0].MessageCount != 2 {
		t.Fatalf("unexpected first participant: %+v", participants[0])
	}
}
```

- [ ] **Step 2: Run the chunk tests to confirm failure**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./pkg/xchunk -run 'Test(BuildStdMsgCapturesAuthorEvidence|ObservedParticipantsDedupesAndCountsAuthors)$'
```

Expected:

- FAIL because `StandardMsg` and `GenericMsg` do not carry author evidence yet.

- [ ] **Step 3: Implement deterministic author capture and participant aggregation**

```go
// pkg/xchunk/chunking.go
type ChunkAuthor struct {
	OpenID string `json:"user_id"`
	Name   string `json:"name"`
}

type StandardMsg struct {
	GroupID_   string      `json:"group_id"`
	MsgID_     string      `json:"msg_id"`
	TimeStamp_ int64       `json:"timestamp"`
	BuildLine_ string      `json:"line"`
	Author     ChunkAuthor `json:"author"`
}

type GenericMsg interface {
	GroupID() string
	MsgID() string
	TimeStamp() int64
	BuildLine() (string, bool)
	Author() (ChunkAuthor, bool)
}

func BuildObservedParticipants(messages []StandardMsg) []*xmodel.Participant {
	// deterministic counts from actual message authors, not LLM-derived guesses
}
```

```go
// internal/application/lark/chunking/msg.go
func (m *LarkMessageEvent) Author() (xchunk.ChunkAuthor, bool) {
	openID := botidentity.MessageSenderOpenID(m.P2MessageReceiveV1)
	if strings.TrimSpace(openID) == "" {
		return xchunk.ChunkAuthor{}, false
	}
	name := resolveChunkSenderName(context.Background(), *m.Event.Message.ChatId, openID)
	return xchunk.ChunkAuthor{OpenID: openID, Name: name}, true
}
```

Implementation notes:

- Add deterministic fields to `MessageChunkLogV3`, for example:
  - `MessageAuthors []*User`
  - `ObservedParticipants []*Participant`
- Normalize bot-authored outbound messages to `bot_open_id` instead of unstable app IDs.
- Keep existing `InteractionAnalysis.Participants` untouched; deterministic fields are additive and intended for profile extraction.

- [ ] **Step 4: Run the chunk tests plus affected handler tests**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./pkg/xchunk ./internal/application/lark/handlers
```

Expected:

- PASS
- Existing word-chunk participant rendering remains green.

- [ ] **Step 5: Commit**

```bash
git add internal/xmodel/models.go \
  internal/application/lark/chunking/common.go \
  internal/application/lark/chunking/msg.go \
  internal/application/lark/chunking/reply.go \
  internal/application/lark/chunking/create.go \
  pkg/xchunk/chunking.go \
  pkg/xchunk/chunking_test.go
git commit -m "feat: persist deterministic chunk speaker evidence"
```

## Chunk 2: Profile Pipeline

### Task 3: Build the profile memory domain, gate, merge rules, and repository

**Files:**
- Create: `internal/xmodel/group_user_profile.go`
- Create: `internal/application/lark/profilememory/types.go`
- Create: `internal/application/lark/profilememory/extractor.go`
- Create: `internal/application/lark/profilememory/gate.go`
- Create: `internal/application/lark/profilememory/merge.go`
- Create: `internal/application/lark/profilememory/extractor_test.go`
- Create: `internal/application/lark/profilememory/merge_test.go`
- Create: `internal/infrastructure/profilememory/repository.go`
- Create: `internal/infrastructure/profilememory/repository_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestGateRejectsEphemeralAssignment(t *testing.T) {
	candidate := Candidate{
		ChatID:         "oc_chat",
		UserID:         "ou_alice",
		Facet:          FacetRole,
		CanonicalValue: "今天值班",
		Origin:         OriginObserved,
		SummaryEvidence: []string{
			"今天我值班",
		},
	}

	if got, ok := ApplyGate(candidate); ok {
		t.Fatalf("expected candidate to be rejected, got %+v", got)
	}
}

func TestMergePromotesRepeatedEvidence(t *testing.T) {
	existing := &xmodel.GroupUserProfileDoc{
		Status:        StatusCandidate,
		Confidence:    0.55,
		EvidenceCount: 1,
	}
	incoming := Candidate{
		Facet:          FacetRole,
		CanonicalValue: "发布 owner",
		Origin:         OriginObserved,
	}

	merged := MergeProfile(existing, incoming, time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC))
	if merged.Status != StatusActive {
		t.Fatalf("status = %q, want %q", merged.Status, StatusActive)
	}
	if merged.EvidenceCount != 2 {
		t.Fatalf("evidence_count = %d", merged.EvidenceCount)
	}
}

func TestRepositorySearchFiltersByScopeAndStatus(t *testing.T) {
	repo := NewRepository(fakeSearch, fakeInsert)
	_, _ = repo.SearchProfiles(context.Background(), SearchRequest{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
		Scope:     ScopeChatUser,
		ChatID:    "oc_chat",
		UserID:    "ou_alice",
		Statuses:  []Status{StatusActive, StatusStale},
	})

	if !strings.Contains(capturedSearchBody, "\"scope\":\"chat_user\"") {
		t.Fatalf("query = %s, want chat_user filter", capturedSearchBody)
	}
}
```

- [ ] **Step 2: Run the new domain and repository tests**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./internal/application/lark/profilememory ./internal/infrastructure/profilememory \
  -run 'Test(GateRejectsEphemeralAssignment|MergePromotesRepeatedEvidence|RepositorySearchFiltersByScopeAndStatus)$'
```

Expected:

- FAIL because the profile domain and repository do not exist yet.

- [ ] **Step 3: Implement the minimal profile pipeline**

```go
// internal/xmodel/group_user_profile.go
type GroupUserProfileDoc struct {
	ID              string    `json:"id"`
	AppID           string    `json:"app_id"`
	BotOpenID       string    `json:"bot_open_id"`
	Scope           string    `json:"scope"`
	ChatID          string    `json:"chat_id"`
	UserID          string    `json:"user_id"`
	Facet           string    `json:"facet"`
	CanonicalValue  string    `json:"canonical_value"`
	Aliases         []string  `json:"aliases,omitempty"`
	Confidence      float64   `json:"confidence"`
	EvidenceCount   int       `json:"evidence_count"`
	Origin          string    `json:"origin"`
	Status          string    `json:"status"`
	FirstObservedAt time.Time `json:"first_observed_at"`
	LastConfirmedAt time.Time `json:"last_confirmed_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	SourceChunkIDs  []string  `json:"source_chunk_ids,omitempty"`
	SourceMsgIDs    []string  `json:"source_msg_ids,omitempty"`
	SummaryEvidence []string  `json:"summary_evidence,omitempty"`
}
```

```go
// internal/application/lark/profilememory/gate.go
var allowedFacets = map[Facet]struct{}{
	FacetRole:                   {},
	FacetExpertise:              {},
	FacetCommunicationPreference:{},
	FacetStableInterest:         {},
	FacetCollaborationPattern:   {},
}

func ApplyGate(candidate Candidate) (Candidate, bool) {
	// reject ephemeral, joking, hostile, or unconfirmed third-party labels
}
```

```go
// internal/infrastructure/profilememory/repository.go
type Repository struct {
	search func(context.Context, string, any) (*opensearchapi.SearchResp, error)
	insert func(context.Context, string, string, any) error
}
```

Implementation notes:

- Keep extraction contract narrow: input is one chunk doc, output is zero or more `Candidate`.
- Use `ark_dal.ResponseWithCache` for chunk-to-candidate extraction only after deterministic preconditions are met.
- Clamp extractor output to the allowed facet set in Go even if the model returns extra fields.
- Repository JSON bodies should filter by `app_id`, `bot_open_id`, `scope`, `chat_id`, `user_id`, and status list.

- [ ] **Step 4: Run the profile domain tests again**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./internal/application/lark/profilememory ./internal/infrastructure/profilememory
```

Expected:

- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/xmodel/group_user_profile.go \
  internal/application/lark/profilememory/types.go \
  internal/application/lark/profilememory/extractor.go \
  internal/application/lark/profilememory/gate.go \
  internal/application/lark/profilememory/merge.go \
  internal/application/lark/profilememory/extractor_test.go \
  internal/application/lark/profilememory/merge_test.go \
  internal/infrastructure/profilememory/repository.go \
  internal/infrastructure/profilememory/repository_test.go
git commit -m "feat: add group user profile domain and repository"
```

### Task 4: Add cold-start backfill and evaluation reporting

**Files:**
- Create: `internal/application/lark/profilememory/backfill.go`
- Create: `internal/application/lark/profilememory/backfill_test.go`
- Create: `cmd/profile-memory-backfill/main.go`

- [ ] **Step 1: Write the failing backfill tests**

```go
func TestBackfillPlannerUsesChunkWindow(t *testing.T) {
	service := NewBackfillService(fakeChunkSource, fakeProfileRepo, fakeExtractor)
	_, err := service.Run(context.Background(), BackfillOptions{
		ChatIDs: []string{"oc_chat"},
		Days:    30,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := capturedChunkSearch.WindowDays; got != 30 {
		t.Fatalf("window_days = %d, want 30", got)
	}
}

func TestBackfillReportSummarizesCandidates(t *testing.T) {
	report := BuildReport([]BackfillRow{
		{Facet: "role", Status: "candidate"},
		{Facet: "role", Status: "active"},
		{Facet: "expertise", Status: "candidate"},
	})
	if report.TotalRows != 3 {
		t.Fatalf("TotalRows = %d", report.TotalRows)
	}
	if report.ByFacet["role"] != 2 {
		t.Fatalf("role count = %d", report.ByFacet["role"])
	}
}
```

- [ ] **Step 2: Run the backfill tests to confirm failure**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./internal/application/lark/profilememory -run 'Test(BackfillPlannerUsesChunkWindow|BackfillReportSummarizesCandidates)$'
```

Expected:

- FAIL because the backfill service and report builder are not implemented.

- [ ] **Step 3: Implement the dry-run-first backfill CLI**

```go
// internal/application/lark/profilememory/backfill.go
type BackfillOptions struct {
	ChatIDs    []string
	Days       int
	Limit      int
	DryRun     bool
	OutputDir  string
	Write      bool
}

func (s *BackfillService) Run(ctx context.Context, opts BackfillOptions) (*BackfillReport, error) {
	// scan chunk index by chat/time window, reuse extractor+gate+merge, emit report rows
}
```

```go
// cmd/profile-memory-backfill/main.go
func main() {
	// flags: --chat, --days, --limit, --out, --write
	// default dry-run, emit candidates.jsonl + report.md
}
```

Implementation notes:

- Default CLI mode must be dry-run.
- `--write` should be explicit and should reuse the exact same gate/merge logic as online writes.
- Write `candidates.jsonl` and `report.md` to the output directory so humans can review precision/contamination before rollout.

- [ ] **Step 4: Run the backfill tests again**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./internal/application/lark/profilememory -run 'Test(BackfillPlannerUsesChunkWindow|BackfillReportSummarizesCandidates)$'
```

Expected:

- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/profilememory/backfill.go \
  internal/application/lark/profilememory/backfill_test.go \
  cmd/profile-memory-backfill/main.go
git commit -m "feat: add group user profile backfill command"
```

## Chunk 3: Online Integration

### Task 5: Wire online profile writes off the chunk pipeline

**Files:**
- Modify: `pkg/xchunk/chunking.go`
- Modify: `cmd/larkrobot/bootstrap.go`
- Create: `internal/application/lark/profilememory/service.go`
- Create: `internal/application/lark/profilememory/service_test.go`
- Modify: `cmd/larkrobot/bootstrap_test.go`

- [ ] **Step 1: Write the failing online-writer tests**

```go
func TestOnMergeInvokesProfileObserverAfterChunkInsert(t *testing.T) {
	mgr := NewNoopManagement("test")
	observer := &fakeChunkObserver{}
	mgr.SetChunkObserver(observer)

	err := mgr.notifyChunkPersisted(context.Background(), &xmodel.MessageChunkLogV3{
		GroupID: "oc_chat",
	})
	if err != nil {
		t.Fatalf("notifyChunkPersisted() error = %v", err)
	}
	if observer.calls != 1 {
		t.Fatalf("observer calls = %d, want 1", observer.calls)
	}
}

func TestProfileWriterSkipsWhenWriteGateDisabled(t *testing.T) {
	service := NewService(fakeRepo, fakeExtractor, fakeAccessor(false, false, false))
	err := service.HandleChunkPersisted(context.Background(), "oc_chat", &xmodel.MessageChunkLogV3{})
	if err != nil {
		t.Fatalf("HandleChunkPersisted() error = %v", err)
	}
	if fakeRepo.upserts != 0 {
		t.Fatal("expected no upserts when write gate is disabled")
	}
}
```

- [ ] **Step 2: Run the writer tests to confirm failure**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./pkg/xchunk ./internal/application/lark/profilememory ./cmd/larkrobot \
  -run 'Test(OnMergeInvokesProfileObserverAfterChunkInsert|ProfileWriterSkipsWhenWriteGateDisabled)$'
```

Expected:

- FAIL because the chunk observer hook and service wiring do not exist.

- [ ] **Step 3: Implement observer-based online writes**

```go
// pkg/xchunk/chunking.go
type ChunkObserver interface {
	OnChunkPersisted(context.Context, *xmodel.MessageChunkLogV3) error
}

func (m *Management) SetChunkObserver(observer ChunkObserver) {
	m.chunkObserver = observer
}

// after successful InsertData(...)
if m.chunkObserver != nil {
	if err := m.chunkObserver.OnChunkPersisted(ctx, chunkLog); err != nil {
		logs.L().Ctx(ctx).Warn("chunk observer failed", zap.Error(err))
	}
}
```

```go
// internal/application/lark/profilememory/service.go
func (s *Service) OnChunkPersisted(ctx context.Context, chunk *xmodel.MessageChunkLogV3) error {
	accessor := appconfig.NewAccessor(ctx, chunk.GroupID, "")
	if !accessor.GroupUserProfileWriteEnabled() {
		return nil
	}
	return s.HandleChunkPersisted(ctx, chunk.GroupID, chunk)
}
```

```go
// cmd/larkrobot/bootstrap.go
profileSvc := profilememory.NewService(profileRepo, extractor, appconfig.GetManager())
larkchunking.M.SetChunkObserver(profileSvc)
```

Implementation notes:

- Keep the profile write path behind `GroupUserProfileWriteEnabled`.
- It is acceptable for the first implementation to run inside the background chunk worker, because chunking is already off the user-facing reply path.
- Do not make `pkg/xchunk` import `internal/application/lark/profilememory`; use the observer interface to keep boundaries clean.

- [ ] **Step 4: Run the writer and bootstrap tests again**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./pkg/xchunk ./internal/application/lark/profilememory ./cmd/larkrobot
```

Expected:

- PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/xchunk/chunking.go \
  cmd/larkrobot/bootstrap.go \
  cmd/larkrobot/bootstrap_test.go \
  internal/application/lark/profilememory/service.go \
  internal/application/lark/profilememory/service_test.go
git commit -m "feat: wire online user profile writes from chunk pipeline"
```

### Task 6: Inject targeted profile facts into standard and agent-runtime prompts

**Files:**
- Modify: `internal/xmodel/models.go`
- Create: `internal/application/lark/profilememory/read_context.go`
- Create: `internal/application/lark/profilememory/read_context_test.go`
- Modify: `internal/application/lark/handlers/chat_handler.go`
- Modify: `internal/application/lark/agentruntime/initial_chat_generation.go`
- Modify: `internal/application/lark/agentruntime/initial_chat_generation_test.go`
- Create: `script/sql/20260323_group_user_profile_prompt_template.sql`

- [ ] **Step 1: Write the failing read-path tests**

```go
func TestResolvePromptFactsUsesMentionedUserOnly(t *testing.T) {
	facts, err := BuildPromptFacts(context.Background(), BuildPromptFactsRequest{
		ChatID:            "oc_chat",
		ActorOpenID:       "ou_sender",
		MentionedOpenIDs:  []string{"ou_alice"},
		MessageText:       "@Alice 这块应该谁来接？",
		ReadEnabled:       true,
		ProfilesByUserID: map[string][]xmodel.GroupUserProfileDoc{
			"ou_alice": {
				{Facet: "role", CanonicalValue: "发布 owner", Confidence: 0.91, Status: "active"},
			},
			"ou_bob": {
				{Facet: "expertise", CanonicalValue: "后端链路", Confidence: 0.88, Status: "active"},
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildPromptFacts() error = %v", err)
	}
	if len(facts) != 1 || !strings.Contains(facts[0], "发布 owner") {
		t.Fatalf("facts = %+v", facts)
	}
}

func TestBuildInitialChatExecutionPlanIncludesUserProfiles(t *testing.T) {
	// extend the existing test harness in initial_chat_generation_test.go
	// and assert the prompt contains the selected profile fact.
}
```

- [ ] **Step 2: Run the read-path tests to confirm failure**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./internal/application/lark/profilememory ./internal/application/lark/agentruntime \
  -run 'Test(ResolvePromptFactsUsesMentionedUserOnly|BuildInitialChatExecutionPlanIncludesUserProfiles)$'
```

Expected:

- FAIL because prompt data does not yet include `UserProfiles`.

- [ ] **Step 3: Implement targeted read gating and prompt template support**

```go
// internal/xmodel/models.go
type PromptTemplateArg struct {
	*model.PromptTemplateArg
	HistoryRecords   []string `json:"history_records" gorm:"-"`
	Context          []string `json:"context" gorm:"-"`
	Topics           []string `json:"topics" gorm:"-"`
	UserProfiles     []string `json:"user_profiles" gorm:"-"`
	UserInput        []string `json:"user_input" gorm:"-"`
	CurrentTimeStamp string   `json:"current_timestamp" gorm:"-"`
}
```

```go
// internal/application/lark/profilememory/read_context.go
func BuildPromptFacts(ctx context.Context, req BuildPromptFactsRequest) ([]string, error) {
	if !req.ReadEnabled {
		return nil, nil
	}
	targetIDs := ResolveTargetUserIDs(req.MentionedOpenIDs, req.MessageText, req.ActorOpenID)
	return SelectTopProfileFacts(req.ProfilesByUserID, targetIDs, 3), nil
}
```

```go
// script/sql/20260323_group_user_profile_prompt_template.sql
update prompt_template_args
set template_str = regexp_replace(
  template_str,
  E'{{\\\\.Topics}}',
  E'{{.Topics}}\\n{{if .UserProfiles}}\\n可能相关的用户画像事实:\\n{{range .UserProfiles}}- {{.}}\\n{{end}}{{end}}'
)
where prompt_id = 5
  and template_str not like '%可能相关的用户画像事实%';
```

Implementation notes:

- Standard chat and agent-runtime paths should call the same `BuildPromptFacts` helper.
- Query the profile index only when `GroupUserProfileReadEnabled` is true and there is a targeted user.
- Keep the snippet budget at `1-3` facts per targeted user.
- Do not inject profile facts into the system prompt; only append them to template data.

- [ ] **Step 4: Run the read-path tests and surrounding packages**

Run:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./internal/application/lark/profilememory ./internal/application/lark/agentruntime ./internal/application/lark/handlers
```

Expected:

- PASS
- Prompt assembly stays green in both standard and agent-runtime codepaths.

- [ ] **Step 5: Commit**

```bash
git add internal/xmodel/models.go \
  internal/application/lark/profilememory/read_context.go \
  internal/application/lark/profilememory/read_context_test.go \
  internal/application/lark/handlers/chat_handler.go \
  internal/application/lark/agentruntime/initial_chat_generation.go \
  internal/application/lark/agentruntime/initial_chat_generation_test.go \
  script/sql/20260323_group_user_profile_prompt_template.sql
git commit -m "feat: inject targeted user profile facts into chat prompts"
```

## Final Verification

After all tasks are complete, run the following end-to-end checks before rollout:

- [ ] Run focused unit tests:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go test ./internal/application/config ./pkg/xchunk ./internal/application/lark/profilememory ./internal/infrastructure/profilememory ./internal/application/lark/agentruntime ./internal/application/lark/handlers ./cmd/larkrobot
```

Expected:

- PASS

- [ ] Run a dry-run cold-start backfill against one dev chat:

```bash
env BETAGO_CONFIG_PATH=$(pwd)/.dev/config.toml GOCACHE=$(pwd)/.codex-gocache GOTMPDIR=$(pwd)/.cache/gotmp \
go run ./cmd/profile-memory-backfill --chat <dev_chat_id> --days 30 --limit 200 --out /tmp/profile-memory-backfill
```

Expected:

- `candidates.jsonl` written
- `report.md` written
- no writes performed unless `--write` is explicitly added

- [ ] Enable rollout gates only for one test chat:

```bash
# Use the existing config management path to set:
# group_user_profile_backfill_enabled = true
# group_user_profile_write_enabled = true
# group_user_profile_read_enabled = true
```

Expected:

- only the whitelisted chat reads/writes user profile memory

- [ ] Manually validate one targeted prompt:

```text
@bot A 这块通常谁负责？
```

Expected:

- reply may reference `A`’s role if a high-confidence profile exists
- unrelated chats and non-person questions remain unchanged

## Rollout Order

1. Ship config plumbing and chunk evidence first.
2. Run cold-start backfill in dry-run mode and review `report.md`.
3. Enable `group_user_profile_backfill_enabled` for one chat and write approved profiles.
4. Enable `group_user_profile_write_enabled` for the same chat.
5. Enable `group_user_profile_read_enabled` last.
6. Watch precision, contamination, latency, and explicit-correction feedback before expanding scope.

## Risks To Watch

- Bot-authored outbound messages may carry unstable sender IDs; normalize them to `bot_open_id`.
- Prompt template `prompt_id = 5` lives in the database; code changes alone are not sufficient.
- Existing `feature_block` cannot implement default-off whitelists; use dynamic config booleans for rollout.
- `InteractionAnalysis.Participants` is LLM-derived and should not replace deterministic `ObservedParticipants`.

Plan complete and saved to `docs/architecture/group-user-profile-memory-plan.md`. Ready to execute?
