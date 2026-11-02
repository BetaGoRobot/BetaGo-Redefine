package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func batchTask(t *testing.T, f fixture) (domain.Task, domain.Job) {
	t.Helper()
	root := event("batch_root", 1)
	root.Message.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user"})
	accept(t, f, root)
	task, err := f.CreateTask(context.Background(), root.Message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(context.Background(), []domain.JobKind{domain.JobAgentTurn}, "turn", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.StartTurn(context.Background(), j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	return task, j
}
func batchPlan(task domain.Task) domain.ConversationPlan {
	return domain.ConversationPlan{Choice: "expand", Messages: []domain.ConversationMessage{{Text: "第一句", ReplyToMessageID: task.Root.MessageID}, {Text: "还有一句", ReplyToMessageID: task.Root.MessageID}}}
}
func deliveryJob(t *testing.T, f fixture) (domain.Job, domain.Delivery) {
	t.Helper()
	j, e := f.ClaimJob(context.Background(), []domain.JobKind{domain.JobDelivery}, "sender", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	var p struct {
		ID string `json:"delivery_id"`
	}
	if json.Unmarshal(j.Payload, &p) != nil {
		t.Fatal("invalid job")
	}
	d, e := f.Delivery(context.Background(), j.Scope, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	return j, d
}
func TestConversationBatchIsFrozenOrderedAndCompletesAfterEveryReceipt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, j := batchTask(t, f)
	plan := batchPlan(task)
	if err := f.QueueConversationPlan(ctx, j, task, plan); err != nil {
		t.Fatal(err)
	}
	if err := f.QueueConversationPlan(ctx, j, task, plan); err != nil {
		t.Fatalf("identical batch not idempotent: %v", err)
	}
	changed := batchPlan(task)
	changed.Messages[1].Text = "changed"
	if err := f.QueueConversationPlan(ctx, j, task, changed); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("batch mutated: %v", err)
	}
	j1, d1 := deliveryJob(t, f)
	j2, d2 := deliveryJob(t, f)
	if d1.MessageType != "interactive" || d2.UpdateOf != d1.ID || d1.UUID == d2.UUID || d2.UUID != domain.StableUUID(task.Scope.Key(), d2.ID, "update") || d1.Sequence != 0 || d2.Sequence != 1 || d2.PreviousID != d1.ID || d1.BatchSize != 2 {
		t.Fatalf("batch identities/order invalid: %+v %+v", d1, d2)
	}
	if _, e := f.CheckDelivery(ctx, j2, d2.ID); !errors.Is(e, domain.ErrDeliveryPending) {
		t.Fatalf("second message can overtake first: %v", e)
	}
	if _, e := f.CheckDelivery(ctx, j1, d1.ID); e != nil {
		t.Fatal(e)
	}
	if e := f.FinishDelivery(ctx, j1, d1, "receipt_one"); e != nil {
		t.Fatal(e)
	}
	after, e := f.Task(ctx, task.Scope, task.ID)
	if e != nil || after.Status != "ready_delivery" {
		t.Fatalf("partial batch completed task: %+v %v", after, e)
	}
	if _, e := f.CheckDelivery(ctx, j2, d2.ID); e != nil {
		t.Fatal(e)
	}
	if e := f.FinishDelivery(ctx, j2, d2, "receipt_one"); e != nil {
		t.Fatal(e)
	}
	after, e = f.Task(ctx, task.Scope, task.ID)
	if e != nil || after.Status != "completed" {
		t.Fatalf("full batch incomplete: %+v %v", after, e)
	}
	var n int
	f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".deliveries`).Scan(&n)
	if n != 2 {
		t.Fatalf("duplicate or partial freeze: %d", n)
	}
}
func TestObservationCompletesTaskAndTurnWithoutDelivery(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, j := batchTask(t, f)
	if e := f.QueueConversationPlan(ctx, j, task, domain.ConversationPlan{Choice: "observe", Messages: []domain.ConversationMessage{}}); e != nil {
		t.Fatal(e)
	}
	after, e := f.Task(ctx, task.Scope, task.ID)
	if e != nil || after.Status != "completed" || after.ActiveJobID != 0 {
		t.Fatalf("observation stranded task: %+v %v", after, e)
	}
	if _, e = f.ClaimJob(ctx, []domain.JobKind{domain.JobDelivery}, "sender", time.Minute); !errors.Is(e, domain.ErrNotFound) {
		t.Fatalf("observation queued delivery: %v", e)
	}
	var status string
	if e = f.db.QueryRow(`SELECT status FROM "`+f.schema+`".turns WHERE task_id=$1`, task.ID).Scan(&status); e != nil || status != "observed" {
		t.Fatalf("observation turn not terminal: %s %v", status, e)
	}
}
func TestBatchTargetsMustBeVisibleSameGroupAndSourcePinned(t *testing.T) {
	for _, mode := range []string{"custom", "other_group", "visible", "changed"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			task, j := batchTask(t, f)
			scene := event("scene", 1)
			scene.Message.Source.MessageID = "scene"
			if mode == "other_group" {
				scene.Message.Source.Scope.ChatID = "other"
			}
			accept(t, f, scene)
			if mode == "visible" || mode == "changed" {
				var err error
				task, err = f.SetTurnContext(ctx, j, task, []domain.SourceRef{scene.Message.Source})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "changed" {
				scene.ID = "scene_edit"
				scene.Message.EventID = scene.ID
				scene.Message.Source.Version++
				scene.Message.UpdatedAt = scene.Message.UpdatedAt.Add(time.Second)
				accept(t, f, scene)
			}
			p := batchPlan(task)
			for i := range p.Messages {
				p.Messages[i].ReplyToMessageID = "scene"
			}
			err := f.QueueConversationPlan(ctx, j, task, p)
			if mode == "visible" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("unseen/foreign/stale target accepted")
			}
			var n int
			f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".deliveries`).Scan(&n)
			if n != 0 {
				t.Fatal("rejected target left partial batch")
			}
		})
	}
}
func TestPartialBatchCancellationBlocksRemainingMessages(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, j := batchTask(t, f)
	if e := f.QueueConversationPlan(ctx, j, task, batchPlan(task)); e != nil {
		t.Fatal(e)
	}
	j1, d1 := deliveryJob(t, f)
	j2, d2 := deliveryJob(t, f)
	if e := f.FinishDelivery(ctx, j1, d1, "receipt_one"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.ControlTask(ctx, task.Scope, task.OwnerID, domain.ControlAction{Action: "cancel", TaskID: task.ID, Revision: task.Revision}, domain.SourceRef{}, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := f.CheckDelivery(ctx, j2, d2.ID); !errors.Is(e, domain.ErrConflict) {
		t.Fatalf("remaining message survived cancellation: %v", e)
	}
}

func TestBatchReclaimedLeaseReconcilesPriorReceiptBeforeAdvancing(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, j := batchTask(t, f)
	if err := f.QueueConversationPlan(ctx, j, task, batchPlan(task)); err != nil {
		t.Fatal(err)
	}
	old, d1 := deliveryJob(t, f)
	second, d2 := deliveryJob(t, f)
	if _, err := f.db.Exec(`UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.FinishDelivery(ctx, old, d1, "provider_known_receipt"); !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("stale sender advanced lifecycle: %v", err)
	}
	if _, err := f.CheckDelivery(ctx, second, d2.ID); !errors.Is(err, domain.ErrDeliveryPending) {
		t.Fatalf("second item bypassed unreconciled receipt: %v", err)
	}
	reclaimed, same := deliveryJob(t, f)
	if reclaimed.ID != old.ID || reclaimed.Fence <= old.Fence || same.UUID != d1.UUID {
		t.Fatal("reclaim changed frozen send identity")
	}
	reconciled, err := f.CheckDelivery(ctx, reclaimed, same.ID)
	if err != nil || reconciled.Status != "delivered" || reconciled.MessageID != "provider_known_receipt" {
		t.Fatalf("receipt failed reconciliation: %+v %v", reconciled, err)
	}
	if _, err = f.CheckDelivery(ctx, second, d2.ID); err != nil {
		t.Fatal(err)
	}
}
func TestPartialBatchSourceEditBlocksRemainingMessages(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, j := batchTask(t, f)
	scene := event("batch_scene", 1)
	scene.Message.Source.MessageID = "batch_scene"
	accept(t, f, scene)
	task, err := f.SetTurnContext(ctx, j, task, []domain.SourceRef{scene.Message.Source})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.QueueConversationPlan(ctx, j, task, batchPlan(task)); err != nil {
		t.Fatal(err)
	}
	j1, d1 := deliveryJob(t, f)
	j2, d2 := deliveryJob(t, f)
	if err = f.FinishDelivery(ctx, j1, d1, "first_confirmed"); err != nil {
		t.Fatal(err)
	}
	scene.ID = "batch_scene_edited"
	scene.Message.EventID = scene.ID
	scene.Message.Source.Version++
	scene.Message.UpdatedAt = scene.Message.UpdatedAt.Add(time.Second)
	accept(t, f, scene)
	if _, err = f.CheckDelivery(ctx, j2, d2.ID); !errors.Is(err, domain.ErrStaleSource) {
		t.Fatalf("remaining message survived edited evidence: %v", err)
	}
}

func TestConcurrentBatchFreezeProducesOneImmutableOrderedSet(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, j := batchTask(t, f)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; results <- f.QueueConversationPlan(ctx, j, task, batchPlan(task)) }()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM "`+f.schema+`".deliveries WHERE task_id=$1`, task.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("concurrent freeze duplicated/partially wrote: %d %v", count, err)
	}
}

func TestRefreshPreservesDirectOrAmbientInvocationWithoutChangingAuthority(t *testing.T) {
	for _, origin := range []string{"mention", "command", "ambient", "legacy_mention"} {
		t.Run(origin, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			root := event("invocation", 1)
			root.Message.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: origin == "mention" || origin == "legacy_mention"})
			if origin == "command" {
				root.Message.Text = "/bb 请解释一下"
			}
			accept(t, f, root)
			task, err := f.CreateTask(ctx, root.Message, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if origin == "legacy_mention" {
				task.InvocationOrigin = ""
				body, _ := json.Marshal(task)
				if _, err = f.db.Exec(`UPDATE "`+f.schema+`".tasks SET body=$1 WHERE task_id=$2`, body, task.ID); err != nil {
					t.Fatal(err)
				}
			}
			root.ID = "invocation_refreshed"
			root.Message.EventID = root.ID
			root.Message.Source.Version++
			root.Message.UpdatedAt = root.Message.UpdatedAt.Add(time.Second)
			root.Message.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "refresh", SenderType: "user"})
			accept(t, f, root)
			after, err := f.CreateTask(ctx, root.Message, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			want := domain.InvocationDirect
			if origin == "ambient" {
				want = domain.InvocationAmbient
			}
			if after.InvocationOrigin != want || after.OwnerID != task.OwnerID || after.RequesterID != task.RequesterID || after.Revision != 2 {
				t.Fatalf("refresh changed social origin/authority: origin=%s owner=%s revision=%d", after.InvocationOrigin, after.OwnerID, after.Revision)
			}
			approvals, err := f.Approvals(ctx, after.Scope, after.ID)
			if err != nil || len(approvals) != 0 {
				t.Fatal("social invitation produced action authority")
			}
		})
	}
}

func TestCardUpdateCannotChangeInitialMessageIdentity(t *testing.T) {
	for _, mutation := range []string{"scope", "task", "revision", "approval", "type", "pending", "batch", "not_initial", "initial_nonshare", "initial_missing_config", "initial_malformed", "next_nonshare"} {
		t.Run(mutation, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			task, j := batchTask(t, f)
			if err := f.QueueConversationPlan(ctx, j, task, batchPlan(task)); err != nil {
				t.Fatal(err)
			}
			j1, d1 := deliveryJob(t, f)
			j2, d2 := deliveryJob(t, f)
			if err := f.FinishDelivery(ctx, j1, d1, "same-provider-card"); err != nil {
				t.Fatal(err)
			}
			initial, err := f.Delivery(ctx, task.Scope, d1.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "scope":
				initial.Scope.ChatID = "foreign"
			case "task":
				initial.TaskID = "foreign-task"
			case "revision":
				initial.Revision++
			case "approval":
				initial.Kind = "approval"
				initial.ApprovalID = "approval"
			case "type":
				initial.MessageType = "text"
			case "pending":
				initial.Status = "pending"
			case "batch":
				initial.BatchID = "other-batch"
			case "not_initial":
				initial.UpdateOf = "another-initial"
			case "initial_nonshare":
				initial.Content = `{"schema":"2.0","config":{"update_multi":false},"body":{"elements":[]}}`
			case "initial_missing_config":
				initial.Content = `{"schema":"2.0","body":{"elements":[]}}`
			case "initial_malformed":
				initial.Content = `not json`
			case "next_nonshare":
				initial = d2
				initial.Content = `{"schema":"2.0","config":{"update_multi":false},"body":{"elements":[]}}`
			}
			body, _ := json.Marshal(initial)
			if _, err = f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET body=$1 WHERE delivery_id=$2`, body, initial.ID); err != nil {
				t.Fatal(err)
			}
			want := domain.ErrConflict
			if mutation == "pending" {
				want = domain.ErrDeliveryPending
			}
			if _, err = f.CheckDelivery(ctx, j2, d2.ID); !errors.Is(err, want) {
				t.Fatalf("card mutation %s did not block: %v", mutation, err)
			}
		})
	}
}

func TestCardUpdateReceiptMustKeepInitialProviderMessageID(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, j := batchTask(t, f)
	if err := f.QueueConversationPlan(ctx, j, task, batchPlan(task)); err != nil {
		t.Fatal(err)
	}
	j1, d1 := deliveryJob(t, f)
	j2, d2 := deliveryJob(t, f)
	if err := f.FinishDelivery(ctx, j1, d1, "same-card"); err != nil {
		t.Fatal(err)
	}
	if err := f.FinishDelivery(ctx, j2, d2, "different-card"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("update forked message identity: %v", err)
	}
	pending, err := f.Delivery(ctx, task.Scope, d2.ID)
	if err != nil || pending.Status != "pending" || pending.MessageID != "" {
		t.Fatal("rejected fork stored a receipt")
	}
}

func TestCardUpdateTaskRevisionBlocksRemainingFrames(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, j := batchTask(t, f)
	if err := f.QueueConversationPlan(ctx, j, task, batchPlan(task)); err != nil {
		t.Fatal(err)
	}
	j1, d1 := deliveryJob(t, f)
	j2, d2 := deliveryJob(t, f)
	if err := f.FinishDelivery(ctx, j1, d1, "same-card"); err != nil {
		t.Fatal(err)
	}
	next := event("followup_revision", 1)
	next.Message.Source.MessageID = "followup_revision"
	next.Message.Source.AuthorID = task.OwnerID
	next.Message.Source.OccurredAt = time.Now().UTC()
	next.Message.UpdatedAt = next.Message.Source.OccurredAt
	accept(t, f, next)
	if _, err := f.ControlTask(ctx, task.Scope, task.OwnerID, domain.ControlAction{Action: "modify", TaskID: task.ID, Revision: task.Revision}, next.Message.Source, "continue"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CheckDelivery(ctx, j2, d2.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("card frame survived task revision: %v", err)
	}
}
