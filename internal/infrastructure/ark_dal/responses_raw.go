package ark_dal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

func ResponseWithCache(ctx context.Context, sysPrompt, userPrompt, modelID string) (res string, err error) {
	if _, _, err := runtimeClient(); err != nil {
		return "", err
	}
	ctx, span := otel.StartNamed(ctx, "ark.responses.cache")
	span.SetAttributes(attribute.String("model.id", modelID))
	span.SetAttributes(otel.PreviewAttrs("sys_prompt", sysPrompt, 256)...)
	span.SetAttributes(otel.PreviewAttrs("user_prompt", userPrompt, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	key := botidentity.Current().NamespaceKey("ark", "response", "cache", "chunking", modelID, hashChunkingCacheInput(sysPrompt, userPrompt))
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
		return
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
		req := &responses.ResponsesRequest{
			Model: modelID,
			Input: &responses.ResponsesInput{
				Union: &responses.ResponsesInput_ListValue{
					ListValue: &responses.InputItemList{
						ListValue: []*responses.InputItem{
							{
								Union: &responses.InputItem_InputMessage{InputMessage: &responses.ItemInputMessage{
									Role: responses.MessageRole_system,
									Content: []*responses.ContentItem{
										{
											Union: &responses.ContentItem_Text{Text: &responses.ContentItemText{Type: responses.ContentItemType_input_text, Text: sysPrompt}},
										},
									},
								}},
							},
						},
					},
				},
			},
			Store: gptr.Of(true),
			Caching: &responses.ResponsesCaching{
				Type: responses.CacheType_enabled.Enum(),
			},
			ExpireAt: gptr.Of(exp),
		}
		// 先创建cache
		resp, err := CreateResponses(ctx, req)
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

	previousResponseID := respID
	secondReq := &responses.ResponsesRequest{
		Model: modelID,
		Input: &responses.ResponsesInput{
			Union: &responses.ResponsesInput_ListValue{
				ListValue: &responses.InputItemList{
					ListValue: []*responses.InputItem{
						{
							Union: &responses.InputItem_InputMessage{InputMessage: &responses.ItemInputMessage{
								Role: responses.MessageRole_user,
								Content: []*responses.ContentItem{
									{
										Union: &responses.ContentItem_Text{Text: &responses.ContentItemText{Type: responses.ContentItemType_input_text, Text: userPrompt}},
									},
								},
							}},
						},
					},
				},
			},
		},
		PreviousResponseId: &previousResponseID,
	}

	resp, err := CreateResponses(ctx, secondReq)
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

func hashChunkingCacheInput(sysPrompt, userPrompt string) string {
	sum := sha256.Sum256([]byte(sysPrompt + "\x00" + userPrompt))
	return hex.EncodeToString(sum[:])
}
