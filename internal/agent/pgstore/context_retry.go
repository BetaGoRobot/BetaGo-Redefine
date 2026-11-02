package pgstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// ReplanStaleContext discards an unsent read-only attempt whose incidental
// conversation changed. It does not rebase requests, approvals, effects, or a
// frozen delivery batch. Every new attempt reloads current scoped evidence.
func (s *Store) ReplanStaleContext(ctx context.Context, j domain.Job, r domain.TurnRequest) (bool, error) {
	if j.Kind != domain.JobAgentTurn || r.TaskID == "" || r.Revision < 1 || r.ApprovalID != "" {
		return false, nil
	}
	replanned := false
	err := s.transact(ctx, func(tx *sql.Tx) error {
		if err := s.lockJob(ctx, tx, j); err != nil {
			return err
		}
		if err := s.checkClaimTx(ctx, tx, j); err != nil {
			return err
		}
		t, err := s.taskTx(ctx, tx, j.Scope, r.TaskID, true)
		if err != nil {
			return err
		}
		if t.Scope != j.Scope || t.Root != j.Source || t.Revision != r.Revision || t.Status != "ready" || t.ContextReplans >= 2 || len(t.ContextSources) == 0 || t.FinalConversation != nil || t.ActiveJobID != 0 && t.ActiveJobID != j.ID {
			return nil
		}
		if err = s.checkTaskDeadlineTx(ctx, tx, t); err != nil {
			return err
		}
		refs := append(append([]domain.SourceRef(nil), t.Sources...), t.ContextSources...)
		for _, ref := range refs {
			if ref.Scope != t.Scope {
				return domain.ErrUnauthorized
			}
		}
		if err = s.lockSourcesTx(ctx, tx, refs); err != nil {
			return err
		}
		if err = s.checkSourcesTx(ctx, tx, t.Sources); err != nil {
			if errors.Is(err, domain.ErrStaleSource) {
				return nil
			}
			return err
		}
		if err = s.checkSourcesTx(ctx, tx, t.ContextSources); err == nil {
			return nil
		} else if !errors.Is(err, domain.ErrStaleSource) {
			return err
		}
		var blocked bool
		// Any approval history or frozen send is excluded, even if revoked or
		// not yet sent. Effects always bind an approval; read-only tool audits
		// may be retained and recomputed without replaying an external action.
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+s.table("approvals")+` WHERE scope_key=$1 AND task_id=$2) OR EXISTS(SELECT 1 FROM `+s.table("deliveries")+` WHERE scope_key=$1 AND task_id=$2) OR EXISTS(SELECT 1 FROM `+s.table("tool_executions")+` WHERE scope_key=$1 AND task_id=$2 AND (status IN ('running_effect','unknown') OR COALESCE(convert_from(body,'UTF8')::jsonb->>'ApprovalID','')<>''))`, t.Scope.Key(), t.ID).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked {
			return nil
		}
		if _, err = tx.ExecContext(ctx, `UPDATE `+s.table("turns")+` SET status='context_changed',updated_at=clock_timestamp() WHERE scope_key=$1 AND task_id=$2 AND revision=$3 AND job_id=$4 AND fence=$5`, t.Scope.Key(), t.ID, t.Revision, j.ID, j.Fence); err != nil {
			return err
		}
		t.ContextSources = nil
		t.ReplyTargets = nil
		t.FinalConversation = nil
		t.ActiveJobID = 0
		t.ContextReplans++
		t.Revision++
		if err = s.saveTaskTx(ctx, tx, t); err != nil {
			return err
		}
		if err = s.turnJobTx(ctx, tx, t, ""); err != nil {
			return err
		}
		if err = s.checkRuntimeClaimTx(ctx, tx, j, t); err != nil {
			return err
		}
		replanned = true
		return nil
	})
	return replanned, err
}
