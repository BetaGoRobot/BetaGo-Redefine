package neteaseapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	musicListResolveConcurrency = 4
	defaultMusicListPageSize    = 5
	maxMusicListPageSize        = 100
	musicListStreamPatchWindow  = 500 * time.Millisecond
)

var activeMusicListStreams sync.Map

type MusicListScene string

const (
	MusicListSceneSongSearch     MusicListScene = "song_search"
	MusicListSceneAlbumSearch    MusicListScene = "album_search"
	MusicListScenePlaylistDetail MusicListScene = "playlist_detail"
	MusicListSceneAlbumDetail    MusicListScene = "album_detail"
)

type musicItemTransFunc[T any] func(context.Context, *T) *SearchMusicItem

type (
	MusicListCardSender  func(context.Context, *larktpl.TemplateCardContent) (string, error)
	MusicListCardPatcher func(context.Context, string, *larktpl.TemplateCardContent) error
)

type MusicListRequest struct {
	Scene    MusicListScene
	Query    string
	Page     int
	PageSize int
}

type musicListCardData struct {
	request      MusicListRequest
	resourceType CommentType
	displayTitle string
	items        []*SearchMusicItem
}

type musicListButtonConfig struct {
	ButtonName string
	ActionName string
}

type musicListLineState struct {
	item *SearchMusicItem
	line larktpl.MusicListCardItem
}

type musicListCardRenderer struct {
	mu           sync.RWMutex
	resourceType CommentType
	request      MusicListRequest
	displayTitle string
	lines        []*musicListLineState
}

func MusicItemNoTrans(_ context.Context, item *SearchMusicItem) *SearchMusicItem {
	return item
}

func MusicItemTransAlbum(_ context.Context, albumItem *Album) *SearchMusicItem {
	if albumItem == nil {
		return nil
	}
	return &SearchMusicItem{
		ID:         int(albumItem.ID),
		Name:       "[" + albumItem.Type + "] " + albumItem.Name,
		PicURL:     albumItem.PicURL,
		ArtistName: albumItem.Artist.Name,
	}
}

func BuildMusicListCard[T any](ctx context.Context, resList []*T, transFunc musicItemTransFunc[T], resourceType CommentType, keywords ...string) (*larktpl.TemplateCardContent, error) {
	ctx, span := otel.Start(ctx)
	defer span.End()

	items := collectMusicItems(ctx, resList, transFunc)
	req := MusicListRequest{
		Page:     1,
		PageSize: max(1, len(items)),
	}
	renderer := newMusicListCardRenderer(musicListCardData{
		request:      req,
		resourceType: resourceType,
		displayTitle: strings.Join(keywords, " "),
		items:        items,
	})
	renderer.resolveCurrentPage(ctx)
	return renderer.Card(ctx), nil
}

func BuildMusicListCardForRequest(ctx context.Context, req MusicListRequest) (*larktpl.TemplateCardContent, error) {
	ctx, span := otel.Start(ctx)
	defer span.End()

	data, err := loadMusicListCardData(ctx, req)
	if err != nil {
		return nil, err
	}

	renderer := newMusicListCardRenderer(data)
	renderer.resolveCurrentPage(ctx)
	return renderer.Card(ctx), nil
}

func StreamMusicListCardForRequest(ctx context.Context, req MusicListRequest, send MusicListCardSender, patch MusicListCardPatcher) error {
	ctx, span := otel.Start(ctx)
	defer span.End()

	data, err := loadMusicListCardData(ctx, req)
	if err != nil {
		return err
	}

	renderer := newMusicListCardRenderer(data)
	_, cancel := context.WithCancel(ctx)
	defer cancel()

	err = renderer.streamCurrentPage(context.WithoutCancel(ctx), send, patch, cancel)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func newMusicListCardRenderer(data musicListCardData) *musicListCardRenderer {
	req := normalizeMusicListRequest(data.request)
	lines := make([]*musicListLineState, 0, len(data.items))
	for _, item := range data.items {
		if item == nil {
			continue
		}
		lines = append(lines, &musicListLineState{
			item: item,
			line: newMusicListCardLoadingItem(item),
		})
	}

	totalPages := totalMusicListPages(len(lines), req.PageSize)
	req.Page = clampMusicListPage(req.Page, totalPages)

	return &musicListCardRenderer{
		resourceType: data.resourceType,
		request:      req,
		displayTitle: firstNonEmpty(data.displayTitle, req.Query),
		lines:        lines,
	}
}

func (r *musicListCardRenderer) Card(ctx context.Context) *larktpl.TemplateCardContent {
	return larktpl.NewCardContentWithData(ctx, larktpl.AlbumListTemplate, r.vars())
}

func (r *musicListCardRenderer) vars() *larktpl.MusicListCardVars {
	currentPage := r.currentPage()
	totalPages := r.totalPages()
	return &larktpl.MusicListCardVars{
		ObjectList1:  r.snapshotCurrentPageLines(),
		Query:        formatMusicListQuery(r.displayTitle),
		PageInfoText: fmt.Sprintf("第 %d / %d 页", currentPage, totalPages),
		CurrentPage:  currentPage,
		TotalPages:   totalPages,
		HasPrev:      currentPage <= 1,
		HasNext:      currentPage >= totalPages,
		PrevPageVal:  r.pagePayload(currentPage - 1),
		NextPageVal:  r.pagePayload(currentPage + 1),
	}
}

func (r *musicListCardRenderer) streamCurrentPage(ctx context.Context, send MusicListCardSender, patch MusicListCardPatcher, cancel context.CancelFunc) error {
	streamGuard := &musicListStreamGuard{}
	defer streamGuard.Release(context.Background())

	return r.streamCurrentPageVars(ctx, streamGuard, func(vars *larktpl.MusicListCardVars, messageID string) (string, error) {
		if err := ctx.Err(); err != nil {
			return messageID, err
		}
		card := larktpl.NewCardContentWithData(ctx, larktpl.AlbumListTemplate, vars)
		if messageID == "" {
			nextMessageID, err := send(ctx, card)
			if err != nil {
				return "", err
			}
			streamGuard.Register(ctx, nextMessageID, cancel)
			return nextMessageID, nil
		}
		if err := streamGuard.EnsureActive(ctx); err != nil {
			return messageID, err
		}
		return messageID, patch(ctx, messageID, card)
	})
}

func (r *musicListCardRenderer) streamCurrentPageVars(ctx context.Context, streamGuard *musicListStreamGuard, emit func(*larktpl.MusicListCardVars, string) (string, error)) error {
	pageStates := r.currentPageStates()
	messageID, err := emit(r.vars(), "")
	if err != nil {
		return err
	}
	if strings.TrimSpace(messageID) == "" {
		return errors.New("empty message id for music list card")
	}
	if len(pageStates) == 0 {
		return nil
	}

	resolvedCh := make(chan struct{}, len(pageStates))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(musicListResolveConcurrency)
	for _, state := range pageStates {
		group.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			r.resolveLine(groupCtx, state)
			if err := groupCtx.Err(); err != nil {
				return err
			}
			select {
			case resolvedCh <- struct{}{}:
				return nil
			case <-groupCtx.Done():
				return groupCtx.Err()
			}
		})
	}

	groupDone := make(chan error, 1)
	go func() {
		groupDone <- group.Wait()
		close(resolvedCh)
	}()

	patchTicker := time.NewTicker(musicListStreamPatchWindow)
	defer patchTicker.Stop()

	pendingPatch := false
	sentResolvedPatch := false
	flushPatch := func() error {
		if !pendingPatch {
			return nil
		}
		emit(r.vars(), messageID)
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-resolvedCh:
			if !ok {
				if err := <-groupDone; err != nil {
					return err
				}
				return flushPatch()
			}
			pendingPatch = true
			if !sentResolvedPatch {
				if err := flushPatch(); err != nil {
					return err
				}
				sentResolvedPatch = true
			}
		case <-patchTicker.C:
			if err := flushPatch(); err != nil {
				return err
			}
		}
	}
}

func (r *musicListCardRenderer) resolveCurrentPage(ctx context.Context) {
	pageStates := r.currentPageStates()
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(musicListResolveConcurrency)
	for _, state := range pageStates {
		group.Go(func() error {
			r.resolveLine(groupCtx, state)
			return nil
		})
	}
	_ = group.Wait()
}

func (r *musicListCardRenderer) resolveLine(ctx context.Context, state *musicListLineState) {
	if state == nil || state.item == nil {
		return
	}

	item := state.item
	imageKey := strings.TrimSpace(item.ImageKey)
	if imageKey == "" && strings.TrimSpace(item.PicURL) != "" {
		uploadedKey, _, err := larkimg.UploadPicAllinOne(ctx, item.PicURL, item.ID, true)
		if err != nil {
			logs.L().Ctx(ctx).Warn("upload music list picture failed", zap.Int("music_id", item.ID), zap.Error(err))
		} else {
			imageKey = uploadedKey
		}
	}

	commentContent := ""
	commentTime := ""
	comment, err := NetEaseGCtx.GetComment(ctx, r.resourceType, strconv.Itoa(item.ID))
	if err != nil {
		logs.L().Ctx(ctx).Error("GetComment Error", zap.Int("music_id", item.ID), zap.Error(err))
	} else if comment != nil && len(comment.Data.Comments) > 0 {
		commentContent = trimMusicComment(comment.Data.Comments[0].Content)
		commentTime = comment.Data.Comments[0].TimeStr
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	line := newMusicListCardItem(item, musicListButtonConfigFor(r.resourceType))
	if imageKey != "" {
		state.item.ImageKey = imageKey
		line.Field2 = larktpl.ImageKeyRef{ImgKey: imageKey}
	}
	line.Field3 = commentContent
	line.CommentTime = commentTime
	line.Filled = true
	state.line = line
}

func (r *musicListCardRenderer) snapshotCurrentPageLines() []*larktpl.MusicListCardItem {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pageStates := r.currentPageStatesLocked()
	lines := make([]*larktpl.MusicListCardItem, 0, len(pageStates))
	for idx := range pageStates {
		lineCopy := pageStates[idx].line
		lines = append(lines, &lineCopy)
	}
	return lines
}

func (r *musicListCardRenderer) currentPageStates() []*musicListLineState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*musicListLineState(nil), r.currentPageStatesLocked()...)
}

func (r *musicListCardRenderer) currentPageStatesLocked() []*musicListLineState {
	start, end := currentMusicListRange(len(r.lines), r.request.Page, r.request.PageSize)
	if start >= end {
		return nil
	}
	return r.lines[start:end]
}

func (r *musicListCardRenderer) totalPages() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return totalMusicListPages(len(r.lines), r.request.PageSize)
}

func (r *musicListCardRenderer) currentPage() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return clampMusicListPage(r.request.Page, totalMusicListPages(len(r.lines), r.request.PageSize))
}

func (r *musicListCardRenderer) pagePayload(targetPage int) map[string]string {
	if targetPage <= 0 {
		return nil
	}
	totalPages := r.totalPages()
	if targetPage > totalPages || r.request.Scene == "" || strings.TrimSpace(r.request.Query) == "" {
		return nil
	}
	return cardaction.New(cardaction.ActionMusicListPage).
		WithValue(cardaction.SceneField, string(r.request.Scene)).
		WithValue(cardaction.QueryField, r.request.Query).
		WithValue(cardaction.PageField, strconv.Itoa(targetPage)).
		WithValue(cardaction.PageSizeField, strconv.Itoa(r.request.PageSize)).
		Payload()
}

func loadMusicListCardData(ctx context.Context, req MusicListRequest) (musicListCardData, error) {
	req = normalizeMusicListRequest(req)
	if strings.TrimSpace(req.Query) == "" {
		return musicListCardData{}, errors.New("music list query is required")
	}

	switch req.Scene {
	case MusicListSceneSongSearch:
		items, err := NetEaseGCtx.SearchMusicByKeyWord(ctx, req.Query)
		if err != nil {
			return musicListCardData{}, err
		}
		return musicListCardData{
			request:      req,
			resourceType: CommentTypeSong,
			displayTitle: req.Query,
			items:        items,
		}, nil
	case MusicListSceneAlbumSearch:
		albumList, err := NetEaseGCtx.SearchAlbumByKeyWord(ctx, req.Query)
		if err != nil {
			return musicListCardData{}, err
		}
		return musicListCardData{
			request:      req,
			resourceType: CommentTypeAlbum,
			displayTitle: req.Query,
			items:        collectMusicItems(ctx, albumList, MusicItemTransAlbum),
		}, nil
	case MusicListScenePlaylistDetail:
		detail, items, err := NetEaseGCtx.SearchMusicByPlaylist(ctx, req.Query)
		if err != nil {
			return musicListCardData{}, err
		}
		displayTitle := req.Query
		if detail != nil {
			displayTitle = firstNonEmpty(detail.Name, req.Query)
		}
		return musicListCardData{
			request:      req,
			resourceType: CommentTypeSong,
			displayTitle: displayTitle,
			items:        items,
		}, nil
	case MusicListSceneAlbumDetail:
		albumDetail, err := NetEaseGCtx.GetAlbumDetail(ctx, req.Query)
		if err != nil {
			return musicListCardData{}, err
		}
		title, items, err := albumDetailToSearchMusicItems(ctx, albumDetail)
		if err != nil {
			return musicListCardData{}, err
		}
		return musicListCardData{
			request:      req,
			resourceType: CommentTypeSong,
			displayTitle: firstNonEmpty(title, req.Query),
			items:        items,
		}, nil
	default:
		return musicListCardData{}, fmt.Errorf("unsupported music list scene: %s", req.Scene)
	}
}

func albumDetailToSearchMusicItems(ctx context.Context, albumDetail *AlbumDetail) (string, []*SearchMusicItem, error) {
	if albumDetail == nil || len(albumDetail.Songs) == 0 {
		return "", nil, nil
	}

	musicIDs := make([]int, 0, len(albumDetail.Songs))
	for idx := range albumDetail.Songs {
		if albumDetail.Songs[idx].ID == 0 {
			continue
		}
		musicIDs = append(musicIDs, albumDetail.Songs[idx].ID)
	}
	urlFetcher, ok := NetEaseGCtx.(interface {
		GetMusicURLs(context.Context, int, ...int) (map[int]string, error)
	})
	if !ok {
		return "", nil, errors.New("netease provider does not support GetMusicURLs")
	}
	urlByID, err := urlFetcher.GetMusicURLs(ctx, defaultMusicURLBatchSize, musicIDs...)
	if err != nil {
		return "", nil, err
	}

	result := make([]*SearchMusicItem, 0, len(albumDetail.Songs))
	title := strings.TrimSpace(albumDetail.Album.Name)
	for idx := range albumDetail.Songs {
		song := &albumDetail.Songs[idx]
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
		result = append(result, &SearchMusicItem{
			ID:         song.ID,
			Name:       song.Name,
			ArtistName: strings.Join(artistNames, ", "),
			PicURL:     song.Al.PicURL,
			SongURL:    urlByID[song.ID],
		})
	}
	return title, result, nil
}

func collectMusicItems[T any](ctx context.Context, resList []*T, transFunc musicItemTransFunc[T]) []*SearchMusicItem {
	if len(resList) == 0 {
		return nil
	}

	items := make([]*SearchMusicItem, len(resList))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(musicListResolveConcurrency)
	for idx, item := range resList {
		idx, item := idx, item
		group.Go(func() error {
			items[idx] = transFunc(groupCtx, item)
			return nil
		})
	}
	_ = group.Wait()

	filtered := make([]*SearchMusicItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func musicListButtonConfigFor(resourceType CommentType) musicListButtonConfig {
	switch resourceType {
	case CommentTypeSong:
		return musicListButtonConfig{
			ButtonName: "点击播放",
			ActionName: cardaction.ActionMusicPlay,
		}
	case CommentTypeAlbum:
		return musicListButtonConfig{
			ButtonName: "查看专辑",
			ActionName: cardaction.ActionMusicAlbum,
		}
	default:
		return musicListButtonConfig{
			ButtonName: "点击查看",
			ActionName: "null",
		}
	}
}

func newMusicListCardItem(item *SearchMusicItem, button musicListButtonConfig) larktpl.MusicListCardItem {
	buttonInfo := button.ButtonName
	if button.ActionName == cardaction.ActionMusicPlay && strings.TrimSpace(item.SongURL) == "" {
		buttonInfo = "歌曲无效"
	}

	cardItem := larktpl.MusicListCardItem{
		Field1:     genMusicTitle(item.Name, item.ArtistName),
		Field2:     larktpl.ImageKeyRef{ImgKey: item.ImageKey},
		ButtonInfo: buttonInfo,
		ElementID:  strconv.Itoa(item.ID),
		ButtonVal:  cardaction.New(button.ActionName).WithID(strconv.Itoa(item.ID)).Payload(),
	}

	// 始终添加"播放语音"按钮
	cardItem.Button2Info = "播放语音"
	cardItem.Button2Val = cardaction.New(cardaction.ActionMusicVoicePlay).WithID(strconv.Itoa(item.ID)).Payload()

	return cardItem
}

func newMusicListCardLoadingItem(item *SearchMusicItem) larktpl.MusicListCardItem {
	elementID := ""
	if item != nil && item.ID != 0 {
		elementID = strconv.Itoa(item.ID)
	}
	return larktpl.MusicListCardItem{
		Field1: "加载中",
		Field2: larktpl.ImageKeyRef{
			ImgKey: "img_v3_02vo_8fa12381-e31b-4241-ad11-7afc7d81650g",
		},
		Field3:      "加载中",
		CommentTime: "加载中",
		ButtonInfo:  "加载中",
		Button2Info: "播放语音",
		ElementID:   elementID,
	}
}

func normalizeMusicListRequest(req MusicListRequest) MusicListRequest {
	req.Query = strings.TrimSpace(req.Query)
	req.Page = max(req.Page, 1)
	req.PageSize = normalizeMusicListPageSize(req.PageSize)
	return req
}

func normalizeMusicListPageSize(pageSize int) int {
	switch {
	case pageSize <= 0:
		return defaultMusicListPageSize
	case pageSize > maxMusicListPageSize:
		return maxMusicListPageSize
	default:
		return pageSize
	}
}

func totalMusicListPages(totalItems, pageSize int) int {
	if pageSize <= 0 {
		pageSize = defaultMusicListPageSize
	}
	if totalItems <= 0 {
		return 1
	}
	return (totalItems + pageSize - 1) / pageSize
}

func clampMusicListPage(page, totalPages int) int {
	if totalPages <= 0 {
		totalPages = 1
	}
	if page <= 0 {
		return 1
	}
	if page > totalPages {
		return totalPages
	}
	return page
}

func currentMusicListRange(totalItems, page, pageSize int) (int, int) {
	if totalItems <= 0 {
		return 0, 0
	}
	pageSize = normalizeMusicListPageSize(pageSize)
	totalPages := totalMusicListPages(totalItems, pageSize)
	page = clampMusicListPage(page, totalPages)
	start := (page - 1) * pageSize
	end := min(start+pageSize, totalItems)
	return start, end
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func formatMusicListQuery(title string) string {
	if strings.TrimSpace(title) == "" {
		return "[]"
	}
	return fmt.Sprintf("[%s]", title)
}

func trimMusicComment(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= 50 {
		return string(runes)
	}
	return string(runes[:50]) + "..."
}

func genMusicTitle(title, artist string) string {
	return fmt.Sprintf("**%s**\n**%s**", title, artist)
}
