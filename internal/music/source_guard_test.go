package music

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCataloguePicturesCannotIntroduceMixedContent(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://p1.music.126.net/cover.jpg", "https://p1.music.126.net/cover.jpg"},
		{"http://p4.music.126.net/cover.jpg", "https://p4.music.126.net/cover.jpg"},
		{"http://attacker.invalid/cover.jpg", ""},
		{"http://p1.music.126.net.attacker.invalid/a", ""},
		{"http://user:pass@p1.music.126.net/a", ""},
		{"https://p2.music.126.net/a", "https://p2.music.126.net/a"},
		{"https://art.example.test/a", "https://art.example.test/a"},
	} {
		var song netSong
		b, _ := json.Marshal(map[string]any{"id": 42, "name": "song", "al": map[string]string{"picUrl": tc.in}})
		if err := json.Unmarshal(b, &song); err != nil {
			t.Fatal(err)
		}
		if got := song.track().Picture; got != tc.want {
			t.Fatalf("unsafe picture persisted: input=%s got=%s", tc.in, got)
		}
	}
}

func TestOldVerifiedLibraryPicturesAreNormalizedAtEveryJSONBoundary(t *testing.T) {
	db := testDB(t)
	ids := []string{"netease:800001", "netease:800002"}
	for i, id := range ids {
		if err := saveTrack(db, Track{ID: id, Source: "netease", ExternalID: strings.TrimPrefix(id, "netease:"), Title: "old verified track"}); err != nil {
			t.Fatal(err)
		}
		picture := "http://p1.music.126.net/old.jpg"
		if i == 1 {
			picture = "http://unknown.invalid/old.jpg"
		}
		if err := db.Exec("UPDATE music_tracks SET picture=? WHERE id=?", picture, id).Error; err != nil {
			t.Fatal(err)
		}
	}
	h := NewServer(db, Options{AppID: "old-pictures"}).HTTP()
	token := guest(t, h)
	call(t, h, token, "POST", "/playlists", map[string]any{"name": "old pictures", "trackIDs": ids}, 200)
	for _, id := range ids {
		call(t, h, token, "PUT", "/likes/"+id, map[string]any{}, 200)
		call(t, h, token, "POST", "/history", map[string]any{"trackID": id, "positionMs": 0}, 200)
	}
	for _, path := range []string{"/tracks/" + ids[0], "/tracks/" + ids[1], "/playlists", "/likes", "/history", "/export"} {
		out := call(t, h, token, "GET", path, nil, 200)
		b, _ := json.Marshal(out)
		if strings.Contains(string(b), "http:") {
			t.Fatalf("old verified picture escaped through %s", path)
		}
		if path == "/tracks/"+ids[0] && out["picture"] != "https://p1.music.126.net/old.jpg" {
			t.Fatal("old official artwork did not upgrade")
		}
		if path == "/tracks/"+ids[1] && out["picture"] != "" {
			t.Fatal("old unknown HTTP artwork did not become a placeholder")
		}
	}
}

type sourceRoundTrip func(*http.Request) (*http.Response, error)

func (f sourceRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOfficialHTTPAudioRequiresCredentialFreeHTTPSVerification(t *testing.T) {
	for _, tc := range []struct {
		name         string
		head, ranged int
		want         string
	}{
		{"https available", 200, 0, "available"},
		{"head unsupported range works", 405, 206, "available"},
		{"head rejected but exact HTTPS range works", 403, 206, "available"},
		{"https denied", 403, 0, "unavailable"},
		{"range denied", 405, 403, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := http.DefaultTransport
			calls := 0
			http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() != "m801.music.126.net" {
					return base.RoundTrip(r)
				}
				calls++
				if r.URL.Scheme != "https" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("audio verification sent credentials or used HTTP")
				}
				status := tc.head
				if r.Method == "GET" {
					status = tc.ranged
					if r.Header.Get("Range") != "bytes=0-0" {
						t.Error("audio verification requested an unrestricted stream")
					}
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("a")), Request: r}, nil
			})
			defer func() { http.DefaultTransport = base }()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"code":200,"data":[{"id":42,"url":"http://m801.music.126.net/recording.mp3","expi":200}]}`)
			}))
			defer api.Close()
			res, err := (AnonymousNetease{BaseURL: api.URL}).Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
			if err != nil || res.Status != tc.want || calls == 0 {
				t.Fatalf("unverified HTTPS audio escaped: status=%s probeCalls=%d err=%v", res.Status, calls, err)
			}
			if res.Status == "available" && !strings.HasPrefix(res.URL, "https://m801.music.126.net/") {
				t.Fatal("HTTP audio escaped to the browser")
			}
			if res.Status == "unavailable" && res.URL != "" {
				t.Fatal("denied HTTPS URL still exposed as playable")
			}
		})
	}
}

func TestConfiguredServiceCookieNeverBecomesUserIdentityOrRedirects(t *testing.T) {
	db := testDB(t)
	const providerCookie = "MUSIC_U=server-owned-secret; __csrf=server-only"
	for _, preview := range []bool{false, true} {
		seen := 0
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Cookie") != providerCookie {
				t.Error("configured service did not receive its server-held authorization")
			}
			seen++
			switch r.URL.Path {
			case "/song/detail":
				fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"authorized recording","dt":1000}]}`)
			case "/song/url/v1":
				trial := "null"
				if preview {
					trial = `{"start":0,"end":30}`
				}
				fmt.Fprintf(w, `{"code":200,"data":[{"id":42,"url":"https://m801.music.126.net/authorized.mp3","freeTrialInfo":%s}]}`, trial)
			default:
				t.Errorf("unexpected provider path %s", r.URL.Path)
			}
		}))
		t.Setenv("MUSIC_MINIO_CACHE_ENABLED", "false")
		t.Setenv("MUSIC_NETEASE_URL", api.URL)
		t.Setenv("MUSIC_NETEASE_COOKIE", providerCookie)
		p := OptionsFromEnv(nil).Provider
		h := NewServer(db, Options{AppID: "cookie-independent-app", Provider: p}).HTTP()
		token := guest(t, h)
		me := call(t, h, token, "GET", "/me", nil, 200)
		if me["user"].(map[string]any)["kind"] != "guest" {
			t.Fatal("provider account became application user")
		}
		call(t, h, token, "GET", "/tracks/netease:42", nil, 200)
		out := call(t, h, token, "POST", "/tracks/netease:42/resolve", map[string]any{}, 200)
		want := "available"
		if preview {
			want = "preview"
		}
		reason, _ := out["reason"].(string)
		if out["status"] != want || !strings.HasPrefix(reason, "account_authorized_") || seen == 0 {
			t.Fatalf("service authorization did not preserve source status: %v reason=%s", out["status"], reason)
		}
		b, _ := json.Marshal(out)
		if strings.Contains(string(b), "server-owned-secret") || strings.Contains(string(b), "MUSIC_U") {
			t.Fatal("provider credentials exposed to browser")
		}
		api.Close()
	}
	exfiltrated := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { exfiltrated = true; w.WriteHeader(200) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL+"/capture", 307) }))
	defer redirect.Close()
	t.Setenv("MUSIC_NETEASE_URL", redirect.URL)
	p := OptionsFromEnv(nil).Provider
	_, err := p.Detail(context.Background(), "netease:99")
	if err == nil || exfiltrated {
		t.Fatal("trusted provider redirect was followed with server credentials")
	}
}
