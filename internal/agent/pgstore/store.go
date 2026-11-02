// Package pgstore owns the new group agent's PostgreSQL schema. It has no
// knowledge of the old message pipeline or application configuration.
package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type Store struct {
	db     *sql.DB
	schema string
}

var schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func New(db *sql.DB, schema string) (*Store, error) {
	if schema == "" {
		schema = "group_agent"
	}
	if db == nil || !schemaName.MatchString(schema) {
		return nil, fmt.Errorf("%w: PostgreSQL handle or schema identifier", domain.ErrInvalid)
	}
	return &Store{db: db, schema: schema}, nil
}

func (s *Store) table(name string) string { return `"` + s.schema + `"."` + name + `"` }

type AcceptResult struct {
	Duplicate, Current bool
	JobIDs             []int64
}

func lockSource(ctx context.Context, tx *sql.Tx, scope domain.Scope, id string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope.Key()+":"+id)
	return err
}

func encode(v any) ([]byte, error) { return json.Marshal(v) }

func decodeMessage(body, raw []byte) (domain.Message, error) {
	var m domain.Message
	if err := json.Unmarshal(body, &m); err != nil {
		return m, err
	}
	m.Raw = append(json.RawMessage(nil), raw...)
	return m, nil
}

func (s *Store) transact(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
