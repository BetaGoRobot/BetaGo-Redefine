package handlers

import (
	"context"
	"errors"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
)

type MusicSearchArgs struct {
	Type     string `json:"type"`
	Keywords string `json:"keywords" cli:"keywords,input,required"`
}

type musicSearchHandler struct{}

var MusicSearch musicSearchHandler

func (musicSearchHandler) ParseCLI(args []string) (MusicSearchArgs, error) {
	argsMap, input := parseArgs(args...)
	searchType := argsMap["type"]
	if searchType == "" {
		searchType = "song"
	}
	if input == "" {
		return MusicSearchArgs{}, errors.New("keywords is required")
	}
	return MusicSearchArgs{
		Type:     searchType,
		Keywords: input,
	}, nil
}

func (musicSearchHandler) ParseTool(raw string) (MusicSearchArgs, error) {
	parsed := MusicSearchArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return MusicSearchArgs{}, err
	}
	if parsed.Type == "" {
		parsed.Type = "song"
	}
	if parsed.Keywords == "" {
		return MusicSearchArgs{}, errors.New("keywords is required")
	}
	return parsed, nil
}

func (musicSearchHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "music_search",
		Desc: "根据输入的关键词搜索相关的音乐并发送卡片",
		Params: arktools.NewParams("object").
			AddProp("type", &arktools.Prop{
				Type: "string",
				Desc: "搜索类型，可选值：song、album。默认 song",
			}).
			AddProp("keywords", &arktools.Prop{
				Type: "string",
				Desc: "音乐搜索的关键词, 多个关键词之间用空格隔开",
			}).
			AddRequired("keywords"),
	}
}

func (musicSearchHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg MusicSearchArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	keywords := []string{arg.Keywords}

	var cardContent *larktpl.TemplateCardContent
	if arg.Type == "album" {
		albumList, err := neteaseapi.NetEaseGCtx.SearchAlbumByKeyWord(ctx, keywords...)
		if err != nil {
			return err
		}

		cardContent, err = neteaseapi.BuildMusicListCard(ctx,
			albumList,
			neteaseapi.MusicItemTransAlbum,
			neteaseapi.CommentTypeAlbum,
			keywords...,
		)
		if err != nil {
			return err
		}
	} else if arg.Type == "artist" {
	} else if arg.Type == "playlist" {
	} else if arg.Type == "song" {
		musicList, err := neteaseapi.NetEaseGCtx.SearchMusicByKeyWord(ctx, keywords...)
		if err != nil {
			return err
		}
		cardContent, err = neteaseapi.BuildMusicListCard(ctx,
			musicList,
			neteaseapi.MusicItemNoTrans,
			neteaseapi.CommentTypeSong,
			keywords...,
		)
		if err != nil {
			return err
		}
	} else {
		return errors.New("Unknown search type")
	}

	err = larkmsg.ReplyCard(ctx, cardContent, *data.Event.Message.MessageId, "_musicSearch", utils.GetIfInthread(ctx, metaData, config.Get().NeteaseMusicConfig.MusicCardInThread))
	if err != nil {
		return err
	}
	return
}
