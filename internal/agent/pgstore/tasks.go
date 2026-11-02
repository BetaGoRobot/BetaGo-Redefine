package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func (s *Store) migrateRuntime(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS ` + s.table("tasks") + `(scope_key text NOT NULL,task_id text NOT NULL,root_message_id text NOT NULL,revision bigint NOT NULL CHECK(revision>0),status text NOT NULL,body bytea NOT NULL,expires_at timestamptz NOT NULL,PRIMARY KEY(scope_key,task_id),UNIQUE(scope_key,root_message_id))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("task_sources") + `(scope_key text NOT NULL,task_id text NOT NULL,message_id text NOT NULL,PRIMARY KEY(scope_key,task_id,message_id))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("approvals") + `(scope_key text NOT NULL,task_id text NOT NULL,approval_id text NOT NULL,revision bigint NOT NULL,body bytea NOT NULL,PRIMARY KEY(scope_key,approval_id))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("turns") + `(scope_key text NOT NULL,task_id text NOT NULL,revision bigint NOT NULL,job_id bigint NOT NULL,fence bigint NOT NULL,status text NOT NULL,started_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(scope_key,task_id,job_id,fence))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("tool_executions") + `(scope_key text NOT NULL,task_id text NOT NULL,execution_key text NOT NULL,status text NOT NULL,body bytea NOT NULL,PRIMARY KEY(scope_key,task_id,execution_key))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("deliveries") + `(scope_key text NOT NULL,delivery_id text NOT NULL,task_id text NOT NULL,revision bigint NOT NULL,message_id text NOT NULL DEFAULT '',status text NOT NULL,body bytea NOT NULL,PRIMARY KEY(scope_key,delivery_id))`,
		`CREATE INDEX IF NOT EXISTS deliveries_message_idx ON ` + s.table("deliveries") + `(scope_key,message_id) WHERE message_id<>''`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("checkpoints") + `(scope_key text NOT NULL,task_id text NOT NULL,revision bigint NOT NULL,checkpoint_key text NOT NULL,body bytea NOT NULL,updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(scope_key,task_id,revision,checkpoint_key))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("tool_policies") + `(tool_name text PRIMARY KEY,side_effect boolean NOT NULL,auto_execute boolean NOT NULL DEFAULT false,enabled boolean NOT NULL DEFAULT true,policy_digest text NOT NULL)`,
		`ALTER TABLE ` + s.table("tool_policies") + ` ADD COLUMN IF NOT EXISTS auto_execute boolean NOT NULL DEFAULT false`} {
		if _, e := tx.ExecContext(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) taskTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, id string, lock bool) (domain.Task, error) {
	var t domain.Task
	var b []byte
	q := `SELECT body FROM ` + s.table("tasks") + ` WHERE scope_key=$1 AND task_id=$2`
	if lock {
		q += ` FOR UPDATE`
	}
	e := tx.QueryRowContext(ctx, q, scope.Key(), id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return t, domain.ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(b, &t)
	}
	return t, e
}
func (s *Store) Task(ctx context.Context, scope domain.Scope, id string) (domain.Task, error) {
	if e := scope.Validate(); e != nil {
		return domain.Task{}, e
	}
	var t domain.Task
	e := s.transact(ctx, func(tx *sql.Tx) error { var e error; t, e = s.taskTx(ctx, tx, scope, id, false); return e })
	return t, e
}
func (s *Store) saveTaskTx(ctx context.Context, tx *sql.Tx, t domain.Task) error {
	t.UpdatedAt = time.Now().UTC()
	b, e := encode(t)
	if e != nil {
		return e
	}
	if len(b) > 2<<20 {
		return domain.ErrInvalid
	}
	_, e = tx.ExecContext(ctx, `UPDATE `+s.table("tasks")+` SET body=$3,revision=$4,status=$5,expires_at=$6 WHERE scope_key=$1 AND task_id=$2`, t.Scope.Key(), t.ID, b, t.Revision, t.Status, t.ExpiresAt)
	return e
}
func (s *Store) turnJobTx(ctx context.Context, tx *sql.Tx, t domain.Task, approvalID string) error {
	b, _ := json.Marshal(domain.TurnRequest{TaskID: t.ID, Revision: t.Revision, ApprovalID: approvalID})
	_, e := s.enqueueTx(ctx, tx, domain.JobSpec{Kind: domain.JobAgentTurn, Scope: t.Scope, Source: t.Root, Payload: b, DedupeKey: fmt.Sprintf("task:%s:%d:%s", t.ID, t.Revision, approvalID), ExpiresAt: t.ExpiresAt})
	return e
}
func (s *Store) checkTaskTx(ctx context.Context, tx *sql.Tx, t domain.Task) error {
	var live bool
	if e := tx.QueryRowContext(ctx, `SELECT expires_at>clock_timestamp() FROM `+s.table("tasks")+` WHERE scope_key=$1 AND task_id=$2`, t.Scope.Key(), t.ID).Scan(&live); e != nil {
		return e
	}
	if !live || t.Status == "canceled" || t.Status == "completed" {
		return domain.ErrConflict
	}
	refs := append(append([]domain.SourceRef(nil), t.Sources...), t.ContextSources...)
	since, err := s.catalogHistorySinceTx(ctx, tx, t.Scope)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.OccurredAt.Before(since) {
			return domain.ErrStaleSource
		}
	}
	if e := s.lockSourcesTx(ctx, tx, refs); e != nil {
		return e
	}
	if e := s.checkSourcesTx(ctx, tx, refs); e != nil {
		return e
	}
	return s.checkTaskDeadlineTx(ctx, tx, t)
}
func (s *Store) guardTaskTx(ctx context.Context, tx *sql.Tx, j domain.Job, expected domain.Task) (domain.Task, error) {
	if j.Scope != expected.Scope {
		return domain.Task{}, domain.ErrUnauthorized
	}
	if e := s.lockJob(ctx, tx, j); e != nil {
		return domain.Task{}, e
	}
	if e := s.checkClaimTx(ctx, tx, j); e != nil {
		return domain.Task{}, e
	}
	t, e := s.taskTx(ctx, tx, j.Scope, expected.ID, true)
	if e != nil {
		return t, e
	}
	if t.Revision != expected.Revision {
		return t, domain.ErrConflict
	}
	if e = s.checkTaskTx(ctx, tx, t); e != nil {
		return t, e
	}
	if e = s.checkClaimTx(ctx, tx, j); e != nil {
		return t, e
	}
	return t, nil
}
func (s *Store) CheckTask(ctx context.Context, j domain.Job, t domain.Task) error {
	return s.transact(ctx, func(tx *sql.Tx) error { _, e := s.guardTaskTx(ctx, tx, j, t); return e })
}
func (s *Store) CreateTask(ctx context.Context, m domain.Message, expires time.Time) (domain.Task, error) {
	var t domain.Task
	if e := m.Source.Validate(); e != nil {
		return t, e
	}
	if m.Deleted || !expires.After(time.Now()) {
		return t, domain.ErrInvalid
	}
	id := "task_" + domain.StableID(m.Source.Scope.Key(), m.Source.MessageID)[:24]
	e := s.transact(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "task:"+id); e != nil {
			return e
		}
		existing, e := s.taskTx(ctx, tx, m.Source.Scope, id, true)
		if e == nil {
			t = existing
			if t.InvocationOrigin == "" {
				var body []byte
				originErr := tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("message_versions")+` WHERE scope_key=$1 AND message_id=$2 AND version=$3`, t.Scope.Key(), t.Root.MessageID, t.Root.Version).Scan(&body)
				if originErr != nil && !errors.Is(originErr, sql.ErrNoRows) {
					return originErr
				}
				var original domain.Message
				if originErr == nil && json.Unmarshal(body, &original) == nil && original.Source == t.Root {
					t.InvocationOrigin = domain.ConversationInvocation(original)
				}
			}
			if m.Source.Version <= t.Root.Version {
				return nil
			}
			if m.Source.AuthorID != t.OwnerID {
				return domain.ErrUnauthorized
			}
			if t.Status == "canceled" || t.Status == "completed" {
				return domain.ErrConflict
			}
			t.Root = m.Source
			t.Request = m.Text
			t.Sources = []domain.SourceRef{m.Source}
			t.ContextSources = nil
			t.FinalConversation = nil
			t.ReplyTargets = nil
			t.Revision++
			t.Status = "ready"
			t.ActiveJobID = 0
			for _, f := range t.Facts {
				t.Sources = append(t.Sources, f.Source)
			}
			if e = s.revokeTx(ctx, tx, t); e != nil {
				return e
			}
			if e = s.checkTaskTx(ctx, tx, t); e != nil {
				return e
			}
			if e = s.saveTaskTx(ctx, tx, t); e != nil {
				return e
			}
			return s.turnJobTx(ctx, tx, t, "")
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if e = s.lockSourcesTx(ctx, tx, []domain.SourceRef{m.Source}); e != nil {
			return e
		}
		if e = s.checkSourcesTx(ctx, tx, []domain.SourceRef{m.Source}); e != nil {
			return e
		}
		now := time.Now().UTC()
		t = domain.Task{ID: id, Scope: m.Source.Scope, OwnerID: m.Source.AuthorID, RequesterID: m.Source.AuthorID, LastActorID: m.Source.AuthorID, Revision: 1, Status: "ready", InvocationOrigin: domain.ConversationInvocation(m), Request: m.Text, Root: m.Source, Sources: []domain.SourceRef{m.Source}, ExpiresAt: expires, CreatedAt: now, UpdatedAt: now}
		b, _ := encode(t)
		if _, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("tasks")+`(scope_key,task_id,root_message_id,revision,status,body,expires_at)VALUES($1,$2,$3,1,'ready',$4,$5)`, t.Scope.Key(), t.ID, m.Source.MessageID, b, t.ExpiresAt); e != nil {
			return e
		}
		if e = s.bindSourceTx(ctx, tx, t, m.Source); e != nil {
			return e
		}
		return s.turnJobTx(ctx, tx, t, "")
	})
	return t, e
}
func (s *Store) bindSourceTx(ctx context.Context, tx *sql.Tx, t domain.Task, ref domain.SourceRef) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO `+s.table("task_sources")+`(scope_key,task_id,message_id)VALUES($1,$2,$3)ON CONFLICT DO NOTHING`, t.Scope.Key(), t.ID, ref.MessageID)
	return e
}
func (s *Store) TaskForMessage(ctx context.Context, scope domain.Scope, id string) (domain.Task, *domain.Delivery, error) {
	if e := scope.Validate(); e != nil {
		return domain.Task{}, nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT DISTINCT task_id FROM `+s.table("task_sources")+` WHERE scope_key=$1 AND message_id=$2 UNION SELECT DISTINCT task_id FROM `+s.table("deliveries")+` WHERE scope_key=$1 AND message_id=$2 AND status='delivered'`, scope.Key(), id)
	if e != nil {
		return domain.Task{}, nil, e
	}
	var ids []string
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return domain.Task{}, nil, e
		}
		ids = append(ids, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return domain.Task{}, nil, e
	}
	if len(ids) == 0 {
		return domain.Task{}, nil, domain.ErrNotFound
	}
	if len(ids) != 1 {
		return domain.Task{}, nil, domain.ErrConflict
	}
	t, e := s.Task(ctx, scope, ids[0])
	if e != nil {
		return t, nil, e
	}
	var b []byte
	e = s.db.QueryRowContext(ctx, `SELECT body FROM `+s.table("deliveries")+` WHERE scope_key=$1 AND message_id=$2 AND status='delivered' ORDER BY COALESCE((convert_from(body,'UTF8')::jsonb->>'Sequence')::int,0) DESC,`+rfc3339NanoEpochSQL(`(convert_from(body,'UTF8')::jsonb->>'UpdatedAt')`)+` DESC,delivery_id DESC LIMIT 1`, scope.Key(), id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		bot, err := s.taskBotMessage(ctx, scope, t.ID, id)
		if err != nil {
			return t, nil, err
		}
		if bot {
			return t, &domain.Delivery{TaskID: t.ID, Scope: scope, Revision: t.Revision, Kind: "tool", MessageID: id, Status: "delivered"}, nil
		}
		return t, nil, nil
	}
	var d domain.Delivery
	if e == nil {
		e = json.Unmarshal(b, &d)
	}
	return t, &d, e
}
func (s *Store) revokeTx(ctx context.Context, tx *sql.Tx, t domain.Task) error {
	rows, e := tx.QueryContext(ctx, `SELECT body FROM `+s.table("approvals")+` WHERE scope_key=$1 AND task_id=$2`, t.Scope.Key(), t.ID)
	if e != nil {
		return e
	}
	var as []domain.Approval
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return e
		}
		var a domain.Approval
		if e = json.Unmarshal(b, &a); e != nil {
			rows.Close()
			return e
		}
		as = append(as, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, a := range as {
		if a.Status == "pending" || a.Status == "approved" {
			a.Status = "revoked"
			if e = s.saveApprovalTx(ctx, tx, a); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) ControlTask(ctx context.Context, scope domain.Scope, actor string, c domain.ControlAction, source domain.SourceRef, text string) (domain.Task, error) {
	var t domain.Task
	if scope.Validate() != nil || actor == "" || c.Revision <= 0 {
		return t, domain.ErrInvalid
	}
	e := s.transact(ctx, func(tx *sql.Tx) error {
		var e error
		t, e = s.taskTx(ctx, tx, scope, c.TaskID, true)
		if e != nil {
			return e
		}
		if !t.Authorized(actor) {
			return domain.ErrUnauthorized
		}
		if t.Revision != c.Revision {
			return domain.ErrConflict
		}
		if t.Status == "canceled" || t.Status == "completed" {
			return domain.ErrConflict
		}
		if e = s.checkTaskDeadlineTx(ctx, tx, t); e != nil {
			return e
		}
		if c.Action == "approve" {
			if e = s.checkTaskTx(ctx, tx, t); e != nil {
				return e
			}
		}
		if source.MessageID != "" {
			if source.Scope != scope || source.AuthorID != actor {
				return domain.ErrUnauthorized
			}
			if e = s.lockSourcesTx(ctx, tx, []domain.SourceRef{source}); e != nil {
				return e
			}
			if e = s.checkSourcesTx(ctx, tx, []domain.SourceRef{source}); e != nil {
				return e
			}
		}
		switch c.Action {
		case "approve":
			a, e := s.approvalTx(ctx, tx, scope, c.ApprovalID)
			if e != nil {
				return e
			}
			if a.TaskID != t.ID || a.Revision != t.Revision || a.Status != "pending" || a.InterruptID == "" {
				return domain.ErrConflict
			}
			if _, e = s.approvalPolicyTx(ctx, tx, a.Tool, a.PolicyDigest); e != nil {
				return e
			}
			a.Status = "approved"
			a.Source = source
			a.ActorID = actor
			a.DecidedAt = time.Now().UTC()
			if e = s.saveApprovalTx(ctx, tx, a); e != nil {
				return e
			}
			t.Status = "ready"
			t.LastActorID = actor
			if e = s.saveTaskTx(ctx, tx, t); e != nil {
				return e
			}
			return s.turnJobTx(ctx, tx, t, a.ID)
		case "cancel":
			t.Status = "canceled"
			t.ActiveJobID = 0
			if e = s.revokeTx(ctx, tx, t); e != nil {
				return e
			}
		case "modify":
			for _, ref := range t.Sources {
				if ref == source {
					return nil
				}
			}
			if source.MessageID == "" || strings.TrimSpace(text) == "" || len(text) > 65536 {
				return domain.ErrInvalid
			}
			t.Request = text
			if e = s.replanSourcesTx(ctx, tx, &t, source); e != nil {
				return e
			}
			t.ContextSources = nil
			t.FinalConversation = nil
			t.ReplyTargets = nil
			t.Revision++
			t.Status = "ready"
			t.ActiveJobID = 0
			t.LastActorID = actor
			if e = s.revokeTx(ctx, tx, t); e != nil {
				return e
			}
			if e = s.bindSourceTx(ctx, tx, t, source); e != nil {
				return e
			}
			if e = s.saveTaskTx(ctx, tx, t); e != nil {
				return e
			}
			return s.turnJobTx(ctx, tx, t, "")
		case "delegate":
			if actor != t.OwnerID {
				return domain.ErrUnauthorized
			}
			target := domain.SourceRef{Scope: t.Scope, MessageID: "delegate", AuthorID: text, Version: 1, OccurredAt: time.Now().UTC()}
			if e = target.Validate(); e != nil {
				return e
			}
			if !t.Authorized(text) {
				t.Delegates = append(t.Delegates, text)
			}
		default:
			return domain.ErrInvalid
		}
		return s.saveTaskTx(ctx, tx, t)
	})
	return t, e
}
func (s *Store) Contribute(ctx context.Context, t domain.Task, m domain.Message) (domain.Task, error) {
	var out domain.Task
	e := s.transact(ctx, func(tx *sql.Tx) error {
		var e error
		out, e = s.taskTx(ctx, tx, t.Scope, t.ID, true)
		if e != nil {
			return e
		}
		if out.Revision != t.Revision {
			return domain.ErrConflict
		}
		if m.Source.Scope != out.Scope || m.Deleted {
			return domain.ErrUnauthorized
		}
		if e = s.checkTaskTx(ctx, tx, out); e != nil {
			return e
		}
		for _, f := range out.Facts {
			if f.Source == m.Source {
				return nil
			}
		}
		if len(out.Facts) >= 64 || len(m.Text) > 65536 {
			return domain.ErrInvalid
		}
		if e = s.lockSourcesTx(ctx, tx, []domain.SourceRef{m.Source}); e != nil {
			return e
		}
		if e = s.checkSourcesTx(ctx, tx, []domain.SourceRef{m.Source}); e != nil {
			return e
		}
		out.Facts = append(out.Facts, domain.TaskFact{Source: m.Source, Text: m.Text})
		out.Sources = append(out.Sources, m.Source)
		out.ContextSources = nil
		out.FinalConversation = nil
		out.ReplyTargets = nil
		out.Revision++
		out.Status = "ready"
		out.LastActorID = m.Source.AuthorID
		out.ActiveJobID = 0
		if e = s.revokeTx(ctx, tx, out); e != nil {
			return e
		}
		if e = s.bindSourceTx(ctx, tx, out, m.Source); e != nil {
			return e
		}
		if e = s.saveTaskTx(ctx, tx, out); e != nil {
			return e
		}
		return s.turnJobTx(ctx, tx, out, "")
	})
	return out, e
}

func (s *Store) checkTaskDeadlineTx(ctx context.Context, tx *sql.Tx, t domain.Task) error {
	var live bool
	e := tx.QueryRowContext(ctx, `SELECT expires_at>clock_timestamp() FROM `+s.table("tasks")+` WHERE scope_key=$1 AND task_id=$2`, t.Scope.Key(), t.ID).Scan(&live)
	if e != nil {
		return e
	}
	if !live {
		return domain.ErrConflict
	}
	return nil
}
func (s *Store) checkRuntimeClaimTx(ctx context.Context, tx *sql.Tx, j domain.Job, t domain.Task) error {
	if e := s.checkClaimTx(ctx, tx, j); e != nil {
		return e
	}
	return s.checkTaskDeadlineTx(ctx, tx, t)
}
func (s *Store) runtimeTransact(ctx context.Context, j domain.Job, t domain.Task, fn func(*sql.Tx) error) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		if e := fn(tx); e != nil {
			return e
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, t)
	})
}

// Replanning reloads canonical material instead of making the owner's control
// depend on the validity of a previous model context. Historical versions stay
// in the immutable message/event ledger, never as executable current facts.
func (s *Store) replanSourcesTx(ctx context.Context, tx *sql.Tx, t *domain.Task, request domain.SourceRef) error {
	refs := []domain.SourceRef{request}
	root, e := s.messageTx(ctx, tx, t.Scope, t.Root.MessageID)
	if e != nil && !errors.Is(e, domain.ErrNotFound) {
		return e
	}
	if e == nil && !root.Deleted {
		t.Root = root.Source
		refs = append(refs, root.Source)
	} else {
		t.Root = request
	}
	var facts []domain.TaskFact
	for _, old := range t.Facts {
		m, e := s.messageTx(ctx, tx, t.Scope, old.Source.MessageID)
		if errors.Is(e, domain.ErrNotFound) || e == nil && m.Deleted {
			continue
		}
		if e != nil {
			return e
		}
		facts = append(facts, domain.TaskFact{Source: m.Source, Text: m.Text})
		refs = append(refs, m.Source)
	}
	if e = s.lockSourcesTx(ctx, tx, refs); e != nil {
		return e
	}
	if e = s.checkSourcesTx(ctx, tx, refs); e != nil {
		return e
	}
	t.Sources = refs
	t.Facts = facts
	return s.checkTaskDeadlineTx(ctx, tx, *t)
}

// Every task_sources row is written by a source-validated ingress or by the
// scoped SDK send receipt. A bound locator with no ingress original is such a
// server-produced bot receipt, not a chat-wide active-task hint.
func (s *Store) taskBotMessage(ctx context.Context, scope domain.Scope, taskID, id string) (bool, error) {
	var bound bool
	e := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+s.table("task_sources")+` WHERE scope_key=$1 AND task_id=$2 AND message_id=$3)`, scope.Key(), taskID, id).Scan(&bound)
	if e != nil || !bound {
		return false, e
	}
	m, e := s.Message(ctx, scope, id)
	if errors.Is(e, domain.ErrNotFound) {
		return true, nil
	}
	if e != nil {
		return false, e
	}
	var meta domain.IngressMetadata
	if json.Unmarshal(m.Raw, &meta) != nil {
		return false, domain.ErrInvalid
	}
	return meta.SenderType == "bot" || meta.SenderType == "app", nil
}
