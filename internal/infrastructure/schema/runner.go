package schema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

var schemaPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Runner struct {
	DB         *gorm.DB
	Schema     string
	Revision   string
	Migrations []Migration
}

type Report struct {
	Applied        []string
	Skipped        []string
	LatestVersion  string
	LatestChecksum string
	CompletedAt    time.Time
}

func (r *Runner) Apply(ctx context.Context) (report Report, resultErr error) {
	if r == nil || r.DB == nil {
		return Report{}, errors.New("runtime schema database is required")
	}
	if !schemaPattern.MatchString(r.Schema) {
		return Report{}, fmt.Errorf("invalid runtime schema %q", r.Schema)
	}
	if len(r.Migrations) == 0 {
		return Report{}, errors.New("runtime schema migrations are required")
	}

	sqlDB, err := r.DB.DB()
	if err != nil {
		return Report{}, fmt.Errorf("open runtime schema database: %w", err)
	}
	connection, err := sqlDB.Conn(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("reserve runtime schema connection: %w", err)
	}
	defer connection.Close()

	lockKey := migrationLockKey(r.Schema)
	if err = acquireMigrationLock(ctx, connection, lockKey); err != nil {
		return Report{}, fmt.Errorf("lock runtime schema migrations: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, unlockErr := connection.ExecContext(
			unlockCtx,
			`SELECT pg_advisory_unlock($1)`,
			lockKey,
		); unlockErr != nil && resultErr == nil {
			resultErr = fmt.Errorf("unlock runtime schema migrations: %w", unlockErr)
		}
	}()

	if _, err = connection.ExecContext(
		ctx,
		`CREATE SCHEMA IF NOT EXISTS `+quoteIdentifier(r.Schema),
	); err != nil {
		return Report{}, fmt.Errorf("create runtime schema: %w", err)
	}
	if err = ensureLedger(ctx, connection, r.Schema); err != nil {
		return Report{}, err
	}

	report.Applied = make([]string, 0, len(r.Migrations))
	report.Skipped = make([]string, 0, len(r.Migrations))
	for _, migration := range r.Migrations {
		if err = validateMigration(migration); err != nil {
			return Report{}, err
		}
		checksum := migrationChecksum(migration)
		storedChecksum, found, lookupErr := lookupMigration(
			ctx,
			connection,
			r.Schema,
			migration.Version,
		)
		if lookupErr != nil {
			return Report{}, lookupErr
		}
		if found {
			if storedChecksum != checksum {
				return Report{}, fmt.Errorf(
					"runtime migration %s checksum mismatch: stored=%s current=%s",
					migration.Version,
					storedChecksum,
					checksum,
				)
			}
			report.Skipped = append(report.Skipped, migration.Version)
			report.LatestVersion = migration.Version
			report.LatestChecksum = checksum
			continue
		}
		rendered := renderMigrationSQL(migration.SQL, r.Schema)
		if migration.NonTransactional {
			if migration.Before != nil {
				return Report{}, fmt.Errorf(
					"runtime migration %s has a hook but is non-transactional",
					migration.Version,
				)
			}
			if err = executeStatements(ctx, connection, rendered); err != nil {
				return Report{}, fmt.Errorf(
					"apply runtime migration %s: %w",
					migration.Version,
					err,
				)
			}
			if err = insertMigration(
				ctx,
				connection,
				r.Schema,
				migration.Version,
				checksum,
				r.Revision,
			); err != nil {
				return Report{}, err
			}
		} else {
			if err = applyTransactionalMigration(
				ctx,
				connection,
				r.Schema,
				r.Revision,
				migration,
				rendered,
				checksum,
			); err != nil {
				return Report{}, err
			}
		}
		report.Applied = append(report.Applied, migration.Version)
		report.LatestVersion = migration.Version
		report.LatestChecksum = checksum
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func acquireMigrationLock(
	ctx context.Context,
	connection *sql.Conn,
	lockKey int64,
) error {
	const retryInterval = 50 * time.Millisecond
	for {
		var acquired bool
		if err := connection.QueryRowContext(
			ctx,
			`SELECT pg_try_advisory_lock($1)`,
			lockKey,
		).Scan(&acquired); err != nil {
			return err
		}
		if acquired {
			return nil
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func validateMigration(migration Migration) error {
	if strings.TrimSpace(migration.Version) == "" ||
		migration.Version != strings.TrimSpace(migration.Version) {
		return errors.New("runtime migration version is not canonical")
	}
	if strings.TrimSpace(migration.SQL) == "" {
		return fmt.Errorf("runtime migration %s has empty SQL", migration.Version)
	}
	current := migrationChecksum(migration)
	if migration.Checksum != "" && migration.Checksum != current {
		return fmt.Errorf(
			"runtime migration %s declared checksum does not match its content",
			migration.Version,
		)
	}
	return nil
}

func ensureLedger(
	ctx context.Context,
	connection *sql.Conn,
	schema string,
) error {
	statement := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.runtime_schema_migrations (
			version text PRIMARY KEY,
			checksum text NOT NULL,
			binary_revision text NOT NULL DEFAULT '',
			applied_at timestamptz NOT NULL DEFAULT now()
		)`,
		quoteIdentifier(schema),
	)
	if _, err := connection.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("create runtime migration ledger: %w", err)
	}
	return nil
}

func lookupMigration(
	ctx context.Context,
	connection *sql.Conn,
	schema string,
	version string,
) (string, bool, error) {
	statement := fmt.Sprintf(
		`SELECT checksum FROM %s.runtime_schema_migrations WHERE version = $1`,
		quoteIdentifier(schema),
	)
	var checksum string
	err := connection.QueryRowContext(ctx, statement, version).Scan(&checksum)
	switch {
	case err == nil:
		return checksum, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	default:
		return "", false, fmt.Errorf(
			"read runtime migration %s: %w",
			version,
			err,
		)
	}
}

func applyTransactionalMigration(
	ctx context.Context,
	connection *sql.Conn,
	schema string,
	revision string,
	migration Migration,
	rendered string,
	checksum string,
) error {
	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime migration %s: %w", migration.Version, err)
	}
	defer transaction.Rollback()
	if migration.Before != nil {
		if err = migration.Before(ctx, transaction, schema); err != nil {
			return fmt.Errorf(
				"prepare runtime migration %s: %w",
				migration.Version,
				err,
			)
		}
	}
	if err = executeStatements(ctx, transaction, rendered); err != nil {
		return fmt.Errorf("apply runtime migration %s: %w", migration.Version, err)
	}
	if err = insertMigration(
		ctx,
		transaction,
		schema,
		migration.Version,
		checksum,
		revision,
	); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit runtime migration %s: %w", migration.Version, err)
	}
	return nil
}

type statementExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func executeStatements(
	ctx context.Context,
	executor statementExecutor,
	document string,
) error {
	for index, statement := range splitSQLStatements(document) {
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("statement %d: %w", index+1, err)
		}
	}
	return nil
}

func insertMigration(
	ctx context.Context,
	executor statementExecutor,
	schema string,
	version string,
	checksum string,
	revision string,
) error {
	statement := fmt.Sprintf(`
		INSERT INTO %s.runtime_schema_migrations (
			version, checksum, binary_revision
		) VALUES ($1, $2, $3)`,
		quoteIdentifier(schema),
	)
	if _, err := executor.ExecContext(
		ctx,
		statement,
		version,
		checksum,
		strings.TrimSpace(revision),
	); err != nil {
		return fmt.Errorf("record runtime migration %s: %w", version, err)
	}
	return nil
}

func renderMigrationSQL(document, schema string) string {
	qualified := quoteIdentifier(schema) + "."
	rendered := strings.ReplaceAll(document, "betago.", qualified)
	rendered = strings.ReplaceAll(
		rendered,
		"schema if not exists betago",
		"schema if not exists "+quoteIdentifier(schema),
	)
	return rendered
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func migrationLockKey(schema string) int64 {
	sum := sha256.Sum256([]byte("betago-runtime-schema\x00" + schema))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func splitSQLStatements(document string) []string {
	statements := make([]string, 0, 16)
	start := 0
	var singleQuoted, doubleQuoted, lineComment, blockComment bool
	var dollarTag string

	for index := 0; index < len(document); index++ {
		current := document[index]
		next := byte(0)
		if index+1 < len(document) {
			next = document[index+1]
		}
		switch {
		case lineComment:
			if current == '\n' {
				lineComment = false
			}
			continue
		case blockComment:
			if current == '*' && next == '/' {
				blockComment = false
				index++
			}
			continue
		case dollarTag != "":
			if strings.HasPrefix(document[index:], dollarTag) {
				index += len(dollarTag) - 1
				dollarTag = ""
			}
			continue
		case singleQuoted:
			if current == '\'' {
				if next == '\'' {
					index++
				} else {
					singleQuoted = false
				}
			}
			continue
		case doubleQuoted:
			if current == '"' {
				if next == '"' {
					index++
				} else {
					doubleQuoted = false
				}
			}
			continue
		}

		switch {
		case current == '-' && next == '-':
			lineComment = true
			index++
		case current == '/' && next == '*':
			blockComment = true
			index++
		case current == '\'':
			singleQuoted = true
		case current == '"':
			doubleQuoted = true
		case current == '$':
			if tag := readDollarTag(document[index:]); tag != "" {
				dollarTag = tag
				index += len(tag) - 1
			}
		case current == ';':
			if statement := strings.TrimSpace(document[start:index]); statement != "" {
				statements = append(statements, statement)
			}
			start = index + 1
		}
	}
	if statement := strings.TrimSpace(document[start:]); statement != "" {
		statements = append(statements, statement)
	}
	return statements
}

func readDollarTag(document string) string {
	if len(document) < 2 || document[0] != '$' {
		return ""
	}
	for index := 1; index < len(document); index++ {
		switch character := document[index]; {
		case character == '$':
			return document[:index+1]
		case character == '_' ||
			character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9':
			continue
		default:
			return ""
		}
	}
	return ""
}
