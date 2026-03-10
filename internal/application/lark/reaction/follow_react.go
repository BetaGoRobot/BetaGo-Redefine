package reaction

import (
	"context"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var _ Op = &FollowReactionOperator{}

// FollowReactionOperator Repeat
//
//	@author heyuhengmatt
//	@update 2024-07-17 01:36:07
type FollowReactionOperator struct {
	OpBase
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *FollowReactionOperator) Run(ctx context.Context, event *larkim.P2MessageReactionCreatedV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(event), 256)...)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	chatID := ""
	if meta != nil {
		chatID = meta.ChatID
	}
	realRate := appconfig.NewAccessor(ctx, chatID, reactionUserID(event, meta)).ReactionFollowDefaultRate()
	if utils.Prob(float64(realRate) / 100) {
		_, err = larkmsg.AddReaction(ctx, *event.Event.ReactionType.EmojiType, *event.Event.MessageId)
		if err != nil {
			return err
		}
	}

	return
}

func reactionUserID(event *larkim.P2MessageReactionCreatedV1, meta *xhandler.BaseMetaData) string {
	if event != nil && event.Event != nil && event.Event.UserId != nil {
		if event.Event.UserId.OpenId != nil {
			return *event.Event.UserId.OpenId
		}
		if event.Event.UserId.UserId != nil {
			return *event.Event.UserId.UserId
		}
	}
	if meta != nil {
		return meta.UserID
	}
	return ""
}
