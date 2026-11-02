package pgstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func continuityHuman(t *testing.T, f fixture, id, author, kind string, at time.Time) domain.Message {
	t.Helper()
	e := event(id, 1)
	e.Message.Source.MessageID, e.Message.Source.AuthorID = id, author
	e.Message.Source.OccurredAt, e.Message.UpdatedAt, e.OccurredAt = at, at, at
	e.Message.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: kind, SenderType: "user"})
	accept(t, f, e)
	return e.Message
}

func continuityDelivered(t *testing.T, f fixture, root domain.Message, refs []domain.SourceRef) {
	t.Helper()
	ctx := context.Background()
	task, err := f.CreateTask(ctx, root, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "continuity-turn", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.StartTurn(ctx, job, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) > 0 {
		task, err = f.SetTurnContext(ctx, job, task, refs)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = f.QueueConversationPlan(ctx, job, task, domain.ConversationPlan{Choice: "reply", Messages: []domain.ConversationMessage{{Text: "已确认的回复", ReplyToMessageID: root.Source.MessageID}}}); err != nil {
		t.Fatal(err)
	}
	deliveryJob, delivery := deliveryJob(t, f)
	if _, err = f.CheckDelivery(ctx, deliveryJob, delivery.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.FinishDelivery(ctx, deliveryJob, delivery, "receipt-"+root.Source.MessageID); err != nil {
		t.Fatal(err)
	}
}

func TestMemberRepliesFindRealParentBeforeGlobalLimits(t *testing.T) {
	for _, kind := range []string{"message", "refresh"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			at := time.Now().UTC().Add(-time.Hour)
			alice := continuityHuman(t, f, "alice-parent", "alice", kind, at)
			continuityDelivered(t, f, alice, nil)
			for i := range 16 {
				bob := continuityHuman(t, f, fmt.Sprintf("bob-%d", i), "bob", "message", at.Add(30*time.Minute+time.Duration(i)*time.Second))
				if i < 8 {
					continuityDelivered(t, f, bob, []domain.SourceRef{alice.Source})
				}
			}
			now := time.Now().UTC()
			global, err := f.RecentReplies(ctx, testScope(), now, 8)
			if err != nil || len(global) != 8 {
				t.Fatalf("global fixture replies: %d %v", len(global), err)
			}
			for _, r := range global {
				if r.ParentID == alice.Source.MessageID {
					t.Fatal("Alice was not displaced from global reply limit")
				}
			}
			recent, err := f.Recent(ctx, testScope(), now, 16)
			if err != nil || len(recent) != 16 {
				t.Fatalf("recent fixture messages: %d %v", len(recent), err)
			}
			for _, m := range recent {
				if m.Source.MessageID == alice.Source.MessageID {
					t.Fatal("Alice was not displaced from Recent limit")
				}
			}
			replies, err := f.RecentRepliesForMember(ctx, testScope(), "alice", now.Add(-5*time.Minute), now, 1)
			if err != nil || len(replies) != 1 || replies[0].ParentID != alice.Source.MessageID {
				t.Fatalf("member reply lost or contextual source treated as parent: %+v %v", replies, err)
			}
		})
	}
}

func TestMemberRepliesPreserveSourceInvalidation(t *testing.T) {
	for _, mutation := range []string{"edit", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			f := setup(t)
			at := time.Now().UTC().Add(-time.Minute)
			root := continuityHuman(t, f, "mutable-parent", "alice", "message", at)
			continuityDelivered(t, f, root, nil)
			e := event("parent-mutated", 2)
			e.Message = root.Clone()
			e.Message.EventID = e.ID
			e.Message.Source.Version++
			e.Message.UpdatedAt, e.OccurredAt = time.Now().UTC(), time.Now().UTC()
			if mutation == "delete" {
				e.Type, e.Message.Deleted = domain.EventDelete, true
			} else {
				e.Message.Text = "edited"
			}
			accept(t, f, e)
			now := time.Now().UTC()
			replies, err := f.RecentRepliesForMember(context.Background(), testScope(), "alice", now.Add(-5*time.Minute), now, 8)
			if err != nil || len(replies) != 0 {
				t.Fatalf("stale evidence revived after %s: %+v %v", mutation, replies, err)
			}
		})
	}
}

func TestMemberRepliesRequireActualHumanScopedParent(t *testing.T) {
	for _, scenario := range []string{"bot", "control", "empty_metadata", "other_scope"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			at := time.Now().UTC().Add(-time.Minute)
			e := event("parent", 1)
			e.Message.Source.MessageID, e.Message.Source.AuthorID = "parent", "alice"
			e.Message.Source.OccurredAt, e.Message.UpdatedAt, e.OccurredAt = at, at, at
			e.Message.Raw = json.RawMessage(`{"kind":"message","sender_type":"user"}`)
			switch scenario {
			case "bot":
				e.Message.Raw = json.RawMessage(`{"kind":"message","sender_type":"app"}`)
			case "control":
				e.Message.Raw = json.RawMessage(`{"kind":"control","sender_type":"user"}`)
			case "empty_metadata":
				e.Message.Raw = nil
			}
			accept(t, f, e)
			continuityDelivered(t, f, e.Message, nil)
			scope := testScope()
			if scenario == "other_scope" {
				scope.ChatID = "different-chat"
			}
			now := time.Now().UTC()
			replies, err := f.RecentRepliesForMember(context.Background(), scope, "alice", now.Add(-5*time.Minute), now, 8)
			if err != nil || len(replies) != 0 {
				t.Fatalf("nonhuman or cross-group parent selected: count=%d err=%v", len(replies), err)
			}
		})
	}
}

func TestMemberRepliesHonorDeliveryWindow(t *testing.T) {
	f := setup(t)
	root := continuityHuman(t, f, "older-question", "alice", "message", time.Now().UTC().Add(-time.Hour))
	continuityDelivered(t, f, root, nil)
	since := time.Now().UTC()
	replies, err := f.RecentRepliesForMember(context.Background(), testScope(), "alice", since, since.Add(time.Second), 8)
	if err != nil || len(replies) != 0 {
		t.Fatalf("delivery before lower bound selected: count=%d err=%v", len(replies), err)
	}
}

func TestMemberRepliesApplyExactNanosecondBoundsAndOrderBeforeLimit(t *testing.T) {
	for _, scenario := range []string{"future_mask", "expired_mask", "exact_order", "offset_order", "zero_width_whole_second"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			anchor := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond).Add(123 * time.Nanosecond)
			if scenario == "zero_width_whole_second" {
				anchor = anchor.Truncate(time.Second)
			}
			since := anchor.Add(-5 * time.Minute)
			if scenario == "zero_width_whole_second" {
				since = anchor
			}
			times := make([]time.Time, 9)
			for i := range times {
				root := continuityHuman(t, f, fmt.Sprintf("nano-parent-%d", i), "alice", "message", since.Add(-time.Hour))
				continuityDelivered(t, f, root, nil)
				switch scenario {
				case "future_mask":
					times[i] = anchor.Add(time.Nanosecond)
					if i == 0 {
						times[i] = anchor.Add(-time.Nanosecond)
					}
				case "expired_mask":
					times[i] = since.Add(-time.Nanosecond)
					if i == 0 {
						times[i] = since.Add(time.Nanosecond)
					}
				case "zero_width_whole_second":
					times[i] = anchor.Add(-time.Duration(i) * time.Nanosecond)
				default:
					times[i] = anchor.Add(-time.Duration(i+1) * time.Nanosecond)
					if scenario == "offset_order" {
						times[i] = times[i].In(time.FixedZone("fixture", 8*60*60))
					}
				}
			}
			deliveries := continuityRetimeDeliveries(t, f, times)
			for _, limit := range []int{1, 8, 64} {
				replies, err := f.RecentRepliesForMember(context.Background(), testScope(), "alice", since, anchor, limit)
				want := 1
				if scenario == "exact_order" || scenario == "offset_order" {
					want = min(limit, len(deliveries))
				}
				if err != nil || len(replies) != want {
					t.Fatalf("exact bounds before LIMIT %d: count=%d want=%d err=%v", limit, len(replies), want, err)
				}
				for i, reply := range replies {
					if reply.MessageID != deliveries[i].MessageID || !reply.OccurredAt.Equal(times[i]) {
						t.Fatalf("exact order before LIMIT %d at %d: got=%s/%s want=%s/%s", limit, i, reply.MessageID, reply.OccurredAt, deliveries[i].MessageID, times[i])
					}
				}
			}
		})
	}
}

// Preserve real workflow source snapshots and receipts, varying only the
// serialized delivery clock to exercise nanoseconds that PostgreSQL rounds.
func continuityRetimeDeliveries(t *testing.T, f fixture, times []time.Time) []domain.Delivery {
	t.Helper()
	rows, err := f.db.Query(`SELECT body FROM "` + f.schema + `".deliveries ORDER BY delivery_id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	var deliveries []domain.Delivery
	for rows.Next() {
		var body []byte
		var delivery domain.Delivery
		if err = rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(body, &delivery); err != nil {
			t.Fatal(err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(deliveries) != len(times) {
		t.Fatalf("retime fixture: deliveries=%d times=%d", len(deliveries), len(times))
	}
	for i := range deliveries {
		deliveries[i].UpdatedAt = times[i]
		body, err := json.Marshal(deliveries[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET body=$1 WHERE scope_key=$2 AND delivery_id=$3`, body, deliveries[i].Scope.Key(), deliveries[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	return deliveries
}

func TestSameCardFramesAreOneLatestConfirmedUtteranceBeforeLimits(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	older := continuityHuman(t, f, "older-card-parent", "author", "message", time.Now().UTC().Add(-time.Minute))
	continuityDelivered(t, f, older, nil)
	task, j := batchTask(t, f)
	plan := batchPlan(task)
	if err := f.QueueConversationPlan(ctx, j, task, plan); err != nil {
		t.Fatal(err)
	}
	j1, d1 := deliveryJob(t, f)
	j2, d2 := deliveryJob(t, f)
	if err := f.FinishDelivery(ctx, j1, d1, "one-card"); err != nil {
		t.Fatal(err)
	}
	if err := f.FinishDelivery(ctx, j2, d2, "one-card"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	replies, err := f.RecentReplies(ctx, task.Scope, now, 2)
	if err != nil || len(replies) != 2 || replies[0].MessageID != "one-card" || replies[0].Text != "第一句\n\n还有一句" || replies[1].MessageID != "receipt-older-card-parent" {
		t.Fatalf("latest same-card frame did not dedup before LIMIT: %+v %v", replies, err)
	}

	member, err := f.RecentRepliesForMember(ctx, task.Scope, task.OwnerID, now.Add(-time.Minute), now, 2)
	if err != nil || len(member) != 2 || member[0].MessageID != "one-card" || member[0].Text != "第一句\n\n还有一句" || member[1].MessageID != "receipt-older-card-parent" {
		t.Fatalf("member latest card frame did not dedup before LIMIT: %+v %v", member, err)
	}
	_, d, err := f.TaskForMessage(ctx, task.Scope, "one-card")
	if err != nil || d == nil || d.ID != d2.ID {
		t.Fatalf("card reply bound old frame: %+v %v", d, err)
	}
}
