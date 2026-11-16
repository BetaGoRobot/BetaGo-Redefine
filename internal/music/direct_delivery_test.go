package music

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// These HTTP fixtures validate source orchestration, never real-song playback.
type webFixture struct {
	t                                                               *testing.T
	mu                                                              sync.Mutex
	events                                                          []string
	objects                                                         map[string]string
	externalOK                                                      bool
	accountResponse                                                 string
	accountHTTP                                                     int
	cacheDenied                                                     string
	cacheHTTP                                                       int
	anonymousEmpty                                                  bool
	accountEntered                                                  chan struct{}
	accountRelease                                                  <-chan struct{}
	accountActive, accountMaximum                                   atomic.Int32
	bridge, catalogue, cache                                        *httptest.Server
	probes, fullDownloads, puts, anonymousAudio, accounts, metadata atomic.Int32
}

func newWebFixture(t *testing.T) *webFixture {
	t.Helper()
	f := &webFixture{t: t, objects: map[string]string{}, accountHTTP: 200}
	f.catalogue = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("application credential reached catalogue")
		}
		if r.URL.Path == "/song/url/v1" || r.URL.Path == "/song/url" {
			if r.Header.Get("Cookie") == "" {
				f.anonymousAudio.Add(1)
				if f.anonymousEmpty {
					fmt.Fprintf(w, `{"code":200,"data":[{"id":%s,"code":200,"url":null}]}`, r.URL.Query().Get("id"))
					return
				}
				fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":200,"url":"https://m801.music.126.net/anonymous.mp3"}]}`)
				return
			}
			if r.Header.Get("Cookie") != "MUSIC_U=operator-test-only" {
				t.Error("unexpected playback credential")
			}
			f.accounts.Add(1)
			f.event("account")
			if f.accountEntered != nil {
				active := f.accountActive.Add(1)
				defer f.accountActive.Add(-1)
				for old := f.accountMaximum.Load(); active > old && !f.accountMaximum.CompareAndSwap(old, active); old = f.accountMaximum.Load() {
				}
				f.accountEntered <- struct{}{}
				<-f.accountRelease
			}
			w.WriteHeader(f.accountHTTP)
			if f.accountResponse != "" {
				fmt.Fprint(w, f.accountResponse)
				return
			}
			fmt.Fprintf(w, `{"code":200,"data":[{"id":%s,"code":200,"url":"https://m801.music.126.net/account.mp3?temporary=not-persisted","expi":45}]}`, r.URL.Query().Get("id"))
			return
		}
		f.metadata.Add(1)
		if r.Header.Get("Cookie") != "" {
			t.Error("metadata consumed account cookie")
		}
		switch r.URL.Path {
		case "/cloudsearch":
			fmt.Fprint(w, `{"code":200,"result":{"songs":[{"id":42,"name":"canonical","dt":2000,"ar":[{"name":"artist"}]}]}}`)
		case "/song/detail":
			fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"canonical","dt":2000,"ar":[{"name":"artist"}]}]}`)
		case "/lyric":
			fmt.Fprint(w, `{"code":200,"lrc":{"lyric":"[00:01]canonical"}}`)
		default:
			t.Errorf("unexpected catalogue route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	f.bridge = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.event("external")
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("bridge received credentials")
		}
		if !f.externalOK {
			w.WriteHeader(404)
			return
		}
		decoded, err := url.QueryUnescape(r.URL.RawQuery)
		var args map[string]string
		if r.URL.Path != "/pyncmd/check" || err != nil || json.Unmarshal([]byte(decoded), &args) != nil || args["id"] != "42" || args["requestID"] == "" {
			t.Error("bridge changed original ID or fresh nonce")
		}
		fmt.Fprint(w, "https://m801.music.126.net/external.mp3?temporary=not-persisted")
	}))
	f.cache = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.event("cache")
		if r.Method == "PUT" {
			f.puts.Add(1)
			w.WriteHeader(500)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			t.Error("unexpected object mutation")
		}
		if r.URL.Path == f.cacheDenied {
			status := f.cacheHTTP
			if status == 0 {
				status = 403
			}
			w.WriteHeader(status)
			fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
			return
		}
		value, ok := f.objects[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(value)))
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("ETag", `"fixed"`)
		if r.Method == "GET" {
			fmt.Fprint(w, value)
		}
	}))
	base := http.DefaultTransport
	http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "m801.music.126.net" {
			return base.RoundTrip(r)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("CDN received credentials")
		}
		if r.Header.Get("Range") == "" {
			f.fullDownloads.Add(1)
		} else {
			f.probes.Add(1)
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Type": {"audio/mpeg"}, "Content-Range": {"bytes 0-13/40000"}}, ContentLength: int64(len(cacheTestMP3)), Body: io.NopCloser(strings.NewReader(cacheTestMP3)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = base; f.bridge.Close(); f.catalogue.Close(); f.cache.Close() })
	return f
}

func (f *webFixture) event(value string) {
	f.mu.Lock()
	f.events = append(f.events, value)
	f.mu.Unlock()
}
func (f *webFixture) sequence() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for _, value := range f.events {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}
func (f *webFixture) cachedTrack() {
	f.objects["/cloudmusic/info/42.json"] = `{"trackID":"netease:42","title":"canonical","artist":"artist","durationMs":2000}`
	f.objects["/cloudmusic/music/42.mp3"] = "existing-object-is-not-a-real-playback-test"
}
func (f *webFixture) options() Options {
	f.t.Helper()
	f.t.Setenv("MUSIC_UNM_BRIDGE_URL", f.bridge.URL)
	f.t.Setenv("MUSIC_NETEASE_COOKIE", "MUSIC_U=operator-test-only")
	f.t.Setenv("MUSIC_KUWO_API_URL", "")
	ownedProviderFromEnv(f.t, f.cache.URL, f.catalogue.URL)
	f.t.Setenv("MUSIC_MINIO_CACHE_WRITE_ENABLED", "true")
	return OptionsFromEnv(nil)
}

func webResolve(t *testing.T, f *webFixture, direct bool, input any) map[string]any {
	t.Helper()
	opts := f.options()
	opts.AppID = "web-direct-tests"
	h := NewServer(testDB(t), opts).HTTP()
	token := guest(t, h)
	raw, _ := json.Marshal(input)
	r := httptest.NewRequest("POST", "http://music.test/music-api/v1/tracks/netease:42/resolve", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	if direct {
		r.Header.Set("X-Music-Delivery", "direct")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("resolve status %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestWebDirectNeverReadsExistingMinIOEvenWhenExternalFails(t *testing.T) {
	for _, outcome := range []string{"account", "preview", "unavailable"} {
		t.Run(outcome, func(t *testing.T) {
			f := newWebFixture(t)
			f.cachedTrack()
			if outcome == "preview" {
				f.accountResponse = `{"code":200,"data":[{"id":42,"url":"https://m801.music.126.net/preview.mp3","freeTrialInfo":{"end":30}}]}`
			}
			if outcome == "unavailable" {
				f.accountHTTP = 403
			}
			// No anonymous source prevents account fallback in this fixture unless the same ID has no URL.
			f.anonymousEmpty = true
			out := webResolve(t, f, true, map[string]any{"delivery": "direct"})
			if out["reason"] == "owned_minio_cache" {
				t.Fatal("web returned old MinIO audio")
			}
			for _, event := range f.sequence() {
				if event == "cache" {
					t.Fatal("web contacted MinIO")
				}
			}
			want := "available"
			if outcome != "account" {
				want = outcome
			}
			if out["status"] != want {
				t.Fatalf("status=%v want=%s", out["status"], want)
			}
			if f.fullDownloads.Load() != 0 || f.puts.Load() != 0 {
				t.Fatal("web downloaded or cached whole audio")
			}
		})
	}
}
func TestWebDirectBodyAloneBypassesCacheAndDefaultStillUsesIt(t *testing.T) {
	for _, direct := range []bool{true, false} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			f := newWebFixture(t)
			f.externalOK = true
			f.cachedTrack()
			input := map[string]any{}
			if direct {
				input["delivery"] = "direct"
			}
			out := webResolve(t, f, false, input)
			if direct {
				if out["mediaSource"] != "unm:pyncmd" {
					t.Fatalf("direct source=%v", out)
				}
				for _, e := range f.sequence() {
					if e == "cache" {
						t.Fatal("direct body contacted cache")
					}
				}
			} else if out["reason"] != "owned_minio_cache" {
				t.Fatal("legacy cache behavior changed")
			}
		})
	}
}
func TestWebDirectMetadataAndLyricsBypassMinIO(t *testing.T) {
	f := newWebFixture(t)
	f.cachedTrack()
	opts := f.options()
	opts.AppID = "web-metadata"
	h := NewServer(testDB(t), opts).HTTP()
	token := guest(t, h)
	for _, path := range []string{"/tracks/netease:42", "/tracks/netease:42/lyrics"} {
		r := httptest.NewRequest("GET", "http://music.test/music-api/v1"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Music-Delivery", "direct")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	for _, event := range f.sequence() {
		if event == "cache" {
			t.Fatal("web metadata or lyrics contacted MinIO")
		}
	}
}

func TestWebDirectProviderIgnoresCacheWithoutHTTPOrDatabase(t *testing.T) {
	for _, externalOK := range []bool{true, false} {
		t.Run(strconv.FormatBool(externalOK), func(t *testing.T) {
			f := newWebFixture(t)
			f.cachedTrack()
			f.externalOK = externalOK
			f.anonymousEmpty = true
			out, err := f.options().Provider.Resolve(context.WithValue(context.Background(), directDeliveryKey{}, true), canonicalFixture("42"))
			if err != nil || out.Status != "available" || out.Delivery != "direct" {
				t.Fatalf("direct contract lost: status=%s delivery=%s reason=%s err=%v", out.Status, out.Delivery, out.Reason, err)
			}
			if !directAudioURL(out.URL, "netease") {
				t.Fatal("returned owned/unsafe URL")
			}
			for _, e := range f.sequence() {
				if e == "cache" {
					t.Fatal("web read MinIO")
				}
			}
			if f.fullDownloads.Load() != 0 || f.puts.Load() != 0 {
				t.Fatal("web downloaded or cached audio")
			}
		})
	}
}
func TestWebDirectRejectsOwnedMediaHostEvenWhenConfigured(t *testing.T) {
	f := newWebFixture(t)
	f.options()
	t.Setenv("MUSIC_UNM_BRIDGE_URL", "")
	t.Setenv("MUSIC_OWNED_MEDIA_HOST", "music.example.test")
	// A poisoned adapter URL cannot become a web audio destination.
	p := &directDeliveryProvider{direct: &fixedDirectProvider{url: "https://music.example.test/cloudmusic/music/42.mp3"}}
	out, err := p.Resolve(context.WithValue(context.Background(), directDeliveryKey{}, true), canonicalFixture("42"))
	if err != nil || out.Status != "unavailable" || out.URL != "" {
		t.Fatal("owned URL accepted for direct delivery")
	}
}

type fixedDirectProvider struct {
	canonicalTestProvider
	url string
}

func (p *fixedDirectProvider) Resolve(_ context.Context, t Track) (Resolution, error) {
	return Resolution{TrackID: t.ID, Status: "available", URL: p.url}, nil
}
