package reaction

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
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
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(event)))
	defer span.End()
	defer recordSpanError(span, &err)
	realRate := config.Get().RateConfig.ReactionFollowDefaultRate
	if utils.Prob(float64(realRate) / 100) {
		_, err = larkmsg.AddReaction(ctx, *event.Event.ReactionType.EmojiType, *event.Event.MessageId)
		if err != nil {
			return err
		}
	}

	return
}
