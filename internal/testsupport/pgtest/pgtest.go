package pgtest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	uuid "github.com/satori/go.uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func OpenTempSchema(t testing.TB) *gorm.DB {
	t.Helper()

	cfg := config.Get()
	if cfg == nil || cfg.DBConfig == nil {
		t.Skip("postgres test config is missing")
	}

	rootDB, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Skipf("postgres is unavailable for test: %v", err)
	}

	schema := "agenttest_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	if err := rootDB.Exec(fmt.Sprintf("create schema if not exists %s", quoteIdentifier(schema))).Error; err != nil {
		t.Fatalf("create temp schema failed: %v", err)
	}

	testCfg := *cfg.DBConfig
	testCfg.SearchPath = schema
	db, err := gorm.Open(postgres.Open(testCfg.DSN()), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() temp schema error = %v", err)
	}

	t.Cleanup(func() {
		closeDB(t, db)
		if err := rootDB.Exec(fmt.Sprintf("drop schema if exists %s cascade", quoteIdentifier(schema))).Error; err != nil {
			t.Fatalf("drop temp schema failed: %v", err)
		}
		closeDB(t, rootDB)
	})

	return db
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func closeDB(t testing.TB, db *gorm.DB) {
	t.Helper()
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	_ = sqlDB.Close()
}
