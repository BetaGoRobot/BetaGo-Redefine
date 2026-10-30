package reaction

import (
	"context"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/larkeventstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
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

func (r *FollowReactionOperator) Name() string {
	return "FollowReaction"
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *FollowReactionOperator) Run(ctx context.Context, event *larkim.P2MessageReactionCreatedV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	larkeventstore.AnnotateReaction(ctx, event)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	if event == nil || event.Event == nil || event.Event.MessageId == nil || event.Event.ReactionType == nil || event.Event.ReactionType.EmojiType == nil || meta == nil || meta.ChatID == "" {
		return nil
	}
	chatID := meta.ChatID
	realRate := appconfig.NewAccessor(ctx, chatID, reactionOpenID(event, meta)).ReactionFollowDefaultRate()
	if utils.Prob(float64(realRate) / 100) {
		larkmsg.AddReactionAsync(ctx, *event.Event.ReactionType.EmojiType, *event.Event.MessageId)
	}

	return
}

func reactionOpenID(event *larkim.P2MessageReactionCreatedV1, meta *xhandler.BaseMetaData) string {
	if openID := botidentity.ReactionOpenID(event); openID != "" {
		return openID
	}
	if meta != nil {
		return meta.OpenID
	}
	return ""
}
