package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestTodoDetailsPreserveRichFieldsAndExactLedgerBinding(t *testing.T) {
	f, _ := workFixture(t)
	ctx := context.Background()
	due := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	in := domain.TodoInput{Title: "release", Description: "notes", Priority: "high", DueAt: &due, Tags: []string{"release", "ops"}, AssigneeID: "alice"}
	j, task, x := workTurn(t, f, "rich-todo", "alice", "todo_create", in)
	forged := in
	forged.Priority = "low"
	if _, err := f.CreateTodo(ctx, j, task, x.Key, "work-policy", forged); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("rich fields escaped ledger: %v", err)
	}
	out, err := f.CreateTodo(ctx, j, task, x.Key, "work-policy", in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.TodoDetail(ctx, task.Scope, out.ID)
	if err != nil || got.Priority != "high" || got.AssigneeID != "alice" || got.DueAt == nil || !got.DueAt.Equal(due) || !reflect.DeepEqual(got.Tags, []string{"ops", "release"}) {
		t.Fatalf("rich fields missing: %+v %v", got, err)
	}
	other := task.Scope
	other.ChatID = "other"
	if _, err = f.TodoDetail(ctx, other, out.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross group detail leak: %v", err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	empty := ""
	tags := []string{}
	change := domain.TodoChange{Priority: &empty, ClearDueAt: true, Tags: &tags, AssigneeID: &empty}
	j, task, x = workTurn(t, f, "clear-rich-todo", "alice", "todo_update", domain.TodoUpdateInput{ID: out.ID, TodoChange: change})
	got, err = f.UpdateTodo(ctx, j, task, x.Key, "work-policy", out.ID, change)
	if err != nil || got.Priority != "medium" || got.DueAt != nil || len(got.Tags) != 0 || got.AssigneeID != "" || got.Description != "notes" || got.CreatedBy != "alice" {
		t.Fatalf("clear changed unset fields or owner: %+v %v", got, err)
	}
}

func TestTodoAssigneeIsServerConstrainedAndDeleteRetainsAudit(t *testing.T) {
	f, _ := workFixture(t)
	ctx := context.Background()
	in := domain.TodoInput{Title: "self only", AssigneeID: "bob"}
	j, task, x := workTurn(t, f, "forged-assignee", "alice", "todo_create", in)
	if _, err := f.CreateTodo(ctx, j, task, x.Key, "work-policy", in); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("unverified assignment: %v", err)
	}
	if err := f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	in = domain.TodoInput{Title: "retain this"}
	j, task, x = workTurn(t, f, "own-item", "alice", "todo_create", in)
	out, err := f.CreateTodo(ctx, j, task, x.Key, "work-policy", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, task, x = workTurn(t, f, "delete-other", "bob", "todo_delete", domain.TodoDeleteInput{ID: out.ID})
	if _, err = f.DeleteTodo(ctx, j, task, x.Key, "work-policy", out.ID); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("other deleted: %v", err)
	}
	f.CompleteJob(ctx, j)
	j, task, x = workTurn(t, f, "delete-own", "alice", "todo_delete", domain.TodoDeleteInput{ID: out.ID})
	deleted, err := f.DeleteTodo(ctx, j, task, x.Key, "work-policy", out.ID)
	if err != nil || deleted.Status != "canceled" || deleted.CreatedBy != "alice" || deleted.Origin != out.Origin {
		t.Fatalf("delete lost audit: %+v %v", deleted, err)
	}
	got, err := f.TodoDetail(ctx, task.Scope, out.ID)
	if err != nil || got.Status != "canceled" {
		t.Fatalf("delete removed durable history: %+v %v", got, err)
	}
}

func TestTodoQueryFiltersAndStablePagination(t *testing.T) {
	f, _ := workFixture(t)
	ctx := context.Background()
	scope := testScope()
	due := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 4; i++ {
		actor := "alice"
		priority := "high"
		tags := []string{"ops"}
		if i == 3 {
			actor = "bob"
			priority = "low"
			tags = []string{"other"}
		}
		in := domain.TodoInput{Title: fmt.Sprintf("item-%d", i), Priority: priority, Tags: tags, DueAt: &due}
		j, task, x := workTurn(t, f, fmt.Sprintf("query-item-%d", i), actor, "todo_create", in)
		if _, err := f.CreateTodo(ctx, j, task, x.Key, "work-policy", in); err != nil {
			t.Fatal(err)
		}
		if err := f.CompleteJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	q := domain.TodoQuery{CreatorID: "alice", AssigneeID: "alice", Priority: "high", Tags: []string{"ops"}, Overdue: true, Limit: 2}
	first, err := f.QueryTodos(ctx, scope, q)
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	q.Cursor = first.NextCursor
	second, err := f.QueryTodos(ctx, scope, q)
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("second page: %+v %v", second, err)
	}
	for _, a := range first.Items {
		if a.ID == second.Items[0].ID {
			t.Fatal("pagination repeated item")
		}
	}
	q.CreatorID = "bob"
	if _, err := f.QueryTodos(ctx, scope, q); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("cursor reused for different filter: %v", err)
	}
	q = domain.TodoQuery{AssigneeID: "bob", Limit: 10}
	rows, err := f.QueryTodos(ctx, scope, q)
	if err != nil || len(rows.Items) != 1 || rows.Items[0].CreatedBy != "bob" {
		t.Fatalf("assignee target ignored: %+v %v", rows, err)
	}
}

func TestTodoLegacyBodiesRemainReadableWithDefaults(t *testing.T) {
	f, _ := workFixture(t)
	ctx := context.Background()
	in := domain.TodoInput{Title: "legacy"}
	j, task, x := workTurn(t, f, "legacy-todo", "alice", "todo_create", in)
	out, err := f.CreateTodo(ctx, j, task, x.Key, "work-policy", in)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(out)
	var raw map[string]any
	json.Unmarshal(body, &raw)
	for _, key := range []string{"Priority", "Tags", "AssigneeID", "DueAt"} {
		delete(raw, key)
	}
	body, _ = json.Marshal(raw)
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".todos SET body=$1 WHERE id=$2`, body, out.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.TodoDetail(ctx, task.Scope, out.ID)
	if err != nil || got.Priority != "medium" || got.DueAt != nil || got.Tags == nil {
		t.Fatalf("legacy body default: %+v %v", got, err)
	}
}

func TestTodoRejectsNULBeforePersistingUnqueryableBody(t *testing.T) {
	f, _ := workFixture(t)
	in := domain.TodoInput{Title: "invalid\x00title"}
	j, task, x := workTurn(t, f, "nul-todo", "alice", "todo_create", in)
	if _, err := f.CreateTodo(context.Background(), j, task, x.Key, "work-policy", in); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("NUL body would poison JSON metadata queries: %v", err)
	}
}

func TestTodoIncrementalTagsAndDateValidation(t *testing.T) {
	f, _ := workFixture(t)
	ctx := context.Background()
	in := domain.TodoInput{Title: "tags", Tags: []string{"keep", "remove"}}
	j, task, x := workTurn(t, f, "tag-source", "alice", "todo_create", in)
	out, err := f.CreateTodo(ctx, j, task, x.Key, "work-policy", in)
	if err != nil {
		t.Fatal(err)
	}
	f.CompleteJob(ctx, j)
	change := domain.TodoChange{AddTags: []string{"add"}, RemoveTags: []string{"remove"}}
	j, task, x = workTurn(t, f, "tag-update", "alice", "todo_update", domain.TodoUpdateInput{ID: out.ID, TodoChange: change})
	got, err := f.UpdateTodo(ctx, j, task, x.Key, "work-policy", out.ID, change)
	if err != nil || !reflect.DeepEqual(got.Tags, []string{"add", "keep"}) {
		t.Fatalf("incremental tags: %+v %v", got, err)
	}
	due := time.Now().UTC()
	if _, err = f.UpdateTodo(ctx, j, task, x.Key, "work-policy", out.ID, domain.TodoChange{DueAt: &due, ClearDueAt: true}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("ambiguous date clear accepted: %v", err)
	}
}
