package agentcard

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCardSpecV1RoundTripPreservesTypedVariantsAndOrder(t *testing.T) {
	spec := CardSpec{
		Version: VersionV1,
		Title:   "确认修改日程",
		Theme:   ThemeBlue,
		Blocks: []Block{
			Markdown("summary", "**时间**：明天 10:00"),
			PlainText("plain", "请确认以下信息"),
			Facts("facts", []Fact{{Label: "日程", Value: "周会"}}),
			Note("note", "提交后将修改日程"),
			Divider("divider"),
			Section("section", "详情", []Block{
				Markdown("section_body", "参会人：研发组"),
			}),
			Columns("columns", []Column{
				{ID: "left", Blocks: []Block{PlainText("left_text", "左栏")}},
				{ID: "right", Blocks: []Block{PlainText("right_text", "右栏")}},
			}),
			TextInput("reason", InputField{
				FieldID: "reason", Label: "修改原因", Required: true,
			}, TextInputConfig{Placeholder: "简要说明", MaxLength: 200}),
			SingleSelect("time", InputField{
				FieldID: "time", Label: "时间", Required: true,
			}, []SelectOption{{Label: "10:00", Value: "10:00"}}),
			MultiSelect("members", InputField{
				FieldID: "members", Label: "参会人",
			}, []SelectOption{
				{Label: "Alice", Value: "alice"},
				{Label: "Bob", Value: "bob"},
			}),
		},
		Actions: []Action{
			{
				Kind: ActionButton, ID: "preview", Label: "预览",
				Style: ActionStyleDefault, Mode: ActionModeUI,
				Intent: "preview_change",
			},
			{
				Kind: ActionSubmit, ID: "confirm", Label: "确认修改",
				Style: ActionStylePrimary, Mode: ActionModeCapabilityConfirm,
				Intent: "confirm_schedule_change", FormRef: "schedule_form",
			},
			{
				Kind: ActionReset, ID: "reset", Label: "重置",
				Mode: ActionModeServer, Intent: "reset_form",
			},
			{
				Kind: ActionCancel, ID: "cancel", Label: "取消",
				Mode: ActionModeUI, Intent: "cancel_change",
			},
		},
		Meta: PublicCardMeta{
			Purpose: "confirmation", Summary: "确认日程修改",
			Labels: []string{"schedule", "confirmation"},
		},
	}

	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(encoded)
	for _, fragment := range []string{
		`"version":"agent-card/v1"`,
		`"kind":"markdown"`,
		`"field_id":"reason"`,
		`"form_ref":"schedule_form"`,
		`"interaction_kind"`,
	} {
		if fragment == `"interaction_kind"` {
			if strings.Contains(text, fragment) {
				t.Fatalf("public CardSpec leaked runtime field: %s", text)
			}
			continue
		}
		if !strings.Contains(text, fragment) {
			t.Fatalf("stable snake_case fragment %s missing from %s", fragment, text)
		}
	}

	var decoded CardSpec
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, spec) {
		t.Fatalf("roundtrip mismatch:\n got: %#v\nwant: %#v", decoded, spec)
	}
	for index, want := range spec.Blocks {
		if decoded.Blocks[index].Kind != want.Kind ||
			decoded.Blocks[index].ID != want.ID {
			t.Fatalf("block order changed at %d: %#v", index, decoded.Blocks)
		}
	}
}

func TestCardSpecRejectsUnknownVariantsFieldsAndRuntimeEnvelope(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "unknown block",
			raw:  `{"version":"agent-card/v1","title":"x","blocks":[{"kind":"raw_lark","id":"x"}]}`,
		},
		{
			name: "unknown action",
			raw:  `{"version":"agent-card/v1","title":"x","blocks":[],"actions":[{"kind":"execute_anything","id":"x","label":"x","mode":"ui_action"}]}`,
		},
		{
			name: "raw callback map",
			raw:  `{"version":"agent-card/v1","title":"x","blocks":[],"actions":[{"kind":"button","id":"x","label":"x","mode":"ui_action","value":{"token":"secret"}}]}`,
		},
		{
			name: "runtime envelope in metadata",
			raw:  `{"version":"agent-card/v1","title":"x","blocks":[],"meta":{"purpose":"x","run_id":"run-1","token":"secret"}}`,
		},
		{
			name: "raw lark tag",
			raw:  `{"version":"agent-card/v1","title":"x","blocks":[{"kind":"markdown","id":"x","text":"hello","tag":"div"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var spec CardSpec
			if err := json.Unmarshal([]byte(test.raw), &spec); err == nil {
				t.Fatalf("json.Unmarshal(%s) error = nil", test.raw)
			}
		})
	}
}
