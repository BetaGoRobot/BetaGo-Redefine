package reaction

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

var _ Op = &RecordReactionOperator{}

// RecordReactionOperator Repeat
//
//	@author heyuhengmatt
//	@update 2024-07-17 01:36:07
type RecordReactionOperator struct {
	OpBase
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *RecordReactionOperator) Run(ctx context.Context, event *larkim.P2MessageReactionCreatedV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(event), 256)...)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	chatID, err := larkmsg.GetChatIDFromMsgID(ctx, *event.Event.MessageId)
	if err != nil {
		return err
	}
	if *event.Event.OperatorType != "user" {
		return nil
	}
	openID := botidentity.ReactionOpenID(event)
	if openID == "" {
		logs.L().Ctx(ctx).Warn("skip reaction record without open_id",
			zap.String("message_id", *event.Event.MessageId),
		)
		return nil
	}
	userName, err := larkuser.GetUserNameCache(ctx, chatID, openID)
	if err != nil {
		return err
	}
	ins := query.Q.InteractionStat
	return ins.WithContext(ctx).Create(&model.InteractionStat{
		OpenID:     openID,
		GuildID:    chatID,
		MsgID:      *event.Event.MessageId,
		UserName:   userName,
		ActionType: "add_reaction",
	})
}
