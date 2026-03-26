package agentruntime

import (
	"context"
	"testing"
	"time"
)

func TestClearFinishedRunStateTriggersPendingScopeSweep(t *testing.T) {
	sessionRepo := &replyCompletionTestSessionRepo{
		session: &AgentSession{
			ID:          "session_1",
			ChatID:      "oc_chat",
			ActiveRunID: "run_1",
		},
	}
	store := &replyCompletionTestCoordinationStore{
		activeRunID: "run_1",
	}
	coordinator := &RunCoordinator{
		sessionRepo:  sessionRepo,
		runtimeStore: store,
		activeRunTTL: time.Minute,
	}

	triggered := make(chan struct{}, 1)
	RegisterPendingScopeSweepTrigger(func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	})
	t.Cleanup(func() {
		RegisterPendingScopeSweepTrigger(nil)
	})

	err := coordinator.clearFinishedRunState(context.Background(), "run_1", "session_1", "ou_actor", time.Date(2026, 3, 24, 6, 3, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("clearFinishedRunState() error = %v", err)
	}
	if len(store.notifyCalls) != 1 || store.notifyCalls[0] != "oc_chat::ou_actor" {
		t.Fatalf("NotifyPendingInitialRun() calls = %+v, want [oc_chat::ou_actor]", store.notifyCalls)
	}
	select {
	case <-triggered:
	case <-time.After(time.Second):
		t.Fatal("expected clearFinishedRunState to trigger pending scope sweep")
	}
}

func TestClearCancelledRunStateTriggersPendingScopeSweep(t *testing.T) {
	sessionRepo := &replyCompletionTestSessionRepo{
		session: &AgentSession{
			ID:          "session_1",
			ChatID:      "oc_chat",
			ActiveRunID: "run_1",
		},
	}
	store := &replyCompletionTestCoordinationStore{
		activeRunID: "run_1",
	}
	coordinator := &RunCoordinator{
		sessionRepo:  sessionRepo,
		runtimeStore: store,
		activeRunTTL: time.Minute,
	}

	triggered := make(chan struct{}, 1)
	RegisterPendingScopeSweepTrigger(func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	})
	t.Cleanup(func() {
		RegisterPendingScopeSweepTrigger(nil)
	})

	err := coordinator.clearCancelledRunState(context.Background(), "run_1", "session_1", "ou_actor", time.Date(2026, 3, 24, 6, 4, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("clearCancelledRunState() error = %v", err)
	}
	if len(store.notifyCalls) != 1 || store.notifyCalls[0] != "oc_chat::ou_actor" {
		t.Fatalf("NotifyPendingInitialRun() calls = %+v, want [oc_chat::ou_actor]", store.notifyCalls)
	}
	select {
	case <-triggered:
	case <-time.After(time.Second):
		t.Fatal("expected clearCancelledRunState to trigger pending scope sweep")
	}
}

type replyCompletionTestSessionRepo struct {
	session *AgentSession
}

func (r *replyCompletionTestSessionRepo) FindOrCreateChatSession(context.Context, string, string, string) (*AgentSession, error) {
	return nil, nil
}

func (r *replyCompletionTestSessionRepo) GetByID(context.Context, string) (*AgentSession, error) {
	if r.session == nil {
		return nil, nil
	}
	copied := *r.session
	return &copied, nil
}

func (r *replyCompletionTestSessionRepo) SetActiveRun(_ context.Context, sessionID, runID, lastMessageID, lastActorOpenID string, updatedAt time.Time) error {
	if r.session != nil && r.session.ID == sessionID {
		r.session.ActiveRunID = runID
		r.session.LastMessageID = lastMessageID
		r.session.LastActorOpenID = lastActorOpenID
		r.session.UpdatedAt = updatedAt
	}
	return nil
}

type replyCompletionTestCoordinationStore struct {
	activeRunID string
	notifyCalls []string
}

func (s *replyCompletionTestCoordinationStore) ActiveChatRun(context.Context, string) (string, error) {
	return "", nil
}

func (s *replyCompletionTestCoordinationStore) SwapActiveChatRun(context.Context, string, string, string, time.Duration) (bool, error) {
	return false, nil
}

func (s *replyCompletionTestCoordinationStore) ActiveActorChatRun(context.Context, string, string) (string, error) {
	return s.activeRunID, nil
}

func (s *replyCompletionTestCoordinationStore) SwapActiveActorChatRun(_ context.Context, chatID, actorOpenID, expectedRunID, newRunID string, ttl time.Duration) (bool, error) {
	if s.activeRunID != expectedRunID {
		return false, nil
	}
	s.activeRunID = newRunID
	return true, nil
}

func (s *replyCompletionTestCoordinationStore) NextCancelGeneration(context.Context, string) (int64, error) {
	return 0, nil
}

func (s *replyCompletionTestCoordinationStore) NotifyPendingInitialRun(_ context.Context, chatID, actorOpenID string) error {
	s.notifyCalls = append(s.notifyCalls, chatID+"::"+actorOpenID)
	return nil
}

func (s *replyCompletionTestCoordinationStore) SaveApprovalReservation(context.Context, string, string, []byte, time.Duration) error {
	return nil
}

func (s *replyCompletionTestCoordinationStore) LoadApprovalReservation(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func (s *replyCompletionTestCoordinationStore) RecordApprovalReservationDecision(context.Context, string, string, []byte) ([]byte, error) {
	return nil, nil
}

func (s *replyCompletionTestCoordinationStore) ConsumeApprovalReservation(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
