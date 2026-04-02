package handlers

import (
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

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

func TestBuildStandardChatSystemPromptContainsV2CoreRules(t *testing.T) {
	prompt := buildStandardChatSystemPrompt(standardPromptModeAmbient)
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
	prompt := buildStandardChatSystemPrompt(standardPromptModeDirect)
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

func chatHandlerStrPtr(v string) *string { return &v }
