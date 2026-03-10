package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveConfigDisplayValueForGlobalScope(t *testing.T) {
	value, source := resolveConfigDisplayValue(
		"global",
		KeyReactionDefaultRate,
		"chat-1",
		"user-1",
		func(candidate configLookupCandidate, key ConfigKey) (string, bool) {
			if candidate.scope == ScopeGlobal {
				return "88", true
			}
			return "", false
		},
		func(key ConfigKey) string { return "30" },
	)

	if value != "88" || source != "global" {
		t.Fatalf("unexpected display value: value=%q source=%q", value, source)
	}
}

func TestResolveConfigDisplayValueForChatScopeIgnoresUserOverride(t *testing.T) {
	value, source := resolveConfigDisplayValue(
		"chat",
		KeyReactionDefaultRate,
		"chat-1",
		"user-1",
		func(candidate configLookupCandidate, key ConfigKey) (string, bool) {
			switch {
			case candidate.scope == ScopeUser && candidate.chatID == "chat-1" && candidate.userID == "user-1":
				return "91", true
			case candidate.scope == ScopeChat && candidate.chatID == "chat-1":
				return "42", true
			case candidate.scope == ScopeGlobal:
				return "18", true
			default:
				return "", false
			}
		},
		func(key ConfigKey) string { return "30" },
	)

	if value != "42" || source != "chat" {
		t.Fatalf("unexpected display value: value=%q source=%q", value, source)
	}
}

func TestResolveConfigDisplayValueForUserScopePrefersChatUser(t *testing.T) {
	value, source := resolveConfigDisplayValue(
		"user",
		KeyReactionDefaultRate,
		"chat-1",
		"user-1",
		func(candidate configLookupCandidate, key ConfigKey) (string, bool) {
			switch {
			case candidate.scope == ScopeUser && candidate.chatID == "chat-1" && candidate.userID == "user-1":
				return "73", true
			case candidate.scope == ScopeUser && candidate.userID == "user-1":
				return "61", true
			case candidate.scope == ScopeChat && candidate.chatID == "chat-1":
				return "42", true
			case candidate.scope == ScopeGlobal:
				return "18", true
			default:
				return "", false
			}
		},
		func(key ConfigKey) string { return "30" },
	)

	if value != "73" || source != "user" {
		t.Fatalf("unexpected display value: value=%q source=%q", value, source)
	}
}

func TestResolveConfigDisplayValueFallsBackToDefault(t *testing.T) {
	value, source := resolveConfigDisplayValue(
		"chat",
		KeyReactionDefaultRate,
		"chat-1",
		"user-1",
		func(candidate configLookupCandidate, key ConfigKey) (string, bool) {
			return "", false
		},
		func(key ConfigKey) string { return "30" },
	)

	if value != "30" || source != "default" {
		t.Fatalf("unexpected display value: value=%q source=%q", value, source)
	}
}

func TestBuildConfigActionRowContainsStandardActionPayload(t *testing.T) {
	element := buildConfigActionRow(ConfigItem{
		Key:       "intent_recognition_enabled",
		Value:     "true",
		ValueType: "bool",
		ChatID:    "chat-1",
		UserID:    "user-1",
	}, "chat")
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"action":"config.set"`) {
		t.Fatalf("expected config action in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"config.delete"`) {
		t.Fatalf("expected config delete action in card json: %s", jsonStr)
	}
}

func TestBuildFeatureActionRowContainsStandardActionPayload(t *testing.T) {
	element := buildFeatureActionRow(FeatureItem{
		Name:              "chat",
		BlockedAtChat:     false,
		BlockedAtUser:     true,
		BlockedAtChatUser: false,
	}, "chat-1", "user-1")
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"action":"feature.`) {
		t.Fatalf("expected feature action in card json: %s", jsonStr)
	}
}

func TestBuildConfigScopeRowContainsViewActionPayload(t *testing.T) {
	element := buildConfigScopeRow("chat", "chat-1", "user-1")
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"action":"config.view_scope"`) {
		t.Fatalf("expected config view action in card json: %s", jsonStr)
	}
}

func TestBuildConfigCustomValueFormContainsInputAndSubmit(t *testing.T) {
	element := buildConfigCustomValueForm(ConfigItem{
		Key:       string(KeyReactionDefaultRate),
		Value:     "30",
		ValueType: "int",
		ChatID:    "chat-1",
		UserID:    "user-1",
	}, "chat")
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"form"`) {
		t.Fatalf("expected form element in card json: %s", jsonStr)
	}
	if tag, ok := element["tag"].(string); !ok || tag != "form" {
		t.Fatalf("expected root tag to be form: %#v", element)
	}
	children, ok := element["elements"].([]any)
	if !ok || len(children) == 0 {
		t.Fatalf("expected form children: %#v", element)
	}
	firstChild, ok := children[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first child to be object: %#v", children[0])
	}
	if tag, ok := firstChild["tag"].(string); !ok || tag != "column_set" {
		t.Fatalf("expected form to wrap a column_set: %#v", firstChild)
	}
	if !strings.Contains(jsonStr, `"tag":"input"`) {
		t.Fatalf("expected input element in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"form_action_type":"submit"`) {
		t.Fatalf("expected form submit button in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"提交"`) {
		t.Fatalf("expected concise submit label in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"form_field":"input_reaction_default_rate"`) {
		t.Fatalf("expected form field payload in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"默认"`) {
		t.Fatalf("expected restore default button in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"type":"laser"`) {
		t.Fatalf("expected restore default button to use distinct style: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"size":"small"`) {
		t.Fatalf("did not expect small size on restore default button: %s", jsonStr)
	}
}

func TestBuildConfigItemSectionPlacesControlsOnRight(t *testing.T) {
	element := buildConfigItemSection(ConfigItem{
		Key:         string(KeyReactionDefaultRate),
		Description: "默认回应表情概率 (0-100)",
		Value:       "30",
		ValueType:   "int",
		Scope:       "chat",
		ChatID:      "chat-1",
		UserID:      "user-1",
	}, "chat")
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"column_set"`) {
		t.Fatalf("expected column layout in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"tag":"form"`) {
		t.Fatalf("expected root-level form wrapper for int config item: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"config.delete"`) {
		t.Fatalf("expected restore default action in right column: %s", jsonStr)
	}
}

func TestBuildConfigActionRowForIntOnlyKeepsDeleteButton(t *testing.T) {
	element := buildConfigActionRow(ConfigItem{
		Key:       string(KeyReactionDefaultRate),
		Value:     "30",
		ValueType: "int",
		ChatID:    "chat-1",
		UserID:    "user-1",
	}, "chat")
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if strings.Contains(jsonStr, `"content":"0"`) || strings.Contains(jsonStr, `"content":"50"`) || strings.Contains(jsonStr, `"content":"100"`) {
		t.Fatalf("did not expect preset int buttons in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"config.delete"`) {
		t.Fatalf("expected delete action in int config action row: %s", jsonStr)
	}
}

func TestNewRawCardUsesSchemaV2Structure(t *testing.T) {
	card := newRawCard("配置面板", []any{buildConfigScopeRow("chat", "chat-1", "user-1")})
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"schema":"2.0"`) {
		t.Fatalf("expected schema v2 card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"body":{`) || !strings.Contains(jsonStr, `"elements":[`) {
		t.Fatalf("expected body elements in card json: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"tag":"action"`) {
		t.Fatalf("did not expect deprecated action tag in card json: %s", jsonStr)
	}
}
