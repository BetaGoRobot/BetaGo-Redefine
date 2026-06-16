package agentruntime

import (
	"context"
	"errors"
	"testing"
)

func TestRunCoordinatorStartShadowRunCreatesSessionRunAndPlanStep(t *testing.T) {
	store := newMemoryStore()
	coordinator := NewRunCoordinator(store)

	result, err := coordinator.StartShadowRun(context.Background(), StartRunRequest{
		AppID:            "cli_app",
		BotOpenID:        "ou_bot",
		ChatID:           "oc_chat",
		ScopeType:        ScopeTypeChat,
		ScopeID:          "oc_chat",
		TriggerType:      TriggerTypeMention,
		TriggerMessageID: "om_1",
		ActorOpenID:      "ou_actor",
		Goal:             "answer",
		InputText:        "hello",
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}
	if result == nil || result.Session == nil || result.Run == nil {
		t.Fatalf("StartShadowRun() result incomplete: %+v", result)
	}
	if result.Run.Status != RunStatusQueued {
		t.Fatalf("run status = %q, want %q", result.Run.Status, RunStatusQueued)
	}
	if result.Session.ActiveRunID != result.Run.ID {
		t.Fatalf("session active run = %q, want %q", result.Session.ActiveRunID, result.Run.ID)
	}
	if len(store.steps) != 1 {
		t.Fatalf("step count = %d, want 1", len(store.steps))
	}
	if store.steps[0].Kind != StepKindPlan || store.steps[0].Status != StepStatusCompleted {
		t.Fatalf("initial step = %+v, want completed plan step", store.steps[0])
	}
}

func TestRunCoordinatorStartShadowRunIsIdempotentBySessionAndMessage(t *testing.T) {
	store := newMemoryStore()
	coordinator := NewRunCoordinator(store)
	req := StartRunRequest{
		AppID:            "cli_app",
		BotOpenID:        "ou_bot",
		ChatID:           "oc_chat",
		ScopeType:        ScopeTypeChat,
		ScopeID:          "oc_chat",
		TriggerType:      TriggerTypeMention,
		TriggerMessageID: "om_1",
		ActorOpenID:      "ou_actor",
		Goal:             "answer",
		InputText:        "hello",
	}

	first, err := coordinator.StartShadowRun(context.Background(), req)
	if err != nil {
		t.Fatalf("first StartShadowRun() error = %v", err)
	}
	second, err := coordinator.StartShadowRun(context.Background(), req)
	if err != nil {
		t.Fatalf("second StartShadowRun() error = %v", err)
	}
	if first.Run.ID != second.Run.ID {
		t.Fatalf("duplicate run created: first=%q second=%q", first.Run.ID, second.Run.ID)
	}
	if len(store.runs) != 1 {
		t.Fatalf("run count = %d, want 1", len(store.runs))
	}
	if len(store.steps) != 1 {
		t.Fatalf("step count = %d, want 1", len(store.steps))
	}
}

type memoryStore struct {
	sessions map[string]*AgentSession
	runs     map[string]*AgentRun
	steps    []*AgentStep
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		sessions: make(map[string]*AgentSession),
		runs:     make(map[string]*AgentRun),
	}
}

func (s *memoryStore) GetOrCreateSession(_ context.Context, session *AgentSession) (*AgentSession, error) {
	if existing, ok := s.sessions[session.ID]; ok {
		return cloneSession(existing), nil
	}
	s.sessions[session.ID] = cloneSession(session)
	return cloneSession(session), nil
}

func (s *memoryStore) FindRunBySessionAndTriggerMessage(_ context.Context, sessionID, messageID string) (*AgentRun, error) {
	for _, run := range s.runs {
		if run.SessionID == sessionID && run.TriggerMessageID == messageID {
			return cloneRun(run), nil
		}
	}
	return nil, ErrNotFound
}

func (s *memoryStore) CreateRun(_ context.Context, run *AgentRun) error {
	if _, ok := s.runs[run.ID]; ok {
		return errors.New("duplicate run")
	}
	s.runs[run.ID] = cloneRun(run)
	return nil
}

func (s *memoryStore) UpdateSessionActiveRun(_ context.Context, sessionID, runID, lastMessageID, lastActorOpenID string) (*AgentSession, error) {
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	session.ActiveRunID = runID
	session.LastMessageID = lastMessageID
	session.LastActorOpenID = lastActorOpenID
	return cloneSession(session), nil
}

func (s *memoryStore) CreateStep(_ context.Context, step *AgentStep) error {
	s.steps = append(s.steps, cloneStep(step))
	return nil
}
