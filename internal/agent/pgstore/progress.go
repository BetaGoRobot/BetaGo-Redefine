package pgstore

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// Progress projections deliberately omit potentially large external result and
// content bodies; the execution ledger retains those complete audit receipts.
type executionProgress struct {
	ExecutionKey, Tool, Status, ActorID string
	Revision                            int64
}
type deliveryProgress struct {
	ID, Kind, Status, MessageID string
	Revision                    int64
}
type turnProgress struct {
	JobID, Fence int64
	Status       string
	JobStatus    domain.JobStatus
	Attempt      int
}
type taskProgress struct {
	executions []executionProgress
	deliveries []deliveryProgress
	turns      []turnProgress
}

func (s *Store) taskProgress(ctx context.Context, scope domain.Scope, id string) (taskProgress, error) {
	var out taskProgress
	// Unresolved effects and active reads must remain visible when completed
	// read history exceeds the bounded projection. Complete receipts stay in PG.
	rows, e := s.db.QueryContext(ctx, `SELECT execution_key,convert_from(body,'UTF8')::jsonb->>'Tool',status,convert_from(body,'UTF8')::jsonb->>'ActorID',(convert_from(body,'UTF8')::jsonb->>'Revision')::bigint FROM `+s.table("tool_executions")+` WHERE scope_key=$1 AND task_id=$2 ORDER BY CASE WHEN status IN ('running_effect','unknown') THEN 0 WHEN status='running_read' THEN 1 ELSE 2 END,execution_key LIMIT 128`, scope.Key(), id)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var p executionProgress
		if e = rows.Scan(&p.ExecutionKey, &p.Tool, &p.Status, &p.ActorID, &p.Revision); e != nil {
			rows.Close()
			return out, e
		}
		out.executions = append(out.executions, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = s.db.QueryContext(ctx, `SELECT delivery_id,convert_from(body,'UTF8')::jsonb->>'Kind',status,message_id,revision FROM `+s.table("deliveries")+` WHERE scope_key=$1 AND task_id=$2 ORDER BY delivery_id LIMIT 128`, scope.Key(), id)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var p deliveryProgress
		if e = rows.Scan(&p.ID, &p.Kind, &p.Status, &p.MessageID, &p.Revision); e != nil {
			rows.Close()
			return out, e
		}
		out.deliveries = append(out.deliveries, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = s.db.QueryContext(ctx, `SELECT t.job_id,t.fence,t.status,j.status,j.attempt FROM `+s.table("turns")+` t JOIN `+s.table("jobs")+` j ON j.id=t.job_id AND j.scope_key=t.scope_key WHERE t.scope_key=$1 AND t.task_id=$2 ORDER BY t.started_at DESC LIMIT 64`, scope.Key(), id)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var p turnProgress
		if e = rows.Scan(&p.JobID, &p.Fence, &p.Status, &p.JobStatus, &p.Attempt); e != nil {
			return out, e
		}
		out.turns = append(out.turns, p)
	}
	return out, rows.Err()
}
