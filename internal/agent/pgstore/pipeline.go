package pgstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"sort"
	"time"
)

func (s *Store) migratePipeline(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{`CREATE TABLE IF NOT EXISTS ` + s.table("pipeline_cleanups") + `(scope_key text NOT NULL,job_id bigint NOT NULL,context_id text NOT NULL,channel text NOT NULL,completed_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(scope_key,job_id,context_id,channel))`, `CREATE TABLE IF NOT EXISTS ` + s.table("media_artifacts") + `(scope_key text NOT NULL,message_id text NOT NULL,source_version bigint NOT NULL,body bytea NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(scope_key,message_id,source_version))`, `CREATE TABLE IF NOT EXISTS ` + s.table("pipeline_stages") + `(scope_key text NOT NULL,message_id text NOT NULL,source_version bigint NOT NULL,name text NOT NULL,body bytea NOT NULL,PRIMARY KEY(scope_key,message_id,source_version,name))`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) checkClaimTx(ctx context.Context, tx *sql.Tx, j domain.Job) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT owner=$3 AND fence=$4 AND status='running' AND lease_until>clock_timestamp() AND expires_at>clock_timestamp() FROM `+s.table("jobs")+` WHERE id=$1 AND scope_key=$2`, j.ID, j.Scope.Key(), j.Owner, j.Fence).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !valid {
		return domain.ErrStaleClaim
	}
	return err
}
func (s *Store) CheckClaim(ctx context.Context, j domain.Job) error {
	return s.transact(ctx, func(tx *sql.Tx) error { return s.checkClaimTx(ctx, tx, j) })
}
func (s *Store) checkSourcesTx(ctx context.Context, tx *sql.Tx, refs []domain.SourceRef) error {
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return err
		}
		m, err := s.messageTx(ctx, tx, ref.Scope, ref.MessageID)
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrStaleSource
		}
		if err != nil {
			return err
		}
		if m.Deleted || m.Source != ref {
			return domain.ErrStaleSource
		}
	}
	return nil
}
func (s *Store) lockSourcesTx(ctx context.Context, tx *sql.Tx, refs []domain.SourceRef) error {
	keys := append([]domain.SourceRef(nil), refs...)
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Scope.Key()+keys[i].MessageID < keys[j].Scope.Key()+keys[j].MessageID
	})
	for _, ref := range keys {
		if err := lockSource(ctx, tx, ref.Scope, ref.MessageID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) PutMedia(ctx context.Context, j domain.Job, a domain.MediaArtifact) error {
	if a.Source != j.Source || j.Kind != domain.JobMedia || len(a.Parts) > 2048 {
		return domain.ErrInvalid
	}
	size := 0
	for _, p := range a.Parts {
		size += len(p.Text) + len(p.Object.Key)
		if size > 8<<20 {
			return domain.ErrInvalid
		}
	}
	body, err := encode(a)
	if err != nil || len(body) > 16<<20 {
		return domain.ErrInvalid
	}
	return s.transact(ctx, func(tx *sql.Tx) error {
		if err := s.lockJob(ctx, tx, j); err != nil {
			return err
		}
		if err := s.checkClaimTx(ctx, tx, j); err != nil {
			return err
		}
		if err := s.lockSourcesTx(ctx, tx, []domain.SourceRef{a.Source}); err != nil {
			return err
		}
		if err := s.checkSourcesTx(ctx, tx, []domain.SourceRef{a.Source}); err != nil {
			return err
		}
		// Source locks may have waited beyond either deadline. Recheck after all
		// blocking acquisition, retaining the job -> source lock order.
		if err := s.checkClaimTx(ctx, tx, j); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO `+s.table("media_artifacts")+`(scope_key,message_id,source_version,body) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, a.Source.Scope.Key(), a.Source.MessageID, a.Source.Version, body)
		if err != nil {
			return err
		}
		// A competing artifact row/DDL lock may also have delayed the INSERT.
		// Returning an expired claim here rolls back the staged write.
		return s.checkClaimTx(ctx, tx, j)
	})
}
func (s *Store) Media(ctx context.Context, ref domain.SourceRef) (domain.MediaArtifact, error) {
	var out domain.MediaArtifact
	if err := ref.Validate(); err != nil {
		return out, err
	}
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM `+s.table("media_artifacts")+` WHERE scope_key=$1 AND message_id=$2 AND source_version=$3`, ref.Scope.Key(), ref.MessageID, ref.Version).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(body, &out)
	return out, err
}
func (s *Store) Stage(ctx context.Context, ref domain.SourceRef, name string) (domain.PipelineStage, error) {
	var out domain.PipelineStage
	if err := ref.Validate(); err != nil {
		return out, err
	}
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM `+s.table("pipeline_stages")+` WHERE scope_key=$1 AND message_id=$2 AND source_version=$3 AND name=$4`, ref.Scope.Key(), ref.MessageID, ref.Version, name).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(body, &out)
	return out, err
}
func (s *Store) PutStage(ctx context.Context, j domain.Job, stage domain.PipelineStage, refs []domain.SourceRef) error {
	if stage.Source != j.Source || len(stage.Name) > 128 || stage.Name == "" || len(refs) < 1 || len(refs) > 256 {
		return domain.ErrInvalid
	}
	stage.CompletedAt = time.Now().UTC()
	body, err := encode(stage)
	if err != nil {
		return err
	}
	return s.transact(ctx, func(tx *sql.Tx) error {
		if err := s.lockJob(ctx, tx, j); err != nil {
			return err
		}
		if err := s.checkClaimTx(ctx, tx, j); err != nil {
			return err
		}
		if err := s.lockSourcesTx(ctx, tx, refs); err != nil {
			return err
		}
		if err := s.checkSourcesTx(ctx, tx, refs); err != nil {
			return err
		}
		if err := s.checkClaimTx(ctx, tx, j); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO `+s.table("pipeline_stages")+`(scope_key,message_id,source_version,name,body) VALUES($1,$2,$3,$4,$5) ON CONFLICT(scope_key,message_id,source_version,name) DO UPDATE SET body=excluded.body`, stage.Source.Scope.Key(), stage.Source.MessageID, stage.Source.Version, stage.Name, body)
		if err != nil {
			return err
		}
		return s.checkClaimTx(ctx, tx, j)
	})
}

// Publication serializes only external writers of one stable artifact. It uses
// a separate advisory-lock namespace from Accept; model I/O never locks raw facts.
func (s *Store) Publication(ctx context.Context, scope domain.Scope, key string, fn func(context.Context) error) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if key == "" || len(key) > 2048 || fn == nil {
		return domain.ErrInvalid
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	lock := "agent-external-publication:" + scope.Key() + ":" + key
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, lock); err != nil {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := conn.ExecContext(release, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lock); e != nil {
			conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return fn(ctx)
}

// DeferJob waits for durable prerequisites without spending execution attempts.
// Global expiry remains enforced, so unavailable prerequisites cannot live forever.
func (s *Store) DeferJob(ctx context.Context, j domain.Job, delay time.Duration) error {
	if delay <= 0 || delay > time.Hour {
		return fmt.Errorf("%w: readiness delay", domain.ErrInvalid)
	}
	return s.transact(ctx, func(tx *sql.Tx) error {
		if err := s.lockJob(ctx, tx, j); err != nil {
			return err
		}
		r, err := tx.ExecContext(ctx, `UPDATE `+s.table("jobs")+` SET status='pending',owner='',lease_until=NULL,attempt=GREATEST(0,attempt-1),available_at=LEAST(expires_at,clock_timestamp()+$5::double precision*interval '1 second'),updated_at=clock_timestamp() WHERE id=$1 AND scope_key=$2 AND owner=$3 AND fence=$4 AND status='running' AND lease_until>clock_timestamp() AND expires_at>clock_timestamp()`, j.ID, j.Scope.Key(), j.Owner, j.Fence, delay.Seconds())
		return claimResult(r, err)
	})
}

// Cleanup receipts are independent of source validity: deletion jobs must record
// successful external cleanup even when their source is a tombstone.
func (s *Store) CleanupDone(ctx context.Context, j domain.Job, id, channel string) (bool, error) {
	if err := j.Scope.Validate(); err != nil {
		return false, err
	}
	var done bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+s.table("pipeline_cleanups")+` WHERE scope_key=$1 AND job_id=$2 AND context_id=$3 AND channel=$4)`, j.Scope.Key(), j.ID, id, channel).Scan(&done)
	return done, err
}
func (s *Store) MarkCleanupDone(ctx context.Context, j domain.Job, id, channel string) error {
	if j.Kind != domain.JobCleanup || id == "" || len(id) > 1024 || (channel != "embed" && channel != "memory") {
		return domain.ErrInvalid
	}
	return s.transact(ctx, func(tx *sql.Tx) error {
		if err := s.lockJob(ctx, tx, j); err != nil {
			return err
		}
		if err := s.checkClaimTx(ctx, tx, j); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+s.table("pipeline_cleanups")+`(scope_key,job_id,context_id,channel) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, j.Scope.Key(), j.ID, id, channel); err != nil {
			return err
		}
		return s.checkClaimTx(ctx, tx, j)
	})
}
