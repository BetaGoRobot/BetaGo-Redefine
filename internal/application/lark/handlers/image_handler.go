package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcopywriting"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/bytedance/sonic"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

type ImageAddArgs struct {
	URL    string         `json:"url"`
	ImgKey string         `json:"img_key"`
	Type   ImageAssetType `json:"type"`
}

type ImageGetArgs struct{}
type ImageDeleteArgs struct {
	ImgKey string         `json:"img_key"`
	Type   ImageAssetType `json:"type"`
}

type imageAddHandler struct{}
type imageGetHandler struct{}
type imageDeleteHandler struct{}

var ImageAdd imageAddHandler
var ImageGet imageGetHandler
var ImageDelete imageDeleteHandler

func (imageAddHandler) ParseCLI(args []string) (ImageAddArgs, error) {
	argMap, _ := parseArgs(args...)
	imageType, err := xcommand.ParseEnum[ImageAssetType](normalizeImageType(argMap["type"]))
	if err != nil {
		return ImageAddArgs{}, err
	}
	return ImageAddArgs{
		URL:    argMap["url"],
		ImgKey: argMap["img_key"],
		Type:   imageType,
	}, nil
}

func (imageAddHandler) ParseTool(raw string) (ImageAddArgs, error) {
	parsed := ImageAddArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ImageAddArgs{}, err
	}
	imageType, err := xcommand.ParseEnum[ImageAssetType](normalizeImageType(string(parsed.Type)))
	if err != nil {
		return ImageAddArgs{}, err
	}
	parsed.Type = imageType
	return parsed, nil
}

func (imageAddHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "image_add",
		Desc: "把图片素材加入当前群聊的图片素材库",
		Params: arktools.NewParams("object").
			AddProp("url", &arktools.Prop{
				Type: "string",
				Desc: "图片 URL。与 img_key 二选一；都不传时尝试使用当前引用/话题中的图片",
			}).
			AddProp("img_key", &arktools.Prop{
				Type: "string",
				Desc: "飞书图片 key。与 url 二选一；都不传时尝试使用当前引用/话题中的图片",
			}).
			AddProp("type", &arktools.Prop{
				Type: "string",
				Desc: "素材类型。使用 img_key 时可传",
			}),
	}
}

func (imageAddHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ImageAddArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	logs.L().Ctx(ctx).Info("wordAddHandler", zap.Any("args", arg))
	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	if arg.URL != "" || arg.ImgKey != "" {
		imgKey := arg.ImgKey
		msgType := normalizeImageType(string(arg.Type))
		if msgType == "" {
			msgType = larkim.MsgTypeImage
		}
		// by url
		if arg.URL != "" {
			imgKey = larkimg.UploadPicture2Lark(ctx, arg.URL)
			msgType = larkim.MsgTypeImage
		}
		if imgKey == "" {
			return errors.New("img_key is required")
		}
		if msgType != larkim.MsgTypeImage && msgType != larkim.MsgTypeSticker {
			return fmt.Errorf("unsupported image type: %s", msgType)
		}
		err := createImage(ctx, currentMessageID(data), chatID, imgKey, msgType)
		if err != nil {
			return err
		}
		if msgID := currentMessageID(data); msgID != "" {
			larkmsg.AddReactionAsync(ctx, "DONE", msgID)
		}
		return nil
	} else if data == nil {
		return errors.New("url or img_key is required for scheduled execution")
	} else if data.Event.Message.ThreadId != nil {
		// 找到话题中的所有图片
		var combinedErr error
		resp, err := lark_dal.Client().Im.Message.List(ctx, larkim.NewListMessageReqBuilder().ContainerIdType("thread").ContainerId(*data.Event.Message.ThreadId).Build())
		if err != nil {
			return err
		}
		currentBot := botidentity.Current()
		for _, msg := range resp.Data.Items {
			if currentBot.BotOpenID == "" || *msg.Sender.Id != currentBot.BotOpenID {
				if imgKey := getImageKey(msg); imgKey != "" {
					err := createImage(ctx, *msg.MessageId, chatID, imgKey, *msg.MsgType)
					if err != nil {
						if combinedErr == nil {
							otel.RecordError(span, err)
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
			err := createImage(ctx, *msg.MessageId, chatID, imgKey, *msg.MsgType)
			if err != nil {
				span.SetStatus(codes.Error, "addImage not complete with some error")
				otel.RecordError(span, err)
				return err
			}
			larkmsg.AddReactionAsync(ctx, "JIAYI", *msg.MessageId)
		} else {
			return errors.New(xcopywriting.GetSampleCopyWritings(ctx, chatID, xcopywriting.ImgQuoteNoParent))
		}
	} else {
		return errors.New(xcopywriting.GetSampleCopyWritings(ctx, chatID, xcopywriting.ImgNotAnyValidArgs))
	}
	if msgID := currentMessageID(data); msgID != "" {
		larkmsg.AddReactionAsync(ctx, "DONE", msgID)
	}
	return nil
}

func (imageGetHandler) ParseCLI(args []string) (ImageGetArgs, error) {
	return ImageGetArgs{}, nil
}

func (imageGetHandler) ParseTool(raw string) (ImageGetArgs, error) {
	if err := parseEmptyToolArgs(raw); err != nil {
		return ImageGetArgs{}, err
	}
	return ImageGetArgs{}, nil
}

func (imageGetHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name:   "image_get",
		Desc:   "查看当前群聊已登记的图片素材",
		Params: arktools.NewParams("object"),
	}
}

func (imageGetHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ImageGetArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	logs.L().Ctx(ctx).Info("replyGetHandler", zap.Any("args", arg))
	ChatID := currentChatID(data, metaData)

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

	return sendCompatibleCard(ctx, data, metaData, cardContent, "_replyGet", false)
}

func (imageDeleteHandler) ParseCLI(args []string) (ImageDeleteArgs, error) {
	argMap, _ := parseArgs(args...)
	imageType, err := xcommand.ParseEnum[ImageAssetType](normalizeImageType(argMap["type"]))
	if err != nil {
		return ImageDeleteArgs{}, err
	}
	return ImageDeleteArgs{
		ImgKey: argMap["img_key"],
		Type:   imageType,
	}, nil
}

func (imageDeleteHandler) ParseTool(raw string) (ImageDeleteArgs, error) {
	parsed := ImageDeleteArgs{}
	if raw == "" || raw == "{}" {
		return parsed, nil
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ImageDeleteArgs{}, err
	}
	imageType, err := xcommand.ParseEnum[ImageAssetType](normalizeImageType(string(parsed.Type)))
	if err != nil {
		return ImageDeleteArgs{}, err
	}
	parsed.Type = imageType
	return parsed, nil
}

func (imageDeleteHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "image_delete",
		Desc: "删除图片素材。可显式传 img_key 和 type；不传时尝试删除当前引用消息或话题中的图片素材",
		Params: arktools.NewParams("object").
			AddProp("img_key", &arktools.Prop{
				Type: "string",
				Desc: "要删除的图片或贴纸 key。schedule 场景建议显式传入",
			}).
			AddProp("type", &arktools.Prop{
				Type: "string",
				Desc: "素材类型。不传则自动尝试两种类型",
			}),
	}
}

func (imageDeleteHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ImageDeleteArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	logs.L().Ctx(ctx).Info("replyDelHandler", zap.Any("args", arg))
	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	if arg.ImgKey != "" {
		if err := deleteImageByKey(ctx, chatID, arg.ImgKey, normalizeImageType(string(arg.Type))); err != nil {
			return err
		}
		if msgID := currentMessageID(data); msgID != "" {
			larkmsg.AddReactionAsync(ctx, "GeneralDoNotDisturb", msgID)
		}
		return nil
	}
	if data == nil {
		return errors.New("img_key is required for scheduled execution")
	}

	if data.Event.Message.ThreadId != nil {
		// 找到话题中的所有图片
		var combinedErr error
		resp, err := lark_dal.Client().Im.Message.List(ctx, larkim.NewListMessageReqBuilder().ContainerIdType("thread").ContainerId(*data.Event.Message.ThreadId).Build())
		if err != nil {
			return err
		}
		for _, msg := range resp.Data.Items {
			if imgKey := getImageKey(msg); imgKey != "" {
				err = deleteImageByKey(ctx, chatID, imgKey, normalizeImageType(*msg.MsgType))
				if err != nil {
					otel.RecordError(span, err)
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
			currentBot := botidentity.Current()
			if currentBot.BotOpenID != "" && *msg.Sender.Id == currentBot.BotOpenID {
				if imgKey := getImageKey(msg); imgKey != "" {
					err := deleteImageByKey(ctx, chatID, imgKey, normalizeImageType(*msg.MsgType))
					if err != nil {
						span.SetStatus(codes.Error, "delImage not complete with some error")
						otel.RecordError(span, err)
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

func normalizeImageType(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "":
		return ""
	case "img", larkim.MsgTypeImage:
		return larkim.MsgTypeImage
	case larkim.MsgTypeSticker:
		return larkim.MsgTypeSticker
	default:
		return strings.ToLower(strings.TrimSpace(input))
	}
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

func deleteImageByKey(ctx context.Context, chatID, imgKey, msgType string) error {
	switch msgType {
	case "":
		errImage := deleteImage(ctx, "", chatID, imgKey, larkim.MsgTypeImage)
		if errImage == nil {
			return nil
		}
		errSticker := deleteImage(ctx, "", chatID, imgKey, larkim.MsgTypeSticker)
		if errSticker == nil {
			return nil
		}
		return errors.Wrapf(errImage, "%v", errSticker)
	case larkim.MsgTypeImage, larkim.MsgTypeSticker:
		return deleteImage(ctx, "", chatID, imgKey, msgType)
	default:
		return fmt.Errorf("unsupported image type: %s", msgType)
	}
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

func (imageAddHandler) CommandDescription() string {
	return "新增图片素材"
}

func (imageGetHandler) CommandDescription() string {
	return "查看图片素材"
}

func (imageDeleteHandler) CommandDescription() string {
	return "删除图片素材"
}

func (imageAddHandler) CommandExamples() []string {
	return []string{
		"/image add --url=https://example.com/demo.png",
	}
}

func (imageGetHandler) CommandExamples() []string {
	return []string{
		"/image get",
	}
}

func (imageDeleteHandler) CommandExamples() []string {
	return []string{
		"/image del",
	}
}
