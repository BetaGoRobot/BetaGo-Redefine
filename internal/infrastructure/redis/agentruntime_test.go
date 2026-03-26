package redis_dal

import (
	"context"
	"strings"
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

func TestAgentRuntimeActiveActorChatSlotIsIsolatedByActor(t *testing.T) {
	mr := mustRunMiniRedis(t)
	defer mr.Close()

	store := NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
	ctx := context.Background()

	swapped, err := store.SwapActiveActorChatRun(ctx, "oc_chat", "ou_actor_a", "", "run_actor_a_1", time.Minute)
	if err != nil {
		t.Fatalf("SwapActiveActorChatRun() actor_a error = %v", err)
	}
	if !swapped {
		t.Fatal("expected actor_a slot to accept first run")
	}

	swapped, err = store.SwapActiveActorChatRun(ctx, "oc_chat", "ou_actor_b", "", "run_actor_b_1", time.Minute)
	if err != nil {
		t.Fatalf("SwapActiveActorChatRun() actor_b error = %v", err)
	}
	if !swapped {
		t.Fatal("expected actor_b slot to be independent from actor_a")
	}

	activeA, err := store.ActiveActorChatRun(ctx, "oc_chat", "ou_actor_a")
	if err != nil {
		t.Fatalf("ActiveActorChatRun() actor_a error = %v", err)
	}
	if activeA != "run_actor_a_1" {
		t.Fatalf("ActiveActorChatRun() actor_a = %q, want %q", activeA, "run_actor_a_1")
	}

	activeB, err := store.ActiveActorChatRun(ctx, "oc_chat", "ou_actor_b")
	if err != nil {
		t.Fatalf("ActiveActorChatRun() actor_b error = %v", err)
	}
	if activeB != "run_actor_b_1" {
		t.Fatalf("ActiveActorChatRun() actor_b = %q, want %q", activeB, "run_actor_b_1")
	}
}

func TestAgentRuntimeExecutionLeaseHonorsConcurrencyAndExpiry(t *testing.T) {
	mr := mustRunMiniRedis(t)
	defer mr.Close()

	store := NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
	ctx := context.Background()

	acquired, err := store.AcquireExecutionLease(ctx, "oc_chat", "ou_actor", "run_1", 2*time.Second, 2)
	if err != nil {
		t.Fatalf("AcquireExecutionLease() run_1 error = %v", err)
	}
	if !acquired {
		t.Fatal("expected first execution lease acquisition to succeed")
	}

	acquired, err = store.AcquireExecutionLease(ctx, "oc_chat", "ou_actor", "run_2", 2*time.Second, 2)
	if err != nil {
		t.Fatalf("AcquireExecutionLease() run_2 error = %v", err)
	}
	if !acquired {
		t.Fatal("expected second execution lease acquisition to succeed")
	}

	acquired, err = store.AcquireExecutionLease(ctx, "oc_chat", "ou_actor", "run_3", 2*time.Second, 2)
	if err != nil {
		t.Fatalf("AcquireExecutionLease() run_3 error = %v", err)
	}
	if acquired {
		t.Fatal("expected third execution lease acquisition to fail when limit is reached")
	}

	count, err := store.ExecutionLeaseCount(ctx, "oc_chat", "ou_actor")
	if err != nil {
		t.Fatalf("ExecutionLeaseCount() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("ExecutionLeaseCount() = %d, want 2", count)
	}

	time.Sleep(2200 * time.Millisecond)

	count, err = store.ExecutionLeaseCount(ctx, "oc_chat", "ou_actor")
	if err != nil {
		t.Fatalf("ExecutionLeaseCount() after expiry error = %v", err)
	}
	if count != 0 {
		t.Fatalf("ExecutionLeaseCount() after expiry = %d, want 0", count)
	}
}

func TestAgentRuntimeExecutionLeaseRenewAndRelease(t *testing.T) {
	mr := mustRunMiniRedis(t)
	defer mr.Close()

	store := NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
	ctx := context.Background()

	acquired, err := store.AcquireExecutionLease(ctx, "oc_chat", "ou_actor", "run_1", 2*time.Second, 1)
	if err != nil {
		t.Fatalf("AcquireExecutionLease() error = %v", err)
	}
	if !acquired {
		t.Fatal("expected initial execution lease acquisition to succeed")
	}

	time.Sleep(1500 * time.Millisecond)

	renewed, err := store.RenewExecutionLease(ctx, "oc_chat", "ou_actor", "run_1", 2*time.Second)
	if err != nil {
		t.Fatalf("RenewExecutionLease() error = %v", err)
	}
	if !renewed {
		t.Fatal("expected execution lease renewal to succeed")
	}

	time.Sleep(1200 * time.Millisecond)

	count, err := store.ExecutionLeaseCount(ctx, "oc_chat", "ou_actor")
	if err != nil {
		t.Fatalf("ExecutionLeaseCount() after renew error = %v", err)
	}
	if count != 1 {
		t.Fatalf("ExecutionLeaseCount() after renew = %d, want 1", count)
	}

	released, err := store.ReleaseExecutionLease(ctx, "oc_chat", "ou_actor", "run_1")
	if err != nil {
		t.Fatalf("ReleaseExecutionLease() error = %v", err)
	}
	if !released {
		t.Fatal("expected execution lease release to succeed")
	}

	count, err = store.ExecutionLeaseCount(ctx, "oc_chat", "ou_actor")
	if err != nil {
		t.Fatalf("ExecutionLeaseCount() after release error = %v", err)
	}
	if count != 0 {
		t.Fatalf("ExecutionLeaseCount() after release = %d, want 0", count)
	}
}

func TestAgentRuntimePendingInitialRunQueueIsScopedAndFIFO(t *testing.T) {
	mr := mustRunMiniRedis(t)
	defer mr.Close()

	store := NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
	ctx := context.Background()

	position, err := store.EnqueuePendingInitialRun(ctx, "oc_chat", "ou_actor", []byte(`{"id":"req_1"}`), 3)
	if err != nil {
		t.Fatalf("EnqueuePendingInitialRun() req_1 error = %v", err)
	}
	if position != 1 {
		t.Fatalf("enqueue position = %d, want 1", position)
	}

	position, err = store.EnqueuePendingInitialRun(ctx, "oc_chat", "ou_actor", []byte(`{"id":"req_2"}`), 3)
	if err != nil {
		t.Fatalf("EnqueuePendingInitialRun() req_2 error = %v", err)
	}
	if position != 2 {
		t.Fatalf("enqueue position = %d, want 2", position)
	}

	if _, err := store.EnqueuePendingInitialRun(ctx, "oc_chat", "ou_actor", []byte(`{"id":"req_3"}`), 2); err == nil {
		t.Fatal("expected enqueue beyond max pending to fail")
	}

	if err := store.NotifyPendingInitialRun(ctx, "oc_chat", "ou_actor"); err != nil {
		t.Fatalf("NotifyPendingInitialRun() error = %v", err)
	}

	scopeChatID, scopeActorOpenID, err := store.DequeuePendingInitialScope(ctx, time.Second)
	if err != nil {
		t.Fatalf("DequeuePendingInitialScope() error = %v", err)
	}
	if scopeChatID != "oc_chat" || scopeActorOpenID != "ou_actor" {
		t.Fatalf("dequeued scope = chat=%q actor=%q, want chat=%q actor=%q", scopeChatID, scopeActorOpenID, "oc_chat", "ou_actor")
	}

	first, remaining, err := store.ConsumePendingInitialRun(ctx, "oc_chat", "ou_actor")
	if err != nil {
		t.Fatalf("ConsumePendingInitialRun() first error = %v", err)
	}
	if string(first) != `{"id":"req_1"}` {
		t.Fatalf("first payload = %s, want req_1", string(first))
	}
	if remaining != 1 {
		t.Fatalf("remaining count = %d, want 1", remaining)
	}

	second, remaining, err := store.ConsumePendingInitialRun(ctx, "oc_chat", "ou_actor")
	if err != nil {
		t.Fatalf("ConsumePendingInitialRun() second error = %v", err)
	}
	if string(second) != `{"id":"req_2"}` {
		t.Fatalf("second payload = %s, want req_2", string(second))
	}
	if remaining != 0 {
		t.Fatalf("remaining count = %d, want 0", remaining)
	}
}

func TestAgentRuntimePendingInitialScopeIndexTracksNonEmptyQueues(t *testing.T) {
	mr := mustRunMiniRedis(t)
	defer mr.Close()

	store := NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
	ctx := context.Background()

	if _, err := store.EnqueuePendingInitialRun(ctx, "oc_chat_a", "ou_actor_a", []byte(`{"id":"req_1"}`), 3); err != nil {
		t.Fatalf("EnqueuePendingInitialRun() first error = %v", err)
	}
	if _, err := store.EnqueuePendingInitialRun(ctx, "oc_chat_a", "ou_actor_a", []byte(`{"id":"req_2"}`), 3); err != nil {
		t.Fatalf("EnqueuePendingInitialRun() second error = %v", err)
	}
	if _, err := store.EnqueuePendingInitialRun(ctx, "oc_chat_b", "ou_actor_b", []byte(`{"id":"req_3"}`), 3); err != nil {
		t.Fatalf("EnqueuePendingInitialRun() third error = %v", err)
	}

	count, err := store.PendingInitialRunCount(ctx, "oc_chat_a", "ou_actor_a")
	if err != nil {
		t.Fatalf("PendingInitialRunCount() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("PendingInitialRunCount() = %d, want 2", count)
	}

	scopes, nextCursor, err := store.ListPendingInitialScopes(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListPendingInitialScopes() error = %v", err)
	}
	if nextCursor != 0 {
		t.Fatalf("ListPendingInitialScopes() nextCursor = %d, want 0", nextCursor)
	}
	if len(scopes) != 2 {
		t.Fatalf("ListPendingInitialScopes() len = %d, want 2", len(scopes))
	}
	if !containsPendingScope(scopes, "oc_chat_a", "ou_actor_a") {
		t.Fatalf("ListPendingInitialScopes() = %+v, want contain oc_chat_a/ou_actor_a", scopes)
	}
	if !containsPendingScope(scopes, "oc_chat_b", "ou_actor_b") {
		t.Fatalf("ListPendingInitialScopes() = %+v, want contain oc_chat_b/ou_actor_b", scopes)
	}

	if _, _, err := store.ConsumePendingInitialRun(ctx, "oc_chat_a", "ou_actor_a"); err != nil {
		t.Fatalf("ConsumePendingInitialRun() first error = %v", err)
	}
	if err := store.ClearPendingInitialScopeIfEmpty(ctx, "oc_chat_a", "ou_actor_a"); err != nil {
		t.Fatalf("ClearPendingInitialScopeIfEmpty() with remaining queue error = %v", err)
	}
	scopes, _, err = store.ListPendingInitialScopes(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListPendingInitialScopes() after partial consume error = %v", err)
	}
	if !containsPendingScope(scopes, "oc_chat_a", "ou_actor_a") {
		t.Fatalf("ListPendingInitialScopes() after partial consume = %+v, want contain oc_chat_a/ou_actor_a", scopes)
	}

	if _, _, err := store.ConsumePendingInitialRun(ctx, "oc_chat_a", "ou_actor_a"); err != nil {
		t.Fatalf("ConsumePendingInitialRun() second error = %v", err)
	}
	if err := store.ClearPendingInitialScopeIfEmpty(ctx, "oc_chat_a", "ou_actor_a"); err != nil {
		t.Fatalf("ClearPendingInitialScopeIfEmpty() final error = %v", err)
	}
	scopes, _, err = store.ListPendingInitialScopes(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListPendingInitialScopes() after final consume error = %v", err)
	}
	if containsPendingScope(scopes, "oc_chat_a", "ou_actor_a") {
		t.Fatalf("ListPendingInitialScopes() after final consume = %+v, want no oc_chat_a/ou_actor_a", scopes)
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

func TestAgentRuntimeApprovalReservationStoresDecisionAndConsumes(t *testing.T) {
	mr := mustRunMiniRedis(t)
	defer mr.Close()

	store := NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
	ctx := context.Background()

	const (
		stepID = "step_reserved"
		token  = "approval_token"
	)
	reservationPayload := []byte(`{"run_id":"run_approval","step_id":"step_reserved","token":"approval_token","approval_type":"side_effect","title":"审批发送消息","summary":"将向群里发送一条消息","capability_name":"send_message","payload_json":{"content":"hello"},"requested_at":"2026-03-23T10:00:00Z","expires_at":"2026-03-23T10:15:00Z"}`)
	if err := store.SaveApprovalReservation(ctx, stepID, token, reservationPayload, 15*time.Minute); err != nil {
		t.Fatalf("SaveApprovalReservation() error = %v", err)
	}

	recorded, err := store.RecordApprovalReservationDecision(ctx, stepID, token, []byte(`{"outcome":"approved","actor_open_id":"ou_reviewer","occurred_at":"2026-03-23T10:01:00Z"}`))
	if err != nil {
		t.Fatalf("RecordApprovalReservationDecision() error = %v", err)
	}
	if string(recorded) == "" || !containsAll(string(recorded), `"decision"`, `"approved"`, `"ou_reviewer"`) {
		t.Fatalf("recorded reservation = %s, want embedded decision", string(recorded))
	}

	loaded, err := store.LoadApprovalReservation(ctx, stepID, token)
	if err != nil {
		t.Fatalf("LoadApprovalReservation() error = %v", err)
	}
	if string(loaded) == "" || !containsAll(string(loaded), `"decision"`, `"approved"`, `"ou_reviewer"`) {
		t.Fatalf("loaded reservation = %s, want persisted decision", string(loaded))
	}

	consumed, err := store.ConsumeApprovalReservation(ctx, stepID, token)
	if err != nil {
		t.Fatalf("ConsumeApprovalReservation() error = %v", err)
	}
	if string(consumed) == "" || !containsAll(string(consumed), `"decision"`, `"approved"`, `"ou_reviewer"`) {
		t.Fatalf("consumed reservation = %s, want decision preserved", string(consumed))
	}

	loaded, err = store.LoadApprovalReservation(ctx, stepID, token)
	if err != nil {
		t.Fatalf("LoadApprovalReservation() after consume error = %v", err)
	}
	if loaded != nil {
		t.Fatalf("LoadApprovalReservation() after consume = %+v, want nil", loaded)
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

func containsAll(raw string, values ...string) bool {
	for _, value := range values {
		if !strings.Contains(raw, value) {
			return false
		}
	}
	return true
}

func containsPendingScope(scopes []PendingInitialScope, chatID, actorOpenID string) bool {
	for _, scope := range scopes {
		if scope.ChatID == chatID && scope.ActorOpenID == actorOpenID {
			return true
		}
	}
	return false
}
