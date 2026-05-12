package neteaseapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
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

	card := renderer.RawCard(context.Background())
	cardJSON, _ := json.Marshal(card)
	cardStr := string(cardJSON)

	// Verify page info text
	if !contains(cardStr, "第 2 / 3 页") {
		t.Fatalf("expected page info text '第 2 / 3 页', got %s", cardStr)
	}

	// Verify prev/next pagination buttons exist with correct action
	if !contains(cardStr, cardactionproto.ActionMusicListPage) {
		t.Fatalf("expected pagination action %q in card", cardactionproto.ActionMusicListPage)
	}

	// Verify scene is present
	if !contains(cardStr, string(MusicListScenePlaylistDetail)) {
		t.Fatalf("expected scene %q in card", MusicListScenePlaylistDetail)
	}

	// Verify loading placeholders
	if !contains(cardStr, "加载中") {
		t.Fatalf("expected loading placeholder in card")
	}

	// Verify element_ids on rows (page 2, size 2 → items 3,4)
	if !contains(cardStr, "m_3") || !contains(cardStr, "m_4") {
		t.Fatalf("expected element_ids m_3 and m_4 in card, got %s", cardStr)
	}
}

func TestMusicListStreamFallbackRevealsMonotonicProgress(t *testing.T) {
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
	if err := renderer.streamCurrentPageFallback(context.Background(), &musicListStreamGuard{}, "om_msg_1", func(ctx context.Context, cardData any) (string, error) {
		cardJSON, _ := json.Marshal(cardData)
		cardStr := string(cardJSON)

		// Count how many rows are resolved (have music title markdown)
		resolved := 0
		for _, name := range []string{"s1", "s2", "s3", "s4", "s5"} {
			if contains(cardStr, "**"+name+"**") {
				resolved++
			}
		}
		resolvedCounts = append(resolvedCounts, resolved)
		return "om_msg_1", nil
	}); err != nil {
		t.Fatalf("stream fallback: %v", err)
	}
	if len(resolvedCounts) < 2 {
		t.Fatalf("expected at least placeholder and final updates, got %v", resolvedCounts)
	}
	if resolvedCounts[len(resolvedCounts)-1] != 5 {
		t.Fatalf("expected final update to resolve all items, got %v", resolvedCounts)
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

	activeMusicListStreams.Delete("om_msg_test")
	CancelMusicListStream(context.Background(), "om_msg_test")

	if err := guard.EnsureActive(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected remote cancel to invalidate guard, got %v", err)
	}
}

func TestBuildMusicListFilledRowUnavailableSong(t *testing.T) {
	row := buildMusicListFilledRow(&SearchMusicItem{
		ID:         7,
		Name:       "无版权歌曲",
		ArtistName: "测试歌手",
	}, CommentTypeSong, "", "")

	rowJSON, _ := json.Marshal(row)
	rowStr := string(rowJSON)

	if !contains(rowStr, "歌曲无效") {
		t.Fatalf("expected unavailable song label, got %s", rowStr)
	}
}

func TestMusicRowElementID(t *testing.T) {
	tests := []struct {
		item *SearchMusicItem
		want string
	}{
		{&SearchMusicItem{ID: 123}, "m_123"},
		{&SearchMusicItem{ID: 0}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := musicRowElementID(tt.item); got != tt.want {
			t.Errorf("musicRowElementID(%v) = %q, want %q", tt.item, got, tt.want)
		}
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

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || jsonContains(s, substr))
}

func jsonContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
