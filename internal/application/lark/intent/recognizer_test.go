package intent

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestIntentSystemPromptMarksResearchAndAnalysisTasksAsAgentic(t *testing.T) {
	requiredPhrases := []string{
		"判断 interaction_mode 时，重点看下面 3 个维度",
		"是否需要综合多来源信息",
		"是否需要归因、比较多个因素",
		"是否预期会触发工具检索",
		"“金价今天多少”更接近 standard",
		"“综合各方信息资源，帮我分析金价剧烈波动的主要原因”更接近 agentic",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(intentSystemPrompt, phrase) {
			t.Fatalf("intentSystemPrompt missing phrase %q", phrase)
		}
	}
}

func TestAnalyzeMessageKeepsMinimalForNonQuestionNonAgentic(t *testing.T) {
	oldResponseTextWithCacheFn := responseTextWithCacheFn
	defer func() {
		responseTextWithCacheFn = oldResponseTextWithCacheFn
	}()

	var captured []ark_dal.CachedResponseRequest
	responseTextWithCacheFn = func(ctx context.Context, req ark_dal.CachedResponseRequest) (string, error) {
		captured = append(captured, req)
		return `{"intent_type":"chat","need_reply":true,"reply_confidence":61,"reason":"闲聊","suggest_action":"chat","interaction_mode":"standard"}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "今天真热", "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}

	if len(captured) != 1 {
		t.Fatalf("responseTextWithCacheFn call count = %d, want 1", len(captured))
	}
	assertIntentRequest(t, captured[0], "今天真热", "intent-lite", responses.ReasoningEffort_minimal)
	if analysis.IntentType != IntentTypeChat {
		t.Fatalf("IntentType = %q, want %q", analysis.IntentType, IntentTypeChat)
	}
	if analysis.InteractionMode != InteractionModeStandard {
		t.Fatalf("InteractionMode = %q, want %q", analysis.InteractionMode, InteractionModeStandard)
	}
	if analysis.ReasoningEffort != responses.ReasoningEffort_minimal {
		t.Fatalf("ReasoningEffort = %v, want %v", analysis.ReasoningEffort, responses.ReasoningEffort_minimal)
	}
}

func TestAnalyzeMessageUsesSinglePassAndParsesReasoningEffort(t *testing.T) {
	oldResponseTextWithCacheFn := responseTextWithCacheFn
	defer func() {
		responseTextWithCacheFn = oldResponseTextWithCacheFn
	}()

	var captured []ark_dal.CachedResponseRequest
	responseTextWithCacheFn = func(ctx context.Context, req ark_dal.CachedResponseRequest) (string, error) {
		captured = append(captured, req)
		return `{"intent_type":"question","need_reply":true,"reply_confidence":93,"reason":"需要更稳妥回答","suggest_action":"chat","interaction_mode":"agentic","reasoning_effort":"high"}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "明天要下雨吗", "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}

	if len(captured) != 1 {
		t.Fatalf("responseTextWithCacheFn call count = %d, want 1", len(captured))
	}
	assertIntentRequest(t, captured[0], "明天要下雨吗", "intent-lite", responses.ReasoningEffort_minimal)
	if analysis.IntentType != IntentTypeQuestion {
		t.Fatalf("IntentType = %q, want %q", analysis.IntentType, IntentTypeQuestion)
	}
	if !analysis.NeedReply {
		t.Fatal("NeedReply should be true for question intent")
	}
	if analysis.ReplyConfidence != 93 {
		t.Fatalf("ReplyConfidence = %d, want %d", analysis.ReplyConfidence, 93)
	}
	if analysis.InteractionMode != InteractionModeAgentic {
		t.Fatalf("InteractionMode = %q, want %q", analysis.InteractionMode, InteractionModeAgentic)
	}
	if analysis.ReasoningEffort != responses.ReasoningEffort_high {
		t.Fatalf("ReasoningEffort = %v, want %v", analysis.ReasoningEffort, responses.ReasoningEffort_high)
	}
}

func TestAnalyzeMessageSanitizesInvalidReasoningEffortForStandardMode(t *testing.T) {
	oldResponseTextWithCacheFn := responseTextWithCacheFn
	defer func() {
		responseTextWithCacheFn = oldResponseTextWithCacheFn
	}()

	var captured []ark_dal.CachedResponseRequest
	responseTextWithCacheFn = func(ctx context.Context, req ark_dal.CachedResponseRequest) (string, error) {
		captured = append(captured, req)
		return `{"intent_type":"chat","need_reply":true,"reply_confidence":81,"reason":"普通闲聊","suggest_action":"chat","interaction_mode":"standard","reasoning_effort":"impossible"}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "帮我看看这个", "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}

	if len(captured) != 1 {
		t.Fatalf("responseTextWithCacheFn call count = %d, want 1", len(captured))
	}
	assertIntentRequest(t, captured[0], "帮我看看这个", "intent-lite", responses.ReasoningEffort_minimal)
	if analysis.InteractionMode != InteractionModeStandard {
		t.Fatalf("InteractionMode = %q, want %q", analysis.InteractionMode, InteractionModeStandard)
	}
	if analysis.ReasoningEffort != responses.ReasoningEffort_minimal {
		t.Fatalf("ReasoningEffort = %v, want %v", analysis.ReasoningEffort, responses.ReasoningEffort_minimal)
	}
}

func TestAnalyzeMessageSanitizesInvalidReasoningEffortForAgenticMode(t *testing.T) {
	oldResponseTextWithCacheFn := responseTextWithCacheFn
	defer func() {
		responseTextWithCacheFn = oldResponseTextWithCacheFn
	}()

	var captured []ark_dal.CachedResponseRequest
	responseTextWithCacheFn = func(ctx context.Context, req ark_dal.CachedResponseRequest) (string, error) {
		captured = append(captured, req)
		return `{"intent_type":"chat","need_reply":true,"reply_confidence":90,"reason":"需要更细致分析","suggest_action":"chat","interaction_mode":"agentic","reasoning_effort":"impossible"}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "深度Agentic化，分析金价波动原因", "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}

	if len(captured) != 1 {
		t.Fatalf("responseTextWithCacheFn call count = %d, want 1", len(captured))
	}
	assertIntentRequest(t, captured[0], "深度Agentic化，分析金价波动原因", "intent-lite", responses.ReasoningEffort_minimal)
	if analysis.InteractionMode != InteractionModeAgentic {
		t.Fatalf("InteractionMode = %q, want %q", analysis.InteractionMode, InteractionModeAgentic)
	}
	if analysis.ReplyConfidence != 90 {
		t.Fatalf("ReplyConfidence = %d, want %d", analysis.ReplyConfidence, 90)
	}
	if analysis.ReasoningEffort != responses.ReasoningEffort_medium {
		t.Fatalf("ReasoningEffort = %v, want %v", analysis.ReasoningEffort, responses.ReasoningEffort_medium)
	}
}

func assertIntentRequest(t *testing.T, req ark_dal.CachedResponseRequest, userPrompt, modelID string, effort responses.ReasoningEffort_Enum) {
	t.Helper()

	if req.CacheScene != "intent" {
		t.Fatalf("cache scene = %q, want %q", req.CacheScene, "intent")
	}
	if req.SystemPrompt != intentSystemPrompt {
		t.Fatal("system prompt mismatch")
	}
	if req.UserPrompt != userPrompt {
		t.Fatalf("user prompt = %q, want %q", req.UserPrompt, userPrompt)
	}
	if req.ModelID != modelID {
		t.Fatalf("model id = %q, want %q", req.ModelID, modelID)
	}
	if req.Text == nil || req.Text.GetFormat() == nil || req.Text.GetFormat().GetType() != responses.TextType_json_object {
		t.Fatalf("text format = %+v, want json_object", req.Text)
	}
	if req.Reasoning == nil || req.Reasoning.GetEffort() != effort {
		t.Fatalf("reasoning = %+v, want %v", req.Reasoning, effort)
	}
}
