package music

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type recommendationProvider struct{ tracks []Track }

func (p recommendationProvider) Search(context.Context, string) ([]Track, error) {
	return p.tracks, nil
}
func (p recommendationProvider) Detail(_ context.Context, id string) (Track, error) {
	for _, track := range p.tracks {
		if track.ID == id {
			return track, nil
		}
	}
	return Track{}, missing
}
func (p recommendationProvider) Resolve(context.Context, Track) (Resolution, error) {
	return Resolution{}, unavailable
}
func (p recommendationProvider) Lyrics(context.Context, Track) (Lyrics, error) { return Lyrics{}, nil }

func TestSearchHistoryIsPersistentScopedAndExplicit(t *testing.T) {
	db := testDB(t)
	provider := recommendationProvider{tracks: []Track{{ID: "netease:111", Source: "netease", ExternalID: "111", Title: "Song", Artist: "Artist"}}}
	opts := Options{AppID: "recommend-app", Provider: provider}
	h := NewServer(db, opts).HTTP()
	a, b := guest(t, h), guest(t, h)
	call(t, h, a, "GET", "/search-history", nil, 200)
	call(t, h, a, "GET", "/search?q=automatic-discovery", nil, 200)
	empty := call(t, h, a, "GET", "/search-history", nil, 200)["searches"].([]any)
	if len(empty) != 0 {
		t.Fatal("metadata search implicitly recorded private search history")
	}
	for _, query := range []string{"  Artist   Song ", "Artist Song"} {
		call(t, h, a, "POST", "/search-history", map[string]any{"query": query, "source": "netease"}, 200)
	}
	h = NewServer(db, opts).HTTP()
	rows := call(t, h, a, "GET", "/search-history", nil, 200)["searches"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["query"] != "Artist Song" || rows[0].(map[string]any)["count"] != float64(2) {
		t.Fatal("search history did not persist, normalize and coalesce")
	}
	if len(call(t, h, b, "GET", "/search-history", nil, 200)["searches"].([]any)) != 0 {
		t.Fatal("private query leaked to another account")
	}
	call(t, h, b, "POST", "/search-history", map[string]any{"query": "owned by B", "source": "netease", "owner_id": "forged"}, 400)
	call(t, h, b, "DELETE", "/search-history", map[string]any{}, 200)
	if len(call(t, h, a, "GET", "/search-history", nil, 200)["searches"].([]any)) != 1 {
		t.Fatal("another user cleared my history")
	}
	call(t, h, a, "POST", "/search-history", map[string]any{"query": strings.Repeat("x", 201), "source": "netease"}, 400)
	call(t, h, a, "POST", "/search-history", map[string]any{"query": "ok", "source": "unknown"}, 400)
	call(t, h, a, "DELETE", "/search-history", map[string]any{}, 200)
	if len(call(t, h, a, "GET", "/search-history", nil, 200)["searches"].([]any)) != 0 {
		t.Fatal("clear did not persist")
	}
}

func TestRecommendationsUseOnlyOwnInterestsAndPreserveTrackIdentity(t *testing.T) {
	db := testDB(t)
	tracks := []Track{
		{ID: "netease:111", Source: "netease", ExternalID: "111", Title: "Liked", Artist: "Artist A", DurationMs: 60000},
		{ID: "netease:112", Source: "netease", ExternalID: "112", Title: "Same title", Artist: "Artist A", DurationMs: 60000},
		{ID: "kuwo:112", Source: "kuwo", ExternalID: "112", Title: "Same title", Artist: "Artist A", DurationMs: 60000},
		{ID: "netease:113", Source: "netease", ExternalID: "113", Title: "Other", Artist: "Artist B", DurationMs: 60000},
	}
	for _, track := range tracks {
		if err := saveTrack(db, track); err != nil {
			t.Fatal(err)
		}
	}
	h := NewServer(db, Options{AppID: "recommend-app", Provider: recommendationProvider{tracks: tracks}, Now: func() time.Time { return time.Now().UTC() }}).HTTP()
	a, b := guest(t, h), guest(t, h)
	call(t, h, a, "PUT", "/likes/netease:111", map[string]any{}, 200)
	call(t, h, b, "PUT", "/likes/netease:113", map[string]any{}, 200)
	result := call(t, h, a, "GET", "/recommendations?limit=10", nil, 200)
	if result["personalized"] != true {
		t.Fatal("own interest was not used")
	}
	seen := map[string]bool{}
	for _, raw := range result["tracks"].([]any) {
		item := raw.(map[string]any)
		track := item["track"].(map[string]any)
		id := track["id"].(string)
		if seen[id] || id == "netease:111" {
			t.Fatal("recommendations repeated a track or an existing like")
		}
		seen[id] = true
		if item["reason"] == "" || item["basis"] == "" {
			t.Fatal("recommendation has no transparent basis")
		}
	}
	if !seen["netease:112"] || !seen["kuwo:112"] {
		t.Fatal("stable source IDs were collapsed by title")
	}
	other := call(t, h, b, "GET", "/recommendations?limit=10", nil, 200)
	for _, raw := range other["tracks"].([]any) {
		item := raw.(map[string]any)
		if strings.Contains(item["reason"].(string), "Artist A") && item["basis"] == "likes" {
			t.Fatal("another account's interest leaked into reasons")
		}
	}
	call(t, h, a, "GET", "/recommendations?limit=51", nil, 400)
	if got := request(h, "", "GET", "/recommendations", nil).Code; got != 401 {
		t.Fatalf("anonymous interest access=%d", got)
	}
}

func TestSearchHistoryConcurrentUpsertsAreBoundedAndClearIsOwnerScoped(t *testing.T) {
	db := testDB(t)
	h := NewServer(db, Options{AppID: "search-bounds"}).HTTP()
	token := guest(t, h)
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- request(h, token, "POST", "/search-history", map[string]any{"query": "同一首歌", "source": "netease"}).Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatalf("concurrent history=%d", code)
		}
	}
	rows := call(t, h, token, "GET", "/search-history", nil, 200)["searches"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["count"] != float64(8) {
		t.Fatal("concurrent search count was lost")
	}
	for i := 0; i < 105; i++ {
		call(t, h, token, "POST", "/search-history", map[string]any{"query": fmt.Sprintf("query-%03d", i), "source": "netease"}, 200)
	}
	rows = call(t, h, token, "GET", "/search-history?limit=100", nil, 200)["searches"].([]any)
	if len(rows) != 100 {
		t.Fatal("history did not retain its bounded recent window")
	}
	call(t, h, token, "GET", "/search-history?limit=101", nil, 400)
}

func TestTrustedBotRecommendationLookupRequiresExactApplicationTenantAndSubject(t *testing.T) {
	db := testDB(t)
	tracks := []Track{{ID: "netease:111", Source: "netease", ExternalID: "111", Title: "Seed A", Artist: "Only A"}, {ID: "netease:112", Source: "netease", ExternalID: "112", Title: "Candidate A", Artist: "Only A"}, {ID: "netease:113", Source: "netease", ExternalID: "113", Title: "Seed B", Artist: "Only B"}, {ID: "netease:114", Source: "netease", ExternalID: "114", Title: "Candidate B", Artist: "Only B"}}
	for _, track := range tracks {
		if err := saveTrack(db, track); err != nil {
			t.Fatal(err)
		}
	}
	identityA := Identity{AppID: "app-a", TenantKey: "tenant-a", Subject: "same-subject"}
	identityB := Identity{AppID: "app-a", TenantKey: "tenant-b", Subject: "same-subject"}
	for n, identity := range []Identity{identityA, identityB} {
		h := NewServer(db, Options{AppID: identity.AppID, Verifier: fakeVerifier{identity}}).HTTP()
		login := call(t, h, "", "POST", "/auth/feishu/exchange", map[string]any{"code": "verified-test-only", "transport": "bearer"}, 200)
		id := "netease:111"
		if n == 1 {
			id = "netease:113"
		}
		call(t, h, login["sessionToken"].(string), "PUT", "/likes/"+id, map[string]any{}, 200)
	}
	for _, identity := range []Identity{{AppID: "app-b", TenantKey: "tenant-a", Subject: "same-subject"}, {AppID: "app-a", TenantKey: "tenant-a", Subject: "wrong"}, {AppID: "app-a", Subject: "same-subject"}} {
		if _, err := RecommendForIdentity(context.Background(), db, nil, identity, 5); err == nil {
			t.Fatal("unbound identity accessed personal recommendations")
		}
	}
	result, err := RecommendForIdentity(context.Background(), db, nil, identityA, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Personalized || len(result.Tracks) == 0 || result.Tracks[0].Track.ID != "netease:112" {
		t.Fatal("trusted identity did not get its own scoped interest")
	}
	for _, item := range result.Tracks {
		if item.Basis == "likes" && strings.Contains(item.Reason, "Only B") {
			t.Fatal("other tenant's private preference leaked")
		}
	}
}

func TestRecommendationsDoNotClaimPersonalizationWhenNoInterestMatches(t *testing.T) {
	db := testDB(t)
	track := Track{ID: "netease:111", Source: "netease", ExternalID: "111", Title: "Public discovery", Artist: "Unrelated artist"}
	if err := saveTrack(db, track); err != nil {
		t.Fatal(err)
	}
	h := NewServer(db, Options{AppID: "honest-discovery", Provider: recommendationProvider{tracks: []Track{track}}}).HTTP()
	token := guest(t, h)
	call(t, h, token, "POST", "/search-history", map[string]any{"query": "no matching artist", "source": "netease"}, 200)
	out := call(t, h, token, "GET", "/recommendations", nil, 200)
	if out["personalized"] != false {
		t.Fatal("generic discovery was falsely labeled personalized")
	}
}
