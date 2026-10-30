package service_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
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

type changedContextModel struct {
	change     func()
	calls      int
	sawFresh   bool
	oldOnRetry bool
}

func (m *changedContextModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.calls++
	for _, msg := range r.Messages {
		text, _ := msg.Content.(string)
		m.sawFresh = m.sawFresh || strings.Contains(text, "更新后的上下文")
		if m.calls > 1 && strings.Contains(text, "旧上下文") {
			m.oldOnRetry = true
		}
	}
	if m.calls == 1 {
		m.change()
	}
	text := "旧上下文回答"
	if m.calls > 1 {
		text = "更新后的回答"
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: text}}, nil
}

type retrySender struct{ texts []string }

func (s *retrySender) Send(_ context.Context, _ domain.Scope, _ string, kind, content, _ string) (string, error) {
	s.texts = append(s.texts, content)
	return "receipt", nil
}

func TestChangedContextReplansOrdinaryTurnInsteadOfStrandingReadyTask(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "edit", true: "delete"}[deleted], func(t *testing.T) {
			dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
			if dsn == "" {
				t.Skip("isolated PostgreSQL required")
			}
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				t.Fatal("fixture open")
			}
			defer db.Close()
			var nonce [8]byte
			rand.Read(nonce[:])
			schema := "retry_context_" + hex.EncodeToString(nonce[:])
			store, err := pgstore.New(db, schema)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
			ctx := context.Background()
			if err = store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			scope := domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "group", Visibility: "group"}
			at := time.Now().UTC().Add(-time.Second)
			accept := func(id, text string, addressed bool) domain.Message {
				raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: addressed})
				m := domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: id, AuthorID: "member", Version: 1, OccurredAt: at}, EventID: id, Text: text, Raw: raw, UpdatedAt: at}
				if _, err := store.Accept(ctx, domain.Event{ID: id, Type: domain.EventMessage, Message: m, OccurredAt: at}); err != nil {
					t.Fatal(err)
				}
				return m
			}
			history := accept("history", "旧上下文", false)
			model := &changedContextModel{change: func() {
				m := history
				m.Source.Version = 2
				m.EventID = "history_changed"
				m.UpdatedAt = at.Add(time.Millisecond)
				m.Text = "更新后的上下文"
				m.Deleted = deleted
				typ := domain.EventMessage
				if deleted {
					typ = domain.EventDelete
				}
				if _, err := store.Accept(ctx, domain.Event{ID: m.EventID, Type: typ, Message: m, OccurredAt: m.UpdatedAt}); err != nil {
					t.Fatal(err)
				}
			}}
			reg, _ := atoms.New(atoms.Options{Sources: store})
			e, err := engine.New(engine.Options{Store: store, Model: model, Registry: reg})
			if err != nil {
				t.Fatal(err)
			}
			sender := &retrySender{}
			svc, err := service.New(service.Options{Store: store, Engine: e, Messenger: sender, Owner: "retry", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			if err = svc.Step(ctx, domain.JobRoute); err != nil {
				t.Fatal(err)
			}
			root := accept("request", "这事怎么看？", true)
			if err = svc.Step(ctx, domain.JobRoute); err != nil {
				t.Fatal(err)
			}
			if err = svc.Step(ctx, domain.JobAgentTurn); err != nil {
				t.Fatal(err)
			}
			if err = svc.Step(ctx, domain.JobAgentTurn); err != nil {
				t.Fatalf("source edit stranded the ordinary turn: no fresh retry (%v)", err)
			}
			if err = svc.Step(ctx, domain.JobDelivery); err != nil {
				t.Fatal(err)
			}
			task, _, err := store.TaskForMessage(ctx, scope, root.Source.MessageID)
			if err != nil || task.Status != "completed" || task.Revision != 2 || model.calls != 2 || len(sender.texts) != 1 || strings.Contains(sender.texts[0], "旧上下文回答") || model.oldOnRetry {
				t.Fatalf("unsafe/absent recovery: revision=%d status=%s calls=%d sends=%d", task.Revision, task.Status, model.calls, len(sender.texts))
			}
			if !deleted && !model.sawFresh {
				t.Fatal("retry did not reload the updated context")
			}
		})
	}
}
