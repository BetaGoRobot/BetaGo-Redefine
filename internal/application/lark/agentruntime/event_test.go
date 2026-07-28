package agentruntime

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEventTypeValues(t *testing.T) {
	tests := map[EventType]string{
		EventTypeMessage:          "message",
		EventTypeCardAction:       "card_action",
		EventTypeCapabilityResult: "capability_result",
		EventTypeSchedule:         "schedule",
		EventTypeAsyncResult:      "async_result",
		EventTypeTimeout:          "timeout",
	}
	for eventType, want := range tests {
		if got := string(eventType); got != want {
			t.Errorf("EventType = %q, want %q", got, want)
		}
	}
}

func TestRuntimeEnvelopeValidateAcceptsCompleteEnvelope(t *testing.T) {
	envelope := RuntimeEnvelope{
		RunID:           "run_1",
		StepID:          "step_1",
		InteractionID:   "ix_1",
		Revision:        1,
		Token:           "secret",
		InteractionKind: "approval",
		ContinueAgent:   true,
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestRuntimeEnvelopeValidateRejectsInvalidFields(t *testing.T) {
	valid := RuntimeEnvelope{
		RunID:           "run_1",
		StepID:          "step_1",
		InteractionID:   "ix_1",
		Revision:        1,
		Token:           "secret",
		InteractionKind: "approval",
		ContinueAgent:   true,
	}
	tests := []struct {
		name   string
		mutate func(*RuntimeEnvelope)
	}{
		{name: "missing run id", mutate: func(e *RuntimeEnvelope) { e.RunID = "" }},
		{name: "missing step id", mutate: func(e *RuntimeEnvelope) { e.StepID = "" }},
		{name: "missing interaction id", mutate: func(e *RuntimeEnvelope) { e.InteractionID = "" }},
		{name: "zero revision", mutate: func(e *RuntimeEnvelope) { e.Revision = 0 }},
		{name: "negative revision", mutate: func(e *RuntimeEnvelope) { e.Revision = -1 }},
		{name: "missing token", mutate: func(e *RuntimeEnvelope) { e.Token = "" }},
		{name: "continue agent false", mutate: func(e *RuntimeEnvelope) { e.ContinueAgent = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope := valid
			tt.mutate(&envelope)
			if err := envelope.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want rejection")
			}
		})
	}
}

func TestRuntimeEnvelopeJSONFields(t *testing.T) {
	data, err := json.Marshal(RuntimeEnvelope{
		RunID:           "run_1",
		StepID:          "step_1",
		InteractionID:   "ix_1",
		Revision:        2,
		Token:           "token",
		InteractionKind: "approval",
		ContinueAgent:   true,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	want := []string{
		"run_id",
		"step_id",
		"interaction_id",
		"revision",
		"token",
		"interaction_kind",
		"continue_agent",
	}
	if len(fields) != len(want) {
		t.Fatalf("JSON fields = %v, want exactly %v", fields, want)
	}
	for _, field := range want {
		if _, ok := fields[field]; !ok {
			t.Errorf("JSON missing field %q", field)
		}
	}
}

func TestConversationEventJSONFields(t *testing.T) {
	data, err := json.Marshal(ConversationEvent{
		ID:            "evt_1",
		Type:          EventTypeCardAction,
		ChatID:        "oc_1",
		ActorOpenID:   "ou_1",
		RunID:         "run_1",
		InteractionID: "ix_1",
		Revision:      3,
		Action:        "confirm",
		SourceRef:     "source_1",
		OccurredAt:    time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC),
		Payload:       json.RawMessage(`{"approved":true}`),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	got := make(map[string]struct{}, len(fields))
	for key := range fields {
		got[key] = struct{}{}
	}
	want := map[string]struct{}{
		"id": {}, "type": {}, "chat_id": {}, "actor_open_id": {},
		"run_id": {}, "interaction_id": {}, "revision": {}, "action": {},
		"source_ref": {}, "occurred_at": {}, "payload": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ConversationEvent JSON keys = %v, want exactly %v", got, want)
	}
	for _, legacyKey := range []string{"ID", "ChatID", "RunID"} {
		if _, exists := fields[legacyKey]; exists {
			t.Errorf("ConversationEvent JSON unexpectedly contains %q", legacyKey)
		}
	}
}

func TestConversationEventDedupeKeyForCardAction(t *testing.T) {
	event := ConversationEvent{
		Type:          EventTypeCardAction,
		RunID:         "run_1",
		InteractionID: "ix_1",
		Revision:      3,
		Action:        "confirm",
	}
	if got, want := event.DedupeKey(), "card_action:ix_1:3:confirm"; got != want {
		t.Fatalf("DedupeKey() = %q, want %q", got, want)
	}
}

func TestConversationEventDedupeKeyPrefersStableSourceRef(t *testing.T) {
	first := ConversationEvent{
		ID:        "evt_1",
		Type:      EventTypeMessage,
		SourceRef: " om_42 ",
		Payload:   json.RawMessage(`{"attempt":1}`),
	}
	retry := ConversationEvent{
		ID:        "evt_2",
		Type:      EventTypeCardAction,
		SourceRef: "om_42",
		Payload:   json.RawMessage(`{"attempt":2}`),
	}
	if got, want := first.DedupeKey(), "source:om_42"; got != want {
		t.Fatalf("first DedupeKey() = %q, want %q", got, want)
	}
	if got, want := retry.DedupeKey(), first.DedupeKey(); got != want {
		t.Fatalf("retry DedupeKey() = %q, want stable key %q", got, want)
	}
}

func TestConversationEventDedupeKeysAreStableAndDoNotEmptyCollide(t *testing.T) {
	tests := []struct {
		name  string
		event ConversationEvent
	}{
		{
			name:  "message id",
			event: ConversationEvent{ID: "evt_message", Type: EventTypeMessage},
		},
		{
			name: "capability result",
			event: ConversationEvent{
				Type: EventTypeCapabilityResult, RunID: "run_1",
				Payload: json.RawMessage(`{"capability":"search","result":"ok"}`),
			},
		},
		{
			name: "schedule",
			event: ConversationEvent{
				Type: EventTypeSchedule, RunID: "run_1",
				OccurredAt: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "async result",
			event: ConversationEvent{
				Type: EventTypeAsyncResult, InteractionID: "ix_async", Revision: 2,
			},
		},
		{
			name: "timeout",
			event: ConversationEvent{
				Type: EventTypeTimeout, InteractionID: "ix_timeout", Revision: 2,
			},
		},
	}
	seen := make(map[string]string, len(tests))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := tt.event.DedupeKey()
			second := tt.event.DedupeKey()
			if first == "" {
				t.Fatal("DedupeKey() = empty")
			}
			if first != second {
				t.Fatalf("DedupeKey() unstable: first=%q second=%q", first, second)
			}
			if previous, exists := seen[first]; exists {
				t.Fatalf("DedupeKey() collision with %q: %q", previous, first)
			}
			seen[first] = tt.name
		})
	}
}

func TestCloneHelpersPreserveConversationRuntimeFields(t *testing.T) {
	now := time.Date(2026, 7, 28, 13, 0, 0, 0, time.UTC)
	run := &AgentRun{
		ID:               "run_1",
		ActivationSource: "mention",
		TopicFingerprint: "topic_1",
		LastRelevantAt:   now,
	}
	step := &AgentStep{
		ID:             "step_1",
		DedupeKey:      "message:evt_1",
		AttemptCount:   2,
		WorkerID:       "worker_1",
		LeaseExpiresAt: now.Add(time.Minute),
		RetryOfStepID:  "step_0",
	}
	if got := cloneRun(run); !reflect.DeepEqual(got, run) || got == run {
		t.Fatalf("cloneRun() = %+v, want distinct full clone %+v", got, run)
	}
	if got := cloneStep(step); !reflect.DeepEqual(got, step) || got == step {
		t.Fatalf("cloneStep() = %+v, want distinct full clone %+v", got, step)
	}
	if strings.TrimSpace(step.DedupeKey) == "" {
		t.Fatal("test fixture requires non-empty step dedupe key")
	}
}
