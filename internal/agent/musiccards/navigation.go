package musiccards

import (
	"context"
	"database/sql"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// SDK callbacks reserve an intent, not a public view. Current membership and
// the target presentation grant are checked before the exact event can advance
// that view. The event's durable admission records its version across retries.
func (p *Service) admitNavigation(ctx context.Context, j domain.Job, s session, c continuation) (session, continuation, error) {
	var admitted int64
	e := p.db.QueryRowContext(ctx, `SELECT admitted_version FROM `+p.table("music_card_events")+` WHERE scope_key=$1 AND session_id=$2 AND event_id=$3`, s.Identity.Scope.Key(), s.ID, c.Event).Scan(&admitted)
	if errors.Is(e, sql.ErrNoRows) {
		return s, c, domain.ErrUnauthorized
	}
	if e != nil {
		return s, c, e
	}
	if admitted > 0 {
		c.Version = admitted
		if s.Version != admitted || s.Pending != nil {
			return s, c, domain.ErrConflict
		}
	} else if !matchesIntent(s, c) {
		return s, c, domain.ErrConflict
	}
	if p.member == nil {
		if admitted == 0 {
			_ = p.clearIntent(ctx, j, s, c)
		}
		return s, c, domain.ErrUnauthorized
	}
	allowed, e := p.member(ctx, s.Identity.Scope, c.Actor)
	if e != nil {
		return s, c, business.ErrProvider
	}
	if !allowed {
		if admitted == 0 {
			if e = p.clearIntent(ctx, j, s, c); e != nil {
				return s, c, e
			}
		}
		return s, c, domain.ErrUnauthorized
	}
	if admitted > 0 {
		return s, c, nil
	}
	target := s
	target.Request = s.Pending.Request
	if e = p.admit(ctx, target); e != nil {
		if errors.Is(e, domain.ErrUnauthorized) {
			_ = p.clearIntent(ctx, j, s, c)
		}
		return s, c, e
	}
	e = p.jobTx(ctx, j, func(tx *sql.Tx) error {
		current, e := p.load(ctx, tx, s.ID, true)
		if e != nil {
			return e
		}
		if e = p.recheckClaimTx(ctx, tx, j); e != nil {
			return e
		}
		if !matchesIntent(current, c) {
			return domain.ErrConflict
		}
		current.Request = current.Pending.Request
		if e = p.presentationGrant(ctx, tx, current, true); e != nil {
			return e
		}
		current.Version++
		current.Pending = nil
		if e = p.save(ctx, tx, current); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE `+p.table("music_card_events")+` SET admitted_version=$4 WHERE scope_key=$1 AND session_id=$2 AND event_id=$3 AND admitted_version=0`, current.Identity.Scope.Key(), current.ID, c.Event, current.Version); e != nil {
			return e
		}
		s = current
		c.Version = current.Version
		return nil
	})
	return s, c, e
}
func matchesIntent(s session, c continuation) bool {
	return s.Pending != nil && s.Pending.Event == c.Event && s.Pending.Actor == c.Actor && s.Pending.Work == c.Work && s.Pending.BaseVersion == c.Version && s.Version == c.Version && s.Pending.Refresh == c.Refresh
}
func (p *Service) clearIntent(ctx context.Context, j domain.Job, s session, c continuation) error {
	return p.jobTx(ctx, j, func(tx *sql.Tx) error {
		current, e := p.load(ctx, tx, s.ID, true)
		if e != nil {
			return e
		}
		if e = p.recheckClaimTx(ctx, tx, j); e != nil {
			return e
		}
		if !matchesIntent(current, c) {
			return domain.ErrConflict
		}
		current.Pending = nil
		return p.save(ctx, tx, current)
	})
}
