package neteaseapi

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestMusicSearchCardAddsConfiguredWebEntry(t *testing.T) {
	for _, appID := range []string{"cli_music_test", ""} {
		t.Run("app="+appID, func(t *testing.T) {
			t.Setenv("MUSIC_WEB_APP_ID", appID)
			vars := &larktpl.MusicListCardVars{
				CardBaseVars: larktpl.CardBaseVars{
					JaegerTraceInfo: "Trace", RefreshTime: "2026-11-16 12:00:00",
					FirstReplyCost: "1s", FinalPatchCost: "2s",
				},
				Query: "[中文 & query]", PageInfoText: "第 1 / 2 页", HasNext: true,
				NextPageVal: map[string]string{"action": cardactionproto.ActionMusicListPage, "page": "2"},
				ObjectList1: []*larktpl.MusicListCardItem{{
					Field1: "**歌曲**\n**歌手**", ElementID: "42", ButtonInfo: "点击播放",
					ButtonVal: map[string]string{"action": cardactionproto.ActionMusicPlay, "id": "42"},
				}},
			}
			t.Setenv("MUSIC_WEB_ORIGIN", "")
			original := BuildMusicListRawCard(context.Background(), vars)
			t.Setenv("MUSIC_WEB_ORIGIN", "https://music.example.test:2443")
			card := BuildMusicListRawCard(context.Background(), vars)
			body := card["body"].(map[string]any)
			elements := body["elements"].([]any)
			remaining := make([]any, 0, len(elements))
			buttons := 0
			for _, element := range elements {
				entry := element.(map[string]any)
				text, _ := entry["text"].(map[string]any)
				if entry["tag"] != "button" || text["content"] != "打开播放器" {
					remaining = append(remaining, element)
					continue
				}
				buttons++
				behaviors := entry["behaviors"].([]any)
				if len(behaviors) != 1 || behaviors[0].(map[string]any)["type"] != "open_url" {
					t.Fatal("the optional player entry must be a user-clicked open_url button")
				}
				entryURL, err := url.Parse(behaviors[0].(map[string]any)["default_url"].(string))
				if err != nil {
					t.Fatal(err)
				}
				target := entryURL
				if appID != "" {
					if entryURL.Host != "applink.feishu.cn" || entryURL.Query().Get("mode") != "sidebar" || entryURL.Query().Has("path") {
						t.Fatal("player button must request the configured Feishu sidebar")
					}
					target, err = url.Parse(entryURL.Query().Get("lk_target_url"))
					if err != nil {
						t.Fatal(err)
					}
				}
				if target.Scheme != "https" || target.Host != "music.example.test:2443" || target.Path != "/play" || target.RawQuery != "" {
					t.Fatal("player entry must open /play without moving the search or choosing a song")
				}
			}
			if buttons != 1 {
				t.Fatalf("expected one optional player button, got %d", buttons)
			}
			body["elements"] = remaining
			if !reflect.DeepEqual(card, original) {
				t.Fatal("adding the player button changed existing search rows, playback callbacks or pagination")
			}
		})
	}
}

func TestMusicSearchCardKeepsLegacyOutputWhenWebUnconfigured(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "")
	raw, err := json.Marshal(BuildMusicListRawCard(context.Background(), &larktpl.MusicListCardVars{Query: "[query]"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "打开播放器") {
		t.Fatal("unconfigured web entry leaked into card")
	}
}
