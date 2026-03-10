package larkmsg

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewCardV2UsesSchemaV2Defaults(t *testing.T) {
	card := NewCardV2("测试卡片", []any{Markdown("hello")}, CardV2Options{})
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"schema":"2.0"`) {
		t.Fatalf("expected schema v2 json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"update_multi":true`) {
		t.Fatalf("expected update_multi default to true: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"template":"blue"`) {
		t.Fatalf("expected blue header template by default: %s", jsonStr)
	}
}

func TestButtonCarriesCallbackPayload(t *testing.T) {
	button := Button("提交", ButtonOptions{
		Type:    "primary",
		Payload: map[string]any{"action": "config.submit"},
	})
	raw, err := json.Marshal(button)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"type":"primary"`) {
		t.Fatalf("expected button type in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"type":"callback"`) {
		t.Fatalf("expected callback behavior in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"config.submit"`) {
		t.Fatalf("expected callback payload in json: %s", jsonStr)
	}
}
