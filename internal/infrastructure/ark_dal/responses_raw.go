package ark_dal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
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

const responseCacheVersion = "v2"

type CachedResponseRequest struct {
	CacheScene         string
	SystemPrompt       string
	UserPrompt         string
	ModelID            string
	Text               *responses.ResponsesText
	Reasoning          *responses.ResponsesReasoning
	Thinking           *responses.ResponsesThinking
	DisablePrefixCache bool
}

func ResponseWithCache(ctx context.Context, sysPrompt, userPrompt, modelID string, scope llmusage.Scope) (res string, err error) {
	return ResponseTextWithCache(ctx, CachedResponseRequest{
		CacheScene:   "chunking",
		SystemPrompt: sysPrompt,
		UserPrompt:   userPrompt,
		ModelID:      modelID,
		Thinking: &responses.ResponsesThinking{
			Type: responses.ThinkingType_enabled.Enum(),
		},
	}, scope)
}

func ResponseTextWithCache(ctx context.Context, req CachedResponseRequest, scope llmusage.Scope) (res string, err error) {
	_, cfg, err := runtimeClientFn()
	if err != nil {
		return "", err
	}
	req.Reasoning = effectiveResponsesReasoning(cfg, req.ModelID, req.Reasoning)
	ctx, span := otel.StartNamed(ctx, "ark.responses.cache")
	span.SetAttributes(attribute.String("model.id", req.ModelID))
	span.SetAttributes(attribute.String("cache.scene", cacheScene(req.CacheScene)))
	span.SetAttributes(otel.PreviewAttrs("sys_prompt", req.SystemPrompt, 256)...)
	span.SetAttributes(otel.PreviewAttrs("user_prompt", req.UserPrompt, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if req.DisablePrefixCache {
		return responseTextDirect(ctx, req, scope, "direct")
	}

	key := botidentity.Current().NamespaceKey(
		"ark",
		"response",
		"cache",
		responseCacheVersion,
		cacheScene(req.CacheScene),
		req.ModelID,
		"thinking_"+req.Thinking.String(),
		"reasoning_"+responseReasoningEffort(req.Reasoning),
		hashResponseCacheInput(req.SystemPrompt),
	)
	span.SetAttributes(
		attribute.String("cache.key.preview", otel.PreviewString(key, 128)),
		attribute.Int("cache.key.len", len(key)),
	)

	redisGetCtx, redisGetSpan := otel.StartNamed(ctx, "ark.responses.cache_get")
	respID, err := redis_dal.GetRedisClient().Get(redisGetCtx, key).Result()
	redisGetSpan.End()
	if err != nil && err != redis.Nil {
		otel.RecordError(redisGetSpan, err)
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
			Store: new(true),
			Caching: &responses.ResponsesCaching{
				Type:   responses.CacheType_enabled.Enum(),
				Prefix: new(true),
			},
			ExpireAt:  new(exp),
			Thinking:  req.Thinking,
			Reasoning: req.Reasoning,
		}
		resp, err := createResponsesFn(ctx, cacheReq, scope)
		if err != nil {
			if isPrefixCacheInputTooShort(err) {
				span.AddEvent("prefix_cache_input_too_short_fallback")
				return responseTextDirect(ctx, req, scope, "cache_fallback")
			}
			logs.L().Ctx(ctx).Error(
				"responses error",
				responseRequestLogFields("cache_head", req.CacheScene, cacheReq, scope, err)...,
			)
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
		PreviousResponseId: new(respID),
		Text:               req.Text,
		Caching: &responses.ResponsesCaching{
			Type: responses.CacheType_enabled.Enum(),
		},
		Thinking:  req.Thinking,
		Reasoning: req.Reasoning,
	}

	resp, err := createResponsesFn(ctx, secondReq, scope)
	if err != nil {
		logs.L().Ctx(ctx).Error(
			"responses error",
			responseRequestLogFields("cache_continuation", req.CacheScene, secondReq, scope, err)...,
		)
		return "", err
	}

	return responseText(resp)
}

func responseTextDirect(
	ctx context.Context,
	req CachedResponseRequest,
	scope llmusage.Scope,
	stage string,
) (string, error) {
	directReq := &responses.ResponsesRequest{
		Model: req.ModelID,
		Input: textInput(
			responseInputMessage{responses.MessageRole_system, req.SystemPrompt},
			responseInputMessage{responses.MessageRole_user, req.UserPrompt},
		),
		Text:      req.Text,
		Thinking:  req.Thinking,
		Reasoning: req.Reasoning,
	}
	resp, err := createResponsesFn(ctx, directReq, scope)
	if err != nil {
		logs.L().Ctx(ctx).Error(
			"responses error",
			responseRequestLogFields(stage, req.CacheScene, directReq, scope, err)...,
		)
		return "", err
	}
	return responseText(resp)
}

func responseText(resp *responses.ResponseObject) (string, error) {
	for _, output := range resp.GetOutput() {
		if msg := output.GetOutputMessage(); msg != nil {
			if content := msg.GetContent(); len(content) > 0 {
				return content[0].GetText().GetText(), nil
			}
		}
	}
	return "", errors.New("text is nil")
}

type responseInputMessage struct {
	role responses.MessageRole_Enum
	text string
}

func singleTextInput(role responses.MessageRole_Enum, text string) *responses.ResponsesInput {
	return textInput(responseInputMessage{role: role, text: text})
}

func textInput(messages ...responseInputMessage) *responses.ResponsesInput {
	items := make([]*responses.InputItem, 0, len(messages))
	for _, message := range messages {
		items = append(items, &responses.InputItem{
			Union: &responses.InputItem_InputMessage{
				InputMessage: &responses.ItemInputMessage{
					Role: message.role,
					Content: []*responses.ContentItem{{
						Union: &responses.ContentItem_Text{
							Text: &responses.ContentItemText{
								Type: responses.ContentItemType_input_text,
								Text: message.text,
							},
						},
					}},
				},
			},
		})
	}
	return &responses.ResponsesInput{
		Union: &responses.ResponsesInput_ListValue{
			ListValue: &responses.InputItemList{
				ListValue: items,
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

func isPrefixCacheInputTooShort(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "input tokens must be greater than") &&
		strings.Contains(message, "when using prefix cache")
}

func responseRequestLogFields(
	stage string,
	scene string,
	req *responses.ResponsesRequest,
	scope llmusage.Scope,
	err error,
) []zap.Field {
	scope = llmusage.NormalizeScope(scope)
	fields := []zap.Field{
		zap.Error(err),
		zap.String("ark_request_stage", stage),
		zap.String("cache_scene", cacheScene(scene)),
		zap.String("source_type", string(scope.SourceType)),
		zap.String("source", scope.Source),
		zap.String("business_scene", string(scope.BusinessScene)),
		zap.String("business_operation", string(scope.BusinessOperation)),
		zap.String("chat_id", scope.ChatID),
		zap.String("open_id", scope.OpenID),
	}
	if req == nil {
		return fields
	}
	fields = append(fields,
		zap.String("model_id", req.Model),
		zap.String("model", req.Model),
		zap.Bool("previous_response_id_set", req.PreviousResponseId != nil),
		zap.String("previous_response_id_preview", otel.PreviewString(req.GetPreviousResponseId(), 128)),
		zap.String("thinking_type", responseThinkingType(req.Thinking)),
		zap.String("reasoning_effort", responseReasoningEffort(req.Reasoning)),
		zap.String("caching_type", responseCachingType(req.Caching)),
		zap.Bool("caching_prefix", req.GetCaching().GetPrefix()),
		zap.Bool("text_format_set", req.GetText().GetFormat() != nil),
	)
	return fields
}
