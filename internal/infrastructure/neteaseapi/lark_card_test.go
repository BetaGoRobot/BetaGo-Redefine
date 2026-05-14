package neteaseapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
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

func TestBuildMusicListRawCardRendersConcreteCardJSON(t *testing.T) {
	card := BuildMusicListRawCard(context.Background(), &larktpl.MusicListCardVars{
		CardBaseVars: larktpl.CardBaseVars{
			JaegerTraceInfo: "Trace",
			JaegerTraceURL:  "https://trace.example",
			WithdrawInfo:    "撤回卡片",
			WithdrawTitle:   "撤回本条消息",
			WithdrawConfirm: "确认撤回？",
			WithdrawObject:  larktpl.WithDrawObj{Action: cardactionproto.ActionCardWithdraw},
			RefreshTime:     "2026-05-14 12:00:00",
		},
		ObjectList1: []*larktpl.MusicListCardItem{{
			Field1:      "**稻香**\n**周杰伦**",
			Field2:      larktpl.ImageKeyRef{ImgKey: "img_test"},
			Field3:      "不要这么容易就想放弃",
			CommentTime: "刚刚",
			ButtonInfo:  "点击播放",
			ElementID:   "1859245776",
			ButtonVal:   map[string]string{cardactionproto.ActionField: cardactionproto.ActionMusicPlay, cardactionproto.IDField: "1859245776"},
		}},
		Query:        "[稻香]",
		PageInfoText: "第 1 / 1 页",
		HasPrev:      true,
		HasNext:      true,
	})

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal raw card: %v", err)
	}
	jsonStr := string(raw)
	for _, forbidden := range []string{`"tag":"repeat"`, "${", "object_list_1"} {
		if strings.Contains(jsonStr, forbidden) {
			t.Fatalf("raw card should not contain template marker %q: %s", forbidden, jsonStr)
		}
	}
	for _, want := range []string{`"schema":"2.0"`, `"img_key":"img_test"`, `"content":"[稻香]的检索结果"`, `"content":"ID:1859245776"`} {
		if !strings.Contains(jsonStr, want) {
			t.Fatalf("raw card missing %q: %s", want, jsonStr)
		}
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
	if err := renderer.streamCurrentPageVars(context.Background(), &musicListStreamGuard{}, func(vars *larktpl.MusicListCardVars, messageID string, sequence int) (string, error) {
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

func TestMusicListStreamDispatchesPatchesWithoutWaitingForPreviousPatch(t *testing.T) {
	previousProvider := NetEaseGCtx
	NetEaseGCtx = fakeMusicListProvider{}
	defer func() { NetEaseGCtx = previousProvider }()
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
	previousCardBuilder := musicListRawCardBuilder
	musicListRawCardBuilder = func(ctx context.Context, vars *larktpl.MusicListCardVars) larkmsg.RawCard {
		return larkmsg.RawCard{"schema": "2.0"}
	}
	defer func() { musicListRawCardBuilder = previousCardBuilder }()

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

	started := make(chan int, 5)
	release := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- renderer.streamCurrentPage(context.Background(),
			func(context.Context, larkmsg.RawCard, int) (string, error) {
				return "om_msg_1", nil
			},
			func(ctx context.Context, msgID string, card larkmsg.RawCard, sequence int) error {
				started <- sequence
				<-release
				return nil
			},
			func() {},
		)
	}()

	first := waitMusicListPatchSequence(t, started)
	second := waitMusicListPatchSequence(t, started)
	if second <= first {
		t.Fatalf("expected increasing patch sequence, first=%d second=%d", first, second)
	}

	select {
	case err := <-errCh:
		t.Fatalf("stream returned before patch goroutines were released: %v", err)
	default:
	}

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("stream current page: %v", err)
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

func waitMusicListPatchSequence(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case seq := <-ch:
		return seq
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for patch dispatch")
		return 0
	}
}

func TestNewMusicListCardItemMarksUnavailableSong(t *testing.T) {
	item := newMusicListCardItem(&SearchMusicItem{
		ID:         7,
		Name:       "无版权歌曲",
		ArtistName: "测试歌手",
	}, musicListButtonConfigFor(CommentTypeSong))

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
