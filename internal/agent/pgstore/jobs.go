package pgstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
)

func defaultPriority(kind domain.JobKind) int {
	switch kind {
	case domain.JobIndexMessage:
		return 1000
	case domain.JobRoute:
		return 900
	case domain.JobCleanup:
		return 800
	case domain.JobDelivery:
		return 700
	case domain.JobReminder:
		return 650
	case domain.JobSchedule:
		return 640
	case domain.JobAgentTurn:
		return 600
	case domain.JobPresentation:
		return 350
	case domain.JobMedia:
		return 300
	case domain.JobContexts:
		return 200
	case domain.JobEmbedContext:
		return 150
	case domain.JobMemory:
		return 100
	}
	return 0
}

func normalizeJob(spec domain.JobSpec) (domain.JobSpec, error) {
	if err := spec.Scope.Validate(); err != nil {
		return spec, err
	}
	if defaultPriority(spec.Kind) == 0 {
		return spec, fmt.Errorf("%w: unknown job kind", domain.ErrInvalid)
	}
	if spec.Source.MessageID != "" && spec.Source.Scope != spec.Scope {
		return spec, fmt.Errorf("%w: job source scope", domain.ErrInvalid)
	}
	if len(spec.Payload) > domain.MaxPayloadBytes || (len(spec.Payload) > 0 && !json.Valid(spec.Payload)) {
		return spec, fmt.Errorf("%w: job payload", domain.ErrInvalid)
	}
	if len(spec.Payload) == 0 {
		spec.Payload = json.RawMessage(`{}`)
	}
	if spec.Priority == 0 {
		spec.Priority = defaultPriority(spec.Kind)
	}
	if spec.MaxAttempts == 0 {
		spec.MaxAttempts = 8
	}
	if spec.MaxAttempts < 1 || spec.MaxAttempts > 100 {
		return spec, fmt.Errorf("%w: job attempts", domain.ErrInvalid)
	}
	if spec.AvailableAt.IsZero() {
		spec.AvailableAt = time.Now().UTC()
	}
	if spec.ExpiresAt.IsZero() {
		spec.ExpiresAt = spec.AvailableAt.Add(7 * 24 * time.Hour)
	}
	if !spec.ExpiresAt.After(spec.AvailableAt) {
		return spec, fmt.Errorf("%w: job expiry", domain.ErrInvalid)
	}
	if spec.DedupeKey == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return spec, err
		}
		spec.DedupeKey = hex.EncodeToString(b[:])
	}
	if len(spec.DedupeKey) > 1024 {
		return spec, fmt.Errorf("%w: job dedupe key", domain.ErrInvalid)
	}
	return spec, nil
}

func (s *Store) Enqueue(ctx context.Context, spec domain.JobSpec) (int64, error) {
	var id int64
	err := s.transact(ctx, func(tx *sql.Tx) error { var err error; id, err = s.enqueueTx(ctx, tx, spec); return err })
	return id, err
}

func (s *Store) enqueueTx(ctx context.Context, tx *sql.Tx, input domain.JobSpec) (int64, error) {
	spec, err := normalizeJob(input)
	if err != nil {
		return 0, err
	}
	spec.Payload = telemetry.InjectJobPayload(ctx, spec.Payload)
	scope, _ := encode(spec.Scope)
	source, _ := encode(spec.Source)
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO `+s.table("jobs")+`(scope_key,scope_body,source_body,kind,payload,dedupe_key,priority,max_attempts,available_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(scope_key,kind,dedupe_key) DO NOTHING RETURNING id`, spec.Scope.Key(), scope, source, spec.Kind, []byte(spec.Payload), spec.DedupeKey, spec.Priority, spec.MaxAttempts, spec.AvailableAt, spec.ExpiresAt).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var existingSource, existingPayload []byte
		err = tx.QueryRowContext(ctx, `SELECT id,source_body,payload FROM `+s.table("jobs")+` WHERE scope_key=$1 AND kind=$2 AND dedupe_key=$3`, spec.Scope.Key(), spec.Kind, spec.DedupeKey).Scan(&id, &existingSource, &existingPayload)
		if err == nil && (string(existingSource) != string(source) || string(jobBusinessPayload(existingPayload)) != string(jobBusinessPayload(spec.Payload))) {
			return 0, fmt.Errorf("%w: job dedupe payload", domain.ErrConflict)
		}
	}
	return id, err
}

// Trace metadata belongs to the queue transport, never to immutable work
// identity. RawMessage preserves exact large integers while removing only the
// server-owned propagation field; a retry retains the first stored parent.
func jobBusinessPayload(payload []byte) []byte {
	var object map[string]json.RawMessage
	if json.Unmarshal(payload, &object) != nil || object == nil {
		return payload
	}
	delete(object, "_otel")
	clean, err := json.Marshal(object)
	if err != nil {
		return payload
	}
	return clean
}

const jobColumns = `id,scope_body,source_body,kind,payload,dedupe_key,priority,max_attempts,status,owner,fence,attempt,available_at,expires_at,lease_until,created_at,updated_at,last_error`

type scanner interface{ Scan(...any) error }

func scanJob(row scanner) (domain.Job, error) {
	var j domain.Job
	var scope, source, payload []byte
	var lease sql.NullTime
	err := row.Scan(&j.ID, &scope, &source, &j.Kind, &payload, &j.DedupeKey, &j.Priority, &j.MaxAttempts, &j.Status, &j.Owner, &j.Fence, &j.Attempt, &j.AvailableAt, &j.ExpiresAt, &lease, &j.CreatedAt, &j.UpdatedAt, &j.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return j, domain.ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if err = json.Unmarshal(scope, &j.Scope); err != nil {
		return j, err
	}
	if err = json.Unmarshal(source, &j.Source); err != nil {
		return j, err
	}
	j.Payload = append(json.RawMessage(nil), payload...)
	if lease.Valid {
		j.LeaseUntil = lease.Time
	}
	return j, nil
}

func (s *Store) Job(ctx context.Context, id int64) (domain.Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM `+s.table("jobs")+` WHERE id=$1`, id))
}

func jobKindsFilter(kinds []domain.JobKind, offset int) (string, []any) {
	if len(kinds) == 0 {
		return "", nil
	}
	args := make([]any, 0, len(kinds))
	placeholders := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		args = append(args, kind)
		placeholders = append(placeholders, fmt.Sprintf("$%d", offset+len(args)))
	}
	return " AND kind IN (" + strings.Join(placeholders, ",") + ")", args
}

// ClaimJob recovers expired leases, increases a fencing token, and serializes
// claims with SKIP LOCKED. A nil kinds slice allows every job kind.
func (s *Store) ClaimJob(ctx context.Context, kinds []domain.JobKind, owner string, lease time.Duration) (domain.Job, error) {
	if strings.TrimSpace(owner) == "" || len(owner) > 1024 || lease <= 0 || lease > 24*time.Hour {
		return domain.Job{}, fmt.Errorf("%w: job lease or owner", domain.ErrInvalid)
	}
	for _, kind := range kinds {
		if defaultPriority(kind) == 0 {
			return domain.Job{}, fmt.Errorf("%w: claim kind", domain.ErrInvalid)
		}
	}
	var job domain.Job
	err := s.transact(ctx, func(tx *sql.Tx) error {
		// Housekeeping must obey the same nonblocking row-lock policy as claim
		// selection. Bound each batch and restrict it to the requested kinds.
		expiryFilter, expiryArgs := jobKindsFilter(kinds, 0)
		expiredRows, err := tx.QueryContext(ctx, `WITH expired AS MATERIALIZED (SELECT id FROM `+s.table("jobs")+` WHERE status IN ('pending','running') AND (expires_at<=clock_timestamp() OR (status='running' AND lease_until<=clock_timestamp() AND attempt>=max_attempts))`+expiryFilter+` ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT 128) UPDATE `+s.table("jobs")+` SET status='failed',owner='',lease_until=NULL,updated_at=clock_timestamp(),last_error=CASE WHEN expires_at<=clock_timestamp() THEN 'job expired' ELSE 'attempt limit after lease expiry' END WHERE id IN (SELECT id FROM expired) RETURNING id,kind`, expiryArgs...)
		if err != nil {
			return err
		}
		var failedSchedules []int64
		for expiredRows.Next() {
			var id int64
			var kind string
			if err = expiredRows.Scan(&id, &kind); err != nil {
				expiredRows.Close()
				return err
			}
			if kind == string(domain.JobSchedule) {
				failedSchedules = append(failedSchedules, id)
			}
		}
		err = expiredRows.Err()
		expiredRows.Close()
		if err != nil {
			return err
		}
		if len(failedSchedules) > 0 {
			if err = s.ReconcileScheduleFailuresTx(ctx, tx, failedSchedules); err != nil {
				return err
			}
		}
		filter, kindArgs := jobKindsFilter(kinds, 2)
		args := []any{owner, lease.Seconds()}
		args = append(args, kindArgs...)
		query := `WITH candidate AS (SELECT id FROM ` + s.table("jobs") + ` WHERE expires_at>clock_timestamp() AND attempt<max_attempts AND ((status='pending' AND available_at<=clock_timestamp()) OR (status='running' AND lease_until<=clock_timestamp()))` + filter + ` ORDER BY CASE WHEN kind='index_message' THEN 0 ELSE 1 END,priority DESC,available_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE ` + s.table("jobs") + ` SET status='running',owner=$1,fence=fence+1,attempt=attempt+1,lease_until=LEAST(clock_timestamp()+$2::double precision*interval '1 second',expires_at),updated_at=clock_timestamp() WHERE id=(SELECT id FROM candidate) RETURNING ` + jobColumns
		job, err = scanJob(tx.QueryRowContext(ctx, query, args...))
		// Commit expiry bookkeeping even when there is nothing left to claim.
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil {
		return domain.Job{}, err
	}
	if job.ID == 0 {
		return domain.Job{}, domain.ErrNotFound
	}
	return job, nil
}

func claimResult(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrStaleClaim
	}
	return nil
}

// lockJob acquires by immutable identity before evaluating volatile lease and
// expiry predicates. An UPDATE can evaluate its WHERE clause before waiting for
// a row lock; separating these statements makes the following clock check run
// only after the lock is held.
func (s *Store) lockJob(ctx context.Context, tx *sql.Tx, job domain.Job) error {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM `+s.table("jobs")+` WHERE id=$1 AND scope_key=$2 FOR UPDATE`, job.ID, job.Scope.Key()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleClaim
	}
	return err
}

func (s *Store) CompleteJob(ctx context.Context, job domain.Job) error {
	if err := job.Scope.Validate(); err != nil {
		return err
	}
	return s.transact(ctx, func(tx *sql.Tx) error {
		if err := s.lockJob(ctx, tx, job); err != nil {
			return err
		}
		r, err := tx.ExecContext(ctx, `UPDATE `+s.table("jobs")+` SET status='completed',owner='',lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1 AND scope_key=$2 AND owner=$3 AND fence=$4 AND status='running' AND lease_until>clock_timestamp() AND expires_at>clock_timestamp()`, job.ID, job.Scope.Key(), job.Owner, job.Fence)
		return claimResult(r, err)
	})
}

func (s *Store) RetryJob(ctx context.Context, job domain.Job, cause error) error {
	if err := job.Scope.Validate(); err != nil {
		return err
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	if len(message) > 4096 {
		message = string([]rune(message)[:min(len([]rune(message)), 1024)])
	}
	backoff := `LEAST(3600,power(2,LEAST(attempt-1,12))) * interval '1 second'`
	return s.transact(ctx, func(tx *sql.Tx) error {
		if err := s.lockJob(ctx, tx, job); err != nil {
			return err
		}
		r, err := tx.ExecContext(ctx, `UPDATE `+s.table("jobs")+` SET status=CASE WHEN attempt>=max_attempts OR clock_timestamp()+`+backoff+`>=expires_at THEN 'failed' ELSE 'pending' END,owner='',lease_until=NULL,available_at=LEAST(expires_at,clock_timestamp()+`+backoff+`),last_error=$5,updated_at=clock_timestamp() WHERE id=$1 AND scope_key=$2 AND owner=$3 AND fence=$4 AND status='running' AND lease_until>clock_timestamp() AND expires_at>clock_timestamp()`, job.ID, job.Scope.Key(), job.Owner, job.Fence, message)
		return claimResult(r, err)
	})
}
