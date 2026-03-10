package ops

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var _ Op = &ReactMsgOperator{}

// ReactMsgOperator  Repeat
type ReactMsgOperator struct {
	OpBase
}

func (r *ReactMsgOperator) Name() string {
	return "ReactMsgOperator"
}

// FeatureInfo 返回功能信息
func (r *ReactMsgOperator) FeatureInfo() *xhandler.FeatureInfo {
	return &xhandler.FeatureInfo{
		ID:          "react",
		Name:        "消息反应功能",
		Description: "随机给消息添加表情反应",
		Default:     true,
	}
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *ReactMsgOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	// React

	// 开始摇骰子, 默认概率10%
	realRate := messageConfigAccessor(ctx, event, meta).ReactionDefaultRate()
	if utils.Prob(float64(realRate) / 100) {
		_, err := larkmsg.AddReaction(ctx, larkmsg.GetRandomEmoji(), *event.Event.Message.MessageId)
		if err != nil {
			logs.L().Ctx(ctx).Error("reactMessage error", zap.Error(err))
			return err
		}
	} else {
		if utils.Prob(float64(realRate) / 100) {
			ins := query.Q.ReactImageMeterial
			res, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(*event.Event.Message.ChatId)).Find()
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				logs.L().Ctx(ctx).Error("reactMessage error", zap.Error(err))
				return err
			}
			if len(res) == 0 {
				return nil
			}
			target := utils.SampleSlice(res)
			err = replyMediaMessage(ctx, *event.Event.Message.MessageId, target.Type, target.FileID, "_imageReact")
		}
	}

	return nil
}
