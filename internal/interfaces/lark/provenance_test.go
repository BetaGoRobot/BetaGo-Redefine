package lark

import (
	"context"
	"errors"
	"testing"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/larkeventstore"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ingressProvenanceStore struct {
	events      []larkeventstore.Event
	locations   []larkeventstore.Location
	resolutions []larkeventstore.Resolution
}

func (s *ingressProvenanceStore) SaveEvent(_ context.Context, e larkeventstore.Event) error {
	s.events = append(s.events, e)
	return nil
}
func (s *ingressProvenanceStore) UpdateResolution(_ context.Context, _ larkeventstore.Scope, _ string, r larkeventstore.Resolution) error {
	s.resolutions = append(s.resolutions, r)
	return nil
}
func (s *ingressProvenanceStore) PutLocation(_ context.Context, l larkeventstore.Location) error {
	s.locations = append(s.locations, l)
	return nil
}
func (s *ingressProvenanceStore) FindLocation(context.Context, larkeventstore.Scope, string) (larkeventstore.Location, bool, error) {
	return larkeventstore.Location{}, false, nil
}

type rejectingProvenanceSubmitter struct{ onSubmit func() }

func (s rejectingProvenanceSubmitter) Submit(context.Context, string, func(context.Context) error) error {
	if s.onSubmit != nil {
		s.onSubmit()
	}
	return errors.New("queue full")
}

func setupProvenanceStore(t *testing.T) *ingressProvenanceStore {
	t.Helper()
	installHandlerTestConfig(t)
	c := infraConfig.Get().LarkConfig
	previous := *c
	c.AppID = "cli_fixture"
	c.BotOpenID = "ou_fixture"
	s := &ingressProvenanceStore{}
	old := larkeventstore.Default()
	larkeventstore.SetDefault(larkeventstore.NewRecorder(s))
	t.Cleanup(func() { larkeventstore.SetDefault(old); *c = previous })
	return s
}

func TestReactionIsJournaledBeforeQueueRejection(t *testing.T) {
	s := setupProvenanceStore(t)
	h := NewHandlerSet(HandlerSetOptions{ReactionExecutor: rejectingProvenanceSubmitter{onSubmit: func() {
		if len(s.events) != 1 {
			t.Error("queue admission preceded durable event capture")
		}
	}}})
	e := newReactionEvent("om_queue")
	actor, op, at := "ou_actor", "user", "1793346959000"
	e.Event.UserId = &larkim.UserId{OpenId: &actor}
	e.Event.OperatorType = &op
	e.Event.ActionTime = &at
	e.EventV2Base = &larkevent.EventV2Base{Header: &larkevent.EventHeader{EventID: "event_queue"}}
	_ = h.MessageReactionHandler(context.Background(), e)
	if len(s.events) != 1 || s.events[0].ActorOpenID != actor || s.events[0].ActionTime != at {
		t.Fatalf("queue rejection lost provenance: %#v", s.events)
	}
	if len(s.resolutions) != 1 || s.resolutions[0].Status != "enqueue_failed" {
		t.Fatal("queue failure was not retained with event")
	}
}

func TestInboundLocationPrecedesOptionalProcessing(t *testing.T) {
	s := setupProvenanceStore(t)
	h := NewHandlerSet(HandlerSetOptions{MessageExecutor: rejectingProvenanceSubmitter{onSubmit: func() {
		if len(s.locations) != 1 {
			t.Error("message processing admission preceded group capture")
		}
	}}})
	if err := h.MessageV2Handler(context.Background(), newMessageEvent("om_inbound")); err == nil {
		t.Fatal("expected fixture queue rejection")
	}
	if len(s.locations) != 1 || s.locations[0].MessageID != "om_inbound" || s.locations[0].ChatID != "oc_test_chat" {
		t.Fatalf("inbound group missing: %#v", s.locations)
	}
}

func TestRecallHandlerRetainsGroupInsteadOfDiscardingEvent(t *testing.T) {
	s := setupProvenanceStore(t)
	mid, cid, at := "om_recalled", "oc_recalled", "1793346959000"
	e := &larkim.P2MessageRecalledV1{EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{EventID: "event_recall"}}, Event: &larkim.P2MessageRecalledV1Data{MessageId: &mid, ChatId: &cid, RecallTime: &at}}
	if err := NewHandlerSet(HandlerSetOptions{}).MessageRecalledHandler(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(s.events) != 1 || len(s.locations) != 1 || !s.locations[0].Recalled || s.events[0].ChatID != cid {
		t.Fatalf("recall provenance missing: %#v %#v", s.events, s.locations)
	}
}
