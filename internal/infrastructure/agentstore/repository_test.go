package agentstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	uuid "github.com/satori/go.uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

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
		ID: sessionID, AppID: "app_" + suffix, BotOpenID: "bot_" + suffix,
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
		ID: runID, SessionID: sessionID, TriggerType: string(agentruntime.TriggerTypeMention),
		TriggerMessageID: "message_" + suffix, Status: string(status), Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := testDB.Create(run).Error; err != nil {
		t.Fatalf("create run fixture: %v", err)
	}
	return &repositoryFixture{db: testDB, repo: NewRepository(testDB), sessionID: sessionID, runID: runID}
}

func (f *repositoryFixture) createStep(t *testing.T, step *agentruntime.AgentStep) {
	t.Helper()
	if step.ID == "" {
		step.ID = "step_test_" + uuid.NewV4().String()
	}
	if step.RunID == "" {
		step.RunID = f.runID
	}
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
