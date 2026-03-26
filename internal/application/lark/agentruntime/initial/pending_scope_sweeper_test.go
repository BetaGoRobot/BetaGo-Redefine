package initial

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakePendingScopeSweepStore struct {
	mu sync.Mutex

	scopes []PendingScope
	counts map[string]int64
	active map[string]int64

	notifyCalls []string
	clearCalls  []string
}

func newFakePendingScopeSweepStore() *fakePendingScopeSweepStore {
	return &fakePendingScopeSweepStore{
		counts: make(map[string]int64),
		active: make(map[string]int64),
	}
}

func (s *fakePendingScopeSweepStore) ListPendingInitialScopes(context.Context, uint64, int64) ([]PendingScope, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]PendingScope, len(s.scopes))
	copy(result, s.scopes)
	return result, 0, nil
}

func (s *fakePendingScopeSweepStore) PendingInitialRunCount(ctx context.Context, chatID, actorOpenID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[s.scopeKey(chatID, actorOpenID)], nil
}

func (s *fakePendingScopeSweepStore) ActiveExecutionLeaseCount(ctx context.Context, chatID, actorOpenID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[s.scopeKey(chatID, actorOpenID)], nil
}

func (s *fakePendingScopeSweepStore) NotifyPendingInitialRun(ctx context.Context, chatID, actorOpenID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifyCalls = append(s.notifyCalls, s.scopeKey(chatID, actorOpenID))
	return nil
}

func (s *fakePendingScopeSweepStore) ClearPendingInitialScopeIfEmpty(ctx context.Context, chatID, actorOpenID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.scopeKey(chatID, actorOpenID)
	s.clearCalls = append(s.clearCalls, key)
	if s.counts[key] == 0 {
		filtered := s.scopes[:0]
		for _, scope := range s.scopes {
			if s.scopeKey(scope.ChatID, scope.ActorOpenID) != key {
				filtered = append(filtered, scope)
			}
		}
		s.scopes = filtered
	}
	return nil
}

func (s *fakePendingScopeSweepStore) scopeKey(chatID, actorOpenID string) string {
	return chatID + "::" + actorOpenID
}

func TestPendingScopeSweeperNotifiesRunnableIndexedScope(t *testing.T) {
	store := newFakePendingScopeSweepStore()
	store.scopes = []PendingScope{{ChatID: "oc_chat", ActorOpenID: "ou_actor"}}
	store.counts["oc_chat::ou_actor"] = 1

	sweeper := NewPendingScopeSweeper(store)
	sweeper.sweepOnce(context.Background())

	if got := len(store.notifyCalls); got != 1 {
		t.Fatalf("NotifyPendingInitialRun() calls = %d, want 1", got)
	}
	if store.notifyCalls[0] != "oc_chat::ou_actor" {
		t.Fatalf("NotifyPendingInitialRun() scope = %q, want %q", store.notifyCalls[0], "oc_chat::ou_actor")
	}
	if len(store.clearCalls) != 0 {
		t.Fatalf("ClearPendingInitialScopeIfEmpty() calls = %+v, want none", store.clearCalls)
	}
}

func TestPendingScopeSweeperClearsStaleIndexedScopeWhenQueueEmpty(t *testing.T) {
	store := newFakePendingScopeSweepStore()
	store.scopes = []PendingScope{{ChatID: "oc_chat", ActorOpenID: "ou_actor"}}

	sweeper := NewPendingScopeSweeper(store)
	sweeper.sweepOnce(context.Background())

	if len(store.notifyCalls) != 0 {
		t.Fatalf("NotifyPendingInitialRun() calls = %+v, want none", store.notifyCalls)
	}
	if len(store.clearCalls) != 1 || store.clearCalls[0] != "oc_chat::ou_actor" {
		t.Fatalf("ClearPendingInitialScopeIfEmpty() calls = %+v, want [oc_chat::ou_actor]", store.clearCalls)
	}
	if len(store.scopes) != 0 {
		t.Fatalf("remaining indexed scopes = %+v, want empty", store.scopes)
	}
}

func TestPendingScopeSweeperSkipsBusyScope(t *testing.T) {
	store := newFakePendingScopeSweepStore()
	store.scopes = []PendingScope{{ChatID: "oc_chat", ActorOpenID: "ou_actor"}}
	store.counts["oc_chat::ou_actor"] = 1
	store.active["oc_chat::ou_actor"] = 2

	sweeper := NewPendingScopeSweeper(store)
	sweeper.sweepOnce(context.Background())

	if len(store.notifyCalls) != 0 {
		t.Fatalf("NotifyPendingInitialRun() calls = %+v, want none", store.notifyCalls)
	}
	if len(store.clearCalls) != 0 {
		t.Fatalf("ClearPendingInitialScopeIfEmpty() calls = %+v, want none", store.clearCalls)
	}
}

func TestPendingScopeSweeperTriggerRunsSweepWithoutWaitingTick(t *testing.T) {
	store := newFakePendingScopeSweepStore()
	store.scopes = []PendingScope{{ChatID: "oc_chat", ActorOpenID: "ou_actor"}}
	store.counts["oc_chat::ou_actor"] = 1

	sweeper := NewPendingScopeSweeper(store)
	sweeper.interval = time.Hour
	sweeper.Start()
	defer sweeper.Stop()

	sweeper.Trigger()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		notifyCalls := append([]string(nil), store.notifyCalls...)
		store.mu.Unlock()
		if len(notifyCalls) == 1 {
			if notifyCalls[0] != "oc_chat::ou_actor" {
				t.Fatalf("NotifyPendingInitialRun() scope = %q, want %q", notifyCalls[0], "oc_chat::ou_actor")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	t.Fatalf("NotifyPendingInitialRun() calls = %+v, want [oc_chat::ou_actor]", store.notifyCalls)
}
