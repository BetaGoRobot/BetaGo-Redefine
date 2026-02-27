package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReplyAddHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:18
func ReplyAddHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	argMap, _ := parseArgs(args...)
	logs.L().Ctx(ctx).Info("args", zap.Any("args", argMap))
	if len(argMap) > 0 {
		word, ok := argMap["word"]
		if !ok {
			return errors.New("arg word is required")
		}

		matchType, ok := argMap["type"]
		if !ok {
			return errors.New("arg type(substr, full) is required") // 临时下掉regex
			return errors.New("arg type(substr, regex, full) is required")
		}
		if word == "" {
			return errors.New("arg word is empty, please change your key word")
		}

		if matchType != string(xmodel.MatchTypeSubStr) && matchType != string(xmodel.MatchTypeRegex) && matchType != string(xmodel.MatchTypeFull) {
			return errors.New("type must be substr, regex or full")
		}
		replyType, ok := argMap["reply_type"]
		if !ok {
			replyType = string(xmodel.ReplyTypeText)
		}

		var reply string

		if replyType == string(xmodel.ReplyTypeImg) { // 图片类型，需要回复图片
			if data.Event.Message.ParentId == nil {
				return errors.New("reply_type **img** must reply to a image message")
			}
			parentMsg := larkmsg.GetMsgFullByID(ctx, *data.Event.Message.ParentId)
			if len(parentMsg.Data.Items) != 0 {
				parentMsgItem := parentMsg.Data.Items[0]
				contentMap := make(map[string]string)
				err := sonic.UnmarshalString(*parentMsgItem.Body.Content, &contentMap)
				if err != nil {
					logs.L().Ctx(ctx).Warn("repeatMessage", zap.Error(err))
					return err
				}
				switch *parentMsgItem.MsgType {
				case larkim.MsgTypeSticker:
					imgKey := contentMap["file_key"]
					ins := query.Q.StickerMapping
					resList, err := ins.WithContext(ctx).Where(ins.StickerKey.Eq(imgKey)).Find()
					if err != nil {
						return err
					}
					if len(resList) == 0 {
						return errors.New("sticker key not found")
					}
					res := resList[0]
					if res == nil {
						if stickerFile, err := larkimg.GetMsgImages(ctx, *data.Event.Message.ParentId, contentMap["file_key"], "image"); err != nil {
							logs.L().Ctx(ctx).Warn("repeatMessage", zap.Error(err))
						} else {
							newImgKey := larkimg.UploadPicture2LarkReader(ctx, stickerFile)
							ins := query.Q.StickerMapping
							err = ins.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.StickerMapping{
								StickerKey: imgKey,
								ImageKey:   newImgKey,
							})
							if err != nil {
								return err
							}
						}
					}
					reply = res.ImageKey
				case larkim.MsgTypeImage:
					imageFile, err := larkimg.GetMsgImages(ctx, *data.Event.Message.ParentId, contentMap["image_key"], "image")
					if err != nil {
						return err
					}
					reply = larkimg.UploadPicture2LarkReader(ctx, imageFile)
				default:
					return errors.New("reply_type **img** must reply to a image message")
				}
			}
		} else {
			reply, ok = argMap["reply"]
			if !ok {
				return errors.New("arg reply is required")
			}
		}

		ins := query.Q.QuoteReplyMsgCustom
		if err := ins.WithContext(ctx).
			Create(&model.QuoteReplyMsgCustom{
				GuildID:   *data.Event.Message.ChatId,
				MatchType: string(xmodel.WordMatchType(matchType)),
				Keyword:   word,
				Reply:     reply,
				ReplyType: replyType,
			}); err != nil {
			return err
		}
		larkmsg.ReplyMsgText(ctx, "回复语句添加成功", *data.Event.Message.MessageId, "_replyAdd", false)
		return nil
	}
	return xerror.ErrArgsIncompelete
}

// ReplyGetHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
func ReplyGetHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	argMap, _ := parseArgs(args...)
	logs.L().Ctx(ctx).Info("args", zap.Any("args", argMap))
	ChatID := *data.Event.Message.ChatId

	lines := make([]map[string]string, 0)
	ins := query.Q.QuoteReplyMsgCustom
	resListCustom, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(ChatID)).Find()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	for _, res := range resListCustom {
		if res.GuildID == ChatID {
			if res.ReplyType == larkim.MsgTypeImage {
				if strings.HasPrefix(res.Reply, "img") {
					res.Reply = fmt.Sprintf("![picture](%s)", res.Reply)
				} else {
					res.Reply = fmt.Sprintf("![picture](%s)", getImageKeyByStickerKey(res.Reply))
				}
			}
			lines = append(lines, map[string]string{
				"title1": "Custom",
				"title2": res.Keyword,
				"title3": res.Reply,
				"title4": string(res.MatchType),
			})
		}
	}
	ins2 := query.Q.QuoteReplyMsg
	resListGlobal, err := ins2.WithContext(ctx).Find()
	if err != nil {
		return err
	}
	for _, res := range resListGlobal {
		if string(res.ReplyType) == larkim.MsgTypeImage {
			if strings.HasPrefix(res.Reply, "img") {
				res.Reply = fmt.Sprintf("![picture](%s)", res.Reply)
			} else {
				res.Reply = fmt.Sprintf("![picture](%s)", getImageKeyByStickerKey(res.Reply))
			}
		}
		lines = append(lines, map[string]string{
			"title1": "Global",
			"title2": res.Keyword,
			"title3": res.Reply,
			"title4": string(res.MatchType),
		})
	}
	cardContent := larktpl.NewCardContent(
		ctx,
		larktpl.FourColSheetTemplate,
	).
		AddVariable("title1", "Scope").
		AddVariable("title2", "Keyword").
		AddVariable("title3", "Reply").
		AddVariable("title4", "MatchType").
		AddVariable("table_raw_array_1", lines)

	err = larkmsg.ReplyCard(ctx, cardContent, *data.Event.Message.MessageId, "_replyGet", false)
	if err != nil {
		return err
	}
	return nil
}
