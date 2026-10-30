package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestAttentionUsesActualNotificationsAndExcludesForeignOrTerminalWork(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Second)
	root := continuityHuman(t, f, "card-root", "alice", "message", at)
	task, err := f.CreateTask(ctx, root, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "attention", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.StartTurn(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.ConversationPlan{Choice: "expand", Messages: []domain.ConversationMessage{{Text: strings.Repeat("完整段落。", 70), ReplyToMessageID: root.Source.MessageID}, {Text: "补充一个边界。", ReplyToMessageID: root.Source.MessageID}}}
	if err = f.QueueConversationPlan(ctx, j, task, plan); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		dj, d := deliveryJob(t, f)
		if _, err = f.CheckDelivery(ctx, dj, d.ID); err != nil {
			t.Fatal(err)
		}
		if err = f.FinishDelivery(ctx, dj, d, "same-visible-card"); err != nil {
			t.Fatal(err)
		}
		if err = f.CompleteJob(ctx, dj); err != nil {
			t.Fatal(err)
		}
	}

	current := continuityHuman(t, f, "current", "alice", "message", time.Now().UTC().Add(-time.Millisecond))
	curTask, err := f.CreateTask(ctx, current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	makePending := func(id string, foreign bool) domain.Task {
		t.Helper()
		e := event("event-"+id, 1)
		e.Message.Source.MessageID = id
		e.Message.Source.OccurredAt = at
		e.Message.UpdatedAt = at
		e.OccurredAt = at
		e.Message.Raw = json.RawMessage(`{"kind":"message","sender_type":"user"}`)
		if foreign {
			e.Message.Source.Scope.ChatID = "foreign-group"
		}
		accept(t, f, e)
		task, err := f.CreateTask(ctx, e.Message, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	makePending("foreign", true)
	failed := makePending("failed", false)
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET status='failed' WHERE kind='agent_turn' AND convert_from(payload,'UTF8')::jsonb->>'task_id'=$1`, failed.ID); err != nil {
		t.Fatal(err)
	}
	live := makePending("live", false)
	a, err := f.ConversationActivity(ctx, curTask, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if a.OwnNotifications != 1 || a.OwnReplyTurns != 1 || a.HumanMessages != 4 || a.HumanSpeakers != 2 {
		t.Fatalf("physical notifications or group boundary wrong: %+v", a)
	}
	if len(a.InFlight) != 1 || a.InFlight[0].Root != live.Root || a.InFlight[0].Phase != "queued" {
		t.Fatalf("nonlive/foreign request leaked as progress: %+v", a.InFlight)
	}

	if _, err = f.ControlTask(ctx, live.Scope, live.OwnerID, domain.ControlAction{Action: "cancel", TaskID: live.ID, Revision: live.Revision}, domain.SourceRef{}, ""); err != nil {
		t.Fatal(err)
	}
	a, err = f.ConversationActivity(ctx, curTask, time.Now().UTC())
	if err != nil || len(a.InFlight) != 0 {
		t.Fatalf("canceled work remained in flight: %+v %v", a, err)
	}
}

func TestAttentionRejectsInvalidOrCanceledContext(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := continuityHuman(t, f, "current", "alice", "message", time.Now().UTC().Add(-time.Second))
	task, err := f.CreateTask(ctx, root, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	bad := task
	bad.Root.Scope.ChatID = "foreign"
	if _, err = f.ConversationActivity(ctx, bad, time.Now()); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("mixed source scope accepted: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = f.ConversationActivity(canceled, task, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestAttentionCardEditIsNotNewNotification(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := continuityHuman(t, f, "old-card-root", "alice", "message", time.Now().UTC().Add(-5*time.Minute))
	task, err := f.CreateTask(ctx, root, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "review-card", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.StartTurn(ctx, job, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.ConversationPlan{Choice: "expand", Messages: []domain.ConversationMessage{
		{Text: strings.Repeat("完整段落。", 70), ReplyToMessageID: root.Source.MessageID},
		{Text: "补充一个边界。", ReplyToMessageID: root.Source.MessageID},
	}}
	if err = f.QueueConversationPlan(ctx, job, task, plan); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		dj, d := deliveryJob(t, f)
		if _, err = f.CheckDelivery(ctx, dj, d.ID); err != nil {
			t.Fatal(err)
		}
		if err = f.FinishDelivery(ctx, dj, d, "same-card-receipt"); err != nil {
			t.Fatal(err)
		}
		if err = f.CompleteJob(ctx, dj); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			d, err = f.Delivery(ctx, d.Scope, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			d.CreatedAt, d.UpdatedAt = time.Now().UTC().Add(-3*time.Minute), time.Now().UTC().Add(-3*time.Minute)
			raw := attentionDeliveryClocks(t, d, d.UpdatedAt, d.UpdatedAt)
			if _, err = f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET body=$1 WHERE delivery_id=$2`, raw, d.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	current := continuityHuman(t, f, "now", "alice", "message", time.Now().UTC().Add(-time.Millisecond))
	curTask, err := f.CreateTask(ctx, current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.ConversationActivity(ctx, curTask, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if a.OwnNotifications != 0 {
		t.Fatalf("same card was created outside window; only a card edit occurred inside window: got OwnNotifications=%d want 0", a.OwnNotifications)
	}
}

func TestAttentionExhaustedLeaseIsNotQueuedWork(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := continuityHuman(t, f, "exhausted-root", "alice", "message", time.Now().UTC().Add(-time.Second))
	task, err := f.CreateTask(ctx, root, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "review-exhausted", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.StartTurn(ctx, job, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET attempt=max_attempts,lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	current := continuityHuman(t, f, "current-exhausted", "alice", "message", time.Now().UTC().Add(-time.Millisecond))
	currentTask, err := f.CreateTask(ctx, current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.ConversationActivity(ctx, currentTask, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(a.InFlight) != 0 {
		t.Fatalf("expired final lease cannot be reclaimed, but is reported as %+v", a.InFlight)
	}
}

// Only delivery clocks vary; task/source snapshots and actual provider receipts
// come from the real workflow. A map permits testing old records without the
// new durable timestamp, rather than silently backfilling their queued time.
func attentionDeliveryClocks(t *testing.T, d domain.Delivery, updated, confirmed time.Time) []byte {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["UpdatedAt"] = updated
	if confirmed.IsZero() {
		delete(fields, "ConfirmedAt")
	} else {
		fields["ConfirmedAt"] = confirmed
	}
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAttentionInFlightRequiresLiveOrRecoverableWork(t *testing.T) {
	for _, tc := range []struct {
		name, status           string
		exhausted, activeLease bool
		want                   string
	}{
		{"pending exhausted", "pending", true, false, ""},
		{"running exhausted expired", "running", true, false, ""},
		{"running final attempt active", "running", true, true, "preparing"},
		{"running recoverable", "running", false, false, "queued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			root := continuityHuman(t, f, "job-root", "alice", "message", time.Now().UTC().Add(-time.Second))
			task, err := f.CreateTask(ctx, root, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			attempt, lease := 1, time.Now().Add(-time.Second)
			if tc.exhausted {
				attempt = 8
			}
			if tc.activeLease {
				lease = time.Now().Add(time.Minute)
			}
			if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET status=$1,attempt=$2,lease_until=$3 WHERE kind='agent_turn' AND convert_from(payload,'UTF8')::jsonb->>'task_id'=$4`, tc.status, attempt, lease, task.ID); err != nil {
				t.Fatal(err)
			}
			current := continuityHuman(t, f, "current-job", "alice", "message", time.Now().UTC().Add(-time.Millisecond))
			curTask, err := f.CreateTask(ctx, current, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			a, err := f.ConversationActivity(ctx, curTask, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" && len(a.InFlight) != 0 {
				t.Fatalf("unrecoverable work reported: %+v", a.InFlight)
			}
			if tc.want != "" && (len(a.InFlight) != 1 || a.InFlight[0].Phase != tc.want) {
				t.Fatalf("live/recoverable work missing: %+v want %q", a.InFlight, tc.want)
			}
		})
	}
}

func TestAttentionUsesConfirmedReceiptTimeAndReportsLegacyUncertainty(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		confirmedAge, updatedAge time.Duration
		legacy                   bool
		want                     int
		complete                 bool
	}{
		{"recent send after old queue", time.Second, time.Second, false, 1, true},
		{"old send reconciled recently", 3 * time.Minute, time.Second, false, 0, true},
		{"recent legacy receipt unknown", 0, time.Second, true, 0, false},
		{"old legacy receipt outside window", 0, 3 * time.Minute, true, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			root := continuityHuman(t, f, "receipt-parent", "alice", "message", time.Now().UTC().Add(-time.Minute))
			continuityDelivered(t, f, root, nil)
			var raw []byte
			if err := f.db.QueryRow(`SELECT body FROM "` + f.schema + `".deliveries`).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var d domain.Delivery
			if err := json.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			d.CreatedAt = now.Add(-5 * time.Minute) // queued time is never send time
			confirmed := now.Add(-tc.confirmedAge)
			if tc.legacy {
				confirmed = time.Time{}
			}
			raw = attentionDeliveryClocks(t, d, now.Add(-tc.updatedAge), confirmed)
			if _, err := f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET body=$1 WHERE delivery_id=$2`, raw, d.ID); err != nil {
				t.Fatal(err)
			}
			current := continuityHuman(t, f, "receipt-current", "alice", "message", now.Add(-time.Millisecond))
			task, err := f.CreateTask(ctx, current, now.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			a, err := f.ConversationActivity(ctx, task, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			raw, err = json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			var counts struct {
				OwnNotifications, OwnReplyTurns int
				NotificationsComplete           bool
			}
			if err = json.Unmarshal(raw, &counts); err != nil {
				t.Fatal(err)
			}
			if counts.OwnNotifications != tc.want || counts.OwnReplyTurns != tc.want || counts.NotificationsComplete != tc.complete {
				t.Fatalf("receipt clock/legacy accuracy lost: got %+v want count=%d complete=%v", counts, tc.want, tc.complete)
			}
		})
	}
}

func TestAttentionLateReceiptReconciliationKeepsFirstConfirmation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, turn := batchTask(t, f)
	if err := f.QueueFinalDelivery(ctx, turn, task, "已确认的回复"); err != nil {
		t.Fatal(err)
	}
	job, d := deliveryJob(t, f)
	if _, err := f.db.Exec(`UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.FinishDelivery(ctx, job, d, "late-physical-message"); !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("expected persisted late receipt, got %v", err)
	}
	d, err := f.Delivery(ctx, d.Scope, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	var receiptFields map[string]any
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &receiptFields); err != nil {
		t.Fatal(err)
	}
	if v, ok := receiptFields["ConfirmedAt"].(string); !ok || v == "" || strings.HasPrefix(v, "0001-") {
		t.Fatalf("first known provider receipt has no durable confirmation time: %v", receiptFields["ConfirmedAt"])
	}
	first := time.Now().UTC().Add(-3 * time.Minute)
	raw = attentionDeliveryClocks(t, d, first, first)
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET body=$1 WHERE delivery_id=$2`, raw, d.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobDelivery}, "receipt-reconciliation", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := f.CheckDelivery(ctx, reclaimed, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(confirmed)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &receiptFields); err != nil {
		t.Fatal(err)
	}
	if receiptFields["ConfirmedAt"] != first.Format(time.RFC3339Nano) {
		t.Fatalf("lifecycle reconciliation rewrote receipt time: got %v want %s", receiptFields["ConfirmedAt"], first.Format(time.RFC3339Nano))
	}
	current := continuityHuman(t, f, "late-current", "alice", "message", time.Now().UTC().Add(-time.Millisecond))
	curTask, err := f.CreateTask(ctx, current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.ConversationActivity(ctx, curTask, time.Now().UTC())
	if err != nil || a.OwnNotifications != 0 || a.OwnReplyTurns != 0 {
		t.Fatalf("late reconciliation created notification: %+v %v", a, err)
	}
}

func TestAttentionExhaustedDeliveryIsNotInFlight(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, turn := batchTask(t, f)
	if err := f.QueueFinalDelivery(ctx, turn, task, "无法再次领取的交付"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE "` + f.schema + `".jobs SET status='running',attempt=max_attempts,lease_until=clock_timestamp()-interval '1 second' WHERE kind='delivery'`); err != nil {
		t.Fatal(err)
	}
	current := continuityHuman(t, f, "delivery-current", "alice", "message", time.Now().UTC().Add(-time.Millisecond))
	curTask, err := f.CreateTask(ctx, current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.ConversationActivity(ctx, curTask, time.Now().UTC())
	if err != nil || len(a.InFlight) != 0 {
		t.Fatalf("unrecoverable delivery reported as progress: %+v %v", a.InFlight, err)
	}
}

func TestAttentionTerminalRowsDoNotHideLiveWork(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	live := continuityHuman(t, f, "buried-live", "alice", "message", at)
	liveTask, err := f.CreateTask(ctx, live, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		m := continuityHuman(t, f, fmt.Sprintf("terminal-%d", i), "alice", "message", at)
		task, err := f.CreateTask(ctx, m, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET status='failed' WHERE kind='agent_turn' AND convert_from(payload,'UTF8')::jsonb->>'task_id'=$1`, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	current := continuityHuman(t, f, "buried-current", "alice", "message", time.Now().UTC().Add(-time.Millisecond))
	curTask, err := f.CreateTask(ctx, current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.ConversationActivity(ctx, curTask, time.Now().UTC())
	if err != nil || len(a.InFlight) != 1 || a.InFlight[0].Root != liveTask.Root || a.InFlightTruncated {
		t.Fatalf("terminal rows consumed the live work limit: %+v %v", a, err)
	}
}

func TestAttentionCountsOnlyRecentSameGroupConfirmedReactionActions(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := continuityHuman(t, f, "reaction-root", "alice", "message", time.Now().UTC().Add(-time.Minute))
	task, err := f.CreateTask(ctx, root, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "reaction", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	policy := automaticPolicy(t, "feishu_add_reaction")
	if err = f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{policy}); err != nil {
		t.Fatal(err)
	}
	x, reused, err := f.BeginTool(ctx, job, task, policy.Name, `{"message_id":"reaction-root","emoji":"THUMBSUP"}`, "", true, policy.Digest)
	if err != nil || reused {
		t.Fatalf("reaction not reserved: %+v %v %v", x, reused, err)
	}
	if err = f.FinishTool(ctx, job, task, x, `{"reaction":{"ID":"own","Own":true}}`, nil); err != nil {
		t.Fatal(err)
	}
	// Idempotent receipt reuse must not add another action to attention.
	if _, reused, err = f.BeginTool(ctx, job, task, policy.Name, x.Arguments, "", true, policy.Digest); err != nil || !reused {
		t.Fatalf("completed reaction not reused: %v %v", reused, err)
	}
	now := time.Now().UTC()
	x, err = f.ToolExecution(ctx, task.Scope, task.ID, x.Key)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, status, tool string
		at                time.Time
		foreign           bool
	}{
		{"old", "completed", policy.Name, now.Add(-3 * time.Minute), false},
		{"future", "completed", policy.Name, now.Add(time.Minute), false},
		{"unknown", "unknown", policy.Name, now, false},
		{"failed", "failed_read", policy.Name, now, false},
		{"running", "running_effect", policy.Name, now, false},
		{"other-tool", "completed", "feishu_remove_reaction", now, false},
		{"foreign", "completed", policy.Name, now, true},
		{"confirmed-late", "completed_late", policy.Name, now, false},
	} {
		copy := x
		copy.Key, copy.Status, copy.Tool, copy.UpdatedAt = tc.key, tc.status, tc.tool, tc.at
		if tc.foreign {
			copy.Scope.ChatID = "another-group"
		}
		raw, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec(`INSERT INTO "`+f.schema+`".tool_executions(scope_key,task_id,execution_key,status,body) VALUES($1,$2,$3,$4,$5)`, copy.Scope.Key(), copy.TaskID, copy.Key, copy.Status, raw); err != nil {
			t.Fatal(err)
		}
	}
	current := continuityHuman(t, f, "reaction-current", "alice", "message", now.Add(-time.Millisecond))
	curTask, err := f.CreateTask(ctx, current, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.ConversationActivity(ctx, curTask, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var counts struct{ OwnReactions int }
	if err = json.Unmarshal(raw, &counts); err != nil {
		t.Fatal(err)
	}
	if counts.OwnReactions != 2 {
		t.Fatalf("reaction receipts/time/group/status boundary wrong: got %+v want 2 completed actions", counts)
	}
	if a.OwnNotifications != 0 || a.OwnReplyTurns != 0 {
		t.Fatalf("reaction counted as message notification or reply turn: %+v", a)
	}
}
