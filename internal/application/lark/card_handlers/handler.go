package cardhandlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/ops"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xfuture"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/minio/minio-go/v7"

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
	ExecuteRawCommandFromCard(ctx, cardAction, srcCmd)
}

func GetCardMusicByPage(ctx context.Context, musicID, page int) larkmsg.RawCard {
	view := buildMusicDetailCardView(ctx, musicID, page)
	if view == nil {
		return nil
	}
	return BuildMusicDetailRawCard(ctx, *view)
}

func buildMusicDetailCardView(ctx context.Context, musicID, page int) *MusicDetailCardView {
	return buildMusicDetailCardViewWithAudio(ctx, musicID, page, true)
}

// buildMusicDetailCardViewWithAudio builds the view and optionally uploads audio synchronously.
// skipAudioUpload: true = skip audio upload (for fast initial display), false = upload audio inline (blocking).
func buildMusicDetailCardViewWithAudio(ctx context.Context, musicID, page int, skipAudioUpload bool) *MusicDetailCardView {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("musicID").Int(musicID))
	defer span.End()

	const (
		maxSingleLineLen = 48
		maxPageSize      = 18
	)
	musicURLFuture := xfuture.New(ctx, func(ctx context.Context) (string, error) {
		musicURL, err := neteaseapi.NetEaseGCtx.GetMusicURL(ctx, musicID)
		if err != nil {
			logs.L().Ctx(ctx).Error("Failed to get music URL", zap.Error(err))
			return "", err
		}
		return musicURL, nil
	})

	songDetailFuture := xfuture.New(ctx, func(ctx context.Context) (neteaseapi.MusicDetailSong, error) {
		detail := neteaseapi.NetEaseGCtx.GetDetail(ctx, musicID)
		if detail == nil || len(detail.Songs) == 0 {
			logs.L().Ctx(ctx).Error("Failed to get music detail", zap.Int("music_id", musicID))
			return neteaseapi.MusicDetailSong{}, nil
		}
		return detail.Songs[0], nil
	})

	lyricFuture := xfuture.New2(ctx, func(ctx context.Context) (string, string, error) {
		lyrics, lyricsURL := neteaseapi.NetEaseGCtx.GetLyrics(ctx, musicID)
		lyrics = utils.TrimLyrics(lyrics)
		return lyrics, lyricsURL, nil
	})

	songDetail, err := songDetailFuture.WaitFirst()
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get picture URL", zap.Error(err))
		return nil
	}
	imageKey, ossURL, err := larkimg.UploadPicAllinOne(ctx, songDetail.Al.PicURL, musicID, true)
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to upload picture", zap.Error(err))
		return nil
	}

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

	lyricRes, err := lyricFuture.WaitFirst()
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get lyrics", zap.Error(err))
		return nil
	}
	lyrics := lyricRes.T
	lyricsURL := lyricRes.K

	musicURL, err := musicURLFuture.WaitFirst()
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get music URL", zap.Error(err))
		return nil
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
		Do("cloudmusic", "info/"+strconv.Itoa(musicID)+".json", minio.PutObjectOptions{}).PreSignURL()
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to upload to minio", zap.Error(err))
		return nil
	}

	playerURL := utils.BuildURL(res)
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

	audioFileKey := ""
	if !skipAudioUpload {
		// Synchronous audio upload - blocking, only used when explicitly requested
		audioData, err := larkimg.GetAudioFromURL(ctx, musicURL)
		if err == nil {
			fileKey, _, err := larkimg.ConvertMp3ToOpusAndUpload(ctx, audioData, songDetail.Name+".opus")
			if err == nil {
				audioFileKey = fileKey
			} else {
				logs.L().Ctx(ctx).Warn("convert+upload to opus failed", zap.Error(err))
			}
		} else {
			logs.L().Ctx(ctx).Warn("get audio from url failed", zap.Error(err))
		}
	}

	subtitle := ""
	if len(songDetail.Ar) > 0 {
		subtitle = songDetail.Ar[0].Name
	}
	view := &MusicDetailCardView{
		Lyrics:       lyrics,
		Title:        songDetail.Name,
		Subtitle:     subtitle,
		ImageKey:     imageKey,
		PlayerURL:    playerURL,
		AudioFileKey: audioFileKey,
		AudioID:      "music_" + strconv.Itoa(musicID),
		FullLyricsButton: cardaction.New(cardaction.ActionMusicLyrics).
			WithID(strconv.Itoa(musicID)).
			Payload(),
		RefreshID: cardaction.New(cardaction.ActionMusicRefresh).
			WithID(strconv.Itoa(musicID)).
			Payload(),
	}
	return view
}

func PatchMusicCard(ctx context.Context, musicID int, msgID string, page int) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("msgID").String(msgID), attribute.Key("musicID").Int(musicID))
	defer span.End()

	view := buildMusicDetailCardView(ctx, musicID, page)
	if view == nil {
		return
	}

	// Patch card WITHOUT audio first (fast)
	if err := patchMusicDetailCardContent(ctx, msgID, *view); err != nil {
		logs.L().Ctx(ctx).Error("patch music detail card failed", zap.Error(err))
		return
	}

	// Async upload audio and patch card with audio (slow, non-blocking)
	go func() {
		bgCtx := context.Background()
		AsyncUploadAudioAndPatch(bgCtx, musicID, msgID, page, view.PlayerURL, view.Title)
	}()
}

// AsyncUploadAudioAndPatch uploads audio and patches the card with audio element.
// Call this in a goroutine after sending a card built with skipAudioUpload=true.
func AsyncUploadAudioAndPatch(ctx context.Context, musicID int, msgID string, page int, playerURL string, songName string) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.Key("msgID").String(msgID),
		attribute.Key("musicID").Int(musicID),
	)
	defer span.End()

	// Get fresh music URL (not the presigned JSON URL)
	musicURL, err := neteaseapi.NetEaseGCtx.GetMusicURL(ctx, musicID)
	if err != nil {
		logs.L().Ctx(ctx).Warn("AsyncUploadAudioAndPatch: get music URL failed", zap.Error(err))
		return
	}

	if strings.TrimSpace(musicURL) == "" {
		logs.L().Ctx(ctx).Warn("AsyncUploadAudioAndPatch: empty musicURL, skipping")
		return
	}

	audioData, err := larkimg.GetAudioFromURL(ctx, musicURL)
	if err != nil {
		logs.L().Ctx(ctx).Warn("AsyncUploadAudioAndPatch: get audio from url failed", zap.Error(err))
		return
	}

	if len(audioData) == 0 {
		logs.L().Ctx(ctx).Warn("AsyncUploadAudioAndPatch: audio data is empty, skipping")
		return
	}

	fileKey, _, err := larkimg.ConvertMp3ToOpusAndUpload(ctx, audioData, songName+".opus")
	if err != nil {
		logs.L().Ctx(ctx).Warn("AsyncUploadAudioAndPatch: convert+upload to opus failed", zap.Error(err))
		return
	}

	// Rebuild view with audio and patch
	view := buildMusicDetailCardViewWithAudio(ctx, musicID, page, true)
	view.AudioFileKey = fileKey

	if err := patchMusicDetailCardContent(ctx, msgID, *view); err != nil {
		logs.L().Ctx(ctx).Error("AsyncUploadAudioAndPatch: patch card failed", zap.Error(err))
		return
	}

	logs.L().Ctx(ctx).Info("AsyncUploadAudioAndPatch: card patched with audio",
		zap.String("msgID", msgID),
		zap.Int("musicID", musicID),
		zap.String("audioKey", fileKey))
}

func patchMusicDetailCardContent(ctx context.Context, msgID string, view MusicDetailCardView) error {
	content, err := BuildMusicDetailRawCard(ctx, view).JSON()
	if err != nil {
		return fmt.Errorf("marshal music detail raw card failed: %w", err)
	}
	if err := larkmsg.PatchRawCard(ctx, msgID, content); err == nil {
		return nil
	} else if shouldRetryMusicCardWithoutAudio(err, view.AudioFileKey) {
		logs.L().Ctx(ctx).Warn("music detail card audio unsupported, retry without audio element", zap.Error(err))
		view.AudioFileKey = ""
		content, marshalErr := BuildMusicDetailRawCard(ctx, view).JSON()
		if marshalErr != nil {
			return fmt.Errorf("marshal fallback music detail raw card failed: %w", marshalErr)
		}
		if retryErr := larkmsg.PatchRawCard(ctx, msgID, content); retryErr != nil {
			return retryErr
		}
		return nil
	} else {
		return err
	}
}

func shouldRetryMusicCardWithoutAudio(err error, audioFileKey string) bool {
	if err == nil || strings.TrimSpace(audioFileKey) == "" {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "audio elem don't support forward") || strings.Contains(msg, "ErrCode: 300260")
}

func SendMusicCard(ctx context.Context, _ *xhandler.BaseMetaData, musicID int, msgID string, page int) {
	PatchMusicCard(ctx, musicID, msgID, page)
}

func SendAlbumCard(ctx context.Context, metaData *xhandler.BaseMetaData, albumID string, msgID string) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("albumID").String(albumID))
	defer span.End()

	cardContent, err := neteaseapi.BuildMusicListCardForRequest(ctx, neteaseapi.MusicListRequest{
		Scene: neteaseapi.MusicListSceneAlbumDetail,
		Query: albumID,
	})
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to build album detail card", zap.Error(err))
		return
	}
	accessor := appconfig.NewAccessorFromMeta(ctx, metaData)
	err = larkmsg.ReplyCard(ctx, cardContent, msgID, "_album", utils.GetIfInthread(ctx, metaData, accessor.MusicCardInThread()))
	if err != nil {
		return
	}
}

func HandleFullLyrics(ctx context.Context, metaData *xhandler.BaseMetaData, musicID int, msgID string) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("msgID").String(msgID), attribute.Key("musicID").Int(musicID))
	defer span.End()
	detail := neteaseapi.NetEaseGCtx.GetDetail(ctx, musicID)
	if detail == nil || len(detail.Songs) == 0 {
		logs.L().Ctx(ctx).Error("Failed to get music detail", zap.Int("music_id", musicID))
		return
	}
	songDetail := detail.Songs[0]

	imgKey, _, err := larkimg.UploadPicAllinOne(ctx, songDetail.Al.PicURL, musicID, true)
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to upload picture", zap.Error(err))
		return
	}
	lyric, _ := neteaseapi.NetEaseGCtx.GetLyrics(ctx, musicID)
	lyric = utils.TrimLyrics(lyric)
	sp := strings.Split(lyric, "\n")
	mid := len(sp) / 2
	left := strings.Join(sp[:mid], "\n")
	right := strings.Join(sp[mid:], "\n")

	cardContent := larktpl.NewCardContentWithData(ctx, larktpl.FullLyricsTemplate, &larktpl.FullLyricsCardVars{
		LeftLyrics:  left,
		RightLyrics: right,
		Title:       songDetail.Name,
		SubTitle:    songDetail.Ar[0].Name,
		ImgKey:      larktpl.ImageKeyRef{ImgKey: imgKey},
	})
	accessor := appconfig.NewAccessorFromMeta(ctx, metaData)
	err = larkmsg.ReplyCard(ctx, cardContent, msgID, "_music", utils.GetIfInthread(ctx, metaData, accessor.MusicCardInThread()))
	if err != nil {
		return
	}
}

func HandleWithDraw(ctx context.Context, cardAction *callback.CardActionTriggerEvent) {
	openID := cardAction.Event.Operator.OpenID
	msgID := cardAction.Event.Context.OpenMessageID
	accessor := appconfig.NewAccessor(ctx, cardAction.Event.Context.OpenChatID, cardAction.Event.Operator.OpenID)
	if accessor.WithDrawReplace() { // 伪撤回
		cardContent := larkcard.NewCardBuildHelper().
			SetContent(fmt.Sprintf("这条消息被%s撤回啦！", larkmsg.AtUserString(openID))).Build(ctx)
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

func HandleRefreshMusic(ctx context.Context, musicID int, msgID string) {
	PatchMusicCard(ctx, musicID, msgID, 1)
}

func HandleRefreshObj(ctx context.Context, cardAction *callback.CardActionTriggerEvent) {
	ExecuteRawCommandFromCard(ctx, cardAction, cardAction.Event.Action.Value["command"].(string))
}

func ExecuteRawCommandFromCard(ctx context.Context, cardAction *callback.CardActionTriggerEvent, rawCommand string) {
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
		rawCommand,
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("Refresh obj error", zap.Error(err))
	}
}
