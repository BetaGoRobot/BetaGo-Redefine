package memestore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"sync"
	"time"
)

// QuotaGate separates shared request start spacing from in-flight concurrency.
// Only the digest of the provider quota key is stored; it is not a credential.
type QuotaGate struct {
	store       *Store
	key         string
	interval    time.Duration
	concurrency int
}

func (s *Store) Gate(key string, interval time.Duration, concurrency int) (*QuotaGate, error) {
	if key == "" || interval < 0 || interval > time.Minute || concurrency < 1 || concurrency > 64 {
		return nil, ErrInvalid
	}
	sum := sha256.Sum256([]byte(key))
	return &QuotaGate{s, hex.EncodeToString(sum[:]), interval, concurrency}, nil
}
func (s *Store) migrateQuota(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS ` + s.table("meme_remote_quota") + ` (quota_key text PRIMARY KEY,next_start timestamptz NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("meme_remote_leases") + ` (quota_key text NOT NULL,id text NOT NULL,expires_at timestamptz NOT NULL,PRIMARY KEY(quota_key,id))`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
func (g *QuotaGate) Acquire(ctx context.Context, route string) (func(), error) {
	if route == "" {
		return nil, ErrInvalid
	}
	key := g.key + ":" + route
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := uniqueID()
		if err != nil {
			return nil, err
		}
		ready := false
		wait := 20 * time.Millisecond
		err = g.attempt(ctx, key, id, &ready, &wait)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if ready {
			var once sync.Once
			return func() {
				once.Do(func() {
					cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
					defer cancel()
					g.store.db.ExecContext(cleanup, `DELETE FROM `+g.store.table("meme_remote_leases")+` WHERE quota_key=$1 AND id=$2`, key, id)
				})
			}, nil
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if wait > 100*time.Millisecond {
			wait = 100 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func (g *QuotaGate) attempt(ctx context.Context, key, id string, ready *bool, wait *time.Duration) error {
	tx, err := g.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO `+g.store.table("meme_remote_quota")+`(quota_key,next_start) VALUES ($1,'epoch') ON CONFLICT DO NOTHING`, key); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM `+g.store.table("meme_remote_leases")+` WHERE quota_key=$1 AND expires_at<=clock_timestamp()`, key); err != nil {
		return err
	}
	var now, next time.Time
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp(),next_start,(SELECT count(*) FROM `+g.store.table("meme_remote_leases")+` WHERE quota_key=$1) FROM `+g.store.table("meme_remote_quota")+` WHERE quota_key=$1 FOR UPDATE`, key).Scan(&now, &next, &count); err != nil {
		return err
	}
	if next.After(now) {
		*wait = next.Sub(now)
	}
	if count < g.concurrency && !next.After(now) {
		// A crashed process cannot permanently occupy a permit. Live callers must
		// have bounded HTTP timeouts shorter than this recovery lease.
		expires := now.Add(10 * time.Minute)
		if deadline, ok := ctx.Deadline(); ok && deadline.Before(expires) {
			expires = deadline
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO `+g.store.table("meme_remote_leases")+`(quota_key,id,expires_at) VALUES ($1,$2,$3)`, key, id, expires); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE `+g.store.table("meme_remote_quota")+` SET next_start=$2 WHERE quota_key=$1`, key, now.Add(g.interval)); err != nil {
			return err
		}
		*ready = true
	}
	return tx.Commit()
}
