package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

type workStore interface {
	CreateTodo(context.Context, domain.Job, domain.Task, string, string, domain.TodoInput) (domain.Todo, error)
	ListTodos(context.Context, domain.Scope, string, int) ([]domain.Todo, error)
	UpdateTodo(context.Context, domain.Job, domain.Task, string, string, string, domain.TodoChange) (domain.Todo, error)
	CreateReminder(context.Context, domain.Job, domain.Task, string, string, domain.ReminderInput) (domain.Reminder, error)
	ListReminders(context.Context, domain.Scope, string, int) ([]domain.Reminder, error)
	CancelReminder(context.Context, domain.Job, domain.Task, string, string, string) (domain.Reminder, error)
	PrepareReminder(context.Context, domain.Job) (domain.Reminder, error)
	FinishReminder(context.Context, domain.Job, domain.Reminder, string) error
}

func workFixture(t *testing.T) (fixture, workStore) {
	t.Helper()
	f := setup(t)
	w, ok := any(f.Store).(workStore)
	if !ok {
		t.Fatal("group work capabilities not implemented")
	}
	return f, w
}

func workTurn(t *testing.T, f fixture, id, actor, tool string, arguments any) (domain.Job, domain.Task, domain.ToolExecution) {
	t.Helper()
	e := event(id, 1)
	e.Message.Source.MessageID, e.Message.Source.AuthorID = id, actor
	accept(t, f, e)
	task, err := f.CreateTask(context.Background(), e.Message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(context.Background(), []domain.JobKind{domain.JobAgentTurn}, "work-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.StartTurn(context.Background(), j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	policy := pgstore.ToolPolicy{Name: tool, SideEffect: true, AutoExecute: true, Digest: "work-policy"}
	if err = f.SyncToolPolicies(context.Background(), []pgstore.ToolPolicy{policy}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(arguments)
	x, _, err := f.BeginTool(context.Background(), j, task, tool, string(args), "", true, policy.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return j, task, x
}

func TestGroupTodoUsesBackendOwnerAndRequiresLiveToolReservation(t *testing.T) {
	f, w := workFixture(t)
	ctx := context.Background()
	j, task, x := workTurn(t, f, "todo-origin", "alice", "todo_create", domain.TodoInput{Title: "准备周报"})
	todo, err := w.CreateTodo(ctx, j, task, x.Key, "work-policy", domain.TodoInput{Title: "准备周报"})
	if err != nil {
		t.Fatal(err)
	}
	if todo.CreatedBy != "alice" || todo.Scope != task.Scope || todo.Status != "open" {
		t.Fatalf("wrong owner/scope: %+v", todo)
	}
	again, err := w.CreateTodo(ctx, j, task, x.Key, "work-policy", domain.TodoInput{Title: "准备周报"})
	if err != nil || again.ID != todo.ID {
		t.Fatalf("retry created duplicate: %+v %v", again, err)
	}
	if _, err = w.CreateTodo(ctx, j, task, "unreserved-key", "work-policy", domain.TodoInput{Title: "绕过执行记录"}); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("unreserved write: %v", err)
	}
	other := task.Scope
	other.ChatID = "other-group"
	items, err := w.ListTodos(ctx, other, "", 20)
	if err != nil || len(items) != 0 {
		t.Fatalf("cross group leak: %v %v", items, err)
	}
	if _, err = w.CreateTodo(ctx, j, task, x.Key, "work-policy", domain.TodoInput{Title: "篡改已入账参数"}); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("ledger arguments not bound: %v", err)
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='todo_create'`); err != nil {
		t.Fatal(err)
	}
	if _, err = w.CreateTodo(ctx, j, task, x.Key, "work-policy", domain.TodoInput{Title: "准备周报"}); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked write reused without checking grant: %v", err)
	}
}

func TestReminderSurvivesOriginalTaskExpiryAndKeepsStableReceipt(t *testing.T) {
	f, w := workFixture(t)
	ctx := context.Background()
	input := domain.ReminderInput{Text: "该休息了", DueAt: time.Now().UTC().Add(time.Second)}
	j, task, x := workTurn(t, f, "reminder-origin", "alice", "reminder_create", input)
	r, err := w.CreateReminder(ctx, j, task, x.Key, "work-policy", input)
	if err != nil {
		t.Fatal(err)
	}
	if r.CreatedBy != "alice" || r.UUID == "" || r.NotifyUntil.Sub(r.DueAt) != 30*time.Minute {
		t.Fatalf("unfrozen reminder: %+v", r)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".tasks SET expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(r.DueAt) + 10*time.Millisecond)
	rj, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobReminder}, "notifier", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := w.PrepareReminder(ctx, rj)
	if err != nil || prepared.UUID != r.UUID || prepared.Text != r.Text {
		t.Fatalf("original task expiry killed scheduled notification: %+v %v", prepared, err)
	}
	if err = w.FinishReminder(ctx, rj, prepared, "notified"); err != nil {
		t.Fatal(err)
	}
	again, err := w.PrepareReminder(ctx, rj)
	if err != nil || again.Status != "sent" || again.MessageID != "notified" {
		t.Fatalf("receipt not reusable: %+v %v", again, err)
	}
}

func TestOtherGroupMemberCannotEditOrCancelPersonalWork(t *testing.T) {
	f, w := workFixture(t)
	ctx := context.Background()
	j, task, x := workTurn(t, f, "alice-todo", "alice", "todo_create", domain.TodoInput{Title: "自己的待办"})
	todo, err := w.CreateTodo(ctx, j, task, x.Key, "work-policy", domain.TodoInput{Title: "自己的待办"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, task, x = workTurn(t, f, "bob-todo", "bob", "todo_update", domain.TodoUpdateInput{ID: todo.ID, TodoChange: domain.TodoChange{Status: "completed"}})
	if _, err = w.UpdateTodo(ctx, j, task, x.Key, "work-policy", todo.ID, domain.TodoChange{Status: "completed"}); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("other member edited todo: %v", err)
	}
}

func TestReminderCancelOwnershipAndLateReceiptPreserveCanceledState(t *testing.T) {
	f, w := workFixture(t)
	ctx := context.Background()
	input := domain.ReminderInput{Text: "提醒测试", DueAt: time.Now().Add(time.Second)}
	j, task, x := workTurn(t, f, "alice-reminder", "alice", "reminder_create", input)
	r, err := w.CreateReminder(ctx, j, task, x.Key, "work-policy", input)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(r.DueAt) + 10*time.Millisecond)
	notify, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobReminder}, "sender", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := w.PrepareReminder(ctx, notify)
	if err != nil {
		t.Fatal(err)
	}
	forged := notify
	forged.Payload = []byte(`{"reminder_id":"another"}`)
	if _, err = w.PrepareReminder(ctx, forged); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("forged persisted job accepted: %v", err)
	}
	j, task, x = workTurn(t, f, "bob-cancel", "bob", "reminder_cancel", domain.ReminderCancelInput{ID: r.ID})
	if _, err = w.CancelReminder(ctx, j, task, x.Key, "work-policy", r.ID); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("other member canceled: %v", err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, task, x = workTurn(t, f, "alice-cancel", "alice", "reminder_cancel", domain.ReminderCancelInput{ID: r.ID})
	if _, err = w.CancelReminder(ctx, j, task, x.Key, "work-policy", r.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.FinishReminder(ctx, notify, prepared, "late-real-receipt"); err != nil {
		t.Fatal(err)
	}
	items, err := w.ListReminders(ctx, task.Scope, "canceled", 20)
	if err != nil || len(items) != 1 || items[0].MessageID != "late-real-receipt" {
		t.Fatalf("late receipt resurrected canceled reminder: %v %v", items, err)
	}
	again, err := w.PrepareReminder(ctx, notify)
	if err != nil || again.Status != "canceled" {
		t.Fatalf("canceled reminder can send again: %+v %v", again, err)
	}
}

func TestReminderStatusFilterRunsBeforeLimitAndIsGroupScoped(t *testing.T) {
	f, w := workFixture(t)
	ctx := context.Background()
	scope := event("filter", 1).Message.Source.Scope
	for i := 0; i < 1002; i++ {
		r := domain.Reminder{ID: fmt.Sprintf("filter-%04d", i), Scope: scope, CreatedBy: "alice", Status: "sent", DueAt: time.Now().Add(time.Duration(i) * time.Minute), NotifyUntil: time.Now().Add(time.Hour)}
		if i == 0 {
			r.Status = "pending"
		}
		b, _ := json.Marshal(r)
		if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".reminders(scope_key,id,creator,status,body,due_at,updated_at)VALUES($1,$2,'alice',$3,$4,$5,clock_timestamp())`, scope.Key(), r.ID, r.Status, b, r.DueAt); err != nil {
			t.Fatal(err)
		}
	}
	items, err := w.ListReminders(ctx, scope, "pending", 1)
	if err != nil || len(items) != 1 || items[0].ID != "filter-0000" {
		t.Fatalf("status filtered after limit: %v %v", items, err)
	}
	scope.ChatID = "other-group"
	items, err = w.ListReminders(ctx, scope, "", 20)
	if err != nil || len(items) != 0 {
		t.Fatalf("cross-group reminders: %v %v", items, err)
	}
}

func TestReminderContributionSourceMutation(t *testing.T) {
	for _, mutation := range []string{"edit", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			f, w := workFixture(t)
			ctx := context.Background()
			root := event("review-root", 1)
			root.Message.Source.MessageID, root.Message.Source.AuthorID = "review-root", "alice"
			accept(t, f, root)
			task, err := f.CreateTask(ctx, root.Message, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			initial, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "review-root", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.CompleteJob(ctx, initial); err != nil {
				t.Fatal(err)
			}
			contribution := event("review-contribution", 1)
			contribution.Message.Source.MessageID, contribution.Message.Source.AuthorID = "review-contribution", "bob"
			contribution.Message.Text = "请在稍后提醒我休息"
			accept(t, f, contribution)
			task, err = f.Contribute(ctx, task, contribution.Message)
			if err != nil {
				t.Fatal(err)
			}
			j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "review-contributor", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			task, err = f.StartTurn(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
			if err != nil {
				t.Fatal(err)
			}
			p := pgstore.ToolPolicy{Name: "reminder_create", SideEffect: true, AutoExecute: true, Digest: "review-policy"}
			if err = f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{p}); err != nil {
				t.Fatal(err)
			}
			in := domain.ReminderInput{Text: "该休息了", DueAt: time.Now().Add(time.Second)}
			raw, _ := json.Marshal(in)
			x, _, err := f.BeginTool(ctx, j, task, p.Name, string(raw), "", true, p.Digest)
			if err != nil {
				t.Fatal(err)
			}
			r, err := w.CreateReminder(ctx, j, task, x.Key, p.Digest, in)
			if err != nil {
				t.Fatal(err)
			}
			if r.CreatedBy != "bob" {
				t.Fatalf("unexpected owner: %s", r.CreatedBy)
			}
			changed := event("review-contribution-mutated", 2)
			changed.Message = contribution.Message.Clone()
			changed.Message.EventID = changed.ID
			changed.Message.Source.Version++
			changed.Message.UpdatedAt, changed.OccurredAt = time.Now().UTC(), time.Now().UTC()
			if mutation == "delete" {
				changed.Type, changed.Message.Deleted = domain.EventDelete, true
			} else {
				changed.Message.Text = "不要提醒我了"
			}
			accept(t, f, changed)
			time.Sleep(time.Until(r.DueAt) + 10*time.Millisecond)
			notify, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobReminder}, "review-notifier", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := w.PrepareReminder(ctx, notify)
			if !errors.Is(err, domain.ErrStaleSource) {
				t.Fatalf("mutated creator request still authorizes notification: owner=%s origin=%s status=%s error=%v", r.CreatedBy, r.Origin.MessageID, prepared.Status, err)
			}
		})
	}
}
