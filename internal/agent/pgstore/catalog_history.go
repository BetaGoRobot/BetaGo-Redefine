package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type catalogQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) catalogHistorySinceFrom(ctx context.Context, q catalogQueryRower, scope domain.Scope) (time.Time, error) {
	if scope.Validate() != nil {
		return time.Time{}, domain.ErrInvalid
	}
	var body []byte
	err := q.QueryRowContext(ctx, `SELECT body FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 AND kind='group_config' AND name='history_since' AND NOT deleted`, scope.Key()).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var entry domain.CatalogEntry
	if err = json.Unmarshal(body, &entry); err != nil {
		return time.Time{}, err
	}
	if entry.Config == nil {
		return time.Time{}, domain.ErrInvalid
	}
	return time.Parse(time.RFC3339Nano, entry.Config.Value)
}
func (s *Store) CatalogHistorySince(ctx context.Context, scope domain.Scope) (time.Time, error) {
	return s.catalogHistorySinceFrom(ctx, s.db, scope)
}
func (s *Store) catalogHistorySinceTx(ctx context.Context, tx *sql.Tx, scope domain.Scope) (time.Time, error) {
	return s.catalogHistorySinceFrom(ctx, tx, scope)
}
