package pgstore

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"time"
)

// RenewJob does not mutate the caller's Job. Heartbeats may safely use their own
// immutable claim while publication keeps the original identity and fence.
func (s *Store) RenewJob(ctx context.Context, job domain.Job, lease time.Duration) (domain.Job, error) {
	if err := job.Scope.Validate(); err != nil {
		return domain.Job{}, err
	}
	if lease <= 0 || lease > 24*time.Hour {
		return domain.Job{}, fmt.Errorf("%w: renewal lease", domain.ErrInvalid)
	}
	var out domain.Job
	err := s.transact(ctx, func(tx *sql.Tx) error {
		if err := s.lockJob(ctx, tx, job); err != nil {
			return err
		}
		var err error
		out, err = scanJob(tx.QueryRowContext(ctx, `UPDATE `+s.table("jobs")+` SET lease_until=LEAST(clock_timestamp()+$5::double precision*interval '1 second',expires_at),updated_at=clock_timestamp() WHERE id=$1 AND scope_key=$2 AND owner=$3 AND fence=$4 AND status='running' AND lease_until>clock_timestamp() AND expires_at>clock_timestamp() RETURNING `+jobColumns, job.ID, job.Scope.Key(), job.Owner, job.Fence, lease.Seconds()))
		if err == domain.ErrNotFound {
			return domain.ErrStaleClaim
		}
		return err
	})
	return out, err
}
