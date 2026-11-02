package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
)

type turnBudgetModel struct {
	budget  time.Duration
	block   bool
	recover bool
}

func (m *turnBudgetModel) Generate(ctx context.Context, _ modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	if deadline, ok := ctx.Deadline(); ok {
		m.budget = time.Until(deadline)
	}
	if m.block {
		<-ctx.Done()
		return modelapi.ChatResponse{}, ctx.Err()
	}
	if m.recover {
		return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: "连接恢复，继续回答。"}}, nil
	}
	return modelapi.ChatResponse{}, errors.New("synthetic transport failure")
}

func TestTurnDeadlineReleasesAttemptAndNextAttemptCanDeliver(t *testing.T) {
	f := setup(t)
	model := &turnBudgetModel{block: true}
	f.svc = budgetService(t, f, model, func(o *service.Options) { o.TurnTimeout = 500 * time.Millisecond })
	f.event(t, "timeout-request", "owner", "@bot 你怎么看", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	ctx := context.Background()
	started := time.Now()
	if err := f.svc.Step(ctx, domain.JobAgentTurn); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout lost classification: %v", err)
	}
	if time.Since(started) > 2*time.Second || ctx.Err() != nil {
		t.Fatal("turn timeout consumed parent or failed to bound attempt")
	}
	var status string
	if err := f.db.QueryRow(`SELECT status FROM "` + f.schema + `".jobs WHERE kind='agent_turn'`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("failed to release timed-out attempt: %s %v", status, err)
	}
	model.block, model.recover = false, true
	f.svc = budgetService(t, f, model, nil)
	if _, err := f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at=clock_timestamp() WHERE kind='agent_turn'`); err != nil {
		t.Fatal(err)
	}
	step(t, f, domain.JobAgentTurn)
	step(t, f, domain.JobDelivery)
	if len(f.messenger.sends) != 1 {
		t.Fatalf("retry did not produce exactly one delivery: %d", len(f.messenger.sends))
	}
}

func TestTurnBudgetRespectsEarlierParentDeadline(t *testing.T) {
	f := setup(t)
	model := &turnBudgetModel{}
	f.svc = budgetService(t, f, model, nil)
	f.event(t, "parent-request", "owner", "@bot 你怎么看", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.svc.Step(ctx, domain.JobAgentTurn); err == nil {
		t.Fatal("expected synthetic failure")
	}
	if model.budget <= 0 || model.budget > time.Second || ctx.Err() != nil {
		t.Fatalf("parent deadline not respected: budget=%v parent=%v", model.budget, ctx.Err())
	}
}

func budgetService(t *testing.T, f fixture, model *turnBudgetModel, configure func(*service.Options)) *service.Service {
	t.Helper()
	reg, err := atoms.New(atoms.Options{Tasks: f.store, Sources: f.store, Messenger: f.messenger})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(engine.Options{Store: f.store, Model: model, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	opts := service.Options{Store: f.store, Engine: eng, Messenger: f.messenger, Owner: "budget-fixture", TaskTimeout: time.Hour, Poll: time.Millisecond}
	if configure != nil {
		configure(&opts)
	}
	svc, err := service.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestAgentTurnHasExecutionBudgetSeparateFromDurableTaskTTL(t *testing.T) {
	f := setup(t)
	model := &turnBudgetModel{}
	f.svc = budgetService(t, f, model, nil)
	m := f.event(t, "budget-request", "owner", "@bot 你怎么看", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	ctx := context.Background()
	if err := f.svc.Step(ctx, domain.JobAgentTurn); err == nil {
		t.Fatal("expected synthetic transport failure")
	}
	task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if model.budget <= 0 || model.budget > 2*time.Minute {
		t.Fatalf("single turn lacks two-minute execution budget: %v", model.budget)
	}
	if time.Until(task.ExpiresAt) < 59*time.Minute {
		t.Fatalf("execution timeout changed durable TTL: %v", task.ExpiresAt)
	}
	var status string
	if err = f.db.QueryRow(`SELECT status FROM "` + f.schema + `".jobs WHERE kind='agent_turn'`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("failed attempt was not acknowledged for retry: status=%s err=%v", status, err)
	}
}
