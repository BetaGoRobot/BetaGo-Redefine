package handlers

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	toolkit "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func useWorkspaceConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

func TestBuildSchedulableToolsContainsStandardToolset(t *testing.T) {
	useWorkspaceConfigPath(t)
	schedulable := BuildSchedulableTools()
	allTools := larktools()

	excluded := map[string]struct{}{
		"create_schedule":   {},
		"list_schedules":    {},
		"query_schedule":    {},
		"delete_schedule":   {},
		"pause_schedule":    {},
		"resume_schedule":   {},
		"revert_message":    {},
		"permission_manage": {},
	}

	for name := range allTools.FunctionCallMap {
		if _, skip := excluded[name]; skip {
			continue
		}
		if _, ok := schedulable.FunctionCallMap[name]; !ok {
			t.Fatalf("schedulable tools missing %q", name)
		}
	}

	if _, ok := schedulable.FunctionCallMap["gold_price_get"]; !ok {
		t.Fatal("schedulable tools missing gold_price_get")
	}
	if _, ok := allTools.FunctionCallMap["query_schedule"]; !ok {
		t.Fatal("lark tools missing query_schedule")
	}
}

func TestBuildSchedulableToolsRestrictsSendMessageChatOverride(t *testing.T) {
	useWorkspaceConfigPath(t)
	schedulable := BuildSchedulableTools()
	unit, ok := schedulable.Get("send_message")
	if !ok {
		t.Fatal("schedulable tools missing send_message")
	}

	result := unit.Function(context.Background(), `{"content":"hi","chat_id":"oc_other"}`, toolkit.FCMeta[larkim.P2MessageReceiveV1]{
		ChatID: "oc_self",
		OpenID: "ou_user",
	})
	if !result.IsErr() {
		t.Fatal("expected send_message to reject chat_id override in schedule context")
	}
	if !strings.Contains(result.Err().Error(), "cannot override chat_id") {
		t.Fatalf("unexpected error: %v", result.Err())
	}
}

func TestLarkToolsIncludeResearchHelpers(t *testing.T) {
	useWorkspaceConfigPath(t)
	allTools := larktools()
	schedulable := BuildSchedulableTools()

	for _, name := range []string{
		"research_read_url",
		"research_extract_evidence",
		"research_source_ledger",
	} {
		if _, ok := allTools.FunctionCallMap[name]; !ok {
			t.Fatalf("lark tools missing %q", name)
		}
		if _, ok := schedulable.FunctionCallMap[name]; !ok {
			t.Fatalf("schedulable tools missing %q", name)
		}
	}
	if allTools.WebsearchTool == nil {
		t.Fatal("expected lark tools to keep builtin web_search enabled")
	}
}

func TestLarkToolsExposeSearchHistoryMetadataFilters(t *testing.T) {
	useWorkspaceConfigPath(t)
	allTools := larktools()

	searchHistory, ok := allTools.Get("search_history")
	if !ok {
		t.Fatal("expected search_history tool")
	}
	for _, name := range []string{"keywords", "user_id", "user_name", "message_type", "start_time", "end_time", "top_k"} {
		if _, exists := searchHistory.Parameters.Props[name]; !exists {
			t.Fatalf("search_history missing %q parameter", name)
		}
	}
}

func TestLarkToolsExposeMemberLookupTools(t *testing.T) {
	useWorkspaceConfigPath(t)
	allTools := larktools()
	schedulable := BuildSchedulableTools()

	for _, name := range []string{"get_chat_members", "get_recent_active_members"} {
		if _, ok := allTools.Get(name); !ok {
			t.Fatalf("expected lark tools to expose %q", name)
		}
		if _, ok := schedulable.Get(name); !ok {
			t.Fatalf("expected schedulable tools to expose %q", name)
		}
	}

	members, _ := allTools.Get("get_chat_members")
	if _, exists := members.Parameters.Props["limit"]; !exists {
		t.Fatalf("get_chat_members missing %q parameter", "limit")
	}

	active, _ := allTools.Get("get_recent_active_members")
	for _, name := range []string{"top_k", "lookback_messages"} {
		if _, exists := active.Parameters.Props[name]; !exists {
			t.Fatalf("get_recent_active_members missing %q parameter", name)
		}
	}
}

func TestLarkToolsExposeTypedConfigAndFeatureEnums(t *testing.T) {
	useWorkspaceConfigPath(t)
	appconfig.SetGetFeaturesFunc(func() []appconfig.Feature {
		return []appconfig.Feature{
			{Name: "chat", Description: "聊天"},
			{Name: "music", Description: "音乐"},
		}
	})
	defer appconfig.SetGetFeaturesFunc(nil)

	allTools := larktools()

	configSetUnit, ok := allTools.Get("config_set")
	if !ok {
		t.Fatal("expected config_set tool")
	}
	keyProp := configSetUnit.Parameters.Props["key"]
	if keyProp == nil || len(keyProp.Enum) == 0 {
		t.Fatalf("expected config_set key enum, got: %+v", keyProp)
	}
	if keyProp.Enum[0] != "reaction_default_rate" {
		t.Fatalf("unexpected first config key enum: %+v", keyProp.Enum)
	}

	featureBlockUnit, ok := allTools.Get("feature_block")
	if !ok {
		t.Fatal("expected feature_block tool")
	}
	featureProp := featureBlockUnit.Parameters.Props["feature"]
	if featureProp == nil || len(featureProp.Enum) != 2 {
		t.Fatalf("expected feature_block feature enum, got: %+v", featureProp)
	}
	if featureProp.Enum[0] != "chat" || featureProp.Enum[1] != "music" {
		t.Fatalf("unexpected feature enum values: %+v", featureProp.Enum)
	}
}
