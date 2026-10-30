package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// PutContext persists all dependencies under the same source locks as Accept.
// A concurrent edit either precedes this validation or invalidates the committed
// context. It can never leave an active context with silently stale sources.
func (s *Store) PutContext(ctx context.Context, input domain.Context) error {
	if err := input.Validate(); err != nil {
		return err
	}
	c := input
	c.Sources = append([]domain.SourceRef(nil), input.Sources...)
	c.Invalidated = false
	c.InvalidatedAt = nil
	body, err := encode(c)
	if err != nil {
		return err
	}
	digest := contentDigest(body, nil)
	return s.transact(ctx, func(tx *sql.Tx) error {
		ids := make([]string, 0, len(c.Sources))
		for _, ref := range c.Sources {
			ids = append(ids, ref.MessageID)
		}
		sort.Strings(ids)
		previous := ""
		for _, id := range ids {
			if id != previous {
				if err := lockSource(ctx, tx, c.Scope, id); err != nil {
					return err
				}
				previous = id
			}
		}
		q := domain.Query{Scope: c.Scope, Anchor: c.UpdatedAt, Mode: domain.QueryDiscussion, Limit: 1}
		for _, ref := range c.Sources {
			m, err := s.messageTx(ctx, tx, c.Scope, ref.MessageID)
			if errors.Is(err, domain.ErrNotFound) {
				return domain.ErrStaleSource
			}
			if err != nil {
				return err
			}
			if !sourceMatches(q, ref, m) {
				return domain.ErrStaleSource
			}
		}
		// Serialize absent context creation independently of overlapping source sets.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, c.Scope.Key()+":context:"+c.ID); err != nil {
			return err
		}
		var version int64
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT version,payload_digest FROM `+s.table("contexts")+` WHERE scope_key=$1 AND context_id=$2`, c.Scope.Key(), c.ID).Scan(&version, &existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if version == c.Version && existing == digest {
				return nil
			}
			if version >= c.Version {
				return fmt.Errorf("%w: context version", domain.ErrConflict)
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO `+s.table("contexts")+`(scope_key,context_id,version,body,payload_digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT(scope_key,context_id) DO UPDATE SET version=excluded.version,body=excluded.body,payload_digest=excluded.payload_digest,invalidated=false,invalidated_at=NULL`, c.Scope.Key(), c.ID, c.Version, body, digest)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+s.table("context_sources")+` WHERE scope_key=$1 AND context_id=$2`, c.Scope.Key(), c.ID); err != nil {
			return err
		}
		for _, ref := range c.Sources {
			source, _ := json.Marshal(ref)
			if _, err = tx.ExecContext(ctx, `INSERT INTO `+s.table("context_sources")+`(scope_key,context_id,message_id,source_version,source_body) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, c.Scope.Key(), c.ID, ref.MessageID, ref.Version, source); err != nil {
				return err
			}
		}
		return nil
	})
}

func scanContext(row scanner) (domain.Context, error) {
	var c domain.Context
	var body []byte
	var invalidAt sql.NullTime
	if err := row.Scan(&body, &c.Invalidated, &invalidAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c, domain.ErrNotFound
		}
		return c, err
	}
	invalidated := c.Invalidated
	if err := json.Unmarshal(body, &c); err != nil {
		return c, err
	}
	c.Invalidated = invalidated
	if invalidAt.Valid {
		c.InvalidatedAt = &invalidAt.Time
	}
	return c, nil
}

func (s *Store) Context(ctx context.Context, scope domain.Scope, id string) (domain.Context, error) {
	if err := scope.Validate(); err != nil {
		return domain.Context{}, err
	}
	return scanContext(s.db.QueryRowContext(ctx, `SELECT body,invalidated,invalidated_at FROM `+s.table("contexts")+` WHERE scope_key=$1 AND context_id=$2`, scope.Key(), id))
}

func (s *Store) AffectedContexts(ctx context.Context, scope domain.Scope, messageID string) ([]domain.Context, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.body,c.invalidated,c.invalidated_at FROM `+s.table("contexts")+` c WHERE c.scope_key=$1 AND EXISTS(SELECT 1 FROM `+s.table("context_sources")+` r WHERE r.scope_key=c.scope_key AND r.context_id=c.context_id AND r.message_id=$2) ORDER BY c.context_id`, scope.Key(), messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Context
	for rows.Next() {
		c, err := scanContext(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) invalidateContextsTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, messageID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `UPDATE `+s.table("contexts")+` c SET invalidated=true,invalidated_at=clock_timestamp() WHERE c.scope_key=$1 AND EXISTS(SELECT 1 FROM `+s.table("context_sources")+` r WHERE r.scope_key=c.scope_key AND r.context_id=c.context_id AND r.message_id=$2) RETURNING c.context_id`, scope.Key(), messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, rows.Err()
}
