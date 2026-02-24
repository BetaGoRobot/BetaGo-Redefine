package cardhandlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/ops"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/minio/minio-go/v7"

	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/gg/gptr"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func HandleSubmit(ctx context.Context, cardAction *callback.CardActionTriggerEvent) {
	// 移除 --st=xxx --et=xxx这样的参数
	srcCmd := cardAction.Event.Action.Value["command"].(string)
	srcCmd = utils.RemoveArgFromStr(srcCmd, "st", "et")
	stStr, _ := cardAction.Event.Action.FormValue["start_time_picker"].(string)
	etStr, _ := cardAction.Event.Action.FormValue["end_time_picker"].(string)
	st, err := time.ParseInLocation("2006-01-02 15:04 -0700", stStr, utils.UTC8Loc())
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to parse start time", zap.Error(err))
	}
	et, err := time.ParseInLocation("2006-01-02 15:04 -0700", etStr, utils.UTC8Loc())
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to parse end time", zap.Error(err))
	}

	srcCmd += fmt.Sprintf(" --st=\"%s\" --et=\"%s\"", st.In(utils.UTC8Loc()).Format(time.RFC3339), et.In(utils.UTC8Loc()).Format(time.RFC3339))
	msgID := cardAction.Event.Context.OpenMessageID

	data := new(larkim.P2MessageReceiveV1)
	data.Event = new(larkim.P2MessageReceiveV1Data)
	data.Event.Message = new(larkim.EventMessage)
	data.Event.Message.MessageId = gptr.Of(msgID)
	data.Event.Message.ChatId = new(string)
	*data.Event.Message.ChatId = cardAction.Event.Context.OpenChatID

	err = ops.ExecuteFromRawCommand(
		ctx,
		data,
		&xhandler.BaseMetaData{
			Refresh: true,
		},
		srcCmd,
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("Refresh obj error", zap.Error(err))
	}
}

func GetCardMusicByPage(ctx context.Context, musicID string, page int) *larktpl.TemplateCardContent {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("musicID").String(musicID))
	defer span.End()

	const (
		maxSingleLineLen = 48
		maxPageSize      = 18
	)
	musicURL, err := neteaseapi.NetEaseGCtx.GetMusicURL(ctx, musicID)
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get music URL", zap.Error(err))
		return nil
	}

	songDetail := neteaseapi.NetEaseGCtx.GetDetail(ctx, musicID).Songs[0]
	picURL := songDetail.Al.PicURL
	imageKey, ossURL, err := larkimg.UploadPicAllinOne(ctx, picURL, musicID, true)
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to upload picture", zap.Error(err))
		return nil
	}

	lyrics, lyricsURL := neteaseapi.NetEaseGCtx.GetLyrics(ctx, musicID)
	lyrics = utils.TrimLyrics(lyrics)

	artistNameList := make([]map[string]string, 0)
	for _, ar := range songDetail.Ar {
		artistNameList = append(artistNameList, map[string]string{"name": ar.Name})
	}

	type resultURL struct {
		Title      string
		LyricsURL  string
		MusicURL   string
		PictureURL string
		Album      string
		Artist     []map[string]string
		Duration   int
	}

	targetURL := &resultURL{
		Title:      songDetail.Name,
		LyricsURL:  lyricsURL,
		MusicURL:   musicURL,
		PictureURL: ossURL,
		Album:      songDetail.Al.Name,
		Artist:     artistNameList,
		Duration:   songDetail.Dt,
	}
	dal := miniodal.New(miniodal.Internal)
	res, err := dal.Upload(ctx).
		WithContentType(xmodel.ContentTypePlainText.String()).
		SkipDedup(false).
		WithReader(io.NopCloser(bytes.NewReader(utils.MustMarshal(targetURL)))).
		Do("cloudmusic", "info/"+musicID+".json", minio.PutObjectOptions{}).PreSignURL()
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to upload to minio", zap.Error(err))
		return nil
	}

	playerURL := utils.BuildURL(res)
	// eg: page = 1
	quotaRemain := maxPageSize
	lyricList := strings.Split(lyrics, "\n")
	newList := make([]string, 0)
	curPage := 1
	for _, l := range lyricList {
		quotaRemain--
		if len(l) > maxSingleLineLen {
			quotaRemain--
		}
		if quotaRemain <= 0 {
			curPage++
			quotaRemain = maxPageSize
			if curPage > page {
				break
			}
		}
		if curPage == page {
			newList = append(newList, l)
		}
	}

	lyrics = strings.Join(newList, "\n")

	return larktpl.NewCardContent(
		ctx,
		larktpl.SingleSongDetailTemplate,
	).
		AddVariable("lyrics", lyrics).
		AddVariable("title", songDetail.Name).
		AddVariable("sub_title", songDetail.Ar[0].Name).
		AddVariable("imgkey", map[string]any{"img_key": imageKey}).
		AddVariable("player_url", playerURL).
		AddVariable("full_lyrics_button", map[string]string{"type": "lyrics", "id": musicID}).
		AddVariable("refresh_id", map[string]string{"type": "refresh", "id": musicID})
}

func SendMusicCard(ctx context.Context, metaData *xhandler.BaseMetaData, musicID string, msgID string, page int) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("musicID").String(musicID))
	defer span.End()

	card := GetCardMusicByPage(ctx, musicID, page)
	err := larkmsg.ReplyCard(ctx, card, msgID, "_music"+musicID, utils.GetIfInthread(ctx, metaData, config.Get().NeteaseMusicConfig.MusicCardInThread))
	if err != nil {
		return
	}
}

func SendAlbumCard(ctx context.Context, metaData *xhandler.BaseMetaData, albumID string, msgID string) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("albumID").String(albumID))
	defer span.End()

	albumDetails, err := neteaseapi.NetEaseGCtx.GetAlbumDetail(ctx, albumID)
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get album detail", zap.Error(err))
		return
	}
	searchRes := neteaseapi.SearchMusic{Result: *albumDetails}

	result, err := neteaseapi.NetEaseGCtx.AsyncGetSearchRes(ctx, searchRes)
	if err != nil {
		return
	}
	cardContent, err := neteaseapi.BuildMusicListCard(ctx,
		result,
		neteaseapi.MusicItemNoTrans,
		neteaseapi.CommentTypeSong,
	)
	if err != nil {
		return
	}
	err = larkmsg.ReplyCard(ctx, cardContent, msgID, "_album", utils.GetIfInthread(ctx, metaData, config.Get().NeteaseMusicConfig.MusicCardInThread))
	if err != nil {
		return
	}
}

func HandleFullLyrics(ctx context.Context, metaData *xhandler.BaseMetaData, musicID, msgID string) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("msgID").String(msgID), attribute.Key("musicID").String(musicID))
	defer span.End()
	songDetail := neteaseapi.NetEaseGCtx.GetDetail(ctx, musicID).Songs[0]

	imgKey, _, err := larkimg.UploadPicAllinOne(ctx, songDetail.Al.PicURL, musicID, true)
	lyric, _ := neteaseapi.NetEaseGCtx.GetLyrics(ctx, musicID)
	lyric = utils.TrimLyrics(lyric)
	sp := strings.Split(lyric, "\n")
	left := strings.Join(sp[:len(sp)/2], "\n")
	right := strings.Join(sp[len(sp)/2+1:], "\n")

	cardContent := larktpl.NewCardContent(
		ctx,
		larktpl.FullLyricsTemplate,
	).
		AddVariable("left_lyrics", left).
		AddVariable("right_lyrics", right).
		AddVariable("title", songDetail.Name).
		AddVariable("sub_title", songDetail.Ar[0].Name).
		AddVariable("imgkey", imgKey)
	err = larkmsg.ReplyCard(ctx, cardContent, msgID, "_music", utils.GetIfInthread(ctx, metaData, config.Get().NeteaseMusicConfig.MusicCardInThread))
	if err != nil {
		return
	}
}

func HandleWithDraw(ctx context.Context, cardAction *callback.CardActionTriggerEvent) {
	userID := cardAction.Event.Operator.OpenID
	msgID := cardAction.Event.Context.OpenMessageID
	if config.Get().LarkConfig.WithDrawReplace { // 伪撤回
		cardContent := larkcard.NewCardBuildHelper().
			SetContent(fmt.Sprintf("这条消息被%s撤回啦！", larkmsg.AtUserString(userID))).Build(ctx)
		err := larkmsg.PatchCard(ctx, cardContent, msgID)
		if err != nil {
			logs.L().Ctx(ctx).Error("Failed to patch card", zap.Error(err))
		}
	} else {
		// 撤回消息
		resp, err := lark_dal.Client().Im.Message.Delete(ctx, larkim.NewDeleteMessageReqBuilder().MessageId(msgID).Build())
		if err != nil {
			logs.L().Ctx(ctx).Error("Failed to delete message", zap.Error(err))
			return
		}
		if !resp.Success() {
			logs.L().Ctx(ctx).Error("Delete message error", zap.String("error", resp.Error()))
		}
	}
}

func HandleRefreshMusic(ctx context.Context, musicID, msgID string) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("msgID").String(msgID), attribute.Key("musicID").String(musicID))
	defer span.End()

	card := GetCardMusicByPage(ctx, musicID, 1)
	resp, err := lark_dal.Client().Im.V1.Message.Patch(
		ctx, larkim.NewPatchMessageReqBuilder().
			MessageId(msgID).
			Body(
				larkim.NewPatchMessageReqBodyBuilder().
					Content(card.String()).
					Build(),
			).
			Build(),
	)
	if err != nil {
		return
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("Refresh music card error", zap.Error(err))
		return
	}
	return
}

func HandleRefreshObj(ctx context.Context, cardAction *callback.CardActionTriggerEvent) {
	srcCmd := cardAction.Event.Action.Value["command"].(string)
	msgID := cardAction.Event.Context.OpenMessageID

	data := new(larkim.P2MessageReceiveV1)
	data.Event = new(larkim.P2MessageReceiveV1Data)
	data.Event.Message = new(larkim.EventMessage)
	data.Event.Message.MessageId = gptr.Of(msgID)
	data.Event.Message.ChatId = new(string)
	*data.Event.Message.ChatId = cardAction.Event.Context.OpenChatID

	err := ops.ExecuteFromRawCommand(
		ctx,
		data,
		&xhandler.BaseMetaData{
			Refresh: true,
		},
		srcCmd,
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("Refresh obj error", zap.Error(err))
	}
}
