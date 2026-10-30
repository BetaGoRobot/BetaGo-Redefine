package pgstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// CheckpointStore is a task/revision/claim-bound implementation of Eino's real
// checkpoint interface. Native bridge checkpoint keys stay in this namespace.
type CheckpointStore struct {
	store *Store
	job   domain.Job
	task  domain.Task
}

func (s *Store) Checkpoints(j domain.Job, t domain.Task) *CheckpointStore {
	return &CheckpointStore{s, j, t}
}
func (c *CheckpointStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var b []byte
	found := true
	e := c.store.runtimeTransact(ctx, c.job, c.task, func(tx *sql.Tx) error {
		if _, e := c.store.guardTaskTx(ctx, tx, c.job, c.task); e != nil {
			return e
		}
		e := tx.QueryRowContext(ctx, `SELECT body FROM `+c.store.table("checkpoints")+` WHERE scope_key=$1 AND task_id=$2 AND revision=$3 AND checkpoint_key=$4`, c.task.Scope.Key(), c.task.ID, c.task.Revision, key).Scan(&b)
		if errors.Is(e, sql.ErrNoRows) {
			found = false
			return nil
		}
		return e
	})
	return b, found, e
}
func (c *CheckpointStore) Set(ctx context.Context, key string, b []byte) error {
	if len(b) > 8<<20 || len(key) > 4096 {
		return domain.ErrInvalid
	}
	return c.store.runtimeTransact(ctx, c.job, c.task, func(tx *sql.Tx) error {
		if _, e := c.store.guardTaskTx(ctx, tx, c.job, c.task); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO `+c.store.table("checkpoints")+`(scope_key,task_id,revision,checkpoint_key,body)VALUES($1,$2,$3,$4,$5)ON CONFLICT(scope_key,task_id,revision,checkpoint_key)DO UPDATE SET body=EXCLUDED.body,updated_at=clock_timestamp()`, c.task.Scope.Key(), c.task.ID, c.task.Revision, key, b)
		return e
	})
}
