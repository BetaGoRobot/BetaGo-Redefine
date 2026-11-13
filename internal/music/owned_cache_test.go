package music

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func ownedProviderFromEnv(t *testing.T, endpoint, fallback string) Provider {
	t.Helper()
	t.Setenv("MUSIC_MINIO_CACHE_ENABLED", "true")
	t.Setenv("MUSIC_MINIO_ENDPOINT", endpoint)
	t.Setenv("MUSIC_MINIO_PUBLIC_ORIGIN", "https://music.example.test:2443")
	t.Setenv("MUSIC_MINIO_BUCKET", "cloudmusic")
	t.Setenv("MUSIC_MINIO_REGION", "us-east-1")
	t.Setenv("MUSIC_MINIO_ACCESS_KEY", "test-read-key")
	t.Setenv("MUSIC_MINIO_SECRET_KEY", "test-read-secret")
	t.Setenv("MUSIC_NETEASE_URL", fallback)
	return OptionsFromEnv(nil).Provider
}

func ownedObjectServer(t *testing.T, objects map[string]string, deny string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			t.Errorf("cache attempted mutation: %s", r.Method)
			w.WriteHeader(405)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=test-read-key/") {
			t.Error("private cache read did not use server-held credentials")
		}
		if r.URL.Path == deny {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(403)
			fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`)
			return
		}
		v, ok := objects[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
			return
		}
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", fmt.Sprint(len(v)))
		w.Header().Set("ETag", `"cache-object"`)
		if r.Method == "GET" {
			fmt.Fprint(w, v)
		}
	}))
}

func TestOwnedMinIOCacheSignsSameRecordingAndUsesMetadata(t *testing.T) {
	objects := map[string]string{
		"/cloudmusic/info/109616.json":       `{"title":"100种生活","artist":"卢广仲","album":"100种生活","duration":287427,"lyrics":"[00:01.00]first song","audioURL":"https://untrusted.invalid/expired.mp3"}`,
		"/cloudmusic/music/109616.mp3":       "real-audio-object-placeholder-not-playback-proof",
		"/cloudmusic/info/1417862065.json":   `{"TrackID":"netease:1417862065","Source":"netease","ExternalID":"1417862065","Title":"小王日记","Artist":[{"name":"毛不易"}],"Duration":2615591,"LyricsURL":"https://untrusted.invalid/lyrics"}`,
		"/cloudmusic/music/1417862065.mp3":   "second-object-placeholder-not-playback-proof",
		"/cloudmusic/lyrics/1417862065.json": `{"code":200,"lrc":{"lyric":"[00:02.00]second song"},"tlyric":{"lyric":"[00:02.00]translation"}}`,
	}
	cache := ownedObjectServer(t, objects, "")
	defer cache.Close()
	p := ownedProviderFromEnv(t, cache.URL, "")
	for _, tc := range []struct {
		id, title, artist, lyric string
		duration                 int64
	}{
		{"109616", "100种生活", "卢广仲", "[00:01.00]first song", 287427},
		{"1417862065", "小王日记", "毛不易", "[00:02.00]second song", 2615591},
	} {
		track, err := p.Detail(context.Background(), "netease:"+tc.id)
		if err != nil || track.ID != "netease:"+tc.id || track.Title != tc.title || track.Artist != tc.artist || track.DurationMs != tc.duration {
			t.Fatalf("owned metadata unavailable or incorrect: track=%+v err=%v", track, err)
		}
		res, err := p.Resolve(context.Background(), track)
		if err != nil || res.Status != "available" || res.Reason != "owned_minio_cache" || res.TrackID != track.ID || res.ExpiresAt == nil {
			t.Fatalf("owned source did not resolve explicitly: status=%s reason=%s err=%v", res.Status, res.Reason, err)
		}
		u, err := url.Parse(res.URL)
		if err != nil || u.Scheme != "https" || u.Host != "music.example.test:2443" || u.Path != "/cloudmusic/music/"+tc.id+".mp3" || u.Query().Get("X-Amz-Expires") != "300" || len(u.Query().Get("X-Amz-Signature")) != 64 || strings.Contains(res.URL, "test-read-secret") {
			t.Fatal("owned URL was not a new fixed-host, fixed-object HTTPS signature")
		}
		if remaining := time.Until(*res.ExpiresAt); remaining < 298*time.Second || remaining > 300*time.Second {
			t.Fatalf("incorrect expiry: %v", remaining)
		}
		lyrics, err := p.Lyrics(context.Background(), track)
		if err != nil || lyrics.TrackID != track.ID || lyrics.LRC != tc.lyric {
			t.Fatalf("owned lyrics used the wrong recording or an embedded URL: %+v %v", lyrics, err)
		}
	}
}

func TestOwnedCacheMissDelegatesOnlySameID(t *testing.T) {
	cache := ownedObjectServer(t, map[string]string{}, "")
	defer cache.Close()
	var requested []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Cookies()) != 0 {
			t.Error("fallback sent personal cookies")
		}
		switch r.URL.Path {
		case "/song/detail":
			requested = append(requested, r.URL.Query().Get("ids"))
			fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"same recording","dt":1000}]}`)
		case "/song/url/v1":
			requested = append(requested, r.URL.Query().Get("id"))
			fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":403,"url":"https://m1.music.126.net/denied.mp3"}]}`)
		default:
			t.Errorf("unexpected fallback endpoint %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	p := ownedProviderFromEnv(t, cache.URL, upstream.URL)
	track, err := p.Detail(context.Background(), "netease:42")
	if err != nil || track.ID != "netease:42" {
		t.Fatalf("cache miss did not delegate same identity: %+v %v", track, err)
	}
	res, err := p.Resolve(context.Background(), track)
	if err != nil || res.Status != "unavailable" || res.URL != "" || len(requested) != 2 || requested[0] != "42" || requested[1] != "42" {
		t.Fatalf("fallback changed identity or ignored denial: status=%s requests=%v err=%v", res.Status, requested, err)
	}
}

func TestOwnedCacheDeniedOrInvalidNeverFallsThrough(t *testing.T) {
	for _, tc := range []struct {
		name, info, denied string
		status             int
	}{
		{"metadata denied", `{}`, "/cloudmusic/info/42.json", 403},
		{"audio denied", `{"title":"cached","duration":1000}`, "/cloudmusic/music/42.mp3", 403},
		{"wrong recording", `{"TrackID":"netease:99","title":"wrong","duration":1000}`, "", 502},
		{"malformed metadata", `{invalid`, "", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := ownedObjectServer(t, map[string]string{"/cloudmusic/info/42.json": tc.info, "/cloudmusic/music/42.mp3": "audio"}, tc.denied)
			defer cache.Close()
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("denied or invalid owned cache silently fell back")
				w.WriteHeader(503)
			}))
			defer fallback.Close()
			p := ownedProviderFromEnv(t, cache.URL, fallback.URL)
			_, err := p.Detail(context.Background(), "netease:42")
			if ae, ok := err.(*apiError); !ok || ae.Status != tc.status {
				t.Fatalf("owned failure not explicit: %v", err)
			}
			res, err := p.Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
			if err != nil || res.Status != "unavailable" || res.URL != "" || !strings.HasPrefix(res.Reason, "owned_cache_") {
				t.Fatalf("owned failure became playable or recoverable outage: status=%s reason=%s err=%v", res.Status, res.Reason, err)
			}
		})
	}
}

func TestOwnedCacheRejectsUnsafeConfigurationAndPaths(t *testing.T) {
	cache := ownedObjectServer(t, map[string]string{}, "")
	defer cache.Close()
	for _, public := range []string{"http://music.example.test", "https://music.example.test/cloudmusic", "https://user:pass@music.example.test", "https://music.example.test?key=value"} {
		p := ownedProviderFromEnv(t, cache.URL, "")
		_ = p
		t.Setenv("MUSIC_MINIO_PUBLIC_ORIGIN", public)
		_, err := OptionsFromEnv(nil).Provider.Detail(context.Background(), "netease:42")
		if ae, ok := err.(*apiError); !ok || ae.Message != "owned_cache_not_configured" {
			t.Fatalf("unsafe public origin accepted or hid configuration failure: %v", err)
		}
	}
	p := ownedProviderFromEnv(t, cache.URL, "")
	for _, id := range []string{"netease:../99", "netease:42/../99", "https://attacker.invalid/a", "legacy:arbitrary"} {
		_, err := p.Detail(context.Background(), id)
		if err == nil {
			t.Fatal("client-controlled object identity accepted")
		}
	}
}

func TestOwnedCacheAPIExportNeverPersistsSignedURL(t *testing.T) {
	db := testDB(t)
	cache := ownedObjectServer(t, map[string]string{"/cloudmusic/info/42.json": `{"title":"owned","duration":1000}`, "/cloudmusic/music/42.mp3": "audio"}, "")
	defer cache.Close()
	h := NewServer(db, Options{AppID: "owned-app", Provider: ownedProviderFromEnv(t, cache.URL, "")}).HTTP()
	a := guest(t, h)
	call(t, h, a, "POST", "/playlists", map[string]any{"name": "owned", "trackIDs": []string{"netease:42"}}, 200)
	call(t, h, a, "POST", "/tracks/netease:42/resolve", map[string]any{}, 200)
	out := call(t, h, a, "GET", "/export", nil, 200)
	b, _ := json.Marshal(out)
	for _, forbidden := range []string{"X-Amz-", "test-read-key", "test-read-secret", "audioURL", "music.example.test"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("export persisted transient owned-source material: %s", forbidden)
		}
	}
}

func TestOwnedArtworkUsesStableFixedObjectAndFreshHTTPSRedirect(t *testing.T) {
	db := testDB(t)
	cache := ownedObjectServer(t, map[string]string{
		"/cloudmusic/info/42.json":   `{"title":"owned","duration":1000,"PictureURL":"http://untrusted.invalid/shortlink"}`,
		"/cloudmusic/music/42.mp3":   "audio",
		"/cloudmusic/picture/42.jpg": "image",
	}, "")
	defer cache.Close()
	h := NewServer(db, Options{AppID: "artwork-app", Provider: ownedProviderFromEnv(t, cache.URL, "")}).HTTP()
	token := guest(t, h)
	track := call(t, h, token, "GET", "/tracks/netease:42", nil, 200)
	if track["picture"] != "https://music.example.test:2443/music-api/v1/tracks/netease:42/artwork" {
		t.Fatalf("artwork is not stable HTTPS: %v", track["picture"])
	}
	w := request(h, "", "GET", "/tracks/netease:42/artwork", nil)
	if w.Code != 307 {
		t.Fatalf("public artwork did not redirect: %d", w.Code)
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil || u.Scheme != "https" || u.Host != "music.example.test:2443" || u.Path != "/cloudmusic/picture/42.jpg" || len(u.Query().Get("X-Amz-Signature")) != 64 {
		t.Fatal("artwork redirected to an unapproved path or insecure URL")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("artwork redirect may outlive its signature")
	}
	call(t, h, "", "GET", "/tracks/netease:99/artwork", nil, 404)
}
