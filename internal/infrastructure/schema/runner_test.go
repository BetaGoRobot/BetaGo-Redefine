package schema

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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
	database, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	schemaName := "zerotouch_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	if err := database.Exec(
		fmt.Sprintf(`CREATE SCHEMA %s`, quoteTestIdent(schemaName)),
	).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, sqlErr := database.DB()
		if sqlErr == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = sqlDB.ExecContext(
				ctx,
				fmt.Sprintf(`DROP SCHEMA %s CASCADE`, quoteTestIdent(schemaName)),
			)
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

func quoteTestIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
