package neteaseapi

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestMusicListRendererIncludesPaginationPayload(t *testing.T) {
	renderer := newMusicListCardRenderer(musicListCardData{
		request: MusicListRequest{
			Scene:    MusicListScenePlaylistDetail,
			Query:    "3778678",
			Page:     2,
			PageSize: 2,
		},
		resourceType: CommentTypeSong,
		displayTitle: "我喜欢的音乐",
		items: []*SearchMusicItem{
			{ID: 1, Name: "s1", ArtistName: "a1", SongURL: "u1", ImageKey: "img1"},
			{ID: 2, Name: "s2", ArtistName: "a2", SongURL: "u2", ImageKey: "img2"},
			{ID: 3, Name: "s3", ArtistName: "a3", SongURL: "u3", ImageKey: "img3"},
			{ID: 4, Name: "s4", ArtistName: "a4", SongURL: "u4", ImageKey: "img4"},
			{ID: 5, Name: "s5", ArtistName: "a5", SongURL: "u5", ImageKey: "img5"},
		},
	})

	vars := renderer.vars()

	if got := len(vars.ObjectList1); got != 2 {
		t.Fatalf("expected 2 visible items, got %d", got)
	}
	if vars.ObjectList1[0].Field1 != "加载中" || vars.ObjectList1[1].Field1 != "加载中" {
		t.Fatalf("expected initial loading placeholders, got %#v", vars.ObjectList1)
	}
	if got := vars.PageInfoText; got != "第 2 / 3 页" {
		t.Fatalf("expected page info text, got %#v", got)
	}
	if vars.PrevPageVal[cardactionproto.ActionField] != cardactionproto.ActionMusicListPage {
		t.Fatalf("expected prev action %q, got %#v", cardactionproto.ActionMusicListPage, vars.PrevPageVal)
	}
	if vars.PrevPageVal[cardactionproto.PageField] != "1" {
		t.Fatalf("expected prev page to be 1, got %#v", vars.PrevPageVal[cardactionproto.PageField])
	}
	if vars.NextPageVal[cardactionproto.PageField] != "3" {
		t.Fatalf("expected next page to be 3, got %#v", vars.NextPageVal[cardactionproto.PageField])
	}
	if vars.NextPageVal[cardactionproto.SceneField] != string(MusicListScenePlaylistDetail) {
		t.Fatalf("expected scene %q, got %#v", MusicListScenePlaylistDetail, vars.NextPageVal[cardactionproto.SceneField])
	}
}

func TestMusicListStreamRevealsMonotonicProgress(t *testing.T) {
	previousProvider := NetEaseGCtx
	NetEaseGCtx = fakeMusicListProvider{}
	defer func() { NetEaseGCtx = previousProvider }()

	renderer := newMusicListCardRenderer(musicListCardData{
		request: MusicListRequest{
			Scene:    MusicListSceneSongSearch,
			Query:    "稻香",
			Page:     1,
			PageSize: 5,
		},
		resourceType: CommentTypeSong,
		displayTitle: "稻香",
		items: []*SearchMusicItem{
			{ID: 1, Name: "s1", ArtistName: "a1", SongURL: "u1", ImageKey: "img1"},
			{ID: 2, Name: "s2", ArtistName: "a2", SongURL: "u2", ImageKey: "img2"},
			{ID: 3, Name: "s3", ArtistName: "a3", SongURL: "u3", ImageKey: "img3"},
			{ID: 4, Name: "s4", ArtistName: "a4", SongURL: "u4", ImageKey: "img4"},
			{ID: 5, Name: "s5", ArtistName: "a5", SongURL: "u5", ImageKey: "img5"},
		},
	})

	var resolvedCounts []int
	if err := renderer.streamCurrentPageVars(context.Background(), &musicListStreamGuard{}, func(vars *larktpl.MusicListCardVars, messageID string) (string, error) {
		resolved := 0
		for _, item := range vars.ObjectList1 {
			if item != nil && item.Field1 != "加载中" {
				resolved++
			}
		}
		resolvedCounts = append(resolvedCounts, resolved)
		if messageID == "" {
			return "om_msg_1", nil
		}
		return messageID, nil
	}); err != nil {
		t.Fatalf("stream current page: %v", err)
	}
	if len(resolvedCounts) < 2 {
		t.Fatalf("expected at least placeholder and final updates, got %v", resolvedCounts)
	}
	if resolvedCounts[0] != 0 {
		t.Fatalf("expected first update to be placeholder-only, got %v", resolvedCounts)
	}
	if resolvedCounts[len(resolvedCounts)-1] != 5 {
		t.Fatalf("expected final update to resolve all items, got %v", resolvedCounts)
	}
	last := -1
	for _, got := range resolvedCounts {
		if got < last {
			t.Fatalf("expected monotonic progress, got %v", resolvedCounts)
		}
		if got < 0 || got > 5 {
			t.Fatalf("expected resolved count in [0,5], got %v", resolvedCounts)
		}
		last = got
	}
}

func TestMusicListStreamGuardCancelsViaRedisLease(t *testing.T) {
	s, rdb := setupMusicListTestRedis(t)
	defer s.Close()

	previousRedisClient := redis_dal.RedisClient
	redis_dal.RedisClient = rdb
	defer func() { redis_dal.RedisClient = previousRedisClient }()
	previousIdentity := currentMusicListStreamIdentity
	currentMusicListStreamIdentity = func() botidentity.Identity {
		return botidentity.Identity{AppID: "cli_test_app", BotOpenID: "ou_test_bot"}
	}
	defer func() { currentMusicListStreamIdentity = previousIdentity }()
	clearMusicListTestStreams()

	guard := &musicListStreamGuard{}
	guard.Register(context.Background(), "om_msg_test", func() {})

	if err := guard.EnsureActive(context.Background()); err != nil {
		t.Fatalf("guard should be active before cancel, got %v", err)
	}

	// 模拟回调打到其他实例: 当前进程没有本地 cancel 注册，只剩下 Redis lease。
	activeMusicListStreams.Delete("om_msg_test")
	CancelMusicListStream(context.Background(), "om_msg_test")

	if err := guard.EnsureActive(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected remote cancel to invalidate guard, got %v", err)
	}
}

func TestNewMusicListCardItemMarksUnavailableSong(t *testing.T) {
	item := newMusicListCardItem(&SearchMusicItem{
		ID:         7,
		Name:       "无版权歌曲",
		ArtistName: "测试歌手",
	}, musicListButtonConfigFor(CommentTypeSong, false))

	if item.ButtonInfo != "歌曲无效" {
		t.Fatalf("expected unavailable song label, got %q", item.ButtonInfo)
	}
}

type fakeMusicListProvider struct {
	noopProvider
}

func (fakeMusicListProvider) GetComment(context.Context, CommentType, string) (*CommentResult, error) {
	return nil, nil
}

func setupMusicListTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()

	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	return s, redis.NewClient(&redis.Options{Addr: s.Addr()})
}

func clearMusicListTestStreams() {
	activeMusicListStreams.Range(func(key, _ any) bool {
		activeMusicListStreams.Delete(key)
		return true
	})
}
