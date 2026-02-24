package larkchat

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/cache"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func GetChatName(ctx context.Context, chatID string) (chatName string) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	resp, err := lark_dal.Client().Im.V1.Chat.Get(ctx, larkim.NewGetChatReqBuilder().ChatId(chatID).Build())
	if err != nil {
		return
	}
	if !resp.Success() {
		err = errors.New(resp.Error())
		return
	}
	if resp != nil && resp.Data != nil && resp.Data.Name != nil {
		chatName = *resp.Data.Name
	}
	return
}

func GetChatInfo(ctx context.Context, chatID string) (chat *larkim.GetChatRespData, err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	req := larkim.NewGetChatReqBuilder().ChatId(chatID).Build()
	resp, err := lark_dal.Client().Im.V1.Chat.Get(ctx, req)
	if err != nil {
		return
	}
	if !resp.Success() {
		err = errors.New(resp.Error())
		return
	}
	return resp.Data, nil
}

func GetChatInfoCache(ctx context.Context, chatID string) (val *larkim.GetChatRespData, err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	return cache.GetOrExecute(ctx, chatID, func() (chat *larkim.GetChatRespData, err error) {
		return GetChatInfo(ctx, chatID)
	})
}
