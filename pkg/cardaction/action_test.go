package cardaction

import (
	"testing"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

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
