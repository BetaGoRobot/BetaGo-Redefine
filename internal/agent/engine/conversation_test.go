package engine_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
	_ "github.com/lib/pq"
)

type conversationModel struct {
	requests []modelapi.ChatRequest
	answer   string
}

func (m *conversationModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.requests = append(m.requests, r)
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: m.answer}}, nil
}

type conversationSender struct {
	sent, updated []domain.Delivery
	failUpdate    bool
}

func (s *conversationSender) Send(_ context.Context, scope domain.Scope, parent, kind, content, uuid string) (string, error) {
	id := "bot_" + uuid
	s.sent = append(s.sent, domain.Delivery{Scope: scope, ParentID: parent, MessageType: kind, Content: content, UUID: uuid, MessageID: id})
	return id, nil
}

func (s *conversationSender) UpdateCard(_ context.Context, scope domain.Scope, id, content, uuid string, sequence int) error {
	s.updated = append(s.updated, domain.Delivery{Scope: scope, MessageID: id, MessageType: "interactive", Content: content, UUID: uuid, Sequence: sequence})
	if s.failUpdate {
		s.failUpdate = false
		return errors.New("ambiguous card update")
	}
	return nil
}

type conversationFixture struct {
	store  *pgstore.Store
	db     *sql.DB
	schema string
	scope  domain.Scope
	model  *conversationModel
	sender *conversationSender
	svc    *service.Service
}

func newConversationFixture(t *testing.T, budget int, conversationOutput ...bool) conversationFixture {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL fixture required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "conversation_test_" + hex.EncodeToString(nonce[:])
	store, err := pgstore.New(db, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); db.Close() })
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	reg, err := atoms.New(atoms.Options{Tasks: store})
	if err != nil {
		t.Fatal(err)
	}
	m, sender := &conversationModel{answer: "你好呀！"}, &conversationSender{}
	eng, err := engine.New(engine.Options{Store: store, Model: m, Registry: reg, MaxContextBytes: budget, ConversationOutput: len(conversationOutput) > 0 && conversationOutput[0]})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Options{Store: store, Engine: eng, Messenger: sender, Owner: "conversation", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return conversationFixture{store, db, schema, domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "group", Visibility: "group"}, m, sender, svc}
}

func (f conversationFixture) message(t *testing.T, id, text, parent string, addressed bool) domain.Message {
	t.Helper()
	now := time.Now().UTC().Add(-time.Millisecond)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: addressed})
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: id, AuthorID: "member", Version: 1, OccurredAt: now}, EventID: id, Text: text, ParentID: parent, UpdatedAt: now, Raw: raw}
	if _, err := f.store.Accept(context.Background(), domain.Event{ID: id, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	f.step(t, domain.JobRoute)
	return m
}

func (f conversationFixture) step(t *testing.T, kind domain.JobKind) {
	t.Helper()
	if err := f.svc.Step(context.Background(), kind); err != nil {
		t.Fatal(err)
	}
}

type conversationPrompt struct {
	CurrentUtterance struct {
		Text, MessageID, SpeakerRef string
		ParentID                    string
	}
	InternalTaskState  map[string]any
	RecentConversation []struct {
		AuthorID                        string
		Role, MessageID, ParentID, Text string
		OccurredAt                      time.Time
	}
	Task json.RawMessage
}

func capturedConversation(t *testing.T, r modelapi.ChatRequest) (conversationPrompt, string) {
	t.Helper()
	var out conversationPrompt
	system := ""
	for _, msg := range r.Messages {
		content, _ := msg.Content.(string)
		if msg.Role == "system" {
			system += content
		}
		if msg.Role == "user" {
			if err := json.Unmarshal([]byte(content), &out); err != nil {
				t.Fatal(err)
			}
		}
	}
	return out, system
}

func TestOrdinaryGreetingUsesCurrentRequestAndOneAutomaticFinal(t *testing.T) {
	f := newConversationFixture(t, 0)
	m := f.message(t, "hello", "/bb hello", "", true)
	f.step(t, domain.JobAgentTurn)
	if len(f.model.requests) != 1 {
		t.Fatalf("greeting called model %d times", len(f.model.requests))
	}
	p, _ := capturedConversation(t, f.model.requests[0])
	if p.CurrentUtterance.Text != m.Text || p.CurrentUtterance.MessageID != m.Source.MessageID || p.CurrentUtterance.SpeakerRef == "" || len(p.InternalTaskState) != 0 || len(p.Task) != 0 {
		t.Fatalf("current user intent is not separated from internal task control: %+v", p)
	}
	f.step(t, domain.JobDelivery)
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	pinnedCurrent := false
	for _, ref := range task.ContextSources {
		pinnedCurrent = pinnedCurrent || ref == m.Source
	}
	if !pinnedCurrent {
		t.Fatal("current source not pinned in backend turn context")
	}

	as, err := f.store.Approvals(context.Background(), f.scope, task.ID)
	if err != nil || len(as) != 0 || len(f.sender.sent) != 1 || f.sender.sent[0].MessageType != "text" {
		t.Fatalf("ordinary reply should be one final, with no approval: %+v %+v %v", as, f.sender.sent, err)
	}
	var body struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(f.sender.sent[0].Content), &body) != nil || body.Text != "你好呀！" {
		t.Fatalf("ordinary delivery added internal report metadata: %+v", f.sender.sent)
	}
	if err = f.svc.Step(context.Background(), domain.JobDelivery); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("greeting queued an extra delivery: %v", err)
	}
	f.message(t, "ordinary_member", "unaddressed group chat", "", false)
	if len(f.model.requests) != 1 {
		t.Fatal("ordinary chatter unexpectedly opened a model turn")
	}
}

func TestNextTurnSeesDeliveredAssistantReplyInChronologicalConversation(t *testing.T) {
	f := newConversationFixture(t, 0)
	f.model.answer = "你可以叫我小贝。"
	first := f.message(t, "first", "/bb 你叫什么？", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	botID := f.sender.sent[0].MessageID
	f.model.answer = "叫我小贝就好。"
	second := f.message(t, "second", "/bb 那我刚才可以怎么叫你？", botID, true)
	f.step(t, domain.JobAgentTurn)
	p, _ := capturedConversation(t, f.model.requests[1])
	if p.CurrentUtterance.Text != second.Text || p.CurrentUtterance.ParentID != botID {
		t.Fatalf("current reply location lost: %+v", p.CurrentUtterance)
	}
	seen := false
	for i, entry := range p.RecentConversation {
		if i > 0 && entry.OccurredAt.Before(p.RecentConversation[i-1].OccurredAt) {
			t.Fatal("conversation is not chronological")
		}
		if entry.Role == "assistant" && entry.MessageID == botID && entry.ParentID == first.Source.MessageID && entry.Text == "你可以叫我小贝。" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("next turn omitted its own delivered ordinary reply: %+v", p.RecentConversation)
	}
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, second.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	pinned := false
	for _, ref := range task.ContextSources {
		pinned = pinned || ref == first.Source
	}
	if !pinned {
		t.Fatal("previous reply's original source dependencies were not pinned")
	}
	f.step(t, domain.JobDelivery)
	if len(f.sender.sent) != 2 {
		t.Fatal("two ordinary turns did not produce exactly two final deliveries")
	}
}

func TestConversationOmitsNonfinalUnconfirmedOtherGroupAndStaleReplies(t *testing.T) {
	f := newConversationFixture(t, 0)
	f.model.answer = "ordinary confirmed reply"
	first := f.message(t, "confirmed", "/bb hello", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, first.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	var b []byte
	if err = f.db.QueryRow(`SELECT body FROM "`+f.schema+`".deliveries WHERE task_id=$1`, task.ID).Scan(&b); err != nil {
		t.Fatal(err)
	}
	var original domain.Delivery
	if err = json.Unmarshal(b, &original); err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"approval", "tool", "delivered_late", "pending", "other_group", "no_receipt", "future"} {
		d := original
		d.ID, d.MessageID = "excluded_"+excluded, "bot_excluded_"+excluded
		d.Content = `{"text":"excluded_` + excluded + `"}`
		switch excluded {
		case "approval", "tool":
			d.Kind = excluded
		case "delivered_late", "pending":
			d.Status = excluded
		case "other_group":
			d.Scope.ChatID = "other_group"
		case "no_receipt":
			d.MessageID = ""
		case "future":
			d.UpdatedAt = time.Now().Add(time.Hour)
		}
		body, _ := json.Marshal(d)
		if _, err = f.db.Exec(`INSERT INTO "`+f.schema+`".deliveries(scope_key,delivery_id,task_id,revision,status,message_id,body)VALUES($1,$2,$3,$4,$5,$6,$7)`, d.Scope.Key(), d.ID, d.TaskID, d.Revision, d.Status, d.MessageID, body); err != nil {
			t.Fatal(err)
		}
	}
	for _, provider := range []struct{ kind, sender string }{{"message", "bot"}, {"refresh", "app"}} {
		now := time.Now().UTC().Add(-time.Millisecond)
		raw, _ := json.Marshal(domain.IngressMetadata{Kind: provider.kind, SenderType: provider.sender})
		id := "unknown_" + provider.sender
		bot := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: id, AuthorID: provider.sender + "_author", Version: 1, OccurredAt: now}, EventID: id, Text: "excluded_" + id, Raw: raw, UpdatedAt: now}
		if _, err = f.store.Accept(context.Background(), domain.Event{ID: bot.EventID, Type: domain.EventMessage, Message: bot, OccurredAt: now}); err != nil {
			t.Fatal(err)
		}
		f.step(t, domain.JobRoute)
	}
	f.message(t, "followup", "/bb continue", original.MessageID, true)
	f.step(t, domain.JobAgentTurn)
	p, _ := capturedConversation(t, f.model.requests[1])
	found := false
	for _, entry := range p.RecentConversation {
		if strings.Contains(entry.Text, "excluded_") {
			t.Fatalf("unconfirmed/control/foreign bot content became conversation: %+v", entry)
		}
		found = found || entry.Role == "assistant" && entry.Text == "ordinary confirmed reply"
	}
	if !found {
		t.Fatal("confirmed ordinary reply missing")
	}
	f.step(t, domain.JobDelivery)
	first.EventID = "confirmed_edited"
	first.Source.Version++
	first.UpdatedAt = time.Now().UTC()
	first.Text = "corrected source"
	first.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "refresh", SenderType: "user"})
	if _, err = f.store.Accept(context.Background(), domain.Event{ID: first.EventID, Type: domain.EventMessage, Message: first, OccurredAt: first.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	f.step(t, domain.JobRoute)
	f.message(t, "after_edit", "/bb what now", "", true)
	f.step(t, domain.JobAgentTurn)
	p, _ = capturedConversation(t, f.model.requests[2])
	for _, entry := range p.RecentConversation {
		if entry.Role == "assistant" {
			t.Fatalf("stale source reentered through an old assistant answer: %+v", entry)
		}
	}
}

func TestConversationSourceDeletionCannotReenterAsAnAssistantClaim(t *testing.T) {
	f := newConversationFixture(t, 0)
	f.model.answer = "previous statement"
	first := f.message(t, "delete_original", "/bb hello", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	first.Source.Version++
	first.EventID = "deleted_original"
	first.Deleted = true
	first.UpdatedAt = time.Now().UTC()
	first.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "refresh", SenderType: "user"})
	if _, err := f.store.Accept(context.Background(), domain.Event{ID: first.EventID, Type: domain.EventDelete, Message: first, OccurredAt: first.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	f.step(t, domain.JobRoute)
	f.message(t, "after_deleted", "/bb continue", f.sender.sent[0].MessageID, true)
	f.step(t, domain.JobAgentTurn)
	p, _ := capturedConversation(t, f.model.requests[1])
	for _, entry := range p.RecentConversation {
		if entry.Role == "assistant" {
			t.Fatal("deleted source returned as a previous assistant claim")
		}
	}
}

type paymentModel struct{}

func (paymentModel) Generate(context.Context, modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: "payment", Type: "function", Function: modelapi.ToolFunction{Name: "make_payment", Arguments: `{"amount":1}`}}}}}, nil
}

func TestConversationHighImpactBusinessActionStillInterruptsBeforeExecution(t *testing.T) {
	f := newConversationFixture(t, 0)
	var effects atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		effects.Add(1)
		w.Write([]byte(`{"receipt":"paid"}`))
	}))
	defer api.Close()
	reg, err := atoms.New(atoms.Options{Tasks: f.store, APIs: []atoms.APIConfig{{Name: "make_payment", URL: api.URL, Parameters: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"integer"}},"required":["amount"],"additionalProperties":false}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(engine.Options{Store: f.store, Model: paymentModel{}, Registry: reg, ConversationOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	f.svc, err = service.New(service.Options{Store: f.store, Engine: eng, Messenger: f.sender, Owner: "payment", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	m := f.message(t, "pay", "/bb pay one unit", "", true)
	f.step(t, domain.JobAgentTurn)
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	as, err := f.store.Approvals(context.Background(), f.scope, task.ID)
	if err != nil || len(as) != 1 || as[0].InterruptID == "" || as[0].Tool != "make_payment" || effects.Load() != 0 {
		t.Fatalf("high-impact action bypassed native approval: %+v effects=%d err=%v", as, effects.Load(), err)
	}
	f.step(t, domain.JobDelivery)
	if len(f.sender.sent) != 1 || f.sender.sent[0].MessageType != "interactive" {
		t.Fatal("high-impact action did not issue its exact-action approval card")
	}
}

func TestConversationHistoryTrimsOldestWithinExistingPromptBudget(t *testing.T) {
	// Allow the real instruction and enabled tools to fit. This remains below
	// the production 131072-byte cap, and the seeded history exceeds its half
	// budget so the test still requires actual oldest-entry trimming.
	const budget = 32768
	f := newConversationFixture(t, budget)
	f.model.answer = "old_long_reply_" + strings.Repeat("x", budget/2+1024)
	f.message(t, "large_reply", "/bb hello", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	f.model.answer = "你好！"
	current := f.message(t, "budget_current", "/bb continue naturally", f.sender.sent[0].MessageID, true)
	f.step(t, domain.JobAgentTurn)
	p, _ := capturedConversation(t, f.model.requests[1])
	if p.CurrentUtterance.Text != current.Text {
		t.Fatal("budget trimming removed the current request")
	}
	for _, msg := range f.model.requests[1].Messages {
		if msg.Role == "user" {
			text, _ := msg.Content.(string)
			if len(text) > budget/2 || strings.Contains(text, "old_long_reply_") {
				t.Fatal("old assistant history exceeded the existing prompt budget")
			}
		}
	}
	request, err := json.Marshal(f.model.requests[1])
	if err != nil || len(request) > budget {
		t.Fatalf("complete model context exceeded configured budget: bytes=%d err=%v", len(request), err)
	}
}

func TestSocialTerminalEnvelopeObservesOrDeliversSeveralNaturalMessages(t *testing.T) {
	for _, observe := range []bool{true, false} {
		t.Run(map[bool]string{true: "observe", false: "two_messages"}[observe], func(t *testing.T) {
			f := newConversationFixture(t, 0, true)
			if observe {
				f.model.answer = `{"choice":"observe","messages":[]}`
			} else {
				f.model.answer = `{"choice":"reply","messages":[{"text":"这个会议确实开得有点空。","reply_to_message_id":"social"},{"text":"没有决策，也没有结论。","reply_to_message_id":"social"}]}`
			}
			m := f.message(t, "social", "@AI 这个会开了两小时，什么也没定。", "", true)
			f.step(t, domain.JobAgentTurn)
			if len(f.model.requests) != 1 || len(f.model.requests[0].ResponseFormat) == 0 {
				t.Fatal("production turn missed structured output contract")
			}
			p, _ := capturedConversation(t, f.model.requests[0])
			if p.CurrentUtterance.Text != m.Text || len(p.InternalTaskState) != 0 {
				t.Fatal("work order frame still exposed")
			}
			if observe {
				if err := f.svc.Step(context.Background(), domain.JobDelivery); !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("observation sent message: %v", err)
				}
			} else {
				f.step(t, domain.JobDelivery)
			}
			task, _, err := f.store.TaskForMessage(context.Background(), f.scope, "social")
			if err != nil || task.Status != "completed" {
				t.Fatalf("terminal choice incomplete: %+v %v", task, err)
			}
			want := 1
			if observe {
				want = 0
			}
			if len(f.sender.sent) != want {
				t.Fatalf("got %d deliveries want %d", len(f.sender.sent), want)
			}
			for _, d := range f.sender.sent {
				if strings.Contains(d.Content, "choice") || strings.Contains(d.Content, "reply_to_message_id") || d.ParentID != "social" {
					t.Fatalf("internal envelope leaked or target lost: %+v", d)
				}
			}
		})
	}
}

func TestConversationParagraphsBecomeFrozenReadableMessagesBeforeDelivery(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	paragraphs := []string{"我觉得这个判断需要分开说，先看它解决的问题。", "真正影响结果的是执行时能不能保留上下文，而不是术语够不够新。", "所以我会先用一个小场景验证，再决定要不要扩大范围。"}
	logical := make([]domain.ConversationMessage, 0, len(paragraphs))
	for _, text := range paragraphs {
		logical = append(logical, domain.ConversationMessage{Text: text, ReplyToMessageID: "readable"})
	}
	encoded, err := json.Marshal(domain.ConversationPlan{Choice: "expand", Messages: logical})
	if err != nil {
		t.Fatal(err)
	}
	f.model.answer = string(encoded)
	f.message(t, "readable", "@AI 你怎么看，详细说说？", "", true)
	f.step(t, domain.JobAgentTurn)
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, "readable")
	if err != nil || task.FinalConversation == nil || len(task.FinalConversation.Messages) != len(paragraphs) {
		t.Fatalf("three logical sections were not preserved before the native Eino turn froze delivery: %+v %v", task.FinalConversation, err)
	}
	if len(f.model.requests) != 1 {
		t.Fatalf("readability made extra model calls: %d", len(f.model.requests))
	}
	var format struct {
		JSONSchema struct {
			Schema struct {
				Properties struct {
					Messages struct {
						MaxItems int `json:"maxItems"`
					} `json:"messages"`
				} `json:"properties"`
			} `json:"schema"`
		} `json:"json_schema"`
	}
	if err = json.Unmarshal(f.model.requests[0].ResponseFormat, &format); err != nil || format.JSONSchema.Schema.Properties.Messages.MaxItems != domain.MaxConversationMessages {
		t.Fatalf("model and transport disagree on whole-batch resource ceiling: %+v %v", format, err)
	}

	for i, text := range paragraphs {
		if got := task.FinalConversation.Messages[i]; got.Text != text || got.ReplyToMessageID != "readable" {
			t.Fatalf("frozen paragraph %d lost text or target: %+v", i, got)
		}
		f.step(t, domain.JobDelivery)
		if len(f.sender.sent) != 1 {
			t.Fatalf("paragraph %d created %d messages; want one card", i, len(f.sender.sent))
		}
		frame := f.sender.sent[0]
		if i > 0 {
			frame = f.sender.updated[i-1]
		}
		if frame.MessageID != f.sender.sent[0].MessageID || frame.MessageType != "interactive" {
			t.Fatalf("frame %d changed card identity/type: %+v", i, frame)
		}
		var card struct {
			Body struct {
				Elements []struct {
					Tag, Content string
					Elements     []struct{ Content string }
				}
			}
		}
		if json.Unmarshal([]byte(frame.Content), &card) != nil || len(card.Body.Elements) != 2 || card.Body.Elements[1].Tag != "collapsible_panel" || len(card.Body.Elements[1].Elements) != i+1 {
			t.Fatalf("frame %d malformed cumulative card: %s", i, frame.Content)
		}
		for k := 0; k <= i; k++ {
			if card.Body.Elements[1].Elements[k].Content != paragraphs[k] {
				t.Fatalf("frame %d section %d changed content/order", i, k)
			}
		}
		task, _, err = f.store.TaskForMessage(context.Background(), f.scope, "readable")
		want := "ready_delivery"
		if i == len(paragraphs)-1 {
			want = "completed"
		}
		if err != nil || task.Status != want {
			t.Fatalf("receipt %d lifecycle: want %s got %+v %v", i, want, task, err)
		}
	}

	approvals, err := f.store.Approvals(context.Background(), f.scope, task.ID)
	if err != nil || len(approvals) != 0 || len(f.model.requests) != 1 || len(f.sender.sent) != 1 || len(f.sender.updated) != len(paragraphs)-1 {
		t.Fatalf("readable final needed extra calls, approvals or sends: approvals=%d calls=%d sends=%d err=%v", len(approvals), len(f.model.requests), len(f.sender.sent), err)
	}
	var receipts int
	if err = f.db.QueryRow(`SELECT count(*) FROM "`+f.schema+`".deliveries WHERE task_id=$1 AND status='delivered' AND message_id<>''`, task.ID).Scan(&receipts); err != nil || receipts != len(paragraphs) {
		t.Fatalf("not every paragraph has a confirmed receipt: %d %v", receipts, err)
	}
	if err = f.svc.Step(context.Background(), domain.JobDelivery); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("readability queued an extra send: %v", err)
	}
}

func TestInvalidConversationOutputRemainsRetryableAndNeverReachesChat(t *testing.T) {
	tooManySections := domain.ConversationPlan{Choice: "expand"}
	for i := 0; i < 17; i++ {
		tooManySections.Messages = append(tooManySections.Messages, domain.ConversationMessage{Text: "一段。", ReplyToMessageID: "invalid"})
	}
	oversizedParagraphs, _ := json.Marshal(tooManySections)
	byteOverflow, _ := json.Marshal(domain.ConversationPlan{Choice: "expand", Messages: []domain.ConversationMessage{{Text: strings.Repeat("字", (domain.MaxConversationBytes/3)+1), ReplyToMessageID: "invalid"}}})
	encodedOverhead, _ := json.Marshal(domain.ConversationPlan{Choice: "expand", Messages: []domain.ConversationMessage{{Text: strings.TrimSuffix(strings.Repeat("```\n"+strings.Repeat("x", 1772)+"\n```\n\n", 16), "\n\n"), ReplyToMessageID: "invalid"}}})
	for _, tc := range []struct{ name, answer string }{
		{"malformed", `plain text`},
		{"unknown_target", `{"choice":"reply","messages":[{"text":"hi","reply_to_message_id":"unseen"}]}`},
		{"too_many_logical_sections", string(oversizedParagraphs)},
		{"whole_answer_byte_budget", string(byteOverflow)},
		{"encoded_batch_too_large", string(encodedOverhead)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConversationFixture(t, 0, true)
			f.model.answer = tc.answer
			m := f.message(t, "invalid", "@AI hello", "", true)
			err := f.svc.Step(context.Background(), domain.JobAgentTurn)
			if !errors.Is(err, engine.ErrConversationOutput) {
				t.Fatalf("invalid envelope did not produce retryable output error: %v", err)
			}
			var state string
			if e := f.db.QueryRow(`SELECT status FROM "`+f.schema+`".jobs WHERE kind=$1`, domain.JobAgentTurn).Scan(&state); e != nil || state != "pending" {
				t.Fatalf("invalid envelope became permanent silent success: %s %v", state, e)
			}
			if e := f.svc.Step(context.Background(), domain.JobDelivery); !errors.Is(e, domain.ErrNotFound) {
				t.Fatalf("invalid output leaked: %v", e)
			}
			var partial int
			if e := f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".deliveries`).Scan(&partial); e != nil || partial != 0 {
				t.Fatalf("rejected plan left a partial frozen batch: %d %v", partial, e)
			}
			task, _, e := f.store.TaskForMessage(context.Background(), f.scope, m.Source.MessageID)
			if e != nil || task.Status == "completed" {
				t.Fatal("invalid output completed task")
			}
			f.model.answer = `{"choice":"reply","messages":[{"text":"你好呀","reply_to_message_id":"invalid"}]}`
			if _, e = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET available_at=clock_timestamp() WHERE kind=$1 AND status='pending'`, domain.JobAgentTurn); e != nil {
				t.Fatal(e)
			}
			f.step(t, domain.JobAgentTurn)
			f.step(t, domain.JobDelivery)
			if len(f.sender.sent) != 1 || !strings.Contains(f.sender.sent[0].Content, "你好呀") {
				t.Fatal("invalid output retry did not recover to exactly one valid send")
			}

		})
	}
}

func TestConversationCanReplyToItsActuallyDeliveredVisibleUtterance(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	f.model.answer = `{"choice":"reply","messages":[{"text":"刚才那句漏了个条件。","reply_to_message_id":"first_own"}]}`
	f.message(t, "first_own", "@AI 说一句你的判断", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	ownID := f.sender.sent[0].MessageID
	encoded, _ := json.Marshal(domain.ConversationPlan{Choice: "reply", Messages: []domain.ConversationMessage{{Text: "补充一下，只有提交后才可见。", ReplyToMessageID: ownID}}})
	f.model.answer = string(encoded)
	f.message(t, "second_own", "@AI 你刚才说的有个条件吧？", ownID, true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	if len(f.sender.sent) != 2 || f.sender.sent[1].ParentID != ownID {
		t.Fatal("actual visible own utterance was not a valid reply target")
	}
}

func TestSpecReviewHumanReplyDuringPartialBatchKeepsConfirmedAssistantUtterance(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	f.model.answer = `{"choice":"expand","messages":[{"text":"刚才我说预算是二十万。","reply_to_message_id":"partial_origin"},{"text":"还有一点补充。","reply_to_message_id":"partial_origin"}]}`
	f.message(t, "partial_origin", "@AI 预算呢", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	own := f.sender.sent[0].MessageID
	plan, _ := json.Marshal(domain.ConversationPlan{Choice: "reply", Messages: []domain.ConversationMessage{{Text: "我刚才说的是二十万。", ReplyToMessageID: own}}})
	f.model.answer = string(plan)
	f.message(t, "partial_followup", "你刚才说的是多少？", own, false)
	f.step(t, domain.JobAgentTurn)
	scene, _ := capturedConversation(t, f.model.requests[len(f.model.requests)-1])
	seen := false
	for _, entry := range scene.RecentConversation {
		if entry.Role == "assistant" && entry.MessageID == own && entry.Text == "刚才我说预算是二十万。" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("human replied while batch pending, but source-valid confirmed assistant utterance disappeared after task revision")
	}
}

// A delivered ordinary utterance remains conversation history after its task
// moves on. Replying to it must not transfer that task's control or delegates.
func historicalFinalFixture(t *testing.T) (conversationFixture, domain.Message, domain.Task, string) {
	t.Helper()
	f := newConversationFixture(t, 0, true)
	evidence := f.message(t, "historical_evidence", "当时讨论的可见依据。", "", false)
	f.model.answer = `{"choice":"expand","messages":[{"text":"当时我说的是二十万。","reply_to_message_id":"historical_root"},{"text":"原批次还有一句。","reply_to_message_id":"historical_root"}]}`
	root := f.message(t, "historical_root", "@AI 说一下预算", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, root.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ControlTask(context.Background(), f.scope, task.OwnerID, domain.ControlAction{Action: "delegate", TaskID: task.ID, Revision: task.Revision}, domain.SourceRef{}, "old-delegate"); err != nil {
		t.Fatal(err)
	}
	own := f.sender.sent[0].MessageID
	f.message(t, "historical_first_followup", "先补充一个条件。", own, false)
	task, err = f.store.Task(context.Background(), f.scope, task.ID)
	if err != nil || task.Revision != 2 {
		t.Fatalf("fixture did not revise partial batch: %d %v", task.Revision, err)
	}
	return f, evidence, task, own
}
func historicalHumanReply(t *testing.T, f conversationFixture, id, author, text, parent string) domain.Message {
	t.Helper()
	at := time.Now().UTC().Add(-time.Millisecond)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user"})
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: id, Version: 1, AuthorID: author, OccurredAt: at}, EventID: id, ParentID: parent, Text: text, Raw: raw, UpdatedAt: at}
	if _, err := f.store.Accept(context.Background(), domain.Event{ID: id, Type: domain.EventMessage, Message: m, OccurredAt: at}); err != nil {
		t.Fatal(err)
	}
	f.step(t, domain.JobRoute)
	return m
}
func TestHistoricalOrdinaryFinalStartsIndependentConversationForActualSpeaker(t *testing.T) {
	for _, tc := range []struct{ name, author, sourceChange string }{{"owner", "member", ""}, {"another_member", "another-member", ""}, {"edited_proof", "another-member", "edit"}, {"deleted_proof", "another-member", "delete"}} {
		t.Run(tc.name, func(t *testing.T) {
			f, evidence, old, own := historicalFinalFixture(t)
			if tc.sourceChange != "" {
				evidence.EventID = "historical_evidence_changed"
				evidence.Source.Version++
				evidence.UpdatedAt = time.Now().UTC()
				evidence.Text = "修改后的依据"
				evidence.Deleted = tc.sourceChange == "delete"
				evidence.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "refresh", SenderType: "user"})
				kind := domain.EventMessage
				if evidence.Deleted {
					kind = domain.EventDelete
				}
				if _, err := f.store.Accept(context.Background(), domain.Event{ID: evidence.EventID, Type: kind, Message: evidence, OccurredAt: evidence.UpdatedAt}); err != nil {
					t.Fatal(err)
				}
				f.step(t, domain.JobRoute)
			}
			current := historicalHumanReply(t, f, "historical_new_reply", tc.author, "你之前说的是多少？", own)
			fresh, _, err := f.store.TaskForMessage(context.Background(), f.scope, current.Source.MessageID)
			if err != nil {
				t.Fatalf("reply to confirmed ordinary history was discarded after revision: %v", err)
			}
			if fresh.ID == old.ID || fresh.OwnerID != tc.author || fresh.RequesterID != tc.author || fresh.Root != current.Source || len(fresh.Delegates) != 0 || len(fresh.Sources) != 1 || fresh.Sources[0] != current.Source {
				t.Fatalf("historical reply inherited old authority: %+v", fresh)
			}
			if fresh.Authorized("old-delegate") {
				t.Fatal("new conversation inherited old delegate")
			}
			unchanged, err := f.store.Task(context.Background(), f.scope, old.ID)
			if err != nil || unchanged.Revision != old.Revision || unchanged.OwnerID != old.OwnerID || len(unchanged.Delegates) != 1 {
				t.Fatal("new conversation modified previous task")
			}
			approvals, err := f.store.Approvals(context.Background(), f.scope, fresh.ID)
			if err != nil || len(approvals) != 0 {
				t.Fatal("historical reply inherited an approval")
			}
			// Finish the older queued turn first, then run the independent current turn.
			f.model.answer = `{"choice":"observe","messages":[]}`
			f.step(t, domain.JobAgentTurn)
			target := current.Source.MessageID
			if tc.sourceChange == "" {
				target = own
			}
			plan, _ := json.Marshal(domain.ConversationPlan{Choice: "reply", Messages: []domain.ConversationMessage{{Text: "按当前上下文回答。", ReplyToMessageID: target}}})
			f.model.answer = string(plan)
			f.step(t, domain.JobAgentTurn)
			scene, _ := capturedConversation(t, f.model.requests[len(f.model.requests)-1])
			seen := false
			for _, entry := range scene.RecentConversation {
				if entry.Role == "assistant" && entry.MessageID == own && entry.Text == "当时我说的是二十万。" {
					seen = true
				}
			}
			if seen != (tc.sourceChange == "") {
				t.Fatalf("historical utterance proof validity ignored: seen=%v change=%s", seen, tc.sourceChange)
			}
			// The revoked remainder of the old batch has a delivery job but cannot send.
			f.step(t, domain.JobDelivery)
			f.step(t, domain.JobDelivery)
			if len(f.sender.sent) != 2 || f.sender.sent[1].ParentID != target {
				t.Fatal("historical reply failed native Eino/frozen delivery path")
			}
			finished, err := f.store.Task(context.Background(), f.scope, fresh.ID)
			if err != nil || finished.Status != "completed" {
				t.Fatal("independent conversation did not complete")
			}

		})
	}
}

func TestHistoricalReceiptControlsAndNonordinaryKindsRemainRevisionBound(t *testing.T) {
	cases := []struct{ name, text, kind, typ, status string }{
		{"bare_approve", "approve", "final", "text", "delivered"},
		{"bare_confirm", "确认", "final", "text", "delivered"},
		{"explicit_confirm", "确认执行", "final", "text", "delivered"},
		{"bare_cancel", "cancel", "final", "text", "delivered"},
		{"cancel_task", "取消任务", "final", "text", "delivered"},
		{"delegate_control", "/delegate @_user_1", "final", "text", "delivered"},
		{"delegate_chinese", "委托 @_user_1", "final", "text", "delivered"},
		{"old_approval", "确认", "approval", "interactive", "delivered"},
		{"old_proposal_discussion", "重新执行它", "approval", "interactive", "delivered"},
		{"tool_receipt", "继续这个动作", "tool", "text", "delivered"},
		{"unknown_receipt", "继续这个动作", "unknown", "text", "delivered"},
		{"missing_type", "普通接话", "final", "", "delivered"},
		{"nontext_final", "普通接话", "final", "interactive", "delivered"},
		{"late_send", "普通接话", "final", "text", "delivered_late"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _, old, own := historicalFinalFixture(t)
			_, receipt, err := f.store.TaskForMessage(context.Background(), f.scope, own)
			if err != nil || receipt == nil {
				t.Fatal("missing confirmed fixture receipt")
			}
			receipt.Kind, receipt.MessageType, receipt.Status = tc.kind, tc.typ, tc.status
			if tc.name == "nontext_final" {
				receipt.ConversationText = ""
			}
			body, _ := json.Marshal(receipt)
			if _, err = f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET status=$1,body=$2 WHERE delivery_id=$3`, tc.status, body, receipt.ID); err != nil {
				t.Fatal(err)
			}
			current := historicalHumanReply(t, f, "historical_control", "another-member", tc.text, own)
			if _, _, err = f.store.TaskForMessage(context.Background(), f.scope, current.Source.MessageID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("stale control/nonordinary receipt opened or selected a task: %v", err)
			}
			unchanged, err := f.store.Task(context.Background(), f.scope, old.ID)
			if err != nil || unchanged.Revision != old.Revision || unchanged.Status != old.Status || len(unchanged.Delegates) != len(old.Delegates) {
				t.Fatal("historical control changed previous task")
			}
			var tasks, executions int
			if err = f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".tasks`).Scan(&tasks); err != nil || tasks != 1 {
				t.Fatal("historical control created a business task")
			}
			if err = f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".tool_executions`).Scan(&executions); err != nil || executions != 0 {
				t.Fatal("historical receipt authorized an effect")
			}
			if len(f.model.requests) != 1 {
				t.Fatal("historical control triggered a model turn")
			}
		})
	}
}

func TestRenderedCardBudgetFailsBeforeAnyDeliveryFreeze(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	// Raw envelope and text fit; backend card metadata and repeated summary do not.
	text := "```\n" + strings.Repeat("x", 14250) + "\n```"
	answer, _ := json.Marshal(map[string]any{"choice": "expand", "summary": strings.Repeat("s", 14250), "messages": []domain.ConversationMessage{{Text: text, ReplyToMessageID: "card_budget"}}})
	f.model.answer = string(answer)
	f.message(t, "card_budget", "@AI explain", "", true)
	if err := f.svc.Step(context.Background(), domain.JobAgentTurn); !errors.Is(err, engine.ErrConversationOutput) {
		t.Fatalf("card overflow not retryable: %v", err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".deliveries`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("overflow partially froze deliveries: %d %v", n, err)
	}
	if len(f.sender.sent) != 0 {
		t.Fatal("overflow reached transport")
	}
}

func TestCardUpdateRetryReusesOneCreateAndSameFrozenContent(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	f.model.answer = `{"choice":"expand","summary":"要点","messages":[{"text":"第一段。","reply_to_message_id":"retry_card"},{"text":"第二段，保留完整的 [链接](https://example.test/path) 和代码。","reply_to_message_id":"retry_card"}]}`
	f.message(t, "retry_card", "@AI explain", "", true)
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	f.sender.failUpdate = true
	if err := f.svc.Step(context.Background(), domain.JobDelivery); err == nil {
		t.Fatal("ambiguous update unexpectedly succeeded")
	}
	if len(f.sender.sent) != 1 || len(f.sender.updated) != 1 {
		t.Fatal("failed update created another message")
	}
	if _, err := f.db.Exec(`UPDATE "`+f.schema+`".jobs SET available_at=clock_timestamp()-interval '1 second' WHERE kind=$1 AND status='pending'`, domain.JobDelivery); err != nil {
		t.Fatal(err)
	}
	f.step(t, domain.JobDelivery)
	if len(f.sender.sent) != 1 || len(f.sender.updated) != 2 || f.sender.updated[0].Content != f.sender.updated[1].Content || f.sender.updated[1].MessageID != f.sender.sent[0].MessageID {
		t.Fatal("card retry changed frozen frame or created duplicate")
	}
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, "retry_card")
	if err != nil || task.Status != "completed" {
		t.Fatalf("retried update not completed: %v %s", err, task.Status)
	}
	replies, err := f.store.RecentReplies(context.Background(), f.scope, time.Now().UTC(), 1)
	if err != nil || len(replies) != 1 || replies[0].Text != "要点\n\n第一段。\n\n第二段，保留完整的 [链接](https://example.test/path) 和代码。" {
		t.Fatalf("history missing complete retried answer: %+v %v", replies, err)
	}
}

func TestConversationSummaryReachesOneDeliveryAndConfirmedHistory(t *testing.T) {
	for _, choice := range []string{"reply", "expand"} {
		t.Run(choice, func(t *testing.T) {
			f := newConversationFixture(t, 0, true)
			answer, _ := json.Marshal(domain.ConversationPlan{Choice: choice, Summary: "仍需人工复核。", Messages: []domain.ConversationMessage{{Text: "现有信息支持这个判断。", ReplyToMessageID: "summary_delivery"}}})
			f.model.answer = string(answer)
			f.message(t, "summary_delivery", "@AI explain", "", true)
			f.step(t, domain.JobAgentTurn)
			f.step(t, domain.JobDelivery)
			if len(f.sender.sent) != 1 {
				t.Fatal("summary created more than one message")
			}
			replies, err := f.store.RecentReplies(context.Background(), f.scope, time.Now().UTC(), 1)
			if err != nil || len(replies) != 1 || replies[0].Text != "仍需人工复核。\n\n现有信息支持这个判断。" {
				t.Fatalf("unique visible summary disappeared from history: %+v %v", replies, err)
			}
		})
	}
}

func TestLongLogicalSectionsStayIntactInOneCardAndTwoUpdates(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	paragraph := strings.Repeat("提交前先核对这条消息的群归属、任务版本与证据时效，确认后才推进下一帧。", 4)
	detail := strings.TrimSuffix(strings.Repeat(paragraph+"\n\n", 6), "\n\n")
	sections := []string{"第一阶段。\n\n" + detail, "第二阶段。\n\n" + detail + "\n\n```go\nfor i := 0; i < 3; i++ {\n    appendFrame(i)\n}\n```\n\n[接口文档](https://example.test/card?mode=append#same-message)", "第三阶段。\n\n" + detail}
	summary := "三个阶段共享同一张卡片，逐段追加时仍需重新验证消息归属与证据时效。"
	plan := domain.ConversationPlan{Choice: "expand", Summary: summary}
	for _, section := range sections {
		plan.Messages = append(plan.Messages, domain.ConversationMessage{Text: section, ReplyToMessageID: "long_logical"})
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("long logical fixture must fit whole-answer budget: %v", err)
	}
	if count := len([]rune(strings.Join(sections, "\n\n"))); count < 2500 || count > 3000 {
		t.Fatalf("fixture must exercise a detailed answer of about 2500 characters: %d", count)
	}
	encoded, _ := json.Marshal(plan)
	f.model.answer = string(encoded)
	f.message(t, "long_logical", "@AI 详细解释三个阶段，并保留代码和链接。", "", true)
	f.step(t, domain.JobAgentTurn)
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, "long_logical")
	if err != nil || task.FinalConversation == nil || len(task.FinalConversation.Messages) != 3 {
		t.Fatalf("long logical sections were split/rejected: %+v %v", task.FinalConversation, err)
	}
	for i, section := range sections {
		if task.FinalConversation.Messages[i].Text != section {
			t.Fatalf("frozen section %d changed its paragraphs/code/link", i)
		}
		f.step(t, domain.JobDelivery)
		if len(f.sender.sent) != 1 || len(f.sender.updated) != i {
			t.Fatalf("frame %d created notifications: create=%d update=%d", i, len(f.sender.sent), len(f.sender.updated))
		}
		frame := f.sender.sent[0]
		if i > 0 {
			frame = f.sender.updated[i-1]
		}
		if frame.MessageID != f.sender.sent[0].MessageID {
			t.Fatal("logical section changed card message ID")
		}
		var card struct {
			Body struct {
				Elements []struct {
					Content  string
					Elements []struct{ Content string }
				}
			}
		}
		if json.Unmarshal([]byte(frame.Content), &card) != nil || len(card.Body.Elements) != 2 || card.Body.Elements[0].Content != summary || len(card.Body.Elements[1].Elements) != i+1 {
			t.Fatalf("frame %d lost public summary or sections", i)
		}
		for k := 0; k <= i; k++ {
			if card.Body.Elements[1].Elements[k].Content != sections[k] {
				t.Fatalf("frame %d section %d truncated/reordered", i, k)
			}
		}
	}
	replies, err := f.store.RecentReplies(context.Background(), f.scope, time.Now().UTC(), 1)
	if err != nil || len(replies) != 1 || replies[0].Text != summary+"\n\n"+strings.Join(sections, "\n\n") {
		t.Fatalf("confirmed long history truncated/dropped content: count=%d err=%v", len(replies), err)
	}
	if err = f.svc.Step(context.Background(), domain.JobDelivery); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("long answer created another notification: %v", err)
	}
}

func TestConversationSchemaAdvertisesThreeChoicesAndKeepsLegacyReactParsing(t *testing.T) {
	f := newConversationFixture(t, 0, true)
	f.model.answer = `{"choice":"observe","summary":"","messages":[]}`
	f.message(t, "reaction_observe", "@AI 这里已经贴了表情。", "", true)
	f.step(t, domain.JobAgentTurn)
	var format struct {
		JSONSchema struct {
			Schema struct {
				Properties struct {
					Choice  struct{ Enum []string }
					Summary struct{ Description string }
				}
			}
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(f.model.requests[0].ResponseFormat, &format); err != nil {
		t.Fatal(err)
	}
	choices := format.JSONSchema.Schema.Properties.Choice.Enum
	if strings.Join(choices, ",") != "observe,reply,expand" {
		t.Fatalf("model still receives ambiguous react choice: %v", choices)
	}
	if !strings.Contains(format.JSONSchema.Schema.Properties.Summary.Description, "群内可见") {
		t.Fatal("summary schema omitted public-content contract")
	}
	if err := f.svc.Step(context.Background(), domain.JobDelivery); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("observation after real reaction created a text message: %v", err)
	}
	if _, err := domain.ParseConversationPlan(`{"choice":"react","messages":[{"text":"🙂","reply_to_message_id":"old"}]}`); err != nil {
		t.Fatalf("stored legacy react envelope lost parsing compatibility: %v", err)
	}
}
