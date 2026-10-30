package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
)

func TestPresentationJobsUseTheExistingFencedQueueWithoutModelOrOrdinarySend(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	kind := domain.JobKind("presentation")
	id, err := f.store.Enqueue(ctx, domain.JobSpec{Scope: f.scope, Kind: kind, Payload: json.RawMessage(`{"domain":"music","session_id":"card-session"}`), DedupeKey: "presentation-event"})
	if err != nil {
		t.Fatalf("presentation must use existing durable queue: %v", err)
	}
	calls := 0
	svc, err := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: f.messenger, Owner: "presentation-runtime", Lease: time.Second, Poll: time.Millisecond, Presentation: func(ctx context.Context, j domain.Job) error {
		calls++
		if j.ID != id || j.Kind != kind || j.Scope != f.scope || j.Owner != "presentation-runtime" || j.Fence < 1 {
			t.Fatal("presentation callback lost the actual leased job identity")
		}
		return f.store.CheckClaim(ctx, j)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Step(ctx, kind); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || f.modelCalls.Load() != 0 || len(f.messenger.sends) != 0 {
		t.Fatal("presentation triggered model or ordinary final delivery")
	}
	if err = svc.Step(ctx, kind); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("completed presentation was claimed twice")
	}
}

func TestPresentationWithoutRegisteredHandlerFailsClosed(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	kind := domain.JobKind("presentation")
	_, err := f.store.Enqueue(ctx, domain.JobSpec{Scope: f.scope, Kind: kind, Payload: json.RawMessage(`{}`), DedupeKey: "no-presentation-handler"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.Step(ctx, kind); err != nil {
		t.Fatal(err)
	}
	if f.modelCalls.Load() != 0 || len(f.messenger.sends) != 0 {
		t.Fatal("unregistered presentation must not reach model or send")
	}
}

func TestSlowPresentationDoesNotOccupyOrdinaryConversationWorker(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	stopped := make(chan error, 1)
	svc, err := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: f.messenger, Owner: "separate-pools", Concurrency: 1, MaxConcurrentTasks: 1, Lease: time.Second, Poll: time.Millisecond, Presentation: func(ctx context.Context, j domain.Job) error { close(started); <-ctx.Done(); return ctx.Err() }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Enqueue(ctx, domain.JobSpec{Scope: f.scope, Kind: domain.JobPresentation, Payload: json.RawMessage(`{}`), DedupeKey: "slow-music"})
	if err != nil {
		t.Fatal(err)
	}
	go func() { stopped <- svc.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Error("runtime worker did not join on shutdown")
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("presentation worker never started")
	}
	f.event(t, "ordinary-during-audio", "other", "普通群消息", "", domain.IngressMetadata{Kind: "message", SenderType: "user"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		err = f.db.QueryRow(`SELECT status FROM "` + f.schema + `".jobs WHERE kind='route'`).Scan(&state)
		if err == nil && state == "completed" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("slow music resources blocked the ordinary conversation route")
}
