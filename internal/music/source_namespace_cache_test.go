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
)

func TestOwnedCacheSeparatesEqualExternalIDsAcrossSources(t *testing.T) {
	objects := map[string][]byte{}
	for _, source := range []string{"netease", "kuwo"} {
		prefix := ""
		if source != "netease" {
			prefix = source + "/"
		}
		track := Track{ID: source + ":7", Source: source, ExternalID: "7", Title: source + " recording", DurationMs: 75000}
		key := "music/" + prefix + "7.mp3"
		manifest, _ := json.Marshal(ownedManifest{Format: "betago-music-cache", Version: 1, Status: "available", Track: track, AudioObject: key})
		objects["/cloudmusic/catalog/"+prefix+"7.json"] = manifest
		objects["/cloudmusic/"+key] = []byte(cacheTestMP3)
		objects["/cloudmusic/lyrics/"+prefix+"7.json"] = []byte(fmt.Sprintf(`{"code":200,"lrc":{"lyric":"[00:00]%s recording"}}`, source))
	}
	cache := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := objects[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		if r.Method != "HEAD" {
			w.Write(data)
		}
	}))
	defer cache.Close()
	for _, source := range []string{"netease", "kuwo"} {
		provider, err := newOwnedCacheProvider(ownedCacheConfig{endpoint: cache.URL, publicOrigin: "https://music.example.test", bucket: "cloudmusic", accessKey: "test-access", secretKey: "test-secret", source: source}, nil)
		if err != nil {
			t.Fatal(err)
		}
		p := provider.(*ownedCacheProvider)
		track, err := p.Detail(context.Background(), source+":7")
		if err != nil || track.Source != source || track.Title != source+" recording" {
			t.Fatalf("source collision: %#v %v", track, err)
		}
		res, err := p.Resolve(context.Background(), track)
		if err != nil || res.Status != "available" {
			t.Fatalf("resolve: %#v %v", res, err)
		}
		u, _ := url.Parse(res.URL)
		want := "/cloudmusic/music/7.mp3"
		if source == "kuwo" {
			want = "/cloudmusic/music/kuwo/7.mp3"
		}
		if u.Path != want {
			t.Fatalf("wrong source object: %s", u.Path)
		}
		lyrics, err := p.Lyrics(context.Background(), track)
		if err != nil || lyrics.TrackID != track.ID || !strings.Contains(lyrics.LRC, source+" recording") {
			t.Fatalf("lyric collision: %#v %v", lyrics, err)
		}
		other := "kuwo"
		if source == "kuwo" {
			other = "netease"
		}
		if _, _, err := p.lookup(context.Background(), other+":7"); err != ownedMiss {
			t.Fatalf("cross-source lookup accepted: %v", err)
		}
	}
}

func TestOwnedCacheAudioHostMustMatchItsConfiguredSource(t *testing.T) {
	for _, tc := range []struct {
		source, host string
		allowed      bool
	}{
		{"netease", "m801.music.126.net", true}, {"netease", "kw-lv.kuwo.cn", false},
		{"kuwo", "kw-lv.kuwo.cn", true}, {"kuwo", "m801.music.126.net", false},
		{"kuwo", "kw-lv.kuwo.cn.attacker.invalid", false},
	} {
		p := &ownedCacheProvider{source: tc.source}
		if p.approvedMediaHost(tc.host) != tc.allowed {
			t.Fatalf("wrong source trust: %#v", tc)
		}
	}
}
