package handlers

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intentmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestBuildUserLLMUsageScopeUsesConversationAttributionFromContext(t *testing.T) {
	ctx := llmusage.WithBusinessAttribution(context.Background(), llmusage.SceneCommand, llmusage.OperationCommandChat)
	scope := buildUserLLMUsageScope(ctx, "", "", "", "", "chat", llmusage.SourceTypeUser)
	if scope.BusinessScene != llmusage.SceneCommand || scope.BusinessOperation != llmusage.OperationCommandChat {
		t.Fatalf("business attribution = %q/%q", scope.BusinessScene, scope.BusinessOperation)
	}
}

func TestBuildUserLLMUsageScopeKeepsSpecializedRetrievalAttribution(t *testing.T) {
	ctx := llmusage.WithBusinessAttribution(context.Background(), llmusage.SceneCommand, llmusage.OperationCommandChat)
	scope := buildUserLLMUsageScope(ctx, "", "", "", "", "history_search", llmusage.SourceTypeUser)
	if scope.BusinessScene != llmusage.SceneRetrieval || scope.BusinessOperation != llmusage.OperationHistorySearch {
		t.Fatalf("business attribution = %q/%q", scope.BusinessScene, scope.BusinessOperation)
	}
}

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

func TestRecordStandardChatPlanCapturesExactContext(t *testing.T) {
	recorder := conversationeval.NewCaptureRecorder()
	ctx := conversationeval.WithCapture(context.Background(), recorder)
	messageID := "om_anchor"
	createTime := "1785301200123"
	event := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Message: &larkim.EventMessage{MessageId: &messageID, CreateTime: &createTime},
	}}
	historyAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.Local)
	history := newContextItem(
		conversationeval.ContextSourceHistory, "om_history",
		conversationeval.ContextKindMessage, "[09:00] <A>: before", 1, historyAt,
		map[string]string{"message_id": "om_history"},
	)
	retrieved := newContextItem(
		conversationeval.ContextSourceRetrieved, "chunk_1",
		conversationeval.ContextKindChunk, "[08:59]topic", 1, historyAt,
		map[string]string{"message_id": "om_topic"},
	)
	excluded := excludedContextItem(newContextItem(
		conversationeval.ContextSourceHistory, "om_old",
		conversationeval.ContextKindMessage, "[08:00] <B>: old", 0, historyAt,
		nil,
	), excludeReasonHistoryLimit, conversationeval.ContextBucketMessages)
	plan := StandardChatPlan{
		SystemPrompt:   "system exact",
		UserPrompt:     "user exact",
		CurrentInput:   "current exact",
		HistoryItems:   []conversationeval.ContextItem{history},
		RetrievedItems: []conversationeval.ContextItem{retrieved},
		ExcludedItems:  []conversationeval.ExcludedContextItem{excluded},
	}

	recordStandardChatPlan(ctx, event, plan)

	snapshot := recorder.Snapshot()
	if snapshot.Context == nil {
		t.Fatal("captured context is nil")
	}
	if snapshot.Context.AnchorEventID != messageID ||
		!snapshot.Context.AnchorAt.Equal(time.UnixMilli(1785301200123)) {
		t.Fatalf("anchor = %q/%s", snapshot.Context.AnchorEventID, snapshot.Context.AnchorAt)
	}
	if len(snapshot.Context.Messages) != 1 ||
		snapshot.Context.Messages[0].SourceID != "om_history" ||
		snapshot.Context.Messages[0].Content != "[09:00] <A>: before" {
		t.Fatalf("history = %+v", snapshot.Context.Messages)
	}
	if len(snapshot.Context.Retrieved) != 1 ||
		snapshot.Context.Retrieved[0].SourceID != "chunk_1" ||
		snapshot.Context.Retrieved[0].Rank != 1 {
		t.Fatalf("retrieved = %+v", snapshot.Context.Retrieved)
	}
	if snapshot.Context.SystemPrompt != "system exact" || snapshot.Context.UserPrompt != "user exact" {
		t.Fatalf("prompts = %q/%q", snapshot.Context.SystemPrompt, snapshot.Context.UserPrompt)
	}
	if snapshot.Context.CurrentInput != "current exact" {
		t.Fatalf("current input = %q", snapshot.Context.CurrentInput)
	}
	if len(snapshot.ExcludedContext) != 1 ||
		snapshot.ExcludedContext[0].SourceID != "om_old" ||
		snapshot.ExcludedContext[0].ExcludeReason != excludeReasonHistoryLimit ||
		snapshot.ExcludedContext[0].OriginalBucket != conversationeval.ContextBucketMessages {
		t.Fatalf("excluded = %+v", snapshot.ExcludedContext)
	}
}

func TestRecordStandardChatPlanMakesDuplicateSourceIDsValidWithoutChangingContent(t *testing.T) {
	recorder := conversationeval.NewCaptureRecorder()
	ctx := conversationeval.WithCapture(context.Background(), recorder)
	messageID := "om_anchor"
	createTime := "1785301200123"
	event := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Message: &larkim.EventMessage{MessageId: &messageID, CreateTime: &createTime},
	}}
	occurredAt := time.UnixMilli(1785301100000)
	first := newContextItem(
		conversationeval.ContextSourceRetrieved, "chunk_same",
		conversationeval.ContextKindChunk, "first exact", 1, occurredAt, nil,
	)
	second := newContextItem(
		conversationeval.ContextSourceRetrieved, "chunk_same",
		conversationeval.ContextKindChunk, "second exact", 2, occurredAt, nil,
	)

	recordStandardChatPlan(ctx, event, StandardChatPlan{
		SystemPrompt: "system",
		UserPrompt:   "first exact\nsecond exact",
		RetrievedItems: []conversationeval.ContextItem{
			first, second,
		},
	})

	snapshot := recorder.Snapshot().Context
	if snapshot == nil {
		t.Fatal("captured context is nil")
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("captured context Validate() error = %v", err)
	}
	if snapshot.Retrieved[0].SourceID != "chunk_same" ||
		snapshot.Retrieved[1].SourceID != "chunk_same#2" ||
		snapshot.Retrieved[0].Content != "first exact" ||
		snapshot.Retrieved[1].Content != "second exact" {
		t.Fatalf("retrieved identities/content = %+v", snapshot.Retrieved)
	}
}

func TestCaptureControlStreamRecordsCapabilityAndFinalOutput(t *testing.T) {
	recorder := conversationeval.NewCaptureRecorder()
	ctx := conversationeval.WithCapture(context.Background(), recorder)
	startedAt := time.Now().Add(-25 * time.Millisecond)
	stream := iter.Seq[*ark_dal.ModelStreamRespReasoning](func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		yield(&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
			CallID: "call_1", FunctionName: "search_history", Arguments: `{"query":"回调"}`, Pending: true,
		}})
		yield(&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
			CallID: "call_1", FunctionName: "search_history", Output: "matched", Pending: false,
		}})
		yield(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
			Decision: "reply", Reply: "可以这样改", Thought: "需要回复",
			ReferenceFromHistory: "history ref",
		}})
	})

	for range captureControlStream(ctx, stream, startedAt) {
	}

	snapshot := recorder.Snapshot()
	if len(snapshot.ToolPlans) != 2 {
		t.Fatalf("tool plan count = %d, want 2", len(snapshot.ToolPlans))
	}
	var firstTrace conversationeval.ToolTrace
	if err := json.Unmarshal(snapshot.ToolPlans[0], &firstTrace); err != nil {
		t.Fatalf("tool trace JSON error = %v", err)
	}
	if firstTrace.Name != "search_history" || string(firstTrace.Arguments) != `{"query":"回调"}` {
		t.Fatalf("first trace = %+v", firstTrace)
	}
	if snapshot.Output == nil ||
		snapshot.Output.Decision != conversationeval.OutputDecisionReply ||
		snapshot.Output.Reply != "可以这样改" ||
		snapshot.Output.References.History != "history ref" {
		t.Fatalf("output = %+v", snapshot.Output)
	}
	if len(snapshot.Output.CapabilityCalls) != 1 ||
		snapshot.Output.CapabilityCalls[0].Output != "matched" ||
		snapshot.Output.Latency < 20*time.Millisecond {
		t.Fatalf("output trace/latency = %+v", snapshot.Output)
	}
}

func TestCaptureHistoryPromptKeepsReplyMessageIdentity(t *testing.T) {
	root := &history.OpensearchMsgLog{
		CreateTimeV2: "2026-07-29 09:00:00",
		OpenID:       "ou_a",
		UserName:     "A",
		MsgList:      []string{"root"},
		MessageID:    "om_root",
	}
	reply := &history.OpensearchMsgLog{
		CreateTimeV2: "2026-07-29 09:01:00",
		OpenID:       "ou_b",
		UserName:     "B",
		MsgList:      []string{"reply"},
		MessageID:    "om_reply",
		ParentID:     "om_root",
	}
	messageList := history.OpensearchMsgLogList{root, reply}

	selected, excluded := captureHistoryPrompt(messageList, messageList.ToThreadLines(), false)

	var foundReply bool
	for _, item := range selected {
		if item.SourceID == "om_reply" {
			foundReply = true
			if item.Content != "└ "+reply.ToLine() {
				t.Fatalf("reply content = %q", item.Content)
			}
		}
	}
	if !foundReply {
		t.Fatalf("selected items do not contain real reply ID: %+v", selected)
	}
	for _, item := range excluded {
		if item.SourceID == "om_reply" {
			t.Fatalf("reply incorrectly excluded: %+v", item)
		}
	}

	recorder := conversationeval.NewCaptureRecorder()
	ctx := conversationeval.WithCapture(context.Background(), recorder)
	anchorID := "om_anchor"
	anchorTime := "1785380400000"
	recordStandardChatPlan(ctx, &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{Message: &larkim.EventMessage{
			MessageId: &anchorID, CreateTime: &anchorTime,
		}},
	}, StandardChatPlan{
		SystemPrompt:  "system",
		UserPrompt:    strings.Join(messageList.ToThreadLines(), "\n"),
		HistoryItems:  selected,
		ExcludedItems: excluded,
	})
	if err := recorder.Snapshot().Context.Validate(); err != nil {
		t.Fatalf("thread context Validate() error = %v", err)
	}
}

func TestCaptureControlStreamPreservesNoCaptureSequence(t *testing.T) {
	first := &ark_dal.ModelStreamRespReasoning{Content: "a"}
	second := &ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
		Decision: "reply", Reply: "ab",
	}}
	stream := iter.Seq[*ark_dal.ModelStreamRespReasoning](func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		if !yield(first) {
			return
		}
		yield(second)
	})

	var got []*ark_dal.ModelStreamRespReasoning
	for item := range captureControlStream(context.Background(), stream, time.Now()) {
		got = append(got, item)
	}
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("captured sequence changed: %#v", got)
	}
}

func TestRunCaptureBuildSkipsConstructionWithoutCapture(t *testing.T) {
	calls := 0
	runCaptureBuild(context.Background(), func() {
		calls++
	})
	if calls != 0 {
		t.Fatalf("capture builder calls without capture = %d, want 0", calls)
	}

	ctx := conversationeval.WithCapture(context.Background(), conversationeval.NewCaptureRecorder())
	runCaptureBuild(ctx, func() {
		calls++
	})
	if calls != 1 {
		t.Fatalf("capture builder calls with capture = %d, want 1", calls)
	}
}

func TestCaptureControlStreamRecordsSkipBeforeConsumerStops(t *testing.T) {
	recorder := conversationeval.NewCaptureRecorder()
	ctx := conversationeval.WithCapture(context.Background(), recorder)
	stream := iter.Seq[*ark_dal.ModelStreamRespReasoning](func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		yield(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
			Decision: "skip", Thought: "quiet",
		}})
	})

	for range captureControlStream(ctx, stream, time.Now()) {
		break
	}

	output := recorder.Snapshot().Output
	if output == nil || output.Decision != conversationeval.OutputDecisionSkip || output.Thought != "quiet" {
		t.Fatalf("skip output = %+v", output)
	}
}

func TestRecordTextDeliveryCapturesActualMessageIDOnlyForReply(t *testing.T) {
	recorder := conversationeval.NewCaptureRecorder()
	ctx := conversationeval.WithCapture(context.Background(), recorder)
	messageID := "om_delivered"
	response := &larkim.ReplyMessageResp{
		Data: &larkim.ReplyMessageRespData{MessageId: &messageID},
	}

	recordTextDelivery(ctx, "actual reply", response)
	if got := recorder.Snapshot().DeliveryMessageID; got != messageID {
		t.Fatalf("delivery message ID = %q, want %q", got, messageID)
	}

	emptyRecorder := conversationeval.NewCaptureRecorder()
	emptyCtx := conversationeval.WithCapture(context.Background(), emptyRecorder)
	recordTextDelivery(emptyCtx, "", response)
	if got := emptyRecorder.Snapshot().DeliveryMessageID; got != "" {
		t.Fatalf("empty reply recorded delivery = %q", got)
	}
}

func TestGenerateChatSeqTwoPhaseNeedReplyFalseCapturesEmptyPlanAndSkip(t *testing.T) {
	useWorkspaceConfigPath(t)
	recorder := conversationeval.NewCaptureRecorder()
	ctx := conversationeval.WithCapture(context.Background(), recorder)
	chatID := "oc_chat"
	messageID := "om_anchor"
	createTime := "1785301200123"
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId: &chatID, MessageId: &messageID, CreateTime: &createTime,
			},
		},
	}
	meta := &xhandler.BaseMetaData{ChatID: chatID, OpenID: "ou_actor"}
	meta.SetIntentAnalysis(&intentmeta.IntentAnalysis{NeedReply: false})

	stream, err := GenerateChatSeqTwoPhase(ctx, event, meta, "model", nil, nil, "candidate should evaluate this")
	if err != nil {
		t.Fatalf("GenerateChatSeqTwoPhase() error = %v", err)
	}
	for range captureControlStream(ctx, stream, time.Now()) {
		break
	}

	snapshot := recorder.Snapshot()
	if snapshot.Context == nil ||
		snapshot.Context.AnchorEventID != messageID ||
		snapshot.Context.SystemPrompt != "" ||
		snapshot.Context.UserPrompt != "" ||
		snapshot.Context.CurrentInput != "candidate should evaluate this" {
		t.Fatalf("early skip context = %+v", snapshot.Context)
	}
	if snapshot.Output == nil || snapshot.Output.Decision != conversationeval.OutputDecisionSkip {
		t.Fatalf("early skip output = %+v", snapshot.Output)
	}
}

func TestGenerateChatSeqTwoPhaseNoCaptureDoesNotAddCreateTimeValidation(t *testing.T) {
	useWorkspaceConfigPath(t)
	chatID := "oc_chat"
	messageID := "om_anchor"
	tests := []struct {
		name       string
		createTime *string
	}{
		{name: "missing", createTime: nil},
		{name: "invalid", createTime: chatHandlerStrPtr("bad")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := &larkim.P2MessageReceiveV1{
				Event: &larkim.P2MessageReceiveV1Data{
					Message: &larkim.EventMessage{
						ChatId: &chatID, MessageId: &messageID, CreateTime: test.createTime,
					},
				},
			}
			meta := &xhandler.BaseMetaData{ChatID: chatID, OpenID: "ou_actor"}
			meta.SetIntentAnalysis(&intentmeta.IntentAnalysis{NeedReply: false})

			if _, err := GenerateChatSeqTwoPhase(
				context.Background(), event, meta, "model", nil, nil,
			); err != nil {
				t.Fatalf("GenerateChatSeqTwoPhase() added error = %v", err)
			}
		})
	}
}

func TestTwoPhaseCaptureRecordsPlannerHintsAndContext(t *testing.T) {
	recorder := conversationeval.NewCaptureRecorder()
	ctx := conversationeval.WithCapture(context.Background(), recorder)
	recordTwoPhaseToolHints(ctx, []intentmeta.ToolHint{
		intentmeta.ToolHintSearchHistory,
		intentmeta.ToolHintFinance,
	})
	messageID := "om_anchor"
	createTime := "1785301200123"
	recordStandardChatPlan(ctx, &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{Message: &larkim.EventMessage{
			MessageId: &messageID, CreateTime: &createTime,
		}},
	}, StandardChatPlan{
		SystemPrompt:    "two-phase system",
		UserPrompt:      "two-phase user",
		DegradedSources: []string{conversationeval.ContextSourceRetrieved},
	})

	snapshot := recorder.Snapshot()
	if len(snapshot.ToolPlans) != 2 {
		t.Fatalf("planner hint count = %d, want 2", len(snapshot.ToolPlans))
	}
	var hint conversationeval.ToolTrace
	if err := json.Unmarshal(snapshot.ToolPlans[0], &hint); err != nil {
		t.Fatalf("planner hint JSON error = %v", err)
	}
	if hint.Name != string(intentmeta.ToolHintSearchHistory) ||
		hint.OutputSource != conversationeval.ToolOutputSourcePlanner {
		t.Fatalf("planner hint = %+v", hint)
	}
	if snapshot.Context == nil ||
		snapshot.Context.SystemPrompt != "two-phase system" ||
		snapshot.Context.UserPrompt != "two-phase user" ||
		len(snapshot.Context.DegradedSources) != 1 {
		t.Fatalf("two-phase context = %+v", snapshot.Context)
	}
}

type fakeStandardChatExecutor struct {
	called bool
}

func (f *fakeStandardChatExecutor) Do(
	context.Context,
	llmusage.Scope,
	string,
	string,
	...string,
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	f.called = true
	return func(func(*ark_dal.ModelStreamRespReasoning) bool) {}, nil
}

func TestExecuteStandardChatPlanOnlyOverridesModelWithCapture(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		wantModelID string
	}{
		{name: "legacy", ctx: context.Background(), wantModelID: ""},
		{
			name: "capture",
			ctx: conversationeval.WithCapture(
				context.Background(),
				conversationeval.NewCaptureRecorder(),
			),
			wantModelID: "plan-model",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &fakeStandardChatExecutor{}
			var capturedModelID string
			factory := func(modelID string) standardChatExecutor {
				capturedModelID = modelID
				return executor
			}
			meta := &xhandler.BaseMetaData{}
			plan := StandardChatPlan{
				ModelID: "plan-model",
				chatID:  "oc_chat",
				openID:  "ou_actor",
			}

			if _, err := executeStandardChatPlanWithExecutorFactory(
				test.ctx,
				nil,
				meta,
				plan,
				factory,
			); err != nil {
				t.Fatalf("executeStandardChatPlanWithExecutorFactory() error = %v", err)
			}
			if capturedModelID != test.wantModelID {
				t.Fatalf("factory model ID = %q, want %q", capturedModelID, test.wantModelID)
			}
			if !executor.called {
				t.Fatal("executor Do() was not called")
			}
		})
	}
}
