package music

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPyncmdRejectsUnsafeCandidatesWithoutContactOrAccount(t *testing.T) {
	for _, candidate := range []string{
		"http://m801.music.126.net/audio.mp3",
		"https://m801.music.126.net.attacker.invalid/audio.mp3",
		"https://127.0.0.1/audio.mp3",
		"https://user:pass@m801.music.126.net/audio.mp3",
		"https://m801.music.126.net:8443/audio.mp3",
		"https://m801.music.126.net/audio.mp3#fragment",
		"file:///tmp/audio.mp3",
		`{"url":"https://m801.music.126.net/audio.mp3"}`,
	} {
		t.Run(candidate, func(t *testing.T) {
			var anonymous, account, bridgeCalls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "" {
					account.Add(1)
				} else {
					anonymous.Add(1)
				}
				fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":200,"url":"https://m801.music.126.net/anonymous.mp3"}]}`)
			}))
			defer api.Close()
			bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { bridgeCalls.Add(1); fmt.Fprint(w, candidate) }))
			defer bridge.Close()
			base := http.DefaultTransport
			http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() != "127.0.0.1" {
					t.Errorf("unsafe candidate contacted: %s", r.URL.Hostname())
					return nil, fmt.Errorf("denied")
				}
				return base.RoundTrip(r)
			})
			defer func() { http.DefaultTransport = base }()
			p := newNeteasePipeline(AnonymousNetease{BaseURL: api.URL}, bridge.URL, "owned-test")
			r, err := p.Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
			if err != nil || r.Status != "available" || r.MediaSource != "netease:anonymous" || anonymous.Load() != 1 || account.Load() != 0 || bridgeCalls.Load() != 1 {
				t.Fatalf("unsafe fallback: %#v %v anon=%d account=%d bridge=%d", r, err, anonymous.Load(), account.Load(), bridgeCalls.Load())
			}
		})
	}
}

func TestPyncmdFailureNegativeTTLAndRedirectIsolation(t *testing.T) {
	var bridgeCalls, exfiltrated atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { exfiltrated.Add(1) }))
	defer destination.Close()
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bridgeCalls.Add(1)
		http.Redirect(w, r, destination.URL+"/capture", 307)
	}))
	defer bridge.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("redirect upgraded to account")
		}
		fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":200,"url":"https://m801.music.126.net/anonymous.mp3"}]}`)
	}))
	defer api.Close()
	p := newNeteasePipeline(AnonymousNetease{BaseURL: api.URL}, bridge.URL, "test-secret")
	now := time.Now()
	p.now = func() time.Time { return now }
	track := Track{ID: "netease:42", Source: "netease", ExternalID: "42"}
	p.Resolve(context.Background(), track)
	p.Resolve(context.Background(), track)
	if bridgeCalls.Load() != 1 || exfiltrated.Load() != 0 {
		t.Fatalf("redirect/negative TTL violation: bridge=%d destination=%d", bridgeCalls.Load(), exfiltrated.Load())
	}
	now = now.Add(16 * time.Second)
	p.Resolve(context.Background(), track)
	if bridgeCalls.Load() != 2 {
		t.Fatal("UNM negative TTL did not expire")
	}
}
