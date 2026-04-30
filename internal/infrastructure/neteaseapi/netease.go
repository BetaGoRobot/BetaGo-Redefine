package neteaseapi

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhttp"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xrequest"
	"github.com/bytedance/sonic"
	"github.com/dlclark/regexp2"
	"github.com/minio/minio-go/v7"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type CommentType string

const (
	CommentTypeSong  CommentType = "0"
	CommentTypeAlbum CommentType = "3"

	defaultMusicURLBatchSize   = 1
	defaultMusicURLConcurrency = 4
	defaultPictureConcurrency  = 4
)

var warnOnce sync.Once

func songIDString(id int) string {
	return strconv.Itoa(id)
}

func joinSongIDs(ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		parts = append(parts, songIDString(id))
	}
	return strings.Join(parts, ",")
}

func normalizeMusicIDs(musicIDs ...int) []int {
	seen := make(map[int]struct{}, len(musicIDs))
	ids := make([]int, 0, len(musicIDs))
	for _, id := range musicIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func normalizeBatchSize(batchSize int) int {
	if batchSize <= 0 {
		return defaultMusicURLBatchSize
	}
	return batchSize
}

func normalizeMusicURLConcurrency(limit int) int {
	if limit <= 0 {
		return defaultMusicURLConcurrency
	}
	return limit
}

func musicURLConcurrencyFor(batchCount int) int {
	if batchCount <= 0 {
		return normalizeMusicURLConcurrency(0)
	}
	return min(batchCount, normalizeMusicURLConcurrency(0))
}

func fetchMusicURLBatch(ctx context.Context, cookies []*http.Cookie, musicIDs []int) ([]*musicData, error) {
	if len(musicIDs) == 0 {
		return nil, nil
	}
	r, err := xhttp.HttpClient.R().SetQueryParams(
		map[string]string{
			"id":        joinSongIDs(musicIDs),
			"level":     "standard",
			"timestamp": fmt.Sprint(time.Now().UnixNano()),
		},
	).SetCookies(cookies).Post(NetEaseAPIBaseURL + "/song/url/v1")
	if err != nil {
		return nil, err
	}
	if r.StatusCode() != 200 {
		return nil, fmt.Errorf("song url http status: %d", r.StatusCode())
	}
	music := &musicList{}
	if err := sonic.Unmarshal(r.Body(), music); err != nil {
		return nil, err
	}
	return music.Data, nil
}

var commonMusicExtensions = []string{".mp3", ".flac", ".m4a", ".wav", ".ogg", ".aac"}

func musicObjectKey(musicID int, rawURL string) string {
	ext := filepath.Ext(rawURL)
	if parsedURL, err := url.Parse(rawURL); err == nil {
		if parsedExt := path.Ext(path.Base(parsedURL.Path)); parsedExt != "" {
			ext = parsedExt
		}
	}
	return "music/" + songIDString(musicID) + ext
}

func tryGetMusicURLFromMinio(ctx context.Context, musicID int) string {
	for _, ext := range commonMusicExtensions {
		objKey := "music/" + songIDString(musicID) + ext
		if u, err := miniodal.TryGetFile(ctx, cacheBucket, objKey); err == nil && u != "" {
			return u
		}
	}
	return ""
}

func ensureMusicPresignedURL(ctx context.Context, item *musicData) (string, error) {
	if item == nil || item.ID == 0 || item.URL == "" {
		return "", nil
	}
	objKey := musicObjectKey(item.ID, item.URL)
	if cachedURL, err := miniodal.TryGetFile(ctx, cacheBucket, objKey); err != nil {
		return "", err
	} else if cachedURL != "" {
		return cachedURL, nil
	}
	return miniodal.New(miniodal.Internal).Upload(ctx).
		WithContentType(xmodel.ContentTypeAudio.String()).
		WithURL(item.URL).
		Do(cacheBucket, objKey, minio.PutObjectOptions{}).
		PreSignURL()
}

func Init() {
	InitCaches()

	config := config.Get().NeteaseMusicConfig
	if config == nil || config.BaseURL == "" {
		setNoop("netease config missing or base url empty")
		return
	}
	NetEaseAPIBaseURL = config.BaseURL
	ctx := &NetEaseContext{}
	NetEaseGCtx = ctx

	startUpCtx := context.Background()
	ctx.TryGetLastCookie(startUpCtx)

	go func() {
		err := ctx.LoginNetEase(startUpCtx)
		if err != nil {
			logs.L().Ctx(startUpCtx).Error("error in init loginNetease", zap.Error(err))
			err = ctx.LoginNetEaseQR(startUpCtx)
			if err != nil {
				logs.L().Ctx(startUpCtx).Error("error in init loginNeteaseQR", zap.Error(err))
			}
		}
		for {
			if ctx.loginType == "qr" {
				if !ctx.CheckIfLogin(startUpCtx) {
					ctx.LoginNetEaseQR(startUpCtx)
				}
			} else {
				ctx.RefreshLogin(startUpCtx)
				if ctx.CheckIfLogin(startUpCtx) {
					ctx.SaveCookie(startUpCtx)
				} else {
					logs.L().Ctx(startUpCtx).Error("error in refresh login")
				}
			}
			time.Sleep(time.Second * 300)
		}
	}()
}

func setNoop(reason string) {
	NetEaseGCtx = noopProvider{reason: reason}
	warnOnce.Do(func() {
		logs.L().Warn("NetEase API disabled, falling back to noop",
			zap.String("reason", reason),
		)
	})
}

// GetDailyRecommendID 获取当前账号日推
//
//	@receiver ctx
//	@return musicIDs
//	@return err
func (neteaseCtx *NetEaseContext) GetDailyRecommendID() (musicIDs map[int]string, err error) {
	musicIDs = make(map[int]string)

	resp, err := xrequest.
		ReqTimestamp().
		SetCookies(neteaseCtx.cookies).
		Post(NetEaseAPIBaseURL + "/recommend/songs")
	if err != nil || resp.StatusCode() != 200 {
		return
	}

	body := resp.Body()
	music := dailySongs{}
	sonic.Unmarshal(body, &music)
	for index := range music.Data.DailySongs {
		musicIDs[music.Data.DailySongs[index].ID] = music.Data.DailySongs[index].Name
	}
	return
}

func (neteaseCtx *NetEaseContext) GetMusicURLs(ctx context.Context, batchSize int, musicIDs ...int) (map[int]string, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Int("batchSize", normalizeBatchSize(batchSize)))
	defer span.End()

	ids := normalizeMusicIDs(musicIDs...)
	if len(ids) == 0 {
		return map[int]string{}, nil
	}

	// Check cache first, similar to AsyncGetSearchRes
	musicIDURL := make(map[int]string, len(ids))
	missingIDs := make([]int, 0, len(ids))

	for _, id := range ids {
		if u := tryGetMusicURLFromMinio(ctx, id); u != "" {
			musicIDURL[id] = u
		} else {
			missingIDs = append(missingIDs, id)
		}
	}

	if len(missingIDs) == 0 {
		return musicIDURL, nil
	}

	batches := utils.Chunk(missingIDs, normalizeBatchSize(batchSize))
	var mu sync.Mutex

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(musicURLConcurrencyFor(len(batches)))
	for _, batch := range batches {
		group.Go(func() error {
			rawItems, err := fetchMusicURLBatch(groupCtx, neteaseCtx.cookies, batch)
			if err != nil {
				return err
			}
			localResults := make(map[int]string, len(rawItems))
			for _, item := range rawItems {
				if item == nil || item.ID == 0 {
					continue
				}
				signedURL, err := ensureMusicPresignedURL(groupCtx, item)
				if err != nil {
					return err
				}
				if signedURL == "" {
					continue
				}
				localResults[item.ID] = signedURL
			}

			mu.Lock()
			for musicID, signedURL := range localResults {
				musicIDURL[musicID] = signedURL
			}
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return musicIDURL, nil
}

// GetMusicURLByIDs 依据ID获取URL/Name
func (neteaseCtx *NetEaseContext) GetMusicURLByIDs(ctx context.Context, musicIDs ...int) (map[int]string, error) {
	return neteaseCtx.GetMusicURLs(ctx, defaultMusicURLBatchSize, musicIDs...)
}

func (neteaseCtx *NetEaseContext) GetMusicURL(ctx context.Context, ID int) (url string, err error) {
	urlByID, err := neteaseCtx.GetMusicURLs(ctx, 1, ID)
	if err != nil {
		return "", err
	}
	if url, ok := urlByID[ID]; ok {
		return url, nil
	}
	return "", fmt.Errorf("song %d url not found", ID)
}

func (neteaseCtx *NetEaseContext) GetDetail(ctx context.Context, musicID int) (musicDetail *MusicDetail) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Int("songID", musicID))
	defer span.End()

	// Try cache first
	var cachedDetail MusicDetail
	if getMusicDetailCache().Get(ctx, "detail/"+songIDString(musicID)+".json", &cachedDetail) {
		return &cachedDetail
	}

	musicDetail, err := neteaseCtx.getDetailByIDs(ctx, []int{musicID})
	if err != nil {
		logs.L().Ctx(ctx).Warn("Unknown error", zap.Error(err))
		return nil
	}
	if len(musicDetail.Songs) == 0 {
		return nil
	}

	// Cache the detail
	getMusicDetailCache().Set(ctx, "detail/"+songIDString(musicID)+".json", musicDetail)

	for _, song := range musicDetail.Songs {
		picURL := song.Al.PicURL
		err := miniodal.New(miniodal.Internal).Upload(ctx).
			WithContentType(xmodel.ContentTypeImgJPEG.String()).
			WithURL(picURL).
			Do(cacheBucket, "picture/"+songIDString(song.ID)+filepath.Ext(picURL), minio.PutObjectOptions{}).Err()
		if err != nil {
			logs.L().Ctx(ctx).Warn("[PreUploadMusic] Get minio url failed...", zap.Error(err))
		}
	}
	return
}

func (neteaseCtx *NetEaseContext) getPlaylistDetail(ctx context.Context, playlistID string) (*PlaylistDetail, error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("playlistID").String(strings.TrimSpace(playlistID)))
	defer span.End()

	resp, err := xhttp.HttpClient.R().
		SetFormDataFromValues(
			map[string][]string{
				"id": {strings.TrimSpace(playlistID)},
			},
		).
		SetCookies(neteaseCtx.cookies).
		SetQueryParam("timestamp", fmt.Sprint(time.Now().UnixNano())).
		Post(NetEaseAPIBaseURL + "/playlist/detail")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("playlist detail http status: %d", resp.StatusCode())
	}

	parsed := &PlaylistDetailResponse{}
	if err := sonic.Unmarshal(resp.Body(), parsed); err != nil {
		return nil, err
	}
	if parsed.Code != 200 {
		return nil, fmt.Errorf("playlist detail code: %d", parsed.Code)
	}
	return &parsed.Playlist, nil
}

func (neteaseCtx *NetEaseContext) getDetailByIDs(ctx context.Context, musicIDs []int) (*MusicDetail, error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("songIDs", joinSongIDs(musicIDs)))
	defer span.End()

	cleanIDs := make([]int, 0, len(musicIDs))
	for _, id := range musicIDs {
		if id == 0 {
			continue
		}
		cleanIDs = append(cleanIDs, id)
	}
	if len(cleanIDs) == 0 {
		return &MusicDetail{}, nil
	}

	resp, err := xhttp.HttpClient.R().
		SetFormDataFromValues(
			map[string][]string{
				"ids": {joinSongIDs(cleanIDs)},
			},
		).
		SetCookies(neteaseCtx.cookies).
		SetQueryParam("timestamp", fmt.Sprint(time.Now().UnixNano())).
		Post(NetEaseAPIBaseURL + "/song/detail")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("song detail http status: %d", resp.StatusCode())
	}

	musicDetail := &MusicDetail{}
	if err := sonic.Unmarshal(resp.Body(), musicDetail); err != nil {
		return nil, err
	}
	return musicDetail, nil
}

func (neteaseCtx *NetEaseContext) GetLyrics(ctx context.Context, songID int) (lyrics string, lyricsURL string) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Int("songID", songID))
	defer span.End()

	// Try cache first
	if cachedLyrics, cachedURL := getLyricsCache().GetText(ctx, "lyrics/"+songIDString(songID)+".txt"); cachedLyrics != "" {
		return cachedLyrics, cachedURL
	}

	resp, err := xhttp.HttpClient.R().
		SetFormDataFromValues(
			map[string][]string{
				"id": {songIDString(songID)},
			},
		).
		SetCookies(neteaseCtx.cookies).
		SetQueryParam("timestamp", fmt.Sprint(time.Now().UnixNano())).
		Post(NetEaseAPIBaseURL + "/lyric")
	if err != nil {
		logs.L().Ctx(ctx).Warn("Unknown error", zap.Error(err))
		return
	}
	if resp.StatusCode() != 200 {
		logs.L().Ctx(ctx).Warn("lyric request failed", zap.Int("status_code", resp.StatusCode()))
		return
	}
	searchLyrics := &SearchLyrics{}
	body := string(resp.Body())
	err = sonic.UnmarshalString(body, searchLyrics)
	if err != nil {
		logs.L().Ctx(ctx).Warn("Unknown error", zap.Error(err))
		return
	}

	// Upload raw JSON for reference
	lyricsURL, err = miniodal.New(miniodal.Internal).Upload(ctx).
		WithContentType(xmodel.ContentTypePlainText.String()).
		WithData([]byte(body)).
		Do(cacheBucket, "lyrics/"+songIDString(songID)+".json", minio.PutObjectOptions{}).PreSignURL()
	if err != nil {
		logs.L().Ctx(ctx).Warn("[PreUploadMusic] Get minio url failed...", zap.Error(err))
	}

	lyricsMerged := mergeLyrics(searchLyrics.Lrc.Lyric, searchLyrics.Tlyric.Lyric)

	// Cache the parsed lyrics as text
	getLyricsCache().SetText(ctx, "lyrics/"+songIDString(songID)+".txt", lyricsMerged)

	return lyricsMerged, lyricsURL
}

var lyricsRepattern = regexp2.MustCompile(`\[(?P<time>.*)\](?P<line>.*)`, regexp2.RE2)

func mergeLyrics(lyrics, translatedLyrics string) string {
	lyricsMap := map[string]string{}
	lines := strings.Split(lyrics, "\n")
	for _, line := range lines {
		match, err := lyricsRepattern.FindStringMatch(line)
		if err != nil {
			continue
		}
		if match != nil {
			if lyric := match.GroupByName("line").String(); lyric != "" {
				lyricsMap[match.GroupByName("time").String()] = lyric + "\n"
			}
		}
	}
	for _, translatedLine := range strings.Split(translatedLyrics, "\n") {
		match, err := lyricsRepattern.FindStringMatch(translatedLine)
		if err != nil {
			continue
		}
		if match != nil {
			if lyric := match.GroupByName("line").String(); lyric != "" {
				lyricsMap[match.GroupByName("time").String()] += lyric + "\n"
			}
		}
	}
	resStr := ""
	type lineStruct struct {
		time string
		line string
	}
	lyricsLines := make([]*lineStruct, 0)
	for time, line := range lyricsMap {
		if line == "" {
			continue
		}
		lyricsLines = append(lyricsLines, &lineStruct{
			time, line,
		})
	}
	slices.SortFunc(lyricsLines, func(i, j *lineStruct) int {
		return cmp.Compare(i.time, j.time)
	})
	for _, line := range lyricsLines {
		resStr += line.line + "\n"
	}
	return resStr
}

func (neteaseCtx *NetEaseContext) AsyncGetSearchRes(ctx context.Context, searchRes SearchMusic) (result []*SearchMusicItem, err error) {
	songIDs := make([]int, 0, len(searchRes.Result.Songs))
	for idx := range searchRes.Result.Songs {
		if searchRes.Result.Songs[idx].ID == 0 {
			continue
		}
		songIDs = append(songIDs, searchRes.Result.Songs[idx].ID)
	}

	urlByID := make(map[int]string, len(songIDs))
	missingIDs := make([]int, 0, len(songIDs))

	for _, id := range songIDs {
		if u := tryGetMusicURLFromMinio(ctx, id); u != "" {
			urlByID[id] = u
		} else {
			missingIDs = append(missingIDs, id)
		}
	}

	if len(missingIDs) > 0 {
		fetched, err := neteaseCtx.GetMusicURLByIDs(ctx, missingIDs...)
		if err != nil {
			return nil, err
		}
		for k, v := range fetched {
			urlByID[k] = v
		}
	}

	imageKeyByID := asyncUploadPics(ctx, searchRes)
	result = make([]*SearchMusicItem, 0, len(searchRes.Result.Songs))
	for idx := range searchRes.Result.Songs {
		song := &searchRes.Result.Songs[idx]
		if song.ID == 0 {
			continue
		}
		result = append(result, &SearchMusicItem{
			ID:         song.ID,
			Name:       song.Name,
			ArtistName: genArtistName(song),
			PicURL:     song.Al.PicURL,
			ImageKey:   imageKeyByID[song.ID],
			SongURL:    urlByID[song.ID],
		})
	}
	return result, nil
}

// SearchMusicByKeyWord 通过关键字搜索歌曲
func (neteaseCtx *NetEaseContext) SearchMusicByKeyWord(ctx context.Context, keywords ...string) (result []*SearchMusicItem, err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("keywords").StringSlice(keywords))
	defer span.End()

	resp1, err := xhttp.HttpClient.R().
		SetFormDataFromValues(
			map[string][]string{
				"limit":    {"5"},
				"type":     {"1"},
				"keywords": {strings.Join(keywords, " ")},
			},
		).
		SetCookies(neteaseCtx.cookies).
		SetQueryParam("timestamp", fmt.Sprint(time.Now().UnixNano())).
		Post(NetEaseAPIBaseURL + "/cloudsearch")
	if err != nil {
		logs.L().Ctx(ctx).Warn("Unknown error", zap.Error(err))
		return nil, err
	}
	if resp1.StatusCode() != 200 {
		return nil, fmt.Errorf("search music http status: %d", resp1.StatusCode())
	}

	searchRes := SearchMusic{}
	if err = sonic.Unmarshal(resp1.Body(), &searchRes); err != nil {
		logs.L().Ctx(ctx).Warn("Unmarshal search result failed", zap.Error(err))
		return nil, err
	}

	return neteaseCtx.AsyncGetSearchRes(ctx, searchRes)
}

func (neteaseCtx *NetEaseContext) SearchMusicByPlaylist(ctx context.Context, playlistID string) (playListDetail *PlaylistDetail, result []*SearchMusicItem, err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("playlistID").String(strings.TrimSpace(playlistID)))
	defer span.End()

	playListDetail, err = neteaseCtx.getPlaylistDetail(ctx, playlistID)
	if err != nil {
		return nil, nil, err
	}
	trackIDs := make([]int, 0, len(playListDetail.TrackIDs))
	for _, track := range playListDetail.TrackIDs {
		if track.ID == 0 {
			continue
		}
		trackIDs = append(trackIDs, track.ID)
	}
	if len(trackIDs) == 0 {
		return nil, nil, fmt.Errorf("playlist %s has no tracks", strings.TrimSpace(playlistID))
	}

	musicDetail, err := neteaseCtx.getDetailByIDs(ctx, trackIDs)
	if err != nil {
		return nil, nil, err
	}
	if musicDetail == nil || len(musicDetail.Songs) == 0 {
		return nil, nil, fmt.Errorf("playlist %s returned no song details", strings.TrimSpace(playlistID))
	}

	type playlistSongSummary struct {
		Name       string
		ArtistName string
		PicURL     string
	}
	songByID := make(map[int]playlistSongSummary, len(musicDetail.Songs))
	for idx := range musicDetail.Songs {
		song := &musicDetail.Songs[idx]
		if song.ID == 0 {
			continue
		}
		artistNames := make([]string, 0, len(song.Ar))
		for _, artist := range song.Ar {
			if strings.TrimSpace(artist.Name) == "" {
				continue
			}
			artistNames = append(artistNames, artist.Name)
		}
		songByID[song.ID] = playlistSongSummary{
			Name:       song.Name,
			ArtistName: strings.Join(artistNames, ", "),
			PicURL:     song.Al.PicURL,
		}
	}

	urlByID, err := neteaseCtx.GetMusicURLs(ctx, defaultMusicURLBatchSize, trackIDs...)
	if err != nil {
		return nil, nil, err
	}

	result = make([]*SearchMusicItem, 0, len(trackIDs))
	for _, id := range trackIDs {
		song, ok := songByID[id]
		if !ok {
			continue
		}
		result = append(result, &SearchMusicItem{
			ID:         id,
			Name:       song.Name,
			ArtistName: song.ArtistName,
			PicURL:     song.PicURL,
			SongURL:    urlByID[id],
		})
	}
	return
}

// SearchAlbumByKeyWord  通过关键字搜索歌曲
//
//	@receiver neteaseCtx *NetEaseContext
//	@param ctx context.Context
//	@param keywords ...string
//	@return result []*Album
//	@return err error
//	@author heyuhengmatt
//	@update 2024-08-07 08:46:58
func (neteaseCtx *NetEaseContext) SearchAlbumByKeyWord(ctx context.Context, keywords ...string) (result []*Album, err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("keywords").StringSlice(keywords))
	defer span.End()

	resp1, err := xhttp.HttpClient.R().
		SetFormDataFromValues(
			map[string][]string{
				"limit":    {"5"},
				"type":     {"10"},
				"keywords": {strings.Join(keywords, " ")},
			},
		).
		SetCookies(neteaseCtx.cookies).
		SetQueryParam("timestamp", fmt.Sprint(time.Now().UnixNano())).
		Post(NetEaseAPIBaseURL + "/cloudsearch")
	if err != nil {
		logs.L().Ctx(ctx).Warn("Unknown error", zap.Error(err))
		return nil, err
	}
	if resp1.StatusCode() != 200 {
		return nil, fmt.Errorf("search album http status: %d", resp1.StatusCode())
	}

	searchRes := searchAlbumResult{}
	err = sonic.Unmarshal(resp1.Body(), &searchRes)
	if err != nil {
		logs.L().Ctx(ctx).Warn("Unknown error", zap.Error(err))
		return nil, err
	}
	result = searchRes.Result.Albums
	return
}

// GetAlbumDetail 通过关键字搜索歌曲
//
//	@receiver neteaseCtx *NetEaseContext
//	@param ctx context.Context
//	@param albumID
//	@return result []*Album
//	@return err error
func (neteaseCtx *NetEaseContext) GetAlbumDetail(ctx context.Context, albumID string) (result *AlbumDetail, err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("albumID").String(albumID))
	defer span.End()

	resp1, err := xhttp.HttpClient.R().
		SetFormDataFromValues(
			map[string][]string{
				"id": {albumID},
			},
		).
		SetCookies(neteaseCtx.cookies).
		SetQueryParam("timestamp", fmt.Sprint(time.Now().UnixNano())).
		Post(NetEaseAPIBaseURL + "/album")
	if err != nil {
		logs.L().Ctx(ctx).Warn("Unknown error", zap.Error(err))
		return nil, err
	}
	if resp1.StatusCode() != 200 {
		return nil, fmt.Errorf("album detail http status: %d", resp1.StatusCode())
	}

	searchRes := AlbumDetail{}
	err = sonic.Unmarshal(resp1.Body(), &searchRes)
	if err != nil {
		logs.L().Ctx(ctx).Warn("Unknown error", zap.Error(err))
		return nil, err
	}

	return &searchRes, err
}

type uploadedPic struct {
	imageKey string
	musicID  int
}

func asyncUploadPics(ctx context.Context, musicInfos SearchMusic) map[int]string {
	ctx, span := otel.Start(ctx)
	defer span.End()
	var (
		c   = make(chan uploadedPic, len(musicInfos.Result.Songs))
		wg  = &sync.WaitGroup{}
		m   = make(map[int]string, len(musicInfos.Result.Songs))
		sem = make(chan struct{}, defaultPictureConcurrency)
	)
	go func(ctx context.Context) {
		defer close(c)
		defer wg.Wait()

		for _, m := range musicInfos.Result.Songs {
			if m.ID == 0 || strings.TrimSpace(m.Al.PicURL) == "" {
				continue
			}
			wg.Add(1)
			go func(song Song) {
				sem <- struct{}{}
				defer func() { <-sem }()
				uploadPicWorker(ctx, wg, song.Al.PicURL, song.ID, c)
			}(m)
		}
	}(ctx)
	for res := range c {
		m[res.musicID] = res.imageKey
	}
	return m
}

func uploadPicWorker(ctx context.Context, wg *sync.WaitGroup, url string, musicID int, c chan uploadedPic) bool {
	defer wg.Done()
	imgKey, _, err := larkimg.UploadPicAllinOne(ctx, url, musicID, true)
	if err != nil {
		logs.L().Ctx(ctx).Warn("upload pic to lark error", zap.Error(err))
		return true
	}
	c <- uploadedPic{imageKey: imgKey, musicID: musicID}
	return false
}

func (neteaseCtx *NetEaseContext) GetComment(ctx context.Context, commentType CommentType, id string) (res *CommentResult, err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Key("commentType").String(string(commentType)))
	defer span.End()

	resp, err := xhttp.HttpClient.R().
		SetCookies(neteaseCtx.cookies).
		SetFormDataFromValues(
			map[string][]string{
				"id":       {id},
				"pageSize": {"1"},
				"pageNo":   {"1"},
				"sortType": {"2"},
				"type":     {string(commentType)},
			},
		).
		SetQueryParam("timestamp", fmt.Sprint(time.Now().UnixNano())).
		Post(NetEaseAPIBaseURL + "/comment/new/")
	if err != nil {
		logs.L().Ctx(ctx).Warn("Unknown error", zap.Error(err))
		return nil, err
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("comment http status: %d", resp.StatusCode())
	}
	res = &CommentResult{}
	err = sonic.Unmarshal(resp.Body(), res)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func genArtistName(song *Song) (artistName string) {
	artistList := make([]string, 0, len(song.Ar))
	for _, s := range song.Ar {
		if s.Name == "" {
			continue
		}
		artistList = append(artistList, s.Name)
	}
	return strings.Join(artistList, ", ")
}

// GetNewRecommendMusic 获得新的推荐歌曲
//
//	@receiver ctx
//	@return res
//	@return err
func (neteaseCtx *NetEaseContext) GetNewRecommendMusic() (res []SearchMusicItem, err error) {
	resp, err := xhttp.HttpClient.R().SetFormDataFromValues(
		map[string][]string{
			"limit": {"5"},
		},
	).Post(NetEaseAPIBaseURL + "/personalized/newsong")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("recommend music http status: %d", resp.StatusCode())
	}

	music := &GlobRecommendMusicRes{}
	if err = sonic.Unmarshal(resp.Body(), music); err != nil {
		return nil, err
	}
	musicIDs := make([]int, 0, len(music.Result))
	nameByID := make(map[int]string, len(music.Result))
	artistNameByID := make(map[int]string, len(music.Result))
	picURLByID := make(map[int]string, len(music.Result))
	for _, result := range music.Result {
		var ArtistName string
		for _, name := range result.Song.Artists {
			if ArtistName != "" {
				ArtistName += ","
			}
			ArtistName += name.Name
		}
		musicIDs = append(musicIDs, result.Song.ID)
		nameByID[result.Song.ID] = result.Song.Name
		artistNameByID[result.Song.ID] = ArtistName
		picURLByID[result.Song.ID] = result.PicURL
	}
	urlByID, err := neteaseCtx.GetMusicURLs(context.Background(), defaultMusicURLBatchSize, musicIDs...)
	if err != nil {
		return nil, err
	}
	for _, musicID := range musicIDs {
		songURL := urlByID[musicID]
		if songURL == "" {
			continue
		}
		res = append(res, SearchMusicItem{
			ID:         musicID,
			Name:       nameByID[musicID],
			ArtistName: artistNameByID[musicID],
			PicURL:     picURLByID[musicID],
			SongURL:    songURL,
		})
	}
	return
}
