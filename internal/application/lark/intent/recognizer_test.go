package intent

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestIntentSystemPromptKeepsStandardOnlyInteractionMode(t *testing.T) {
	requiredPhrases := []string{
		"interaction_mode 固定为 standard",
		"reply_mode 用于判断这条消息属于哪种回复模式",
		"user_willingness 表示用户此刻主观上有多希望机器人接话",
		"interrupt_risk 表示如果机器人现在插话，打扰感有多强",
		`"interaction_mode": "standard"`,
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(intentSystemPrompt, phrase) {
			t.Fatalf("intentSystemPrompt missing phrase %q", phrase)
		}
	}
	for _, phrase := range []string{"needs_history", "needs_web"} {
		if strings.Contains(intentSystemPrompt, phrase) {
			t.Fatalf("intentSystemPrompt should not contain %q", phrase)
		}
	}
}

func TestAnalyzeMessageKeepsMinimalForNonQuestionStandard(t *testing.T) {
	oldResponseTextWithCacheFn := responseTextWithCacheFn
	defer func() {
		responseTextWithCacheFn = oldResponseTextWithCacheFn
	}()

	var captured []ark_dal.CachedResponseRequest
	responseTextWithCacheFn = func(ctx context.Context, req ark_dal.CachedResponseRequest) (string, error) {
		captured = append(captured, req)
		return `{"intent_type":"chat","need_reply":true,"reply_confidence":61,"reason":"闲聊","suggest_action":"chat","interaction_mode":"standard"}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "今天真热", nil, "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}

	if len(captured) != 1 {
		t.Fatalf("responseTextWithCacheFn call count = %d, want 1", len(captured))
	}
	assertIntentRequest(t, captured[0], buildIntentUserPrompt("今天真热", nil), "intent-lite", responses.ReasoningEffort_minimal)
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
		return `{"intent_type":"question","need_reply":true,"reply_confidence":93,"reason":"需要更稳妥回答","suggest_action":"chat","interaction_mode":"standard","reasoning_effort":"high"}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "明天要下雨吗", nil, "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}

	if len(captured) != 1 {
		t.Fatalf("responseTextWithCacheFn call count = %d, want 1", len(captured))
	}
	assertIntentRequest(t, captured[0], buildIntentUserPrompt("明天要下雨吗", nil), "intent-lite", responses.ReasoningEffort_minimal)
	if analysis.IntentType != IntentTypeQuestion {
		t.Fatalf("IntentType = %q, want %q", analysis.IntentType, IntentTypeQuestion)
	}
	if !analysis.NeedReply {
		t.Fatal("NeedReply should be true for question intent")
	}
	if analysis.ReplyConfidence != 93 {
		t.Fatalf("ReplyConfidence = %d, want %d", analysis.ReplyConfidence, 93)
	}
	if analysis.InteractionMode != InteractionModeStandard {
		t.Fatalf("InteractionMode = %q, want %q", analysis.InteractionMode, InteractionModeStandard)
	}
	if analysis.ReasoningEffort != responses.ReasoningEffort_high {
		t.Fatalf("ReasoningEffort = %v, want %v", analysis.ReasoningEffort, responses.ReasoningEffort_high)
	}
}

func TestAnalyzeMessageParsesReplyModeMetadata(t *testing.T) {
	oldResponseTextWithCacheFn := responseTextWithCacheFn
	defer func() {
		responseTextWithCacheFn = oldResponseTextWithCacheFn
	}()

	responseTextWithCacheFn = func(ctx context.Context, req ark_dal.CachedResponseRequest) (string, error) {
		return `{"intent_type":"chat","need_reply":true,"reply_confidence":72,"reason":"用户明确要继续追问历史上下文","suggest_action":"chat","interaction_mode":"standard","reply_mode":"passive_reply","user_willingness":88,"interrupt_risk":12}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "把刚才讨论的方案接着说完", []string{
		"[2026-04-02 10:00:01](ou_a) <甲>: 先按旧方案拆接口",
		"[2026-04-02 10:00:05](ou_b) <乙>: 那降级策略也补一下",
	}, "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}
	if analysis.ReplyMode != ReplyModePassiveReply {
		t.Fatalf("ReplyMode = %q, want %q", analysis.ReplyMode, ReplyModePassiveReply)
	}
	if analysis.UserWillingness != 88 {
		t.Fatalf("UserWillingness = %d, want 88", analysis.UserWillingness)
	}
	if analysis.InterruptRisk != 12 {
		t.Fatalf("InterruptRisk = %d, want 12", analysis.InterruptRisk)
	}
}

func TestAnalyzeMessageIncludesRecentLinesInUserPrompt(t *testing.T) {
	oldResponseTextWithCacheFn := responseTextWithCacheFn
	defer func() {
		responseTextWithCacheFn = oldResponseTextWithCacheFn
	}()

	var captured ark_dal.CachedResponseRequest
	responseTextWithCacheFn = func(ctx context.Context, req ark_dal.CachedResponseRequest) (string, error) {
		captured = req
		return `{"intent_type":"chat","need_reply":true,"reply_confidence":72,"reason":"续聊","suggest_action":"chat","interaction_mode":"standard","reply_mode":"passive_reply","user_willingness":88,"interrupt_risk":12}`, nil
	}

	recent := []string{
		"[2026-04-02 10:00:01](ou_a) <甲>: 先按旧方案拆接口",
		"[2026-04-02 10:00:05](ou_b) <乙>: 那降级策略也补一下",
	}
	if _, err := analyzeMessage(context.Background(), "把刚才讨论的方案接着说完", recent, "intent-lite"); err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}

	wantPrompt := buildIntentUserPrompt("把刚才讨论的方案接着说完", recent)
	if captured.UserPrompt != wantPrompt {
		t.Fatalf("user prompt = %q, want %q", captured.UserPrompt, wantPrompt)
	}
}

func TestAnalyzeMessageSanitizesInvalidReasoningEffortForStandardModeDeepAnalysis(t *testing.T) {
	oldResponseTextWithCacheFn := responseTextWithCacheFn
	defer func() {
		responseTextWithCacheFn = oldResponseTextWithCacheFn
	}()

	var captured []ark_dal.CachedResponseRequest
	responseTextWithCacheFn = func(ctx context.Context, req ark_dal.CachedResponseRequest) (string, error) {
		captured = append(captured, req)
		return `{"intent_type":"chat","need_reply":true,"reply_confidence":81,"reason":"普通闲聊","suggest_action":"chat","interaction_mode":"standard","reasoning_effort":"impossible"}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "帮我看看这个", nil, "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}

	if len(captured) != 1 {
		t.Fatalf("responseTextWithCacheFn call count = %d, want 1", len(captured))
	}
	assertIntentRequest(t, captured[0], buildIntentUserPrompt("帮我看看这个", nil), "intent-lite", responses.ReasoningEffort_minimal)
	if analysis.InteractionMode != InteractionModeStandard {
		t.Fatalf("InteractionMode = %q, want %q", analysis.InteractionMode, InteractionModeStandard)
	}
	if analysis.ReasoningEffort != responses.ReasoningEffort_minimal {
		t.Fatalf("ReasoningEffort = %v, want %v", analysis.ReasoningEffort, responses.ReasoningEffort_minimal)
	}
}

func TestAnalyzeMessageSanitizesInvalidReplyMetadata(t *testing.T) {
	oldResponseTextWithCacheFn := responseTextWithCacheFn
	defer func() {
		responseTextWithCacheFn = oldResponseTextWithCacheFn
	}()

	responseTextWithCacheFn = func(ctx context.Context, req ark_dal.CachedResponseRequest) (string, error) {
		return `{"intent_type":"share","need_reply":true,"reply_confidence":130,"reason":"也许可以插一句","suggest_action":"chat","interaction_mode":"standard","reply_mode":"surprise","user_willingness":180,"interrupt_risk":-3}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "我刚看到一条新闻", nil, "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}
	if analysis.ReplyConfidence != 100 {
		t.Fatalf("ReplyConfidence = %d, want 100", analysis.ReplyConfidence)
	}
	if analysis.ReplyMode != ReplyModePassiveReply {
		t.Fatalf("ReplyMode = %q, want %q", analysis.ReplyMode, ReplyModePassiveReply)
	}
	if analysis.UserWillingness != 100 {
		t.Fatalf("UserWillingness = %d, want 100", analysis.UserWillingness)
	}
	if analysis.InterruptRisk != 0 {
		t.Fatalf("InterruptRisk = %d, want 0", analysis.InterruptRisk)
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
		return `{"intent_type":"chat","need_reply":true,"reply_confidence":90,"reason":"需要更细致分析","suggest_action":"chat","interaction_mode":"standard","reasoning_effort":"impossible"}`, nil
	}

	analysis, err := analyzeMessage(context.Background(), "请深入分析金价波动原因", nil, "intent-lite")
	if err != nil {
		t.Fatalf("analyzeMessage() error = %v", err)
	}

	if len(captured) != 1 {
		t.Fatalf("responseTextWithCacheFn call count = %d, want 1", len(captured))
	}
	assertIntentRequest(t, captured[0], buildIntentUserPrompt("请深入分析金价波动原因", nil), "intent-lite", responses.ReasoningEffort_minimal)
	if analysis.InteractionMode != InteractionModeStandard {
		t.Fatalf("InteractionMode = %q, want %q", analysis.InteractionMode, InteractionModeStandard)
	}
	if analysis.ReplyConfidence != 90 {
		t.Fatalf("ReplyConfidence = %d, want %d", analysis.ReplyConfidence, 90)
	}
	if analysis.ReasoningEffort != responses.ReasoningEffort_minimal {
		t.Fatalf("ReasoningEffort = %v, want %v", analysis.ReasoningEffort, responses.ReasoningEffort_minimal)
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
