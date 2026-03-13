package command

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
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
	if !strings.Contains(jsonStr, `"name":"key"`) || !strings.Contains(jsonStr, `"initial_option":"intent_recognition_enabled"`) {
		t.Fatalf("expected key enum field to rehydrate current value: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"value":"intent_recognition_enabled"`) || !strings.Contains(jsonStr, `intent_recognition_enabled | 是否启用意图识别`) {
		t.Fatalf("expected config key options in form card: %s", jsonStr)
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

func TestBuildCommandFormCardJSONResolvesFeatureEnumsLazily(t *testing.T) {
	appconfig.SetGetFeaturesFunc(func() []appconfig.Feature {
		return []appconfig.Feature{
			{Name: "chat", Description: "聊天"},
			{Name: "music", Description: "音乐"},
		}
	})
	defer appconfig.SetGetFeaturesFunc(nil)

	root := NewLarkRootCommand()
	cardData, err := BuildCommandFormCardJSON(root, "/feature block --feature=music")
	if err != nil {
		t.Fatalf("BuildCommandFormCardJSON() error = %v", err)
	}

	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"initial_option":"music"`) {
		t.Fatalf("expected selected feature option to be rehydrated: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"value":"music"`) || !strings.Contains(jsonStr, `music | 音乐`) {
		t.Fatalf("expected runtime feature options in form card: %s", jsonStr)
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

func TestBuildCommandFormCardJSONUsesEnumFiltersForWordChunks(t *testing.T) {
	root := NewLarkRootCommand()

	cardData, err := BuildCommandFormCardJSON(root, "/wordcount chunks --question_mode=question")
	if err != nil {
		t.Fatalf("BuildCommandFormCardJSON() error = %v", err)
	}

	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"name":"sort"`) ||
		!strings.Contains(jsonStr, `"name":"intent"`) ||
		!strings.Contains(jsonStr, `"name":"sentiment"`) ||
		!strings.Contains(jsonStr, `"name":"question_mode"`) {
		t.Fatalf("expected wordcount chunk filters in form card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"initial_option":"question"`) {
		t.Fatalf("expected explicit question_mode to be rehydrated: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"value":"CASUAL_CHITCHAT"`) ||
		!strings.Contains(jsonStr, `"value":"NEGATIVE"`) ||
		!strings.Contains(jsonStr, `"value":"no_question"`) {
		t.Fatalf("expected enum options for chunk filters: %s", jsonStr)
	}
}

func TestBuildCommandFormCardJSONSupportsWordCountChunkDetailCommand(t *testing.T) {
	root := NewLarkRootCommand()

	cardData, err := BuildCommandFormCardJSON(root, "/wordcount chunk")
	if err != nil {
		t.Fatalf("BuildCommandFormCardJSON() error = %v", err)
	}

	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"name":"id"`) {
		t.Fatalf("expected chunk detail form to expose id field: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"name":"chat_id"`) {
		t.Fatalf("expected chunk detail form to expose chat_id override: %s", jsonStr)
	}
}

func TestBuildCommandFormCardJSONSupportsWcAliasSubcommands(t *testing.T) {
	root := NewLarkRootCommand()

	cardData, err := BuildCommandFormCardJSON(root, "/wc chunks --question_mode=question")
	if err != nil {
		t.Fatalf("BuildCommandFormCardJSON() error = %v", err)
	}

	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"name":"question_mode"`) {
		t.Fatalf("expected wc chunks form to resolve to chunk filters: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"**命令**: `+"`wc chunks`"+`"`) {
		t.Fatalf("expected wc alias path to be preserved in form title block: %s", jsonStr)
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

	cardData := buildHelpCard(context.TODO(), root, "config set")
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
