package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestBuildInitialChatExecutionPlanUsesReplyScopedContext(t *testing.T) {
	originalPromptTemplateLoader := initialChatPromptTemplateLoader
	originalUserNameLoader := initialChatUserNameLoader
	originalHistoryLoader := agenticChatHistoryLoader
	originalReplyScopeLoader := agenticChatReplyScopeLoader
	originalRecallDocs := agenticChatRecallDocs
	originalChunkIndexResolver := agenticChatChunkIndexResolver
	originalTopicLookup := agenticChatTopicSummaryLookup
	defer func() {
		initialChatPromptTemplateLoader = originalPromptTemplateLoader
		initialChatUserNameLoader = originalUserNameLoader
		agenticChatHistoryLoader = originalHistoryLoader
		agenticChatReplyScopeLoader = originalReplyScopeLoader
		agenticChatRecallDocs = originalRecallDocs
		agenticChatChunkIndexResolver = originalChunkIndexResolver
		agenticChatTopicSummaryLookup = originalTopicLookup
	}()

	initialChatPromptTemplateLoader = func(ctx context.Context) (*model.PromptTemplateArg, error) {
		return &model.PromptTemplateArg{
			TemplateStr: `History={{.HistoryRecords}} Context={{.Context}} Topics={{.Topics}}`,
		}, nil
	}
	initialChatUserNameLoader = func(ctx context.Context, chatID, openID string) (string, error) {
		return "Alice", nil
	}
	agenticChatHistoryLoader = func(ctx context.Context, chatID string, size int) (history.OpensearchMsgLogList, error) {
		t.Fatal("broad chat history should not be loaded for reply-scoped standard context")
		return nil, nil
	}
	agenticChatReplyScopeLoader = func(ctx context.Context, event *larkim.P2MessageReceiveV1) (agenticReplyScopeContext, bool, error) {
		return agenticReplyScopeContext{
			MessageList: history.OpensearchMsgLogList{
				{
					CreateTime: "2026-03-20 10:00:00",
					OpenID:     "ou_user",
					UserName:   "Alice",
					MsgList:    []string{"请继续推进 agentic 改造"},
				},
				{
					CreateTime: "2026-03-20 10:01:00",
					OpenID:     "ou_bot",
					UserName:   "Agent",
					MsgList:    []string{"最后一步是首轮 turn durable 化。"},
				},
			},
			ContextLines: []string{"关联运行状态: waiting_approval"},
			RecallQuery:  "请继续推进 agentic 改造 首轮 turn durable 化",
		}, true, nil
	}
	agenticChatRecallDocs = func(ctx context.Context, chatID, query string, topK int) ([]schema.Document, error) {
		if !strings.Contains(query, "首轮 turn durable 化") {
			t.Fatalf("recall query = %q, want contain reply-scoped chain", query)
		}
		return []schema.Document{
			{
				PageContent: "reply scoped standard context",
				Metadata: map[string]any{
					"create_time": "2026-03-20 09:59:00",
					"user_id":     "ou_arch",
					"user_name":   "Bob",
				},
			},
		}, nil
	}
	agenticChatChunkIndexResolver = func(ctx context.Context, chatID, openID string) string {
		return ""
	}
	agenticChatTopicSummaryLookup = func(ctx context.Context, chunkIndex, msgID string) (string, error) {
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
	if strings.Contains(plan.Prompt, "broad chat history") {
		t.Fatalf("prompt = %q, should not contain broad history", plan.Prompt)
	}
	if !strings.Contains(plan.Prompt, "关联运行状态: waiting_approval") {
		t.Fatalf("prompt = %q, want contain scoped runtime context", plan.Prompt)
	}
	if !strings.Contains(plan.Prompt, "reply scoped standard context") {
		t.Fatalf("prompt = %q, want contain recalled scoped context", plan.Prompt)
	}
	if plan.ReasoningEffort != responses.ReasoningEffort_high {
		t.Fatalf("ReasoningEffort = %v, want %v", plan.ReasoningEffort, responses.ReasoningEffort_high)
	}
}
