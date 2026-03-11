package config

import (
	"testing"

	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestBuildFeatureActionValueUsesStandardAction(t *testing.T) {
	payload := BuildFeatureActionValue(FeatureActionBlockChat, "debug", "chat-1", "user-1")
	if payload[cardaction.ActionField] != cardaction.ActionFeatureBlockChat {
		t.Fatalf("expected feature action name, got %q", payload[cardaction.ActionField])
	}
	if _, ok := payload[cardaction.LegacyTypeField]; ok {
		t.Fatalf("unexpected legacy type field")
	}
}

func TestBuildConfigActionValueUsesStandardAction(t *testing.T) {
	payload := BuildConfigActionValue(ConfigActionSet, "k", "1", "chat", "chat-1", "user-1")
	if payload[cardaction.ActionField] != cardaction.ActionConfigSet {
		t.Fatalf("expected config action name, got %q", payload[cardaction.ActionField])
	}
}

func TestBuildConfigDeleteActionValueUsesStandardAction(t *testing.T) {
	payload := BuildConfigActionValue(ConfigActionDelete, "k", "", "chat", "chat-1", "user-1")
	if payload[cardaction.ActionField] != cardaction.ActionConfigDelete {
		t.Fatalf("expected config delete action name, got %q", payload[cardaction.ActionField])
	}
	if _, ok := payload[cardaction.ValueField]; ok {
		t.Fatalf("did not expect value field for delete action")
	}
}

func TestBuildConfigFormActionValueUsesFormField(t *testing.T) {
	payload := BuildConfigFormActionValue("k", "chat", "chat-1", "user-1", "config_value_k")
	if payload[cardaction.ActionField] != cardaction.ActionConfigSet {
		t.Fatalf("expected config set action, got %q", payload[cardaction.ActionField])
	}
	if payload[cardaction.FormFieldField] != "config_value_k" {
		t.Fatalf("expected form field to be preserved")
	}
	if _, ok := payload[cardaction.ValueField]; ok {
		t.Fatalf("did not expect direct value field for form submit action")
	}
}

func TestParseFeatureActionRequest(t *testing.T) {
	req, err := ParseFeatureActionRequest(&cardaction.Parsed{
		Name: cardaction.ActionFeatureUnblockUser,
		Value: map[string]any{
			cardaction.FeatureField: "music",
			cardaction.UserIDField:  "user-1",
		},
	})
	if err != nil {
		t.Fatalf("ParseFeatureActionRequest() error = %v", err)
	}
	if req.Action != FeatureActionUnblockUser || req.Feature != "music" || req.OpenID != "user-1" {
		t.Fatalf("unexpected req: %+v", req)
	}
}

func TestParseConfigActionRequest(t *testing.T) {
	req, err := ParseConfigActionRequest(&cardaction.Parsed{
		Name: cardaction.ActionConfigSet,
		Value: map[string]any{
			cardaction.KeyField:    "mute_cnt",
			cardaction.ValueField:  "2",
			cardaction.ScopeField:  "chat",
			cardaction.ChatIDField: "chat-1",
		},
	})
	if err != nil {
		t.Fatalf("ParseConfigActionRequest() error = %v", err)
	}
	if req.Key != "mute_cnt" || req.Value != "2" || req.Scope != "chat" || req.ChatID != "chat-1" {
		t.Fatalf("unexpected req: %+v", req)
	}
}

func TestBuildConfigInputActionValueUsesStandardAction(t *testing.T) {
	payload := BuildConfigInputActionValue("k", "chat", "chat-1", "user-1")
	if payload[cardaction.ActionField] != cardaction.ActionConfigSet {
		t.Fatalf("expected config action name, got %q", payload[cardaction.ActionField])
	}
	if _, ok := payload[cardaction.ValueField]; ok {
		t.Fatalf("did not expect value field for input action")
	}
}

func TestParseConfigActionRequestUsesInputValue(t *testing.T) {
	req, err := ParseConfigActionRequest(&cardaction.Parsed{
		Name:       cardaction.ActionConfigSet,
		InputValue: "37",
		Value: map[string]any{
			cardaction.KeyField:    "mute_cnt",
			cardaction.ScopeField:  "chat",
			cardaction.ChatIDField: "chat-1",
		},
	})
	if err != nil {
		t.Fatalf("ParseConfigActionRequest() error = %v", err)
	}
	if req.Value != "37" {
		t.Fatalf("expected input value to be used, got %+v", req)
	}
}

func TestParseConfigDeleteActionRequest(t *testing.T) {
	req, err := ParseConfigActionRequest(&cardaction.Parsed{
		Name: cardaction.ActionConfigDelete,
		Value: map[string]any{
			cardaction.KeyField:   "mute_cnt",
			cardaction.ScopeField: "chat",
		},
	})
	if err != nil {
		t.Fatalf("ParseConfigActionRequest() error = %v", err)
	}
	if req.Action != ConfigActionDelete || req.Key != "mute_cnt" || req.Scope != "chat" {
		t.Fatalf("unexpected req: %+v", req)
	}
}

func TestParseConfigActionRequestReadsFormValue(t *testing.T) {
	req, err := ParseConfigActionRequest(&cardaction.Parsed{
		Name: cardaction.ActionConfigSet,
		Value: map[string]any{
			cardaction.KeyField:       "mute_cnt",
			cardaction.ScopeField:     "chat",
			cardaction.FormFieldField: "config_value_mute_cnt",
		},
		FormValue: map[string]any{
			"config_value_mute_cnt": "73",
		},
	})
	if err != nil {
		t.Fatalf("ParseConfigActionRequest() error = %v", err)
	}
	if req.Value != "73" {
		t.Fatalf("unexpected req: %+v", req)
	}
}

func TestParseConfigViewRequest(t *testing.T) {
	req, err := ParseConfigViewRequest(&cardaction.Parsed{
		Name: cardaction.ActionConfigViewScope,
		Value: map[string]any{
			cardaction.ScopeField:  "user",
			cardaction.ChatIDField: "chat-1",
			cardaction.UserIDField: "user-1",
		},
	})
	if err != nil {
		t.Fatalf("ParseConfigViewRequest() error = %v", err)
	}
	if req.Scope != "user" || req.ChatID != "chat-1" || req.OpenID != "user-1" {
		t.Fatalf("unexpected req: %+v", req)
	}
}

func TestBuildFeatureViewValueUsesStandardAction(t *testing.T) {
	payload := BuildFeatureViewValue("chat-1", "user-1")
	if payload[cardaction.ActionField] != cardaction.ActionFeatureView {
		t.Fatalf("expected feature view action, got %q", payload[cardaction.ActionField])
	}
	if payload[cardaction.ChatIDField] != "chat-1" || payload[cardaction.UserIDField] != "user-1" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestParseFeatureViewRequest(t *testing.T) {
	req, err := ParseFeatureViewRequest(&cardaction.Parsed{
		Name: cardaction.ActionFeatureView,
		Value: map[string]any{
			cardaction.ChatIDField: "chat-1",
			cardaction.UserIDField: "user-1",
		},
	})
	if err != nil {
		t.Fatalf("ParseFeatureViewRequest() error = %v", err)
	}
	if req.ChatID != "chat-1" || req.OpenID != "user-1" {
		t.Fatalf("unexpected req: %+v", req)
	}
}
