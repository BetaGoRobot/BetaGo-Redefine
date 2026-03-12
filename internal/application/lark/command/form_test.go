package command

import (
	"encoding/json"
	"strings"
	"testing"

	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestBuildCommandFormCardJSON(t *testing.T) {
	root := NewLarkRootCommand()

	cardData, err := BuildCommandFormCardJSON(root, "/config set --key=intent_recognition_enabled")
	if err != nil {
		t.Fatalf("BuildCommandFormCardJSON() error = %v", err)
	}

	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"action":"command.submit_form"`) {
		t.Fatalf("expected submit action in form card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"tag":"form"`) {
		t.Fatalf("expected form wrapper in command form card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"form_action_type":"submit"`) {
		t.Fatalf("expected submit button to use form_action_type: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"name":"key"`) || !strings.Contains(jsonStr, `"name":"value"`) {
		t.Fatalf("expected config fields in form card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"tag":"select_static"`) || !strings.Contains(jsonStr, `"value":"chat"`) || !strings.Contains(jsonStr, `"value":"global"`) {
		t.Fatalf("expected enum args to render as select_static options: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"initial_option":"chat"`) {
		t.Fatalf("expected enum field to rehydrate default option: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"name":"key"`) || !strings.Contains(jsonStr, `"default_value":"intent_recognition_enabled"`) {
		t.Fatalf("expected input field to rehydrate current value: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"command":"/config set --key=intent_recognition_enabled"`) {
		t.Fatalf("expected original command payload in form card: %s", jsonStr)
	}
}

func TestBuildCommandFormCardJSONRehydratesExplicitEnumSelection(t *testing.T) {
	root := NewLarkRootCommand()

	cardData, err := BuildCommandFormCardJSON(root, "/config list --scope=user")
	if err != nil {
		t.Fatalf("BuildCommandFormCardJSON() error = %v", err)
	}

	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"initial_option":"user"`) {
		t.Fatalf("expected explicit enum selection to be rehydrated: %s", jsonStr)
	}
}

func TestBuildCommandFormCardJSONUsesTypedEnumDescriptorWithoutLegacyTag(t *testing.T) {
	root := NewLarkRootCommand()

	cardData, err := BuildCommandFormCardJSON(root, "/feature block --feature=chat")
	if err != nil {
		t.Fatalf("BuildCommandFormCardJSON() error = %v", err)
	}

	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"select_static"`) ||
		!strings.Contains(jsonStr, `"value":"chat_user"`) ||
		!strings.Contains(jsonStr, `"value":"user"`) {
		t.Fatalf("expected typed enum scope options in feature form: %s", jsonStr)
	}
}

func TestBuildCommandFormCardJSONUsesDynamicSpecOptionsForDebugCard(t *testing.T) {
	root := NewLarkRootCommand()

	cardData, err := BuildCommandFormCardJSON(root, "/debug card")
	if err != nil {
		t.Fatalf("BuildCommandFormCardJSON() error = %v", err)
	}

	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"name":"spec"`) ||
		!strings.Contains(jsonStr, `"value":"config"`) ||
		!strings.Contains(jsonStr, `"value":"schedule.task"`) ||
		!strings.Contains(jsonStr, `"value":"ratelimit.sample"`) {
		t.Fatalf("expected debug spec field to render finite options: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"name":"template","tag":"select_static"`) {
		t.Fatalf("did not expect template field to be forced into enum options: %s", jsonStr)
	}
}

func TestBuildCommandFormRawCommandMergesFormValues(t *testing.T) {
	root := NewLarkRootCommand()

	rawCommand, err := BuildCommandFormRawCommand(root, "/config set --key=intent_recognition_enabled", map[string]any{
		"value": "true",
		"scope": "user",
	})
	if err != nil {
		t.Fatalf("BuildCommandFormRawCommand() error = %v", err)
	}
	if !strings.Contains(rawCommand, "/config set") {
		t.Fatalf("expected command path, got: %s", rawCommand)
	}
	if !strings.Contains(rawCommand, "--key=intent_recognition_enabled") {
		t.Fatalf("expected preserved key arg, got: %s", rawCommand)
	}
	if !strings.Contains(rawCommand, "--value=true") {
		t.Fatalf("expected merged value arg, got: %s", rawCommand)
	}
	if !strings.Contains(rawCommand, "--scope=user") {
		t.Fatalf("expected merged scope arg, got: %s", rawCommand)
	}
}

func TestBuildCommandFormRawCommandPreservesAndUpdatesFlagArgs(t *testing.T) {
	root := NewLarkRootCommand()

	rawCommand, err := BuildCommandFormRawCommand(root, "/mute --cancel", map[string]any{
		"cancel": "false",
	})
	if err != nil {
		t.Fatalf("BuildCommandFormRawCommand() error = %v", err)
	}
	if strings.Contains(rawCommand, "--cancel") {
		t.Fatalf("expected false selection to remove flag, got: %s", rawCommand)
	}

	rawCommand, err = BuildCommandFormRawCommand(root, "/mute", map[string]any{
		"cancel": "true",
	})
	if err != nil {
		t.Fatalf("BuildCommandFormRawCommand() error = %v", err)
	}
	if !strings.Contains(rawCommand, "--cancel") {
		t.Fatalf("expected true selection to add flag, got: %s", rawCommand)
	}
}

func TestBuildHelpCardIncludesCommandFormButton(t *testing.T) {
	root := NewLarkRootCommand()

	cardData := buildHelpCard(nil, root, "config set")
	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"action":"`+cardaction.ActionCommandOpenForm+`"`) {
		t.Fatalf("expected open form action button in help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"command":"/config set"`) {
		t.Fatalf("expected target command payload in help card: %s", jsonStr)
	}
}
