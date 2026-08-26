package schedule

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestDecodeTaskResultDecision(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    TaskResultDecision
		wantErr string
	}{
		{
			name: "send",
			raw:  `{"send":true,"content":"货单已刷新：炫彩蛋，160 万洛克贝，限购 1 个。","reason":"结果时效性强"}`,
			want: TaskResultDecision{
				Send:    true,
				Content: "货单已刷新：炫彩蛋，160 万洛克贝，限购 1 个。",
				Reason:  "结果时效性强",
			},
		},
		{
			name: "silent",
			raw:  `{"send":false,"content":"","reason":"没有有意义的更新"}`,
			want: TaskResultDecision{Reason: "没有有意义的更新"},
		},
		{name: "missing send", raw: `{"content":"","reason":"missing"}`, wantErr: "send is required"},
		{name: "send without content", raw: `{"send":true,"content":"","reason":"bad"}`, wantErr: "send decision requires content"},
		{name: "silent with content", raw: `{"send":false,"content":"unexpected","reason":"bad"}`, wantErr: "silent decision cannot include content"},
		{name: "missing reason", raw: `{"send":false,"content":"","reason":""}`, wantErr: "reason is required"},
		{name: "unknown field", raw: `{"send":false,"content":"","reason":"ok","extra":1}`, wantErr: "decode task result decision"},
		{name: "multiple documents", raw: `{"send":false,"content":"","reason":"ok"}{}`, wantErr: "one JSON document"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeTaskResultDecision(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("decodeTaskResultDecision() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeTaskResultDecision() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("decodeTaskResultDecision() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDecodeTaskResultDecisionLimits(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name: "content at rune limit",
			raw: `{"send":true,"content":"` + strings.Repeat("a", scheduleResultReviewMaxContentRunes) +
				`","reason":"ok"}`,
		},
		{
			name: "content over rune limit",
			raw: `{"send":true,"content":"` + strings.Repeat("a", scheduleResultReviewMaxContentRunes+1) +
				`","reason":"ok"}`,
			wantErr: "content is too long",
		},
		{
			name: "multibyte reason at rune limit",
			raw:  `{"send":false,"content":"","reason":"` + strings.Repeat("因", scheduleResultReviewMaxReasonRunes) + `"}`,
		},
		{
			name:    "multibyte reason over rune limit",
			raw:     `{"send":false,"content":"","reason":"` + strings.Repeat("因", scheduleResultReviewMaxReasonRunes+1) + `"}`,
			wantErr: "reason is too long",
		},
		{
			name:    "decision over byte limit",
			raw:     strings.Repeat(" ", scheduleResultReviewMaxDecisionBytes+1),
			wantErr: "invalid size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeTaskResultDecision(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("decodeTaskResultDecision() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("decodeTaskResultDecision() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestModelTaskResultReviewerBuildsRestrictedBackgroundRequest(t *testing.T) {
	task := &model.ScheduledTask{
		ID:        "task-1",
		Name:      "洛克王国远行商人定时货单播报",
		ChatID:    "oc_chat",
		CreatorID: "ou_creator",
		ToolName:  "research_read_url",
		ToolArgs:  `{"url":"https://example.com/merchant"}`,
		Timezone:  "Asia/Shanghai",
	}
	finishedAt := time.Date(2026, 8, 26, 0, 15, 10, 0, time.UTC)

	var capturedRequest ark_dal.CachedResponseRequest
	var capturedScope llmusage.Scope
	reviewer := &modelTaskResultReviewer{
		modelID: func(_ context.Context, chatID, openID string) string {
			if chatID != task.ChatID || openID != task.CreatorID {
				t.Fatalf("model resolver scope = %q/%q, want %q/%q", chatID, openID, task.ChatID, task.CreatorID)
			}
			return "normal-model"
		},
		responseText: func(_ context.Context, req ark_dal.CachedResponseRequest, scope llmusage.Scope) (string, error) {
			capturedRequest = req
			capturedScope = scope
			return `{"send":true,"content":"炫彩蛋已刷新，售价 160 万，限购 1 个。","reason":"适合主动播报"}`, nil
		},
	}

	decision, err := reviewer.Review(context.Background(), task, `{"status":"open","items":[{"name":"炫彩蛋"}]}`, finishedAt)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !decision.Send || decision.Content == "" {
		t.Fatalf("Review() decision = %+v, want send content", decision)
	}
	if capturedRequest.CacheScene != "schedule_result_review" || capturedRequest.ModelID != "normal-model" {
		t.Fatalf("request cache/model = %q/%q", capturedRequest.CacheScene, capturedRequest.ModelID)
	}
	if !capturedRequest.RedactUserPromptPreview {
		t.Fatal("schedule result review must redact the user prompt from telemetry previews")
	}
	if capturedRequest.Text == nil || capturedRequest.Text.Format == nil || capturedRequest.Text.Format.Type != responses.TextType_json_object {
		t.Fatalf("request text format = %+v, want json_object", capturedRequest.Text)
	}
	if capturedRequest.Reasoning == nil || capturedRequest.Reasoning.Effort != responses.ReasoningEffort_minimal {
		t.Fatalf("request reasoning = %+v, want minimal", capturedRequest.Reasoning)
	}
	if capturedRequest.Thinking == nil || capturedRequest.Thinking.Type == nil || *capturedRequest.Thinking.Type != responses.ThinkingType_disabled {
		t.Fatalf("request thinking = %+v, want disabled", capturedRequest.Thinking)
	}
	for _, required := range []string{task.Name, task.ToolName, task.ToolArgs, "炫彩蛋", `"result_truncated":false`} {
		if !strings.Contains(capturedRequest.UserPrompt, required) {
			t.Fatalf("user prompt missing %q: %s", required, capturedRequest.UserPrompt)
		}
	}
	if !strings.Contains(capturedRequest.SystemPrompt, "不可信数据") || !strings.Contains(capturedRequest.SystemPrompt, "不得调用工具") {
		t.Fatalf("system prompt lacks safety boundary: %s", capturedRequest.SystemPrompt)
	}
	if capturedScope.ChatID != task.ChatID || capturedScope.OpenID != task.CreatorID ||
		capturedScope.SourceType != llmusage.SourceTypeBackground ||
		capturedScope.Source != "schedule_result_review" ||
		capturedScope.BusinessScene != llmusage.SceneBackground ||
		capturedScope.BusinessOperation != llmusage.OperationJudge {
		t.Fatalf("usage scope = %+v", capturedScope)
	}
}

func TestModelTaskResultReviewerRejectsMissingModel(t *testing.T) {
	reviewer := &modelTaskResultReviewer{
		modelID: func(context.Context, string, string) string { return "" },
		responseText: func(context.Context, ark_dal.CachedResponseRequest, llmusage.Scope) (string, error) {
			return "", errors.New("must not be called")
		},
	}

	_, err := reviewer.Review(context.Background(), &model.ScheduledTask{ChatID: "oc", CreatorID: "ou"}, "result", time.Now())
	if err == nil || !strings.Contains(err.Error(), "model is not configured") {
		t.Fatalf("Review() error = %v, want missing model", err)
	}
}

func TestModelTaskResultReviewerRedactsSensitiveToolArgs(t *testing.T) {
	task := &model.ScheduledTask{
		ID:        "task-1",
		ChatID:    "oc",
		CreatorID: "ou",
		ToolArgs: `{
			"url":"https://example.com/merchant?access_token=url-secret&round=1",
			"headers":{"Authorization":"Bearer auth-secret","X-API-Key":"header-secret"},
			"nested":[{"password":"password-secret"}],
			"query":"safe-value"
		}`,
	}
	var prompt string
	reviewer := &modelTaskResultReviewer{
		modelID: func(context.Context, string, string) string { return "normal-model" },
		responseText: func(_ context.Context, req ark_dal.CachedResponseRequest, _ llmusage.Scope) (string, error) {
			prompt = req.UserPrompt
			return `{"send":false,"content":"","reason":"无需发送"}`, nil
		},
	}

	if _, err := reviewer.Review(context.Background(), task, "result", time.Now()); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	for _, secret := range []string{"url-secret", "auth-secret", "header-secret", "password-secret"} {
		if strings.Contains(prompt, secret) {
			t.Fatalf("user prompt leaked %q: %s", secret, prompt)
		}
	}
	for _, required := range []string{"[REDACTED]", "safe-value", "round=1"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("redacted user prompt missing %q: %s", required, prompt)
		}
	}
}

func TestModelTaskResultReviewerDoesNotForwardMalformedToolArgs(t *testing.T) {
	task := &model.ScheduledTask{
		ID:        "task-1",
		ChatID:    "oc",
		CreatorID: "ou",
		ToolArgs:  `{"token":"malformed-secret"`,
	}
	var prompt string
	reviewer := &modelTaskResultReviewer{
		modelID: func(context.Context, string, string) string { return "normal-model" },
		responseText: func(_ context.Context, req ark_dal.CachedResponseRequest, _ llmusage.Scope) (string, error) {
			prompt = req.UserPrompt
			return `{"send":false,"content":"","reason":"无需发送"}`, nil
		},
	}

	if _, err := reviewer.Review(context.Background(), task, "result", time.Now()); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if strings.Contains(prompt, "malformed-secret") {
		t.Fatalf("user prompt forwarded malformed tool args: %s", prompt)
	}
	if !strings.Contains(prompt, "[INVALID_JSON]") {
		t.Fatalf("user prompt did not mark invalid tool args: %s", prompt)
	}
}

func TestModelTaskResultReviewerMarksTruncatedResult(t *testing.T) {
	var prompt string
	reviewer := &modelTaskResultReviewer{
		modelID: func(context.Context, string, string) string { return "normal-model" },
		responseText: func(_ context.Context, req ark_dal.CachedResponseRequest, _ llmusage.Scope) (string, error) {
			prompt = req.UserPrompt
			return `{"send":false,"content":"","reason":"信息不足"}`, nil
		},
	}

	_, err := reviewer.Review(
		context.Background(),
		&model.ScheduledTask{ID: "task-1", ChatID: "oc", CreatorID: "ou"},
		strings.Repeat("洛", scheduleResultReviewMaxInputBytes),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !strings.Contains(prompt, `"result_truncated":true`) {
		t.Fatalf("prompt did not mark truncation: %s", prompt)
	}
}
