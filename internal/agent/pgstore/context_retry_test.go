package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func retryContext(t *testing.T, f fixture, task domain.Task, j domain.Job) (domain.Task, domain.Event) {
	t.Helper()
	ctx := context.Background()
	history := event("context1", 1)
	history.Message.Source.MessageID = "history"
	accept(t, f, history)
	task, err := f.SetTurnContext(ctx, j, task, []domain.SourceRef{task.Root, history.Message.Source})
	if err != nil {
		t.Fatal(err)
	}
	history.ID = "context2"
	history.Message.EventID = history.ID
	history.Message.Source.Version = 2
	history.Message.UpdatedAt = history.Message.UpdatedAt.Add(time.Second)
	history.OccurredAt = history.Message.UpdatedAt
	history.Message.Text = "edited"
	return task, history
}
func TestContextReplanRetainsClaimRequestAndFrozenActionGuards(t *testing.T) {
	for _, mode := range []string{"unchanged", "request_changed", "canceled", "claim_lost", "proposal", "frozen_delivery", "unknown_effect"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			task, j := batchTask(t, f)
			task, history := retryContext(t, f, task, j)
			if mode != "unchanged" {
				accept(t, f, history)
			}
			switch mode {
			case "request_changed":
				e := event("root_edit", 2)
				accept(t, f, e)
			case "canceled":
				// Restore context while issuing a valid cancellation; then stale it.
				_, err := f.ControlTask(ctx, task.Scope, task.OwnerID, domain.ControlAction{Action: "cancel", TaskID: task.ID, Revision: task.Revision}, task.Root, "")
				if err != nil {
					t.Fatal(err)
				}
			case "claim_lost":
				if _, err := f.db.Exec(`UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
					t.Fatal(err)
				}
			case "proposal":
				if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".approvals(scope_key,task_id,approval_id,revision,body) VALUES($1,$2,'prior',1,$3)`, task.Scope.Key(), task.ID, []byte(`{"Status":"revoked"}`)); err != nil {
					t.Fatal(err)
				}
			case "frozen_delivery":
				if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".deliveries(scope_key,delivery_id,task_id,revision,status,body) VALUES($1,'frozen',$2,1,'pending',$3)`, task.Scope.Key(), task.ID, []byte(`{}`)); err != nil {
					t.Fatal(err)
				}
			case "unknown_effect":
				if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".tool_executions(scope_key,task_id,execution_key,status,body) VALUES($1,$2,'unknown','unknown',$3)`, task.Scope.Key(), task.ID, []byte(`{}`)); err != nil {
					t.Fatal(err)
				}
			}
			ok, err := f.ReplanStaleContext(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
			if ok {
				t.Fatalf("%s can replay a fenced or frozen action", mode)
			}
			if mode == "claim_lost" && err == nil {
				t.Fatal("lost claim accepted")
			}
			current, e := f.Task(ctx, task.Scope, task.ID)
			if e != nil || current.Revision != task.Revision {
				t.Fatal("rejected recovery changed task revision")
			}
		})
	}
}
func TestContextReplanIsBoundedAndCannotReplayApprovalResume(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, j := batchTask(t, f)
	task, history := retryContext(t, f, task, j)
	accept(t, f, history)
	if ok, err := f.ReplanStaleContext(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision, ApprovalID: "approved"}); err != nil || ok {
		t.Fatal("approval resume automatically rebased")
	}
	for attempt := 0; attempt < 3; attempt++ {
		ok, err := f.ReplanStaleContext(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
		if err != nil || ok != (attempt < 2) {
			t.Fatalf("unbounded/wrong retry: attempt=%d ok=%v err=%v", attempt, ok, err)
		}
		if !ok {
			break
		}
		if err = f.CompleteJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		j, err = f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "retry", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		task, err = f.StartTurn(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision + 1})
		if err != nil {
			t.Fatal(err)
		}
		task, err = f.SetTurnContext(ctx, j, task, []domain.SourceRef{task.Root, history.Message.Source})
		if err != nil {
			t.Fatal(err)
		}
		history.ID += "n"
		history.Message.EventID = history.ID
		history.Message.Source.Version++
		history.Message.UpdatedAt = history.Message.UpdatedAt.Add(time.Second)
		history.OccurredAt = history.Message.UpdatedAt
		accept(t, f, history)
	}
}
