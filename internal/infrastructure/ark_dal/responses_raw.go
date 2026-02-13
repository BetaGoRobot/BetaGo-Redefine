package ark_dal

import (
	"context"
	"errors"
	"fmt"
	"time"

	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo/utility/otel"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/gg/gptr"
	"github.com/redis/go-redis/v9"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func ResponseWithCache(ctx context.Context, sysPrompt, userPrompt, modelID string) (res string, err error) {
	ctx, span := otel.LarkRobotOtelTracer.Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("sys_prompt").String(sysPrompt))
	span.SetAttributes(attribute.Key("user_prompt").String(userPrompt))
	defer span.End()
	defer func() { span.RecordError(err) }()
	key := fmt.Sprintf("ark:response:cache:chunking:%s:%s", modelID, userPrompt)

	respID, err := redis_dal.GetRedisClient().Get(ctx, key).Result()
	if err != nil && err != redis.Nil {
		logs.L().Ctx(ctx).Error("get cache error", zap.Error(err))
		return
	}
	if respID == "" {
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
		resp, err := client.CreateResponses(ctx, req)
		if err != nil {
			logs.L().Ctx(ctx).Error("responses error", zap.Error(err))
			return "", err
		}
		if err := redis_dal.GetRedisClient().Set(ctx, key, resp.Id, 0).Err(); err != nil && err != redis.Nil {
			logs.L().Ctx(ctx).Error("set cache error", zap.Error(err))
			return "", err
		}
		if err := redis_dal.GetRedisClient().ExpireAt(ctx, key, time.Unix(exp, 0)).Err(); err != nil && err != redis.Nil {
			logs.L().Ctx(ctx).Error("expire cache error", zap.Error(err))
			return "", err
		}
		respID = resp.Id
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

	resp, err := client.CreateResponses(ctx, secondReq)
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
