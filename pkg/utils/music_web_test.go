package utils

import (
	"net/url"
	"testing"
)

func TestMusicURLUsesConfiguredWebAppWithoutLegacyDescriptor(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "https://music.example.test:2443")
	t.Setenv("MUSIC_WEB_APP_ID", "cli_music_test")
	got, err := url.Parse(BuildMusicURL("https://legacy.example.test/private.json", "netease:42"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "applink.feishu.cn" || got.Path != "/client/web_app/open" || got.Query().Get("appId") != "cli_music_test" {
		t.Fatalf("configured app entry not selected: host=%s path=%s", got.Host, got.Path)
	}
	if got.Query().Has("path") || got.Query().Get("mode") != "window" {
		t.Fatal("web AppLink must use window and a complete lk_target_url, not path with query")
	}
	target, err := url.Parse(got.Query().Get("lk_target_url"))
	if err != nil {
		t.Fatal(err)
	}
	if target.Scheme != "https" || target.Host != "music.example.test:2443" || target.Path != "/play" || target.Query().Get("trackID") != "netease:42" || target.Query().Has("target") {
		t.Fatalf("unexpected target path: %s", target)
	}
}

func TestMusicURLWithoutWebConfigurationPreservesLegacyTarget(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "")
	t.Setenv("MUSIC_WEB_APP_ID", "")
	got, err := url.Parse(BuildMusicURL("https://legacy.example.test/song.json", "netease:42"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Query().Get("target") != "https://legacy.example.test/song.json" || got.Query().Get("trackID") != "netease:42" {
		t.Fatal("legacy target was lost")
	}
}

func TestMusicWebLinksEncodeSearchAndRejectUntrustedConfiguration(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "https://music.example.test:2443")
	t.Setenv("MUSIC_WEB_APP_ID", "cli_music_test")
	links := BuildMusicWebLinks("中文 & a=b?next=/private", "")
	browser, err := url.Parse(links.BrowserURL)
	if err != nil {
		t.Fatal(err)
	}
	if browser.Path != "/search" || browser.Query().Get("q") != "中文 & a=b?next=/private" || len(browser.Query()) != 1 {
		t.Fatal("search query not encoded as one value")
	}
	app, err := url.Parse(links.AppLink)
	if err != nil {
		t.Fatal(err)
	}
	if app.Query().Has("path") || app.Query().Get("mode") != "window" || app.Query().Get("lk_target_url") != links.BrowserURL {
		t.Fatal("complete browser URL was incorrectly nested in AppLink")
	}
	target, err := url.Parse(app.Query().Get("lk_target_url"))
	if err != nil {
		t.Fatal(err)
	}
	if target.Query().Get("q") != "中文 & a=b?next=/private" || len(target.Query()) != 1 {
		t.Fatal("AppLink target lost or double-encoded the original search query")
	}
	for _, origin := range []string{"http://music.example.test", "https://user:password@music.example.test", "https://music.example.test?open_id=private", "https://music.example.test/path", "javascript:alert(1)"} {
		t.Setenv("MUSIC_WEB_ORIGIN", origin)
		if links := BuildMusicWebLinks("query", ""); links.BrowserURL != "" || links.AppLink != "" {
			t.Fatalf("unsafe origin accepted: %s", origin)
		}
	}
}

func TestMusicWebLinksBrowserWorksWithoutAppAndDoesNotCarryOwner(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "https://music.example.test")
	t.Setenv("MUSIC_WEB_APP_ID", "")
	links := BuildMusicWebLinks("", "kuwo:42")
	u, err := url.Parse(links.BrowserURL)
	if err != nil {
		t.Fatal(err)
	}
	if links.AppLink != "" || u.Path != "/play" || u.Query().Get("trackID") != "kuwo:42" || len(u.Query()) != 1 {
		t.Fatal("browser entry is not independently usable")
	}
	if got := BuildMusicWebLinks("", "netease:42?open_id=private"); got.BrowserURL != "" {
		t.Fatal("unsafe track id accepted")
	}
}
