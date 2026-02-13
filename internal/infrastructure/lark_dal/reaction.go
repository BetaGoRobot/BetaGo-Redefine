package lark_dal

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo/utility/log"
	"github.com/BetaGoRobot/BetaGo/utility/otel"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/kevinmatthe/zaplog"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
)

func AddReaction(ctx context.Context, reactionType, msgID string) (reactionID string, err error) {
	_, span := otel.LarkRobotOtelTracer.Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("msgID").String(msgID))
	defer span.End()

	req := larkim.NewCreateMessageReactionReqBuilder().Body(larkim.NewCreateMessageReactionReqBodyBuilder().ReactionType(larkim.NewEmojiBuilder().EmojiType(reactionType).Build()).Build()).MessageId(msgID).Build()
	resp, err := client.Im.V1.MessageReaction.Create(ctx, req)
	if err != nil {
		log.Zlog.Error("AddReaction", zaplog.Error(err))
		return "", err
	}
	if !resp.Success() {
		log.Zlog.Error("AddReaction", zaplog.String("Error", resp.Error()))
		return "", errors.New(resp.Error())
	}
	AddReaction2DB(ctx, msgID)
	return *resp.Data.ReactionId, err
}
