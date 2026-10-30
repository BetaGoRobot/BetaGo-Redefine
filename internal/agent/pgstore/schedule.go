package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"time"
)

func (s *Store) MigrateSchedules(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS ` + s.table("schedules") + `(scope_key text NOT NULL,id text NOT NULL,creator text NOT NULL,status text NOT NULL,body bytea NOT NULL,updated_at timestamptz NOT NULL,PRIMARY KEY(scope_key,id))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("schedule_runs") + `(scope_key text NOT NULL,schedule_id text NOT NULL,revision bigint NOT NULL,run_index integer NOT NULL,status text NOT NULL,body bytea NOT NULL,PRIMARY KEY(scope_key,schedule_id,revision,run_index))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("schedule_mutations") + `(scope_key text NOT NULL,task_id text NOT NULL,execution_key text NOT NULL,body bytea NOT NULL,PRIMARY KEY(scope_key,task_id,execution_key))`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) scheduleTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, id string, lock bool) (out domain.Schedule, err error) {
	q := `SELECT body FROM ` + s.table("schedules") + ` WHERE scope_key=$1 AND id=$2`
	if lock {
		q += ` FOR UPDATE`
	}
	var b []byte
	err = tx.QueryRowContext(ctx, q, scope.Key(), id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(b, &out)
		if err == nil && out.Scope != scope {
			err = domain.ErrUnauthorized
		}
	}
	return
}
func (s *Store) saveScheduleTx(ctx context.Context, tx *sql.Tx, out domain.Schedule) error {
	b, _ := json.Marshal(out)
	_, err := tx.ExecContext(ctx, `UPDATE `+s.table("schedules")+` SET status=$3,body=$4,updated_at=$5 WHERE scope_key=$1 AND id=$2`, out.Scope.Key(), out.ID, out.Status, b, out.UpdatedAt)
	return err
}
func (s *Store) saveScheduleRunTx(ctx context.Context, tx *sql.Tx, out domain.ScheduleRun) error {
	b, _ := json.Marshal(out)
	_, err := tx.ExecContext(ctx, `UPDATE `+s.table("schedule_runs")+` SET status=$5,body=$6 WHERE scope_key=$1 AND schedule_id=$2 AND revision=$3 AND run_index=$4`, out.Scope.Key(), out.ScheduleID, out.Revision, out.Index, out.Status, b)
	return err
}
func (s *Store) queueScheduleTx(ctx context.Context, tx *sql.Tx, plan *domain.Schedule) error {
	if plan.Status != "active" || plan.NextIndex >= plan.Plan.Runs {
		return nil
	}
	due := plan.NextAt()
	run := domain.ScheduleRun{ScheduleID: plan.ID, Revision: plan.Revision, Index: plan.NextIndex, Scope: plan.Scope, Origin: plan.Origin, Sources: plan.Sources, Text: plan.Plan.Text, Query: plan.Plan.Query, QueryPolicyDigest: plan.QueryPolicyDigest, UUID: domain.StableUUID(plan.Scope.Key(), plan.ID, fmt.Sprint(plan.Revision), fmt.Sprint(plan.NextIndex)), Status: "pending", DueAt: due, NotifyUntil: due.Add(30 * time.Minute)}
	b, _ := json.Marshal(run)
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+s.table("schedule_runs")+`(scope_key,schedule_id,revision,run_index,status,body)VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, plan.Scope.Key(), plan.ID, plan.Revision, plan.NextIndex, run.Status, b); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"schedule_id": plan.ID, "revision": plan.Revision, "index": plan.NextIndex})
	// Keep an overdue tick claimable so its transaction can skip downtime and
	// enqueue a future slot. The run's own deadline bounds all actual sends.
	expires := plan.Plan.StartAt.Add(time.Duration(plan.Plan.Runs-1)*time.Duration(plan.Plan.IntervalSeconds)*time.Second + 24*time.Hour)
	_, err := s.enqueueTx(ctx, tx, domain.JobSpec{Kind: domain.JobSchedule, Scope: plan.Scope, Source: plan.Origin, Payload: payload, DedupeKey: run.UUID, AvailableAt: due, ExpiresAt: expires})
	return err
}
func (s *Store) CreateSchedule(ctx context.Context, j domain.Job, t domain.Task, key, digest string, in domain.ScheduleInput) (out domain.Schedule, err error) {
	if err = domain.ValidateScheduleInput(in, in.StartAt.Add(-time.Nanosecond)); err != nil {
		return
	}
	err = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, actor, e := s.workGuardTx(ctx, tx, j, t, key, digest, "schedule_create", in)
		if e != nil {
			return e
		}
		id := "schedule_" + domain.StableID(t.Scope.Key(), t.ID, key)[:24]
		out, e = s.scheduleTx(ctx, tx, t.Scope, id, true)
		if e == nil {
			return nil
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, t.Scope.Key()+":schedule-capacity"); e != nil {
			return e
		}
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+s.table("schedules")+` WHERE scope_key=$1 AND status IN ('active','paused')`, t.Scope.Key()).Scan(&count); e != nil {
			return e
		}
		if count >= 100 {
			return domain.ErrConflict
		}
		now := time.Now().UTC()
		if e = domain.ValidateScheduleInput(in, now); e != nil {
			return e
		}
		out = domain.Schedule{ID: id, CreatedBy: actor, Status: "active", Scope: t.Scope, Origin: workOrigin(current), Sources: workSources(current), Plan: in, Revision: 1, PolicyName: "schedule_create", PolicyDigest: digest, CreatedAt: now, UpdatedAt: now}
		if e = s.bindScheduleQueryPolicyTx(ctx, tx, &out); e != nil {
			return e
		}
		b, _ := json.Marshal(out)
		if _, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("schedules")+`(scope_key,id,creator,status,body,updated_at)VALUES($1,$2,$3,$4,$5,$6)`, out.Scope.Key(), out.ID, actor, out.Status, b, now); e != nil {
			return e
		}
		if e = s.queueScheduleTx(ctx, tx, &out); e != nil {
			return e
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, current)
	})
	return
}
func (s *Store) QuerySchedule(ctx context.Context, scope domain.Scope, id string) (out domain.Schedule, err error) {
	if scope.Validate() != nil || id == "" || len(id) > 128 {
		return out, domain.ErrInvalid
	}
	err = s.transact(ctx, func(tx *sql.Tx) error {
		var e error
		out, e = s.scheduleTx(ctx, tx, scope, id, false)
		if e == nil {
			out = effectiveSchedule(out)
		}
		return e
	})
	return
}
func (s *Store) ListSchedules(ctx context.Context, scope domain.Scope, status string, limit int) ([]domain.Schedule, error) {
	if scope.Validate() != nil || limit < 1 || limit > 50 || (status != "" && status != "active" && status != "paused" && status != "canceled" && status != "completed" && status != "invalidated") {
		return nil, domain.ErrInvalid
	}
	effectiveStatus := `CASE WHEN status='active' AND ((convert_from(body,'UTF8')::jsonb#>>'{Plan,start_at}')::timestamptz + ((convert_from(body,'UTF8')::jsonb#>>'{Plan,interval_seconds}')::double precision * ((convert_from(body,'UTF8')::jsonb#>>'{Plan,runs}')::double precision-1))*interval '1 second')<=clock_timestamp() THEN 'completed' ELSE status END`
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM `+s.table("schedules")+` WHERE scope_key=$1 AND ($2='' OR (`+effectiveStatus+`)=$2) ORDER BY updated_at DESC,id LIMIT $3`, scope.Key(), status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Schedule{}
	for rows.Next() {
		var b []byte
		var item domain.Schedule
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &item); err != nil {
			return nil, err
		}
		if item.Scope != scope {
			return nil, domain.ErrUnauthorized
		}
		out = append(out, effectiveSchedule(item))
	}
	return out, rows.Err()
}
func (s *Store) ScheduleRuns(ctx context.Context, scope domain.Scope, id string, limit int) ([]domain.ScheduleRun, error) {
	if scope.Validate() != nil || id == "" || len(id) > 128 || limit < 1 || limit > 50 {
		return nil, domain.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM `+s.table("schedule_runs")+` WHERE scope_key=$1 AND schedule_id=$2 ORDER BY revision DESC,run_index LIMIT $3`, scope.Key(), id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ScheduleRun{}
	for rows.Next() {
		var b []byte
		var item domain.ScheduleRun
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &item); err != nil {
			return nil, err
		}
		if item.Scope != scope {
			return nil, domain.ErrUnauthorized
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) UpdateSchedule(ctx context.Context, j domain.Job, t domain.Task, key, digest string, in domain.ScheduleUpdateInput) (out domain.Schedule, err error) {
	if in.ID == "" || len(in.ID) > 128 || (in.Action != "pause" && in.Action != "resume" && in.Action != "cancel" && in.Action != "edit") {
		return out, domain.ErrInvalid
	}
	changes := in.Text != nil || in.Query != nil || in.StartAt != nil || in.IntervalSeconds != nil || in.Runs != nil
	if changes != (in.Action == "edit") || (in.Text != nil && in.Query != nil) {
		return out, domain.ErrInvalid
	}
	err = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, actor, e := s.workGuardTx(ctx, tx, j, t, key, digest, "schedule_update", in)
		if e != nil {
			return e
		}
		out, e = s.scheduleTx(ctx, tx, t.Scope, in.ID, true)
		if e != nil {
			return e
		}
		if out.CreatedBy != actor {
			return domain.ErrUnauthorized
		}
		var replay []byte
		e = tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("schedule_mutations")+` WHERE scope_key=$1 AND task_id=$2 AND execution_key=$3`, t.Scope.Key(), t.ID, key).Scan(&replay)
		if e == nil {
			return json.Unmarshal(replay, &out)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if out.Status == "canceled" || (out.Status == "completed" && in.Action != "edit") {
			if in.Action != "cancel" {
				return domain.ErrConflict
			}
		}
		now := time.Now().UTC()
		out.Revision++
		out.UpdatedAt = now
		if e = s.cancelScheduleRunsTx(ctx, tx, t.Scope, out.ID); e != nil {
			return e
		}
		switch in.Action {
		case "pause":
			out.Status = "paused"
		case "cancel":
			out.Status = "canceled"
		case "resume":
			if out.Status != "paused" && out.Status != "invalidated" {
				return domain.ErrConflict
			}
			out.Status = "active"
			out.NextIndex = domain.NextScheduleIndex(out.Plan, now, out.NextIndex)
		case "edit":
			if in.Text != nil {
				out.Plan.Text = *in.Text
				out.Plan.Query = nil
			}
			if in.Query != nil {
				out.Plan.Query = in.Query
				out.Plan.Text = ""
			}
			if in.StartAt != nil {
				out.Plan.StartAt = *in.StartAt
			}
			if in.IntervalSeconds != nil {
				out.Plan.IntervalSeconds = *in.IntervalSeconds
			}
			if in.Runs != nil {
				out.Plan.Runs = *in.Runs
			}
			if in.StartAt != nil && !in.StartAt.After(now) {
				return domain.ErrInvalid
			}
			if e = domain.ValidateScheduleInput(out.Plan, out.Plan.StartAt.Add(-time.Nanosecond)); e != nil {
				return e
			}
			end := out.Plan.StartAt.Add(time.Duration(out.Plan.Runs-1) * time.Duration(out.Plan.IntervalSeconds) * time.Second)
			if end.After(now.Add(366 * 24 * time.Hour)) {
				return domain.ErrInvalid
			}
			out.NextIndex = domain.NextScheduleIndex(out.Plan, now, 0)
		}
		if in.Action == "resume" || in.Action == "edit" {
			out.Origin = workOrigin(current)
			out.Sources = workSources(current)
			out.PolicyName = "schedule_update"
			out.PolicyDigest = digest
			if e = s.bindScheduleQueryPolicyTx(ctx, tx, &out); e != nil {
				return e
			}
		}
		if out.NextIndex >= out.Plan.Runs && out.Status == "active" {
			out.Status = "completed"
		}
		if e = s.saveScheduleTx(ctx, tx, out); e != nil {
			return e
		}
		if e = s.queueScheduleTx(ctx, tx, &out); e != nil {
			return e
		}
		b, _ := json.Marshal(out)
		if _, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("schedule_mutations")+`(scope_key,task_id,execution_key,body)VALUES($1,$2,$3,$4)`, t.Scope.Key(), t.ID, key, b); e != nil {
			return e
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, current)
	})
	return
}
func (s *Store) cancelScheduleRunsTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, id string) error {
	rows, err := tx.QueryContext(ctx, `SELECT body FROM `+s.table("schedule_runs")+` WHERE scope_key=$1 AND schedule_id=$2 AND status IN ('pending','sending','rendering') FOR UPDATE`, scope.Key(), id)
	if err != nil {
		return err
	}
	runs := []domain.ScheduleRun{}
	for rows.Next() {
		var b []byte
		var run domain.ScheduleRun
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(b, &run); err != nil {
			rows.Close()
			return err
		}
		run.Status = "canceled"
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, run := range runs {
		if err = s.saveScheduleRunTx(ctx, tx, run); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) bindScheduleQueryPolicyTx(ctx context.Context, tx *sql.Tx, plan *domain.Schedule) error {
	plan.QueryPolicyDigest = ""
	if plan.Plan.Query == nil {
		return nil
	}
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT policy_digest,enabled AND NOT side_effect FROM `+s.table("tool_policies")+` WHERE tool_name=$1 FOR SHARE`, plan.Plan.Query.Name).Scan(&plan.QueryPolicyDigest, &enabled)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !enabled {
		return domain.ErrUnauthorized
	}
	return err
}

func effectiveSchedule(out domain.Schedule) domain.Schedule {
	if out.Status == "active" && domain.NextScheduleIndex(out.Plan, time.Now(), out.NextIndex) >= out.Plan.Runs {
		out.Status = "completed"
		out.NextIndex = out.Plan.Runs
	}
	return out
}
