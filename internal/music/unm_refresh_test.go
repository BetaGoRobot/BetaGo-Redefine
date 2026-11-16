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
	"sync"
	"testing"
	"time"
)

func TestPyncmdRefreshBypassesBothArgumentCachesWithoutChangingTrackID(t *testing.T) {
	var mu sync.Mutex
	// Both official cache layers use the complete decoded argument as key.
	// Emulate that contract, rather than a server that always returns a new URL.
	providerCache := map[string]string{}
	var arguments []map[string]string
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pyncmd/check" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("refresh used a different route or credentials")
		}
		decoded, err := url.QueryUnescape(r.URL.RawQuery)
		var argument map[string]string
		if err != nil || json.Unmarshal([]byte(decoded), &argument) != nil {
			t.Error("invalid original-ID argument")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		arguments = append(arguments, argument)
		candidate, found := providerCache[decoded]
		if !found {
			candidate = fmt.Sprintf("https://m801.music.126.net/refreshed-%d.mp3?temporary=not-persisted", len(providerCache)+1)
			providerCache[decoded] = candidate
		}
		mu.Unlock()
		fmt.Fprint(w, candidate)
	}))
	defer bridge.Close()
	base := http.DefaultTransport
	http.DefaultTransport = sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "m801.music.126.net" {
			return base.RoundTrip(r)
		}
		if r.Header.Get("Range") == "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("refresh lost bounded credential-free Range verification")
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Type": {"audio/mpeg"}, "Content-Range": {"bytes 0-13/40000"}}, Body: io.NopCloser(strings.NewReader(cacheTestMP3)), ContentLength: int64(len(cacheTestMP3)), Request: r}, nil
	})
	defer func() { http.DefaultTransport = base }()
	p := newNeteasePipeline(AnonymousNetease{}, bridge.URL, "")
	initial := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	now := initial
	p.now = func() time.Time { return now }
	track := Track{ID: "netease:42", Source: "netease", ExternalID: "42"}
	seenURLs := map[string]bool{}
	for _, elapsed := range []time.Duration{0, time.Second, 121 * time.Second} {
		// The second request models a media error inside the same 120-second
		// epoch. The third models expiry. Neither may reuse a bridge cache key.
		now = initial.Add(elapsed)
		r, err := p.Resolve(context.Background(), track)
		if err != nil || r.Status != "available" || r.TrackID != track.ID || r.OriginTrackID != track.ID || r.MediaSource != "unm:pyncmd" {
			t.Fatalf("same-ID refresh failed: status=%s track=%s origin=%s err=%v", r.Status, r.TrackID, r.OriginTrackID, err)
		}
		if r.ExpiresAt == nil || !r.ExpiresAt.Equal(now.Add(120*time.Second)) {
			t.Fatal("refreshed URL lost the bounded 120-second deadline")
		}
		seenURLs[r.URL] = true
	}
	mu.Lock()
	defer mu.Unlock()
	if len(providerCache) != 3 || len(seenURLs) != 3 {
		t.Fatalf("re-resolution reused stale upstream cache: providerCalls=%d distinctURLs=%d", len(providerCache), len(seenURLs))
	}
	seenNonces := map[string]bool{}
	for _, argument := range arguments {
		if argument["id"] != "42" || argument["requestID"] == "" || len(argument) != 2 || seenNonces[argument["requestID"]] {
			t.Fatal("refresh did not keep the original ID with a fresh non-sensitive nonce")
		}
		seenNonces[argument["requestID"]] = true
	}
	if len(p.negative) != 0 || len(p.accountNegative) != 0 || len(p.unmNegative) != 0 {
		t.Fatal("successful temporary source responses entered a persistent/negative cache")
	}
}
