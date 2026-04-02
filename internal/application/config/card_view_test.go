package config

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

func useWorkspaceConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

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
			case candidate.scope == ScopeUser && candidate.chatID == "chat-1" && candidate.openID == "user-1":
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
			case candidate.scope == ScopeUser && candidate.chatID == "chat-1" && candidate.openID == "user-1":
				return "73", true
			case candidate.scope == ScopeUser && candidate.openID == "user-1":
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
		OpenID:    "user-1",
	}, ConfigViewState{
		Scope: "chat",
	})
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

func TestBuildConfigCardContainsPickerForm(t *testing.T) {
	useWorkspaceConfigPath(t)
	card, err := BuildConfigCardWithOptions(context.Background(), "chat", "chat-1", "user-1", ConfigCardViewOptions{})
	if err != nil {
		t.Fatalf("BuildConfigCardWithOptions() error = %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `配置筛选`) || !strings.Contains(jsonStr, `"name":"config_selected_key"`) {
		t.Fatalf("expected config picker form in config card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, string(KeyMusicCardInThread)) || !strings.Contains(jsonStr, string(KeyChatReasoningModel)) {
		t.Fatalf("expected accessor-backed config keys in picker: %s", jsonStr)
	}
}

func TestBuildConfigCardOnlyRendersSelectedConfigItem(t *testing.T) {
	useWorkspaceConfigPath(t)
	card, err := BuildConfigCardWithOptions(context.Background(), "chat", "chat-1", "user-1", ConfigCardViewOptions{
		SelectedKey: string(KeyChatReasoningModel),
	})
	if err != nil {
		t.Fatalf("BuildConfigCardWithOptions() error = %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"name":"form_chat_reasoning_model"`) {
		t.Fatalf("expected selected config item in card json: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"name":"form_intent_recognition_enabled"`) {
		t.Fatalf("did not expect non-selected config item in card json: %s", jsonStr)
	}
}

func TestBuildFeatureActionRowContainsStandardActionPayload(t *testing.T) {
	element := buildFeatureActionRow(FeatureItem{
		Name:              "chat",
		BlockedAtChat:     false,
		BlockedAtUser:     true,
		BlockedAtChatUser: false,
	}, FeatureViewState{
		ChatID: "chat-1",
		OpenID: "user-1",
	})
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
	element := buildConfigScopeRow(ConfigViewState{
		Scope:  "chat",
		ChatID: "chat-1",
		OpenID: "user-1",
	})
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"action":"config.view_scope"`) {
		t.Fatalf("expected config view action in card json: %s", jsonStr)
	}
}

func TestBuildConfigCardAddsOperationHistoryPanel(t *testing.T) {
	useWorkspaceConfigPath(t)
	card, err := BuildConfigCardWithOptions(context.Background(), "chat", "chat-1", "user-1", ConfigCardViewOptions{})
	if err != nil {
		t.Fatalf("BuildConfigCardWithOptions() error = %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) || !strings.Contains(jsonStr, `操作记录`) {
		t.Fatalf("expected operation history panel in config card: %s", jsonStr)
	}
}

func TestBuildFeatureCardAddsOperationHistoryPanel(t *testing.T) {
	useWorkspaceConfigPath(t)
	card, err := BuildFeatureCardWithOptions(context.Background(), "chat-1", "user-1", FeatureCardViewOptions{})
	if err != nil {
		t.Fatalf("BuildFeatureCardWithOptions() error = %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) || !strings.Contains(jsonStr, `操作记录`) {
		t.Fatalf("expected operation history panel in feature card: %s", jsonStr)
	}
}

func TestBuildConfigCustomValueFormContainsInputAndSubmit(t *testing.T) {
	element := buildConfigValueForm(ConfigItem{
		Key:       string(KeyReactionDefaultRate),
		Value:     "30",
		ValueType: "int",
		ChatID:    "chat-1",
		OpenID:    "user-1",
	}, ConfigViewState{
		Scope: "chat",
	})
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

func TestBuildConfigValueFormSupportsStringConfig(t *testing.T) {
	element := buildConfigValueForm(ConfigItem{
		Key:       string(KeyChatReasoningModel),
		Value:     "deep-reasoner",
		ValueType: "string",
		ChatID:    "chat-1",
		OpenID:    "user-1",
	}, ConfigViewState{
		Scope: "chat",
	})
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"select_static"`) || !strings.Contains(jsonStr, `"initial_option":"deep-reasoner"`) {
		t.Fatalf("expected enum string config select in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `deep-reasoner | 当前值`) && !strings.Contains(jsonStr, `deep-reasoner | reasoning_model`) {
		t.Fatalf("expected string config options in card json: %s", jsonStr)
	}
}

func TestBuildConfigItemSectionPlacesControlsOnRight(t *testing.T) {
	element := buildConfigItemSection(ConfigItem{
		Key:         string(KeyReactionDefaultRate),
		Description: "默认回应表情概率 (0-100)",
		Value:       "30",
		ValueType:   "int",
		Scope:       "chat",
		IsEditable:  true,
		ChatID:      "chat-1",
		OpenID:      "user-1",
	}, ConfigViewState{
		Scope: "chat",
	})
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

func TestBuildConfigItemSectionOmitsControlsForReadOnlyConfig(t *testing.T) {
	element := buildConfigItemSection(ConfigItem{
		Key:         "lark_card_action_index",
		Description: "Lark 卡片操作记录索引名",
		Value:       "card-action-index",
		ValueType:   "string",
		Scope:       "toml",
		IsEditable:  false,
		ChatID:      "chat-1",
		OpenID:      "user-1",
	}, ConfigViewState{
		Scope: "chat",
	})
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if strings.Contains(jsonStr, `"tag":"form"`) {
		t.Fatalf("did not expect form wrapper for read-only config: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"tag":"button"`) {
		t.Fatalf("did not expect action buttons for read-only config: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `只读`) {
		t.Fatalf("expected read-only hint in card json: %s", jsonStr)
	}
}

func TestBuildConfigActionRowForIntOnlyKeepsDeleteButton(t *testing.T) {
	element := buildConfigActionRow(ConfigItem{
		Key:       string(KeyReactionDefaultRate),
		Value:     "30",
		ValueType: "int",
		ChatID:    "chat-1",
		OpenID:    "user-1",
	}, ConfigViewState{
		Scope: "chat",
	})
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

func TestHandleConfigActionRejectsReadOnlyStartupConfig(t *testing.T) {
	resp, err := HandleConfigAction(context.Background(), &ConfigActionRequest{
		Action: ConfigActionSet,
		Key:    "lark_card_action_index",
		Value:  "another-index",
		Scope:  "global",
	})
	if err == nil {
		t.Fatal("expected error for read-only startup config")
	}
	if resp == nil || resp.Success {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !strings.Contains(resp.Message, "只读") {
		t.Fatalf("expected read-only message, got %+v", resp)
	}
}

func TestNewRawCardUsesSchemaV2Structure(t *testing.T) {
	useWorkspaceConfigPath(t)
	card := newRawCard(context.Background(), "配置面板", []any{buildConfigScopeRow(ConfigViewState{
		Scope:  "chat",
		ChatID: "chat-1",
		OpenID: "user-1",
	})}, larkmsg.StandardCardFooterOptions{
		RefreshPayload: larkmsg.StringMapToAnyMap(BuildConfigViewValue("chat", "chat-1", "user-1")),
	})
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
	if !strings.Contains(jsonStr, `"template":"wathet"`) || !strings.Contains(jsonStr, `"padding":"12px"`) {
		t.Fatalf("expected unified panel style in card json: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"tag":"action"`) {
		t.Fatalf("did not expect deprecated action tag in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"撤回"`) || !strings.Contains(jsonStr, `更新于 `) {
		t.Fatalf("expected default footer actions in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"刷新"`) || !strings.Contains(jsonStr, `"action":"config.view_scope"`) {
		t.Fatalf("expected refresh footer action in card json: %s", jsonStr)
	}
}

func TestBuildFeatureActionRowUsesFilledPrimaryForUnblock(t *testing.T) {
	element := buildFeatureActionRow(FeatureItem{
		Name:              "chat",
		BlockedAtChat:     false,
		BlockedAtUser:     true,
		BlockedAtChatUser: false,
	}, FeatureViewState{
		ChatID: "chat-1",
		OpenID: "user-1",
	})
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"content":"取消用户"`) || !strings.Contains(jsonStr, `"type":"primary_filled"`) {
		t.Fatalf("expected filled primary style for unblock action: %s", jsonStr)
	}
}
