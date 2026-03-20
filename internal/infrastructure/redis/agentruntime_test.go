package redis_dal

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestAgentRuntimeRunLockAcquireAndRelease(t *testing.T) {
	mr := mustRunMiniRedis(t)
	defer mr.Close()

	store := NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
	ctx := context.Background()

	acquired, err := store.AcquireRunLock(ctx, "run_01", "owner_a", time.Minute)
	if err != nil {
		t.Fatalf("AcquireRunLock() error = %v", err)
	}
	if !acquired {
		t.Fatal("expected first AcquireRunLock() to succeed")
	}

	acquired, err = store.AcquireRunLock(ctx, "run_01", "owner_b", time.Minute)
	if err != nil {
		t.Fatalf("AcquireRunLock() second call error = %v", err)
	}
	if acquired {
		t.Fatal("expected second AcquireRunLock() to fail for another owner")
	}

	released, err := store.ReleaseRunLock(ctx, "run_01", "owner_a")
	if err != nil {
		t.Fatalf("ReleaseRunLock() error = %v", err)
	}
	if !released {
		t.Fatal("expected ReleaseRunLock() to succeed for lock owner")
	}
}

func TestAgentRuntimeActiveChatSlotCompareAndSwap(t *testing.T) {
	mr := mustRunMiniRedis(t)
	defer mr.Close()

	store := NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
	ctx := context.Background()

	swapped, err := store.SwapActiveChatRun(ctx, "oc_chat", "", "run_01", time.Minute)
	if err != nil {
		t.Fatalf("SwapActiveChatRun() error = %v", err)
	}
	if !swapped {
		t.Fatal("expected empty slot to accept first run")
	}

	active, err := store.ActiveChatRun(ctx, "oc_chat")
	if err != nil {
		t.Fatalf("ActiveChatRun() error = %v", err)
	}
	if active != "run_01" {
		t.Fatalf("ActiveChatRun() = %q, want %q", active, "run_01")
	}

	swapped, err = store.SwapActiveChatRun(ctx, "oc_chat", "", "run_02", time.Minute)
	if err != nil {
		t.Fatalf("SwapActiveChatRun() second call error = %v", err)
	}
	if swapped {
		t.Fatal("expected compare-and-swap with wrong expected run to fail")
	}
}

func TestAgentRuntimeResumeQueueAndCancelGeneration(t *testing.T) {
	mr := mustRunMiniRedis(t)
	defer mr.Close()

	store := NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
	ctx := context.Background()

	generation, err := store.NextCancelGeneration(ctx, "run_03")
	if err != nil {
		t.Fatalf("NextCancelGeneration() error = %v", err)
	}
	if generation != 1 {
		t.Fatalf("NextCancelGeneration() = %d, want 1", generation)
	}

	event := ResumeEvent{
		RunID:    "run_03",
		Revision: 2,
		Source:   "callback",
		Token:    "cb_token",
	}
	if err := store.EnqueueResumeEvent(ctx, event); err != nil {
		t.Fatalf("EnqueueResumeEvent() error = %v", err)
	}

	dequeued, err := store.DequeueResumeEvent(ctx, time.Second)
	if err != nil {
		t.Fatalf("DequeueResumeEvent() error = %v", err)
	}
	if dequeued == nil || dequeued.RunID != event.RunID || dequeued.Revision != event.Revision || dequeued.Source != event.Source || dequeued.Token != event.Token {
		t.Fatalf("DequeueResumeEvent() = %+v, want %+v", dequeued, event)
	}
}

func mustRunMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	return mr
}
