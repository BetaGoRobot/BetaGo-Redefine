package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestScheduledReminderRetriesSameUUIDAfterOriginalTaskExpiry(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "schedule-request", AuthorID: "member", Version: 1, OccurredAt: now}, EventID: "schedule-request", Text: "稍后提醒我喝水", UpdatedAt: now}
	if _, err := f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	task, err := f.store.CreateTask(ctx, m, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "schedule-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.store.StartTurn(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	policy := pgstore.ToolPolicy{Name: "reminder_create", SideEffect: true, AutoExecute: true, Digest: "schedule-policy"}
	if err = f.store.SyncToolPolicies(ctx, []pgstore.ToolPolicy{policy}); err != nil {
		t.Fatal(err)
	}
	input := domain.ReminderInput{Text: "喝口水", DueAt: time.Now().Add(time.Second)}
	raw, _ := json.Marshal(input)
	x, _, err := f.store.BeginTool(ctx, j, task, policy.Name, string(raw), "", true, policy.Digest)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.store.CreateReminder(ctx, j, task, x.Key, policy.Digest, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".tasks SET expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(r.DueAt) + 10*time.Millisecond)
	f.messenger.failFinal = true
	if err = f.svc.Step(ctx, domain.JobReminder); err == nil {
		t.Fatal("injected ambiguous transport did not fail")
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at=clock_timestamp() WHERE kind='reminder'`); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.Step(ctx, domain.JobReminder); err != nil {
		t.Fatal(err)
	}
	if len(f.messenger.sends) != 2 || f.messenger.sends[0].uuid != r.UUID || f.messenger.sends[1].uuid != r.UUID {
		t.Fatalf("retry changed UUID: %v", f.messenger.sends)
	}
	rows, err := f.store.ListReminders(ctx, f.scope, "sent", 20)
	if err != nil || len(rows) != 1 || rows[0].MessageID != "out_"+r.UUID {
		t.Fatalf("notification receipt not durable: %v %v", rows, err)
	}
}
