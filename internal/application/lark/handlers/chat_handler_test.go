package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intentmeta"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestResolveStandardPromptMode(t *testing.T) {
	useWorkspaceConfigPath(t)
	group := "group"
	p2p := "p2p"
	botOpenID := "ou_test_bot"
	configPath := filepath.Join(t.TempDir(), "test_config.toml")
	if err := os.WriteFile(configPath, []byte("[lark_config]\nbot_open_id = \""+botOpenID+"\"\n"), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	if _, err := infraConfig.LoadFileE(configPath); err != nil {
		t.Fatalf("load temp config: %v", err)
	}

	if got := resolveStandardPromptMode(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{ChatType: &p2p},
		},
	}); got != standardPromptModeDirect {
		t.Fatalf("p2p prompt mode = %q, want %q", got, standardPromptModeDirect)
	}

	if got := resolveStandardPromptMode(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatType: &group,
				Mentions: []*larkim.MentionEvent{{
					Id: &larkim.UserId{OpenId: chatHandlerStrPtr(botOpenID)},
				}},
			},
		},
	}); got != standardPromptModeDirect {
		t.Fatalf("mention prompt mode = %q, want %q", got, standardPromptModeDirect)
	}

	if got := resolveStandardPromptMode(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{ChatType: &group},
		},
	}); got != standardPromptModeAmbient {
		t.Fatalf("group prompt mode = %q, want %q", got, standardPromptModeAmbient)
	}
}

func TestBuildStandardChatSystemPromptContainsV2CoreRules(t *testing.T) {
	prompt := buildStandardChatSystemPrompt(context.Background(), standardPromptModeAmbient, "")
	for _, want := range []string{
		"# 任务",
		"# 输入",
		"消息含 file_key 时",
		"每个 @名字 后必须有一个空格",
		"thought 仅用 1-2 句话说明",
		"不得输出 JSON 以外内容",
		`"decision"`,
		`"thought"`,
		`"reply"`,
		`"reference_from_web"`,
		`"reference_from_history"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestBuildStandardChatSystemPromptGuidesMentionsAndThreadContinuation(t *testing.T) {
	prompt := buildStandardChatSystemPrompt(context.Background(), standardPromptModeDirect, "")
	for _, want := range []string{
		"默认应回答，不要轻易 skip",
		"优先直接延续当前子话题",
		"不要为了点名而重复 @",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestBuildStandardChatSystemPromptGuidesFinanceToolDiscovery(t *testing.T) {
	prompt := buildStandardChatSystemPrompt(context.Background(), standardPromptModeAmbient, "")
	for _, want := range []string{
		"优先使用金融工具而不是 web_search",
		"先调用 finance_tool_discover",
		"只使用 category 或 tool_names 这类枚举参数",
		"不要停在 discover 结果本身",
		"结构化行情、新闻和指标查询优先用金融工具",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestBuildStandardChatSystemPromptRestrictsLuckinTriggerToExplicitOrdering(t *testing.T) {
	prompt := buildStandardChatSystemPrompt(context.Background(), standardPromptModeAmbient, "")
	if !strings.Contains(prompt, "明确表达想点咖啡、买咖啡、下瑞幸订单、加购饮品、结算瑞幸购物车") {
		t.Fatalf("prompt = %q, want explicit luckin ordering trigger", prompt)
	}
	for _, unwanted := range []string{"查看门店", "开始点单"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("prompt should not contain %q: %q", unwanted, prompt)
		}
	}
}

func TestBuildStandardChatUserPromptCarriesRecentHistoryAndCurrentInput(t *testing.T) {
	prompt := buildStandardChatUserPrompt(botidentity.Profile{}, []string{"[09:01] <A>: 第二条", "[09:02] <B>: 第三条"}, nil, "[09:03] <Alice>: 这里展开一下")
	for _, want := range []string{"最近对话", "第二条", "第三条", "当前用户消息", "这里展开一下"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestBuildStandardChatUserPromptIncludesSelfIdentity(t *testing.T) {
	prompt := buildStandardChatUserPrompt(botidentity.Profile{
		AppID:     "cli_test_app",
		BotOpenID: "ou_bot_self",
		BotName:   "BetaGo",
	}, []string{"[09:01] <A>: 第二条"}, nil, "[09:03] <Alice>: 这里展开一下")
	for _, want := range []string{
		"机器人身份",
		"self_open_id: ou_bot_self",
		"sender user_id/open_id 等于 self_open_id",
		"mention target open_id 等于 self_open_id",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

//go:fix inline
func chatHandlerStrPtr(v string) *string { return new(v) }

func TestShouldUseStreamingCardByIntent(t *testing.T) {
	if shouldUseStreamingCard(nil) {
		t.Fatalf("nil meta should not stream")
	}

	meta := &xhandler.BaseMetaData{}
	if shouldUseStreamingCard(meta) {
		t.Fatalf("meta without intent should not stream")
	}

	// baseline: question + professional + effort>=low -> stream
	meta.SetIntentAnalysis(&intentmeta.IntentAnalysis{
		IntentType:      intentmeta.IntentTypeQuestion,
		Domain:          intentmeta.DomainProfessional,
		NeedReply:       true,
		ReasoningEffort: responses.ReasoningEffort_low,
	})
	if !shouldUseStreamingCard(meta) {
		t.Fatalf("professional question+low effort should stream")
	}

	// higher effort should also stream
	meta.SetIntentAnalysis(&intentmeta.IntentAnalysis{
		IntentType:      intentmeta.IntentTypeQuestion,
		Domain:          intentmeta.DomainProfessional,
		NeedReply:       true,
		ReasoningEffort: responses.ReasoningEffort_high,
	})
	if !shouldUseStreamingCard(meta) {
		t.Fatalf("professional question+high effort should stream")
	}

	// casual question ("今天吃啥") -> no card even if effort low
	meta.SetIntentAnalysis(&intentmeta.IntentAnalysis{
		IntentType:      intentmeta.IntentTypeQuestion,
		Domain:          intentmeta.DomainCasual,
		NeedReply:       true,
		ReasoningEffort: responses.ReasoningEffort_low,
	})
	if shouldUseStreamingCard(meta) {
		t.Fatalf("casual question should not stream")
	}

	// professional but minimal effort (simple fact that doesn't need reasoning) -> no card
	meta.SetIntentAnalysis(&intentmeta.IntentAnalysis{
		IntentType:      intentmeta.IntentTypeQuestion,
		Domain:          intentmeta.DomainProfessional,
		NeedReply:       true,
		ReasoningEffort: responses.ReasoningEffort_minimal,
	})
	if shouldUseStreamingCard(meta) {
		t.Fatalf("professional question with minimal effort should not stream")
	}

	// chat intent should never stream
	meta.SetIntentAnalysis(&intentmeta.IntentAnalysis{
		IntentType:      intentmeta.IntentTypeChat,
		Domain:          intentmeta.DomainProfessional,
		NeedReply:       true,
		ReasoningEffort: responses.ReasoningEffort_medium,
	})
	if shouldUseStreamingCard(meta) {
		t.Fatalf("chat intent should not stream")
	}

	// need_reply=false -> no stream
	meta.SetIntentAnalysis(&intentmeta.IntentAnalysis{
		IntentType:      intentmeta.IntentTypeQuestion,
		Domain:          intentmeta.DomainProfessional,
		NeedReply:       false,
		ReasoningEffort: responses.ReasoningEffort_low,
	})
	if shouldUseStreamingCard(meta) {
		t.Fatalf("question with need_reply=false should not stream")
	}
}
