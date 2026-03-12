package neteaseapi

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"

	"go.uber.org/zap"
)

type musicItemTransFunc[T any] func(context.Context, *T) *SearchMusicItem

func MusicItemNoTrans(_ context.Context, item *SearchMusicItem) *SearchMusicItem {
	return item
}

func MusicItemTransAlbum(ctx context.Context, albumItem *Album) *SearchMusicItem {
	if albumItem == nil {
		return nil
	}
	imageKey, _, err := larkimg.UploadPicAllinOne(ctx, albumItem.PicURL, int(albumItem.ID), true)
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to upload picture", zap.Error(err))
		return nil
	}
	return &SearchMusicItem{
		ID:         int(albumItem.ID),
		Name:       "[" + albumItem.Type + "] " + albumItem.Name,
		PicURL:     albumItem.PicURL,
		ArtistName: albumItem.Artist.Name,
		ImageKey:   imageKey,
	}
}

func MusicItemTransItemPic(ctx context.Context, itme *SearchMusicItem) *SearchMusicItem {
	if itme == nil {
		return nil
	}
	imageKey, _, err := larkimg.UploadPicAllinOne(ctx, itme.PicURL, itme.ID, true)
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to upload picture", zap.Error(err))
		return nil
	}
	itme.ImageKey = imageKey
	return itme
}

func BuildMusicListCard[T any](ctx context.Context, resList []*T, transFunc musicItemTransFunc[T], resourceType CommentType, keywords ...string) (content *larktpl.TemplateCardContent, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()

	res := make([]*SearchMusicItem, len(resList))
	// trans应该走异步
	wgTrans := &sync.WaitGroup{}
	for i, item := range resList {
		wgTrans.Add(1)
		go func(i int, item *T) {
			defer wgTrans.Done()
			res[i] = transFunc(ctx, item)
		}(i, item)
	}
	wgTrans.Wait()
	filtered := make([]*SearchMusicItem, 0, len(res))
	for _, item := range res {
		if item != nil {
			filtered = append(filtered, item)
		}
	}
	lines := make([]map[string]interface{}, len(filtered))
	var buttonName string
	var actionName string
	switch resourceType {
	case CommentTypeSong:
		buttonName = "点击播放"
		actionName = cardaction.ActionMusicPlay
	case CommentTypeAlbum:
		buttonName = "查看专辑"
		actionName = cardaction.ActionMusicAlbum
	default:
		buttonName = "点击查看"
		actionName = "null"
	}

	var (
		commentChan = make(chan map[string]interface{}, len(filtered))
		wg          = &sync.WaitGroup{}
	)
	go func() {
		defer close(commentChan)
		defer wg.Wait()
		for idx, item := range filtered {
			wg.Add(1)
			go func(idx int, item *SearchMusicItem) {
				defer wg.Done()
				comment, err := NetEaseGCtx.GetComment(ctx, resourceType, strconv.Itoa(item.ID))
				if err != nil {
					logs.L().Ctx(ctx).Error("GetComment Error", zap.Error(err))
				}
				line := map[string]interface{}{
					"idx":         idx,
					"field_1":     genMusicTitle(item.Name, item.ArtistName),
					"field_2":     map[string]any{"img_key": item.ImageKey},
					"button_info": buttonName,
					"element_id":  strconv.Itoa(item.ID),
					"button_val":  cardaction.New(actionName).WithID(strconv.Itoa(item.ID)).Payload(),
				}
				if comment != nil && len(comment.Data.Comments) != 0 {
					line["field_3"] = comment.Data.Comments[0].Content
					if runeSlice := []rune(comment.Data.Comments[0].Content); len(runeSlice) > 50 {
						line["field_3"] = string(runeSlice[:50]) + "..."
					}
					line["comment_time"] = comment.Data.Comments[0].TimeStr
				}
				if resourceType == CommentTypeSong && item.SongURL == "" { // 无效歌曲
					line["button_info"] = "歌曲无效"
				}
				commentChan <- line
			}(idx, item)
		}
	}()
	for line := range commentChan {
		idx := line["idx"].(int)
		lines[idx] = line
	}
	content = larktpl.NewCardContent(
		ctx,
		larktpl.AlbumListTemplate,
	).
		AddVariable("object_list_1", lines).
		AddVariable("query", fmt.Sprintf("[%s]", strings.Join(keywords, " ")))

	return
}

func genMusicTitle(title, artist string) string {
	return fmt.Sprintf("**%s**\n**%s**", title, artist)
}
