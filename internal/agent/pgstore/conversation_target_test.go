package pgstore_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func visibleReplyTargets(t *testing.T, f fixture) (domain.Task, domain.Job, string) {
	t.Helper()
	task, job := batchTask(t, f)
	scene := event("visible_reply_target", 1)
	scene.Message.Source.MessageID = "visible_reply_target"
	accept(t, f, scene)
	task, err := f.SetTurnContext(context.Background(), job, task, []domain.SourceRef{scene.Message.Source})
	if err != nil {
		t.Fatal(err)
	}
	return task, job, scene.Message.Source.MessageID
}

func TestConversationPlanRejectsMixedVisibleReplyTargetsAtomically(t *testing.T) {
	for _, choice := range []string{"reply", "expand"} {
		t.Run(choice, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			task, job, sceneID := visibleReplyTargets(t, f)
			before, err := f.Task(ctx, task.Scope, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			plan := batchPlan(task)
			plan.Choice = choice
			plan.Messages[1].ReplyToMessageID = sceneID
			if err = f.QueueConversationPlan(ctx, job, task, plan); !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("mixed individually visible targets must be invalid: %v", err)
			}
			after, err := f.Task(ctx, task.Scope, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Errorf("rejected mixed targets mutated task: status=%s final=%v active_job=%d", after.Status, after.FinalConversation != nil, after.ActiveJobID)
			}
			for _, table := range []string{"deliveries", "jobs"} {
				query := `SELECT count(*) FROM "` + f.schema + `".` + table
				if table == "jobs" {
					query += ` WHERE kind='delivery'`
				}
				var count int
				if err = f.db.QueryRow(query).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Errorf("rejected mixed targets left %d %s", count, table)
				}
			}
			var turnStatus string
			if err = f.db.QueryRow(`SELECT status FROM "`+f.schema+`".turns WHERE task_id=$1 AND job_id=$2`, task.ID, job.ID).Scan(&turnStatus); err != nil || turnStatus != "running" {
				t.Errorf("rejected mixed targets finalized turn: status=%s err=%v", turnStatus, err)
			}
		})
	}
}

func TestConversationPlanUsesSelectedReplyTargetForEveryCardFrame(t *testing.T) {
	for _, target := range []string{"root", "scene"} {
		t.Run(target, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			task, job, sceneID := visibleReplyTargets(t, f)
			targetID := task.Root.MessageID
			if target == "scene" {
				targetID = sceneID
			}
			plan := batchPlan(task)
			for i := range plan.Messages {
				plan.Messages[i].ReplyToMessageID = targetID
			}
			if err := f.QueueConversationPlan(ctx, job, task, plan); err != nil {
				t.Fatal(err)
			}
			firstJob, first := deliveryJob(t, f)
			updateJob, update := deliveryJob(t, f)
			if first.ParentID != targetID || update.ParentID != targetID || first.MessageType != "interactive" || update.UpdateOf != first.ID || update.PreviousID != first.ID {
				t.Fatalf("card frames lost selected target or one-card identity: first=%+v update=%+v", first, update)
			}
			if _, err := f.CheckDelivery(ctx, updateJob, update.ID); !errors.Is(err, domain.ErrDeliveryPending) {
				t.Fatalf("card update overtook initial send: %v", err)
			}
			if err := f.FinishDelivery(ctx, firstJob, first, "selected_target_card"); err != nil {
				t.Fatal(err)
			}
			checked, err := f.CheckDelivery(ctx, updateJob, update.ID)
			if err != nil || checked.MessageID != "selected_target_card" || checked.ParentID != targetID {
				t.Fatalf("card update resolved a different physical target: %+v %v", checked, err)
			}
			if err = f.FinishDelivery(ctx, updateJob, checked, "selected_target_card"); err != nil {
				t.Fatal(err)
			}
			after, err := f.Task(ctx, task.Scope, task.ID)
			if err != nil || after.Status != "completed" || !reflect.DeepEqual(after.FinalConversation, &plan) {
				t.Fatalf("same-target card did not preserve and finish plan: status=%s err=%v", after.Status, err)
			}
		})
	}
}
