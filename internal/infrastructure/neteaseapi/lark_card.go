package neteaseapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	musicListResolveConcurrency = 4
	defaultMusicListPageSize    = 5
	maxMusicListPageSize        = 100
	musicListStreamPatchWindow  = 500 * time.Millisecond

	musicListPaginationElementID = "music_list_pagination"
	musicListFooterElementID     = "music_list_footer"
	musicListLoadingImageKey     = "img_v3_02vo_8fa12381-e31b-4241-ad11-7afc7d81650g"
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

// MusicListCardSender sends a card and returns the message ID.
type MusicListCardSender func(ctx context.Context, cardData any) (messageID string, err error)

// MusicListCardUpdater updates a single element in a card by element_id with a streaming sequence.
type MusicListCardUpdater func(ctx context.Context, cardID, elementID string, sequence int, elementJSON string) error

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
	item    *SearchMusicItem
	element map[string]any
	filled  bool
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

func BuildMusicListCard[T any](ctx context.Context, resList []*T, transFunc musicItemTransFunc[T], resourceType CommentType, keywords ...string) (larkmsg.RawCard, error) {
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
	return renderer.RawCard(ctx), nil
}

func BuildMusicListCardForRequest(ctx context.Context, req MusicListRequest) (larkmsg.RawCard, error) {
	ctx, span := otel.Start(ctx)
	defer span.End()

	data, err := loadMusicListCardData(ctx, req)
	if err != nil {
		return nil, err
	}

	renderer := newMusicListCardRenderer(data)
	renderer.resolveCurrentPage(ctx)
	return renderer.RawCard(ctx), nil
}

func StreamMusicListCardForRequest(ctx context.Context, req MusicListRequest, send MusicListCardSender, update MusicListCardUpdater) error {
	ctx, span := otel.Start(ctx)
	defer span.End()

	data, err := loadMusicListCardData(ctx, req)
	if err != nil {
		return err
	}

	renderer := newMusicListCardRenderer(data)
	_, cancel := context.WithCancel(ctx)
	defer cancel()

	err = renderer.streamCurrentPage(context.WithoutCancel(ctx), send, update, cancel)
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
			item:    item,
			element: buildMusicListLoadingRow(item),
			filled:  false,
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

func (r *musicListCardRenderer) RawCard(ctx context.Context) larkmsg.RawCard {
	return r.buildRawCard(ctx)
}

func (r *musicListCardRenderer) buildRawCard(ctx context.Context) larkmsg.RawCard {
	r.mu.RLock()
	defer r.mu.RUnlock()

	currentPage := r.currentPageLocked()
	totalPages := r.totalPagesLocked()

	elements := make([]any, 0, len(r.lines)+2)

	start, end := currentMusicListRange(len(r.lines), r.request.Page, r.request.PageSize)
	for i := start; i < end; i++ {
		elements = append(elements, r.lines[i].element)
	}

	elements = append(elements, r.buildPaginationElement(currentPage, totalPages))
	elements = append(elements, r.buildFooterElement(ctx))

	return larkmsg.RawCard{
		"schema": "2.0",
		"config": map[string]any{
			"update_multi":   true,
			"enable_forward": false,
		},
		"header": map[string]any{
			"template": "blue",
			"title":    larkmsg.PlainText(formatMusicListQuery(r.displayTitle)),
		},
		"body": map[string]any{
			"direction":          "vertical",
			"horizontal_spacing": "8px",
			"vertical_spacing":   "8px",
			"padding":            "12px",
			"elements":           elements,
		},
	}
}

func (r *musicListCardRenderer) streamCurrentPage(ctx context.Context, send MusicListCardSender, update MusicListCardUpdater, cancel context.CancelFunc) error {
	streamGuard := &musicListStreamGuard{}
	defer streamGuard.Release(context.Background())

	// 1. Send initial card with loading placeholders
	messageID, err := send(ctx, r.buildRawCard(ctx))
	if err != nil {
		return err
	}
	if strings.TrimSpace(messageID) == "" {
		return errors.New("empty message id for music list card")
	}
	streamGuard.Register(ctx, messageID, cancel)

	// 2. Convert message_id to card_id for cardKit APIs
	cardID, err := larkmsg.IdConvert(ctx, messageID)
	if err != nil {
		logs.L().Ctx(ctx).Warn("id_convert failed, falling back to full patch", zap.String("message_id", messageID), zap.Error(err))
		return r.streamCurrentPageFallback(ctx, streamGuard, messageID, send)
	}

	// 3. Enable streaming mode
	var seq atomic.Int64
	seq.Store(time.Now().UnixMilli())

	if err := larkmsg.BatchUpdateCard(ctx, cardID, int(seq.Add(1)), `[{"action":"partial_update_setting","params":{"settings":"{\"config\":{\"streaming_mode\":true}}"}}]`); err != nil {
		logs.L().Ctx(ctx).Warn("enable streaming mode failed, falling back to full patch", zap.String("card_id", cardID), zap.Error(err))
		return r.streamCurrentPageFallback(ctx, streamGuard, messageID, send)
	}

	// 4. Resolve each line in parallel and update individually via cardKit
	pageStates := r.currentPageStates()
	if len(pageStates) == 0 {
		_ = larkmsg.BatchUpdateCard(ctx, cardID, int(seq.Add(1)), `[{"action":"partial_update_setting","params":{"settings":"{\"config\":{\"streaming_mode\":false}}"}}]`)
		return nil
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(musicListResolveConcurrency)

	for _, state := range pageStates {
		state := state
		group.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			r.resolveLine(groupCtx, state)

			if err := groupCtx.Err(); err != nil {
				return err
			}
			if err := streamGuard.EnsureActive(groupCtx); err != nil {
				return err
			}

			// Update this single element via cardKit
			elementJSON := mustMarshalElement(state.element)
			elementID := musicRowElementID(state.item)
			if elementID != "" && elementJSON != "" {
				if err := update(groupCtx, cardID, elementID, int(seq.Add(1)), elementJSON); err != nil {
					logs.L().Ctx(groupCtx).Warn("update card element failed", zap.String("element_id", elementID), zap.Error(err))
				}
			}
			return nil
		})
	}

	resolveErr := group.Wait()

	// 5. Disable streaming mode
	_ = larkmsg.BatchUpdateCard(context.WithoutCancel(ctx), cardID, int(seq.Add(1)), `[{"action":"partial_update_setting","params":{"settings":"{\"config\":{\"streaming_mode\":false}}"}}]`)

	return resolveErr
}

func (r *musicListCardRenderer) streamCurrentPageFallback(ctx context.Context, streamGuard *musicListStreamGuard, messageID string, send MusicListCardSender) error {
	pageStates := r.currentPageStates()
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
		cardData := r.buildRawCard(ctx)
		if _, err := send(ctx, cardData); err != nil {
			return err
		}
		pendingPatch = false
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
	var imageKey string
	var commentContent string
	var commentTime string

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		if strings.TrimSpace(item.ImageKey) != "" || strings.TrimSpace(item.PicURL) == "" {
			return nil
		}
		uploadedKey, _, err := larkimg.UploadPicAllinOne(gCtx, item.PicURL, item.ID, true)
		if err != nil {
			logs.L().Ctx(gCtx).Warn("upload music list picture failed", zap.Int("music_id", item.ID), zap.Error(err))
			return nil
		}
		imageKey = uploadedKey
		return nil
	})

	g.Go(func() error {
		comment, err := NetEaseGCtx.GetComment(gCtx, r.resourceType, strconv.Itoa(item.ID))
		if err != nil {
			logs.L().Ctx(gCtx).Error("GetComment Error", zap.Int("music_id", item.ID), zap.Error(err))
			return nil
		}
		if comment != nil && len(comment.Data.Comments) > 0 {
			commentContent = trimMusicComment(comment.Data.Comments[0].Content)
			commentTime = comment.Data.Comments[0].TimeStr
		}
		return nil
	})

	_ = g.Wait()

	if imageKey != "" {
		item.ImageKey = imageKey
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	state.element = buildMusicListFilledRow(item, r.resourceType, commentContent, commentTime)
	state.filled = true
}

// --- RawCard element builders ---

func musicRowElementID(item *SearchMusicItem) string {
	if item == nil || item.ID == 0 {
		return ""
	}
	return "m_" + strconv.Itoa(item.ID)
}

func buildMusicListLoadingRow(item *SearchMusicItem) map[string]any {
	elementID := musicRowElementID(item)
	imgCol := map[string]any{
		"tag":      "column",
		"width":    "auto",
		"elements": []any{buildImgElement(musicListLoadingImageKey)},
	}
	textCol := map[string]any{
		"tag":      "column",
		"width":    "weighted",
		"weight":   3,
		"elements": buildLoadingTextElements(),
	}
	row := map[string]any{
		"tag":                "column_set",
		"horizontal_spacing": "8px",
		"flex_mode":          "bisect",
		"columns":            []any{imgCol, textCol},
	}
	if elementID != "" {
		row["element_id"] = elementID
	}
	return row
}

func buildMusicListFilledRow(item *SearchMusicItem, resourceType CommentType, commentContent, commentTime string) map[string]any {
	elementID := musicRowElementID(item)
	buttonCfg := musicListButtonConfigFor(resourceType)

	buttonInfo := buttonCfg.ButtonName
	if buttonCfg.ActionName == cardaction.ActionMusicPlay && strings.TrimSpace(item.SongURL) == "" {
		buttonInfo = "歌曲无效"
	}

	imgKey := strings.TrimSpace(item.ImageKey)
	if imgKey == "" {
		imgKey = musicListLoadingImageKey
	}

	rightElements := []any{
		map[string]any{
			"tag":     "markdown",
			"content": genMusicTitle(item.Name, item.ArtistName),
		},
	}
	if commentContent != "" {
		rightElements = append(rightElements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "plain_text", "content": commentContent, "text_size": "notation", "text_color": "grey"},
		})
	}
	if commentTime != "" {
		rightElements = append(rightElements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "plain_text", "content": commentTime, "text_size": "notation", "text_color": "grey"},
		})
	}

	imgCol := map[string]any{
		"tag":      "column",
		"width":    "auto",
		"elements": []any{buildImgElement(imgKey)},
	}
	textCol := map[string]any{
		"tag":              "column",
		"width":            "weighted",
		"weight":           3,
		"elements":         rightElements,
		"vertical_spacing": "4px",
	}
	btnCol := map[string]any{
		"tag":      "column",
		"width":    "auto",
		"elements": []any{buildButtonElement(buttonInfo, buttonCfg.ActionName, item.ID), buildButtonElement("播放语音", cardaction.ActionMusicVoicePlay, item.ID)},
	}

	row := map[string]any{
		"tag":                "column_set",
		"horizontal_spacing": "8px",
		"flex_mode":          "bisect",
		"columns":            []any{imgCol, textCol, btnCol},
	}
	if elementID != "" {
		row["element_id"] = elementID
	}
	return row
}

func buildImgElement(imgKey string) map[string]any {
	return map[string]any{
		"tag":           "img",
		"img_key":       imgKey,
		"corner_radius": "4px",
	}
}

func buildLoadingTextElements() []any {
	return []any{
		map[string]any{
			"tag":     "markdown",
			"content": "加载中",
		},
		map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "plain_text", "content": "加载中", "text_size": "notation", "text_color": "grey"},
		},
	}
}

func buildButtonElement(label, actionName string, musicID int) map[string]any {
	return map[string]any{
		"tag":  "button",
		"text": map[string]any{"tag": "plain_text", "content": label},
		"type": "primary",
		"size": "tiny",
		"behaviors": []any{map[string]any{
			"type":  "callback",
			"value": larkmsg.StringMapToAnyMap(cardaction.New(actionName).WithID(strconv.Itoa(musicID)).Payload()),
		}},
	}
}

func (r *musicListCardRenderer) buildPaginationElement(currentPage, totalPages int) map[string]any {
	columns := make([]any, 0, 3)

	if currentPage > 1 {
		columns = append(columns, map[string]any{
			"tag":      "column",
			"width":    "auto",
			"elements": []any{buildPaginationButton("上一页", r.pagePayload(currentPage-1))},
		})
	}

	columns = append(columns, map[string]any{
		"tag":      "column",
		"width":    "weighted",
		"weight":   1,
		"elements": []any{map[string]any{"tag": "div", "text": map[string]any{"tag": "plain_text", "content": fmt.Sprintf("第 %d / %d 页", currentPage, totalPages), "text_align": "center"}}},
	})

	if currentPage < totalPages {
		columns = append(columns, map[string]any{
			"tag":      "column",
			"width":    "auto",
			"elements": []any{buildPaginationButton("下一页", r.pagePayload(currentPage+1))},
		})
	}

	return map[string]any{
		"tag":                "column_set",
		"element_id":        musicListPaginationElementID,
		"horizontal_spacing": "8px",
		"flex_mode":          "bisect",
		"columns":            columns,
	}
}

func buildPaginationButton(label string, payload map[string]string) map[string]any {
	if len(payload) == 0 {
		return map[string]any{
			"tag":      "button",
			"text":     map[string]any{"tag": "plain_text", "content": label},
			"type":     "default",
			"size":     "tiny",
			"disabled": true,
		}
	}
	return map[string]any{
		"tag":  "button",
		"text": map[string]any{"tag": "plain_text", "content": label},
		"type": "default",
		"size": "tiny",
		"behaviors": []any{map[string]any{
			"type":  "callback",
			"value": larkmsg.StringMapToAnyMap(payload),
		}},
	}
}

func (r *musicListCardRenderer) buildFooterElement(ctx context.Context) map[string]any {
	traceID := ""
	spanCtx := oteltrace.SpanContextFromContext(ctx)
	if spanCtx.HasTraceID() {
		traceID = spanCtx.TraceID().String()
	}
	traceURL := ""
	if traceID != "" {
		traceURL = utils.GenTraceURL(traceID)
	}

	footerColumns := make([]any, 0, 3)

	footerColumns = append(footerColumns, map[string]any{
		"tag":      "column",
		"width":    "weighted",
		"weight":   1,
		"elements": []any{map[string]any{"tag": "div", "text": map[string]any{"tag": "plain_text", "content": "更新于 " + time.Now().In(utils.UTC8Loc()).Format(time.DateTime), "text_size": "notation", "text_color": "grey"}}},
	})

	footerColumns = append(footerColumns, map[string]any{
		"tag":   "column",
		"width": "auto",
		"elements": []any{map[string]any{
			"tag":  "button",
			"text": map[string]any{"tag": "plain_text", "content": "撤回"},
			"type": "danger_filled",
			"size": "tiny",
			"behaviors": []any{map[string]any{
				"type":  "callback",
				"value": map[string]any{cardaction.ActionField: cardaction.ActionCardWithdraw},
			}},
		}},
	})

	if traceURL != "" {
		footerColumns = append(footerColumns, map[string]any{
			"tag":   "column",
			"width": "auto",
			"elements": []any{map[string]any{
				"tag":  "button",
				"text": map[string]any{"tag": "plain_text", "content": "Trace"},
				"type": "primary",
				"size": "tiny",
				"behaviors": []any{map[string]any{
					"type":        "open_url",
					"default_url": traceURL,
				}},
			}},
		})
	}

	return map[string]any{
		"tag":                "column_set",
		"element_id":        musicListFooterElementID,
		"horizontal_spacing": "8px",
		"columns":            footerColumns,
	}
}

// --- State accessors ---

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

func (r *musicListCardRenderer) totalPagesLocked() int {
	return totalMusicListPages(len(r.lines), r.request.PageSize)
}

func (r *musicListCardRenderer) currentPageLocked() int {
	return clampMusicListPage(r.request.Page, totalMusicListPages(len(r.lines), r.request.PageSize))
}

func (r *musicListCardRenderer) pagePayload(targetPage int) map[string]string {
	if targetPage <= 0 {
		return nil
	}
	totalPages := r.totalPagesLocked()
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

// --- Data loading ---

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

// --- Helpers ---

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

func mustMarshalElement(element map[string]any) string {
	b, err := sonic.Marshal(element)
	if err != nil {
		return ""
	}
	return string(b)
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
