package ark_dal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/bytedance/gg/gptr"
	"github.com/redis/go-redis/v9"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var (
	runtimeClientFn   = runtimeClient
	createResponsesFn = CreateResponses
)

type CachedResponseRequest struct {
	CacheScene   string
	SystemPrompt string
	UserPrompt   string
	ModelID      string
	Text         *responses.ResponsesText
	Reasoning    *responses.ResponsesReasoning
	Thinking     *responses.ResponsesThinking
}

func ResponseWithCache(ctx context.Context, sysPrompt, userPrompt, modelID string) (res string, err error) {
	return ResponseTextWithCache(ctx, CachedResponseRequest{
		CacheScene:   "chunking",
		SystemPrompt: sysPrompt,
		UserPrompt:   userPrompt,
		ModelID:      modelID,
	})
}

func ResponseTextWithCache(ctx context.Context, req CachedResponseRequest) (res string, err error) {
	if _, _, err := runtimeClientFn(); err != nil {
		return "", err
	}
	ctx, span := otel.StartNamed(ctx, "ark.responses.cache")
	span.SetAttributes(attribute.String("model.id", req.ModelID))
	span.SetAttributes(attribute.String("cache.scene", cacheScene(req.CacheScene)))
	span.SetAttributes(otel.PreviewAttrs("sys_prompt", req.SystemPrompt, 256)...)
	span.SetAttributes(otel.PreviewAttrs("user_prompt", req.UserPrompt, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	key := botidentity.Current().NamespaceKey(
		"ark",
		"response",
		"cache",
		cacheScene(req.CacheScene),
		req.ModelID,
		hashResponseCacheInput(req.Thinking.String()+req.Reasoning.String()+req.SystemPrompt),
	)
	span.SetAttributes(
		attribute.String("cache.key.preview", otel.PreviewString(key, 128)),
		attribute.Int("cache.key.len", len(key)),
	)

	redisGetCtx, redisGetSpan := otel.StartNamed(ctx, "ark.responses.cache_get")
	respID, err := redis_dal.GetRedisClient().Get(redisGetCtx, key).Result()
	otel.RecordError(redisGetSpan, err)
	redisGetSpan.End()
	if err != nil && err != redis.Nil {
		logs.L().Ctx(ctx).Error("get cache error", zap.Error(err))
		return "", err
	}
	if respID == "" {
		span.AddEvent(
			"cache_miss",
			trace.WithAttributes(
				attribute.String("cache.key.preview", otel.PreviewString(key, 128)),
				attribute.Int("cache.key.len", len(key)),
			),
		)
		exp := time.Now().Add(time.Hour).Unix()
		cacheReq := &responses.ResponsesRequest{
			Model: req.ModelID,
			Input: singleTextInput(responses.MessageRole_system, req.SystemPrompt),
			Store: gptr.Of(true),
			Caching: &responses.ResponsesCaching{
				Type:   responses.CacheType_enabled.Enum(),
				Prefix: gptr.Of(true),
			},
			ExpireAt: gptr.Of(exp),
			Thinking: req.Thinking,
		}
		resp, err := createResponsesFn(ctx, cacheReq)
		if err != nil {
			logs.L().Ctx(ctx).Error("responses error", zap.Error(err))
			return "", err
		}
		redisSetCtx, redisSetSpan := otel.StartNamed(ctx, "ark.responses.cache_set")
		if err := redis_dal.GetRedisClient().Set(redisSetCtx, key, resp.Id, 0).Err(); err != nil && err != redis.Nil {
			otel.RecordError(redisSetSpan, err)
			redisSetSpan.End()
			logs.L().Ctx(ctx).Error("set cache error", zap.Error(err))
			return "", err
		}
		redisSetSpan.End()
		redisExpireCtx, redisExpireSpan := otel.StartNamed(ctx, "ark.responses.cache_expire")
		if err := redis_dal.GetRedisClient().ExpireAt(redisExpireCtx, key, time.Unix(exp, 0)).Err(); err != nil && err != redis.Nil {
			otel.RecordError(redisExpireSpan, err)
			redisExpireSpan.End()
			logs.L().Ctx(ctx).Error("expire cache error", zap.Error(err))
			return "", err
		}
		redisExpireSpan.End()
		respID = resp.Id
	} else {
		span.AddEvent(
			"cache_hit",
			trace.WithAttributes(
				attribute.String("cache.key.preview", otel.PreviewString(key, 128)),
				attribute.Int("cache.key.len", len(key)),
			),
		)
	}

	secondReq := &responses.ResponsesRequest{
		Model:              req.ModelID,
		Input:              singleTextInput(responses.MessageRole_user, req.UserPrompt),
		PreviousResponseId: gptr.Of(respID),
		Text:               req.Text,
		// Reasoning:          req.Reasoning,
		Thinking: req.Thinking,
	}

	resp, err := createResponsesFn(ctx, secondReq)
	if err != nil {
		logs.L().Ctx(ctx).Error("responses error", zap.Error(err))
		return "", err
	}

	for _, output := range resp.GetOutput() {
		if msg := output.GetOutputMessage(); msg != nil {
			if content := msg.GetContent(); len(content) > 0 {
				return content[0].GetText().GetText(), nil
			}
		}
	}
	return "", errors.New("text is nil")
}

func singleTextInput(role responses.MessageRole_Enum, text string) *responses.ResponsesInput {
	return &responses.ResponsesInput{
		Union: &responses.ResponsesInput_ListValue{
			ListValue: &responses.InputItemList{
				ListValue: []*responses.InputItem{
					{
						Union: &responses.InputItem_InputMessage{
							InputMessage: &responses.ItemInputMessage{
								Role: role,
								Content: []*responses.ContentItem{
									{
										Union: &responses.ContentItem_Text{
											Text: &responses.ContentItemText{
												Type: responses.ContentItemType_input_text,
												Text: text,
											},
										},
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

func cacheScene(scene string) string {
	scene = strings.TrimSpace(scene)
	if scene == "" {
		return "default"
	}
	return scene
}

func hashResponseCacheInput(sysPrompt string) string {
	sum := sha256.Sum256([]byte(sysPrompt))
	return hex.EncodeToString(sum[:])
}
