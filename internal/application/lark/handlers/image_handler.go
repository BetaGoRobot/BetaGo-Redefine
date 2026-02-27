package handlers

import (
	"context"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcopywriting"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

// ImageAddHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:13
func ImageAddHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	argMap, _ := parseArgs(args...)
	logs.L().Ctx(ctx).Info("wordAddHandler", zap.String("TraceID", span.SpanContext().TraceID().String()), zap.Any("args", argMap))
	if len(argMap) > 0 {
		var imgKey string
		// by url
		picURL, ok := argMap["url"]
		if ok {
			imgKey = larkimg.UploadPicture2Lark(ctx, picURL)
		}
		// by img_key
		inputImgKey, ok := argMap["img_key"]
		if ok {
			imgKey = inputImgKey
		}
		err := createImage(ctx, *data.Event.Message.MessageId, *data.Event.Message.ChatId, imgKey, larkim.MsgTypeImage)
		if err != nil {
			return err
		}

	} else if data.Event.Message.ThreadId != nil {
		// 找到话题中的所有图片
		var combinedErr error
		resp, err := lark_dal.Client().Im.Message.List(ctx, larkim.NewListMessageReqBuilder().ContainerIdType("thread").ContainerId(*data.Event.Message.ThreadId).Build())
		if err != nil {
			return err
		}
		for _, msg := range resp.Data.Items {
			if *msg.Sender.Id != config.Get().LarkConfig.BotOpenID {
				if imgKey := getImageKey(msg); imgKey != "" {
					err := createImage(ctx, *msg.MessageId, *msg.ChatId, imgKey, *msg.MsgType)
					if err != nil {
						if combinedErr == nil {
							span.RecordError(err)
							combinedErr = err
						} else {
							combinedErr = errors.Wrapf(combinedErr, "%v", err)
						}
					} else {
						larkmsg.AddReactionAsync(ctx, "JIAYI", *msg.MessageId)
					}
				}
			}
		}
		if combinedErr != nil {
			span.SetStatus(codes.Error, "addImage not complete with some error")
			return errors.New("addImage not complete with some error")
		}
	} else if data.Event.Message.ParentId != nil {
		parentMsg := larkmsg.GetMsgFullByID(ctx, *data.Event.Message.ParentId)
		if len(parentMsg.Data.Items) != 0 {
			msg := parentMsg.Data.Items[0]
			imgKey := getImageKey(msg)
			err := createImage(ctx, *msg.MessageId, *data.Event.Message.ChatId, imgKey, *msg.MsgType)
			if err != nil {
				span.SetStatus(codes.Error, "addImage not complete with some error")
				span.RecordError(err)
				return err
			}
			larkmsg.AddReactionAsync(ctx, "JIAYI", *msg.MessageId)
		} else {
			return errors.New(xcopywriting.GetSampleCopyWritings(ctx, *data.Event.Message.ChatId, xcopywriting.ImgQuoteNoParent))
		}
	} else {
		return errors.New(xcopywriting.GetSampleCopyWritings(ctx, *data.Event.Message.ChatId, xcopywriting.ImgNotAnyValidArgs))
	}
	larkmsg.AddReactionAsync(ctx, "DONE", *data.Event.Message.MessageId)
	return nil
}

// ImageGetHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
func ImageGetHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	argMap, _ := parseArgs(args...)
	logs.L().Ctx(ctx).Info("replyGetHandler", zap.String("TraceID", span.SpanContext().TraceID().String()), zap.Any("args", argMap))
	ChatID := *data.Event.Message.ChatId

	lines := make([]map[string]string, 0)
	ins := query.Q.ReactImageMeterial
	resList, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(ChatID)).Find()
	for _, res := range resList {
		if res.GuildID == ChatID {
			lines = append(lines, map[string]string{
				"title1": res.Type,
				"title2": fmt.Sprintf("![picture](%s)", getImageKeyByStickerKey(res.FileID)),
			})
		}
	}
	cardContent := larktpl.NewCardContent(
		ctx,
		larktpl.TwoColSheetTemplate,
	).
		AddVariable("title1", "Type").
		AddVariable("title2", "Picture").
		AddVariable("table_raw_array_1", lines)

	err = larkmsg.ReplyCard(ctx, cardContent, *data.Event.Message.MessageId, "_replyGet", false)
	if err != nil {
		return err
	}
	return nil
}

// ImageDelHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
func ImageDelHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	defer span.RecordError(err)

	argMap, _ := parseArgs(args...)
	logs.L().Ctx(ctx).Info("replyDelHandler", zap.String("TraceID", span.SpanContext().TraceID().String()), zap.Any("args", argMap))

	if data.Event.Message.ThreadId != nil {
		// 找到话题中的所有图片
		var combinedErr error
		resp, err := lark_dal.Client().Im.Message.List(ctx, larkim.NewListMessageReqBuilder().ContainerIdType("thread").ContainerId(*data.Event.Message.ThreadId).Build())
		if err != nil {
			return err
		}
		for _, msg := range resp.Data.Items {
			if imgKey := getImageKey(msg); imgKey != "" {
				err = deleteImage(ctx, *msg.MessageId, *msg.ChatId, imgKey, *msg.MsgType)
				if err != nil {
					span.RecordError(err)
					if combinedErr == nil {
						combinedErr = err
					} else {
						combinedErr = errors.Wrapf(combinedErr, "%v", err)
					}
				} else {
					larkmsg.AddReactionAsync(ctx, "GeneralDoNotDisturb", *msg.MessageId)
				}
			}
		}
		if combinedErr != nil {
			span.SetStatus(codes.Error, "delImage not complete with some error")
			return errors.New("delImage not complete with some error")
		}
	} else if data.Event.Message.ParentId != nil {
		parentMsgResp := larkmsg.GetMsgFullByID(ctx, *data.Event.Message.ParentId)
		if len(parentMsgResp.Data.Items) != 0 {
			msg := parentMsgResp.Data.Items[0]
			if *msg.Sender.Id == config.Get().LarkConfig.BotOpenID {
				if imgKey := getImageKey(msg); imgKey != "" {
					err := deleteImage(ctx, *msg.MessageId, *msg.ChatId, imgKey, *msg.MsgType)
					if err != nil {
						span.SetStatus(codes.Error, "delImage not complete with some error")
						span.RecordError(err)
						return err
					}
					larkmsg.AddReactionAsync(ctx, "GeneralDoNotDisturb", *msg.MessageId)
				}
			}

		}

	}
	return nil
}

func getImageKeyByStickerKey(stickerKey string) string {
	ins := query.Q.StickerMapping
	resList, err := ins.WithContext(context.Background()).Where(ins.StickerKey.Eq(stickerKey)).Find()
	if err != nil {
		return stickerKey
	}
	if len(resList) == 0 {
		return stickerKey
	}
	return resList[0].ImageKey
}

func getImageKey(msg *larkim.Message) string {
	if *msg.MsgType == larkim.MsgTypeSticker || *msg.MsgType == larkim.MsgTypeImage {
		contentMap := make(map[string]string)
		err := sonic.UnmarshalString(*msg.Body.Content, &contentMap)
		if err != nil {
			logs.L().Error("Error", zap.Error(err))
			return ""
		}
		switch *msg.MsgType {
		case larkim.MsgTypeImage:
			return contentMap["image_key"]
		case larkim.MsgTypeSticker:
			return contentMap["file_key"]
		default:
			return ""
		}
	}
	return ""
}

func deleteImage(ctx context.Context, msgID, chatID, imgKey, msgType string) error {
	ins := query.Q.ReactImageMeterial
	switch msgType {
	case "image":
		// 检查存在性
		res, err := ins.WithContext(ctx).
			Where(ins.GuildID.Eq(chatID), ins.FileID.Eq(imgKey), ins.Type.Eq(larkim.MsgTypeImage)).
			Delete()
		if err != nil {
			return err
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("img_key %s not exists", imgKey)
		}
	case "sticker":
		res, err := ins.WithContext(ctx).
			Where(ins.GuildID.Eq(chatID), ins.FileID.Eq(imgKey), ins.Type.Eq(larkim.MsgTypeSticker)).
			Delete()
		if err != nil {
			return err
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("sticker_key %s not exists", imgKey)
		}
	default:
		// do nothing
	}
	return nil
}

func createImage(ctx context.Context, msgID, chatID, imgKey, msgType string) error {
	ins := query.Q.ReactImageMeterial
	switch msgType {
	case "image":
		// 检查存在性
		err := ins.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
			Create(&model.ReactImageMeterial{GuildID: chatID, FileID: imgKey, Type: larkim.MsgTypeImage})
		if err != nil {
			return err
		}
	case "sticker":
		err := ins.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
			Create(&model.ReactImageMeterial{GuildID: chatID, FileID: imgKey, Type: larkim.MsgTypeSticker})
		if err != nil {
			return err
		}
	default:
		// do nothing
	}
	return nil
}
