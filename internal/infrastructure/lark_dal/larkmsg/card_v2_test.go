package larkmsg

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func useWorkspaceConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

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

func TestNewStandardPanelCardUsesSharedPanelOptions(t *testing.T) {
	card := NewStandardPanelCard(context.Background(), "测试卡片", []any{Markdown("hello")})
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"template":"wathet"`) {
		t.Fatalf("expected wathet header template: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"padding":"12px"`) {
		t.Fatalf("expected standard panel padding: %s", jsonStr)
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

func TestButtonCarriesOpenURLBehavior(t *testing.T) {
	button := Button("Trace", ButtonOptions{
		URL: "https://example.com/trace",
	})
	raw, err := json.Marshal(button)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"type":"open_url"`) {
		t.Fatalf("expected open_url behavior in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"default_url":"https://example.com/trace"`) {
		t.Fatalf("expected default_url in json: %s", jsonStr)
	}
}

func TestAppendStandardCardFooterAddsWithdrawAndTrace(t *testing.T) {
	useWorkspaceConfigPath(t)
	elements := AppendStandardCardFooter(context.Background(), []any{Markdown("hello")})
	raw, err := json.Marshal(elements)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"content":"撤回"`) {
		t.Fatalf("expected withdraw action in footer: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `更新于 `) {
		t.Fatalf("expected updated timestamp in footer: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"card.withdraw"`) {
		t.Fatalf("expected withdraw callback payload in footer: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"type":"danger_filled"`) {
		t.Fatalf("expected withdraw button to use danger_filled: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"horizontal_align"`) {
		t.Fatalf("did not expect footer to emit horizontal_align: %s", jsonStr)
	}
}

func TestAppendStandardCardFooterAddsRefreshWhenPayloadProvided(t *testing.T) {
	elements := AppendStandardCardFooter(context.Background(), []any{Markdown("hello")}, StandardCardFooterOptions{
		RefreshPayload: map[string]any{"action": "schedule.view"},
	})
	raw, err := json.Marshal(elements)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"content":"刷新"`) {
		t.Fatalf("expected refresh action in footer: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"schedule.view"`) {
		t.Fatalf("expected refresh payload in footer: %s", jsonStr)
	}
}

func TestAppendStandardCardFooterAddsLastModifierPerson(t *testing.T) {
	elements := AppendStandardCardFooter(context.Background(), []any{Markdown("hello")}, StandardCardFooterOptions{
		LastModifierOpenID: "ou_modifier",
	})
	raw, err := json.Marshal(elements)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"content":"最后修改"`) {
		t.Fatalf("expected last modifier label in footer: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"tag":"person"`) || !strings.Contains(jsonStr, `"user_id":"ou_modifier"`) {
		t.Fatalf("expected person component in footer: %s", jsonStr)
	}
}

func TestAppendStandardCardFooterAddsActionHistoryPanel(t *testing.T) {
	elements := AppendStandardCardFooter(context.Background(), []any{Markdown("hello")}, StandardCardFooterOptions{
		ActionHistory: CardActionHistoryOptions{
			Enabled: true,
			PendingRecords: []CardActionHistoryRecord{{
				ActionName:     "schedule.pause",
				ActionValue:    map[string]any{"id": "task-1"},
				OpenID:         "ou_actor",
				CreateTimeUnix: 1741680000000000,
			}},
		},
	})
	raw, err := json.Marshal(elements)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) {
		t.Fatalf("expected collapsible action history panel: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `暂停 Schedule`) || !strings.Contains(jsonStr, `"user_id":"ou_actor"`) {
		t.Fatalf("expected action history content in panel: %s", jsonStr)
	}
}

func TestSelectPersonBuildsCallbackPicker(t *testing.T) {
	element := SelectPerson(SelectPersonOptions{
		Placeholder:   "选择用户",
		Width:         "fill",
		Type:          "default",
		InitialOption: "ou_selected",
		Payload:       map[string]any{"action": "schedule.view"},
		Options:       []string{"ou_selected", "ou_other"},
		ElementID:     "creator_picker",
	})
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"select_person"`) {
		t.Fatalf("expected select_person tag in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"initial_option":"ou_selected"`) {
		t.Fatalf("expected initial option in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"schedule.view"`) {
		t.Fatalf("expected callback payload in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"value":"ou_other"`) {
		t.Fatalf("expected options in json: %s", jsonStr)
	}
}

func TestSelectPersonNormalizesElementID(t *testing.T) {
	element := SelectPerson(SelectPersonOptions{
		ElementID: "permission_target_picker",
	})
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"element_id":"permission_target_pi"`) {
		t.Fatalf("expected normalized element id in json: %s", jsonStr)
	}
}

func TestCollapsiblePanelBuildsSchemaV2Container(t *testing.T) {
	element := CollapsiblePanel(
		"操作记录",
		[]any{HintMarkdown("暂无操作记录。")},
		CollapsiblePanelOptions{
			ElementID:       "card_action_log",
			Expanded:        false,
			Padding:         "8px",
			VerticalSpacing: "6px",
		},
	)
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) {
		t.Fatalf("expected collapsible_panel tag in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"expanded":false`) || !strings.Contains(jsonStr, `"element_id":"card_action_log"`) {
		t.Fatalf("expected collapsible options in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"padding":"8px"`) || !strings.Contains(jsonStr, `"vertical_spacing":"6px"`) {
		t.Fatalf("expected layout options in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"icon_position":"follow_text"`) {
		t.Fatalf("expected standard collapsible header config in json: %s", jsonStr)
	}
}

func TestBuildCardActionHistoryBlockUsesPlaceholderWithoutMessageID(t *testing.T) {
	block := CardActionHistoryPanel(context.Background(), CardActionHistoryOptions{})
	raw, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) {
		t.Fatalf("expected collapsible_panel in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `首次发送后可在此查看操作记录`) {
		t.Fatalf("expected placeholder hint in json: %s", jsonStr)
	}
}

func TestFormatCardActionLabelUsesReadableChineseText(t *testing.T) {
	label, detail := describeCardAction(cardaction.ActionPermissionGrant, map[string]any{
		cardaction.PermissionPointField: "permission.manage",
		cardaction.ScopeField:           "global",
		cardaction.TargetUserIDField:    "ou_target",
	})
	if label != "授予权限" || !strings.Contains(detail, "permission.manage@global") {
		t.Fatalf("expected readable permission label, got %q / %q", label, detail)
	}
}

func TestStringMapToAnyMapCopiesStringPairs(t *testing.T) {
	got := StringMapToAnyMap(map[string]string{
		"action": "config.view_scope",
		"scope":  "chat",
	})
	if got["action"] != "config.view_scope" || got["scope"] != "chat" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestSplitColumnsBuildsTwoColumnLayout(t *testing.T) {
	row := SplitColumns(
		[]any{Markdown("left")},
		[]any{Markdown("right")},
		SplitColumnsOptions{
			Left:  ColumnOptions{Weight: 3, VerticalAlign: "top"},
			Right: ColumnOptions{Weight: 2, VerticalAlign: "top"},
			Row:   ColumnSetOptions{HorizontalSpacing: "16px", FlexMode: "stretch"},
		},
	)
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"weight":3`) || !strings.Contains(jsonStr, `"weight":2`) {
		t.Fatalf("expected column weights in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"width":"weighted"`) {
		t.Fatalf("expected weighted widths in json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"horizontal_spacing":"16px"`) || !strings.Contains(jsonStr, `"flex_mode":"stretch"`) {
		t.Fatalf("expected row options in json: %s", jsonStr)
	}
}

func TestAppendSectionsWithDividersSkipsEmptySections(t *testing.T) {
	got := AppendSectionsWithDividers([]any{Markdown("head")},
		[]any{Markdown("first")},
		nil,
		[]any{Markdown("second")},
	)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if strings.Count(jsonStr, `"tag":"hr"`) != 1 {
		t.Fatalf("expected one divider between non-empty sections: %s", jsonStr)
	}
}

func TestAppendCardActionHistoryAddsPlaceholderWhenMessageUnknown(t *testing.T) {
	elements := AppendCardActionHistory(context.Background(), []any{Markdown("hello")}, CardActionHistoryOptions{})
	raw, err := json.Marshal(elements)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) {
		t.Fatalf("expected collapsible action history panel: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `首次发送后可在此查看操作记录`) {
		t.Fatalf("expected placeholder history note: %s", jsonStr)
	}
}
