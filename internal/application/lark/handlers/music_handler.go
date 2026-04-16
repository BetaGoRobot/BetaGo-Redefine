package handlers

import (
	"bytes"
	"context"
	"errors"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

type MusicSearchArgs struct {
	Type     MusicSearchType `json:"type"`
	Keywords string          `json:"keywords" cli:"keywords,input,required"`
	Voice    bool            `json:"voice"`
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
		input = argsMap["keywords"]
		if input == "" {
			return MusicSearchArgs{}, errors.New("keywords is required")
		}
	}
	voice := argsMap["voice"] == "true" || argsMap["voice"] == "1"
	return MusicSearchArgs{
		Type:     searchType,
		Keywords: input,
		Voice:    voice,
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
		Desc: "根据输入的关键词搜索相关的音乐并发送卡片或语音消息",
		Params: arktools.NewParams("object").
			AddProp("type", &arktools.Prop{
				Type: "string",
				Desc: "搜索对象类型",
			}).
			AddProp("keywords", &arktools.Prop{
				Type: "string",
				Desc: "搜索关键词；当 type=playlist 时传歌单 ID",
			}).
			AddProp("voice", &arktools.Prop{
				Type: "boolean",
				Desc: "是否直接发送语音消息（仅限单曲搜索）",
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

	if arg.Type == "" || arg.Type == MusicSearchTypeSong {
		err = neteaseapi.StreamMusicListCardForRequest(ctx, neteaseapi.MusicListRequest{
			Scene: neteaseapi.MusicListSceneSongSearch,
			Query: arg.Keywords,
		}, send, patch)
	} else if arg.Type == MusicSearchTypeAlbum {
		err = neteaseapi.StreamMusicListCardForRequest(ctx, neteaseapi.MusicListRequest{
			Scene: neteaseapi.MusicListSceneAlbumSearch,
			Query: arg.Keywords,
		}, send, patch)
	} else if arg.Type == MusicSearchTypePlaylist {
		err = neteaseapi.StreamMusicListCardForRequest(ctx, neteaseapi.MusicListRequest{
			Scene: neteaseapi.MusicListScenePlaylistDetail,
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

// sendMusicVoice 搜索单曲并直接发送语音消息
func sendMusicVoice(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, keywords string) error {
	// 搜索歌曲
	songs, err := neteaseapi.NetEaseGCtx.SearchMusicByKeyWord(ctx, keywords)
	if err != nil {
		logs.L().Ctx(ctx).Error("search song for voice failed", zap.Error(err))
		return err
	}
	if len(songs) == 0 {
		return errors.New("未找到相关歌曲")
	}

	// 取第一首
	song := songs[0]
	if song.SongURL == "" {
		return errors.New("该歌曲无可用播放链接")
	}

	// 下载音频
	audioData, err := larkimg.GetAudioFromURL(ctx, song.SongURL)
	if err != nil {
		logs.L().Ctx(ctx).Error("download audio failed", zap.Error(err))
		return err
	}

	// 转换为 opus 格式（飞书语音消息需要 opus）
	opusData, durationMs, err := larkimg.ConvertMp3ToOpus(ctx, audioData)
	if err != nil {
		logs.L().Ctx(ctx).Error("convert to opus failed", zap.Error(err))
		return err
	}

	// 上传到 Lark
	fileKey, err := larkimg.UploadAudio(ctx, bytes.NewReader(opusData), song.Name+".opus", durationMs)
	if err != nil {
		logs.L().Ctx(ctx).Error("upload audio to lark failed", zap.Error(err))
		return err
	}

	// 发送语音消息
	msgID := currentMessageID(data)
	if msgID == "" {
		return errors.New("无法获取消息ID")
	}

	replyInThread := utils.GetIfInthread(ctx, metaData, false)
	_, err = larkmsg.ReplyMsgAudio(ctx, fileKey, msgID, "_musicVoice", replyInThread)
	if err != nil {
		logs.L().Ctx(ctx).Error("reply audio message failed", zap.Error(err))
		return err
	}

	metaData.SetExtra(musicSearchToolResultKey, "语音消息已发送: "+song.Name)
	return nil
}

func resolveMusicSearchApprovalSummary(arg MusicSearchArgs) string {
	msgType := "音乐卡片"
	if arg.Voice {
		msgType = "语音消息"
	}
	if arg.Type == "" {
		return "将根据关键词「" + arg.Keywords + "」发送" + msgType
	}
	return "将根据" + string(arg.Type) + "搜索「" + arg.Keywords + "」并发送" + msgType
}

func (musicSearchHandler) CommandDescription() string {
	return "搜索音乐"
}

func (musicSearchHandler) CommandExamples() []string {
	return []string{
		"/music 稻香",
		"/music --type=album 范特西",
		"/music --type=playlist 3778678",
		"/music --voice --type=song 夜曲",
	}
}
