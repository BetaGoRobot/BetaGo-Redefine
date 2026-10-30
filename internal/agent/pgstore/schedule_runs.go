package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"strings"
	"time"
)

type scheduleJobPayload struct {
	ID       string `json:"schedule_id"`
	Revision int64  `json:"revision"`
	Index    int    `json:"index"`
}

func schedulePayload(j domain.Job) (p scheduleJobPayload, err error) {
	if j.Kind != domain.JobSchedule || json.Unmarshal(j.Payload, &p) != nil || p.ID == "" || len(p.ID) > 128 || p.Revision < 1 || p.Index < 0 || p.Index > 365 {
		return p, domain.ErrInvalid
	}
	return
}
func (s *Store) scheduleRunTx(ctx context.Context, tx *sql.Tx, j domain.Job, p scheduleJobPayload, lock bool) (out domain.ScheduleRun, err error) {
	q := `SELECT body FROM ` + s.table("schedule_runs") + ` WHERE scope_key=$1 AND schedule_id=$2 AND revision=$3 AND run_index=$4`
	if lock {
		q += ` FOR UPDATE`
	}
	var b []byte
	err = tx.QueryRowContext(ctx, q, j.Scope.Key(), p.ID, p.Revision, p.Index).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(b, &out)
		if err == nil && (out.Scope != j.Scope || out.Origin != j.Source || out.ScheduleID != p.ID || out.Revision != p.Revision || out.Index != p.Index) {
			err = domain.ErrUnauthorized
		}
	}
	return
}
func (s *Store) verifyScheduleJobTx(ctx context.Context, tx *sql.Tx, j domain.Job) error {
	var kind string
	var source, payload []byte
	if err := tx.QueryRowContext(ctx, `SELECT kind,source_body,payload FROM `+s.table("jobs")+` WHERE id=$1 AND scope_key=$2`, j.ID, j.Scope.Key()).Scan(&kind, &source, &payload); err != nil {
		return err
	}
	var ref domain.SourceRef
	if kind != string(domain.JobSchedule) || json.Unmarshal(source, &ref) != nil || ref != j.Source || string(payload) != string(j.Payload) {
		return domain.ErrUnauthorized
	}
	return nil
}
func (s *Store) scheduleSourcesCurrentTx(ctx context.Context, tx *sql.Tx, run domain.ScheduleRun) error {
	if err := s.checkSourcesTx(ctx, tx, run.Sources); err != nil {
		return err
	}
	since, err := s.catalogHistorySinceTx(ctx, tx, run.Scope)
	if err != nil {
		return err
	}
	for _, ref := range run.Sources {
		if !since.IsZero() && ref.OccurredAt.Before(since) {
			return domain.ErrStaleSource
		}
	}
	return nil
}
func (s *Store) scheduleControlsTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, query *domain.ScheduleQuery) (allow bool, err error) {
	allow = true
	feature := ""
	if query != nil {
		feature = "public_data"
		if strings.HasPrefix(query.Name, "music_") {
			feature = "music"
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT body FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 AND NOT deleted AND ((kind='feature' AND name IN ('reminders','public_data','music')) OR (kind='group_config' AND name='mute_until')) FOR SHARE`, scope.Key())
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var entry domain.CatalogEntry
		if err = rows.Scan(&b); err != nil {
			return false, err
		}
		if err = json.Unmarshal(b, &entry); err != nil {
			return false, err
		}
		if entry.Kind == domain.CatalogFeature {
			if entry.Feature == nil {
				return false, domain.ErrInvalid
			}
			if (entry.Name == "reminders" || entry.Name == feature) && !entry.Feature.Enabled {
				allow = false
			}
		}
		if entry.Kind == domain.CatalogGroupConfig {
			if entry.Config == nil {
				return false, domain.ErrInvalid
			}
			until, e := time.Parse(time.RFC3339Nano, entry.Config.Value)
			if e != nil {
				return false, e
			}
			if time.Now().Before(until) {
				allow = false
			}
		}
	}
	return allow, rows.Err()
}
func (s *Store) schedulePolicyTx(ctx context.Context, tx *sql.Tx, plan domain.Schedule) (bool, error) {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT enabled AND side_effect AND auto_execute AND policy_digest=$2 FROM `+s.table("tool_policies")+` WHERE tool_name=$1 FOR SHARE`, plan.PolicyName, plan.PolicyDigest).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !allowed || plan.Plan.Query == nil {
		return allowed, err
	}
	err = tx.QueryRowContext(ctx, `SELECT enabled AND NOT side_effect AND policy_digest=$2 FROM `+s.table("tool_policies")+` WHERE tool_name=$1 FOR SHARE`, plan.Plan.Query.Name, plan.QueryPolicyDigest).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return allowed, err
}
func (s *Store) advanceScheduleTx(ctx context.Context, tx *sql.Tx, plan *domain.Schedule, run domain.ScheduleRun, now time.Time) error {
	plan.NextIndex = domain.NextScheduleIndex(plan.Plan, now, run.Index+1)
	plan.UpdatedAt = now
	if plan.NextIndex >= plan.Plan.Runs {
		plan.Status = "completed"
	}
	if err := s.saveScheduleTx(ctx, tx, *plan); err != nil {
		return err
	}
	return s.queueScheduleTx(ctx, tx, plan)
}
func (s *Store) PrepareScheduleRun(ctx context.Context, j domain.Job) (out domain.ScheduleRun, err error) {
	p, err := schedulePayload(j)
	if err != nil {
		return out, err
	}
	err = s.transact(ctx, func(tx *sql.Tx) error {
		if e := s.lockJob(ctx, tx, j); e != nil {
			return e
		}
		if e := s.verifyScheduleJobTx(ctx, tx, j); e != nil {
			return e
		}
		if e := s.checkClaimTx(ctx, tx, j); e != nil {
			return e
		}
		snapshot, e := s.scheduleRunTx(ctx, tx, j, p, false)
		if e != nil {
			return e
		}
		if e = s.lockSourcesTx(ctx, tx, snapshot.Sources); e != nil {
			return e
		}
		plan, e := s.scheduleTx(ctx, tx, j.Scope, p.ID, true)
		if e != nil {
			return e
		}
		out, e = s.scheduleRunTx(ctx, tx, j, p, true)
		if e != nil {
			return e
		}
		if out.Status == "sent" || out.Status == "canceled" || out.Status == "expired" || out.Status == "failed" {
			return nil
		}
		if plan.Revision != out.Revision || (plan.Status != "active" && !(plan.Status == "completed" && (out.Status == "sending" || out.Status == "rendering"))) {
			out.Status = "canceled"
			return s.saveScheduleRunTx(ctx, tx, out)
		}
		if e = s.scheduleSourcesCurrentTx(ctx, tx, out); e != nil {
			if !errors.Is(e, domain.ErrStaleSource) {
				return e
			}
			plan.Status = "invalidated"
			plan.UpdatedAt = time.Now().UTC()
			out.Status = "canceled"
			if e = s.saveScheduleRunTx(ctx, tx, out); e != nil {
				return e
			}
			return s.saveScheduleTx(ctx, tx, plan)
		}
		allowed, e := s.schedulePolicyTx(ctx, tx, plan)
		if e != nil {
			return e
		}
		if !allowed {
			plan.Status = "paused"
			plan.UpdatedAt = time.Now().UTC()
			out.Status = "canceled"
			if e = s.saveScheduleRunTx(ctx, tx, out); e != nil {
				return e
			}
			return s.saveScheduleTx(ctx, tx, plan)
		}
		allowed, e = s.scheduleControlsTx(ctx, tx, j.Scope, out.Query)
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		if now.Before(out.DueAt) {
			return domain.ErrConflict
		}
		previous := out.Status
		if !allowed || !now.Before(out.NotifyUntil) {
			out.Status = "expired"
		} else {
			out.Status = "sending"
			if out.Query != nil && out.Content == "" {
				out.Status = "rendering"
			}
			if out.Query == nil && out.Content == "" {
				body, _ := json.Marshal(map[string]string{"text": out.Text})
				out.MessageType = "text"
				out.Content = string(body)
			}
			if out.Status == "sending" && out.SendStartedAt.IsZero() {
				out.SendStartedAt = now
			}
		}
		if e = s.saveScheduleRunTx(ctx, tx, out); e != nil {
			return e
		}
		if previous == "pending" {
			if e = s.advanceScheduleTx(ctx, tx, &plan, out, now); e != nil {
				return e
			}
		}
		return s.checkClaimTx(ctx, tx, j)
	})
	return
}

// CheckScheduleRun rechecks source, group controls, policy, pause/cancel and the
// lease immediately before transport. A later in-flight cancellation can still
// receive a late provider receipt; it does not authorize a fresh send.
func (s *Store) CheckScheduleRun(ctx context.Context, j domain.Job, input domain.ScheduleRun) error {
	p, err := schedulePayload(j)
	if err != nil {
		return err
	}
	return s.transact(ctx, func(tx *sql.Tx) error {
		if e := s.lockJob(ctx, tx, j); e != nil {
			return e
		}
		if e := s.verifyScheduleJobTx(ctx, tx, j); e != nil {
			return e
		}
		if e := s.checkClaimTx(ctx, tx, j); e != nil {
			return e
		}
		snapshot, e := s.scheduleRunTx(ctx, tx, j, p, false)
		if e != nil {
			return e
		}
		if e := s.lockSourcesTx(ctx, tx, snapshot.Sources); e != nil {
			return e
		}
		plan, e := s.scheduleTx(ctx, tx, j.Scope, p.ID, true)
		if e != nil {
			return e
		}
		run, e := s.scheduleRunTx(ctx, tx, j, p, true)
		if e != nil {
			return e
		}
		if run.UUID != input.UUID || !sameScheduleAction(run, input) || run.MessageType != input.MessageType || run.Content != input.Content || run.Status != "sending" || plan.Revision != run.Revision || (plan.Status != "active" && plan.Status != "completed") || !time.Now().Before(run.NotifyUntil) {
			return domain.ErrConflict
		}
		if e = s.scheduleSourcesCurrentTx(ctx, tx, run); e != nil {
			return e
		}
		allowed, e := s.scheduleControlsTx(ctx, tx, j.Scope, run.Query)
		if e != nil {
			return e
		}
		if !allowed {
			return domain.ErrConflict
		}
		allowed, e = s.schedulePolicyTx(ctx, tx, plan)
		if e != nil {
			return e
		}
		if !allowed {
			return domain.ErrUnauthorized
		}
		return s.checkClaimTx(ctx, tx, j)
	})
}
func (s *Store) FinishScheduleRun(ctx context.Context, j domain.Job, input domain.ScheduleRun, messageID string) error {
	p, err := schedulePayload(j)
	if err != nil {
		return err
	}
	if input.Scope != j.Scope || input.ScheduleID != p.ID || input.Revision != p.Revision || input.Index != p.Index || input.Origin != j.Source || messageID == "" || len(messageID) > 1024 {
		return domain.ErrInvalid
	}
	var validity error
	err = s.transact(ctx, func(tx *sql.Tx) error {
		if e := s.lockJob(ctx, tx, j); e != nil {
			return e
		}
		if e := s.verifyScheduleJobTx(ctx, tx, j); e != nil {
			return e
		}
		validity = s.checkClaimTx(ctx, tx, j)
		run, e := s.scheduleRunTx(ctx, tx, j, p, true)
		if e != nil {
			return e
		}
		if run.UUID != input.UUID || !sameScheduleAction(run, input) || run.MessageType != input.MessageType || run.Content != input.Content || run.SendStartedAt.IsZero() {
			return domain.ErrConflict
		}
		if run.MessageID != "" {
			if run.MessageID != messageID {
				return domain.ErrConflict
			}
			return nil
		}
		run.MessageID = messageID
		run.SentAt = time.Now().UTC()
		if run.Status != "canceled" {
			run.Status = "sent"
		}
		return s.saveScheduleRunTx(ctx, tx, run)
	})
	if err != nil {
		return err
	}
	return validity
}

func sameScheduleAction(a, b domain.ScheduleRun) bool {
	ax, _ := json.Marshal(a.Query)
	bx, _ := json.Marshal(b.Query)
	return a.Text == b.Text && a.QueryPolicyDigest == b.QueryPolicyDigest && string(ax) == string(bx)
}

// FreezeScheduleRun persists the exact rendered payload before the first send.
// Rendering failures before this transaction are safe to retry. Once frozen,
// retries must reuse the same payload and UUID even after an ambiguous send.
func (s *Store) FreezeScheduleRun(ctx context.Context, j domain.Job, input domain.ScheduleRun, messageType, content string) (out domain.ScheduleRun, err error) {
	p, err := schedulePayload(j)
	if err != nil {
		return out, err
	}
	if (messageType != "text" && messageType != "interactive") || len(content) == 0 || len(content) > 256<<10 || !json.Valid([]byte(content)) {
		return out, domain.ErrInvalid
	}
	err = s.transact(ctx, func(tx *sql.Tx) error {
		if e := s.lockJob(ctx, tx, j); e != nil {
			return e
		}
		if e := s.verifyScheduleJobTx(ctx, tx, j); e != nil {
			return e
		}
		if e := s.checkClaimTx(ctx, tx, j); e != nil {
			return e
		}
		snapshot, e := s.scheduleRunTx(ctx, tx, j, p, false)
		if e != nil {
			return e
		}
		if e = s.lockSourcesTx(ctx, tx, snapshot.Sources); e != nil {
			return e
		}
		plan, e := s.scheduleTx(ctx, tx, j.Scope, p.ID, true)
		if e != nil {
			return e
		}
		out, e = s.scheduleRunTx(ctx, tx, j, p, true)
		if e != nil {
			return e
		}
		if out.UUID != input.UUID || !sameScheduleAction(out, input) || plan.Revision != out.Revision || (plan.Status != "active" && plan.Status != "completed") || !time.Now().Before(out.NotifyUntil) {
			return domain.ErrConflict
		}
		if out.Content != "" {
			if out.Content != content || out.MessageType != messageType {
				return domain.ErrConflict
			}
			return nil
		}
		if out.Status != "rendering" || !out.SendStartedAt.IsZero() {
			return domain.ErrConflict
		}
		if e = s.scheduleSourcesCurrentTx(ctx, tx, out); e != nil {
			return e
		}
		allow, e := s.scheduleControlsTx(ctx, tx, j.Scope, out.Query)
		if e != nil {
			return e
		}
		if !allow {
			return domain.ErrConflict
		}
		allow, e = s.schedulePolicyTx(ctx, tx, plan)
		if e != nil {
			return e
		}
		if !allow {
			return domain.ErrUnauthorized
		}
		out.MessageType = messageType
		out.Content = content
		out.Status = "sending"
		out.SendStartedAt = time.Now().UTC()
		return s.saveScheduleRunTx(ctx, tx, out)
	})
	return
}

// FailScheduleRun records a definite failure before transport. It never rewrites
// an occurrence whose send may already have arrived or a later parent revision.
func (s *Store) FailScheduleRun(ctx context.Context, j domain.Job, input domain.ScheduleRun, reason string) error {
	switch reason {
	case "render_capability_missing", "render_not_authorized", "render_invalid", "render_source_invalid", "render_rejected", "render_attempts_exhausted":
	default:
		return domain.ErrInvalid
	}
	p, err := schedulePayload(j)
	if err != nil {
		return err
	}
	return s.transact(ctx, func(tx *sql.Tx) error {
		if e := s.lockJob(ctx, tx, j); e != nil {
			return e
		}
		if e := s.verifyScheduleJobTx(ctx, tx, j); e != nil {
			return e
		}
		if e := s.checkClaimTx(ctx, tx, j); e != nil {
			return e
		}
		plan, e := s.scheduleTx(ctx, tx, j.Scope, p.ID, true)
		if e != nil {
			return e
		}
		run, e := s.scheduleRunTx(ctx, tx, j, p, true)
		if e != nil {
			return e
		}
		if run.UUID != input.UUID || !sameScheduleAction(run, input) {
			return domain.ErrConflict
		}
		if run.Status == "canceled" || run.Status == "expired" || run.Status == "failed" || run.Status == "sent" {
			return nil
		}
		if run.Status != "rendering" || !run.SendStartedAt.IsZero() || run.Content != "" || run.MessageID != "" {
			return domain.ErrConflict
		}
		run.Status = "failed"
		run.FailureReason = reason
		if e = s.saveScheduleRunTx(ctx, tx, run); e != nil {
			return e
		}
		if plan.Revision == run.Revision && reason != "render_attempts_exhausted" {
			plan.Status = "paused"
			if reason == "render_source_invalid" {
				plan.Status = "invalidated"
			}
			plan.UpdatedAt = time.Now().UTC()
			return s.saveScheduleTx(ctx, tx, plan)
		}
		return s.checkClaimTx(ctx, tx, j)
	})
}

// ReconcileScheduleFailuresTx is called with the bounded failed-job IDs returned
// by ClaimJob housekeeping, in that same transaction. Each lookup uses a job ID
// and an occurrence primary key; it never scans all historical runs.
func (s *Store) ReconcileScheduleFailuresTx(ctx context.Context, tx *sql.Tx, jobIDs []int64) error {
	if tx == nil || len(jobIDs) > 128 {
		return domain.ErrInvalid
	}
	for _, id := range jobIDs {
		job, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM `+s.table("jobs")+` WHERE id=$1`, id))
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if job.Kind != domain.JobSchedule || job.Status != domain.JobFailed {
			continue
		}
		payload, err := schedulePayload(job)
		if err != nil {
			return err
		}
		run, err := s.scheduleRunTx(ctx, tx, job, payload, true)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if run.Status != "rendering" || !run.SendStartedAt.IsZero() || run.Content != "" || run.MessageID != "" {
			continue
		}
		run.Status = "failed"
		run.FailureReason = "render_attempts_exhausted"
		if err = s.saveScheduleRunTx(ctx, tx, run); err != nil {
			return err
		}
	}
	return nil
}
