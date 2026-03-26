package chatflow

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestBuildInitialChatExecutionPlanUsesReplyScopedContext(t *testing.T) {
	originalUserNameLoader := initialChatUserNameLoader
	originalHistoryLoader := agenticChatHistoryLoader
	originalReplyScopeLoader := agenticChatReplyScopeLoader
	originalRecallDocs := agenticChatRecallDocs
	originalChunkIndexResolver := agenticChatChunkIndexResolver
	originalTopicLookup := agenticChatTopicSummaryLookup
	originalBotProfileLoader := chatPromptBotProfileLoader
	defer func() {
		initialChatUserNameLoader = originalUserNameLoader
		agenticChatHistoryLoader = originalHistoryLoader
		agenticChatReplyScopeLoader = originalReplyScopeLoader
		agenticChatRecallDocs = originalRecallDocs
		agenticChatChunkIndexResolver = originalChunkIndexResolver
		agenticChatTopicSummaryLookup = originalTopicLookup
		chatPromptBotProfileLoader = originalBotProfileLoader
	}()
	initialChatUserNameLoader = func(ctx context.Context, chatID, openID string) (string, error) { return "Alice", nil }
	chatPromptBotProfileLoader = func(ctx context.Context) botidentity.Profile {
		return botidentity.Profile{AppID: "cli_test_app", BotOpenID: "ou_bot_self", BotName: "BetaGo"}
	}
	agenticChatHistoryLoader = func(ctx context.Context, chatID string, size int) (history.OpensearchMsgLogList, error) {
		t.Fatal("broad chat history should not be loaded for reply-scoped standard context")
		return nil, nil
	}
	agenticChatReplyScopeLoader = func(ctx context.Context, event *larkim.P2MessageReceiveV1) (agenticReplyScopeContext, bool, error) {
		return agenticReplyScopeContext{
			MessageList: history.OpensearchMsgLogList{
				{CreateTime: "2026-03-20 10:00:00", OpenID: "ou_user", UserName: "Alice", MsgList: []string{"请继续推进 agentic 改造"}},
				{CreateTime: "2026-03-20 10:01:00", OpenID: "ou_bot", UserName: "Agent", MsgList: []string{"最后一步是首轮 turn durable 化。"}},
			},
			ContextLines: []string{"关联运行状态: waiting_approval"},
		}, true, nil
	}
	agenticChatRecallDocs = func(ctx context.Context, chatID, query string, topK int) ([]schema.Document, error) {
		t.Fatal("reply-scoped standard prompt should not preload recall docs")
		return nil, nil
	}
	agenticChatChunkIndexResolver = func(ctx context.Context, chatID, openID string) string {
		t.Fatal("reply-scoped standard prompt should not preload topic summaries")
		return ""
	}
	agenticChatTopicSummaryLookup = func(ctx context.Context, chunkIndex, msgID string) (string, error) {
		t.Fatal("reply-scoped standard prompt should not lookup topic summaries")
		return "", nil
	}

	plan, err := BuildInitialChatExecutionPlan(context.Background(), InitialChatGenerationRequest{
		Event:           testAgenticReplyEvent("oc_chat", "ou_actor", "om_parent", "这里展开一下"),
		ModelID:         "ep-test",
		Size:            20,
		ReasoningEffort: responses.ReasoningEffort_high,
		Tools:           &arktools.Impl[larkim.P2MessageReceiveV1]{},
	})
	if err != nil {
		t.Fatalf("BuildInitialChatExecutionPlan() error = %v", err)
	}
	if got := strings.Join(plan.MessageList.ToLines(), "\n"); !strings.Contains(got, "最后一步是首轮 turn durable 化。") {
		t.Fatalf("message list = %q, want contain scoped parent reply", got)
	}
	if !strings.Contains(plan.Prompt, "当前属于 direct reply") {
		t.Fatalf("prompt = %q, want direct reply guidance", plan.Prompt)
	}
	if !strings.Contains(plan.Prompt, "只输出 JSON object") || !strings.Contains(plan.Prompt, `"decision"`) {
		t.Fatalf("prompt should keep JSON contract: %q", plan.Prompt)
	}
	if strings.Contains(plan.Prompt, "关联运行状态: waiting_approval") {
		t.Fatalf("runtime context should move to user input, got prompt: %q", plan.Prompt)
	}
	if !strings.Contains(plan.UserInput, "关联运行状态: waiting_approval") {
		t.Fatalf("user input should carry scoped runtime context: %q", plan.UserInput)
	}
	if !strings.Contains(plan.UserInput, "最后一步是首轮 turn durable 化。") {
		t.Fatalf("user input should carry recent thread history: %q", plan.UserInput)
	}
	if !strings.Contains(plan.UserInput, "self_open_id: ou_bot_self") || !strings.Contains(plan.UserInput, "self_name: BetaGo") {
		t.Fatalf("user input should carry bot self identity: %q", plan.UserInput)
	}
}

func TestBuildInitialChatExecutionPlanUsesAmbientPromptAndSkipsBroadRecall(t *testing.T) {
	originalUserNameLoader := initialChatUserNameLoader
	originalHistoryLoader := agenticChatHistoryLoader
	originalReplyScopeLoader := agenticChatReplyScopeLoader
	originalRecallDocs := agenticChatRecallDocs
	originalChunkIndexResolver := agenticChatChunkIndexResolver
	originalTopicLookup := agenticChatTopicSummaryLookup
	defer func() {
		initialChatUserNameLoader = originalUserNameLoader
		agenticChatHistoryLoader = originalHistoryLoader
		agenticChatReplyScopeLoader = originalReplyScopeLoader
		agenticChatRecallDocs = originalRecallDocs
		agenticChatChunkIndexResolver = originalChunkIndexResolver
		agenticChatTopicSummaryLookup = originalTopicLookup
	}()
	initialChatUserNameLoader = func(ctx context.Context, chatID, openID string) (string, error) { return "Alice", nil }
	agenticChatHistoryLoader = func(ctx context.Context, chatID string, size int) (history.OpensearchMsgLogList, error) {
		return history.OpensearchMsgLogList{
			{CreateTime: "2026-03-20 09:00:00", OpenID: "ou_1", UserName: "U1", MsgList: []string{"第一条"}},
			{CreateTime: "2026-03-20 09:01:00", OpenID: "ou_2", UserName: "U2", MsgList: []string{"第二条"}},
			{CreateTime: "2026-03-20 09:02:00", OpenID: "ou_3", UserName: "U3", MsgList: []string{"第三条"}},
			{CreateTime: "2026-03-20 09:03:00", OpenID: "ou_4", UserName: "U4", MsgList: []string{"第四条"}},
			{CreateTime: "2026-03-20 09:04:00", OpenID: "ou_5", UserName: "U5", MsgList: []string{"第五条"}},
		}, nil
	}
	agenticChatReplyScopeLoader = func(ctx context.Context, event *larkim.P2MessageReceiveV1) (agenticReplyScopeContext, bool, error) {
		return agenticReplyScopeContext{}, false, nil
	}
	agenticChatRecallDocs = func(ctx context.Context, chatID, query string, topK int) ([]schema.Document, error) {
		t.Fatal("ambient standard prompt should not preload broad recall docs")
		return nil, nil
	}
	agenticChatChunkIndexResolver = func(ctx context.Context, chatID, openID string) string {
		t.Fatal("ambient standard prompt should not preload topic summaries")
		return ""
	}
	agenticChatTopicSummaryLookup = func(ctx context.Context, chunkIndex, msgID string) (string, error) {
		t.Fatal("ambient standard prompt should not lookup topic summaries")
		return "", nil
	}

	event := testAgenticReplyEvent("oc_chat", "ou_actor", "", "随便聊聊这个话题")
	chatType := "group"
	event.Event.Message.ChatType = &chatType

	plan, err := BuildInitialChatExecutionPlan(context.Background(), InitialChatGenerationRequest{
		Event:           event,
		ModelID:         "ep-test",
		Size:            20,
		ReasoningEffort: responses.ReasoningEffort_minimal,
		Tools:           &arktools.Impl[larkim.P2MessageReceiveV1]{},
	})
	if err != nil {
		t.Fatalf("BuildInitialChatExecutionPlan() error = %v", err)
	}
	if !strings.Contains(plan.Prompt, "当前属于 ambient/passive reply") {
		t.Fatalf("prompt = %q, want ambient reply guidance", plan.Prompt)
	}
	if strings.Contains(plan.Prompt, "第一条") {
		t.Fatalf("system prompt should not inline broad history: %q", plan.Prompt)
	}
	if strings.Contains(plan.Prompt, "第五条") {
		t.Fatalf("recent history should move to user input, got prompt: %q", plan.Prompt)
	}
	if !strings.Contains(plan.Prompt, "只输出 JSON object") || !strings.Contains(plan.Prompt, `"reference_from_history"`) {
		t.Fatalf("prompt should keep JSON contract: %q", plan.Prompt)
	}
	if strings.Contains(plan.UserInput, "第一条") {
		t.Fatalf("user input should trim broad history for ambient reply: %q", plan.UserInput)
	}
	if !strings.Contains(plan.UserInput, "第五条") {
		t.Fatalf("user input should retain latest history lines: %q", plan.UserInput)
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
	prompt := buildStandardChatUserPrompt(botidentity.Profile{
		AppID:     "cli_test_app",
		BotOpenID: "ou_bot_self",
		BotName:   "BetaGo",
	}, []string{"[09:01] <A>: 第二条", "[09:02] <B>: 第三条"}, []string{"关联运行状态: waiting_approval"}, "[09:03] <Alice>: 这里展开一下")
	for _, want := range []string{"机器人身份", "self_open_id: ou_bot_self", "self_name: BetaGo", "最近对话", "第二条", "第三条", "补充上下文", "waiting_approval", "当前用户消息", "这里展开一下"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}
