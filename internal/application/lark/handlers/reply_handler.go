package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
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

type ReplyAddArgs struct {
	Word      string `json:"word"`
	Type      string `json:"type"`
	Reply     string `json:"reply"`
	ReplyType string `json:"reply_type"`
}

type ReplyGetArgs struct{}

type replyAddHandler struct{}
type replyGetHandler struct{}

var ReplyAdd replyAddHandler
var ReplyGet replyGetHandler

func (replyAddHandler) ParseCLI(args []string) (ReplyAddArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := ReplyAddArgs{
		Word:      argMap["word"],
		Type:      argMap["type"],
		Reply:     argMap["reply"],
		ReplyType: argMap["reply_type"],
	}
	if parsed.Word == "" {
		return ReplyAddArgs{}, errors.New("arg word is required")
	}
	if parsed.Type == "" {
		return ReplyAddArgs{}, errors.New("arg type(substr, full) is required")
	}
	if parsed.ReplyType == "" {
		parsed.ReplyType = string(xmodel.ReplyTypeText)
	}
	return parsed, nil
}

func (replyAddHandler) ParseTool(raw string) (ReplyAddArgs, error) {
	parsed := ReplyAddArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ReplyAddArgs{}, err
	}
	if parsed.Word == "" {
		return ReplyAddArgs{}, errors.New("arg word is required")
	}
	if parsed.Type == "" {
		return ReplyAddArgs{}, errors.New("arg type(substr, full) is required")
	}
	if parsed.ReplyType == "" {
		parsed.ReplyType = string(xmodel.ReplyTypeText)
	}
	return parsed, nil
}

func (replyAddHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "reply_add",
		Desc: "新增群聊关键词回复规则",
		Params: arktools.NewParams("object").
			AddProp("word", &arktools.Prop{
				Type: "string",
				Desc: "触发关键词",
			}).
			AddProp("type", &arktools.Prop{
				Type: "string",
				Desc: "匹配类型，可选值：substr、full、regex",
			}).
			AddProp("reply", &arktools.Prop{
				Type: "string",
				Desc: "文本回复内容。reply_type=img 时可以不传，改为使用当前引用图片",
			}).
			AddProp("reply_type", &arktools.Prop{
				Type: "string",
				Desc: "回复类型，可选值：text、image。默认 text",
			}).
			AddRequired("word").
			AddRequired("type"),
	}
}

func (replyAddHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ReplyAddArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	logs.L().Ctx(ctx).Info("args", zap.Any("args", arg))
	if arg.Word == "" {
		return xerror.ErrArgsIncompelete
	}

	if arg.Word == "" {
		return errors.New("arg word is empty, please change your key word")
	}
	if arg.Type != string(xmodel.MatchTypeSubStr) && arg.Type != string(xmodel.MatchTypeRegex) && arg.Type != string(xmodel.MatchTypeFull) {
		return errors.New("type must be substr, regex or full")
	}

	reply := arg.Reply
	if arg.ReplyType == string(xmodel.ReplyTypeImg) {
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
	} else if reply == "" {
		return errors.New("arg reply is required")
	}

	ins := query.Q.QuoteReplyMsgCustom
	if err := ins.WithContext(ctx).
		Create(&model.QuoteReplyMsgCustom{
			GuildID:   *data.Event.Message.ChatId,
			MatchType: string(xmodel.WordMatchType(arg.Type)),
			Keyword:   arg.Word,
			Reply:     reply,
			ReplyType: arg.ReplyType,
		}); err != nil {
		return err
	}
	larkmsg.ReplyMsgText(ctx, "回复语句添加成功", *data.Event.Message.MessageId, "_replyAdd", false)
	return nil
}

func (replyGetHandler) ParseCLI(args []string) (ReplyGetArgs, error) {
	return ReplyGetArgs{}, nil
}

func (replyGetHandler) ParseTool(raw string) (ReplyGetArgs, error) {
	if err := parseEmptyToolArgs(raw); err != nil {
		return ReplyGetArgs{}, err
	}
	return ReplyGetArgs{}, nil
}

func (replyGetHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name:   "reply_get",
		Desc:   "查看当前群聊的关键词回复规则",
		Params: arktools.NewParams("object"),
	}
}

func (replyGetHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ReplyGetArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	logs.L().Ctx(ctx).Info("args", zap.Any("args", arg))
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
