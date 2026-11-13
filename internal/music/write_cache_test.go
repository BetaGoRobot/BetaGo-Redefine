package music

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Protocol-only fixture: an ID3v2 header followed by an MPEG frame header.
// This is not used as proof of real song playback.
const cacheTestMP3 = "ID3\x04\x00\x00\x00\x00\x00\x00\xff\xfb\x90\x64"

func TestWriteThroughCacheCoalescesAndNeverStoresCredentialsOrURLs(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	puts, downloads, resolves := 0, 0, 0
	cache := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "PUT" {
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("cache PUT is not protected against concurrent overwrite")
			}
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			objects[r.URL.Path] = b
			puts++
			w.Header().Set("ETag", `"written"`)
			w.WriteHeader(200)
			return
		}
		b, ok := objects[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		w.Header().Set("ETag", `"written"`)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		if r.Method == "GET" {
			w.Write(b)
		}
	}))
	defer cache.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/song/url/v1":
			mu.Lock()
			resolves++
			mu.Unlock()
			fmt.Fprint(w, `{"code":200,"data":[{"id":42,"url":"https://m801.music.126.net/cache-test.mp3","expi":200}]}`)
		case "/song/detail":
			fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"canonical write-through","dt":2000,"ar":[{"name":"artist"}]}]}`)
		case "/lyric":
			fmt.Fprint(w, `{"code":200,"lrc":{"lyric":"[00:01.00]cached line"},"tlyric":{"lyric":"translated"}}`)
		default:
			t.Errorf("unexpected provider endpoint %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	base := http.DefaultTransport
	http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "m801.music.126.net" {
			return base.RoundTrip(r)
		}
		mu.Lock()
		downloads++
		mu.Unlock()
		if r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("cache downloader sent credentials or used wrong method")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(cacheTestMP3)), Body: io.NopCloser(strings.NewReader(cacheTestMP3)), Request: r}, nil
	})
	defer func() { http.DefaultTransport = base }()
	p := ownedProviderFromEnv(t, cache.URL, upstream.URL)
	_ = p
	t.Setenv("MUSIC_MINIO_CACHE_WRITE_ENABLED", "true")
	t.Setenv("MUSIC_NETEASE_COOKIE", "MUSIC_U=never-persist-this")
	p = OptionsFromEnv(nil).Provider
	track := Track{ID: "netease:42", Source: "netease", ExternalID: "42"}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := p.Resolve(context.Background(), track)
			if err != nil || r.Status != "available" || !strings.HasPrefix(r.Reason, "owned_minio_cache") {
				t.Errorf("first cache result status=%s reason=%s err=%v", r.Status, r.Reason, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	second, err := p.Resolve(context.Background(), track)
	if err != nil || second.Reason != "owned_minio_cache" {
		t.Fatalf("later play did not hit completed cache: reason=%s err=%v", second.Reason, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if downloads != 1 || resolves != 1 || puts != 3 {
		t.Fatalf("same recording was fetched repeatedly: downloads=%d sourceResolves=%d puts=%d", downloads, resolves, puts)
	}
	if len(objects["/cloudmusic/music/42.mp3"]) == 0 || len(objects["/cloudmusic/catalog/42.json"]) == 0 || len(objects["/cloudmusic/lyrics/42.json"]) == 0 {
		t.Fatal("write-through missing complete audio/manifest/lyrics")
	}
	for _, key := range []string{"/cloudmusic/catalog/42.json", "/cloudmusic/lyrics/42.json"} {
		for _, bad := range []string{"https://m801", "X-Amz-", "never-persist-this", "MUSIC_U"} {
			if strings.Contains(string(objects[key]), bad) {
				t.Fatalf("cache persisted transient credentials/URL in %s", key)
			}
		}
	}
}

func TestWriteThroughSkipsPreviewAndReportsFailedCommit(t *testing.T) {
	for _, preview := range []bool{true, false} {
		t.Run(fmt.Sprint("preview=", preview), func(t *testing.T) {
			puts, downloads := 0, 0
			cache := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					puts++
					w.WriteHeader(403)
					fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
					return
				}
				w.WriteHeader(404)
				fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			}))
			defer cache.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/song/url/v1":
					trial := "null"
					if preview {
						trial = `{"start":0,"end":30}`
					}
					fmt.Fprintf(w, `{"code":200,"data":[{"id":42,"url":"https://m801.music.126.net/cache-failed.mp3","freeTrialInfo":%s}]}`, trial)
				case "/song/detail":
					fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"canonical","dt":1000}]}`)
				case "/lyric":
					fmt.Fprint(w, `{"code":200}`)
				}
			}))
			defer upstream.Close()
			base := http.DefaultTransport
			http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() != "m801.music.126.net" {
					return base.RoundTrip(r)
				}
				downloads++
				return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(cacheTestMP3)), Body: io.NopCloser(strings.NewReader(cacheTestMP3)), Request: r}, nil
			})
			defer func() { http.DefaultTransport = base }()
			ownedProviderFromEnv(t, cache.URL, upstream.URL)
			t.Setenv("MUSIC_MINIO_CACHE_WRITE_ENABLED", "true")
			r, err := OptionsFromEnv(nil).Provider.Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
			if err != nil {
				t.Fatal(err)
			}
			if preview {
				if r.Status != "preview" || !strings.Contains(r.Reason, "cache_skipped_preview") || puts != 0 || downloads != 0 {
					t.Fatalf("preview cached as full: status=%s reason=%s puts=%d downloads=%d", r.Status, r.Reason, puts, downloads)
				}
			} else if r.Status != "available" || !strings.Contains(r.Reason, "cache_failed") || puts == 0 || downloads != 1 || !strings.HasPrefix(r.URL, "https://m801.music.126.net/") {
				t.Fatalf("failed cache commit misreported: status=%s reason=%s puts=%d downloads=%d", r.Status, r.Reason, puts, downloads)
			}
		})
	}
}

func TestWriteThroughMissingManifestDoesNotExposeOrphanAudio(t *testing.T) {
	objects := map[string][]byte{}
	var mu sync.Mutex
	cache := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "PUT" {
			if strings.HasPrefix(r.URL.Path, "/cloudmusic/catalog/") {
				w.WriteHeader(403)
				fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
				return
			}
			b, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = b
			w.Header().Set("ETag", `"complete"`)
			w.WriteHeader(200)
			return
		}
		b, ok := objects[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		if r.Method == "GET" {
			w.Write(b)
		}
	}))
	defer cache.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/song/detail":
			fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"canonical","dt":1000}]}`)
		case "/song/url/v1":
			fmt.Fprint(w, `{"code":200,"data":[{"id":42,"url":"https://m801.music.126.net/manifest-failure.mp3"}]}`)
		case "/lyric":
			fmt.Fprint(w, `{"code":200}`)
		}
	}))
	defer upstream.Close()
	base := http.DefaultTransport
	http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "m801.music.126.net" {
			return base.RoundTrip(r)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(cacheTestMP3)), Body: io.NopCloser(strings.NewReader(cacheTestMP3)), Request: r}, nil
	})
	defer func() { http.DefaultTransport = base }()
	ownedProviderFromEnv(t, cache.URL, upstream.URL)
	t.Setenv("MUSIC_MINIO_CACHE_WRITE_ENABLED", "true")
	p := OptionsFromEnv(nil).Provider
	r, err := p.Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
	if err != nil || r.Status != "available" || !strings.Contains(r.Reason, "cache_failed_manifest_write") || !strings.HasPrefix(r.URL, "https://m801.music.126.net/") {
		t.Fatalf("failed manifest claimed a committed cache: status=%s reason=%s err=%v", r.Status, r.Reason, err)
	}
	mu.Lock()
	audioExists := len(objects["/cloudmusic/music/42.mp3"]) > 0
	manifestExists := len(objects["/cloudmusic/catalog/42.json"]) > 0
	mu.Unlock()
	if !audioExists || manifestExists {
		t.Fatal("test did not reproduce an audio-only incomplete commit")
	}
	if _, _, err := p.(*ownedCacheProvider).lookup(context.Background(), "netease:42"); err != ownedMiss {
		t.Fatalf("orphan audio became a cache hit: %v", err)
	}
}

func TestWriteThroughRejectsOversizePartialAndRedirectedDownloads(t *testing.T) {
	for _, mode := range []string{"oversize", "partial", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			puts, calls := 0, 0
			cache := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					puts++
				}
				w.WriteHeader(404)
				fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			}))
			defer cache.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/song/detail":
					fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"canonical"}]}`)
				case "/song/url/v1":
					fmt.Fprint(w, `{"code":200,"data":[{"id":42,"url":"https://m801.music.126.net/bounded.mp3"}]}`)
				}
			}))
			defer upstream.Close()
			base := http.DefaultTransport
			http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
				if !officialMediaHost(r.URL.Hostname()) {
					return base.RoundTrip(r)
				}
				calls++
				header := make(http.Header)
				status := 200
				size := int64(5)
				if mode == "oversize" {
					size = ownedAudioLimit + 1
				}
				if mode == "partial" {
					status = 206
					header.Set("Content-Range", "bytes 0-4/100")
				}
				if mode == "redirect" {
					status = 307
					header.Set("Location", "https://m802.music.126.net/different.mp3")
				}
				return &http.Response{StatusCode: status, Header: header, ContentLength: size, Body: io.NopCloser(strings.NewReader("audio")), Request: r}, nil
			})
			defer func() { http.DefaultTransport = base }()
			ownedProviderFromEnv(t, cache.URL, upstream.URL)
			t.Setenv("MUSIC_MINIO_CACHE_WRITE_ENABLED", "true")
			r, err := OptionsFromEnv(nil).Provider.Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
			if err != nil || r.Status != "available" || !strings.Contains(r.Reason, "cache_failed") || puts != 0 || calls != 1 {
				t.Fatalf("unsafe full-track download was cached/retried: mode=%s reason=%s puts=%d calls=%d err=%v", mode, r.Reason, puts, calls, err)
			}
		})
	}
}

func TestWriteThroughDoesNotCacheHTTP200ErrorPagesOrOtherFormats(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body string }{
		{"html", "text/html", "<!doctype html><html>access denied</html>"},
		{"json", "application/json", `{"error":"not a recording"}`},
		{"wrong MIME despite prefix", "text/html", cacheTestMP3},
		{"other format", "audio/flac", "fLaC-other-format"},
		{"plain error disguised as audio", "audio/mpeg", "upstream returned an error page"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			puts := 0
			cache := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					puts++
					w.Header().Set("ETag", `"bad-content"`)
					w.WriteHeader(200)
					return
				}
				w.WriteHeader(404)
				fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			}))
			defer cache.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/song/detail":
					fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"canonical"}]}`)
				case "/song/url/v1":
					fmt.Fprint(w, `{"code":200,"data":[{"id":42,"url":"https://m801.music.126.net/not-a-track.mp3"}]}`)
				case "/lyric":
					fmt.Fprint(w, `{"code":200}`)
				}
			}))
			defer upstream.Close()
			base := http.DefaultTransport
			http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
				if !officialMediaHost(r.URL.Hostname()) {
					return base.RoundTrip(r)
				}
				header := make(http.Header)
				header.Set("Content-Type", tc.contentType)
				return &http.Response{StatusCode: 200, Header: header, ContentLength: int64(len(tc.body)), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			defer func() { http.DefaultTransport = base }()
			ownedProviderFromEnv(t, cache.URL, upstream.URL)
			t.Setenv("MUSIC_MINIO_CACHE_WRITE_ENABLED", "true")
			r, err := OptionsFromEnv(nil).Provider.Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
			if err != nil || r.Status != "available" || !strings.Contains(r.Reason, "cache_failed_format") || puts != 0 {
				t.Fatalf("HTTP200 non-MP3 response entered cache: status=%s reason=%s puts=%d err=%v", r.Status, r.Reason, puts, err)
			}
		})
	}
}

func TestWriteThroughPreservesExistingUnverifiedAudioWithoutDownload(t *testing.T) {
	puts, downloads := 0, 0
	cache := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			puts++
			w.Header().Set("ETag", `"overwritten"`)
			w.WriteHeader(200)
			return
		}
		if r.URL.Path == "/cloudmusic/music/42.mp3" {
			w.Header().Set("Content-Length", "12345")
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.Header().Set("ETag", `"preserve-existing"`)
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
		fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
	}))
	defer cache.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/song/url/v1":
			fmt.Fprint(w, `{"code":200,"data":[{"id":42,"url":"https://m801.music.126.net/existing-unknown.mp3"}]}`)
		case "/song/detail":
			fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"canonical"}]}`)
		case "/lyric":
			fmt.Fprint(w, `{"code":200}`)
		}
	}))
	defer upstream.Close()
	base := http.DefaultTransport
	http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if !officialMediaHost(r.URL.Hostname()) {
			return base.RoundTrip(r)
		}
		downloads++
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(cacheTestMP3)), Body: io.NopCloser(strings.NewReader(cacheTestMP3)), Request: r}, nil
	})
	defer func() { http.DefaultTransport = base }()
	ownedProviderFromEnv(t, cache.URL, upstream.URL)
	t.Setenv("MUSIC_MINIO_CACHE_WRITE_ENABLED", "true")
	r, err := OptionsFromEnv(nil).Provider.Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
	if err != nil || r.Status != "available" || !strings.Contains(r.Reason, "cache_skipped_existing_unverified_audio") || puts != 0 || downloads != 0 {
		t.Fatalf("existing unverified object was overwritten or trusted: status=%s reason=%s puts=%d downloads=%d err=%v", r.Status, r.Reason, puts, downloads, err)
	}
}
