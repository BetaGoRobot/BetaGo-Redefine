package ark_dal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestResponseRequestLogFieldsIncludeModelAndCallContext(t *testing.T) {
	req := &responses.ResponsesRequest{Model: "candidate-current"}
	scope := llmusage.Scope{
		ChatID:            "oc_chat",
		OpenID:            "ou_user",
		SourceType:        llmusage.SourceTypeBackground,
		Source:            "conversation_candidate_activation",
		BusinessScene:     llmusage.SceneEvaluation,
		BusinessOperation: llmusage.OperationCandidateGeneration,
	}

	fields := responseRequestLogFields(
		"cache_head",
		"conversation_candidate_activation",
		req,
		scope,
		errors.New("endpoint not found"),
	)
	got := make(map[string]string, len(fields))
	for _, field := range fields {
		got[field.Key] = field.String
	}

	want := map[string]string{
		"model_id":           "candidate-current",
		"cache_scene":        "conversation_candidate_activation",
		"source_type":        "background",
		"source":             "conversation_candidate_activation",
		"business_scene":     "evaluation",
		"business_operation": "candidate_generation",
		"chat_id":            "oc_chat",
		"open_id":            "ou_user",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("field %q = %q, want %q", key, got[key], value)
		}
	}
}

func TestResponseTextWithCacheDisabledPrefixUsesDirectRequest(t *testing.T) {
	loadResponseCacheTestConfig(t)

	oldRuntimeClientFn := runtimeClientFn
	runtimeClientFn = func() (*arkruntime.Client, *config.ArkConfig, error) {
		return nil, &config.ArkConfig{}, nil
	}
	t.Cleanup(func() { runtimeClientFn = oldRuntimeClientFn })

	tokenizationCalls := 0
	oldCountPrefixTokensFn := countPrefixTokensFn
	countPrefixTokensFn = func(_ context.Context, _ *arkruntime.Client, _, _ string) (int, error) {
		tokenizationCalls++
		return 257, nil
	}
	t.Cleanup(func() { countPrefixTokensFn = oldCountPrefixTokensFn })

	var captured []*responses.ResponsesRequest
	oldCreateResponsesFn := createResponsesFn
	createResponsesFn = func(_ context.Context, req *responses.ResponsesRequest, _ llmusage.Scope) (*responses.ResponseObject, error) {
		captured = append(captured, req)
		return responseTextFixture(`{"ok":true}`), nil
	}
	t.Cleanup(func() { createResponsesFn = oldCreateResponsesFn })

	got, err := ResponseTextWithCache(context.Background(), CachedResponseRequest{
		CacheScene:         "short-system",
		SystemPrompt:       "system prompt",
		UserPrompt:         "user prompt",
		ModelID:            "model",
		DisablePrefixCache: true,
		Text: &responses.ResponsesText{Format: &responses.TextFormat{
			Type: responses.TextType_json_object,
		}},
		Thinking: &responses.ResponsesThinking{Type: responses.ThinkingType_disabled.Enum()},
	}, llmusage.Scope{Source: "short-system"})
	if err != nil {
		t.Fatalf("ResponseTextWithCache() error = %v", err)
	}
	if got != `{"ok":true}` {
		t.Fatalf("ResponseTextWithCache() = %q", got)
	}
	if len(captured) != 1 {
		t.Fatalf("CreateResponses call count = %d, want 1", len(captured))
	}
	if tokenizationCalls != 0 {
		t.Fatalf("CreateTokenization call count = %d, want 0", tokenizationCalls)
	}
	req := captured[0]
	if req.GetPreviousResponseId() != "" || req.GetCaching() != nil || req.GetStore() {
		t.Fatalf("direct request unexpectedly enables cache chaining: %+v", req)
	}
	if req.GetText().GetFormat().GetType() != responses.TextType_json_object {
		t.Fatalf("direct request Text = %+v", req.GetText())
	}
	assertInputMessages(t, req,
		inputMessageExpectation{responses.MessageRole_system, "system prompt"},
		inputMessageExpectation{responses.MessageRole_user, "user prompt"},
	)
}

func TestResponseTextWithCacheUsesDirectRequestWhenPrefixInputIsNotLongEnough(t *testing.T) {
	loadResponseCacheTestConfig(t)
	installResponseCacheTestRedis(t)

	oldRuntimeClientFn := runtimeClientFn
	runtimeClientFn = func() (*arkruntime.Client, *config.ArkConfig, error) {
		return nil, &config.ArkConfig{}, nil
	}
	t.Cleanup(func() { runtimeClientFn = oldRuntimeClientFn })

	tokenizationCalls := 0
	oldCountPrefixTokensFn := countPrefixTokensFn
	countPrefixTokensFn = func(_ context.Context, _ *arkruntime.Client, modelID, input string) (int, error) {
		tokenizationCalls++
		if modelID != "model" || input != "short system prompt" {
			t.Fatalf("tokenization input = (%q, %q), want (%q, %q)", modelID, input, "model", "short system prompt")
		}
		return 256, nil
	}
	t.Cleanup(func() { countPrefixTokensFn = oldCountPrefixTokensFn })

	var captured []*responses.ResponsesRequest
	oldCreateResponsesFn := createResponsesFn
	createResponsesFn = func(_ context.Context, req *responses.ResponsesRequest, _ llmusage.Scope) (*responses.ResponseObject, error) {
		captured = append(captured, req)
		return responseTextFixture(`{"ok":true}`), nil
	}
	t.Cleanup(func() { createResponsesFn = oldCreateResponsesFn })

	got, err := ResponseTextWithCache(context.Background(), CachedResponseRequest{
		CacheScene:   "short-cache-head",
		SystemPrompt: "short system prompt",
		UserPrompt:   "user prompt",
		ModelID:      "model",
		Thinking:     &responses.ResponsesThinking{Type: responses.ThinkingType_disabled.Enum()},
	}, llmusage.Scope{Source: "fallback"})
	if err != nil {
		t.Fatalf("ResponseTextWithCache() error = %v", err)
	}
	if got != `{"ok":true}` {
		t.Fatalf("ResponseTextWithCache() = %q", got)
	}
	if tokenizationCalls != 1 {
		t.Fatalf("CreateTokenization call count = %d, want 1", tokenizationCalls)
	}
	if len(captured) != 1 {
		t.Fatalf("CreateResponses call count = %d, want 1", len(captured))
	}
	if captured[0].GetCaching() != nil || captured[0].GetStore() || captured[0].GetPreviousResponseId() != "" {
		t.Fatalf("direct request unexpectedly uses cache: %+v", captured[0])
	}
	assertInputMessages(t, captured[0],
		inputMessageExpectation{responses.MessageRole_system, "short system prompt"},
		inputMessageExpectation{responses.MessageRole_user, "user prompt"},
	)
}

func TestResponseTextWithCacheUsesPrefixCacheWhenInputIsLongEnough(t *testing.T) {
	loadResponseCacheTestConfig(t)
	installResponseCacheTestRedis(t)

	oldRuntimeClientFn := runtimeClientFn
	runtimeClientFn = func() (*arkruntime.Client, *config.ArkConfig, error) {
		return nil, &config.ArkConfig{}, nil
	}
	t.Cleanup(func() { runtimeClientFn = oldRuntimeClientFn })

	oldCountPrefixTokensFn := countPrefixTokensFn
	countPrefixTokensFn = func(_ context.Context, _ *arkruntime.Client, _, _ string) (int, error) {
		return 257, nil
	}
	t.Cleanup(func() { countPrefixTokensFn = oldCountPrefixTokensFn })

	var captured []*responses.ResponsesRequest
	oldCreateResponsesFn := createResponsesFn
	createResponsesFn = func(_ context.Context, req *responses.ResponsesRequest, _ llmusage.Scope) (*responses.ResponseObject, error) {
		captured = append(captured, req)
		if len(captured) == 1 {
			return &responses.ResponseObject{Id: "resp_seed"}, nil
		}
		return responseTextFixture(`{"ok":true}`), nil
	}
	t.Cleanup(func() { createResponsesFn = oldCreateResponsesFn })

	_, err := ResponseTextWithCache(context.Background(), CachedResponseRequest{
		CacheScene:   "long-cache-head",
		SystemPrompt: "long system prompt",
		UserPrompt:   "user prompt",
		ModelID:      "model",
		Thinking:     &responses.ResponsesThinking{Type: responses.ThinkingType_disabled.Enum()},
	}, llmusage.Scope{Source: "prefix-cache"})
	if err != nil {
		t.Fatalf("ResponseTextWithCache() error = %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("CreateResponses call count = %d, want 2", len(captured))
	}
	if captured[0].GetCaching() == nil || !captured[0].GetCaching().GetPrefix() {
		t.Fatalf("first request should use prefix cache: %+v", captured[0])
	}
	if captured[1].GetPreviousResponseId() != "resp_seed" {
		t.Fatalf("continuation PreviousResponseId = %q, want resp_seed", captured[1].GetPreviousResponseId())
	}
}

func TestResponseTextWithCacheUsesDirectRequestWhenTokenizationFails(t *testing.T) {
	loadResponseCacheTestConfig(t)
	installResponseCacheTestRedis(t)

	oldRuntimeClientFn := runtimeClientFn
	runtimeClientFn = func() (*arkruntime.Client, *config.ArkConfig, error) {
		return nil, &config.ArkConfig{}, nil
	}
	t.Cleanup(func() { runtimeClientFn = oldRuntimeClientFn })

	wantErr := errors.New("tokenization unavailable")
	oldCountPrefixTokensFn := countPrefixTokensFn
	countPrefixTokensFn = func(_ context.Context, _ *arkruntime.Client, _, _ string) (int, error) {
		return 0, wantErr
	}
	t.Cleanup(func() { countPrefixTokensFn = oldCountPrefixTokensFn })

	var captured []*responses.ResponsesRequest
	oldCreateResponsesFn := createResponsesFn
	createResponsesFn = func(_ context.Context, req *responses.ResponsesRequest, _ llmusage.Scope) (*responses.ResponseObject, error) {
		captured = append(captured, req)
		return responseTextFixture(`{"ok":true}`), nil
	}
	t.Cleanup(func() { createResponsesFn = oldCreateResponsesFn })

	_, err := ResponseTextWithCache(context.Background(), CachedResponseRequest{
		CacheScene:   "tokenization-error",
		SystemPrompt: "system prompt",
		UserPrompt:   "user prompt",
		ModelID:      "model",
		Thinking:     &responses.ResponsesThinking{Type: responses.ThinkingType_disabled.Enum()},
	}, llmusage.Scope{Source: "tokenization-error"})
	if err != nil {
		t.Fatalf("ResponseTextWithCache() error = %v", err)
	}
	if len(captured) != 1 || captured[0].GetCaching() != nil {
		t.Fatalf("CreateResponses requests = %+v, want one direct request", captured)
	}
}

func TestResponseTextWithCacheDoesNotFallbackForUnrelatedError(t *testing.T) {
	loadResponseCacheTestConfig(t)
	installResponseCacheTestRedis(t)

	oldRuntimeClientFn := runtimeClientFn
	runtimeClientFn = func() (*arkruntime.Client, *config.ArkConfig, error) {
		return nil, &config.ArkConfig{}, nil
	}
	t.Cleanup(func() { runtimeClientFn = oldRuntimeClientFn })

	oldCountPrefixTokensFn := countPrefixTokensFn
	countPrefixTokensFn = func(_ context.Context, _ *arkruntime.Client, _, _ string) (int, error) {
		return 257, nil
	}
	t.Cleanup(func() { countPrefixTokensFn = oldCountPrefixTokensFn })

	calls := 0
	wantErr := errors.New("endpoint unavailable")
	oldCreateResponsesFn := createResponsesFn
	createResponsesFn = func(_ context.Context, _ *responses.ResponsesRequest, _ llmusage.Scope) (*responses.ResponseObject, error) {
		calls++
		return nil, wantErr
	}
	t.Cleanup(func() { createResponsesFn = oldCreateResponsesFn })

	_, err := ResponseTextWithCache(context.Background(), CachedResponseRequest{
		CacheScene:   "ordinary-error",
		SystemPrompt: "system prompt",
		UserPrompt:   "user prompt",
		ModelID:      "model",
		Thinking:     &responses.ResponsesThinking{Type: responses.ThinkingType_disabled.Enum()},
	}, llmusage.Scope{Source: "no-fallback"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ResponseTextWithCache() error = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Fatalf("CreateResponses call count = %d, want 1", calls)
	}
}

func TestResponseTextWithCacheReusesSeededResponseID(t *testing.T) {
	loadResponseCacheTestConfig(t)

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	defer mr.Close()

	oldRedisClient := redis_dal.RedisClient
	redis_dal.RedisClient = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = redis_dal.RedisClient.Close()
		redis_dal.RedisClient = oldRedisClient
	})

	oldRuntimeClientFn := runtimeClientFn
	runtimeClientFn = func() (*arkruntime.Client, *config.ArkConfig, error) {
		return nil, &config.ArkConfig{}, nil
	}
	t.Cleanup(func() {
		runtimeClientFn = oldRuntimeClientFn
	})

	tokenizationCalls := 0
	oldCountPrefixTokensFn := countPrefixTokensFn
	countPrefixTokensFn = func(_ context.Context, _ *arkruntime.Client, _, _ string) (int, error) {
		tokenizationCalls++
		return 257, nil
	}
	t.Cleanup(func() { countPrefixTokensFn = oldCountPrefixTokensFn })

	var captured []*responses.ResponsesRequest
	var capturedScopes []llmusage.Scope
	oldCreateResponsesFn := createResponsesFn
	createResponsesFn = func(ctx context.Context, req *responses.ResponsesRequest, scope llmusage.Scope) (*responses.ResponseObject, error) {
		captured = append(captured, req)
		capturedScopes = append(capturedScopes, scope)
		var resp *responses.ResponseObject
		switch len(captured) {
		case 1:
			resp = &responses.ResponseObject{
				Id: "resp_seed",
				Usage: &responses.Usage{
					InputTokens:  10,
					OutputTokens: 0,
					TotalTokens:  10,
				},
			}
		case 2, 3:
			resp = responseTextFixture(`{"intent_type":"question","need_reply":true,"reply_confidence":88,"reason":"ask","suggest_action":"chat","interaction_mode":"standard"}`)
		default:
			t.Fatalf("unexpected createResponses call count %d", len(captured))
			return nil, nil
		}
		recordResponseUsage(ctx, scope, bodyModel(req), llmusage.KindResponses, resp, nil)
		return resp, nil
	}
	t.Cleanup(func() {
		createResponsesFn = oldCreateResponsesFn
	})

	req := CachedResponseRequest{
		CacheScene:   "intent",
		SystemPrompt: "system prompt",
		UserPrompt:   "user prompt",
		ModelID:      "intent-lite",
		Text: &responses.ResponsesText{
			Format: &responses.TextFormat{
				Type: responses.TextType_json_object,
			},
		},
		Reasoning: &responses.ResponsesReasoning{
			Effort: responses.ReasoningEffort_low,
		},
		Thinking: &responses.ResponsesThinking{
			Type: responses.ThinkingType_disabled.Enum(),
		},
	}
	scope := llmusage.Scope{
		ChatID:     "oc_chat",
		ChatName:   "Test Chat",
		OpenID:     "ou_user",
		UserName:   "Alice",
		SourceType: llmusage.SourceTypeUser,
		Source:     "intent",
	}
	store := &arkUsageStore{}
	llmusage.SetDefaultRecorder(llmusage.NewRecorderWithStore(store))
	t.Cleanup(func() {
		llmusage.SetDefaultRecorder(nil)
	})

	if _, err := ResponseTextWithCache(context.Background(), req, scope); err != nil {
		t.Fatalf("first ResponseTextWithCache() error = %v", err)
	}
	if _, err := ResponseTextWithCache(context.Background(), req, scope); err != nil {
		t.Fatalf("second ResponseTextWithCache() error = %v", err)
	}

	if len(captured) != 3 {
		t.Fatalf("CreateResponses call count = %d, want 3", len(captured))
	}
	if tokenizationCalls != 1 {
		t.Fatalf("CreateTokenization call count = %d, want 1", tokenizationCalls)
	}
	if len(capturedScopes) != 3 {
		t.Fatalf("captured scope count = %d, want 3", len(capturedScopes))
	}
	for i, got := range capturedScopes {
		if got != scope {
			t.Fatalf("scope[%d] = %+v, want %+v", i, got, scope)
		}
	}
	if len(store.rows) != 3 {
		t.Fatalf("usage record count = %d, want 3", len(store.rows))
	}
	if store.rows[0].ChatID != "oc_chat" || store.rows[0].OpenID != "ou_user" || store.rows[0].Source != "intent" {
		t.Fatalf("usage row scope = %+v", store.rows[0])
	}
	if store.rows[0].PromptTokens != 10 || store.rows[0].TotalTokens != 10 {
		t.Fatalf("usage row tokens = %d/%d", store.rows[0].PromptTokens, store.rows[0].TotalTokens)
	}

	first := captured[0]
	if first.GetPreviousResponseId() != "" {
		t.Fatalf("seed request PreviousResponseId = %q, want empty", first.GetPreviousResponseId())
	}
	if !first.GetStore() {
		t.Fatal("seed request should enable Store")
	}
	if first.GetCaching() == nil || first.GetCaching().GetType() != responses.CacheType_enabled {
		t.Fatalf("seed request Caching = %+v, want enabled", first.GetCaching())
	}
	if first.GetExpireAt() == 0 {
		t.Fatal("seed request should set ExpireAt")
	}
	assertSingleInputMessage(t, first, responses.MessageRole_system, "system prompt")

	second := captured[1]
	if second.GetPreviousResponseId() != "resp_seed" {
		t.Fatalf("response request PreviousResponseId = %q, want %q", second.GetPreviousResponseId(), "resp_seed")
	}
	if second.GetText() == nil || second.GetText().GetFormat() == nil || second.GetText().GetFormat().GetType() != responses.TextType_json_object {
		t.Fatalf("response request Text = %+v, want json_object", second.GetText())
	}
	if second.GetCaching() == nil || second.GetCaching().GetType() != responses.CacheType_enabled {
		t.Fatalf("response request Caching = %+v, want enabled to continue cached head", second.GetCaching())
	}
	if second.GetReasoning() != nil {
		t.Fatalf("response request Reasoning = %+v, want omitted for unlisted model", second.GetReasoning())
	}
	if second.GetThinking() == nil || second.GetThinking().GetType() != responses.ThinkingType_disabled {
		t.Fatalf("response request Thinking = %+v, want disabled to match seed", second.GetThinking())
	}
	assertSingleInputMessage(t, second, responses.MessageRole_user, "user prompt")

	third := captured[2]
	if third.GetPreviousResponseId() != "resp_seed" {
		t.Fatalf("cached response request PreviousResponseId = %q, want %q", third.GetPreviousResponseId(), "resp_seed")
	}
	assertSingleInputMessage(t, third, responses.MessageRole_user, "user prompt")

	keys := mr.Keys()
	if len(keys) != 1 {
		t.Fatalf("cache key count = %d, want 1", len(keys))
	}
	if !strings.Contains(keys[0], ":ark:response:cache:v2:intent:") {
		t.Fatalf("cache key = %q, want versioned intent cache namespace", keys[0])
	}
}

func TestCountPrefixTokensUsesArkTokenizationEndpoint(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/tokenization" {
			t.Errorf("request path = %q, want /tokenization", req.URL.Path)
		}
		var body struct {
			Model string `json:"model"`
			Text  string `json:"text"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode tokenization request: %v", err)
		}
		if body.Model != "model-endpoint" || body.Text != "system prompt" {
			t.Errorf("tokenization request = %+v", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"id":"tok","model":"model-endpoint","data":[{"index":0,"total_tokens":257}]}`,
			)),
		}, nil
	})}

	client := arkruntime.NewClientWithApiKey(
		"test-api-key",
		arkruntime.WithBaseUrl("https://ark.test"),
		arkruntime.WithHTTPClient(httpClient),
		arkruntime.WithRetryTimes(0),
	)
	got, err := countPrefixTokens(context.Background(), client, "model-endpoint", "system prompt")
	if err != nil {
		t.Fatalf("countPrefixTokens() error = %v", err)
	}
	if got != 257 {
		t.Fatalf("countPrefixTokens() = %d, want 257", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func assertSingleInputMessage(t *testing.T, req *responses.ResponsesRequest, role responses.MessageRole_Enum, text string) {
	t.Helper()
	assertInputMessages(t, req, inputMessageExpectation{role: role, text: text})
}

type inputMessageExpectation struct {
	role responses.MessageRole_Enum
	text string
}

func assertInputMessages(t *testing.T, req *responses.ResponsesRequest, want ...inputMessageExpectation) {
	t.Helper()

	items := req.GetInput().GetListValue().GetListValue()
	if len(items) != len(want) {
		t.Fatalf("input item count = %d, want %d", len(items), len(want))
	}
	for index, expectation := range want {
		msg := items[index].GetInputMessage()
		if msg == nil {
			t.Fatalf("input message %d is nil", index)
		}
		if msg.GetRole() != expectation.role {
			t.Fatalf("input message %d role = %v, want %v", index, msg.GetRole(), expectation.role)
		}
		if len(msg.GetContent()) != 1 {
			t.Fatalf("input message %d content count = %d, want 1", index, len(msg.GetContent()))
		}
		if got := msg.GetContent()[0].GetText().GetText(); got != expectation.text {
			t.Fatalf("input message %d text = %q, want %q", index, got, expectation.text)
		}
	}
}

func responseTextFixture(text string) *responses.ResponseObject {
	return &responses.ResponseObject{
		Id: "resp_output",
		Usage: &responses.Usage{
			InputTokens:  12,
			OutputTokens: 5,
			TotalTokens:  17,
		},
		Output: []*responses.OutputItem{
			{
				Union: &responses.OutputItem_OutputMessage{
					OutputMessage: &responses.ItemOutputMessage{
						Role: responses.MessageRole_assistant,
						Content: []*responses.OutputContentItem{
							{
								Union: &responses.OutputContentItem_Text{
									Text: &responses.OutputContentItemText{
										Type: responses.ContentItemType_output_text,
										Text: text,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

type arkUsageStore struct {
	rows []llmusage.UsageRecordRow
}

func (s *arkUsageStore) CreateUsageTurn(_ context.Context, row *llmusage.UsageRecordRow, _ []llmusage.ToolCallRecordRow) error {
	s.rows = append(s.rows, *row)
	return nil
}

func loadResponseCacheTestConfig(t *testing.T) {
	t.Helper()

	restorePath := ".dev/config.toml"
	if envPath := os.Getenv("BETAGO_CONFIG_PATH"); envPath != "" {
		restorePath = envPath
	}
	if _, err := os.Stat(restorePath); err == nil {
		t.Cleanup(func() {
			config.LoadFile(restorePath)
		})
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[base_info]
robot_name = "test-bot"

[lark_config]
app_id = "cli_test"
bot_open_id = "ou_test"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	config.LoadFile(path)
}

func installResponseCacheTestRedis(t *testing.T) {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(mr.Close)

	oldRedisClient := redis_dal.RedisClient
	redis_dal.RedisClient = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = redis_dal.RedisClient.Close()
		redis_dal.RedisClient = oldRedisClient
	})
}
