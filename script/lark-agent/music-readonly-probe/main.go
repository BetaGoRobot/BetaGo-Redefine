// Deployment-only read probe. It does not initialize services, mutate databases,
// upload media, or send messages.
package main

import (
	"context"
	"encoding/json"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	c, err := config.Load(os.Args[1], "agent")
	if err != nil {
		panic("private configuration invalid")
	}
	checks := []map[string]any{}
	passed := true
	check := func(name string, ok bool) {
		checks = append(checks, map[string]any{"name": name, "passed": ok})
		passed = passed && ok
	}

	fetch := func(raw string) bool {
		h := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, e := http.NewRequest("GET", raw, nil)
		if e != nil {
			return false
		}
		req.Header.Set("Range", "bytes=0-4095")
		resp, e := h.Do(req)
		if e != nil {
			return false
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return e == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 && len(b) > 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	music, err := business.NewMusicClient(business.MusicConfig{BaseURL: c.Capabilities.MusicURL, Cookie: c.Capabilities.MusicCookie, Timeout: 30 * time.Second})
	if err != nil {
		panic("music configuration invalid")
	}
	result, err := music.Search(ctx, business.MusicSearch{Kind: "song", Keywords: "晴天", Limit: 1})
	check("search", err == nil && len(result.Items) > 0)
	if len(result.Items) > 0 {
		id := result.Items[0].ID
		detail, e := music.Detail(ctx, "song", id, 1, 0)
		check("detail", e == nil && len(detail.Tracks) == 1 && detail.Tracks[0].AlbumID > 0)
		if len(detail.Tracks) == 1 {
			cover, e := business.ValidateMusicMediaURL(detail.Tracks[0].ImageURL, []string{"music.126.net", "music.163.com"})
			check("cover_readable", e == nil && fetch(cover.String()))
		}

		lyrics, e := music.Lyrics(ctx, id)
		check("lyrics", e == nil && lyrics.Full != "")
		_, e = music.Comment(ctx, "song", id)
		check("hot_comment", e == nil)
		raw, e := music.PlaybackURL(ctx, id, business.PlaybackOptions{Refresh: true})
		check("unblocked_source", e == nil && raw != "")
		url, e := business.ValidateMusicMediaURL(raw, c.Capabilities.AudioMediaHosts)
		check("source_host_allowed", e == nil)
		if e == nil {
			checks = append(checks, map[string]any{"source_host": url.Hostname()})
			check("audio_source_readable", fetch(url.String()))
		}
	}
	client, e := lark.NewClient(lark.ClientConfig{AppID: c.Lark.AppID, AppSecret: c.Lark.AppSecret, BotOpenID: c.Lark.BotOpenID, TenantID: c.App.TenantID, AllowedChatIDs: c.App.AllowedChatIDs})
	if e != nil {
		panic("Lark configuration invalid")
	}
	for i, a := range c.Capabilities.Admins {
		ok, e := client.MusicMember(ctx, domain.Scope{TenantID: a.TenantID, AppID: c.Lark.AppID, ChatID: a.ChatID, Visibility: c.App.Visibility}, a.ActorID)
		checks = append(checks, map[string]any{"name": "scoped_member_permission", "scope_number": i + 1, "passed": e == nil && ok})
		passed = passed && e == nil && ok
	}
	hc := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, "GET", c.Capabilities.MusicPlayerURL, nil)
	if e != nil {
		panic("player configuration invalid")
	}
	resp, e := hc.Do(req)
	ok := e == nil && resp.StatusCode >= 200 && resp.StatusCode < 400
	if resp != nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
	}
	check("legacy_player_reachable", ok)
	json.NewEncoder(os.Stdout).Encode(map[string]any{"passed": passed, "read_only": true, "checks": checks, "real_lark_messages_sent": 0, "real_lark_uploads": 0, "orders_prepared_or_created": 0})
	if !passed {
		os.Exit(1)
	}
}
