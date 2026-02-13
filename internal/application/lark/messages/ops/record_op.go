package ops

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/BetaGo/consts"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

var _ Op = &RepeatMsgOperator{}

// RecordMsgOperator  RepeatMsg Op
//
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:51
type RecordMsgOperator struct {
	OpBase
}

func (r *RecordMsgOperator) Name() string {
	return "RecordMsgOperator"
}

// PreRun Repeat
//
//	@receiver r *RepeatMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:35
func (r *RecordMsgOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	return
}

// Run Repeat
//
//	@receiver r *RepeatMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:41
func (r *RecordMsgOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	imgSeq, err := larkimg.GetAllImageFromMsgEvent(ctx, event.Event.Message)
	if err != nil {
		return
	}
	if imgSeq != nil {
		for imageKey := range imgSeq {
			err = larkimg.DownImgFromMsgAsync(
				ctx,
				*event.Event.Message.MessageId,
				larkim.MsgTypeImage,
				imageKey,
			)
			if err != nil {
				return err
			}
		}
	}
	msg := event.Event.Message
	if msg != nil && *msg.MessageType == larkim.MsgTypeSticker {
		contentMap := make(map[string]string)
		err := sonic.UnmarshalString(*msg.Content, &contentMap)
		if err != nil {
			logs.L().Ctx(ctx).Error("repeatMessage error", zap.Error(err))
			return err
		}
		stickerKey := contentMap["file_key"]
		// 表情包为全局file_key，可以直接存下
		ins := query.Q.ReactImageMeterial
		if err := ins.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
			Create(&model.ReactImageMeterial{GuildID: *msg.ChatId, FileID: stickerKey, Type: consts.LarkResourceTypeSticker}); err != nil {
			return err
		}
	}

	return
}
