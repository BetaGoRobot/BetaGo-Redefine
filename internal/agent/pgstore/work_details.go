package pgstore

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func todoText(text string, max int, empty bool) bool {
	return workText(text, max, empty) && !strings.ContainsRune(text, 0)
}
func todoPriority(s string) bool {
	return s == "" || s == "low" || s == "medium" || s == "high" || s == "urgent"
}
func todoStatus(s string) bool { return s == "" || s == "open" || s == "completed" || s == "canceled" }
func todoDate(t *time.Time) bool {
	return t == nil || (!t.IsZero() && t.Year() >= 2000 && t.Year() <= 2200)
}
func todoTags(tags []string) ([]string, error) {
	if len(tags) > 20 {
		return nil, domain.ErrInvalid
	}
	seen := map[string]bool{}
	out := []string{}
	for _, s := range tags {
		if !todoText(s, 64, false) || strings.TrimSpace(s) != s {
			return nil, domain.ErrInvalid
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out, nil
}
func normalizeTodo(t *domain.Todo) {
	if t.Priority == "" {
		t.Priority = "medium"
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
}
func validTodoInput(in domain.TodoInput) bool {
	_, err := todoTags(in.Tags)
	return todoText(in.Title, 1024, false) && todoText(in.Description, 8192, true) && todoPriority(in.Priority) && todoDate(in.DueAt) && todoText(in.AssigneeID, 1024, true) && err == nil
}
func validTodoChange(c domain.TodoChange) bool {
	changed := c.Title != nil || c.Description != nil || c.Status != "" || c.Priority != nil || c.DueAt != nil || c.ClearDueAt || c.Tags != nil || len(c.AddTags) > 0 || len(c.RemoveTags) > 0 || c.AssigneeID != nil
	if !changed || (c.Title != nil && !todoText(*c.Title, 1024, false)) || (c.Description != nil && !todoText(*c.Description, 8192, true)) || !todoStatus(c.Status) || (c.Priority != nil && !todoPriority(*c.Priority)) || !todoDate(c.DueAt) || (c.ClearDueAt && c.DueAt != nil) || (c.AssigneeID != nil && !todoText(*c.AssigneeID, 1024, true)) || (c.Tags != nil && (len(c.AddTags) > 0 || len(c.RemoveTags) > 0)) {
		return false
	}
	if c.Tags != nil {
		if _, err := todoTags(*c.Tags); err != nil {
			return false
		}
	}
	if _, err := todoTags(c.AddTags); err != nil {
		return false
	}
	if _, err := todoTags(c.RemoveTags); err != nil {
		return false
	}
	for _, a := range c.AddTags {
		for _, r := range c.RemoveTags {
			if a == r {
				return false
			}
		}
	}
	return true
}
func applyTodoChange(t *domain.Todo, actor string, c domain.TodoChange) error {
	if c.AssigneeID != nil && *c.AssigneeID != "" && *c.AssigneeID != actor {
		return domain.ErrUnauthorized
	}
	if c.Title != nil {
		t.Title = *c.Title
	}
	if c.Description != nil {
		t.Description = *c.Description
	}
	if c.Status != "" {
		t.Status = c.Status
	}
	if c.Priority != nil {
		t.Priority = *c.Priority
		if t.Priority == "" {
			t.Priority = "medium"
		}
	}
	if c.ClearDueAt {
		t.DueAt = nil
	}
	if c.DueAt != nil {
		v := c.DueAt.UTC()
		t.DueAt = &v
	}
	if c.AssigneeID != nil {
		t.AssigneeID = *c.AssigneeID
	}
	if c.Tags != nil {
		t.Tags, _ = todoTags(*c.Tags)
	} else if len(c.AddTags) > 0 || len(c.RemoveTags) > 0 {
		keep := []string{}
		for _, s := range t.Tags {
			remove := false
			for _, r := range c.RemoveTags {
				if s == r {
					remove = true
				}
			}
			if !remove {
				keep = append(keep, s)
			}
		}
		keep = append(keep, c.AddTags...)
		var err error
		t.Tags, err = todoTags(keep)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) TodoDetail(ctx context.Context, scope domain.Scope, id string) (domain.Todo, error) {
	var out domain.Todo
	if scope.Validate() != nil || id == "" || len(id) > 128 {
		return out, domain.ErrInvalid
	}
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM `+s.table("todos")+` WHERE scope_key=$1 AND id=$2`, scope.Key(), id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal(b, &out) != nil || out.Scope != scope || out.ID != id {
		return out, domain.ErrUnauthorized
	}
	normalizeTodo(&out)
	return out, nil
}

type todoCursor struct {
	Fingerprint, ID   string
	CreatedAt, Anchor time.Time
}

func (s *Store) QueryTodos(ctx context.Context, scope domain.Scope, q domain.TodoQuery) (domain.TodoPage, error) {
	out := domain.TodoPage{Items: []domain.Todo{}}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if scope.Validate() != nil || q.Limit < 1 || q.Limit > 50 || !todoStatus(q.Status) || !todoPriority(q.Priority) || !todoDate(q.DueBefore) || !todoDate(q.DueAfter) || (q.DueBefore != nil && q.DueAfter != nil && q.DueBefore.Before(*q.DueAfter)) || (q.Unassigned && q.AssigneeID != "") || !todoText(q.CreatorID, 1024, true) || !todoText(q.AssigneeID, 1024, true) || len(q.Cursor) > 2048 {
		return out, domain.ErrInvalid
	}
	tags, err := todoTags(q.Tags)
	if err != nil {
		return out, err
	}
	q.Tags = tags
	fingerprintQuery := q
	fingerprintQuery.Cursor = ""
	fingerprintQuery.Limit = 0
	body, _ := json.Marshal(fingerprintQuery)
	fingerprint := domain.StableID(scope.Key(), string(body))
	cursor := todoCursor{Fingerprint: fingerprint, Anchor: time.Now().UTC()}
	if q.Cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || json.Unmarshal(b, &cursor) != nil || cursor.Fingerprint != fingerprint || cursor.ID == "" || len(cursor.ID) > 128 || cursor.CreatedAt.IsZero() || cursor.Anchor.IsZero() {
			return out, domain.ErrInvalid
		}
	}
	tagJSON, _ := json.Marshal(tags)
	rows, err := s.db.QueryContext(ctx, `WITH scoped AS (SELECT id,creator,status,body,convert_from(body,'UTF8')::jsonb doc FROM `+s.table("todos")+` WHERE scope_key=$1)
SELECT body FROM scoped WHERE ($2='' OR status=$2) AND ($3='' OR creator=$3)
AND ($4='' OR COALESCE(doc->>'AssigneeID','')=$4) AND (NOT $5::boolean OR COALESCE(doc->>'AssigneeID','')='')
AND ($6='' OR COALESCE(NULLIF(doc->>'Priority',''),'medium')=$6)
AND COALESCE(NULLIF(doc->'Tags','null'::jsonb),'[]'::jsonb) @> $7::jsonb
AND ($8::timestamptz IS NULL OR (doc->>'DueAt')::timestamptz <= $8::timestamptz)
AND ($9::timestamptz IS NULL OR (doc->>'DueAt')::timestamptz >= $9::timestamptz)
AND (NOT $10::boolean OR (status='open' AND (doc->>'DueAt')::timestamptz < $11::timestamptz))
AND ($12='' OR (doc->>'CreatedAt')::timestamptz < $13::timestamptz OR ((doc->>'CreatedAt')::timestamptz=$13::timestamptz AND id>$12))
ORDER BY (doc->>'CreatedAt')::timestamptz DESC,id ASC LIMIT $14`, scope.Key(), q.Status, q.CreatorID, q.AssigneeID, q.Unassigned, q.Priority, string(tagJSON), q.DueBefore, q.DueAfter, q.Overdue, cursor.Anchor, cursor.ID, cursor.CreatedAt, q.Limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var item domain.Todo
		if err = rows.Scan(&b); err != nil {
			return out, err
		}
		if err = json.Unmarshal(b, &item); err != nil {
			return out, err
		}
		if item.Scope != scope {
			return out, domain.ErrUnauthorized
		}
		normalizeTodo(&item)
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		last := out.Items[len(out.Items)-1]
		cursor.ID = last.ID
		cursor.CreatedAt = last.CreatedAt
		b, _ := json.Marshal(cursor)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, nil
}

func (s *Store) mutateTodo(ctx context.Context, j domain.Job, t domain.Task, key, digest, name, id string, expected any, change func(*domain.Todo, string) error) (out domain.Todo, err error) {
	err = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, actor, e := s.workGuardTx(ctx, tx, j, t, key, digest, name, expected)
		if e != nil {
			return e
		}
		var b []byte
		e = tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("todos")+` WHERE scope_key=$1 AND id=$2 FOR UPDATE`, t.Scope.Key(), id).Scan(&b)
		if errors.Is(e, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		if e != nil {
			return e
		}
		if e = json.Unmarshal(b, &out); e != nil {
			return e
		}
		if out.Scope != t.Scope || out.CreatedBy != actor || out.ID != id {
			return domain.ErrUnauthorized
		}
		normalizeTodo(&out)
		if e = change(&out, actor); e != nil {
			return e
		}
		out.UpdatedAt = time.Now().UTC()
		b, _ = encode(out)
		if _, e = tx.ExecContext(ctx, `UPDATE `+s.table("todos")+` SET status=$3,body=$4,updated_at=$5 WHERE scope_key=$1 AND id=$2`, t.Scope.Key(), id, out.Status, b, out.UpdatedAt); e != nil {
			return e
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, current)
	})
	return
}
func (s *Store) DeleteTodo(ctx context.Context, j domain.Job, t domain.Task, key, digest, id string) (domain.Todo, error) {
	if id == "" || len(id) > 128 {
		return domain.Todo{}, domain.ErrInvalid
	}
	return s.mutateTodo(ctx, j, t, key, digest, "todo_delete", id, domain.TodoDeleteInput{ID: id}, func(out *domain.Todo, _ string) error { out.Status = "canceled"; return nil })
}
