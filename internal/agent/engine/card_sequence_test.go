package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	agentlark "github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
)

type sequenceOperation struct {
	UUID, Content string
	Sequence      int
}

type qualityLatePatchSender struct {
	highest    int
	operations []sequenceOperation
	applied    map[string]sequenceOperation
	mu         sync.Mutex
	calls      int
	visible    string
	started    chan struct{}
	release    chan struct{}
}

func (s *qualityLatePatchSender) Send(context.Context, domain.Scope, string, string, string, string) (string, error) {
	return "", errors.New("unexpected create")
}
func (s *qualityLatePatchSender) UpdateCard(_ context.Context, _ domain.Scope, _, content, uuid string, sequence int) error {
	s.mu.Lock()
	s.calls++
	op := sequenceOperation{uuid, content, sequence}
	s.operations = append(s.operations, op)
	call := s.calls
	s.mu.Unlock()
	// The old request has reached the provider; cancellation cannot roll it back.
	if call == 1 {
		close(s.started)
		<-s.release
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if uuid == "" || sequence < 1 {
		return errors.New("provider requires UUID and positive sequence")
	}
	if s.applied == nil {
		s.applied = map[string]sequenceOperation{}
	}
	if old, ok := s.applied[uuid]; ok {
		if old != op {
			return errors.New("UUID changed frozen operation")
		}
		return nil
	}
	if sequence <= s.highest {
		return errors.New("provider rejected stale sequence")
	}
	s.highest = sequence
	s.visible = content
	s.applied[uuid] = op
	return nil
}
func (s *qualityLatePatchSender) content() string { s.mu.Lock(); defer s.mu.Unlock(); return s.visible }

func TestQualityLateOldCardPatchCannotOverwriteNewerFrame(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	p := domain.ConversationPlan{Choice: "expand", Summary: "Public summary", Messages: []domain.ConversationMessage{
		{Text: "first detail", ReplyToMessageID: "quality_late_patch"},
		{Text: "second detail", ReplyToMessageID: "quality_late_patch"},
		{Text: "third final detail with [link](https://example.test/last) and `code`", ReplyToMessageID: "quality_late_patch"},
	}}
	raw, _ := json.Marshal(p)
	f.model.answer = string(raw)
	f.message(t, "quality_late_patch", "@AI explain", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)

	reg, err := atoms.New(atoms.Options{Tasks: f.store})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(engine.Options{Store: f.store, Model: f.model, Registry: reg, ConversationOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	sender := &qualityLatePatchSender{started: make(chan struct{}), release: make(chan struct{})}
	makeService := func(owner string) *service.Service {
		svc, err := service.New(service.Options{Store: f.store, Engine: eng, Messenger: sender, Owner: owner, Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}
	oldSvc, newSvc := makeService("quality-old"), makeService("quality-new")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- oldSvc.Step(ctx, domain.JobDelivery) }()
	select {
	case <-sender.started:
	case <-ctx.Done():
		t.Fatal("old PATCH did not start")
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE kind='delivery' AND status='running'`); err != nil {
		close(sender.release)
		t.Fatal(err)
	}
	if err = newSvc.Step(ctx, domain.JobDelivery); err != nil {
		close(sender.release)
		t.Fatal("reclaimed frame: ", err)
	}
	if err = newSvc.Step(ctx, domain.JobDelivery); err != nil {
		close(sender.release)
		t.Fatal("final frame: ", err)
	}
	if !strings.Contains(sender.content(), "third final detail") {
		close(sender.release)
		t.Fatal("final frame did not reach provider")
	}
	close(sender.release)
	oldErr := <-done
	sender.mu.Lock()
	if len(sender.operations) != 3 || sender.operations[0] != sender.operations[1] || sender.operations[0].Sequence != 1 || sender.operations[2].Sequence != 2 || sender.operations[2].UUID == sender.operations[0].UUID {
		t.Error("reclaim changed frozen UUID/sequence or frames share UUID")
	}
	sender.mu.Unlock()
	replies, err := f.store.RecentReplies(ctx, f.scope, time.Now().UTC(), 1)
	if err != nil || len(replies) != 1 {
		t.Fatalf("confirmed history: %+v %v", replies, err)
	}
	if !strings.Contains(sender.content(), "third final detail") {
		t.Fatalf("late old PATCH regressed card: provider_final_missing=true confirmed_history_has_final=%v old_worker_error=%v", strings.Contains(replies[0].Text, "third final detail"), oldErr)
	}
}

func TestQualityObserveCannotSilentlyDropPublicSummaryOrBypassBudget(t *testing.T) {
	for _, summary := range []string{"public text", strings.Repeat("s", domain.MaxConversationBytes+1), strings.Repeat(" ", domain.MaxConversationBytes+1)} {
		n := len(summary)
		p := domain.ConversationPlan{Choice: "observe", Summary: summary, Messages: []domain.ConversationMessage{}}
		raw, _ := json.Marshal(p)
		if _, err := domain.ParseConversationPlan(string(raw)); err == nil {
			frames, renderErr := domain.ConversationFrames(p)
			t.Errorf("observe accepted public summary bytes=%d and discarded it: frames=%d renderErr=%v", n, len(frames), renderErr)
		}
	}
}

func TestQualityLatestConfirmedCardUsesFrameSequenceAcrossWorkerClockSkew(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	p := domain.ConversationPlan{Choice: "expand", Summary: "Summary", Messages: []domain.ConversationMessage{
		{Text: "first detail", ReplyToMessageID: "quality_clock"},
		{Text: "second final detail with [link](https://example.test/final) and `code`", ReplyToMessageID: "quality_clock"},
	}}
	raw, _ := json.Marshal(p)
	f.model.answer = string(raw)
	f.message(t, "quality_clock", "@AI explain", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	f.step(t, domain.JobDelivery)
	// Production writes UpdatedAt using time.Now on each worker. Simulate the
	// first frame's worker clock being ahead of the final frame's worker.
	skewed := time.Now().UTC().Add(30 * time.Second).Format(time.RFC3339Nano)
	if _, err := f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET body=convert_to(jsonb_set(convert_from(body,'UTF8')::jsonb,'{UpdatedAt}',to_jsonb($1::text))::text,'UTF8') WHERE convert_from(body,'UTF8')::jsonb->>'Sequence' IS NULL`, skewed); err != nil {
		t.Fatal(err)
	}
	// Sequence=0 is omitted by the Delivery JSON omitempty contract.
	replies, err := f.store.RecentReplies(context.Background(), f.scope, time.Now().UTC().Add(time.Minute), 1)
	if err != nil || len(replies) != 1 {
		t.Fatalf("history: %+v %v", replies, err)
	}

	member, memberErr := f.store.RecentRepliesForMember(context.Background(), f.scope, "member", time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Minute), 1)
	if memberErr != nil || len(member) != 1 || !strings.Contains(member[0].Text, "second final detail") {
		t.Fatalf("member history selected old skewed frame: %+v %v", member, memberErr)
	}
	_, receipt, bindErr := f.store.TaskForMessage(context.Background(), f.scope, f.sender.sent[0].MessageID)
	if !strings.Contains(replies[0].Text, "second final detail") || bindErr != nil || receipt == nil || receipt.Sequence != 1 {
		seq := -1
		if receipt != nil {
			seq = receipt.Sequence
		}
		t.Fatalf("confirmed latest regressed on worker clock skew: history_has_final=%v binding_sequence=%d err=%v", strings.Contains(replies[0].Text, "second final detail"), seq, bindErr)
	}
	bogus := domain.SourceRef{Scope: f.scope, MessageID: "not-visible", AuthorID: "nobody", Version: 1, OccurredAt: time.Now().UTC().Add(-time.Minute)}
	sourceJSON, _ := json.Marshal([]domain.SourceRef{bogus})
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET body=convert_to(jsonb_set(convert_from(body,'UTF8')::jsonb,'{Sources}',$1::jsonb)::text,'UTF8') WHERE convert_from(body,'UTF8')::jsonb->>'Sequence' IS NULL`, string(sourceJSON)); err != nil {
		t.Fatal(err)
	}
	own := f.sender.sent[0].MessageID
	next, _ := json.Marshal(domain.ConversationPlan{Choice: "reply", Messages: []domain.ConversationMessage{{Text: "按最新完整内容继续。", ReplyToMessageID: own}}})
	f.model.answer = string(next)
	f.message(t, "clock_scene_followup", "@AI 接着说。", own, true)
	f.step(t, domain.JobAgentTurn)

}

type cardReceiptTransport func(*http.Request) (*http.Response, error)

func (f cardReceiptTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCardKitLostReceiptRetryConsumedUUIDContinuesSameCard(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	plan := domain.ConversationPlan{Choice: "expand", Summary: "公开概要。", Messages: []domain.ConversationMessage{{Text: "第一段。", ReplyToMessageID: "lost_receipt"}, {Text: "第二段。", ReplyToMessageID: "lost_receipt"}, {Text: "最终段，完整保留 [链接](https://example.test/final) 与 `代码`。", ReplyToMessageID: "lost_receipt"}}}
	encoded, _ := json.Marshal(plan)
	f.model.answer = string(encoded)
	f.message(t, "lost_receipt", "@AI explain", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	messageID := f.sender.sent[0].MessageID
	type operation struct {
		Target   string
		Card     struct{ Type, Data string }
		Uuid     string
		Sequence int
	}
	var calls []operation
	accepted := map[string]operation{}
	visible := ""
	response := func(body string) *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}
	}
	client, err := agentlark.NewClient(agentlark.ClientConfig{AppID: f.scope.AppID, AppSecret: "secret", BotOpenID: "bot", TenantID: f.scope.TenantID, HTTPClient: &http.Client{Transport: cardReceiptTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
		}
		if r.Method == "GET" {
			body, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"message_id": messageID, "chat_id": f.scope.ChatID, "msg_type": "interactive", "deleted": false, "sender": map[string]any{"id": f.scope.AppID, "id_type": "app_id", "sender_type": "app", "open_bot_id": "bot"}}}}})
			return response(string(body)), nil
		}
		if r.URL.Path == "/open-apis/cardkit/v1/cards/id_convert" {
			var body struct {
				MessageID string `json:"message_id"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.MessageID != messageID {
				t.Error("conversion changed frozen message target")
			}
			return response(`{"code":0,"data":{"card_id":"entity"}}`), nil
		}
		if r.Method != "PUT" || r.URL.Path != "/open-apis/cardkit/v1/cards/entity" {
			t.Error("retry created another notification or changed card")
		}
		var op operation
		op.Target = r.URL.Path
		if json.NewDecoder(r.Body).Decode(&op) != nil {
			t.Error("invalid CardKit operation")
		}
		calls = append(calls, op)
		if prior, ok := accepted[op.Uuid]; ok {
			if prior != op {
				t.Error("consumed UUID changed its frozen operation")
			}
			return response(`{"code":200770,"msg":"this UUID has been recently consumed"}`), nil
		}
		accepted[op.Uuid] = op
		visible = op.Card.Data
		if len(calls) == 1 {
			return response(""), nil
		} // Applied, but the receipt is lost.
		return response(`{"code":0}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := atoms.New(atoms.Options{Tasks: f.store})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(engine.Options{Store: f.store, Model: f.model, Registry: reg, ConversationOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Options{Store: f.store, Engine: eng, Messenger: client, Owner: "lost-receipt", Lease: time.Minute, TaskTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Step(context.Background(), domain.JobDelivery); err == nil {
		t.Fatal("lost first receipt unexpectedly completed")
	}
	if len(calls) != 1 {
		t.Fatalf("lost receipt retried invisibly: calls=%d", len(calls))
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at='2000-01-01T00:00:00Z'::timestamptz WHERE kind='delivery' AND status='pending'`); err != nil {
		t.Fatal(err)
	}
	if err = svc.Step(context.Background(), domain.JobDelivery); err != nil {
		t.Fatalf("consumed UUID failed to reconcile accepted frozen operation: %v", err)
	}
	if len(calls) != 2 || calls[0] != calls[1] {
		t.Fatal("retry changed immutable target/content/sequence/UUID")
	}
	if err = svc.Step(context.Background(), domain.JobDelivery); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[2].Uuid == calls[0].Uuid || calls[0].Sequence != 1 || calls[2].Sequence != 2 || !strings.Contains(visible, "最终段") {
		t.Fatal("receipt retry blocked later frame or changed card identity")
	}
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, "lost_receipt")
	if err != nil || task.Status != "completed" {
		t.Fatalf("all accepted card frames did not complete task: %v %s", err, task.Status)
	}
	replies, err := f.store.RecentReplies(context.Background(), f.scope, time.Now().UTC(), 1)
	if err != nil || len(replies) != 1 || !strings.Contains(replies[0].Text, "最终段") || len(f.sender.sent) != 1 {
		t.Fatalf("confirmed history or single-create identity lost: replies=%d err=%v", len(replies), err)
	}
}
