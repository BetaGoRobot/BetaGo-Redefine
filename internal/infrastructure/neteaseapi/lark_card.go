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
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
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
	musicListStreamPatchWindow  = 100 * time.Millisecond
	musicListLoadingImageKey    = "img_v3_02vo_8fa12381-e31b-4241-ad11-7afc7d81650g"
)

var (
	activeMusicListStreams  sync.Map
	musicListRawCardBuilder = BuildMusicListRawCard
	musicListImageUploader  = defaultMusicListImageUploader
	musicListImageKeyCache  sync.Map
	musicListCommentCache   sync.Map
)

type musicListComment struct {
	Content string
	Time    string
}

type musicListImageAsset struct {
	ImageKey string
	OSSURL   string
}

type musicListImageUploadFunc func(context.Context, string, int) (string, string, error)

func defaultMusicListImageUploader(ctx context.Context, picURL string, musicID int) (string, string, error) {
	return larkimg.UploadPicAllinOne(ctx, picURL, musicID, true)
}

func EnsureMusicImageKey(ctx context.Context, musicID int, picURL string) (string, error) {
	asset, err := EnsureMusicImageAsset(ctx, musicID, picURL)
	if err != nil {
		return "", err
	}
	return asset.ImageKey, nil
}

func EnsureMusicImageAsset(ctx context.Context, musicID int, picURL string) (musicListImageAsset, error) {
	picURL = strings.TrimSpace(picURL)
	if picURL == "" {
		return musicListImageAsset{}, nil
	}
	cacheKey := musicListImageCacheKey(musicID, picURL)
	if cached, ok := musicListImageKeyCache.Load(cacheKey); ok {
		return cached.(musicListImageAsset), nil
	}
	imageKey, ossURL, err := musicListImageUploader(ctx, picURL, musicID)
	if err != nil {
		return musicListImageAsset{}, err
	}
	asset := musicListImageAsset{
		ImageKey: strings.TrimSpace(imageKey),
		OSSURL:   strings.TrimSpace(ossURL),
	}
	if asset.ImageKey != "" {
		musicListImageKeyCache.Store(cacheKey, asset)
	}
	return asset, nil
}

func EnsureMusicComment(ctx context.Context, commentType CommentType, id string) (musicListComment, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return musicListComment{}, nil
	}
	cacheKey := string(commentType) + ":" + id
	if cached, ok := musicListCommentCache.Load(cacheKey); ok {
		return cached.(musicListComment), nil
	}
	comment, err := NetEaseGCtx.GetComment(ctx, commentType, id)
	if err != nil {
		return musicListComment{}, err
	}
	resolved := musicListComment{}
	if comment != nil && len(comment.Data.Comments) > 0 {
		resolved.Content = trimMusicComment(comment.Data.Comments[0].Content)
		resolved.Time = comment.Data.Comments[0].TimeStr
	}
	musicListCommentCache.Store(cacheKey, resolved)
	return resolved, nil
}

func musicListImageCacheKey(musicID int, picURL string) string {
	return strconv.Itoa(musicID) + ":" + strings.TrimSpace(picURL)
}

func clearMusicListResourceCache() {
	musicListImageKeyCache = sync.Map{}
	musicListCommentCache = sync.Map{}
}

type MusicListScene string

const (
	MusicListSceneSongSearch     MusicListScene = "song_search"
	MusicListSceneAlbumSearch    MusicListScene = "album_search"
	MusicListScenePlaylistDetail MusicListScene = "playlist_detail"
	MusicListSceneAlbumDetail    MusicListScene = "album_detail"
)

type musicItemTransFunc[T any] func(context.Context, *T) *SearchMusicItem

type (
	MusicListCardSender  func(context.Context, larkmsg.RawCard, int) (string, error)
	MusicListCardPatcher func(context.Context, string, larkmsg.RawCard, int) error
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
			line: newMusicListCardLoadingItem(item, musicListButtonConfigFor(data.resourceType)),
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

func (r *musicListCardRenderer) RawCard(ctx context.Context) larkmsg.RawCard {
	return musicListRawCardBuilder(ctx, r.vars())
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
	ctx, span := otel.Start(ctx)
	defer span.End()

	streamGuard := &musicListStreamGuard{}
	defer streamGuard.Release(context.Background())

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		asyncErr error
	)
	recordAsyncErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		defer errMu.Unlock()
		if asyncErr == nil {
			asyncErr = err
		}
	}
	waitAsync := func() error {
		wg.Wait()
		errMu.Lock()
		defer errMu.Unlock()
		return asyncErr
	}

	err := r.streamCurrentPageVars(ctx, streamGuard, func(vars *larktpl.MusicListCardVars, messageID string, sequence int) (string, error) {
		if err := ctx.Err(); err != nil {
			return messageID, err
		}
		card := musicListRawCardBuilder(ctx, vars)
		if messageID == "" {
			nextMessageID, err := send(ctx, card, sequence)
			if err != nil {
				return "", err
			}
			streamGuard.Register(ctx, nextMessageID, cancel)
			return nextMessageID, nil
		}
		if err := streamGuard.EnsureActive(ctx); err != nil {
			return messageID, err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordAsyncErr(patch(ctx, messageID, card, sequence))
		}()
		return messageID, nil
	})
	if err != nil {
		return err
	}
	return waitAsync()
}

func (r *musicListCardRenderer) streamCurrentPageVars(ctx context.Context, streamGuard *musicListStreamGuard, emit func(*larktpl.MusicListCardVars, string, int) (string, error)) error {
	pageStates := r.currentPageStates()
	nextSeq := newMusicListStreamSequence()
	messageID, err := emit(r.vars(), "", nextSeq())
	if err != nil {
		return err
	}
	if strings.TrimSpace(messageID) == "" {
		return errors.New("empty message id for music list card")
	}
	if len(pageStates) == 0 {
		return nil
	}

	resolvedCh := make(chan struct{}, len(pageStates)*3)
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(musicListResolveConcurrency)
	for _, state := range pageStates {
		state := state
		group.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			r.resolveLine(groupCtx, state, func() {
				select {
				case resolvedCh <- struct{}{}:
				case <-groupCtx.Done():
				}
			})
			return groupCtx.Err()
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
		pendingPatch = false
		_, err := emit(r.vars(), messageID, nextSeq())
		return err
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

func newMusicListStreamSequence() func() int {
	// Sequence 1 is reserved for enabling CardKit streaming before content updates.
	seq := 1
	return func() int {
		seq++
		return seq
	}
}

func (r *musicListCardRenderer) resolveCurrentPage(ctx context.Context) {
	pageStates := r.currentPageStates()
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(musicListResolveConcurrency)
	for _, state := range pageStates {
		state := state
		group.Go(func() error {
			r.resolveLine(groupCtx, state, nil)
			return nil
		})
	}
	_ = group.Wait()
}

func (r *musicListCardRenderer) resolveLine(ctx context.Context, state *musicListLineState, notify func()) {
	if state == nil || state.item == nil {
		return
	}

	item := state.item
	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		imageKey, err := EnsureMusicImageKey(groupCtx, item.ID, item.PicURL)
		if err != nil {
			logs.L().Ctx(groupCtx).Warn("resolve music list picture failed", zap.Int("music_id", item.ID), zap.Error(err))
			return nil
		}
		if imageKey != "" {
			r.updateLineImage(state, imageKey)
			if notify != nil {
				notify()
			}
		}
		return nil
	})

	group.Go(func() error {
		comment, err := EnsureMusicComment(groupCtx, r.resourceType, strconv.Itoa(item.ID))
		if err != nil {
			logs.L().Ctx(groupCtx).Error("resolve music list comment failed", zap.Int("music_id", item.ID), zap.Error(err))
			return nil
		}
		r.updateLineComment(state, comment)
		if notify != nil {
			notify()
		}
		return nil
	})

	_ = group.Wait()
	r.markLineFilled(state)
	if notify != nil {
		notify()
	}
}

func (r *musicListCardRenderer) updateLineImage(state *musicListLineState, imageKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state == nil || state.item == nil {
		return
	}
	state.item.ImageKey = imageKey
	state.line.Field2 = larktpl.ImageKeyRef{ImgKey: imageKey}
}

func (r *musicListCardRenderer) updateLineComment(state *musicListLineState, comment musicListComment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state == nil {
		return
	}
	state.line.Field3 = comment.Content
	state.line.CommentTime = comment.Time
}

func (r *musicListCardRenderer) markLineFilled(state *musicListLineState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state == nil {
		return
	}
	state.line.Filled = true
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
		title, items, err := albumDetailToSearchMusicItems(albumDetail)
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

func albumDetailToSearchMusicItems(albumDetail *AlbumDetail) (string, []*SearchMusicItem, error) {
	if albumDetail == nil || len(albumDetail.Songs) == 0 {
		return "", nil, nil
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
	cardItem := larktpl.MusicListCardItem{
		Field1:     genMusicTitle(item.Name, item.ArtistName),
		Field2:     larktpl.ImageKeyRef{ImgKey: item.ImageKey},
		ButtonInfo: button.ButtonName,
		ElementID:  strconv.Itoa(item.ID),
		ButtonVal:  cardaction.New(button.ActionName).WithID(strconv.Itoa(item.ID)).Payload(),
	}

	// 始终添加"播放语音"按钮
	cardItem.Button2Info = "播放语音"
	cardItem.Button2Val = cardaction.New(cardaction.ActionMusicVoicePlay).WithID(strconv.Itoa(item.ID)).Payload()

	return cardItem
}

func newMusicListCardLoadingItem(item *SearchMusicItem, button musicListButtonConfig) larktpl.MusicListCardItem {
	cardItem := newMusicListCardItem(item, button)
	if strings.TrimSpace(cardItem.Field2.ImgKey) == "" {
		cardItem.Field2 = larktpl.ImageKeyRef{ImgKey: musicListLoadingImageKey}
	}
	cardItem.Field3 = "加载中"
	cardItem.CommentTime = "加载中"
	return cardItem
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
