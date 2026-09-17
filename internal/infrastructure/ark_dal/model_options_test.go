package ark_dal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/pelletier/go-toml/v2"
	"github.com/redis/go-redis/v9"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestModelOptionsResolverUsesRequestScopeAndPreservesDefaults(t *testing.T) {
	oldResolver := modelOptionsResolver
	t.Cleanup(func() { modelOptionsResolver = oldResolver })
	scope := llmusage.Scope{ChatID: "chat", OpenID: "user"}
	modelOptionsResolver = func(_ context.Context, got llmusage.Scope) (config.ModelOptionsMap, error) {
		if got.ChatID != scope.ChatID || got.OpenID != scope.OpenID {
			t.Fatalf("wrong resolver scope: %+v", got)
		}
		return config.ModelOptionsMap{}, nil
	}
	original := &responses.ResponsesRequest{
		Model: "target", ServiceTier: responses.ResponsesServiceTier_fast.Enum(),
		Reasoning: &responses.ResponsesReasoning{Effort: responses.ReasoningEffort_low},
	}
	cfg := &config.ArkConfig{ReasoningEffortModels: []string{"target"}, ModelOptions: config.ModelOptionsMap{"target": {ServiceTier: "flex"}}}
	prepared, err := prepareConfiguredResponsesRequest(context.Background(), cfg, original, scope)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ServiceTier != original.ServiceTier || prepared.Reasoning != original.Reasoning {
		t.Fatal("empty dynamic map should preserve caller defaults and override the TOML map")
	}
	modelOptionsResolver = func(context.Context, llmusage.Scope) (config.ModelOptionsMap, error) {
		return nil, errors.New("invalid config")
	}
	if _, err := prepareConfiguredResponsesRequest(context.Background(), cfg, original, scope); err == nil {
		t.Fatal("resolver error was ignored")
	}
}

func TestModelOptionsReasoningSeparatesPrefixCaches(t *testing.T) {
	loadResponseCacheTestConfig(t)
	mr := miniredis.RunT(t)
	oldRedis, oldResolver, oldRuntime, oldCreate, oldCount := redis_dal.RedisClient, modelOptionsResolver, runtimeClientFn, createResponsesFn, countPrefixTokensFn
	redis_dal.RedisClient = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = redis_dal.RedisClient.Close()
		redis_dal.RedisClient, modelOptionsResolver, runtimeClientFn, createResponsesFn, countPrefixTokensFn = oldRedis, oldResolver, oldRuntime, oldCreate, oldCount
	})
	effort := "high"
	modelOptionsResolver = func(context.Context, llmusage.Scope) (config.ModelOptionsMap, error) {
		return config.ModelOptionsMap{"target": {ReasoningEffort: effort}}, nil
	}
	runtimeClientFn = func() (*arkruntime.Client, *config.ArkConfig, error) { return nil, &config.ArkConfig{}, nil }
	countPrefixTokensFn = func(context.Context, *arkruntime.Client, string, string) (int, error) { return 257, nil }
	seeds := 0
	createResponsesFn = func(_ context.Context, body *responses.ResponsesRequest, _ llmusage.Scope) (*responses.ResponseObject, error) {
		if body.GetReasoning().GetEffort().String() != effort {
			t.Fatalf("wrong cached request reasoning: %v", body.Reasoning)
		}
		if body.GetCaching().GetPrefix() {
			seeds++
			return &responses.ResponseObject{Id: fmt.Sprintf("seed-%d", seeds)}, nil
		}
		return responseTextFixture("ok"), nil
	}
	req := CachedResponseRequest{ModelID: "target", SystemPrompt: "system", UserPrompt: "user"}
	for _, value := range []string{"high", "low", "low"} {
		effort = value
		if _, err := ResponseTextWithCache(context.Background(), req, llmusage.Scope{}); err != nil {
			t.Fatal(err)
		}
	}
	if seeds != 2 || len(mr.Keys()) != 2 {
		t.Fatalf("want two separate prefix caches, seeds=%d keys=%v", seeds, mr.Keys())
	}
	for _, key := range mr.Keys() {
		if !strings.Contains(key, "reasoning_high") && !strings.Contains(key, "reasoning_low") {
			t.Fatalf("cache missing configured effort: %s", key)
		}
	}
}

func TestModelOptionsResponsesWire(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, modelID := range []string{"target", "target-other"} {
			t.Run(fmt.Sprintf("stream=%v/model=%s", stream, modelID), func(t *testing.T) {
				var cfg config.ArkConfig
				if err := toml.Unmarshal([]byte("[model_options.target]\nservice_tier = 'flex'\nreasoning_effort = 'high'\n"), &cfg); err != nil {
					t.Fatal(err)
				}
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if modelID == "target" {
						if body["service_tier"] != "flex" {
							t.Errorf("service_tier = %v, want flex", body["service_tier"])
						}
						reasoning, _ := body["reasoning"].(map[string]any)
						if reasoning["effort"] != "high" {
							t.Errorf("reasoning = %v, want high", body["reasoning"])
						}
					} else if body["service_tier"] != nil || body["reasoning"] != nil {
						t.Errorf("non-target model received overrides: %v", body)
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-test\",\"service_tier\":\"flex\"}}\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"id":"resp-test","service_tier":"flex"}`)
					}
				})
				oldClient, oldConfig := client, arkConfig
				client = arkruntime.NewClientWithApiKey("test", arkruntime.WithBaseUrl("https://ark.test"), arkruntime.WithRetryTimes(0), arkruntime.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, r)
					return recorder.Result(), nil
				})}))
				arkConfig = &cfg
				t.Cleanup(func() { client, arkConfig = oldClient, oldConfig })
				body := &responses.ResponsesRequest{Model: modelID}
				if stream {
					reader, err := CreateResponsesStream(context.Background(), body, llmusage.Scope{})
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
					if _, err := reader.Recv(); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := CreateResponses(context.Background(), body, llmusage.Scope{}); err != nil {
						t.Fatal(err)
					}
				}
				if body.ServiceTier != nil || body.Reasoning != nil || body.Stream != nil {
					t.Fatal("caller-owned request was mutated")
				}
			})
		}
	}
}
