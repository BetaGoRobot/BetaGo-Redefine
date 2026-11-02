package pgstore

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// ListDiscussions returns only current contexts whose complete source set still
// matches the authoritative originals. No old OpenSearch discussion is read.
func (s *Store) ListDiscussions(ctx context.Context, scope domain.Scope, anchor time.Time, after string, limit int) ([]domain.Context, string, bool, error) {
	if scope.Validate() != nil || anchor.IsZero() || limit < 1 || limit > 20 || len(after) > 1024 {
		return nil, after, false, domain.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body,invalidated,invalidated_at FROM `+s.table("contexts")+` WHERE scope_key=$1 AND NOT invalidated AND context_id>$2 ORDER BY context_id LIMIT $3`, scope.Key(), after, limit+1)
	if err != nil {
		return nil, after, false, err
	}
	var candidates []domain.Context
	for rows.Next() {
		c, e := scanContext(rows)
		if e != nil {
			rows.Close()
			return nil, after, false, e
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, after, false, err
	}
	more := len(candidates) > limit
	if more {
		candidates = candidates[:limit]
	}
	out := []domain.Context{}
	next := after
	for _, c := range candidates {
		next = c.ID
		if c.Scope != scope || c.Invalidated || c.UpdatedAt.After(anchor) {
			continue
		}
		valid, e := s.ValidSources(ctx, domain.Query{Scope: scope, Anchor: anchor, Mode: domain.QueryDiscussion, Limit: 1}, c.Sources)
		if e != nil {
			return nil, after, false, e
		}
		if valid {
			out = append(out, c)
		}
	}
	return out, next, more, nil
}
