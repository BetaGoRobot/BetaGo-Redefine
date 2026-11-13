package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func kuwoTestTrack() Track {
	return Track{ID: "kuwo:629335603", Source: "kuwo", ExternalID: "629335603", Title: "小星星 (伴奏版)", Artist: "贝乐虎儿歌", Album: "贝乐虎儿歌大全", DurationMs: 75000}
}

func TestKuwoProviderExplicitIdentityAndCanonicalMetadata(t *testing.T) {
	track := kuwoTestTrack()
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected request credentials or mutation")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search":
			if r.URL.Query().Get("q") != "小星星 贝乐虎" {
				t.Errorf("query changed: %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tracks": []Track{track, track}})
		case "/tracks/629335603":
			_ = json.NewEncoder(w).Encode(map[string]any{"track": track})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	p := KuwoProvider{BaseURL: upstream.URL}
	found, err := p.Search(context.Background(), "小星星 贝乐虎")
	if err != nil || len(found) != 1 || found[0] != track {
		t.Fatalf("canonical search: %#v %v", found, err)
	}
	got, err := p.Detail(context.Background(), track.ID)
	if err != nil || got != track {
		t.Fatalf("canonical detail: %#v %v", got, err)
	}
	for _, id := range []string{"netease:629335603", "kuwo:0", "kuwo:123/resolve", "kuwo:123?cookie=x", "kuwo:0123"} {
		if _, err := p.Detail(context.Background(), id); err == nil {
			t.Errorf("accepted foreign/invalid ID %q", id)
		}
	}
	if _, err := p.SearchSource(context.Background(), "netease", "x"); err == nil {
		t.Fatal("source selection silently crossed providers")
	}
	if calls != 2 {
		t.Errorf("invalid identity reached adapter: %d calls", calls)
	}
	lyrics, err := p.Lyrics(context.Background(), track)
	if err != nil || lyrics.TrackID != track.ID || lyrics.LRC != "" {
		t.Fatalf("empty same-ID lyrics: %#v %v", lyrics, err)
	}
}

func TestKuwoProviderResolveGuardsAndDuration(t *testing.T) {
	track := kuwoTestTrack()
	cases := []struct {
		name, status, url, id string
		duration              int64
		tls                   bool
		expired               bool
		want                  string
	}{
		{"free TLS verified", "available", "https://kw-lv.kuwo.cn/resource.mp3", track.ID, 75000, true, false, "available"},
		{"short fixed denial clip", "available", "https://kw-bj.kuwo.cn/resource.mp3", track.ID, 11285, true, false, "unavailable"},
		{"HTTP is never sent to browser", "available", "http://kw-lv.kuwo.cn/resource.mp3", track.ID, 75000, true, false, "unavailable"},
		{"foreign audio", "available", "https://music.126.net/resource.mp3", track.ID, 75000, true, false, "unavailable"},
		{"suffix lookalike", "available", "https://kw-lv.kuwo.cn.evil.test/resource.mp3", track.ID, 75000, true, false, "unavailable"},
		{"credentials", "available", "https://user:password@kw-lv.kuwo.cn/resource.mp3", track.ID, 75000, true, false, "unavailable"},
		{"foreign recording", "available", "https://kw-lv.kuwo.cn/resource.mp3", "kuwo:123", 75000, true, false, "unavailable"},
		{"TLS not verified", "available", "https://kw-lv.kuwo.cn/resource.mp3", track.ID, 75000, false, false, "unavailable"},
		{"expired", "available", "https://kw-lv.kuwo.cn/resource.mp3", track.ID, 75000, true, true, "unavailable"},
		{"restriction cannot leak URL", "unavailable", "https://kw-lv.kuwo.cn/resource.mp3", track.ID, 75000, true, false, "unavailable"},
		{"explicit preview", "preview", "https://kw-lv.kuwo.cn/resource.mp3", track.ID, 30000, true, false, "preview"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			expires := time.Now().Add(time.Minute)
			if tt.expired {
				expires = time.Now().Add(-time.Minute)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/resolve/629335603" {
					t.Errorf("recording changed: %s", r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"trackID": tt.id, "status": tt.status, "url": tt.url, "expiresAt": expires, "reason": "provider_explicit_preview", "audio": map[string]any{"tlsVerified": tt.tls, "durationMs": tt.duration, "sizeBytes": 1202674, "contentType": "audio/mpeg"}})
			}))
			defer upstream.Close()
			got, err := (KuwoProvider{BaseURL: upstream.URL}).Resolve(context.Background(), track)
			if err != nil || got.TrackID != track.ID || got.Status != tt.want {
				t.Fatalf("resolve %#v %v", got, err)
			}
			if tt.want == "unavailable" && (got.URL != "" || got.ExpiresAt != nil) {
				t.Fatalf("denied source leaked URL: %#v", got)
			}
		})
	}
}

func TestKuwoProviderRejectsPoisoningAndTransportFailures(t *testing.T) {
	track := kuwoTestTrack()
	for _, base := range []string{"https://example.test", "http://127.0.0.1:37836?token=secret", "http://user:pass@localhost:37836", "http://localhost.evil.test"} {
		if _, err := (KuwoProvider{BaseURL: base}).Search(context.Background(), "x"); err == nil {
			t.Errorf("accepted untrusted adapter %q", base)
		}
	}
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"foreign canonical ID", func(w http.ResponseWriter, r *http.Request) {
			other := track
			other.ID = "netease:629335603"
			_ = json.NewEncoder(w).Encode(map[string]any{"track": other})
		}},
		{"wrong detail ID", func(w http.ResponseWriter, r *http.Request) {
			other := track
			other.ID = "kuwo:123"
			other.ExternalID = "123"
			_ = json.NewEncoder(w).Encode(map[string]any{"track": other})
		}},
		{"oversized response", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1))) }},
		{"invalid JSON", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"track":`)) }},
		{"source denial", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://example.test", http.StatusFound)
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			up := httptest.NewServer(tt.handler)
			defer up.Close()
			if _, err := (KuwoProvider{BaseURL: up.URL}).Detail(context.Background(), track.ID); err == nil {
				t.Fatal("accepted poisoned or unavailable canonical metadata")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled request reached adapter") }))
	defer up.Close()
	if _, err := (KuwoProvider{BaseURL: up.URL}).Search(ctx, "x"); err == nil {
		t.Fatal("cancelled source lookup succeeded")
	}
}

// Opt-in live evidence only; regular CI stays deterministic and account-free.
func TestKuwoProviderLiveSource(t *testing.T) {
	if os.Getenv("MUSIC_KUWO_LIVE_TEST") != "1" {
		t.Skip("set MUSIC_KUWO_LIVE_TEST=1 with an isolated local adapter")
	}
	p := KuwoProvider{BaseURL: os.Getenv("MUSIC_KUWO_API_URL")}
	found, err := p.SearchSource(context.Background(), "kuwo", "小星星 纯音乐")
	if err != nil {
		t.Fatalf("live anonymous catalogue: %v", err)
	}
	present := false
	for _, track := range found {
		if track.ID == "kuwo:629335603" {
			present = true
		}
	}
	if !present {
		t.Fatal("live public catalogue no longer contains exact free sample")
	}
	track, err := p.Detail(context.Background(), "kuwo:629335603")
	if err != nil || track.Source != "kuwo" || track.ExternalID != "629335603" {
		t.Fatalf("live canonical identity failed: %v", err)
	}
	resolved, err := p.Resolve(context.Background(), track)
	if err != nil || resolved.Status != "available" {
		t.Fatalf("live recording unavailable: status=%s reason=%s error=%v", resolved.Status, resolved.Reason, err)
	}
	audio, err := url.Parse(resolved.URL)
	if err != nil || audio.Scheme != "https" {
		t.Fatal("live resolver did not return HTTPS")
	}
	t.Logf("source=%s id=%s title=%s audioHost=%s catalogueDurationMs=%d status=%s", track.Source, track.ExternalID, track.Title, audio.Hostname(), track.DurationMs, resolved.Status)
	lyrics, err := p.Lyrics(context.Background(), track)
	if err != nil || lyrics.TrackID != track.ID || lyrics.LRC != "" {
		t.Fatal("live lyrics changed recording identity")
	}
	if _, err = p.Search(context.Background(), "海阔天空 Beyond"); err != nil {
		t.Fatalf("live restriction catalogue: %v", err)
	}
	restricted, err := p.Detail(context.Background(), "kuwo:5886682")
	if err != nil {
		t.Fatalf("live restriction detail: %v", err)
	}
	denied, err := p.Resolve(context.Background(), restricted)
	if err != nil || denied.Status != "unavailable" || denied.URL != "" {
		t.Fatalf("restricted recording did not fail closed: status=%s reason=%s error=%v", denied.Status, denied.Reason, err)
	}
	t.Logf("restricted id=%s status=%s reason=%s", denied.TrackID, denied.Status, denied.Reason)
}
