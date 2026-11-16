package neteaseapi

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
)

func TestMusicSearchCardAddsConfiguredWebEntry(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "https://music.example.test:2443")
	t.Setenv("MUSIC_WEB_APP_ID", "cli_music_test")
	card := BuildMusicListRawCard(context.Background(), &larktpl.MusicListCardVars{Query: "[中文 & query]"})
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"网页搜歌", "浏览器打开", "applink.feishu.cn", "music.example.test:2443/search?q="} {
		if !strings.Contains(text, want) {
			t.Fatalf("card entry missing %s", want)
		}
	}
	content := card["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)["content"].(string)
	match := regexp.MustCompile(`\]\((https://applink\.feishu\.cn/[^)]+)\)`).FindStringSubmatch(content)
	if len(match) != 2 {
		t.Fatal("missing concrete search AppLink")
	}
	app, err := url.Parse(match[1])
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(app.Query().Get("lk_target_url"))
	if err != nil {
		t.Fatal(err)
	}
	if app.Query().Has("path") || app.Query().Get("mode") != "appCenter" || target.Host != "music.example.test:2443" || target.Path != "/search" || target.Query().Get("q") != "中文 & query" {
		t.Fatal("card search AppLink did not preserve the actual target URL")
	}
}

func TestMusicSearchCardKeepsLegacyOutputWhenWebUnconfigured(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "")
	raw, err := json.Marshal(BuildMusicListRawCard(context.Background(), &larktpl.MusicListCardVars{Query: "[query]"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "网页搜歌") {
		t.Fatal("unconfigured web entry leaked into card")
	}
}
