package handlers

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
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

func TestBuildSchedulableToolsIncludesAgentRuntimeResumeOnlyForScheduler(t *testing.T) {
	useWorkspaceConfigPath(t)
	schedulable := BuildSchedulableTools()
	allTools := larktools()

	if _, ok := schedulable.FunctionCallMap["agent_runtime_resume"]; !ok {
		t.Fatal("schedulable tools missing agent_runtime_resume")
	}
	if _, ok := allTools.FunctionCallMap["agent_runtime_resume"]; ok {
		t.Fatal("lark tools should not expose internal agent_runtime_resume tool")
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

func TestLarkToolsDeferSelectedSideEffectsWhenCollectorPresent(t *testing.T) {
	useWorkspaceConfigPath(t)
	appconfig.SetGetFeaturesFunc(func() []appconfig.Feature {
		return []appconfig.Feature{
			{Name: "chat", Description: "聊天"},
		}
	})
	defer appconfig.SetGetFeaturesFunc(nil)

	allTools := larktools()
	cases := []struct {
		name           string
		raw            string
		expectedResult string
		expectedTitle  string
	}{
		{
			name:           "send_message",
			raw:            `{"content":"hi"}`,
			expectedResult: "已发起审批，等待确认后发送消息。",
			expectedTitle:  "审批发送消息",
		},
		{
			name:           "config_set",
			raw:            `{"key":"chat_mode","value":"agentic","scope":"chat"}`,
			expectedResult: "已发起审批，等待确认后修改配置。",
			expectedTitle:  "审批修改配置",
		},
		{
			name:           "config_delete",
			raw:            `{"key":"chat_mode","scope":"chat"}`,
			expectedResult: "已发起审批，等待确认后删除配置。",
			expectedTitle:  "审批删除配置",
		},
		{
			name:           "feature_block",
			raw:            `{"feature":"chat","scope":"chat"}`,
			expectedResult: "已发起审批，等待确认后屏蔽功能。",
			expectedTitle:  "审批屏蔽功能",
		},
		{
			name:           "feature_unblock",
			raw:            `{"feature":"chat","scope":"chat"}`,
			expectedResult: "已发起审批，等待确认后恢复功能。",
			expectedTitle:  "审批恢复功能",
		},
		{
			name:           "mute_robot",
			raw:            `{"time":"5m"}`,
			expectedResult: "已发起审批，等待确认后调整禁言。",
			expectedTitle:  "审批设置禁言",
		},
		{
			name:           "word_add",
			raw:            `{"word":"收到","rate":80}`,
			expectedResult: "已发起审批，等待确认后更新复读词条。",
			expectedTitle:  "审批更新复读词条",
		},
		{
			name:           "reply_add",
			raw:            `{"word":"你好","type":"full","reply":"你好呀"}`,
			expectedResult: "已发起审批，等待确认后添加关键词回复。",
			expectedTitle:  "审批添加关键词回复",
		},
		{
			name:           "image_add",
			raw:            `{"img_key":"img_test"}`,
			expectedResult: "已发起审批，等待确认后添加图片素材。",
			expectedTitle:  "审批添加图片素材",
		},
		{
			name:           "image_delete",
			raw:            `{"img_key":"img_test"}`,
			expectedResult: "已发起审批，等待确认后删除图片素材。",
			expectedTitle:  "审批删除图片素材",
		},
		{
			name:           "revert_message",
			raw:            `{}`,
			expectedResult: "已发起审批，等待确认后撤回消息。",
			expectedTitle:  "审批撤回消息",
		},
		{
			name:           "permission_manage",
			raw:            `{"user_id":"ou_target"}`,
			expectedResult: "已发起审批，等待确认后发送权限管理卡片。",
			expectedTitle:  "审批权限管理",
		},
		{
			name:           "oneword_get",
			raw:            `{"type":"诗词"}`,
			expectedResult: "已发起审批，等待确认后发送一言。",
			expectedTitle:  "审批发送一言",
		},
		{
			name:           "music_search",
			raw:            `{"type":"song","keywords":"稻香"}`,
			expectedResult: "已发起审批，等待确认后发送音乐卡片。",
			expectedTitle:  "审批发送音乐卡片",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unit, ok := allTools.Get(tc.name)
			if !ok {
				t.Fatalf("expected %s tool", tc.name)
			}

			ctx := runtimecontext.WithDeferredToolCallCollector(context.Background(), runtimecontext.NewDeferredToolCallCollector())
			result := unit.Function(ctx, tc.raw, toolkit.FCMeta[larkim.P2MessageReceiveV1]{
				ChatID: "oc_self",
				OpenID: "ou_user",
			})
			if result.IsErr() {
				t.Fatalf("tool returned error: %v", result.Err())
			}
			if result.Value() != tc.expectedResult {
				t.Fatalf("result = %q, want %q", result.Value(), tc.expectedResult)
			}

			deferred, ok := runtimecontext.PopDeferredToolCall(ctx)
			if !ok {
				t.Fatal("expected deferred tool call to be recorded")
			}
			if deferred.ApprovalType != "capability" {
				t.Fatalf("approval type = %q, want %q", deferred.ApprovalType, "capability")
			}
			if deferred.ApprovalTitle != tc.expectedTitle {
				t.Fatalf("approval title = %q, want %q", deferred.ApprovalTitle, tc.expectedTitle)
			}
			if strings.TrimSpace(deferred.PlaceholderOutput) != tc.expectedResult {
				t.Fatalf("placeholder output = %q, want %q", deferred.PlaceholderOutput, tc.expectedResult)
			}
		})
	}
}
