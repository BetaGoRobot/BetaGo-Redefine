package music

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPublicPlaylistImportParsesOnlyOfficialIDsAndURLs(t *testing.T) {
	for _, input := range []string{
		"123", " 123 ", "https://music.163.com/playlist?id=123&userid=999",
		"https://music.163.com/#/playlist?id=123&userid=999", "https://music.163.com/playlist/123",
		"https://y.music.163.com/m/playlist?id=123", "music.163.com/#/playlist?id=123",
	} {
		id, err := parseNeteasePlaylistID(input)
		if err != nil || id != "123" {
			t.Errorf("valid official input rejected: id=%q err=%v", id, err)
		}
	}
	for _, input := range []string{
		"", "0", "-1", "0123", "9223372036854775808", "https://evil.test/playlist?id=123",
		"https://music.163.com.evil.test/playlist?id=123", "https://music.163.com@evil.test/playlist?id=123",
		"https://x@music.163.com/playlist?id=123", "http://127.0.0.1/playlist?id=123",
		"file:///playlist/123", "https://music.163.com:37834/playlist?id=123",
		"https://music.163.com/song?id=123", "https://music.163.com/playlist?id=1&id=123",
		"https://music.163.com/playlist/123?id=456", "https://music.163.com/playlist?id=123#evil",
		"https://music.163.com/?id=456#/playlist?id=123", "https://music.163.com\\@evil.test/playlist?id=123",
	} {
		if _, err := parseNeteasePlaylistID(input); err == nil {
			t.Errorf("accepted untrusted/ambiguous input %q", input)
		}
	}
	_, err := parseNeteasePlaylistID("https://163cn.tv/example")
	if ae, ok := err.(*apiError); !ok || ae.Message != "netease_playlist_full_url_required" {
		t.Fatal("short links need an actionable full-link error")
	}
}

type playlistImportFixture struct {
	mu                         sync.Mutex
	ids                        []int64
	status, code, privacy      int
	count                      int
	missing                    int64
	badMetadata                bool
	playlistCalls, detailCalls int
	batchSizes                 []int
	cookieSeen                 bool
}

func newPlaylistImportFixture(t *testing.T, ids []int64) (*playlistImportFixture, *httptest.Server) {
	t.Helper()
	f := &playlistImportFixture{ids: ids, status: 200, code: 200, count: len(ids)}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.URL.Query().Get("cookie") != "" {
			f.cookieSeen = true
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/playlist/detail":
			f.playlistCalls++
			if r.URL.Query().Get("id") != "123" {
				t.Error("user URL rather than canonical ID reached upstream")
			}
			w.WriteHeader(f.status)
			tracks := []map[string]any{}
			for _, id := range f.ids {
				tracks = append(tracks, map[string]any{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": f.code, "playlist": map[string]any{"id": 123, "name": "Public fixture", "privacy": f.privacy, "trackCount": f.count, "trackIds": tracks}})
		case "/song/detail":
			f.detailCalls++
			ids := strings.Split(r.URL.Query().Get("ids"), ",")
			f.batchSizes = append(f.batchSizes, len(ids))
			if len(ids) > 50 {
				t.Error("metadata batch exceeded 50")
			}
			songs := []map[string]any{}
			for i := len(ids) - 1; i >= 0; i-- { // Provider response order is not playlist order.
				id, _ := strconv.ParseInt(ids[i], 10, 64)
				if id == f.missing {
					continue
				}
				name := fmt.Sprintf("Song %d", id)
				if f.badMetadata {
					name = ""
				}
				songs = append(songs, map[string]any{"id": id, "name": name, "dt": 60000, "ar": []map[string]string{{"name": "Public artist"}}, "al": map[string]string{"name": "Public album"}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "songs": songs})
		default:
			t.Errorf("unexpected importer path %q", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(upstream.Close)
	return f, upstream
}

func TestPublicPlaylistUsesAnonymousBoundedMetadataBatchesAndStableOrder(t *testing.T) {
	ids := []int64{}
	for n := int64(1); n <= 101; n++ {
		ids = append(ids, n)
	}
	ids = append(ids, 1, 2)
	f, upstream := newPlaylistImportFixture(t, ids)
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(upstream.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "MUSIC_U", Value: "fixture-account-not-real"}})
	p := AnonymousNetease{BaseURL: upstream.URL, ServerCookie: "MUSIC_U=fixture-account-not-real", Client: &http.Client{Jar: jar}}
	got, err := p.PublicPlaylist(context.Background(), "123")
	if err != nil || got.ID != "123" || got.Name != "Public fixture" || len(got.Tracks) != len(ids) {
		t.Fatalf("incomplete public playlist: tracks=%d err=%v", len(got.Tracks), err)
	}
	for n, track := range got.Tracks {
		if track.ID != "netease:"+strconv.FormatInt(ids[n], 10) || track.Title != fmt.Sprintf("Song %d", ids[n]) {
			t.Fatal("playlist order or same-source stable identity changed")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cookieSeen || f.playlistCalls != 1 || f.detailCalls != 3 {
		t.Fatalf("anonymous/batch boundary failed: cookie=%v playlistCalls=%d detailCalls=%d", f.cookieSeen, f.playlistCalls, f.detailCalls)
	}
}

func TestPublicPlaylistRejectsRestrictedOversizedAndMissingMetadata(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		status, code, privacy int
		count                 int
		missing               int64
		badMetadata           bool
		want                  int
	}{
		{"HTTP private", 403, 200, 0, 2, 0, false, 403},
		{"code private", 200, 403, 0, 2, 0, false, 403},
		{"login required", 200, 301, 0, 2, 0, false, 403},
		{"privacy flag", 200, 200, 10, 2, 0, false, 403},
		{"over limit", 200, 200, 0, 1001, 0, false, 413},
		{"track list incomplete", 200, 200, 0, 3, 0, false, 422},
		{"missing metadata", 200, 200, 0, 2, 2, false, 422},
		{"invalid metadata", 200, 200, 0, 2, 0, true, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, upstream := newPlaylistImportFixture(t, []int64{1, 2})
			f.status, f.code, f.privacy, f.count, f.missing, f.badMetadata = tc.status, tc.code, tc.privacy, tc.count, tc.missing, tc.badMetadata
			_, err := (AnonymousNetease{BaseURL: upstream.URL}).PublicPlaylist(context.Background(), "123")
			ae, ok := err.(*apiError)
			if !ok || ae.Status != tc.want {
				t.Fatalf("wrong public failure: %v", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.cookieSeen || (tc.want == 403 || tc.want == 413) && f.detailCalls != 0 {
				t.Fatal("restricted playlist attempted metadata/account fallback")
			}
		})
	}
	_, upstream := newPlaylistImportFixture(t, nil)
	_, err := (AnonymousNetease{BaseURL: upstream.URL}).PublicPlaylist(context.Background(), "123")
	if ae, ok := err.(*apiError); !ok || ae.Status != 422 || ae.Message != "public_playlist_empty" {
		t.Fatal("empty public playlist was accepted")
	}
}

func TestPublicPlaylistAllowsExactly1000AndRejectsRawOverflow(t *testing.T) {
	ids := make([]int64, 1000)
	for n := range ids {
		ids[n] = int64(n + 1)
	}
	f, upstream := newPlaylistImportFixture(t, ids)
	p := AnonymousNetease{BaseURL: upstream.URL}
	got, err := p.PublicPlaylist(context.Background(), "123")
	if err != nil || len(got.Tracks) != 1000 {
		t.Fatalf("1000-track boundary rejected: count=%d err=%v", len(got.Tracks), err)
	}
	f.mu.Lock()
	if f.detailCalls != 20 {
		t.Fatal("1000 tracks were not fetched in bounded metadata batches")
	}
	f.ids = append(f.ids, 1) // raw entries still exceed the cap even after dedup.
	f.detailCalls = 0
	f.mu.Unlock()
	_, err = p.PublicPlaylist(context.Background(), "123")
	if ae, ok := err.(*apiError); !ok || ae.Status != 413 {
		t.Fatal("raw 1001-entry playlist was not rejected before dedup")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.detailCalls != 0 {
		t.Fatal("oversized playlist fetched metadata")
	}
}

func TestPublicPlaylistRefusesRedirectAndHonorsCancellation(t *testing.T) {
	var followed atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Add(1)
		w.WriteHeader(200)
	}))
	t.Cleanup(destination.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	t.Cleanup(upstream.Close)
	p := AnonymousNetease{BaseURL: upstream.URL}
	_, err := p.PublicPlaylist(context.Background(), "123")
	if ae, ok := err.(*apiError); !ok || ae.Status != 503 || followed.Load() != 0 {
		t.Fatal("anonymous playlist metadata followed a redirect")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.PublicPlaylist(ctx, "123")
	if ae, ok := err.(*apiError); !ok || ae.Status != 503 {
		t.Fatal("cancelled import continued reading source")
	}
}

func TestPublicPlaylistImportCopiesOwnDataAtomicallyAndPersists(t *testing.T) {
	db := testDB(t)
	_, upstream := newPlaylistImportFixture(t, []int64{1, 2, 1})
	opts := Options{AppID: "playlist-import", PlaylistImporter: AnonymousNetease{BaseURL: upstream.URL}}
	h := NewServer(db, opts).HTTP()
	a, b := guest(t, h), guest(t, h)
	existing := call(t, h, a, "POST", "/playlists", map[string]any{"name": "Existing"}, 200)
	input := map[string]any{"urlOrID": "https://music.163.com/#/playlist?id=123&userid=attacker", "name": "Copied playlist"}
	response := call(t, h, a, "POST", "/imports/netease-playlist", input, 200)
	p := response["playlist"].(map[string]any)
	id := p["id"].(string)
	if response["imported"] != float64(2) || response["duplicatesSkipped"] != float64(1) || response["sourcePlaylistID"] != "123" || p["name"] != "Copied playlist" || len(p["tracks"].([]any)) != 2 {
		t.Fatal("owned copy counts, metadata or stable-ID dedup incorrect")
	}
	call(t, h, b, "GET", "/playlists/"+id, nil, 404)
	call(t, h, b, "DELETE", "/playlists/"+id, map[string]any{"revision": 1}, 404)
	second := call(t, h, b, "POST", "/imports/netease-playlist", map[string]any{"urlOrID": "123"}, 200)["playlist"].(map[string]any)
	if second["id"] == p["id"] || second["name"] != "Public fixture" {
		t.Fatal("public playlist was linked to another owner instead of copied")
	}
	restarted := NewServer(db, opts).HTTP()
	readback := call(t, restarted, a, "GET", "/playlists/"+id, nil, 200)
	if len(readback["tracks"].([]any)) != 2 || readback["name"] != "Copied playlist" {
		t.Fatal("import did not persist across HTTP server instances")
	}
	call(t, restarted, a, "GET", "/playlists/"+existing["id"].(string), nil, 200)
	call(t, restarted, a, "POST", "/imports/netease-playlist", map[string]any{"urlOrID": "https://127.0.0.1/playlist?id=123"}, 400)
	call(t, restarted, a, "POST", "/imports/netease-playlist", map[string]any{"urlOrID": "123", "name": strings.Repeat("x", 201)}, 400)
	call(t, restarted, a, "POST", "/imports/netease-playlist", map[string]any{"urlOrID": "123", "owner_id": "forged"}, 400)
	call(t, NewServer(db, Options{AppID: "playlist-import"}).HTTP(), a, "POST", "/imports/netease-playlist", map[string]any{"urlOrID": "123"}, 503)
	call(t, restarted, "", "POST", "/imports/netease-playlist", input, 401)
	call(t, NewServer(db, Options{AppID: "other-app", PlaylistImporter: opts.PlaylistImporter}).HTTP(), a, "POST", "/imports/netease-playlist", input, 401)
}

func TestPublicPlaylistImportFailureLeavesNoPartialPlaylistOrMetadata(t *testing.T) {
	db := testDB(t)
	f, upstream := newPlaylistImportFixture(t, []int64{1, 2})
	h := NewServer(db, Options{AppID: "playlist-import", PlaylistImporter: AnonymousNetease{BaseURL: upstream.URL}}).HTTP()
	a := guest(t, h)
	for _, tc := range []struct {
		status, count int
		missing       int64
		want          int
	}{{403, 2, 0, 403}, {200, 1001, 0, 413}, {200, 2, 2, 422}} {
		f.mu.Lock()
		f.status, f.count, f.missing = tc.status, tc.count, tc.missing
		f.mu.Unlock()
		call(t, h, a, "POST", "/imports/netease-playlist", map[string]any{"urlOrID": "123"}, tc.want)
		var playlists, tracks int64
		db.Table("music_playlists").Count(&playlists)
		db.Table("music_tracks").Count(&tracks)
		if playlists != 0 || tracks != 0 {
			t.Fatal("failed import left partial data")
		}
	}
	f.mu.Lock()
	f.status, f.count, f.missing = 200, 2, 0
	f.mu.Unlock()
	if err := db.Exec(`CREATE FUNCTION music_import_test_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.position=1 THEN RAISE EXCEPTION 'fixture insertion failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER music_import_test_failure BEFORE INSERT ON music_entries FOR EACH ROW EXECUTE FUNCTION music_import_test_failure()`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DROP TRIGGER IF EXISTS music_import_test_failure ON music_entries; DROP FUNCTION IF EXISTS music_import_test_failure()")
	})
	call(t, h, a, "POST", "/imports/netease-playlist", map[string]any{"urlOrID": "123"}, 500)
	for _, table := range []string{"music_playlists", "music_entries", "music_tracks"} {
		var count int64
		db.Table(table).Count(&count)
		if count != 0 {
			t.Fatalf("transaction failure left rows in %s", table)
		}
	}
}
