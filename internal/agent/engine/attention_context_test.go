package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestConversationReceivesActualAttentionBurdenAndPendingRequest(t *testing.T) {
	f := newConversationFixture(t, 131072, true)
	ctx := context.Background()
	first := f.message(t, "already-answered", "说一个观点就好", "", true)
	f.model.answer = `{"choice":"reply","messages":[{"text":"这点我觉得需要再看实际证据。","reply_to_message_id":"already-answered"}]}`
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	if len(f.sender.sent) != 1 {
		t.Fatal("seed answer did not deliver")
	}

	pending := f.message(t, "still-working", "请查一下这份资料", "", true)
	oldTask, _, err := f.store.TaskForMessage(ctx, f.scope, pending.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "still-working", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.StartTurn(ctx, job, domain.TurnRequest{TaskID: oldTask.ID, Revision: oldTask.Revision}); err != nil {
		t.Fatal(err)
	}

	current := f.message(t, "feedback", "他怎么还在一条一条地回复", f.sender.sent[0].MessageID, false)
	f.model.answer = `{"choice":"observe","messages":[]}`
	f.step(t, domain.JobAgentTurn)
	var scene struct {
		Attention struct {
			OwnNotifications     int
			OwnReplyTurns        int
			HumanMessages        int
			CurrentRepliesToSelf bool
			InFlight             []struct {
				MessageID, Text, Phase string
			}
		}
	}
	for _, msg := range f.model.requests[len(f.model.requests)-1].Messages {
		if msg.Role == "user" {
			raw, _ := msg.Content.(string)
			if err = json.Unmarshal([]byte(raw), &scene); err != nil {
				t.Fatal(err)
			}
		}
	}
	a := scene.Attention
	if a.OwnNotifications != 1 || a.OwnReplyTurns != 1 || a.HumanMessages != 3 || !a.CurrentRepliesToSelf {
		t.Fatalf("actual group turn-taking context absent: %+v", a)
	}
	if len(a.InFlight) != 1 || a.InFlight[0].MessageID != pending.Source.MessageID || a.InFlight[0].Text != pending.Text || a.InFlight[0].Phase != "preparing" {
		t.Fatalf("request in progress confused with rejection: %+v", a.InFlight)
	}
	if current.Source == first.Source || len(f.sender.sent) != 1 {
		t.Fatal("observation emitted an acknowledgement")
	}
	visibleTask, _, err := f.store.TaskForMessage(ctx, f.scope, current.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	pinnedPending := false
	for _, ref := range visibleTask.ContextSources {
		pinnedPending = pinnedPending || ref == pending.Source
	}
	if !pinnedPending {
		t.Fatal("in-flight reading view lost backend source proof")
	}

}

func TestAttentionHumanParentUnderBotRootIsMemberReply(t *testing.T) {
	f := newConversationFixture(t, 131072, true)
	f.message(t, "question", "说一个观点", "", true)
	f.model.answer = `{"choice":"reply","messages":[{"text":"可以先看证据。","reply_to_message_id":"question"}]}`
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	ownID := f.sender.sent[0].MessageID
	f.message(t, "human-parent", "大家觉得这个说法怎样？", ownID, false)
	f.model.answer = `{"choice":"observe","messages":[]}`
	f.step(t, domain.JobAgentTurn)
	now := time.Now().UTC().Add(-time.Millisecond)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user"})
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "member-to-member", AuthorID: "another-member", Version: 1, OccurredAt: now}, EventID: "member-to-member", Text: "我同意你这个说法", ParentID: "human-parent", RootID: ownID, UpdatedAt: now, Raw: raw}
	if _, err := f.store.Accept(context.Background(), domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateTask(context.Background(), m, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.step(t, domain.JobAgentTurn)
	var p struct {
		Attention struct{ CurrentRepliesToSelf, CurrentRepliesToMember bool }
	}
	for _, msg := range f.model.requests[len(f.model.requests)-1].Messages {
		if msg.Role == "user" {
			body, _ := msg.Content.(string)
			if err := json.Unmarshal([]byte(body), &p); err != nil {
				t.Fatal(err)
			}
		}
	}
	if p.Attention.CurrentRepliesToSelf || !p.Attention.CurrentRepliesToMember {
		t.Fatalf("reply directly addresses human-parent; root is only a thread ancestor: %+v", p.Attention)
	}
}

func TestAttentionDirectHumanParentOutsideRecentLimit(t *testing.T) {
	f := newConversationFixture(t, 131072, true)
	parent := f.message(t, "older-human-parent", "我看应该先测一下", "", false)
	for i := range 33 {
		f.message(t, fmt.Sprintf("later-human-%d", i), "另一段话题", "", false)
	}
	now := time.Now().UTC().Add(-time.Millisecond)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user"})
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "direct-old-parent", AuthorID: "another-member", Version: 1, OccurredAt: now}, EventID: "direct-old-parent", Text: "我同意你的说法", ParentID: parent.Source.MessageID, UpdatedAt: now, Raw: raw}
	if _, err := f.store.Accept(context.Background(), domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateTask(context.Background(), m, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.model.answer = `{"choice":"observe","messages":[]}`
	f.step(t, domain.JobAgentTurn)
	var p struct {
		Attention      struct{ CurrentRepliesToSelf, CurrentRepliesToMember bool }
		BoundMaterials []struct{ MessageID string }
	}
	for _, msg := range f.model.requests[len(f.model.requests)-1].Messages {
		if msg.Role == "user" {
			body, _ := msg.Content.(string)
			if err := json.Unmarshal([]byte(body), &p); err != nil {
				t.Fatal(err)
			}
		}
	}
	materialSeen := false
	for _, bound := range p.BoundMaterials {
		materialSeen = materialSeen || bound.MessageID == parent.Source.MessageID
	}
	if !materialSeen {
		t.Fatal("probe setup: explicit source-bound parent was not loaded")
	}
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	pinned := false
	for _, ref := range task.ContextSources {
		pinned = pinned || ref == parent.Source
	}
	if !pinned {
		t.Fatal("human-parent reading view lost exact backend proof")
	}

	if p.Attention.CurrentRepliesToSelf || !p.Attention.CurrentRepliesToMember {
		t.Fatalf("direct human parent is present in source-validated BoundMaterials but fell outside Recent(32): %+v", p.Attention)
	}
}

func TestAttentionDirectOwnReceiptOutsideRecentRepliesLimit(t *testing.T) {
	f := newConversationFixture(t, 1<<20, true)
	for i := range 33 {
		id := fmt.Sprintf("delivered-root-%d", i)
		f.message(t, id, "给一个简短观点", "", true)
		plan, _ := json.Marshal(domain.ConversationPlan{Choice: "reply", Messages: []domain.ConversationMessage{{Text: "先看这次的证据。", ReplyToMessageID: id}}})
		f.model.answer = string(plan)
		f.step(t, domain.JobAgentTurn)
		f.step(t, domain.JobDelivery)
	}
	oldestID := f.sender.sent[0].MessageID
	_, receipt, err := f.store.TaskForMessage(context.Background(), f.scope, oldestID)
	if err != nil || receipt == nil || receipt.Status != "delivered" {
		t.Fatalf("probe setup: old own receipt is not durably delivered: %+v %v", receipt, err)
	}
	visible, err := f.store.RecentReplies(context.Background(), f.scope, time.Now().UTC(), 32)
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range visible {
		if reply.MessageID == oldestID {
			t.Fatal("probe setup: oldest receipt was not displaced from RecentReplies32")
		}
	}
	f.message(t, "reply-to-old-own", "那你之前这个说法呢？", oldestID, false)
	f.model.answer = `{"choice":"observe","messages":[]}`
	f.step(t, domain.JobAgentTurn)
	var p struct {
		Attention        struct{ CurrentRepliesToSelf, CurrentRepliesToMember bool }
		CurrentUtterance struct {
			ParentID  string
			Addressed bool
		}
	}
	for _, msg := range f.model.requests[len(f.model.requests)-1].Messages {
		if msg.Role == "user" {
			body, _ := msg.Content.(string)
			if err := json.Unmarshal([]byte(body), &p); err != nil {
				t.Fatal(err)
			}
		}
	}
	if p.CurrentUtterance.ParentID != oldestID || p.CurrentUtterance.Addressed {
		t.Fatalf("probe setup: need an implicit reply to known old receipt: %+v", p.CurrentUtterance)
	}
	if !p.Attention.CurrentRepliesToSelf || p.Attention.CurrentRepliesToMember {
		t.Fatalf("explicit ParentID is backed by own confirmed delivered receipt, outside RecentReplies32: %+v", p.Attention)
	}
}

func TestAttentionOwnReceiptRelationDoesNotReviveStaleEvidenceOrAuthority(t *testing.T) {
	for _, mutation := range []string{"edit", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			f := newConversationFixture(t, 131072, true)
			ctx := context.Background()
			root := f.message(t, "old-root", "之前的问题", "", true)
			f.model.answer = `{"choice":"reply","messages":[{"text":"不得复活的旧事实","reply_to_message_id":"old-root"}]}`
			f.step(t, domain.JobAgentTurn)
			f.step(t, domain.JobDelivery)
			ownID := f.sender.sent[0].MessageID
			oldTask, receipt, err := f.store.TaskForMessage(ctx, f.scope, ownID)
			if err != nil || receipt == nil {
				t.Fatalf("missing real delivered receipt: %+v %v", receipt, err)
			}
			oldSource := root.Source
			root.Source.Version++
			root.EventID = "mutated-root"
			root.UpdatedAt = time.Now().UTC()
			root.Text = "已纠正的内容"
			root.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "refresh", SenderType: "user"})
			kind := domain.EventMessage
			if mutation == "delete" {
				root.Deleted = true
				kind = domain.EventDelete
			}
			if _, err = f.store.Accept(ctx, domain.Event{ID: root.EventID, Type: kind, Message: root, OccurredAt: root.UpdatedAt}); err != nil {
				t.Fatal(err)
			}
			f.step(t, domain.JobRoute)
			current := historicalHumanReply(t, f, "new-speaker", "another-member", "那你之前这个说法呢？", ownID)
			f.model.answer = `{"choice":"observe","messages":[]}`
			f.step(t, domain.JobAgentTurn)
			var scene struct {
				Attention          struct{ CurrentRepliesToSelf, CurrentRepliesToMember bool }
				RecentConversation []struct{ MessageID, Text string }
				BoundMaterials     []struct{ MessageID, Text string }
			}
			for _, msg := range f.model.requests[len(f.model.requests)-1].Messages {
				if msg.Role == "user" {
					body, _ := msg.Content.(string)
					if err = json.Unmarshal([]byte(body), &scene); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !scene.Attention.CurrentRepliesToSelf || scene.Attention.CurrentRepliesToMember {
				t.Fatalf("physical self receipt relation lost after evidence %s: %+v", mutation, scene.Attention)
			}
			for _, entry := range scene.RecentConversation {
				if entry.MessageID == ownID || entry.Text == "不得复活的旧事实" {
					t.Fatalf("stale self text revived through relation fallback: %+v", entry)
				}
			}
			for _, material := range scene.BoundMaterials {
				if material.Text == "不得复活的旧事实" {
					t.Fatalf("stale source repinned through relation fallback: %+v", material)
				}
			}
			newTask, _, err := f.store.TaskForMessage(ctx, f.scope, current.Source.MessageID)
			if err != nil {
				t.Fatal(err)
			}
			if newTask.ID == oldTask.ID || newTask.Root != current.Source || newTask.OwnerID != current.Source.AuthorID || len(newTask.Delegates) != 0 {
				t.Fatalf("reply relation transferred old task authority: %+v", newTask)
			}
			for _, source := range newTask.ContextSources {
				if source == oldSource {
					t.Fatalf("stale source became current turn evidence: %+v", source)
				}
			}
		})
	}
}

func TestAttentionRawBotAndNonordinaryReceiptsAreNotOwnConversation(t *testing.T) {
	for _, targetKind := range []string{"raw-bot", "approval", "unknown-interactive"} {
		t.Run(targetKind, func(t *testing.T) {
			f := newConversationFixture(t, 131072, true)
			ctx := context.Background()
			targetID := "another-bot-message"
			if targetKind == "raw-bot" {
				at := time.Now().UTC().Add(-time.Second)
				raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "bot"})
				m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: targetID, AuthorID: "another-bot", Version: 1, OccurredAt: at}, EventID: targetID, Text: "另一机器人的消息", Raw: raw, UpdatedAt: at}
				if _, err := f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: at}); err != nil {
					t.Fatal(err)
				}
			} else {
				f.message(t, "ordinary-root", "先建立真实回执", "", true)
				f.model.answer = `{"choice":"reply","messages":[{"text":"普通回答","reply_to_message_id":"ordinary-root"}]}`
				f.step(t, domain.JobAgentTurn)
				f.step(t, domain.JobDelivery)
				targetID = f.sender.sent[0].MessageID
				_, receipt, err := f.store.TaskForMessage(ctx, f.scope, targetID)
				if err != nil || receipt == nil {
					t.Fatalf("missing receipt: %+v %v", receipt, err)
				}
				// Vary the receipt classification while preserving its actual
				// sender, exact message ID, group and confirmed delivery state.
				receipt.MessageType, receipt.ConversationText, receipt.Content = "interactive", "", `{"type":"template","data":{}}`
				if targetKind == "approval" {
					receipt.Kind = "approval"
				}
				raw, err := json.Marshal(receipt)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET body=$1 WHERE delivery_id=$2`, raw, receipt.ID); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC().Add(-time.Millisecond)
			raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user"})
			current := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "nonordinary-followup", AuthorID: "member", Version: 1, OccurredAt: now}, EventID: "nonordinary-followup", ParentID: targetID, Text: "这条消息呢？", Raw: raw, UpdatedAt: now}
			if _, err := f.store.Accept(ctx, domain.Event{ID: current.EventID, Type: domain.EventMessage, Message: current, OccurredAt: now}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.CreateTask(ctx, current, now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			f.model.answer = `{"choice":"observe","messages":[]}`
			f.step(t, domain.JobAgentTurn)
			var scene struct {
				Attention struct{ CurrentRepliesToSelf, CurrentRepliesToMember bool }
			}
			for _, msg := range f.model.requests[len(f.model.requests)-1].Messages {
				if msg.Role == "user" {
					body, _ := msg.Content.(string)
					if err := json.Unmarshal([]byte(body), &scene); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scene.Attention.CurrentRepliesToSelf || scene.Attention.CurrentRepliesToMember {
				t.Fatalf("nonordinary target impersonated ordinary self/member reply: %+v", scene.Attention)
			}
		})
	}
}
