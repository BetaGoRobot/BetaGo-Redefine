package schema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	sqlmigrations "github.com/BetaGoRobot/BetaGo-Redefine/script/sql"
)

type MigrationHook func(context.Context, *sql.Tx, string) error

type Migration struct {
	Version          string
	SQL              string
	NonTransactional bool
	Checksum         string
	Before           MigrationHook
}

var defaultMigrations = buildDefaultMigrations()

func DefaultMigrations() []Migration {
	return append([]Migration(nil), defaultMigrations...)
}

func buildDefaultMigrations() []Migration {
	migrations := []Migration{
		loadSQLMigration("20260318_agent_runtime_tables.sql", false),
		loadSQLMigration("20260325_agent_runtime_stale_run_recovery.sql", false),
		loadSQLMigration("20260728_conversation_callback_runtime.sql", true),
		loadSQLMigration("20260728_agent_card_surfaces.sql", true),
		loadSQLMigration("20260728_conversation_parallel_evaluation.sql", false),
		loadSQLMigration("20260729_conversation_evaluation_runtime.sql", false),
		loadSQLMigration("20260730_runtime_tenant_prepare.sql", false),
		{
			Version: "20260730_runtime_tenant_backfill",
			SQL:     "-- go migration: tenant_backfill_v1",
			Before:  tenantBackfill,
		},
		loadSQLMigration("20260730_runtime_tenant_constraints.sql", false),
		loadSQLMigration("20260803_llm_usage_business_taxonomy.sql", false),
	}
	for index := range migrations {
		migrations[index].Checksum = migrationChecksum(migrations[index])
	}
	return migrations
}

func loadSQLMigration(name string, nonTransactional bool) Migration {
	contents, err := sqlmigrations.Files.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("embed runtime migration %s: %v", name, err))
	}
	version := name
	if len(version) > 4 && version[len(version)-4:] == ".sql" {
		version = version[:len(version)-4]
	}
	return Migration{
		Version:          version,
		SQL:              string(contents),
		NonTransactional: nonTransactional,
	}
}

func migrationChecksum(migration Migration) string {
	sum := sha256.Sum256([]byte(
		migration.Version + "\x00" +
			fmt.Sprintf("%t", migration.NonTransactional) + "\x00" +
			migration.SQL,
	))
	return hex.EncodeToString(sum[:])
}
