package musiccards

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// duringView bounds a finite queue job's client work by its persisted view and
// lease. It owns and joins the watcher; no scheduler or detached work survives
// the job. Short independent read deadlines also bound shutdown on a busy pool.
func (p *Service) duringView(ctx context.Context, j domain.Job, s session, work func(context.Context) error) error {
	workCtx, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				return
			case <-ticker.C:
				checkCtx, checkCancel := context.WithTimeout(workCtx, 200*time.Millisecond)
				var version, intent int64
				var state string
				var claim bool
				err := p.db.QueryRowContext(checkCtx, `SELECT s.version,s.state,s.intent_version,j.owner=$4 AND j.fence=$5 AND j.status='running' AND j.lease_until>clock_timestamp() AND j.expires_at>clock_timestamp() FROM `+p.table("music_card_sessions")+` s JOIN `+p.table("jobs")+` j ON j.id=$3 AND j.scope_key=s.scope_key WHERE s.id=$1 AND s.scope_key=$2`, s.ID, s.Identity.Scope.Key(), j.ID, j.Owner, j.Fence).Scan(&version, &state, &intent, &claim)
				checkCancel()
				if workCtx.Err() != nil {
					return
				}
				if errors.Is(err, sql.ErrNoRows) || err == nil && (version != s.Version || intent != s.IntentVersion || state != "active") {
					cancel(domain.ErrConflict)
					return
				}
				if err != nil {
					cancel(business.ErrProvider)
					return
				}
				if !claim {
					cancel(domain.ErrStaleClaim)
					return
				}
			}
		}
	}()
	err := work(workCtx)
	cancel(context.Canceled)
	<-done
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cause := context.Cause(workCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}
