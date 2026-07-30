package agentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
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

func TestRepositoryScopesGeneratedQueriesToTenant(t *testing.T) {
	owner, err := tenant.New("app-a", "bot-a")
	if err != nil {
		t.Fatal(err)
	}
	database, err := gorm.Open(postgres.New(postgres.Config{
		Conn: &dryRunPool{},
	}), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(database, owner)
	if err != nil {
		t.Fatal(err)
	}
	statement := repository.db.Model(&model.AgentRun{}).
		Where("id = ?", "run-other").
		Find(&model.AgentRun{}).Statement
	if sql := statement.SQL.String(); !strings.Contains(sql, `"tenant_id" = $2`) {
		t.Fatalf("tenant predicate missing from SQL: %s", sql)
	}
	if len(statement.Vars) != 2 || statement.Vars[1] != owner.ID {
		t.Fatalf("tenant bind missing from vars: %#v", statement.Vars)
	}
}

func TestRepositoryDoesNotReadOrClaimAnotherTenant(t *testing.T) {
	fixture := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	queued := &agentruntime.AgentStep{
		ID:    "step_tenant_" + uuid.NewV4().String(),
		RunID: fixture.runID, Kind: agentruntime.StepKindObserve,
		Status: agentruntime.StepStatusQueued, InputJSON: "{}", OutputJSON: "{}",
		CreatedAt: time.Now().UTC(),
	}
	if _, err := fixture.repo.AppendEvent(
		context.Background(), queued, testProjection(fixture.runID),
	); err != nil {
		t.Fatal(err)
	}
	otherTenant, err := tenant.New("app-agentstore-other", "bot-agentstore-other")
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewRepository(fixture.db, otherTenant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.FindRunChatID(
		context.Background(), fixture.runID,
	); !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("cross-tenant FindRunChatID() error = %v, want ErrNotFound", err)
	}
	if _, err := other.ClaimQueuedStep(
		context.Background(),
		agentruntime.StepClaim{
			WorkerID: "other-worker", LeaseTTL: time.Minute,
			Now: time.Now().UTC(),
		},
	); !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("cross-tenant ClaimQueuedStep() error = %v, want ErrNotFound", err)
	}
}

type dryRunPool struct{}

func (*dryRunPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("dry-run connection must not prepare")
}

func (*dryRunPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("dry-run connection must not execute")
}

func (*dryRunPool) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("dry-run connection must not query")
}

func (*dryRunPool) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return &sql.Row{}
}

var repositoryTestTenant, _ = tenant.New("app-agentstore-test", "bot-agentstore-test")

func mustNewTestRepository(database *gorm.DB) *Repository {
	repository, err := NewRepository(database, repositoryTestTenant)
	if err != nil {
		panic(err)
	}
	return repository
}

type repositoryFixture struct {
	db        *gorm.DB
	repo      *Repository
	sessionID string
	runID     string
}

func newRepositoryFixture(t *testing.T, status agentruntime.RunStatus) *repositoryFixture {
	t.Helper()
	configPath := os.Getenv("BETAGO_CONFIG_PATH")
	if configPath == "" {
		t.Skip("BETAGO_CONFIG_PATH is not set; skipping PostgreSQL integration test")
	}
	cfg, err := config.LoadFileE(configPath)
	if err != nil || cfg == nil || cfg.DBConfig == nil {
		t.Skip("PostgreSQL test configuration is unavailable")
	}
	db, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Skip("PostgreSQL is unavailable")
	}
	rootSQLDB, err := db.DB()
	if err != nil || rootSQLDB.PingContext(context.Background()) != nil {
		t.Skip("PostgreSQL is unavailable")
	}

	var exists bool
	if err := db.Raw(`SELECT to_regclass('betago.agent_projection_outbox') IS NOT NULL`).Scan(&exists).Error; err != nil || !exists {
		_ = rootSQLDB.Close()
		t.Skip("conversation runtime migration is not installed")
	}

	suffix := uuid.NewV4().String()
	schema := "agentstore_test_" + strings.ReplaceAll(suffix, "-", "")
	if err := db.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Error; err != nil {
		_ = rootSQLDB.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	ddl := []string{
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
		fmt.Sprintf(`CREATE TABLE %q.scheduled_tasks (LIKE betago.scheduled_tasks INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_sessions ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_runs ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_steps ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_capability_executions ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.agent_projection_outbox ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT ''`, schema),
	}
	for _, statement := range ddl {
		if err := db.Exec(statement).Error; err != nil {
			_ = db.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error
			_ = rootSQLDB.Close()
			t.Fatalf("initialize isolated schema: %v", err)
		}
	}
	testConfig := *cfg.DBConfig
	testConfig.SearchPath = schema
	testDB, err := gorm.Open(postgres.Open(testConfig.DSN()), &gorm.Config{})
	if err != nil {
		_ = db.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error
		_ = rootSQLDB.Close()
		t.Fatalf("open isolated schema: %v", err)
	}
	testSQLDB, err := testDB.DB()
	if err != nil {
		t.Fatalf("get isolated database handle: %v", err)
	}
	t.Cleanup(func() {
		_ = testSQLDB.Close()
		if err := db.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error; err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
		_ = rootSQLDB.Close()
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	sessionID := "session_test_" + suffix
	runID := "run_test_" + suffix
	session := &model.AgentSession{
		ID: sessionID, TenantID: repositoryTestTenant.ID,
		AppID: repositoryTestTenant.AppID, BotOpenID: repositoryTestTenant.BotOpenID,
		ChatID: "chat_" + suffix, ScopeType: "chat", ScopeID: "scope_" + suffix,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := testDB.Create(session).Error; err != nil {
		t.Fatalf("create session fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := testDB.Exec("DELETE FROM agent_sessions WHERE id = ?", sessionID).Error; err != nil {
			t.Errorf("cleanup session fixture: %v", err)
		}
	})
	run := &model.AgentRun{
		ID: runID, TenantID: repositoryTestTenant.ID,
		SessionID: sessionID, TriggerType: string(agentruntime.TriggerTypeMention),
		TriggerMessageID: "message_" + suffix, Status: string(status), Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := testDB.Create(run).Error; err != nil {
		t.Fatalf("create run fixture: %v", err)
	}
	return &repositoryFixture{
		db: testDB, repo: mustNewTestRepository(testDB),
		sessionID: sessionID, runID: runID,
	}
}

func (f *repositoryFixture) createStep(t *testing.T, step *agentruntime.AgentStep) {
	t.Helper()
	if step.ID == "" {
		step.ID = "step_test_" + uuid.NewV4().String()
	}
	if step.RunID == "" {
		step.RunID = f.runID
	}
	step.TenantID = repositoryTestTenant.ID
	if step.InputJSON == "" {
		step.InputJSON = "{}"
	}
	if step.OutputJSON == "" {
		step.OutputJSON = "{}"
	}
	if step.CreatedAt.IsZero() {
		step.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	if err := f.db.Create(toDBStep(step)).Error; err != nil {
		t.Fatalf("create step fixture: %v", err)
	}
}

func testProjection(runID string) agentruntime.ProjectionDocument {
	return agentruntime.ProjectionDocument{
		IndexAlias: "agent-conversations",
		DocumentID: runID,
		Payload:    []byte(`{"state":"queued"}`),
	}
}
