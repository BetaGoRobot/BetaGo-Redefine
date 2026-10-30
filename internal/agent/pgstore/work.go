package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func (s *Store) migrateWork(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS ` + s.table("todos") + `(scope_key text NOT NULL,id text NOT NULL,creator text NOT NULL,status text NOT NULL,body bytea NOT NULL,updated_at timestamptz NOT NULL,PRIMARY KEY(scope_key,id))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("reminders") + `(scope_key text NOT NULL,id text NOT NULL,creator text NOT NULL,status text NOT NULL,body bytea NOT NULL,due_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,PRIMARY KEY(scope_key,id))`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Work mutation admission and the actual PG write share the job/task/source,
// execution and policy locks. A model-side check before the call is not enough.
func (s *Store) workGuardTx(ctx context.Context, tx *sql.Tx, j domain.Job, t domain.Task, key, digest, name string, expected any) (domain.Task, string, error) {
	current, err := s.guardTaskTx(ctx, tx, j, t)
	if err != nil {
		return current, "", err
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT enabled AND side_effect AND auto_execute AND policy_digest=$2 FROM `+s.table("tool_policies")+` WHERE tool_name=$1 FOR SHARE`, name, digest).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return current, "", domain.ErrUnauthorized
	}
	if err != nil {
		return current, "", err
	}
	x, err := s.toolTx(ctx, tx, t.Scope, t.ID, key)
	if errors.Is(err, domain.ErrNotFound) {
		return current, "", domain.ErrUnauthorized
	}
	if err != nil {
		return current, "", err
	}
	if x.Tool != name || x.Status != "running_effect" || x.JobID != j.ID || x.Fence != j.Fence || x.Revision != current.Revision || x.ActorID == "" || x.ActorID != current.LastActorID {
		return current, "", domain.ErrUnauthorized
	}
	// Decode the persisted validated arguments into the exact input type. This
	// binds the actual write to the ledger, including defaulted optional fields.
	actual := reflect.New(reflect.TypeOf(expected))
	actualErr := json.Unmarshal([]byte(x.Arguments), actual.Interface())
	actualBytes, _ := json.Marshal(actual.Elem().Interface())
	expectedBytes, _ := json.Marshal(expected)
	if actualErr != nil || string(actualBytes) != string(expectedBytes) {
		return current, "", domain.ErrUnauthorized
	}
	return current, x.ActorID, s.checkRuntimeClaimTx(ctx, tx, j, current)
}

func workOrigin(t domain.Task) domain.SourceRef {
	// Contributions are appended in server acceptance order. Attribute the
	// operation to the newest bound material from the actual executing speaker.
	for i := len(t.Sources) - 1; i >= 0; i-- {
		if t.Sources[i].AuthorID == t.LastActorID {
			return t.Sources[i]
		}
	}
	return t.Root
}
func workSources(t domain.Task) []domain.SourceRef {
	seen := map[domain.SourceRef]bool{}
	refs := []domain.SourceRef{}
	for _, ref := range append(append([]domain.SourceRef{t.Root}, t.Sources...), t.ContextSources...) {
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	return refs
}
func reminderSources(r domain.Reminder) []domain.SourceRef {
	if len(r.Sources) > 0 {
		return r.Sources
	}
	return []domain.SourceRef{r.Origin}
}
func workText(text string, max int, empty bool) bool {
	return utf8.ValidString(text) && len(text) <= max && (empty || strings.TrimSpace(text) != "")
}

func (s *Store) CreateTodo(ctx context.Context, j domain.Job, t domain.Task, key, digest string, in domain.TodoInput) (out domain.Todo, err error) {
	if !validTodoInput(in) {
		return out, domain.ErrInvalid
	}
	err = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, actor, e := s.workGuardTx(ctx, tx, j, t, key, digest, "todo_create", in)
		if e != nil {
			return e
		}
		if in.AssigneeID != "" && in.AssigneeID != actor {
			return domain.ErrUnauthorized
		}
		assignee := in.AssigneeID
		if assignee == "" {
			assignee = actor
		}
		priority := in.Priority
		if priority == "" {
			priority = "medium"
		}
		tags, _ := todoTags(in.Tags)
		id := "todo_" + domain.StableID(t.Scope.Key(), t.ID, key)[:24]
		var b []byte
		e = tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("todos")+` WHERE scope_key=$1 AND id=$2`, t.Scope.Key(), id).Scan(&b)
		if e == nil {
			if e = json.Unmarshal(b, &out); e != nil {
				return e
			}
			normalizeTodo(&out)
			if out.Scope != t.Scope || out.CreatedBy != actor || out.Title != in.Title || out.Description != in.Description || out.Priority != priority || out.AssigneeID != assignee || !reflect.DeepEqual(out.Tags, tags) || (out.DueAt == nil) != (in.DueAt == nil) || (out.DueAt != nil && !out.DueAt.Equal(*in.DueAt)) {
				return domain.ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		now := time.Now().UTC()
		out = domain.Todo{ID: id, Scope: t.Scope, CreatedBy: actor, Title: in.Title, Description: in.Description, Status: "open", Priority: priority, AssigneeID: assignee, Tags: tags, DueAt: in.DueAt, Origin: workOrigin(current), CreatedAt: now, UpdatedAt: now}
		b, _ = encode(out)
		_, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("todos")+`(scope_key,id,creator,status,body,updated_at)VALUES($1,$2,$3,$4,$5,$6)`, t.Scope.Key(), id, actor, out.Status, b, now)
		if e != nil {
			return e
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, current)
	})
	return
}

func (s *Store) ListTodos(ctx context.Context, scope domain.Scope, status string, limit int) ([]domain.Todo, error) {
	if scope.Validate() != nil || limit < 1 || limit > 50 || (status != "" && status != "open" && status != "completed" && status != "canceled") {
		return nil, domain.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM `+s.table("todos")+` WHERE scope_key=$1 AND ($2='' OR status=$2) ORDER BY updated_at DESC,id LIMIT $3`, scope.Key(), status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Todo{}
	for rows.Next() {
		var b []byte
		var item domain.Todo
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &item); err != nil {
			return nil, err
		}
		if item.Scope != scope {
			return nil, domain.ErrUnauthorized
		}
		normalizeTodo(&item)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpdateTodo(ctx context.Context, j domain.Job, t domain.Task, key, digest, id string, change domain.TodoChange) (out domain.Todo, err error) {
	if id == "" || len(id) > 128 || !validTodoChange(change) {
		return out, domain.ErrInvalid
	}
	return s.mutateTodo(ctx, j, t, key, digest, "todo_update", id, domain.TodoUpdateInput{ID: id, TodoChange: change}, func(out *domain.Todo, actor string) error { return applyTodoChange(out, actor, change) })
}

func (s *Store) CreateReminder(ctx context.Context, j domain.Job, t domain.Task, key, digest string, in domain.ReminderInput) (out domain.Reminder, err error) {
	now := time.Now().UTC()
	if !workText(in.Text, 4096, false) || !in.DueAt.After(now) || in.DueAt.After(now.Add(366*24*time.Hour)) {
		return out, domain.ErrInvalid
	}
	err = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, actor, e := s.workGuardTx(ctx, tx, j, t, key, digest, "reminder_create", in)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, t.Scope.Key()+":reminder-capacity"); e != nil {
			return e
		}
		id := "reminder_" + domain.StableID(t.Scope.Key(), t.ID, key)[:24]
		var b []byte
		e = tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("reminders")+` WHERE scope_key=$1 AND id=$2`, t.Scope.Key(), id).Scan(&b)
		if e == nil {
			if e = json.Unmarshal(b, &out); e != nil {
				return e
			}
			if out.Scope != t.Scope || out.CreatedBy != actor || out.Text != in.Text || !out.DueAt.Equal(in.DueAt) {
				return domain.ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+s.table("reminders")+` WHERE scope_key=$1 AND status IN ('pending','sending') AND due_at>clock_timestamp()-interval '30 minutes'`, t.Scope.Key()).Scan(&n); e != nil {
			return e
		}
		if n >= 100 {
			return domain.ErrConflict
		}
		now := time.Now().UTC()
		if !in.DueAt.After(now) {
			return domain.ErrInvalid
		}
		out = domain.Reminder{ID: id, CreatedBy: actor, Text: in.Text, Status: "pending", Scope: t.Scope, Origin: workOrigin(current), Sources: workSources(current), DueAt: in.DueAt.UTC(), NotifyUntil: in.DueAt.UTC().Add(30 * time.Minute), UUID: domain.StableUUID(t.Scope.Key(), id), CreatedAt: now, UpdatedAt: now}
		b, _ = encode(out)
		if _, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("reminders")+`(scope_key,id,creator,status,body,due_at,updated_at)VALUES($1,$2,$3,$4,$5,$6,$7)`, t.Scope.Key(), id, actor, out.Status, b, out.DueAt, now); e != nil {
			return e
		}
		payload, _ := json.Marshal(map[string]string{"reminder_id": id})
		if _, e = s.enqueueTx(ctx, tx, domain.JobSpec{Kind: domain.JobReminder, Scope: t.Scope, Source: out.Origin, Payload: payload, DedupeKey: id, AvailableAt: out.DueAt, ExpiresAt: out.NotifyUntil}); e != nil {
			return e
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, current)
	})
	return
}

func (s *Store) ListReminders(ctx context.Context, scope domain.Scope, status string, limit int) ([]domain.Reminder, error) {
	if scope.Validate() != nil || limit < 1 || limit > 50 || (status != "" && status != "pending" && status != "sent" && status != "canceled" && status != "expired") {
		return nil, domain.ErrInvalid
	}
	// Expiry is derived from the frozen deadline even if no worker was online
	// to consume the job; never describe an overdue unsent item as delivered.
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM `+s.table("reminders")+` WHERE scope_key=$1 AND ($2='' OR CASE WHEN status IN ('pending','sending') AND (convert_from(body,'UTF8')::jsonb->>'NotifyUntil')::timestamptz<=clock_timestamp() THEN 'expired' WHEN status='sending' THEN 'pending' ELSE status END=$2) ORDER BY due_at DESC,id LIMIT $3`, scope.Key(), status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Reminder{}
	for rows.Next() {
		var b []byte
		var item domain.Reminder
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &item); err != nil {
			return nil, err
		}
		if item.Scope != scope {
			return nil, domain.ErrUnauthorized
		}
		if (item.Status == "pending" || item.Status == "sending") && time.Now().After(item.NotifyUntil) {
			item.Status = "expired"
		}
		if item.Status == "sending" {
			item.Status = "pending"
		}
		out = append(out, item)
		if len(out) == limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *Store) CancelReminder(ctx context.Context, j domain.Job, t domain.Task, key, digest, id string) (out domain.Reminder, err error) {
	if id == "" || len(id) > 128 {
		return out, domain.ErrInvalid
	}
	err = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, actor, e := s.workGuardTx(ctx, tx, j, t, key, digest, "reminder_cancel", domain.ReminderCancelInput{ID: id})
		if e != nil {
			return e
		}
		out, e = s.reminderTx(ctx, tx, t.Scope, id)
		if e != nil {
			return e
		}
		if out.CreatedBy != actor {
			return domain.ErrUnauthorized
		}
		if out.Status == "sent" {
			return domain.ErrConflict
		}
		out.Status = "canceled"
		out.UpdatedAt = time.Now().UTC()
		if e = s.saveReminderTx(ctx, tx, out); e != nil {
			return e
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, current)
	})
	return
}

func (s *Store) reminderTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, id string, lock ...bool) (out domain.Reminder, err error) {
	var b []byte
	query := `SELECT body FROM ` + s.table("reminders") + ` WHERE scope_key=$1 AND id=$2`
	if len(lock) == 0 || lock[0] {
		query += ` FOR UPDATE`
	}
	err = tx.QueryRowContext(ctx, query, scope.Key(), id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if err != nil {
		return
	}
	err = json.Unmarshal(b, &out)
	if err == nil && out.Scope != scope {
		err = domain.ErrUnauthorized
	}
	return
}
func (s *Store) saveReminderTx(ctx context.Context, tx *sql.Tx, r domain.Reminder) error {
	b, err := encode(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE `+s.table("reminders")+` SET status=$3,body=$4,updated_at=$5 WHERE scope_key=$1 AND id=$2`, r.Scope.Key(), r.ID, r.Status, b, r.UpdatedAt)
	return err
}

// Bind a claim to the persisted notification, not a caller-supplied payload.
func (s *Store) verifyReminderJobTx(ctx context.Context, tx *sql.Tx, j domain.Job) error {
	var kind string
	var source, payload []byte
	err := tx.QueryRowContext(ctx, `SELECT kind,source_body,payload FROM `+s.table("jobs")+` WHERE id=$1 AND scope_key=$2`, j.ID, j.Scope.Key()).Scan(&kind, &source, &payload)
	if err != nil {
		return err
	}
	var ref domain.SourceRef
	if kind != string(domain.JobReminder) || json.Unmarshal(source, &ref) != nil || ref != j.Source || string(payload) != string(j.Payload) {
		return domain.ErrUnauthorized
	}
	return nil
}

func (s *Store) PrepareReminder(ctx context.Context, j domain.Job) (out domain.Reminder, err error) {
	var payload struct {
		ID string `json:"reminder_id"`
	}
	if j.Kind != domain.JobReminder || json.Unmarshal(j.Payload, &payload) != nil || payload.ID == "" {
		return out, domain.ErrInvalid
	}
	err = s.transact(ctx, func(tx *sql.Tx) error {
		if e := s.lockJob(ctx, tx, j); e != nil {
			return e
		}
		if e := s.verifyReminderJobTx(ctx, tx, j); e != nil {
			return e
		}
		if e := s.checkClaimTx(ctx, tx, j); e != nil {
			return e
		}
		snapshot, e := s.reminderTx(ctx, tx, j.Scope, payload.ID, false)
		if e != nil {
			return e
		}
		if e = s.lockSourcesTx(ctx, tx, reminderSources(snapshot)); e != nil {
			return e
		}
		out, e = s.reminderTx(ctx, tx, j.Scope, payload.ID)
		if e != nil {
			return e
		}
		if out.Status == "sent" || out.Status == "canceled" {
			return nil
		}
		if out.Origin != j.Source {
			return domain.ErrUnauthorized
		}
		if e = s.checkSourcesTx(ctx, tx, reminderSources(out)); e != nil {
			return e
		}
		now := time.Now().UTC()
		if now.Before(out.DueAt) || !now.Before(out.NotifyUntil) {
			return domain.ErrConflict
		}
		if e = s.checkClaimTx(ctx, tx, j); e != nil {
			return e
		}
		out.Status = "sending"
		if out.SendStartedAt.IsZero() {
			out.SendStartedAt = now
		}
		out.UpdatedAt = now
		return s.saveReminderTx(ctx, tx, out)
	})
	return
}

// Record a real receipt even if cancellation/lease loss arrived while sending.
// A cancellation remains canceled, and a later sender cannot replace a receipt.
func (s *Store) FinishReminder(ctx context.Context, j domain.Job, input domain.Reminder, messageID string) error {
	var payload struct {
		ID string `json:"reminder_id"`
	}
	if j.Kind != domain.JobReminder || json.Unmarshal(j.Payload, &payload) != nil || payload.ID != input.ID || input.Scope != j.Scope || input.Origin != j.Source || messageID == "" || len(messageID) > 1024 {
		return domain.ErrInvalid
	}
	var validity error
	err := s.transact(ctx, func(tx *sql.Tx) error {
		if e := s.lockJob(ctx, tx, j); e != nil {
			return e
		}
		if e := s.verifyReminderJobTx(ctx, tx, j); e != nil {
			return e
		}
		validity = s.checkClaimTx(ctx, tx, j)
		current, e := s.reminderTx(ctx, tx, j.Scope, input.ID)
		if e != nil {
			return e
		}
		if current.UUID != input.UUID || current.Text != input.Text || current.Origin != input.Origin {
			return domain.ErrConflict
		}
		if current.MessageID != "" {
			if current.MessageID != messageID {
				return domain.ErrConflict
			}
			return nil
		}
		if current.SendStartedAt.IsZero() {
			return domain.ErrConflict
		}
		current.MessageID = messageID
		current.SentAt = time.Now().UTC()
		current.UpdatedAt = current.SentAt
		if current.Status != "canceled" {
			current.Status = "sent"
		}
		return s.saveReminderTx(ctx, tx, current)
	})
	if err != nil {
		return err
	}
	return validity
}
