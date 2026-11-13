package music

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type routedFixture struct {
	source      string
	calls       []string
	unavailable bool
	forge       bool
}

func (p *routedFixture) track() Track {
	return Track{ID: p.source + ":629335603", Source: p.source, ExternalID: "629335603", Title: "Same composition (different recording)", Artist: p.source + " artist", Album: p.source + " edition", DurationMs: 75000}
}
func (p *routedFixture) Search(context.Context, string) ([]Track, error) {
	p.calls = append(p.calls, "search")
	if p.unavailable {
		return nil, unavailable
	}
	track := p.track()
	if p.forge {
		track.Source = "netease"
		track.ID = "netease:629335603"
	}
	return []Track{track}, nil
}
func (p *routedFixture) Detail(_ context.Context, id string) (Track, error) {
	p.calls = append(p.calls, "detail:"+id)
	if p.unavailable {
		return Track{}, unavailable
	}
	track := p.track()
	if id != track.ID {
		return Track{}, missing
	}
	return track, nil
}
func (p *routedFixture) Resolve(_ context.Context, track Track) (Resolution, error) {
	p.calls = append(p.calls, "resolve:"+track.ID)
	if p.unavailable {
		return Resolution{}, unavailable
	}
	return Resolution{TrackID: track.ID, Status: "unavailable", Reason: "recording_restricted"}, nil
}
func (p *routedFixture) Lyrics(_ context.Context, track Track) (Lyrics, error) {
	p.calls = append(p.calls, "lyrics:"+track.ID)
	return Lyrics{TrackID: track.ID, LRC: "[00:00]" + p.source}, nil
}
func (p *routedFixture) Artwork(_ context.Context, id string) (string, error) {
	p.calls = append(p.calls, "artwork:"+id)
	return "https://objects.test/" + p.source + "/629335603.jpg", nil
}

func TestSourceRouterNeverSubstitutesStableRecording(t *testing.T) {
	primary, other := &routedFixture{source: "netease"}, &routedFixture{source: "kuwo"}
	router := SourceRouter{Primary: primary, Sources: map[string]Provider{"kuwo": other}}
	tracks, err := router.Search(context.Background(), "same title")
	if err != nil || len(tracks) != 1 || tracks[0].ID != "netease:629335603" || len(other.calls) != 0 {
		t.Fatalf("default source changed: %#v %v", tracks, err)
	}
	tracks, err = router.SearchSource(context.Background(), "kuwo", "same title")
	if err != nil || len(tracks) != 1 || tracks[0].ID != "kuwo:629335603" {
		t.Fatalf("explicit source identity lost: %#v %v", tracks, err)
	}
	exact, err := router.Detail(context.Background(), tracks[0].ID)
	if err != nil || exact.Source != "kuwo" {
		t.Fatal("detail routed to wrong platform")
	}
	if _, err = router.Resolve(context.Background(), exact); err != nil {
		t.Fatal(err)
	}
	lyrics, err := router.Lyrics(context.Background(), exact)
	if err != nil || lyrics.TrackID != exact.ID || !strings.Contains(lyrics.LRC, "kuwo") {
		t.Fatal("lyrics crossed source")
	}
	art, err := router.Artwork(context.Background(), exact.ID)
	if err != nil || !strings.Contains(art, "/kuwo/") {
		t.Fatal("artwork crossed source")
	}
	if len(primary.calls) != 1 {
		t.Fatalf("alternative consulted primary: %v", primary.calls)
	}
	primary.unavailable = true
	before := len(other.calls)
	if _, err = router.Search(context.Background(), "same title"); err == nil || len(other.calls) != before {
		t.Fatal("outage silently enabled alternative")
	}
	if _, err = router.SearchSource(context.Background(), "unknown", "same title"); err == nil {
		t.Fatal("unconfigured source accepted")
	}
	forged := exact
	forged.ID = "netease:629335603"
	if _, err = router.Resolve(context.Background(), forged); err == nil {
		t.Fatal("source/ID mismatch accepted")
	}
	other.forge = true
	if _, err = router.SearchSource(context.Background(), "kuwo", "same title"); err == nil {
		t.Fatal("provider poisoned another platform ID")
	}
}

func TestCatalogueSourcePublicOptionsAndAccountDataIsolation(t *testing.T) {
	db := testDB(t)
	primary, other := &routedFixture{source: "netease"}, &routedFixture{source: "kuwo"}
	router := SourceRouter{Primary: primary, Sources: map[string]Provider{"kuwo": other}}
	handler := NewServer(db, Options{AppID: "source-isolation", Provider: router}).HTTP()
	out := call(t, handler, "", "GET", "/catalog/options", nil, http.StatusOK)
	if out["defaultSource"] != "netease" || len(out["sources"].([]any)) != 2 || strings.Contains(strings.ToLower(sourceJSON(out)), "127.0.0.1") {
		t.Fatalf("source discovery leaked endpoints or wrong default: %#v", out)
	}
	a, b := guest(t, handler), guest(t, handler)
	netease := call(t, handler, a, "GET", "/search?q=same", nil, http.StatusOK)["tracks"].([]any)[0].(map[string]any)
	kuwo := call(t, handler, a, "GET", "/search?q=same&source=kuwo", nil, http.StatusOK)["tracks"].([]any)[0].(map[string]any)
	if netease["id"] != "netease:629335603" || kuwo["id"] != "kuwo:629335603" {
		t.Fatal("same numeric external ID collided across platforms")
	}
	call(t, handler, a, "GET", "/search?q=same&source=unknown", nil, http.StatusBadRequest)
	call(t, handler, a, "PUT", "/likes/kuwo:629335603", map[string]any{}, http.StatusOK)
	own := call(t, handler, a, "GET", "/likes", nil, http.StatusOK)
	stranger := call(t, handler, b, "GET", "/likes", nil, http.StatusOK)
	if !strings.Contains(sourceJSON(own), "kuwo:629335603") || strings.Contains(sourceJSON(stranger), "629335603") {
		t.Fatal("explicit source personal data crossed accounts")
	}
	call(t, handler, a, "POST", "/tracks/kuwo:629335603/resolve", map[string]any{}, http.StatusOK)
	if strings.Contains(strings.Join(primary.calls, "|"), "resolve:kuwo") {
		t.Fatal("resolve routed Kuwo ID to primary cache")
	}
}

func sourceJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestCatalogueSourceConfigurationKeepsDefaultAndSharesCacheWriterPool(t *testing.T) {
	t.Setenv("MUSIC_MINIO_CACHE_ENABLED", "false")
	t.Setenv("MUSIC_KUWO_API_URL", "")
	if _, ok := OptionsFromEnv(nil).Provider.(SourceRouter); ok {
		t.Fatal("default configuration unnecessarily replaced primary provider")
	}
	t.Setenv("MUSIC_KUWO_API_URL", "http://127.0.0.1:37836")
	router, ok := OptionsFromEnv(nil).Provider.(SourceRouter)
	if !ok || router.Sources["kuwo"] == nil {
		t.Fatal("explicit source not registered")
	}
	t.Setenv("MUSIC_MINIO_CACHE_ENABLED", "true")
	t.Setenv("MUSIC_MINIO_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("MUSIC_MINIO_PUBLIC_ORIGIN", "https://music.example.test")
	t.Setenv("MUSIC_MINIO_BUCKET", "cloudmusic")
	t.Setenv("MUSIC_MINIO_ACCESS_KEY", "test-access")
	t.Setenv("MUSIC_MINIO_SECRET_KEY", "test-secret")
	options := OptionsFromEnv(nil)
	if options.providerErr != nil {
		t.Fatal(options.providerErr)
	}
	router = options.Provider.(SourceRouter)
	primary := router.Primary.(*ownedCacheProvider)
	other := router.Sources["kuwo"].(*ownedCacheProvider)
	if primary.sourceID() != "netease" || other.sourceID() != "kuwo" {
		t.Fatal("cache source namespace changed")
	}
	if primary.writers != other.writers || cap(primary.writers) != 2 {
		t.Fatal("source caches do not share the bounded module writer pool")
	}
}
