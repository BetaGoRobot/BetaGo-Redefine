package ark_dal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

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

	var captured []*responses.ResponsesRequest
	oldCreateResponsesFn := createResponsesFn
	createResponsesFn = func(ctx context.Context, req *responses.ResponsesRequest) (*responses.ResponseObject, error) {
		captured = append(captured, req)
		switch len(captured) {
		case 1:
			return &responses.ResponseObject{Id: "resp_seed"}, nil
		case 2, 3:
			return responseTextFixture(`{"intent_type":"question","need_reply":true,"reply_confidence":88,"reason":"ask","suggest_action":"chat","interaction_mode":"standard"}`), nil
		default:
			t.Fatalf("unexpected createResponses call count %d", len(captured))
			return nil, nil
		}
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
	}

	if _, err := ResponseTextWithCache(context.Background(), req); err != nil {
		t.Fatalf("first ResponseTextWithCache() error = %v", err)
	}
	if _, err := ResponseTextWithCache(context.Background(), req); err != nil {
		t.Fatalf("second ResponseTextWithCache() error = %v", err)
	}

	if len(captured) != 3 {
		t.Fatalf("CreateResponses call count = %d, want 3", len(captured))
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
	if second.GetReasoning() == nil || second.GetReasoning().GetEffort() != responses.ReasoningEffort_low {
		t.Fatalf("response request Reasoning = %+v, want low", second.GetReasoning())
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
	if !strings.Contains(keys[0], ":ark:response:cache:intent:") {
		t.Fatalf("cache key = %q, want intent cache namespace", keys[0])
	}
}

func assertSingleInputMessage(t *testing.T, req *responses.ResponsesRequest, role responses.MessageRole_Enum, text string) {
	t.Helper()

	items := req.GetInput().GetListValue().GetListValue()
	if len(items) != 1 {
		t.Fatalf("input item count = %d, want 1", len(items))
	}
	msg := items[0].GetInputMessage()
	if msg == nil {
		t.Fatal("input message is nil")
	}
	if msg.GetRole() != role {
		t.Fatalf("input message role = %v, want %v", msg.GetRole(), role)
	}
	if len(msg.GetContent()) != 1 {
		t.Fatalf("content item count = %d, want 1", len(msg.GetContent()))
	}
	if got := msg.GetContent()[0].GetText().GetText(); got != text {
		t.Fatalf("input text = %q, want %q", got, text)
	}
}

func responseTextFixture(text string) *responses.ResponseObject {
	return &responses.ResponseObject{
		Id: "resp_output",
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
