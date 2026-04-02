package handlers

import (
	"context"
	"errors"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type MusicSearchArgs struct {
	Type     MusicSearchType `json:"type"`
	Keywords string          `json:"keywords" cli:"keywords,input,required"`
}

type musicSearchHandler struct{}

var MusicSearch musicSearchHandler

const musicSearchToolResultKey = "music_search_result"

func (musicSearchHandler) ParseCLI(args []string) (MusicSearchArgs, error) {
	argsMap, input := parseArgs(args...)
	searchType, err := xcommand.ParseEnum[MusicSearchType](argsMap["type"])
	if err != nil {
		return MusicSearchArgs{}, err
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
	searchType, err := xcommand.ParseEnum[MusicSearchType](string(parsed.Type))
	if err != nil {
		return MusicSearchArgs{}, err
	}
	parsed.Type = searchType
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
				Desc: "搜索对象类型",
			}).
			AddProp("keywords", &arktools.Prop{
				Type: "string",
				Desc: "搜索关键词；当 type=playlist 时传歌单 ID",
			}).
			AddRequired("keywords"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(musicSearchToolResultKey)
			return result
		},
	}
}

func (musicSearchHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg MusicSearchArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	accessor := appconfig.NewAccessor(ctx, currentChatID(data, metaData), currentOpenID(data, metaData))
	replyInThread := utils.GetIfInthread(ctx, metaData, accessor.MusicCardInThread())
	send := func(sendCtx context.Context, cardContent *larktpl.TemplateCardContent) (string, error) {
		return sendCompatibleCardWithMessageID(sendCtx, data, metaData, cardContent, "_musicSearch", replyInThread)
	}
	patch := func(patchCtx context.Context, msgID string, cardContent *larktpl.TemplateCardContent) error {
		return larkmsg.PatchCard(patchCtx, cardContent, msgID)
	}

	if arg.Type == MusicSearchTypeAlbum {
		err = neteaseapi.StreamMusicListCardForRequest(ctx, neteaseapi.MusicListRequest{
			Scene: neteaseapi.MusicListSceneAlbumSearch,
			Query: arg.Keywords,
		}, send, patch)
	} else if arg.Type == MusicSearchTypePlaylist {
		err = neteaseapi.StreamMusicListCardForRequest(ctx, neteaseapi.MusicListRequest{
			Scene: neteaseapi.MusicListScenePlaylistDetail,
			Query: arg.Keywords,
		}, send, patch)
	} else if arg.Type == MusicSearchTypeSong {
		err = neteaseapi.StreamMusicListCardForRequest(ctx, neteaseapi.MusicListRequest{
			Scene: neteaseapi.MusicListSceneSongSearch,
			Query: arg.Keywords,
		}, send, patch)
	} else {
		err = errors.New("unknown search type")
	}
	if err != nil {
		return err
	}
	metaData.SetExtra(musicSearchToolResultKey, "音乐卡片已发送")
	return nil
}

func resolveMusicSearchApprovalSummary(arg MusicSearchArgs) string {
	if arg.Type == "" {
		return "将根据关键词「" + arg.Keywords + "」发送音乐卡片"
	}
	return "将根据" + string(arg.Type) + "搜索「" + arg.Keywords + "」并发送音乐卡片"
}

func (musicSearchHandler) CommandDescription() string {
	return "搜索音乐"
}

func (musicSearchHandler) CommandExamples() []string {
	return []string{
		"/music 稻香",
		"/music --type=album 范特西",
		"/music --type=playlist 3778678",
	}
}
