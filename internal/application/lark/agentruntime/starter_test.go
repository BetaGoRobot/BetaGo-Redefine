package agentruntime

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type starterCoordinatorStub struct {
	result   StartScheduleEditInteractionResult
	requests []CreateScheduleEditInteractionRequest
}

func (s *starterCoordinatorStub) CreateScheduleEditInteraction(
	_ context.Context,
	req CreateScheduleEditInteractionRequest,
) (StartScheduleEditInteractionResult, error) {
	req.TrustedInput = append(json.RawMessage(nil), req.TrustedInput...)
	req.Projection.Payload = append(json.RawMessage(nil), req.Projection.Payload...)
	s.requests = append(s.requests, req)
	return s.result, nil
}

func TestDurableScheduleEditStarterPersistsStableSecretFreeInteraction(t *testing.T) {
	createdAt := time.Date(2026, time.July, 29, 9, 30, 0, 0, time.UTC)
	store := &starterCoordinatorStub{result: StartScheduleEditInteractionResult{
		RunID:         "run_1",
		StepID:        "step_wait_1",
		InteractionID: "interaction_schedule_1",
		Revision:      1,
		ExpiresAt:     createdAt.Add(15 * time.Minute),
	}}
	starter, err := NewDurableScheduleEditStarter(DurableScheduleEditStarterOptions{
		Store:           store,
		AppID:           "app_1",
		BotOpenID:       "bot_1",
		TokenSecret:     []byte("stable-test-secret"),
		WaitTTL:         15 * time.Minute,
		ProjectionIndex: "conversation-events",
	})
	if err != nil {
		t.Fatalf("NewDurableScheduleEditStarter() error = %v", err)
	}
	request := StartScheduleEditRequest{
		TaskID:          "task_1",
		ActorOpenID:     "actor_1",
		ChatID:          "chat_1",
		SourceMessageID: "message_1",
		NewValues:       map[string]any{"name": "new task name"},
	}

	first, err := starter.StartScheduleEdit(context.Background(), request)
	if err != nil {
		t.Fatalf("first StartScheduleEdit() error = %v", err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("first envelope is invalid: %v", err)
	}
	if first.InteractionKind != "schedule_edit" {
		t.Fatalf("InteractionKind = %q, want schedule_edit", first.InteractionKind)
	}

	// Simulate process restart: the atomic store returns the same persisted
	// interaction, including its original expiry.
	second, err := starter.StartScheduleEdit(context.Background(), request)
	if err != nil {
		t.Fatalf("replayed StartScheduleEdit() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replayed envelope = %#v, want %#v", second, first)
	}

	if len(store.requests) != 2 {
		t.Fatalf("CreateScheduleEditInteraction calls = %d, want 2", len(store.requests))
	}
	if !reflect.DeepEqual(store.requests[0], store.requests[1]) {
		t.Fatalf("replayed interaction request changed:\nfirst:  %#v\nsecond: %#v", store.requests[0], store.requests[1])
	}
	persisted := store.requests[0]
	if persisted.WaitTTL != 15*time.Minute {
		t.Fatalf("WaitTTL = %s, want 15m", persisted.WaitTTL)
	}
	if persisted.TokenHash != HashInteractionToken(first.Token) {
		t.Fatalf("TokenHash does not match returned token")
	}
	if strings.Contains(string(persisted.TrustedInput), first.Token) {
		t.Fatal("trusted input contains plaintext interaction token")
	}
	if strings.Contains(string(persisted.Projection.Payload), first.Token) {
		t.Fatal("projection payload contains plaintext interaction token")
	}
	if strings.Contains(string(persisted.Projection.Payload), string(persisted.TrustedInput)) {
		t.Fatal("projection payload contains trusted schedule edit input")
	}

	start := persisted.Run
	if start.AppID != "app_1" || start.BotOpenID != "bot_1" ||
		start.ChatID != request.ChatID || start.ActorOpenID != request.ActorOpenID ||
		start.TriggerMessageID != request.SourceMessageID ||
		start.TriggerType != TriggerTypeShadow {
		t.Fatalf("StartRunRequest = %#v, want durable shadow run identity", start)
	}
}

func TestDurableScheduleEditStarterRequiresSourceMessageID(t *testing.T) {
	store := &starterCoordinatorStub{result: StartScheduleEditInteractionResult{
		RunID: "run_1", StepID: "step_1", InteractionID: "interaction_1",
		Revision: 1, ExpiresAt: time.Now().Add(time.Minute),
	}}
	starter, err := NewDurableScheduleEditStarter(DurableScheduleEditStarterOptions{
		Store: store, AppID: "app_1", BotOpenID: "bot_1",
		TokenSecret: []byte("stable-test-secret"), WaitTTL: time.Minute,
		ProjectionIndex: "conversation-events",
	})
	if err != nil {
		t.Fatalf("NewDurableScheduleEditStarter() error = %v", err)
	}
	_, err = starter.StartScheduleEdit(context.Background(), StartScheduleEditRequest{
		TaskID: "task_1", ActorOpenID: "actor_1", ChatID: "chat_1",
		NewValues: map[string]any{"name": "new name"},
	})
	if err == nil {
		t.Fatal("StartScheduleEdit() error = nil, want missing source message error")
	}
	if len(store.requests) != 0 {
		t.Fatalf("atomic store calls = %d, want 0", len(store.requests))
	}
}

func TestNewDurableScheduleEditStarterRejectsMissingDependencies(t *testing.T) {
	_, err := NewDurableScheduleEditStarter(DurableScheduleEditStarterOptions{})
	if err == nil {
		t.Fatal("NewDurableScheduleEditStarter() error = nil, want configuration error")
	}
}
