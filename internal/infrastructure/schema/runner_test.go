package schema

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/satori/go.uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRunnerBootstrapsEmptySchema(t *testing.T) {
	fixture := newRunnerFixture(t)
	report, err := fixture.runner().Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(report.Applied) != len(DefaultMigrations()) || len(report.Skipped) != 0 {
		t.Fatalf("Apply() report = %#v", report)
	}
	if report.LatestVersion == "" || report.LatestChecksum == "" ||
		report.CompletedAt.IsZero() {
		t.Fatalf("Apply() report lacks readiness metadata: %#v", report)
	}

	requiredTables := []string{
		"runtime_schema_migrations",
		"agent_sessions",
		"agent_runs",
		"agent_steps",
		"agent_capability_executions",
		"agent_projection_outbox",
		"agent_card_surfaces",
		"evaluation_cohorts",
		"evaluation_episodes",
		"evaluation_episode_messages",
		"evaluation_candidate_tasks",
		"evaluation_lane_outputs",
		"evaluation_feedback",
		"evaluation_judgments",
	}
	for _, table := range requiredTables {
		if !fixture.tableExists(t, table) {
			t.Errorf("table %s.%s was not created", fixture.schema, table)
		}
	}
	for _, table := range requiredTables[1:] {
		if !fixture.columnExists(t, table, "tenant_id") {
			t.Errorf("column %s.%s.tenant_id was not created", fixture.schema, table)
		} else if !fixture.columnIsNotNull(t, table, "tenant_id") {
			t.Errorf("column %s.%s.tenant_id is nullable", fixture.schema, table)
		}
	}
	for _, constraint := range []string{
		"agent_runs_tenant_session_fk",
		"agent_steps_tenant_run_fk",
		"agent_capability_tenant_run_fk",
		"agent_capability_tenant_step_fk",
		"agent_outbox_tenant_step_fk",
		"agent_card_tenant_run_fk",
		"agent_card_tenant_step_fk",
		"evaluation_episode_tenant_cohort_fk",
		"evaluation_message_tenant_episode_fk",
		"evaluation_task_tenant_episode_fk",
		"evaluation_lane_tenant_episode_fk",
		"evaluation_feedback_tenant_episode_fk",
		"evaluation_judgment_tenant_episode_fk",
	} {
		if !fixture.constraintExists(t, constraint) {
			t.Errorf("tenant constraint %s was not created", constraint)
		}
	}
}

func TestRunnerIsIdempotent(t *testing.T) {
	fixture := newRunnerFixture(t)
	if _, err := fixture.runner().Apply(context.Background()); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	report, err := fixture.runner().Apply(context.Background())
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if len(report.Applied) != 0 || len(report.Skipped) != len(DefaultMigrations()) {
		t.Fatalf("second Apply() report = %#v", report)
	}

	var count int64
	if err := fixture.db.Raw(
		fmt.Sprintf(`SELECT count(*) FROM %s.runtime_schema_migrations`, quoteTestIdent(fixture.schema)),
	).Scan(&count).Error; err != nil {
		t.Fatalf("count migration ledger: %v", err)
	}
	if count != int64(len(DefaultMigrations())) {
		t.Fatalf("migration ledger count = %d, want %d", count, len(DefaultMigrations()))
	}
}

func TestConcurrentRunnersApplyEachVersionOnce(t *testing.T) {
	fixture := newRunnerFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := fixture.runner().Apply(context.Background())
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Apply() error = %v", err)
		}
	}

	var count int64
	if err := fixture.db.Raw(
		fmt.Sprintf(`SELECT count(*) FROM %s.runtime_schema_migrations`, quoteTestIdent(fixture.schema)),
	).Scan(&count).Error; err != nil {
		t.Fatalf("count migration ledger: %v", err)
	}
	if count != int64(len(DefaultMigrations())) {
		t.Fatalf("migration ledger count = %d, want %d", count, len(DefaultMigrations()))
	}
}

func TestRunnerRejectsChecksumDrift(t *testing.T) {
	fixture := newRunnerFixture(t)
	if _, err := fixture.runner().Apply(context.Background()); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}

	migration := DefaultMigrations()[0]
	migration.SQL += "\nselect 1;"
	runner := fixture.runner()
	runner.Migrations = []Migration{migration}
	if _, err := runner.Apply(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Apply() checksum drift error = %v", err)
	}
}

func TestRunnerDoesNotWrapConcurrentIndexInTransaction(t *testing.T) {
	fixture := newRunnerFixture(t)
	if _, err := fixture.runner().Apply(context.Background()); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	requiredIndexes := []string{
		"idx_agent_runs_session_last_relevant",
		"idx_agent_steps_run_dedupe_unique",
		"idx_agent_card_surfaces_message",
	}
	for _, index := range requiredIndexes {
		var valid bool
		err := fixture.db.Raw(`
			SELECT i.indisvalid
			FROM pg_catalog.pg_index i
			JOIN pg_catalog.pg_class c ON c.oid = i.indexrelid
			JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = ? AND c.relname = ?`,
			fixture.schema,
			index,
		).Scan(&valid).Error
		if err != nil {
			t.Fatalf("inspect index %s: %v", index, err)
		}
		if !valid {
			t.Errorf("index %s is missing or invalid", index)
		}
	}
}

func TestRunnerBackfillsExistingAgentAndEvaluationTenantChains(t *testing.T) {
	fixture := newRunnerFixture(t)
	migrations := DefaultMigrations()
	prepare := fixture.runner()
	prepare.Migrations = migrations[:7]
	if _, err := prepare.Apply(context.Background()); err != nil {
		t.Fatalf("prepare legacy schema: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	inserts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO agent_sessions (
			id, app_id, bot_open_id, chat_id, scope_type, scope_id, status
		) VALUES ('session-legacy', 'app-legacy', 'bot-legacy', 'chat', 'chat', 'scope', 'active')`, nil},
		{`INSERT INTO agent_runs (
			id, session_id, trigger_type, status
		) VALUES ('run-legacy', 'session-legacy', 'message', 'active')`, nil},
		{`INSERT INTO agent_steps (
			id, run_id, index, kind, status
		) VALUES ('step-legacy', 'run-legacy', 0, 'message', 'completed')`, nil},
		{`INSERT INTO evaluation_cohorts (
			id, app_id, bot_open_id, chat_ids, start_at, end_at, status,
			serving_lane, control_version, candidate_version
		) VALUES (
			'cohort-legacy', 'app-legacy', 'bot-legacy', '["chat"]'::jsonb,
			?, ?, 'collecting', 'control', 'control-v1', 'candidate-v1'
		)`, []any{now.Add(-time.Hour), now.Add(time.Hour)}},
		{`INSERT INTO evaluation_episodes (
			id, cohort_id, chat_id, anchor_event_id, anchor_message_id,
			serving_lane, status, pre_window_start, anchor_at,
			late_feedback_until
		) VALUES (
			'episode-legacy', 'cohort-legacy', 'chat', 'event', 'message',
			'control', 'collecting', ?, ?, ?
		)`, []any{now.Add(-time.Minute), now, now.Add(24 * time.Hour)}},
		{`INSERT INTO evaluation_episode_messages (
			id, episode_id, position, event_id, message_id, sequence,
			occurred_at
		) VALUES (
			'message-legacy', 'episode-legacy', 'anchor', 'event', 'message',
			0, ?
		)`, []any{now}},
	}
	for index, insert := range inserts {
		if err := fixture.db.Exec(insert.sql, insert.args...).Error; err != nil {
			t.Fatalf("insert legacy row %d: %v", index, err)
		}
	}
	harden := fixture.runner()
	harden.Migrations = migrations[7:]
	if _, err := harden.Apply(context.Background()); err != nil {
		t.Fatalf("harden legacy schema: %v", err)
	}
	owner, _ := tenant.New("app-legacy", "bot-legacy")
	for table, id := range map[string]string{
		"agent_sessions":              "session-legacy",
		"agent_runs":                  "run-legacy",
		"agent_steps":                 "step-legacy",
		"evaluation_cohorts":          "cohort-legacy",
		"evaluation_episodes":         "episode-legacy",
		"evaluation_episode_messages": "message-legacy",
	} {
		var tenantID string
		if err := fixture.db.Table(table).
			Select("tenant_id").
			Where("id = ?", id).
			Scan(&tenantID).Error; err != nil {
			t.Fatalf("read %s tenant: %v", table, err)
		}
		if tenantID != owner.ID {
			t.Fatalf("%s tenant_id = %q, want %q", table, tenantID, owner.ID)
		}
	}
	report, err := harden.Apply(context.Background())
	if err != nil {
		t.Fatalf("restart hardened schema: %v", err)
	}
	if len(report.Applied) != 0 || len(report.Skipped) != len(migrations[7:]) {
		t.Fatalf("restart hardening report = %#v", report)
	}
}

func TestRunnerFailsClosedForUnresolvableLegacyTenant(t *testing.T) {
	fixture := newRunnerFixture(t)
	migrations := DefaultMigrations()
	prepare := fixture.runner()
	prepare.Migrations = migrations[:7]
	if _, err := prepare.Apply(context.Background()); err != nil {
		t.Fatalf("prepare legacy schema: %v", err)
	}
	if err := fixture.db.Exec(`
		INSERT INTO agent_sessions (
			id, app_id, bot_open_id, chat_id, scope_type, scope_id, status
		) VALUES ('session-unresolved', '', '', 'chat', 'chat', 'scope', 'active')`,
	).Error; err != nil {
		t.Fatal(err)
	}
	harden := fixture.runner()
	harden.Migrations = migrations[7:]
	if _, err := harden.Apply(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "resolve tenant root") {
		t.Fatalf("unresolvable tenant hardening error = %v", err)
	}
	var nullable string
	if err := fixture.db.Raw(`
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = 'agent_sessions'
		  AND column_name = 'tenant_id'`,
		fixture.schema,
	).Scan(&nullable).Error; err != nil {
		t.Fatal(err)
	}
	if nullable != "YES" {
		t.Fatalf("failed hardening partially applied tenant constraint: is_nullable=%q", nullable)
	}
}

type runnerFixture struct {
	db     *gorm.DB
	schema string
}

func newRunnerFixture(t *testing.T) *runnerFixture {
	t.Helper()
	path := strings.TrimSpace(os.Getenv("BETAGO_CONFIG_PATH"))
	if path == "" {
		t.Skip("BETAGO_CONFIG_PATH is not set")
	}
	cfg := infraConfig.LoadFile(path)
	if cfg == nil || cfg.DBConfig == nil {
		t.Fatal("configured PostgreSQL is unavailable")
	}
	rootDatabase, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	schemaName := "zerotouch_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	if err := rootDatabase.Exec(
		fmt.Sprintf(`CREATE SCHEMA %s`, quoteTestIdent(schemaName)),
	).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	testConfig := *cfg.DBConfig
	testConfig.SearchPath = schemaName
	testConfig.ApplicationName = schemaName
	database, err := gorm.Open(postgres.Open(testConfig.DSN()))
	if err != nil {
		_ = rootDatabase.Exec(
			fmt.Sprintf(`DROP SCHEMA %s CASCADE`, quoteTestIdent(schemaName)),
		).Error
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, sqlErr := database.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rootSQL, rootErr := rootDatabase.DB()
		if rootErr == nil {
			_, _ = rootSQL.ExecContext(
				ctx,
				fmt.Sprintf(`DROP SCHEMA %s CASCADE`, quoteTestIdent(schemaName)),
			)
			_ = rootSQL.Close()
		}
	})
	return &runnerFixture{db: database, schema: schemaName}
}

func (f *runnerFixture) runner() *Runner {
	return &Runner{
		DB:         f.db,
		Schema:     f.schema,
		Revision:   "test-revision",
		Migrations: DefaultMigrations(),
	}
}

func (f *runnerFixture) tableExists(t *testing.T, table string) bool {
	t.Helper()
	var exists bool
	if err := f.db.Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = ? AND table_name = ?
		)`,
		f.schema,
		table,
	).Scan(&exists).Error; err != nil {
		t.Fatalf("inspect table %s: %v", table, err)
	}
	return exists
}

func (f *runnerFixture) columnExists(t *testing.T, table, column string) bool {
	t.Helper()
	var exists bool
	if err := f.db.Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = ? AND table_name = ? AND column_name = ?
		)`,
		f.schema,
		table,
		column,
	).Scan(&exists).Error; err != nil {
		t.Fatalf("inspect column %s.%s: %v", table, column, err)
	}
	return exists
}

func (f *runnerFixture) columnIsNotNull(
	t *testing.T,
	table string,
	column string,
) bool {
	t.Helper()
	var nullable string
	if err := f.db.Raw(`
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ? AND column_name = ?`,
		f.schema,
		table,
		column,
	).Scan(&nullable).Error; err != nil {
		t.Fatalf("inspect nullability %s.%s: %v", table, column, err)
	}
	return nullable == "NO"
}

func (f *runnerFixture) constraintExists(t *testing.T, name string) bool {
	t.Helper()
	var exists bool
	if err := f.db.Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM pg_catalog.pg_constraint c
			JOIN pg_catalog.pg_namespace n ON n.oid = c.connamespace
			WHERE n.nspname = ? AND c.conname = ?
		)`,
		f.schema,
		name,
	).Scan(&exists).Error; err != nil {
		t.Fatalf("inspect constraint %s: %v", name, err)
	}
	return exists
}

func quoteTestIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
