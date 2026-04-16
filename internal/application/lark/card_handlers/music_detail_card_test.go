package cardhandlers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func useWorkspaceConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

func TestBuildMusicDetailRawCardUsesSchemaV2WithAudioRegion(t *testing.T) {
	useWorkspaceConfigPath(t)

	card := BuildMusicDetailRawCard(context.Background(), MusicDetailCardView{
		Title:        "稻香",
		Subtitle:     "周杰伦",
		Lyrics:       "对这个世界如果你有太多的抱怨",
		PlayerURL:    "https://example.com/player",
		ImageKey:     "img_v3_001",
		AudioFileKey: "file_v3_audio_001",
		AudioID:      "music_42",
		RefreshTime:  "2026-04-16 18:00:00",
		FullLyricsButton: map[string]string{
			cardactionproto.ActionField: cardactionproto.ActionMusicLyrics,
			cardactionproto.IDField:     "42",
		},
		RefreshID: map[string]string{
			cardactionproto.ActionField: cardactionproto.ActionMusicRefresh,
			cardactionproto.IDField:     "42",
		},
	})

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"schema":"2.0"`) {
		t.Fatalf("expected schema v2 card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"Play"`) || !strings.Contains(jsonStr, `"default_url":"https://example.com/player"`) {
		t.Fatalf("expected play button open_url in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, cardactionproto.ActionMusicLyrics) {
		t.Fatalf("expected full lyrics callback in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, cardactionproto.ActionMusicRefresh) {
		t.Fatalf("expected refresh callback in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"tag":"audio"`) || !strings.Contains(jsonStr, `file_v3_audio_001`) || !strings.Contains(jsonStr, `"show_progress_bar":true`) || !strings.Contains(jsonStr, `"show_time":true`) {
		t.Fatalf("expected inline audio region in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"img_key":"img_v3_001"`) {
		t.Fatalf("expected uploaded image key in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, "卡片更新时间：2026-04-16 18:00:00") {
		t.Fatalf("expected refresh time footer in card json: %s", jsonStr)
	}
}

func TestShouldRetryMusicCardWithoutAudio(t *testing.T) {
	if !shouldRetryMusicCardWithoutAudio(assertErr("msg:Failed to create card content, ext=ErrCode: 300260; ErrMsg: audio elem don't support forward; ,code:230099"), "file_v3_audio_001") {
		t.Fatal("expected unsupported audio error to trigger fallback")
	}
	if shouldRetryMusicCardWithoutAudio(assertErr("random network error"), "file_v3_audio_001") {
		t.Fatal("did not expect unrelated error to trigger fallback")
	}
	if shouldRetryMusicCardWithoutAudio(assertErr("ErrCode: 300260"), "") {
		t.Fatal("did not expect fallback when audio region missing")
	}
}

func assertErr(msg string) error {
	return errString(msg)
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func TestBuildMusicDetailRawCardOmitsAudioRegionWhenUnavailable(t *testing.T) {
	useWorkspaceConfigPath(t)

	card := BuildMusicDetailRawCard(context.Background(), MusicDetailCardView{
		Title:       "夜曲",
		Subtitle:    "周杰伦",
		Lyrics:      "一群嗜血的蚂蚁 被腐肉所吸引",
		PlayerURL:   "https://example.com/player",
		ImageKey:    "img_v3_002",
		RefreshTime: "2026-04-16 18:00:01",
	})

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if strings.Contains(jsonStr, `"tag":"audio"`) || strings.Contains(jsonStr, `"show_progress_bar":true`) {
		t.Fatalf("did not expect audio region when audio element missing: %s", jsonStr)
	}
}
