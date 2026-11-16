package music

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestUNMCachePersistsProvenanceInSeparateNamespaceAndRestartsWithoutAccount(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{"/cloudmusic/music/42.mp3": []byte("pre-existing unverified orphan; must remain unchanged")}
	cache := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "PUT" {
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("missing immutable write condition")
			}
			if _, exists := objects[r.URL.Path]; exists {
				w.WriteHeader(412)
				return
			}
			objects[r.URL.Path], _ = io.ReadAll(r.Body)
			w.Header().Set("ETag", `"written"`)
			return
		}
		b, ok := objects[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		w.Header().Set("ETag", `"stored"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		if r.Method != "HEAD" {
			w.Write(b)
		}
	}))
	defer cache.Close()
	var metadataCalls, bridgeCalls, downloads atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataCalls.Add(1)
		if r.Header.Get("Cookie") != "" {
			t.Error("metadata consumed account")
		}
		switch r.URL.Path {
		case "/song/detail":
			fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"official canonical","dt":2612}]}`)
		case "/lyric":
			fmt.Fprint(w, `{"code":200,"lrc":{"lyric":"[00:01]original ID lyrics"}}`)
		default:
			t.Errorf("unexpected anonymous/account resolve %s", r.URL.Path)
		}
	}))
	defer api.Close()
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bridgeCalls.Add(1)
		fmt.Fprint(w, "https://m801.music.126.net/cache-source.mp3?secret-temporary-url=must-not-store")
	}))
	defer bridge.Close()
	audio := durationTestFrames(3, 0, 100, false, false)
	base := http.DefaultTransport
	http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "m801.music.126.net" {
			return base.RoundTrip(r)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("CDN received credentials")
		}
		data, status := audio, 200
		headers := http.Header{"Content-Type": {"audio/mpeg"}}
		if r.Header.Get("Range") != "" {
			status = 206
			headers.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(audio)-1, len(audio)))
		} else {
			downloads.Add(1)
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(string(data))), ContentLength: int64(len(data)), Request: r}, nil
	})
	defer func() { http.DefaultTransport = base }()
	pipelineEnv(t, api.URL, bridge.URL)
	ownedProviderFromEnv(t, cache.URL, api.URL)
	t.Setenv("MUSIC_MINIO_CACHE_WRITE_ENABLED", "true")
	track := Track{ID: "netease:42", Source: "netease", ExternalID: "42", DurationMs: 2612}
	p := OptionsFromEnv(nil).Provider
	r, err := p.Resolve(context.Background(), track)
	if err != nil || r.Status != "available" || !strings.HasPrefix(r.Reason, "owned_minio_cache_stored") {
		t.Fatalf("UNM cache did not commit: %#v %v", r, err)
	}
	mu.Lock()
	for _, key := range []string{"music/unm/pyncmd/netease/42.mp3", "catalog/unm/pyncmd/netease/42.json", "lyrics/unm/pyncmd/netease/42.json"} {
		if len(objects["/cloudmusic/"+key]) == 0 {
			t.Errorf("missing namespace %s", key)
		}
	}
	manifest := string(objects["/cloudmusic/catalog/unm/pyncmd/netease/42.json"])
	if string(objects["/cloudmusic/music/42.mp3"]) != "pre-existing unverified orphan; must remain unchanged" {
		t.Error("legacy orphan overwritten")
	}
	for _, bad := range []string{"temporary-url", "https://m801", "MUSIC_U", "X-Amz-"} {
		if strings.Contains(manifest, bad) {
			t.Errorf("transient secret in manifest: %s", bad)
		}
	}
	mu.Unlock()
	if !strings.Contains(manifest, `"mediaSource":"unm:pyncmd"`) || !strings.Contains(manifest, `"verification":"id_and_duration"`) {
		t.Fatalf("durable provenance missing: %s", manifest)
	}
	before := metadataCalls.Load()
	// Reconstruct all providers, as after a process restart. No route/process state
	// can be used to infer the original audio's provenance.
	p = OptionsFromEnv(nil).Provider
	r, err = p.Resolve(context.Background(), track)
	u, _ := url.Parse(r.URL)
	if err != nil || r.MediaSource != "unm:pyncmd" || r.OriginTrackID != track.ID || r.Verification != "id_and_duration" || u.Path != "/cloudmusic/music/unm/pyncmd/netease/42.mp3" || r.Reason != "owned_minio_cache" {
		t.Fatalf("restart lost provenance/path: %#v %v", r, err)
	}
	lyrics, err := p.Lyrics(context.Background(), track)
	if err != nil || lyrics.TrackID != track.ID || !strings.Contains(lyrics.LRC, "original ID") {
		t.Fatalf("namespace lyrics wrong: %#v %v", lyrics, err)
	}
	if metadataCalls.Load() != before || bridgeCalls.Load() != 1 || downloads.Load() != 1 {
		t.Fatalf("cache hit consumed source/account: metadata=%d/%d bridge=%d downloads=%d", metadataCalls.Load(), before, bridgeCalls.Load(), downloads.Load())
	}
	// A forged mapping must fail closed; a requested ID is never enough to
	// relabel another cached original recording as this track.
	mu.Lock()
	objects["/cloudmusic/catalog/unm/pyncmd/netease/42.json"] = []byte(strings.Replace(manifest, `"originTrackID":"netease:42"`, `"originTrackID":"netease:43"`, 1))
	mu.Unlock()
	r, _ = OptionsFromEnv(nil).Provider.Resolve(context.Background(), track)
	if r.Status != "unavailable" || r.URL != "" {
		t.Fatalf("cross-ID cache accepted: %#v", r)
	}
}

func TestUNMCacheRejectsShortOrTruncatedFullDownload(t *testing.T) {
	for _, mode := range []string{"eleven_seconds", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			var puts atomic.Int32
			cache := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					puts.Add(1)
				}
				w.WriteHeader(404)
				fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			}))
			defer cache.Close()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"canonical","dt":326000}]}`)
			}))
			defer api.Close()
			bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "https://m801.music.126.net/short.mp3") }))
			defer bridge.Close()
			audio := durationTestFrames(3, 0, 420, false, false)
			if mode == "truncated" {
				audio = audio[:len(audio)-1]
			}
			base := http.DefaultTransport
			http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() != "m801.music.126.net" {
					return base.RoundTrip(r)
				}
				b, status := audio, 200
				headers := http.Header{"Content-Type": {"audio/mpeg"}}
				if r.Header.Get("Range") != "" {
					b = audio[:417]
					status = 206
					headers.Set("Content-Range", fmt.Sprintf("bytes 0-416/%d", len(audio)))
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(string(b))), ContentLength: int64(len(b)), Request: r}, nil
			})
			defer func() { http.DefaultTransport = base }()
			pipelineEnv(t, api.URL, bridge.URL)
			ownedProviderFromEnv(t, cache.URL, api.URL)
			t.Setenv("MUSIC_MINIO_CACHE_WRITE_ENABLED", "true")
			r, err := OptionsFromEnv(nil).Provider.Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42", DurationMs: 326000})
			wantStatus := "preview"
			if mode == "truncated" {
				wantStatus = "unavailable"
			}
			if err != nil || r.Status != wantStatus || puts.Load() != 0 || strings.HasPrefix(r.Reason, "owned_minio_cache") || r.Verification == "id_and_duration" {
				t.Fatalf("incomplete audio published as complete: %#v %v puts=%d", r, err, puts.Load())
			}
		})
	}
}
