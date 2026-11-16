package music

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func pipelineEnv(t *testing.T, endpoint, bridge string) Provider {
	t.Helper()
	t.Setenv("MUSIC_MINIO_CACHE_ENABLED", "false")
	t.Setenv("MUSIC_KUWO_API_URL", "")
	t.Setenv("MUSIC_NETEASE_URL", endpoint)
	t.Setenv("MUSIC_UNM_BRIDGE_URL", bridge)
	t.Setenv("MUSIC_NETEASE_COOKIE", "MUSIC_U=operator-owned-test-only")
	return OptionsFromEnv(nil).Provider
}

func TestNeteasePipelineMetadataAndSuccessfulAnonymousNeverUseAccount(t *testing.T) {
	var anonymous, account atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			account.Add(1)
		} else {
			anonymous.Add(1)
		}
		switch r.URL.Path {
		case "/cloudsearch":
			fmt.Fprint(w, `{"code":200,"result":{"songs":[]}}`)
		case "/song/detail":
			fmt.Fprint(w, `{"code":200,"songs":[{"id":42,"name":"original","dt":2000}]}`)
		case "/lyric":
			fmt.Fprint(w, `{"code":200,"lrc":{"lyric":"[00:01]original"}}`)
		case "/song/url/v1":
			fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":200,"url":"https://m801.music.126.net/original.mp3"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer api.Close()
	p := pipelineEnv(t, api.URL, "")
	track, err := p.Detail(context.Background(), "netease:42")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Search(context.Background(), "original"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Lyrics(context.Background(), track); err != nil {
		t.Fatal(err)
	}
	r, err := p.Resolve(context.Background(), track)
	if err != nil || r.Status != "available" {
		t.Fatalf("resolve: %#v %v", r, err)
	}
	if account.Load() != 0 || anonymous.Load() != 4 {
		t.Fatalf("ordinary catalogue/playback consumed account: anonymous=%d account=%d", anonymous.Load(), account.Load())
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"mediaSource":"netease:anonymous"`) || !strings.Contains(string(b), `"originTrackID":"netease:42"`) {
		t.Fatalf("source provenance missing: %s", b)
	}
}

func TestNeteasePipelinePyncmdUsesOnlyOriginalIDAndNoCredentials(t *testing.T) {
	var sourceCalls, bridgeCalls, audioCalls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceCalls.Add(1)
		w.WriteHeader(500)
	}))
	defer api.Close()
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bridgeCalls.Add(1)
		if r.URL.Path != "/pyncmd/check" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("bridge route or credentials violated")
		}
		var input struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(r.URL.RawQuery), &input); err != nil || input.ID != "42" {
			// The official bridge decodes the whole query as JSON, not form fields.
			decoded, e := url.QueryUnescape(r.URL.RawQuery)
			if e != nil || json.Unmarshal([]byte(decoded), &input) != nil || input.ID != "42" {
				t.Errorf("wrong original ID: %s", r.URL.RawQuery)
			}
		}
		fmt.Fprint(w, "https://m801.music.126.net/unm-test.mp3?temporary=must-not-persist")
	}))
	defer bridge.Close()
	base := http.DefaultTransport
	http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "m801.music.126.net" {
			return base.RoundTrip(r)
		}
		audioCalls.Add(1)
		if r.Method != "GET" || r.Header.Get("Range") == "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("unsafe audio probe")
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Type": {"audio/mpeg"}, "Content-Range": {"bytes 0-13/40000"}}, Body: io.NopCloser(strings.NewReader(cacheTestMP3)), ContentLength: int64(len(cacheTestMP3)), Request: r}, nil
	})
	defer func() { http.DefaultTransport = base }()
	r, err := pipelineEnv(t, api.URL, bridge.URL).Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42", DurationMs: 2000})
	if err != nil || r.Status != "available" || sourceCalls.Load() != 0 || bridgeCalls.Load() != 1 || audioCalls.Load() != 1 {
		t.Fatalf("UNM original-ID priority missing: status=%s err=%v source=%d bridge=%d audio=%d", r.Status, err, sourceCalls.Load(), bridgeCalls.Load(), audioCalls.Load())
	}
	b, _ := json.Marshal(r)
	for _, field := range []string{`"mediaSource":"unm:pyncmd"`, `"originTrackID":"netease:42"`, `"matchMethod":"provider_external_id"`, `"verification":"audio_prefix"`} {
		if !strings.Contains(string(b), field) {
			t.Fatalf("missing provenance %s: %s", field, b)
		}
	}
	if r.ExpiresAt == nil || r.ExpiresAt.Sub(time.Now()) > 120000000000 {
		t.Fatal("UNM deadline exceeds 120s")
	}
}

func TestNeteasePipelineAccountRequiresEligibleAnonymousResponse(t *testing.T) {
	for _, scenario := range []string{"empty", "preview", "forbidden", "rate_limited", "wrong_id", "wrong_host", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			var anon, account atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "" {
					account.Add(1)
					fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":200,"url":"https://m801.music.126.net/account.mp3"}]}`)
					return
				}
				anon.Add(1)
				switch scenario {
				case "empty":
					fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":200,"url":null}]}`)
				case "preview":
					fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":200,"url":"https://m801.music.126.net/preview.mp3","freeTrialInfo":{"start":0,"end":30}}]}`)
				case "forbidden":
					w.WriteHeader(403)
				case "rate_limited":
					w.WriteHeader(429)
				case "wrong_id":
					fmt.Fprint(w, `{"code":200,"data":[{"id":43,"code":200,"url":null}]}`)
				case "wrong_host":
					fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":200,"url":"https://other.invalid/recording.mp3"}]}`)
				case "malformed":
					fmt.Fprint(w, `broken`)
				}
			}))
			defer api.Close()
			r, _ := pipelineEnv(t, api.URL, "").Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
			eligible := scenario == "empty" || scenario == "preview"
			if anon.Load() == 0 || (account.Load() == 1) != eligible {
				t.Fatalf("unsafe account escalation: anonymous=%d account=%d eligible=%v", anon.Load(), account.Load(), eligible)
			}
			if eligible && r.Status != "available" {
				t.Fatalf("authorized same-ID fallback failed: %#v", r)
			}
		})
	}
}
