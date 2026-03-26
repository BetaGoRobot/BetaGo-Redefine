package chatflow

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
)

func TestAgenticChatSystemPromptEmphasizesRuntimeOwnedAgentLoop(t *testing.T) {
	prompt := agenticChatSystemPrompt()
	for _, want := range []string{"durable agent", "工具", "JSON object", "thought", "reply", "一次只发起一个", "像群里的正常成员一样说话", "只发起一个 function call", "不能同时输出"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestAgenticChatSystemPromptGuidesReadQueriesAndApprovalActions(t *testing.T) {
	prompt := agenticChatSystemPrompt()
	for _, want := range []string{"实时数据", "优先调用查询工具", "不要直接口头说“已经完成”", "让 runtime 进入审批或等待流程"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestAgenticChatSystemPromptGuidesDeepResearchWorkflow(t *testing.T) {
	prompt := agenticChatSystemPrompt()
	for _, want := range []string{"调研", "搜索", "阅读", "research_read_url", "research_extract_evidence", "research_source_ledger", "证据不足", "不要在第一次查询后直接结束"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestAgenticChatSystemPromptConstrainsAnthropomorphicParticles(t *testing.T) {
	prompt := agenticChatSystemPrompt()
	for _, want := range []string{
		"少用语气词",
		"不要为了显得亲近而堆砌“哟”“呀”“啦”这类口头禅",
		"拟人感过强",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestAgenticChatSystemPromptGuidesMentionsAndCallbacks(t *testing.T) {
	prompt := agenticChatSystemPrompt()
	for _, want := range []string{
		"只有在需要某个具体成员响应",
		"@姓名",
		"<at user_id=\"open_id\">姓名</at>",
		"优先沿当前线程或当前子话题继续",
		"不要为了刷存在感乱 @",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestBuildAgenticChatUserPromptIncludesRuntimeContextSections(t *testing.T) {
	prompt := buildAgenticChatUserPrompt(agenticChatPromptContext{
		UserRequest: "帮我总结今天讨论并给下一步建议",
		SelfProfile: botidentity.Profile{
			AppID:     "cli_test_app",
			BotOpenID: "ou_bot_self",
			BotName:   "BetaGo",
		},
		HistoryLines: []string{
			"[10:00](ou_a) <Alice>: 今天主要讨论了 agentic runtime",
		},
		ContextLines: []string{
			"[09:55](ou_b) <Bob>: 我们需要一套新的 agentic prompt",
		},
		Topics: []string{"agentic runtime", "prompt 改造"},
		Files:  []string{"https://example.com/a.png"},
	})

	for _, want := range []string{"机器人身份", "self_open_id: ou_bot_self", "self_name: BetaGo", "对话边界", "回复风格", "当前用户请求", "最近对话", "召回上下文", "主题线索", "附件", "帮我总结今天讨论并给下一步建议"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}

func TestBuildAgenticChatUserPromptUsesReplyScopedBoundaryHint(t *testing.T) {
	prompt := buildAgenticChatUserPrompt(agenticChatPromptContext{UserRequest: "这里再补一句", ReplyScoped: true})
	if !strings.Contains(prompt, "当前是对某条消息的定向续聊") {
		t.Fatalf("prompt = %q, want contain reply-scoped boundary hint", prompt)
	}
	if !strings.Contains(prompt, "延续当前子话题") {
		t.Fatalf("prompt = %q, want contain thread continuation hint", prompt)
	}
}

func TestBuildAgenticChatPromptContextUsesReplyScopedContext(t *testing.T) {
	originalHistoryLoader := agenticChatHistoryLoader
	originalReplyScopeLoader := agenticChatReplyScopeLoader
	originalRecallDocs := agenticChatRecallDocs
	originalChunkIndexResolver := agenticChatChunkIndexResolver
	originalTopicLookup := agenticChatTopicSummaryLookup
	originalBotProfileLoader := chatPromptBotProfileLoader
	defer func() {
		agenticChatHistoryLoader = originalHistoryLoader
		agenticChatReplyScopeLoader = originalReplyScopeLoader
		agenticChatRecallDocs = originalRecallDocs
		agenticChatChunkIndexResolver = originalChunkIndexResolver
		agenticChatTopicSummaryLookup = originalTopicLookup
		chatPromptBotProfileLoader = originalBotProfileLoader
	}()

	chatPromptBotProfileLoader = func(ctx context.Context) botidentity.Profile {
		return botidentity.Profile{AppID: "cli_test_app", BotOpenID: "ou_bot_self", BotName: "BetaGo"}
	}
	agenticChatHistoryLoader = func(ctx context.Context, chatID string, size int) (history.OpensearchMsgLogList, error) {
		t.Fatal("broad chat history should not be loaded for reply-scoped context")
		return nil, nil
	}

	scopedMessages := history.OpensearchMsgLogList{
		{CreateTime: "2026-03-20 10:00:00", OpenID: "ou_user", UserName: "Alice", MsgList: []string{"请把 agentic runtime 的最后一步展开"}},
		{CreateTime: "2026-03-20 10:01:00", OpenID: "ou_bot", UserName: "Agent", MsgList: []string{"最后一步是把首轮 model turn 也纳入 durable runtime。"}},
	}
	agenticChatReplyScopeLoader = func(ctx context.Context, event *larkim.P2MessageReceiveV1) (agenticReplyScopeContext, bool, error) {
		return agenticReplyScopeContext{
			MessageList:  scopedMessages,
			ContextLines: []string{"关联运行状态: waiting_approval", "关联计划: 首轮 model turn durable 化"},
			RecallQuery:  "请把 agentic runtime 的最后一步展开 首轮 model turn durable 化",
		}, true, nil
	}
	agenticChatRecallDocs = func(ctx context.Context, chatID, query string, topK int) ([]schema.Document, error) {
		if !strings.Contains(query, "首轮 model turn durable 化") {
			t.Fatalf("recall query = %q, want contain reply-scoped plan", query)
		}
		return []schema.Document{{PageContent: "reply scoped recalled context", Metadata: map[string]any{"create_time": "2026-03-20 09:59:00", "user_id": "ou_arch", "user_name": "Bob"}}}, nil
	}
	agenticChatChunkIndexResolver = func(ctx context.Context, chatID, openID string) string { return "" }
	agenticChatTopicSummaryLookup = func(ctx context.Context, chunkIndex, msgID string) (string, error) { return "", nil }

	messageList, promptCtx, err := buildAgenticChatPromptContext(context.Background(), InitialChatGenerationRequest{
		Event:   testAgenticReplyEvent("oc_chat", "ou_actor", "om_parent", "这里展开一下"),
		ModelID: "ep-test",
		Size:    20,
	}, "oc_chat", "ou_actor")
	if err != nil {
		t.Fatalf("buildAgenticChatPromptContext() error = %v", err)
	}
	if len(messageList) != len(scopedMessages) {
		t.Fatalf("message list len = %d, want %d", len(messageList), len(scopedMessages))
	}
	if got, want := strings.Join(promptCtx.HistoryLines, "\n"), strings.Join(scopedMessages.ToLines(), "\n"); got != want {
		t.Fatalf("history lines = %q, want %q", got, want)
	}
	if got := strings.Join(promptCtx.ContextLines, "\n"); !strings.Contains(got, "关联运行状态: waiting_approval") {
		t.Fatalf("context lines = %q, want contain reply-scoped runtime context", got)
	}
	if got := strings.Join(promptCtx.ContextLines, "\n"); !strings.Contains(got, "reply scoped recalled context") {
		t.Fatalf("context lines = %q, want contain recalled context", got)
	}
	if got := promptCtx.SelfProfile; got.BotOpenID != "ou_bot_self" || got.BotName != "BetaGo" {
		t.Fatalf("self profile = %+v, want ou_bot_self/BetaGo", got)
	}
}

func TestDefaultAgenticChatReplyScopeLoaderIncludesParentChainAndRuntimeState(t *testing.T) {
	originalMessageFetcher := agenticChatMessageFetcher
	originalRunLookup := agenticChatRunLookupByResponseMessage
	originalStepLoader := agenticChatRunStepsLoader
	defer func() {
		agenticChatMessageFetcher = originalMessageFetcher
		agenticChatRunLookupByResponseMessage = originalRunLookup
		agenticChatRunStepsLoader = originalStepLoader
	}()

	agenticChatMessageFetcher = func(ctx context.Context, msgID string) (*larkim.Message, error) {
		switch msgID {
		case "om_parent":
			return testFetchedMessage("om_parent", "om_root", "app", "ou_bot", "interactive", `{"type":"card"}`, "1710900060000"), nil
		case "om_root":
			return testFetchedMessage("om_root", "", "user", "ou_user", "text", `{"text":"请继续推进 agentic 改造"}`, "1710900000000"), nil
		default:
			return nil, nil
		}
	}
	agenticChatRunLookupByResponseMessage = func(ctx context.Context, messageID string) (*agenticReplyRunState, error) {
		if messageID != "om_parent" {
			return nil, nil
		}
		return &agenticReplyRunState{RunID: "run_1", Status: "waiting_approval", Goal: "推进 agentic runtime 理想态", InputText: "请继续推进 agentic 改造", ResultSummary: "已经发起审批，等待继续执行"}, nil
	}
	agenticChatRunStepsLoader = func(ctx context.Context, runID string) ([]*agenticReplyStep, error) {
		if runID != "run_1" {
			t.Fatalf("runID = %q, want %q", runID, "run_1")
		}
		return []*agenticReplyStep{
			{Kind: stepKindPlan, Status: "completed", InputJSON: []byte(`{"thought_text":"先收敛上下文，再把首轮 turn durable 化","reply_text":"下一步是把首轮 turn 也沉淀为 plan step。","pending_capability":{"call_id":"call_pending_1","capability_name":"send_message","arguments":"{\"text\":\"继续推进\"}"}}`)},
			{Kind: stepKindReply, OutputJSON: []byte(`{"thought_text":"先收敛上下文，再把首轮 turn durable 化","reply_text":"最后一步是让首轮 model turn 也进入 durable runtime。","response_message_id":"om_parent"}`)},
		}, nil
	}

	scoped, ok, err := defaultAgenticChatReplyScopeLoader(context.Background(), testAgenticReplyEvent("oc_chat", "ou_actor", "om_parent", "这里展开一下"))
	if err != nil {
		t.Fatalf("defaultAgenticChatReplyScopeLoader() error = %v", err)
	}
	if !ok {
		t.Fatal("expected reply-scoped context to be available")
	}

	lines := scoped.MessageList.ToLines()
	if len(lines) != 2 {
		t.Fatalf("reply-scoped lines len = %d, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "请继续推进 agentic 改造") || !strings.Contains(lines[1], "最后一步是让首轮 model turn 也进入 durable runtime。") {
		t.Fatalf("lines = %+v, want contain parent chain", lines)
	}
	contextText := strings.Join(scoped.ContextLines, "\n")
	for _, want := range []string{"关联运行状态: waiting_approval", "关联计划: 推进 agentic runtime 理想态", "最近一轮计划:", "下一步是把首轮 turn 也沉淀为 plan step。", "待执行能力: send_message {\"text\":\"继续推进\"}", "最近一轮思考: 先收敛上下文，再把首轮 turn durable 化"} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context = %q, want contain %q", contextText, want)
		}
	}
}

func TestDefaultAgenticChatReplyScopeLoaderUsesShortThreadContext(t *testing.T) {
	originalThreadLoader := agenticChatReplyThreadLoader
	originalThreadLimit := agenticChatReplyScopeThreadMessageLimit
	originalRunLookup := agenticChatRunLookupByResponseMessage
	originalStepLoader := agenticChatRunStepsLoader
	originalMessageFetcher := agenticChatMessageFetcher
	defer func() {
		agenticChatReplyThreadLoader = originalThreadLoader
		agenticChatReplyScopeThreadMessageLimit = originalThreadLimit
		agenticChatRunLookupByResponseMessage = originalRunLookup
		agenticChatRunStepsLoader = originalStepLoader
		agenticChatMessageFetcher = originalMessageFetcher
	}()

	agenticChatReplyScopeThreadMessageLimit = 4
	agenticChatReplyThreadLoader = func(ctx context.Context, event *larkim.P2MessageReceiveV1) ([]*larkim.Message, error) {
		return []*larkim.Message{
			testFetchedMessage("om_root", "", "user", "ou_user", "text", `{"text":"先看这段金价分析"}`, "1710900000000"),
			testFetchedMessage("om_parent", "om_root", "app", "ou_bot", "interactive", `{"type":"card"}`, "1710900060000"),
			testFetchedMessage("om_current", "om_parent", "user", "ou_user", "text", `{"text":"再展开一下美元和实际利率"}`, "1710900120000"),
		}, nil
	}
	agenticChatMessageFetcher = func(ctx context.Context, msgID string) (*larkim.Message, error) {
		t.Fatalf("legacy compat path should not be used, fetched msg id=%q", msgID)
		return nil, nil
	}
	agenticChatRunLookupByResponseMessage = func(ctx context.Context, messageID string) (*agenticReplyRunState, error) {
		if messageID != "om_parent" {
			return nil, nil
		}
		return &agenticReplyRunState{RunID: "run_thread_1", Status: "waiting_approval", Goal: "分析金价波动", InputText: "先看这段金价分析", ResultSummary: "已经给出第一版分析框架"}, nil
	}
	agenticChatRunStepsLoader = func(ctx context.Context, runID string) ([]*agenticReplyStep, error) {
		if runID != "run_thread_1" {
			t.Fatalf("runID = %q, want %q", runID, "run_thread_1")
		}
		return []*agenticReplyStep{{Kind: stepKindReply, OutputJSON: []byte(`{"thought_text":"先按宏观因子拆解","reply_text":"主要先看美元、实际利率和避险情绪。","response_message_id":"om_parent"}`)}}, nil
	}

	event := testAgenticReplyEvent("oc_chat", "ou_actor", "om_parent", "再展开一下美元和实际利率")
	threadID, currentID := "omt_gold_thread", "om_current"
	event.Event.Message.ThreadId = &threadID
	event.Event.Message.MessageId = &currentID

	scoped, ok, err := defaultAgenticChatReplyScopeLoader(context.Background(), event)
	if err != nil || !ok {
		t.Fatalf("scope err=%v ok=%v", err, ok)
	}
	lines := scoped.MessageList.ToLines()
	if len(lines) != 2 || !strings.Contains(lines[0], "先看这段金价分析") || !strings.Contains(lines[1], "主要先看美元、实际利率和避险情绪。") {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestDefaultAgenticChatReplyScopeLoaderFallsBackToCompatWhenThreadTooLong(t *testing.T) {
	originalThreadLoader := agenticChatReplyThreadLoader
	originalThreadLimit := agenticChatReplyScopeThreadMessageLimit
	originalMessageFetcher := agenticChatMessageFetcher
	originalRunLookup := agenticChatRunLookupByResponseMessage
	originalStepLoader := agenticChatRunStepsLoader
	defer func() {
		agenticChatReplyThreadLoader = originalThreadLoader
		agenticChatReplyScopeThreadMessageLimit = originalThreadLimit
		agenticChatMessageFetcher = originalMessageFetcher
		agenticChatRunLookupByResponseMessage = originalRunLookup
		agenticChatRunStepsLoader = originalStepLoader
	}()

	agenticChatReplyScopeThreadMessageLimit = 2
	agenticChatReplyThreadLoader = func(ctx context.Context, event *larkim.P2MessageReceiveV1) ([]*larkim.Message, error) {
		return []*larkim.Message{
			testFetchedMessage("om_root", "", "user", "ou_user", "text", `{"text":"第一轮问题"}`, "1710900000000"),
			testFetchedMessage("om_mid_1", "om_root", "user", "ou_user", "text", `{"text":"第二轮追问"}`, "1710900030000"),
			testFetchedMessage("om_parent", "om_mid_1", "app", "ou_bot", "interactive", `{"type":"card"}`, "1710900060000"),
		}, nil
	}
	agenticChatMessageFetcher = func(ctx context.Context, msgID string) (*larkim.Message, error) {
		switch msgID {
		case "om_parent":
			return testFetchedMessage("om_parent", "om_root", "app", "ou_bot", "interactive", `{"type":"card"}`, "1710900060000"), nil
		case "om_root":
			return testFetchedMessage("om_root", "", "user", "ou_user", "text", `{"text":"第一轮问题"}`, "1710900000000"), nil
		default:
			return nil, nil
		}
	}
	agenticChatRunLookupByResponseMessage = func(ctx context.Context, messageID string) (*agenticReplyRunState, error) {
		if messageID != "om_parent" {
			return nil, nil
		}
		return &agenticReplyRunState{RunID: "run_fallback_1", Status: "waiting_approval", Goal: "分析金价波动", InputText: "第一轮问题", ResultSummary: "已经给出第一版分析框架"}, nil
	}
	agenticChatRunStepsLoader = func(ctx context.Context, runID string) ([]*agenticReplyStep, error) {
		if runID != "run_fallback_1" {
			t.Fatalf("runID = %q, want %q", runID, "run_fallback_1")
		}
		return []*agenticReplyStep{{Kind: stepKindReply, OutputJSON: []byte(`{"thought_text":"先按宏观因子拆解","reply_text":"先看美元和实际利率。","response_message_id":"om_parent"}`)}}, nil
	}

	event := testAgenticReplyEvent("oc_chat", "ou_actor", "om_parent", "第三轮继续追问")
	threadID, currentID := "omt_gold_thread_long", "om_current"
	event.Event.Message.ThreadId = &threadID
	event.Event.Message.MessageId = &currentID

	scoped, ok, err := defaultAgenticChatReplyScopeLoader(context.Background(), event)
	if err != nil || !ok {
		t.Fatalf("scope err=%v ok=%v", err, ok)
	}
	lines := scoped.MessageList.ToLines()
	if len(lines) != 2 || !strings.Contains(lines[0], "第一轮问题") || !strings.Contains(lines[1], "先看美元和实际利率。") {
		t.Fatalf("lines = %+v", lines)
	}
}

func testAgenticReplyEvent(chatID, openID, parentID, text string) *larkim.P2MessageReceiveV1 {
	createTime := "1710900120000"
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: strPtr(openID)}},
			Message: &larkim.EventMessage{
				ChatId:      strPtr(chatID),
				ParentId:    strPtr(parentID),
				CreateTime:  &createTime,
				MessageType: strPtr("text"),
				Content:     strPtr(`{"text":"` + text + `"}`),
			},
		},
	}
}

func testFetchedMessage(messageID, parentID, senderType, senderID, msgType, content, createTime string) *larkim.Message {
	msg := &larkim.Message{
		MessageId:  strPtr(messageID),
		MsgType:    strPtr(msgType),
		CreateTime: strPtr(createTime),
		Sender:     &larkim.Sender{Id: strPtr(senderID), SenderType: strPtr(senderType)},
		Body:       &larkim.MessageBody{Content: strPtr(content)},
	}
	if parentID != "" {
		msg.ParentId = strPtr(parentID)
	}
	return msg
}

func strPtr(v string) *string { return &v }
