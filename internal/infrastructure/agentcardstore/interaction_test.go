package agentcardstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardcompiler"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	uuid "github.com/satori/go.uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestNewRepositoryRequiresValidTenant(t *testing.T) {
	if _, err := NewRepository(nil, tenant.Tenant{}); err == nil {
		t.Fatal("NewRepository() accepted an invalid tenant")
	}
}

func TestRepositoryDoesNotReadAnotherTenantSurface(t *testing.T) {
	fixture := newCardStoreFixture(t)
	request := fixture.beginRequest("tenant-isolation")
	if _, err := fixture.repo.BeginCardInteraction(
		context.Background(), request,
	); err != nil {
		t.Fatal(err)
	}
	otherTenant, err := tenant.New("app-agentcard-other", "bot-agentcard-other")
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewRepository(fixture.db, otherTenant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.GetByInteraction(
		context.Background(),
		agentcard.GetSurfaceRequest{
			RunID: request.RunID, InteractionID: request.InteractionID,
		},
	); !errors.Is(err, agentcard.ErrCardNotFound) {
		t.Fatalf("cross-tenant GetByInteraction() error = %v, want ErrCardNotFound", err)
	}
}

var repositoryTestTenant, _ = tenant.New("app-agentcard-test", "bot-agentcard-test")

func mustNewTestRepository(database *gorm.DB) *Repository {
	repository, err := NewRepository(database, repositoryTestTenant)
	if err != nil {
		panic(err)
	}
	return repository
}

func mustNewAgentTestRepository(database *gorm.DB) *agentstore.Repository {
	repository, err := agentstore.NewRepository(database, repositoryTestTenant)
	if err != nil {
		panic(err)
	}
	return repository
}

func TestBeginCardInteractionAtomicallyCreatesWaitOutboxSurfaceAndWaitingState(t *testing.T) {
	fixture := newCardStoreFixture(t)
	request := fixture.beginRequest("compose-1")

	surface, err := fixture.repo.BeginCardInteraction(context.Background(), request)
	if err != nil {
		t.Fatalf("BeginCardInteraction() error = %v", err)
	}
	if surface.Status != agentcard.SurfaceStatusDraft ||
		surface.Revision != request.Revision ||
		surface.InteractionID != request.InteractionID {
		t.Fatalf("surface = %#v", surface)
	}

	var run model.AgentRun
	if err := fixture.db.First(&run, "id = ?", fixture.runID).Error; err != nil {
		t.Fatalf("load run: %v", err)
	}
	if run.Status != string(agentruntime.RunStatusWaitingCallback) ||
		run.WaitingReason != string(agentruntime.WaitingReasonCallback) ||
		run.WaitingToken != request.TokenHash ||
		run.Revision != request.Revision {
		t.Fatalf("waiting run = %#v", run)
	}
	var session model.AgentSession
	if err := fixture.db.First(&session, "id = ?", fixture.sessionID).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.ActiveRunID != fixture.runID {
		t.Fatalf("active run = %q", session.ActiveRunID)
	}

	var wait model.AgentStep
	if err := fixture.db.First(&wait, "id = ?", request.StepID).Error; err != nil {
		t.Fatalf("load wait: %v", err)
	}
	if wait.Kind != string(agentruntime.StepKindWait) ||
		wait.ExternalRef != request.InteractionID ||
		strings.Contains(wait.InputJSON, "plaintext-token") ||
		!strings.Contains(wait.InputJSON, "trusted-task") {
		t.Fatalf("wait step = %#v", wait)
	}
	var outboxCount int64
	if err := fixture.db.Model(&model.AgentProjectionOutbox{}).
		Where("step_id = ?", request.StepID).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("outbox count = %d, want 1", outboxCount)
	}
}

func TestBeginCardInteractionRollsBackEveryRuntimeFactWhenSurfaceInsertFails(t *testing.T) {
	fixture := newCardStoreFixture(t)
	trigger := `CREATE FUNCTION fail_card_surface_insert() RETURNS trigger AS $$
BEGIN
	RAISE EXCEPTION 'injected surface failure';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER fail_card_surface_insert
BEFORE INSERT ON agent_card_surfaces
FOR EACH ROW EXECUTE FUNCTION fail_card_surface_insert();`
	if err := fixture.db.Exec(trigger).Error; err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}

	request := fixture.beginRequest("compose-failure")
	if _, err := fixture.repo.BeginCardInteraction(
		context.Background(),
		request,
	); err == nil {
		t.Fatal("BeginCardInteraction() unexpectedly succeeded")
	}
	for table, where := range map[string]string{
		"agent_steps":             "run_id = ?",
		"agent_projection_outbox": "step_id = ?",
		"agent_card_surfaces":     "run_id = ?",
	} {
		var count int64
		arg := fixture.runID
		if table == "agent_projection_outbox" {
			arg = request.StepID
		}
		if err := fixture.db.Table(table).Where(where, arg).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count after rollback = %d", table, count)
		}
	}
	var run model.AgentRun
	if err := fixture.db.First(&run, "id = ?", fixture.runID).Error; err != nil {
		t.Fatalf("load run: %v", err)
	}
	if run.Status != string(agentruntime.RunStatusRunning) ||
		run.Revision != 1 || run.WaitingReason != "" || run.WaitingToken != "" {
		t.Fatalf("run changed despite rollback: %#v", run)
	}
	var session model.AgentSession
	if err := fixture.db.First(&session, "id = ?", fixture.sessionID).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.ActiveRunID != "" {
		t.Fatalf("session active run changed despite rollback: %q", session.ActiveRunID)
	}
}

func TestBeginCardInteractionIsIdempotentAndRejectsComposeCollision(t *testing.T) {
	fixture := newCardStoreFixture(t)
	request := fixture.beginRequest("compose-replay")
	first, err := fixture.repo.BeginCardInteraction(context.Background(), request)
	if err != nil {
		t.Fatalf("first BeginCardInteraction() error = %v", err)
	}
	replay, err := fixture.repo.BeginCardInteraction(context.Background(), request)
	if err != nil {
		t.Fatalf("replay BeginCardInteraction() error = %v", err)
	}
	if replay.ID != first.ID || replay.InteractionID != first.InteractionID {
		t.Fatalf("replay changed identity: first=%#v replay=%#v", first, replay)
	}

	collision := request
	collision.SpecJSON = `{"version":"agent-card/v1","title":"forged","blocks":[]}`
	if _, err := fixture.repo.BeginCardInteraction(
		context.Background(),
		collision,
	); !errors.Is(err, agentcard.ErrCardConflict) {
		t.Fatalf("collision error = %v, want card conflict", err)
	}

	second := fixture.beginRequest("compose-second")
	if _, err := fixture.repo.BeginCardInteraction(
		context.Background(),
		second,
	); !errors.Is(err, agentcard.ErrCardConflict) {
		t.Fatalf("second blocking surface error = %v, want card conflict", err)
	}
}

func TestBinderCompilerAndStoreKeepPlaintextTokenOutOfPostgres(t *testing.T) {
	fixture := newCardStoreFixture(t)
	binder, err := agentcard.NewBinder(agentcard.BinderOptions{
		Store: fixture.repo, Compiler: agentcardcompiler.New(),
		BindingKey: []byte("0123456789abcdef0123456789abcdef"),
		Now:        time.Now,
	})
	if err != nil {
		t.Fatalf("NewBinder() error = %v", err)
	}
	capability, err := agentcard.NewTrustedCapability(
		"schedule.update",
		json.RawMessage(`{"task_id":"trusted-task"}`),
	)
	if err != nil {
		t.Fatalf("NewTrustedCapability() error = %v", err)
	}
	result, err := binder.BindAndBegin(context.Background(), agentcard.BindRequest{
		RunID: fixture.runID, ExpectedRunRevision: 1, ChatID: "chat-1",
		ReplyToMessageID: "message-1", ExpectedActorOpenID: "owner-1",
		InteractionKind: "agent_card", IdempotencyKey: "compose-e2e",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		Spec: agentcard.CardSpec{
			Version: agentcard.VersionV1, Title: "确认修改",
			Blocks: []agentcard.Block{agentcard.Markdown("summary", "请确认")},
			Actions: []agentcard.Action{{
				Kind: agentcard.ActionButton, ID: "confirm", Label: "确认",
				Mode:   agentcard.ActionModeCapabilityConfirm,
				Intent: "schedule.update",
			}},
		},
		TrustedCapabilities: map[string]agentcard.TrustedCapability{
			"confirm": capability,
		},
		Projection: agentruntime.ProjectionDocument{
			IndexAlias: "agent-conversations", DocumentID: fixture.runID,
			Payload: json.RawMessage(`{"event_type":"agent_card_wait"}`),
		},
	})
	if err != nil {
		t.Fatalf("BindAndBegin() error = %v", err)
	}
	token := findJSONToken(t, result.CompiledJSON)
	if token == "" || token == "[REDACTED]" {
		t.Fatalf("immediate compile token = %q", token)
	}

	var wait model.AgentStep
	if err := fixture.db.First(
		&wait,
		"id = ?",
		result.Surface.WaitStepID,
	).Error; err != nil {
		t.Fatalf("load wait: %v", err)
	}
	var surface model.AgentCardSurface
	if err := fixture.db.First(
		&surface,
		"id = ?",
		result.Surface.ID,
	).Error; err != nil {
		t.Fatalf("load surface: %v", err)
	}
	if strings.Contains(wait.InputJSON, token) ||
		strings.Contains(surface.SpecJSON, token) ||
		strings.Contains(surface.CompiledJSONRedacted, token) {
		t.Fatal("plaintext callback token was persisted")
	}
	if !strings.Contains(surface.CompiledJSONRedacted, "[REDACTED]") {
		t.Fatalf("compiled redacted artifact = %s", surface.CompiledJSONRedacted)
	}
	if !strings.Contains(wait.InputJSON, agentruntime.HashInteractionToken(token)) {
		t.Fatal("wait step does not contain the callback token hash")
	}
}

type cardStoreFixture struct {
	db        *gorm.DB
	repo      *Repository
	sessionID string
	runID     string
}

func newCardStoreFixture(t *testing.T) *cardStoreFixture {
	t.Helper()
	configPath := os.Getenv("BETAGO_CONFIG_PATH")
	if configPath == "" {
		t.Skip("BETAGO_CONFIG_PATH is not set; skipping PostgreSQL integration test")
	}
	cfg, err := config.LoadFileE(configPath)
	if err != nil || cfg == nil || cfg.DBConfig == nil {
		t.Skip("PostgreSQL test configuration is unavailable")
	}
	rootDB, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Skip("PostgreSQL is unavailable")
	}
	rootSQLDB, err := rootDB.DB()
	if err != nil || rootSQLDB.PingContext(context.Background()) != nil {
		t.Skip("PostgreSQL is unavailable")
	}
	var installed bool
	if err := rootDB.Raw(
		`SELECT to_regclass('betago.agent_card_surfaces') IS NOT NULL`,
	).Scan(&installed).Error; err != nil || !installed {
		_ = rootSQLDB.Close()
		t.Skip("agent card surface migration is not installed")
	}

	suffix := strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	schema := "agentcardstore_test_" + suffix
	if err := rootDB.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Error; err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	for _, statement := range []string{
		fmt.Sprintf(`CREATE TABLE %q.agent_sessions (LIKE betago.agent_sessions INCLUDING ALL)`, schema),
		fmt.Sprintf(`CREATE TABLE %q.agent_runs (LIKE betago.agent_runs INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_runs ADD FOREIGN KEY (session_id) REFERENCES %q.agent_sessions(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`CREATE TABLE %q.agent_steps (LIKE betago.agent_steps INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_steps ADD FOREIGN KEY (run_id) REFERENCES %q.agent_runs(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`CREATE TABLE %q.agent_capability_executions (LIKE betago.agent_capability_executions INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_capability_executions ADD FOREIGN KEY (run_id) REFERENCES %q.agent_runs(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_capability_executions ADD FOREIGN KEY (step_id) REFERENCES %q.agent_steps(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`CREATE TABLE %q.agent_projection_outbox (LIKE betago.agent_projection_outbox INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_projection_outbox ADD FOREIGN KEY (step_id) REFERENCES %q.agent_steps(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`CREATE TABLE %q.agent_card_surfaces (LIKE betago.agent_card_surfaces INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_card_surfaces ADD FOREIGN KEY (run_id) REFERENCES %q.agent_runs(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_card_surfaces ADD FOREIGN KEY (wait_step_id) REFERENCES %q.agent_steps(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_sessions ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_runs ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_steps ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_capability_executions ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_projection_outbox ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_card_surfaces ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
	} {
		if err := rootDB.Exec(statement).Error; err != nil {
			_ = rootDB.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error
			_ = rootSQLDB.Close()
			t.Fatalf("initialize isolated schema: %v", err)
		}
	}
	testConfig := *cfg.DBConfig
	testConfig.SearchPath = schema
	testDB, err := gorm.Open(postgres.Open(testConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Fatalf("open isolated schema: %v", err)
	}
	testSQLDB, err := testDB.DB()
	if err != nil {
		t.Fatalf("get isolated database: %v", err)
	}
	t.Cleanup(func() {
		_ = testSQLDB.Close()
		if err := rootDB.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error; err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
		_ = rootSQLDB.Close()
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	sessionID := "session_" + suffix
	runID := "run_" + suffix
	if err := testDB.Create(&model.AgentSession{
		ID: sessionID, TenantID: repositoryTestTenant.ID,
		AppID: repositoryTestTenant.AppID, BotOpenID: repositoryTestTenant.BotOpenID,
		ChatID: "chat_" + suffix, ScopeType: "chat", ScopeID: "scope_" + suffix,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := testDB.Create(&model.AgentRun{
		ID: runID, TenantID: repositoryTestTenant.ID, SessionID: sessionID,
		TriggerType:      string(agentruntime.TriggerTypeMention),
		TriggerMessageID: "message_" + suffix,
		Status:           string(agentruntime.RunStatusRunning), Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create run: %v", err)
	}
	return &cardStoreFixture{
		db: testDB, repo: mustNewTestRepository(testDB),
		sessionID: sessionID, runID: runID,
	}
}

func (f *cardStoreFixture) beginRequest(composeKey string) agentcard.BeginCardInteractionRequest {
	sum := agentruntime.HashInteractionToken("token-" + composeKey)
	suffix := strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	trusted, _ := json.Marshal(agentcard.TrustedWaitInput{
		Version: 1, ComposeKey: composeKey, SpecDigest: strings.Repeat("a", 64),
		ActorPolicy: agentcard.ActorPolicy{
			Mode: agentcard.ActorPolicyOwner, OpenID: "owner-1",
		},
		ActionBindings: []agentcard.TrustedActionDescriptor{{
			ActionID: "confirm", Mode: agentcard.ActionModeCapabilityConfirm,
			Intent: "schedule.update", ContinueAgent: true,
			CapabilityName:  "schedule.update",
			CapabilityInput: json.RawMessage(`{"task_id":"trusted-task"}`),
		}},
	})
	return agentcard.BeginCardInteractionRequest{
		SurfaceID: "surface_" + composeKey, RunID: f.runID,
		StepID: "step_" + suffix, InteractionID: "interaction_" + suffix,
		IdempotencyKey: composeKey, ExpectedRunRevision: 1, Revision: 2,
		TokenHash: sum, InteractionKind: "agent_card",
		ExpiresAt: time.Now().UTC().Add(time.Hour), ExpectedActorOpenID: "owner-1",
		ChatID: "chat-1", ReplyToMessageID: "message-1",
		SpecVersion:          agentcard.VersionV1,
		SpecJSON:             `{"version":"agent-card/v1","title":"确认","blocks":[]}`,
		CompiledJSONRedacted: `{"schema":"2.0","token":"[REDACTED]"}`,
		TrustedInput:         trusted,
		Projection: agentruntime.ProjectionDocument{
			IndexAlias: "agent-conversations", DocumentID: f.runID,
			Payload: json.RawMessage(`{"event_type":"agent_card_wait"}`),
		},
	}
}

func findJSONToken(t *testing.T, document json.RawMessage) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatalf("decode compiled card: %v", err)
	}
	var visit func(any) string
	visit = func(current any) string {
		switch typed := current.(type) {
		case map[string]any:
			if token, ok := typed["token"].(string); ok {
				return token
			}
			for _, child := range typed {
				if token := visit(child); token != "" {
					return token
				}
			}
		case []any:
			for _, child := range typed {
				if token := visit(child); token != "" {
					return token
				}
			}
		}
		return ""
	}
	return visit(value)
}
