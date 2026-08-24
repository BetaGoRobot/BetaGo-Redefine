package handlers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/toolmeta"
	toolkit "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type agentCardToolServiceFake struct {
	discover agentcardtool.DiscoverResponse
	compose  agentcardtool.ComposeResponse
}

func (f *agentCardToolServiceFake) DiscoverComponents(
	context.Context,
	agentcardtool.DiscoverRequest,
) (agentcardtool.DiscoverResponse, error) {
	return f.discover, nil
}

func (f *agentCardToolServiceFake) ComposeCard(
	context.Context,
	agentcardtool.ComposeContext,
	agentcardtool.ComposeRequest,
) (agentcardtool.ComposeResponse, error) {
	return f.compose, nil
}

func useWorkspaceConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

func TestNewCandidateRunnerFactoryRequiresArkModelID(t *testing.T) {
	if _, err := NewCandidateRunnerFactory("  "); err == nil {
		t.Fatal("NewCandidateRunnerFactory() accepted an empty Ark model ID")
	}
	factory, err := NewCandidateRunnerFactory(" endpoint-candidate ")
	if err != nil {
		t.Fatalf("NewCandidateRunnerFactory() error = %v", err)
	}
	if factory == nil {
		t.Fatal("NewCandidateRunnerFactory() returned a nil factory")
	}
}

func TestBuildSchedulableToolsContainsStandardToolset(t *testing.T) {
	useWorkspaceConfigPath(t)
	schedulable := BuildSchedulableTools()
	allTools := larktools(context.Background())

	excluded := map[string]struct{}{
		"create_schedule":   {},
		"list_schedules":    {},
		"query_schedule":    {},
		"delete_schedule":   {},
		"pause_schedule":    {},
		"resume_schedule":   {},
		"edit_schedule":     {},
		"revert_message":    {},
		"permission_manage": {},
	}

	for name := range allTools.FunctionCallMap {
		if strings.HasPrefix(name, "luckin_") {
			continue
		}
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

func TestBuildCandidateShadowToolsUsesExplicitRegisteredReadOnlyAllowlist(t *testing.T) {
	useWorkspaceConfigPath(t)
	shadow, err := BuildCandidateShadowTools()
	if err != nil {
		t.Fatalf("BuildCandidateShadowTools() error = %v", err)
	}
	want := []string{
		"search_history",
		"finance_tool_discover",
		"finance_market_data_get",
		"finance_news_get",
		"economy_indicator_get",
		"get_chat_members",
		"get_recent_active_members",
	}
	if len(shadow.FunctionCallMap) != len(want) {
		t.Fatalf("shadow tool count = %d, want %d: %#v", len(shadow.FunctionCallMap), len(want), shadow.FunctionCallMap)
	}
	production := BuildRuntimeCapabilityTools()
	for _, name := range want {
		if _, ok := production.Get(name); !ok {
			t.Fatalf("production registry missing allowlisted tool %q", name)
		}
		if _, ok := shadow.Get(name); !ok {
			t.Fatalf("shadow registry missing allowlisted tool %q", name)
		}
		behavior, ok := toolmeta.LookupRuntimeBehavior(name)
		if !ok {
			t.Fatalf("allowlisted tool %q has no explicit runtime behavior", name)
		}
		if behavior.SideEffectLevel != toolmeta.SideEffectLevelNone {
			t.Fatalf("shadow tool %q side effect = %q, want none", name, behavior.SideEffectLevel)
		}
	}
	for _, blocked := range []string{"send_message", "config_set", "unknown_tool"} {
		if _, ok := shadow.Get(blocked); ok {
			t.Fatalf("shadow registry unexpectedly contains %q", blocked)
		}
	}
}

func TestBuildCandidateShadowRegistryAdaptsOnlySafeLarkTools(t *testing.T) {
	useWorkspaceConfigPath(t)
	registry, err := BuildCandidateShadowRegistry(
		conversationeval.NewObservationCache(),
		nil,
		"oc_chat",
		"ou_actor",
		time.Date(2026, 7, 29, 15, 0, 0, 123, time.UTC),
	)
	if err != nil {
		t.Fatalf("BuildCandidateShadowRegistry() error = %v", err)
	}
	got := registry.Names()
	want := conversationeval.CandidateShadowToolNames()
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shadow registry names = %#v, want %#v", got, want)
	}
}

func TestBuildCandidateShadowRegistryRejectsMissingAnchor(t *testing.T) {
	if _, err := BuildCandidateShadowRegistry(
		conversationeval.NewObservationCache(),
		nil,
		"oc_chat",
		"ou_actor",
		time.Time{},
	); err == nil {
		t.Fatal("BuildCandidateShadowRegistry() accepted missing anchor")
	}
}

func TestClampCandidateSearchHistoryArgumentsToAnchor(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 123, time.UTC)
	tests := []struct {
		name      string
		arguments json.RawMessage
		wantEnd   string
	}{
		{
			name:      "missing end time",
			arguments: json.RawMessage(`{"keywords":"callback"}`),
			wantEnd:   anchor.Format(time.RFC3339Nano),
		},
		{
			name:      "future end time",
			arguments: json.RawMessage(`{"keywords":"callback","end_time":"2026-07-30T00:00:00Z"}`),
			wantEnd:   anchor.Format(time.RFC3339Nano),
		},
		{
			name:      "invalid end time",
			arguments: json.RawMessage(`{"keywords":"callback","end_time":"later"}`),
			wantEnd:   anchor.Format(time.RFC3339Nano),
		},
		{
			name:      "past end time",
			arguments: json.RawMessage(`{"keywords":"callback","end_time":"2026-07-29T14:00:00Z"}`),
			wantEnd:   "2026-07-29T14:00:00Z",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := conversationeval.ClampCandidateSearchHistoryArguments(test.arguments, anchor)
			if err != nil {
				t.Fatalf("ClampCandidateSearchHistoryArguments() error = %v", err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(got, &object); err != nil {
				t.Fatalf("arguments = %s: %v", got, err)
			}
			var endTime string
			if err := json.Unmarshal(object["end_time"], &endTime); err != nil {
				t.Fatalf("end_time = %s: %v", object["end_time"], err)
			}
			if endTime != test.wantEnd {
				t.Fatalf("end_time = %q, want %q", endTime, test.wantEnd)
			}
		})
	}
}

func TestClampCandidateSearchHistoryTopK(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 123, time.UTC)
	tests := []struct {
		name      string
		arguments json.RawMessage
		wantTopK  int
	}{
		{name: "missing uses bounded default", arguments: json.RawMessage(`{}`), wantTopK: 20},
		{name: "below minimum", arguments: json.RawMessage(`{"top_k":0}`), wantTopK: 1},
		{name: "within range", arguments: json.RawMessage(`{"top_k":7}`), wantTopK: 7},
		{name: "above maximum", arguments: json.RawMessage(`{"top_k":200}`), wantTopK: 20},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := conversationeval.ClampCandidateSearchHistoryArguments(test.arguments, anchor)
			if err != nil {
				t.Fatalf("ClampCandidateSearchHistoryArguments() error = %v", err)
			}
			var object struct {
				TopK int `json:"top_k"`
			}
			if err := json.Unmarshal(got, &object); err != nil {
				t.Fatalf("arguments = %s: %v", got, err)
			}
			if object.TopK != test.wantTopK {
				t.Fatalf("top_k = %d, want %d", object.TopK, test.wantTopK)
			}
		})
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
	allTools := larktools(context.Background())
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
	allTools := larktools(context.Background())

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
	allTools := larktools(context.Background())
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

	allTools := larktools(context.Background())

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

func TestLarkToolsEmitStrictCompatibleSchemas(t *testing.T) {
	useWorkspaceConfigPath(t)
	allTools := larktools(context.Background())

	for _, tool := range allTools.Tools() {
		fn := tool.GetToolFunction()
		if fn == nil {
			continue
		}
		if !fn.GetStrict() {
			t.Fatalf("%s strict = false, want true", fn.GetName())
		}
		var schema map[string]any
		if err := json.Unmarshal(fn.GetParameters().GetValue(), &schema); err != nil {
			t.Fatalf("%s parameters should be json: %v", fn.GetName(), err)
		}
		if strings.HasPrefix(fn.GetName(), "luckin_") {
			continue
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("%s root additionalProperties = %#v, want false", fn.GetName(), schema["additionalProperties"])
		}
		props, _ := schema["properties"].(map[string]any)
		for propName, raw := range props {
			prop, _ := raw.(map[string]any)
			if prop["type"] == "array" {
				if _, ok := prop["items"].(map[string]any); !ok {
					t.Fatalf("%s.%s array items = %#v, want schema object", fn.GetName(), propName, prop["items"])
				}
			}
		}
	}
}

func TestAgentCardToolsExposeTypedStrictSchemasOnlyWhenInjected(t *testing.T) {
	useWorkspaceConfigPath(t)
	if _, exists := BuildLarkTools().Get("compose_card"); exists {
		t.Fatal("compose_card is available without a scoped service")
	}
	tools := BuildLarkToolsWithAgentCardService(&agentCardToolServiceFake{})
	discover, exists := tools.Get("discover_card_components")
	if !exists {
		t.Fatal("discover_card_components is missing")
	}
	for _, name := range []string{"version", "category", "name"} {
		if discover.Parameters.Props[name] == nil {
			t.Fatalf("discover filter %q is missing", name)
		}
	}
	compose, exists := tools.Get("compose_card")
	if !exists {
		t.Fatal("compose_card is missing")
	}
	for _, name := range []string{"purpose", "card", "interaction"} {
		if compose.Parameters.Props[name] == nil {
			t.Fatalf("compose field %q is missing", name)
		}
	}
	for _, forbidden := range []string{
		"runtime", "runtime_envelope", "raw_json", "callback", "token",
		"trusted_capability", "capability_input",
	} {
		if compose.Parameters.Props[forbidden] != nil {
			t.Fatalf("compose schema exposes forbidden field %q", forbidden)
		}
	}
	card := compose.Parameters.Props["card"]
	interaction := compose.Parameters.Props["interaction"]
	if card.Type != "object" || card.Props["blocks"].Type != "array" ||
		card.Props["blocks"].Items == nil ||
		card.Props["blocks"].Items.Type != "object" ||
		card.Props["actions"].Items == nil ||
		card.Props["actions"].Items.Type != "object" ||
		interaction.Type != "object" ||
		interaction.Props["mode"] == nil ||
		interaction.Props["expires_in_seconds"] == nil {
		t.Fatalf("compose schema is not typed: card=%#v interaction=%#v", card, interaction)
	}
	schema := map[string]any{}
	if err := json.Unmarshal(compose.Parameters.JSON(), &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("compose additionalProperties = %#v", schema["additionalProperties"])
	}
	description := strings.ToLower(compose.Description)
	for _, guidance := range []string{"text", "secret", "duplicate"} {
		if !strings.Contains(description, guidance) {
			t.Fatalf("compose description lacks %q guidance: %s", guidance, compose.Description)
		}
	}
}

func TestAgentCardToolReturnsStructuredRepairResult(t *testing.T) {
	service := &agentCardToolServiceFake{
		compose: agentcardtool.ComposeResponse{
			Status: "repair_required", Attempt: 1,
			Issues: []agentcardtool.Issue{{
				Code: "title_length", Path: "$.card.title",
			}},
		},
	}
	tools := BuildLarkToolsWithAgentCardService(service)
	compose, ok := tools.Get("compose_card")
	if !ok {
		t.Fatal("compose_card is missing")
	}
	result := compose.Function(
		context.Background(),
		`{"purpose":"confirmation","card":{"title":"","blocks":[]},"interaction":{"mode":"ui_action"}}`,
		toolkit.FCMeta[larkim.P2MessageReceiveV1]{
			ChatID: "chat-1", OpenID: "owner-1",
		},
	)
	if result.IsErr() ||
		!strings.Contains(result.Value(), `"status":"repair_required"`) ||
		!strings.Contains(result.Value(), `"attempt":1`) {
		t.Fatalf("compose result = %#v", result)
	}
}
