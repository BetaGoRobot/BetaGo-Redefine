package agentcard

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

type authoringRunStoreFake struct {
	session *agentruntime.AgentSession
	active  *agentruntime.AgentRun
	runs    map[string]*agentruntime.AgentRun
}

func (s *authoringRunStoreFake) GetOrCreateSession(
	_ context.Context,
	session *agentruntime.AgentSession,
) (*agentruntime.AgentSession, error) {
	if s.session == nil {
		copy := *session
		s.session = &copy
	}
	copy := *s.session
	return &copy, nil
}

func (s *authoringRunStoreFake) FindRunBySessionAndTriggerMessage(
	_ context.Context,
	sessionID, messageID string,
) (*agentruntime.AgentRun, error) {
	for _, run := range s.runs {
		if run.SessionID == sessionID && run.TriggerMessageID == messageID {
			copy := *run
			return &copy, nil
		}
	}
	return nil, agentruntime.ErrNotFound
}

func (s *authoringRunStoreFake) CreateRun(
	_ context.Context,
	run *agentruntime.AgentRun,
) error {
	if s.runs == nil {
		s.runs = make(map[string]*agentruntime.AgentRun)
	}
	copy := *run
	s.runs[run.ID] = &copy
	return nil
}

func (*authoringRunStoreFake) CreateStep(
	context.Context,
	*agentruntime.AgentStep,
) error {
	return nil
}

func (s *authoringRunStoreFake) UpdateSessionActiveRun(
	_ context.Context,
	sessionID, runID, messageID, actorOpenID string,
) (*agentruntime.AgentSession, error) {
	s.session.ID = sessionID
	s.session.ActiveRunID = runID
	s.session.LastMessageID = messageID
	s.session.LastActorOpenID = actorOpenID
	s.active = s.runs[runID]
	copy := *s.session
	return &copy, nil
}

func (s *authoringRunStoreFake) FindActiveRun(
	context.Context,
	string,
) (*agentruntime.AgentRun, error) {
	if s.active == nil {
		return nil, agentruntime.ErrNotFound
	}
	copy := *s.active
	return &copy, nil
}

func TestDurableAuthoringRunResolverReusesActiveRunnableRun(t *testing.T) {
	active := &agentruntime.AgentRun{
		ID: "run-active", Status: agentruntime.RunStatusRunning,
		TriggerMessageID: "message-1",
	}
	store := &authoringRunStoreFake{active: active}
	resolver, err := NewDurableAuthoringRunResolver(
		DurableAuthoringRunResolverOptions{
			Store: store, AppID: "app-1", BotOpenID: "bot-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.ResolveAuthoringRun(
		context.Background(),
		agentcardtool.ComposeContext{
			ChatID: "chat-1", ActorOpenID: "actor-1",
			ReplyToMessageID: "message-1", TriggerEventID: "event-1",
		},
		"choose",
	)
	if err != nil || got.ID != active.ID || len(store.runs) != 0 {
		t.Fatalf("ResolveAuthoringRun() = %#v, %v", got, err)
	}
}

func TestDurableAuthoringRunResolverCreatesIdempotentShadowRun(t *testing.T) {
	store := &authoringRunStoreFake{}
	resolver, err := NewDurableAuthoringRunResolver(
		DurableAuthoringRunResolverOptions{
			Store: store, AppID: "app-1", BotOpenID: "bot-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := agentcardtool.ComposeContext{
		ChatID: "chat-1", ActorOpenID: "actor-1",
		ReplyToMessageID: "message-1", TriggerEventID: "event-1",
	}
	first, err := resolver.ResolveAuthoringRun(
		context.Background(),
		request,
		"choose",
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolver.ResolveAuthoringRun(
		context.Background(),
		request,
		"choose",
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || second.ID != first.ID || len(store.runs) != 1 {
		t.Fatalf("runs first=%#v second=%#v count=%d", first, second, len(store.runs))
	}
}
