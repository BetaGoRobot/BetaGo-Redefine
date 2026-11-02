package larkeventstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type memoryStore struct {
	events    []Event
	locations map[string]Location
	updates   []Resolution
	err       error
}

func (s *memoryStore) SaveEvent(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return s.err
}
func (s *memoryStore) UpdateResolution(_ context.Context, scope Scope, eventID string, result Resolution) error {
	s.updates = append(s.updates, result)
	return s.err
}
func (s *memoryStore) PutLocation(_ context.Context, loc Location) error {
	if s.locations == nil {
		s.locations = make(map[string]Location)
	}
	s.locations[loc.AppID+"/"+loc.BotOpenID+"/"+loc.MessageID] = loc
	return s.err
}
func (s *memoryStore) FindLocation(_ context.Context, scope Scope, messageID string) (Location, bool, error) {
	loc, ok := s.locations[scope.AppID+"/"+scope.BotOpenID+"/"+messageID]
	return loc, ok, s.err
}

func testReaction() *larkim.P2MessageReactionCreatedV1 {
	mid, actor, op, emoji, action := "om_target", "ou_actor", "user", "THUMBSUP", "1793346959000"
	return &larkim.P2MessageReactionCreatedV1{
		EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{EventID: "event-1", AppID: "cli_bot", EventType: "im.message.reaction.created_v1"}},
		Event:       &larkim.P2MessageReactionCreatedV1Data{MessageId: &mid, UserId: &larkim.UserId{OpenId: &actor}, OperatorType: &op, ReactionType: &larkim.Emoji{EmojiType: &emoji}, ActionTime: &action},
	}
}

func TestCaptureKeepsActorAndActionBeforeLookupFailure(t *testing.T) {
	s := &memoryStore{}
	r := NewRecorder(s)
	p := sdktrace.NewTracerProvider()
	defer p.Shutdown(context.Background())
	ctx, span := p.Tracer("test").Start(context.Background(), "reaction")
	defer span.End()
	e := r.CaptureReaction(ctx, Scope{AppID: "cli_bot", BotOpenID: "ou_bot"}, testReaction())
	if len(s.events) != 1 || e.ActorOpenID != "ou_actor" || e.OperatorType != "user" || e.ActionTime != "1793346959000" || e.OccurredAt == nil || !e.OccurredAt.Equal(time.UnixMilli(1793346959000)) {
		t.Fatalf("event lost actor/action before resolution: %#v", e)
	}
	if e.TraceID != trace.SpanContextFromContext(ctx).TraceID().String() {
		t.Fatalf("trace changed: %s", e.TraceID)
	}
	calls := 0
	result := r.Resolve(ctx, e.Scope(), e.MessageID, func(got context.Context, _ string) (string, error) {
		calls++
		if trace.SpanContextFromContext(got).TraceID() != trace.SpanContextFromContext(ctx).TraceID() {
			t.Fatal("resolution detached trace")
		}
		return "", errors.New("msg:The message is invisible to the operator.,code:230050")
	})
	r.RecordResolution(ctx, e.Scope(), e.EventID, result)
	if calls != 1 || result.Status != "unresolved" || len(s.updates) != 1 || s.events[0].ActorOpenID != "ou_actor" {
		t.Fatalf("failed resolution lost journal: %#v", result)
	}
}

func TestKnownLocationSurvivesRemoteInvisibilityAndIsScoped(t *testing.T) {
	s := &memoryStore{}
	r := NewRecorder(s)
	scope := Scope{AppID: "cli_a", BotOpenID: "ou_a"}
	r.ObserveMessage(context.Background(), scope, "om_target", "oc_known", false)
	calls := 0
	remote := func(context.Context, string) (string, error) { calls++; return "", errors.New("230050") }
	got := r.Resolve(context.Background(), scope, "om_target", remote)
	if got.ChatID != "oc_known" || got.Source != "locator" || calls != 0 {
		t.Fatalf("known location did not bypass remote: %#v calls=%d", got, calls)
	}
	other := r.Resolve(context.Background(), Scope{AppID: "cli_b", BotOpenID: "ou_b"}, "om_target", remote)
	if other.ChatID != "" || calls != 1 {
		t.Fatalf("cross-bot location leak: %#v", other)
	}
}

func TestCompleteRawEventRedactsSecretsAndPreservesUnknownFields(t *testing.T) {
	event := testReaction()
	event.EventReq = &larkevent.EventReq{Body: []byte(`{"schema":"2.0","header":{"event_id":"event-1","token":"verification-secret","app_id":"cli_bot"},"event":{"message_id":"om_target","user_id":{"open_id":"ou_actor"},"action_time":"1793346959000","extra":{"chat_id":"oc_extra","Authorization":"bearer-secret","nested":{"app_secret":"app-secret","Cookie":"cookie-secret"}}}}`)}
	e := NewRecorder(&memoryStore{}).CaptureReaction(context.Background(), Scope{AppID: "cli_bot", BotOpenID: "ou_bot"}, event)
	for _, secret := range []string{"verification-secret", "bearer-secret", "app-secret", "cookie-secret"} {
		if strings.Contains(e.Payload, secret) {
			t.Fatalf("payload leaked credential %q", secret)
		}
	}
	if !strings.Contains(e.Payload, "oc_extra") || !strings.Contains(e.Payload, "ou_actor") || len(e.Payload) <= 256 {
		t.Fatalf("complete event was discarded: %s", e.Payload)
	}
}

func TestMalformedAndMissingActionTimeDoNotInventTime(t *testing.T) {
	r := NewRecorder(&memoryStore{})
	if e := r.CaptureReaction(context.Background(), Scope{}, nil); e.EventID != "" {
		t.Fatalf("nil event became valid: %#v", e)
	}
	event := testReaction()
	bad := "invalid"
	event.Event.ActionTime = &bad
	e := r.CaptureReaction(context.Background(), Scope{AppID: "cli_bot", BotOpenID: "ou_bot"}, event)
	if e.OccurredAt != nil || e.ActionTime != "invalid" {
		t.Fatalf("invented action time: %#v", e)
	}
}

func TestRecallRetainsMessageGroupAndRecallTime(t *testing.T) {
	mid, cid, at, kind := "om_recalled", "oc_known", "1793346959000", "message_owner"
	s := &memoryStore{}
	r := NewRecorder(s)
	scope := Scope{AppID: "cli_bot", BotOpenID: "ou_bot"}
	e := r.CaptureRecall(context.Background(), scope, &larkim.P2MessageRecalledV1{
		EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{EventID: "recall-1", EventType: "im.message.recalled_v1"}},
		Event:       &larkim.P2MessageRecalledV1Data{MessageId: &mid, ChatId: &cid, RecallTime: &at, RecallType: &kind},
	})
	loc, ok, _ := s.FindLocation(context.Background(), scope, mid)
	if e.ChatID != cid || e.ActionTime != at || !ok || !loc.Recalled {
		t.Fatalf("recall provenance missing: %#v %#v", e, loc)
	}
}

func TestRawEventChatIDIsUsedEvenBeforeSDKModelsIt(t *testing.T) {
	event := testReaction()
	event.EventReq = &larkevent.EventReq{Body: []byte(`{"header":{"event_id":"event-1"},"event":{"message_id":"om_target","chat_id":"oc_raw"}}`)}
	s := &memoryStore{}
	r := NewRecorder(s)
	scope := Scope{AppID: "cli_bot", BotOpenID: "ou_bot"}
	e := r.CaptureReaction(context.Background(), scope, event)
	if e.ChatID != "oc_raw" {
		t.Fatalf("unmodeled chat ID lost: %#v", e)
	}
	got := r.Resolve(context.Background(), scope, e.MessageID, func(context.Context, string) (string, error) {
		t.Fatal("raw chat ID should avoid remote query")
		return "", nil
	})
	if got.ChatID != "oc_raw" {
		t.Fatalf("raw event did not populate locator: %#v", got)
	}
}

func TestLocationObservationRespectsPrivateModeExclusion(t *testing.T) {
	s := &memoryStore{}
	r := NewRecorder(s)
	r.AllowChat = func(context.Context, string) bool { return false }
	r.ObserveMessage(context.Background(), Scope{AppID: "cli_bot", BotOpenID: "ou_bot"}, "om_private", "oc_private", false)
	if len(s.locations) != 0 {
		t.Fatal("private-mode message metadata was retained")
	}
}

func TestStoreFailureDoesNotLoseReturnedEvent(t *testing.T) {
	s := &memoryStore{err: errors.New("database unavailable")}
	e := NewRecorder(s).CaptureReaction(context.Background(), Scope{AppID: "cli_bot", BotOpenID: "ou_bot"}, testReaction())
	if e.ActorOpenID != "ou_actor" || e.ActionTime != "1793346959000" || !strings.Contains(e.Payload, "ou_actor") {
		t.Fatalf("failed persistence lost diagnostic data: %#v", e)
	}
}

func TestErrorDiagnosticsRedactWholeCredentials(t *testing.T) {
	for _, input := range []string{
		"Authorization: Bearer actual-secret\ncode:230050",
		`HTTP response {"access_token":"actual-secret","code":230050}`,
		"Cookie: first=actual-secret; second=also-secret\ncode:230050",
		"GET https://user:actual-secret@example.test/api?api_key=also-secret code:230050",
	} {
		got, code := safeError(errors.New(input))
		if strings.Contains(got, "actual-secret") || strings.Contains(got, "also-secret") {
			t.Errorf("credential leaked from diagnostic: %s", got)
		}
		if strings.Contains(input, "code:230050") && code != 230050 {
			t.Errorf("lost provider code: %d", code)
		}
	}
}

func TestRawEventRedactsCredentialStringsInArrays(t *testing.T) {
	event := testReaction()
	event.EventReq = &larkevent.EventReq{Body: []byte(`{"event":{"message_id":"om_target","extra":["Authorization: Bearer actual-secret","{\"access_token\":\"also-secret\"}"]}}`)}
	e := NewRecorder(nil).CaptureReaction(context.Background(), Scope{AppID: "cli_bot", BotOpenID: "ou_bot"}, event)
	if strings.Contains(e.Payload, "actual-secret") || strings.Contains(e.Payload, "also-secret") {
		t.Fatal("credential string inside array was not redacted")
	}
}

func TestEmbeddedJSONRetainsExactLargeNumericIDs(t *testing.T) {
	event := testReaction()
	event.EventReq = &larkevent.EventReq{Body: []byte(`{"event":{"message_id":"om_target","extra":"{\"opaque_id\":9007199254740993,\"access_token\":\"sample-secret\"}"}}`)}
	e := NewRecorder(nil).CaptureReaction(context.Background(), Scope{AppID: "cli_bot", BotOpenID: "ou_bot"}, event)
	if !strings.Contains(e.Payload, "9007199254740993") || strings.Contains(e.Payload, "sample-secret") {
		t.Fatal("embedded JSON lost exact non-secret ID or retained token")
	}
}
