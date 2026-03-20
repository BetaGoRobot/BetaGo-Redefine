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
	"github.com/bytedance/sonic"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ReplyAddArgs struct {
	Word      string           `json:"word"`
	Type      ReplyMatchType   `json:"type"`
	Reply     string           `json:"reply"`
	ReplyType ReplyContentType `json:"reply_type"`
	ImageKey  string           `json:"image_key"`
}

type ReplyGetArgs struct{}

type replyAddHandler struct{}
type replyGetHandler struct{}

var ReplyAdd replyAddHandler
var ReplyGet replyGetHandler

const replyActionToolResultKey = "reply_action_result"

func (replyAddHandler) ParseCLI(args []string) (ReplyAddArgs, error) {
	argMap, _ := parseArgs(args...)
	matchType, err := xcommand.ParseEnum[ReplyMatchType](argMap["type"])
	if err != nil {
		return ReplyAddArgs{}, err
	}
	replyType, err := parseReplyContentType(argMap["reply_type"])
	if err != nil {
		return ReplyAddArgs{}, err
	}
	parsed := ReplyAddArgs{
		Word:      argMap["word"],
		Type:      matchType,
		Reply:     argMap["reply"],
		ReplyType: replyType,
		ImageKey:  argMap["image_key"],
	}
	if parsed.Word == "" {
		return ReplyAddArgs{}, errors.New("arg word is required")
	}
	if parsed.Type == "" {
		return ReplyAddArgs{}, errors.New("arg type(substr, full) is required")
	}
	return parsed, nil
}

func (replyAddHandler) ParseTool(raw string) (ReplyAddArgs, error) {
	parsed := ReplyAddArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ReplyAddArgs{}, err
	}
	matchType, err := xcommand.ParseEnum[ReplyMatchType](string(parsed.Type))
	if err != nil {
		return ReplyAddArgs{}, err
	}
	replyType, err := parseReplyContentType(string(parsed.ReplyType))
	if err != nil {
		return ReplyAddArgs{}, err
	}
	parsed.Type = matchType
	parsed.ReplyType = replyType
	if parsed.Word == "" {
		return ReplyAddArgs{}, errors.New("arg word is required")
	}
	if parsed.Type == "" {
		return ReplyAddArgs{}, errors.New("arg type(substr, full) is required")
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
				Desc: "关键词匹配方式",
			}).
			AddProp("reply", &arktools.Prop{
				Type: "string",
				Desc: "文本回复内容。reply_type=img 时可以不传，改为使用当前引用图片",
			}).
			AddProp("image_key", &arktools.Prop{
				Type: "string",
				Desc: "reply_type=image 时可直接指定图片 key，适合 schedule 场景",
			}).
			AddProp("reply_type", &arktools.Prop{
				Type: "string",
				Desc: "回复内容类型",
			}).
			AddRequired("word").
			AddRequired("type"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(replyActionToolResultKey)
			return result
		},
	}
}

func (replyAddHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ReplyAddArgs) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()

	logs.L().Ctx(ctx).Info("args", zap.Any("args", arg))
	if arg.Word == "" {
		return xerror.ErrArgsIncompelete
	}

	if arg.Word == "" {
		return errors.New("arg word is empty, please change your key word")
	}
	if arg.Type != ReplyMatchTypeSubstr && arg.Type != ReplyMatchTypeRegex && arg.Type != ReplyMatchTypeFull {
		return errors.New("type must be substr, regex or full")
	}
	if tryDeferAgenticApproval(ctx, metaData, agenticDeferredApprovalSpec{
		ToolName:        "reply_add",
		ApprovalSummary: "将为关键词「" + arg.Word + "」添加 " + string(arg.Type) + " 匹配回复",
	}) {
		return nil
	}
	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}

	reply := arg.Reply
	replyType := normalizeReplyType(string(arg.ReplyType))
	if replyType == string(xmodel.ReplyTypeImg) {
		if arg.ImageKey != "" {
			reply = arg.ImageKey
		} else if data == nil || data.Event.Message.ParentId == nil {
			return errors.New("reply_type=image requires image_key or replying to an image message")
		}
		parentMsg := larkmsg.GetMsgFullByID(ctx, *data.Event.Message.ParentId)
		if len(parentMsg.Data.Items) != 0 && reply == "" {
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
		if reply == "" {
			return errors.New("reply_type=image requires image_key or replying to an image message")
		}
	} else if reply == "" {
		return errors.New("arg reply is required")
	}

	ins := query.Q.QuoteReplyMsgCustom
	if err := ins.WithContext(ctx).
		Create(&model.QuoteReplyMsgCustom{
			GuildID:   chatID,
			MatchType: string(xmodel.WordMatchType(arg.Type)),
			Keyword:   arg.Word,
			Reply:     reply,
			ReplyType: replyType,
		}); err != nil {
		return err
	}
	metaData.SetExtra(replyActionToolResultKey, "回复语句添加成功")
	return sendCompatibleText(ctx, data, metaData, "回复语句添加成功", "_replyAdd", false)
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
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	logs.L().Ctx(ctx).Info("args", zap.Any("args", arg))
	ChatID := currentChatID(data, metaData)

	lines := make([]map[string]string, 0)
	ins := query.Q.QuoteReplyMsgCustom
	resListCustom, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(ChatID)).Find()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	for _, res := range resListCustom {
		if res.GuildID == ChatID {
			if isImageReplyType(res.ReplyType) {
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
		if isImageReplyType(string(res.ReplyType)) {
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

	return sendCompatibleCard(ctx, data, metaData, cardContent, "_replyGet", false)
}

func (replyAddHandler) CommandDescription() string {
	return "新增关键词回复"
}

func (replyGetHandler) CommandDescription() string {
	return "查看关键词回复"
}

func (replyAddHandler) CommandExamples() []string {
	return []string{
		"/reply add --word=早安 --reply=早安早安",
		"/reply add --word=天气 --reply=今天天气不错 --type=substr",
	}
}

func (replyGetHandler) CommandExamples() []string {
	return []string{
		"/reply get",
	}
}

func normalizeReplyType(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "", string(xmodel.ReplyTypeText):
		return string(xmodel.ReplyTypeText)
	case "image", "img":
		return string(xmodel.ReplyTypeImg)
	default:
		return strings.ToLower(strings.TrimSpace(input))
	}
}

func parseReplyContentType(raw string) (ReplyContentType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(ReplyContentTypeText):
		return xcommand.ParseEnum[ReplyContentType](string(ReplyContentTypeText))
	case "image", "img":
		return xcommand.ParseEnum[ReplyContentType](string(ReplyContentTypeImage))
	default:
		return xcommand.ParseEnum[ReplyContentType](raw)
	}
}

func isImageReplyType(replyType string) bool {
	switch strings.ToLower(strings.TrimSpace(replyType)) {
	case string(xmodel.ReplyTypeImg), larkim.MsgTypeImage:
		return true
	default:
		return false
	}
}
