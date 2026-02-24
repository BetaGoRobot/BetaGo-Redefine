package ops

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
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

// PreRun Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *ReactMsgOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	return
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *ReactMsgOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	defer func() { span.RecordError(err) }()

	// React

	// 开始摇骰子, 默认概率10%
	realRate := config.Get().RateConfig.ReactionDefaultRate
	if utils.Prob(float64(realRate) / 100) {
		_, err := larkmsg.AddReaction(ctx, larkmsg.GetRandomEmoji(), *event.Event.Message.MessageId)
		if err != nil {
			logs.L().Ctx(ctx).Error("reactMessage error", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
			return err
		}
	} else {
		if utils.Prob(float64(realRate) / 100) {
			ins := query.Q.ReactImageMeterial
			res, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(*event.Event.Message.ChatId)).Find()
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				logs.L().Ctx(ctx).Error("reactMessage error", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
				return err
			}
			if len(res) == 0 {
				return nil
			}
			target := utils.SampleSlice(res)
			if target.Type == larkim.MsgTypeImage {
				content, _ := sonic.MarshalString(map[string]string{
					"image_key": target.FileID,
				})
				_, err = larkmsg.ReplyMsgRawContentType(
					ctx,
					*event.Event.Message.MessageId,
					larkim.MsgTypeImage,
					content,
					"_imageReact",
					false,
				)
			} else {
				content, _ := sonic.MarshalString(map[string]string{
					"file_key": target.FileID,
				})
				_, err = larkmsg.ReplyMsgRawContentType(
					ctx,
					*event.Event.Message.MessageId,
					larkim.MsgTypeSticker,
					content,
					"_imageReact",
					false,
				)
			}
		}
	}

	return nil
}
