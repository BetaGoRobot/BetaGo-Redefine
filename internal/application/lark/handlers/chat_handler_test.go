package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestChatGenerationPlanGenerateReturnsNotConfiguredWithoutRegisteredExecutor(t *testing.T) {
	agentruntime.SetChatGenerationPlanExecutor(nil)

	_, err := (agentruntime.ChatGenerationPlan{}).Generate(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when executor is not registered")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveChatExecutionModeUsesInteractionModeOverride(t *testing.T) {
	meta := &xhandler.BaseMetaData{}
	meta.SetIntentAnalysis(&intent.IntentAnalysis{InteractionMode: intent.InteractionModeStandard})

	if got := resolveChatExecutionMode(meta); got != intent.InteractionModeStandard {
		t.Fatalf("resolveChatExecutionMode() = %q, want %q", got, intent.InteractionModeStandard)
	}

	meta.SetIntentAnalysis(&intent.IntentAnalysis{InteractionMode: intent.InteractionModeAgentic})
	if got := resolveChatExecutionMode(meta); got != intent.InteractionModeAgentic {
		t.Fatalf("resolveChatExecutionMode() = %q, want %q", got, intent.InteractionModeAgentic)
	}
}

func TestResolveChatExecutionModeDefaultsToStandardWithoutDecision(t *testing.T) {
	if got := resolveChatExecutionMode(&xhandler.BaseMetaData{}); got != intent.InteractionModeStandard {
		t.Fatalf("resolveChatExecutionMode() = %q, want %q", got, intent.InteractionModeStandard)
	}
}

func TestResolveStandardPromptMode(t *testing.T) {
	useWorkspaceConfigPath(t)
	group := "group"
	p2p := "p2p"

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
					Id: &larkim.UserId{OpenId: chatHandlerStrPtr(infraConfig.Get().LarkConfig.BotOpenID)},
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

func TestBuildStandardChatSystemPromptConstrainsAnthropomorphicParticles(t *testing.T) {
	prompt := buildStandardChatSystemPrompt(standardPromptModeAmbient)
	for _, want := range []string{
		"少用语气词",
		"不要为了显得亲近而堆砌“哟”“呀”“啦”这类口头禅",
		"拟人感过强",
		"只输出 JSON object",
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
	prompt := buildStandardChatSystemPrompt(standardPromptModeDirect)
	for _, want := range []string{
		"只有在需要某个具体成员响应",
		"@姓名",
		"<at user_id=\"open_id\">姓名</at>",
		"优先直接延续当前子话题",
		"不要为了点名而重复 @",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
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
		"self_name: BetaGo",
		"sender user_id/open_id 等于 self_open_id",
		"mention target open_id 等于 self_open_id",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func chatHandlerStrPtr(v string) *string { return &v }
