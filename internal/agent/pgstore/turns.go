package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func (s *Store) StartTurn(ctx context.Context, j domain.Job, r domain.TurnRequest) (domain.Task, error) {
	var out domain.Task
	if j.Kind != domain.JobAgentTurn {
		return out, domain.ErrInvalid
	}
	e := s.transact(ctx, func(tx *sql.Tx) error {
		var e error
		expected, e := s.taskTx(ctx, tx, j.Scope, r.TaskID, false)
		if e != nil {
			return e
		}
		expected.Revision = r.Revision
		out, e = s.guardTaskTx(ctx, tx, j, expected)
		if e != nil {
			return e
		}
		if out.Status == "ready_delivery" {
			return nil
		}
		if out.ActiveJobID != 0 && out.ActiveJobID != j.ID {
			var active bool
			e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+s.table("jobs")+` WHERE id=$1 AND status='running' AND lease_until>clock_timestamp() AND expires_at>clock_timestamp())`, out.ActiveJobID).Scan(&active)
			if e != nil {
				return e
			}
			if active {
				return domain.ErrTaskBusy
			}
		}
		if r.ApprovalID != "" {
			a, e := s.approvalTx(ctx, tx, j.Scope, r.ApprovalID)
			if e != nil {
				return e
			}
			if a.TaskID != out.ID || a.Revision != out.Revision || (a.Status != "approved" && a.Status != "executed") || !out.Authorized(a.ActorID) {
				return domain.ErrUnauthorized
			}
			if a.Source.MessageID != "" {
				if e = s.lockSourcesTx(ctx, tx, []domain.SourceRef{a.Source}); e != nil {
					return e
				}
				if e = s.checkSourcesTx(ctx, tx, []domain.SourceRef{a.Source}); e != nil {
					return e
				}
			}
		}
		out.ActiveJobID = j.ID
		if e = s.saveTaskTx(ctx, tx, out); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("turns")+`(scope_key,task_id,revision,job_id,fence,status)VALUES($1,$2,$3,$4,$5,'running')ON CONFLICT DO NOTHING`, out.Scope.Key(), out.ID, out.Revision, j.ID, j.Fence)
		return e
	})
	return out, e
}
func (s *Store) SetTurnContext(ctx context.Context, j domain.Job, t domain.Task, refs []domain.SourceRef, replyTargets ...[]string) (domain.Task, error) {
	var out domain.Task
	e := s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		var e error
		out, e = s.guardTaskTx(ctx, tx, j, t)
		if e != nil {
			return e
		}
		for _, ref := range refs {
			if ref.Scope != t.Scope {
				return domain.ErrUnauthorized
			}
		}
		since, e := s.catalogHistorySinceTx(ctx, tx, t.Scope)
		if e != nil {
			return e
		}
		for _, ref := range refs {
			if ref.OccurredAt.Before(since) {
				return domain.ErrStaleSource
			}
		}
		if e = s.lockSourcesTx(ctx, tx, refs); e != nil {
			return e
		}
		if e = s.checkSourcesTx(ctx, tx, refs); e != nil {
			return e
		}
		out.ContextSources = append([]domain.SourceRef(nil), refs...)
		out.ReplyTargets = nil
		if len(replyTargets) > 1 {
			return domain.ErrInvalid
		}
		if len(replyTargets) == 1 {
			if len(replyTargets[0]) > 64 {
				return domain.ErrInvalid
			}
			seen := map[string]bool{}
			for _, id := range replyTargets[0] {
				if seen[id] {
					continue
				}
				seen[id] = true
				if e = s.validateSceneReplyTx(ctx, tx, out, id); e != nil {
					return e
				}
				out.ReplyTargets = append(out.ReplyTargets, id)
			}
		}
		return s.saveTaskTx(ctx, tx, out)
	})
	return out, e
}
func (s *Store) EndTurn(ctx context.Context, j domain.Job, t domain.Task, status string) error {
	return s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		out, e := s.guardTaskTx(ctx, tx, j, t)
		if e != nil {
			return e
		}
		if out.ActiveJobID == j.ID {
			out.ActiveJobID = 0
			if e = s.saveTaskTx(ctx, tx, out); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, `UPDATE `+s.table("turns")+` SET status=$6,updated_at=clock_timestamp() WHERE scope_key=$1 AND task_id=$2 AND revision=$3 AND job_id=$4 AND fence=$5`, t.Scope.Key(), t.ID, t.Revision, j.ID, j.Fence, status)
		return e
	})
}
func (s *Store) TaskFacts(ctx context.Context, scope domain.Scope, id string) (json.RawMessage, error) {
	t, e := s.Task(ctx, scope, id)
	if e != nil {
		return nil, e
	}
	as, e := s.Approvals(ctx, scope, id)
	if e != nil {
		return nil, e
	}
	progress, e := s.taskProgress(ctx, scope, id)
	if e != nil {
		return nil, e
	}
	b, e := json.Marshal(struct {
		Task       domain.Task
		Approvals  []domain.Approval
		Executions []executionProgress
		Deliveries []deliveryProgress
		Turns      []turnProgress
	}{t, as, progress.executions, progress.deliveries, progress.turns})
	return b, e
}

// SourceJobStatus exposes only the current source's durable prerequisite state.
func (s *Store) SourceJobStatus(ctx context.Context, ref domain.SourceRef, kind domain.JobKind) (domain.JobStatus, error) {
	if e := ref.Validate(); e != nil {
		return "", e
	}
	body, _ := encode(ref)
	var status domain.JobStatus
	e := s.db.QueryRowContext(ctx, `SELECT status FROM `+s.table("jobs")+` WHERE scope_key=$1 AND source_body=$2 AND kind=$3 ORDER BY id DESC LIMIT 1`, ref.Scope.Key(), body, kind).Scan(&status)
	if errors.Is(e, sql.ErrNoRows) {
		return status, domain.ErrNotFound
	}
	return status, e
}

// AddTurnSources records validated retrieval/media dependencies separately from
// material contributions. These references never grant task authority.
func (s *Store) AddTurnSources(ctx context.Context, j domain.Job, t domain.Task, refs []domain.SourceRef) error {
	return s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		out, e := s.guardTaskTx(ctx, tx, j, t)
		if e != nil {
			return e
		}
		for _, ref := range refs {
			if ref.Scope != t.Scope {
				return domain.ErrUnauthorized
			}
		}
		since, e := s.catalogHistorySinceTx(ctx, tx, t.Scope)
		if e != nil {
			return e
		}
		for _, ref := range refs {
			if ref.OccurredAt.Before(since) {
				return domain.ErrStaleSource
			}
		}
		if e = s.lockSourcesTx(ctx, tx, refs); e != nil {
			return e
		}
		if e = s.checkSourcesTx(ctx, tx, refs); e != nil {
			return e
		}
		for _, ref := range refs {
			found := false
			for _, old := range out.ContextSources {
				found = found || old == ref
			}
			if !found {
				out.ContextSources = append(out.ContextSources, ref)
			}
		}
		if len(out.ContextSources) > 256 {
			return domain.ErrInvalid
		}
		return s.saveTaskTx(ctx, tx, out)
	})
}
