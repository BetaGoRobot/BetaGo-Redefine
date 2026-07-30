package cardaction

import (
	"errors"
	"reflect"
	"testing"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func TestParseAgentRuntimeEnvelopeAndCallbackInputs(t *testing.T) {
	event := newCardActionEvent(map[string]any{
		ActionField: ActionAgentRuntimeResume, RunIDField: "run-1",
		StepIDField: "step-1", InteractionIDField: "interaction-1",
		RevisionField: float64(3), TokenField: "opaque-token",
		InteractionKindField: "agent_card", ContinueAgentField: true,
		ActionIDField: "confirm",
	}, map[string]any{
		"reason": "looks good", "choices": []any{"a", "b"},
	})
	event.Event.Action.Tag = "multi_select_static"
	event.Event.Action.Name = "choices"
	event.Event.Action.InputValue = "typed"
	event.Event.Action.Option = "a"
	event.Event.Action.Options = []string{"a", "b"}
	event.Event.Action.Checked = true
	event.Event.Context = &callback.Context{
		OpenMessageID: "om-card", OpenChatID: "oc-chat",
	}
	event.Event.Operator = &callback.Operator{OpenID: "ou-actor"}
	event.EventV2Base = &larkevent.EventV2Base{
		Header: &larkevent.EventHeader{EventID: "event-1"},
	}

	parsed, err := Parse(event)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	wantRuntime := &RuntimeEnvelope{
		RunID: "run-1", StepID: "step-1", InteractionID: "interaction-1",
		Revision: 3, Token: "opaque-token", InteractionKind: "agent_card",
		ContinueAgent: true, ActionID: "confirm",
	}
	if !reflect.DeepEqual(parsed.Runtime, wantRuntime) {
		t.Fatalf("runtime = %#v, want %#v", parsed.Runtime, wantRuntime)
	}
	if parsed.Source.EventID != "event-1" ||
		parsed.Source.MessageID != "om-card" ||
		parsed.Source.ChatID != "oc-chat" ||
		parsed.Source.OperatorOpenID != "ou-actor" {
		t.Fatalf("source = %#v", parsed.Source)
	}
	if parsed.InputValue != "typed" || parsed.SelectedOption() != "a" ||
		!reflect.DeepEqual(parsed.Options, []string{"a", "b"}) ||
		!parsed.Checked ||
		!reflect.DeepEqual(parsed.FormValue["choices"], []any{"a", "b"}) {
		t.Fatalf("callback inputs = %#v", parsed)
	}
}

func TestParseRejectsPartialOrMalformedRuntimeEnvelope(t *testing.T) {
	partial := map[string]any{
		ActionField: ActionAgentRuntimeResume,
		RunIDField:  "run-1",
	}
	if _, err := Parse(newCardActionEvent(partial, nil)); !errors.Is(
		err,
		ErrPartialRuntimeEnvelope,
	) {
		t.Fatalf("partial Parse() error = %v", err)
	}
	malformed := map[string]any{
		ActionField: ActionAgentRuntimeResume, RunIDField: "run-1",
		StepIDField: "step-1", InteractionIDField: "interaction-1",
		RevisionField: 1.5, TokenField: "token",
		InteractionKindField: "agent_card", ContinueAgentField: true,
		ActionIDField: "confirm",
	}
	if _, err := Parse(newCardActionEvent(malformed, nil)); !errors.Is(
		err,
		ErrMalformedRuntimeEnvelope,
	) {
		t.Fatalf("malformed Parse() error = %v", err)
	}
}

func TestRuntimeEnvelopeFieldNamesAndValues(t *testing.T) {
	value := map[string]any{
		ActionField:          "test.runtime.resume",
		RunIDField:           "run-1",
		StepIDField:          "step-1",
		InteractionIDField:   "interaction-1",
		RevisionField:        "3",
		TokenField:           "opaque-token",
		InteractionKindField: "capability_confirm",
		ContinueAgentField:   "true",
	}

	parsed, err := Parse(newCardActionEvent(value, nil))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !reflect.DeepEqual(parsed.Value, value) {
		t.Fatalf("Parse() value = %#v, want %#v", parsed.Value, value)
	}

	wantFields := map[string]string{
		"run":              RunIDField,
		"step":             StepIDField,
		"interaction":      InteractionIDField,
		"revision":         RevisionField,
		"token":            TokenField,
		"interaction_kind": InteractionKindField,
		"continue_agent":   ContinueAgentField,
	}
	wantValues := map[string]string{
		"run":              "run_id",
		"step":             "step_id",
		"interaction":      "interaction_id",
		"revision":         "revision",
		"token":            "token",
		"interaction_kind": "interaction_kind",
		"continue_agent":   "continue_agent",
	}
	if !reflect.DeepEqual(wantFields, wantValues) {
		t.Fatalf("runtime field constants = %#v, want %#v", wantFields, wantValues)
	}
}

func TestParseLegacyPayloadWithoutRuntimeEnvelope(t *testing.T) {
	value := map[string]any{
		LegacyTypeField: "song",
		IDField:         "legacy-song",
	}

	parsed, err := Parse(newCardActionEvent(value, nil))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Name != ActionMusicPlay {
		t.Fatalf("Parse() name = %q, want %q", parsed.Name, ActionMusicPlay)
	}
	if _, ok := parsed.String(RunIDField); ok {
		t.Fatalf("legacy payload unexpectedly contains %q", RunIDField)
	}
}

func TestParsePrefersStandardActionField(t *testing.T) {
	parsed, err := Parse(newCardActionEvent(
		map[string]any{
			ActionField:     ActionMusicPlay,
			LegacyTypeField: "song",
			IDField:         "123",
		},
		nil,
	))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Name != ActionMusicPlay {
		t.Fatalf("expected %q, got %q", ActionMusicPlay, parsed.Name)
	}
}

func TestParseMapsLegacyActionType(t *testing.T) {
	parsed, err := Parse(newCardActionEvent(
		map[string]any{
			LegacyTypeField: "refresh_obj",
			CommandField:    "/todo list",
		},
		nil,
	))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Name != ActionCommandRefresh {
		t.Fatalf("expected %q, got %q", ActionCommandRefresh, parsed.Name)
	}
}

func TestParseMapsLegacyFeatureActionType(t *testing.T) {
	parsed, err := Parse(newCardActionEvent(
		map[string]any{
			LegacyTypeField: "feature_action",
			ActionField:     "block_chat",
			FeatureField:    "debug",
		},
		nil,
	))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Name != ActionFeatureBlockChat {
		t.Fatalf("expected %q, got %q", ActionFeatureBlockChat, parsed.Name)
	}
}

func TestParseMapsLegacyConfigActionType(t *testing.T) {
	parsed, err := Parse(newCardActionEvent(
		map[string]any{
			LegacyTypeField: "config_action",
			ActionField:     "set",
			KeyField:        "rate_limit",
		},
		nil,
	))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Name != ActionConfigSet {
		t.Fatalf("expected %q, got %q", ActionConfigSet, parsed.Name)
	}
}

func TestParseUsesFormFallback(t *testing.T) {
	parsed, err := Parse(newCardActionEvent(
		map[string]any{
			CommandField: "/todo stats",
		},
		map[string]any{
			"start_time_picker": "2025-01-01 00:00 +0800",
		},
	))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Name != ActionCommandSubmitTimeRange {
		t.Fatalf("expected %q, got %q", ActionCommandSubmitTimeRange, parsed.Name)
	}
}

func TestParseKeepsInputMetadata(t *testing.T) {
	parsed, err := Parse(&callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Action: &callback.CallBackAction{
				Tag:        "input",
				Name:       "rate_input",
				InputValue: "37",
				Value: map[string]any{
					ActionField: ActionConfigSet,
					KeyField:    "mute_cnt",
					ScopeField:  "chat",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Tag != "input" || parsed.NameField != "rate_input" || parsed.InputValue != "37" {
		t.Fatalf("unexpected parsed metadata: %+v", parsed)
	}
}

func TestBuilderUsesActionField(t *testing.T) {
	payload := New(ActionMusicLyrics).WithID("42").Payload()

	if payload[ActionField] != ActionMusicLyrics {
		t.Fatalf("expected action field %q, got %q", ActionMusicLyrics, payload[ActionField])
	}
	if _, ok := payload[LegacyTypeField]; ok {
		t.Fatalf("unexpected legacy type field in payload")
	}
	if payload[IDField] != "42" {
		t.Fatalf("expected id field to be preserved")
	}
}

func TestBuilderAddsContinuationEnvelopeFields(t *testing.T) {
	payload := New(ActionAgentRuntimeResume).
		WithInteractionID("interaction-1").
		WithInteractionKind("capability_confirm").
		WithContinueAgent(true).
		Payload()

	if payload[InteractionIDField] != "interaction-1" {
		t.Fatalf("interaction ID = %q, want %q", payload[InteractionIDField], "interaction-1")
	}
	if payload[InteractionKindField] != "capability_confirm" {
		t.Fatalf("interaction kind = %q, want %q", payload[InteractionKindField], "capability_confirm")
	}
	if payload[ContinueAgentField] != "true" {
		t.Fatalf("continue agent = %q, want %q", payload[ContinueAgentField], "true")
	}

	payload = New(ActionAgentRuntimeReject).WithContinueAgent(false).Payload()
	if payload[ContinueAgentField] != "false" {
		t.Fatalf("continue agent = %q, want %q", payload[ContinueAgentField], "false")
	}
}

func newCardActionEvent(value, formValue map[string]any) *callback.CardActionTriggerEvent {
	return &callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Action: &callback.CallBackAction{
				Value:     value,
				FormValue: formValue,
			},
		},
	}
}
