package chatflow

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
)

func TestResolveResearchToolTurnLimitRaisesBudgetForResearchRequests(t *testing.T) {
	got := ResolveResearchToolTurnLimit("请帮我做一个深度调研，对比方案并给出处和链接")
	if got <= DefaultAgenticInitialChatToolTurns {
		t.Fatalf("tool turn limit = %d, want > %d for research request", got, DefaultAgenticInitialChatToolTurns)
	}
}

func TestResolveResearchToolTurnLimitKeepsDefaultForRegularRequests(t *testing.T) {
	got := ResolveResearchToolTurnLimit("今天天气怎么样")
	if got != DefaultAgenticInitialChatToolTurns {
		t.Fatalf("tool turn limit = %d, want %d for regular request", got, DefaultAgenticInitialChatToolTurns)
	}
}

func TestBuildAgenticChatExecutionPlanExtendsToolBudgetForResearchRequests(t *testing.T) {
	originalHistoryLoader := agenticChatHistoryLoader
	originalReplyScopeLoader := agenticChatReplyScopeLoader
	originalRecallDocs := agenticChatRecallDocs
	originalChunkIndexResolver := agenticChatChunkIndexResolver
	defer func() {
		agenticChatHistoryLoader = originalHistoryLoader
		agenticChatReplyScopeLoader = originalReplyScopeLoader
		agenticChatRecallDocs = originalRecallDocs
		agenticChatChunkIndexResolver = originalChunkIndexResolver
	}()

	agenticChatHistoryLoader = func(ctx context.Context, chatID string, size int) (history.OpensearchMsgLogList, error) {
		return nil, nil
	}
	agenticChatReplyScopeLoader = func(ctx context.Context, event *larkim.P2MessageReceiveV1) (agenticReplyScopeContext, bool, error) {
		return agenticReplyScopeContext{}, false, nil
	}
	agenticChatRecallDocs = func(ctx context.Context, chatID, query string, topK int) ([]schema.Document, error) { return nil, nil }
	agenticChatChunkIndexResolver = func(ctx context.Context, chatID, openID string) string { return "" }

	plan, err := BuildAgenticChatExecutionPlan(context.Background(), InitialChatGenerationRequest{
		Event:   testAgenticReplyEvent("oc_chat", "ou_actor", "", "请做深度调研"),
		ModelID: "ep-test-agentic",
		Input:   []string{"请深度调研 durable agent runtime 的实现差异，并给出处"},
		Tools:   arktools.New[larkim.P2MessageReceiveV1](),
	})
	if err != nil {
		t.Fatalf("BuildAgenticChatExecutionPlan() error = %v", err)
	}
	if plan.MaxToolTurns <= DefaultAgenticInitialChatToolTurns {
		t.Fatalf("max tool turns = %d, want > %d", plan.MaxToolTurns, DefaultAgenticInitialChatToolTurns)
	}
}
