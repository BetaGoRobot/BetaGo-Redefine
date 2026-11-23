package music

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func assertPlaylistTracks(t *testing.T, p map[string]any, want ...string) {
	t.Helper()
	tracks, ok := p["tracks"].([]any)
	if !ok || len(tracks) != len(want) {
		t.Fatalf("track count: got %d want %d", len(tracks), len(want))
	}
	for i, v := range tracks {
		if got := v.(map[string]any)["id"]; got != want[i] {
			t.Fatalf("track %d: got %v want %s", i, got, want[i])
		}
	}
}

func TestPlaylistPrependAPIKeepsExistingOrderAndRevision(t *testing.T) {
	db := testDB(t)
	h := NewServer(db, Options{}).HTTP()
	a, b := guest(t, h), guest(t, h)
	ids := seed(t, db)
	makeTrack := func(id string) string {
		t.Helper()
		if err := saveTrack(db, Track{ID: "fixture:" + id, Source: "fixture", ExternalID: id, Title: id}); err != nil {
			t.Fatal(err)
		}
		return "fixture:" + id
	}
	fourth, fifth := makeTrack("fourth"), makeTrack("fifth")
	p := call(t, h, a, "POST", "/playlists", map[string]any{"name": "old", "trackIDs": ids[:2]}, 200)
	path := "/playlists/" + p["id"].(string)
	call(t, h, b, "POST", path+"/tracks", map[string]any{"trackIDs": []string{fifth}}, 404)
	add := func(input ...string) map[string]any {
		return call(t, h, a, "POST", path+"/tracks", map[string]any{"trackIDs": input}, 200)
	}
	got := add(ids[2], ids[0], fourth, ids[2])
	if !reflect.DeepEqual(got["addedIDs"], []any{ids[2], fourth}) || !reflect.DeepEqual(got["existingIDs"], []any{ids[0]}) {
		t.Fatal(got)
	}
	assertPlaylistTracks(t, got["playlist"].(map[string]any), ids[2], fourth, ids[0], ids[1])
	got = add(fourth, ids[2])
	if got["playlist"].(map[string]any)["revision"] != float64(2) {
		t.Fatal("duplicates should not update the revision")
	}
	got = add(fifth)
	assertPlaylistTracks(t, got["playlist"].(map[string]any), fifth, ids[2], fourth, ids[0], ids[1])
	got = call(t, h, a, "PUT", path, map[string]any{"name": "ordered", "trackIDs": ids, "revision": 3}, 200)
	assertPlaylistTracks(t, got, ids...)
}

func TestPlaylistSyncAPIFollowsCompleteSnapshotsAndRejectsLocalEdits(t *testing.T) {
	db := testDB(t)
	f, upstream := newPlaylistImportFixture(t, []int64{1, 2, 3})
	now := time.Now()
	opts := Options{
		PlaylistImporter: AnonymousNetease{BaseURL: upstream.URL},
		Now:              func() time.Time { return now },
	}
	h := NewServer(db, opts).HTTP()
	a, b := guest(t, h), guest(t, h)
	in := map[string]any{"urlOrID": "分享歌单《公开歌单》 https://music.163.com/playlist/123/456/?userid=456 (来自@网易云音乐)"}
	p := call(t, h, a, "POST", "/playlists/sync", in, 200)
	id := p["id"].(string)
	path := "/playlists/" + id
	assertPlaylistTracks(t, p, "netease:1", "netease:2", "netease:3")
	if p["kind"] != "sync" || p["sync"].(map[string]any)["sourcePlaylistID"] != "123" {
		t.Fatal(p)
	}
	ready := p["sync"].(map[string]any)
	call(t, h, a, "POST", "/playlists/sync", in, 409)
	call(t, h, b, "GET", path, nil, 404)
	call(t, h, b, "POST", path+"/sync", map[string]any{}, 404)
	call(t, h, a, "POST", path+"/tracks", map[string]any{"trackIDs": []string{"netease:1"}}, 409)
	call(t, h, a, "PUT", path, map[string]any{"name": "blocked", "trackIDs": []string{}, "revision": 1}, 409)
	call(t, h, a, "PUT", "/likes/netease:1", map[string]any{}, 200)
	call(t, h, a, "PUT", "/queue/testdevice", map[string]any{"trackIDs": []string{"netease:2"}, "currentIndex": 0}, 200)

	now = now.Add(time.Minute)
	f.mu.Lock()
	f.ids, f.count, f.missing = []int64{3, 1, 4}, 3, 4
	f.mu.Unlock()
	call(t, h, a, "POST", path+"/sync", map[string]any{}, 422)
	p = call(t, h, a, "GET", path, nil, 200)
	assertPlaylistTracks(t, p, "netease:1", "netease:2", "netease:3")
	status := p["sync"].(map[string]any)
	if status["lastSuccessAt"] != ready["lastSuccessAt"] || status["lastError"] != "public_playlist_metadata_incomplete" {
		t.Fatal(status)
	}
	call(t, h, a, "POST", path+"/sync", map[string]any{}, http.StatusTooManyRequests)

	now = now.Add(time.Minute)
	f.mu.Lock()
	f.ids, f.count, f.missing = []int64{3, 1, 4, 3}, 4, 0
	f.mu.Unlock()
	p = call(t, h, a, "POST", path+"/sync", map[string]any{}, 200)
	assertPlaylistTracks(t, p, "netease:3", "netease:1", "netease:4", "netease:3")
	if p["sync"].(map[string]any)["lastError"] != nil {
		t.Fatal(p)
	}

	now = now.Add(time.Minute)
	f.mu.Lock()
	f.ids, f.count = nil, 0
	f.mu.Unlock()
	p = call(t, h, a, "POST", path+"/sync", map[string]any{}, 200)
	assertPlaylistTracks(t, p)
	h = NewServer(db, opts).HTTP()
	persisted := call(t, h, a, "GET", path, nil, 200)
	if !reflect.DeepEqual(persisted, p) {
		t.Fatal(fmt.Errorf("restart did not preserve subscription: %+v", persisted))
	}
	call(t, h, a, "DELETE", path, map[string]any{"revision": p["revision"]}, 200)
	call(t, h, a, "GET", path, nil, 404)
}

func TestSyncLinkRestrictsShortLookupAndTarget(t *testing.T) {
	for _, raw := range []string{
		"分享《歌单》：https://music.163.com/#/playlist?id=123（来自网易云音乐）",
		"https://music.163.com/playlist?id=123&userid=456",
		"https://y.music.163.com/m/playlist?id=123",
		"https://music.163.com/playlist/123/456/",
		"music.163.com/playlist/123",
	} {
		if id, err := playlistSyncID(context.Background(), raw, nil); err != nil || id != "123" {
			t.Errorf("rejected supported format %q: %s %v", raw, id, err)
		}
	}
	var requests int
	redirect := "https://y.music.163.com/m/playlist?id=123"
	client := &http.Client{Transport: playlistSyncTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() != "https://163cn.tv/Abc123" || req.Header.Get("Cookie") != "" {
			t.Errorf("unexpected short-link request: %s", req.URL)
		}
		return &http.Response{
			StatusCode: 302, Header: http.Header{"Location": {redirect}},
			Body: io.NopCloser(strings.NewReader("")), Request: req,
		}, nil
	})}
	ctx := context.Background()
	if id, err := playlistSyncID(ctx, "http://163cn.tv/Abc123", client); err != nil || id != "123" {
		t.Fatalf("could not read safe short location: %s %v", id, err)
	}
	for _, raw := range []string{
		"https://163cn.tv:80/Abc123", "https://163cn.tv:/Abc123", "https://163cn.tv/abc/123",
		"https://163cn.tv/Abc123?a=b", "https://x@163cn.tv/Abc123", "https://163cn.tv/#Abc123",
		"https://music.163.com/playlist/123/456?id=999", "https://music.163.com:443/playlist?id=123",
		"https://music.163.com.evil/playlist?id=123",
		"https://music.163.com/m/playlist?id=music.163.com%2Fplaylist%2F123", "https://y.music.163.com/m/song?id=123",
		"https://music.163.com/playlist?id=123 https://music.163.com/playlist?id=456",
	} {
		if _, err := playlistSyncID(ctx, raw, client); err == nil {
			t.Errorf("accepted ambiguous or unsupported URL %q", raw)
		}
	}
	for _, location := range []string{
		"http://127.0.0.1/playlist?id=123", "https://163cn.tv/Abc123",
		"https://y.music.163.com/g/vip-invite-cashier/abc",
	} {
		redirect = location
		if _, err := playlistSyncID(ctx, "https://163cn.tv/Abc123", client); err == nil {
			t.Errorf("accepted non-playlist redirect %q", location)
		}
	}
	if requests != 4 {
		t.Fatalf("rejected inputs or redirect targets caused network requests: %d", requests)
	}
}

type playlistSyncTransport func(*http.Request) (*http.Response, error)

func (f playlistSyncTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fixtureSnapshot func(context.Context, string) (PublicPlaylist, error)

func (f fixtureSnapshot) PublicPlaylist(ctx context.Context, id string) (PublicPlaylist, error) {
	return f(ctx, id)
}

func snapshotWithTrack(id, suffix string) PublicPlaylist {
	return PublicPlaylist{ID: id, Name: "Fixture", Tracks: []Track{{
		ID: "netease:" + suffix, Source: "netease", ExternalID: suffix, Title: "Song", Artist: "Artist", DurationMs: 10000,
	}}}
}

func TestSyncConcurrentClaimAndLeaseCannotPublishStaleResult(t *testing.T) {
	db := testDB(t)
	var seconds, calls atomic.Int64
	seconds.Store(time.Now().Unix())
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	opts := Options{
		Now: func() time.Time { return time.Unix(seconds.Load(), 0) },
		PlaylistImporter: fixtureSnapshot(func(ctx context.Context, id string) (PublicPlaylist, error) {
			switch calls.Add(1) {
			case 1:
				return snapshotWithTrack(id, "1"), nil
			case 2:
				close(started)
				select {
				case <-release:
					return snapshotWithTrack(id, "2"), nil
				case <-ctx.Done():
					return PublicPlaylist{}, ctx.Err()
				}
			default:
				return snapshotWithTrack(id, "3"), nil
			}
		}),
	}
	a := NewServer(db, opts)
	b := NewServer(db, opts)
	token := guest(t, a.HTTP())
	created := call(t, a.HTTP(), token, "POST", "/playlists/sync", map[string]string{"urlOrID": "123"}, 200)
	path := "/playlists/" + created["id"].(string)
	seconds.Add(60)
	finished := make(chan int, 1)
	go func() { finished <- request(a.HTTP(), token, "POST", path+"/sync", struct{}{}).Code }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("manual sync did not start")
	}
	duplicate := call(t, b.HTTP(), token, "POST", path+"/sync", struct{}{}, 409)
	if duplicate["error"] != "playlist_sync_in_progress" || calls.Load() != 2 {
		t.Fatal("duplicate hit source")
	}
	seconds.Add(46)
	assertPlaylistTracks(t, call(t, b.HTTP(), token, "POST", path+"/sync", struct{}{}, 200), "netease:3")
	release <- struct{}{}
	select {
	case code := <-finished:
		if code != 409 {
			t.Fatalf("stale result returned %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale request did not finish")
	}
	p := call(t, a.HTTP(), token, "GET", path, nil, 200)
	assertPlaylistTracks(t, p, "netease:3")
	if p["sync"].(map[string]any)["lastError"] != nil {
		t.Fatal("stale failure overwrote a successful attempt")
	}
}

func TestSyncDailySweepIsAppScopedAndRespectsDueTime(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	value := "1"
	options := Options{AppID: "scheduled", Now: func() time.Time { return now }, PlaylistImporter: fixtureSnapshot(
		func(_ context.Context, id string) (PublicPlaylist, error) { return snapshotWithTrack(id, value), nil },
	)}
	s := NewServer(db, options)
	a := guest(t, s.HTTP())
	other := options
	other.AppID = "other-app"
	otherServer := NewServer(db, other)
	b := guest(t, otherServer.HTTP())
	in := map[string]string{"urlOrID": "123"}
	idA := call(t, s.HTTP(), a, "POST", "/playlists/sync", in, 200)["id"].(string)
	idB := call(t, otherServer.HTTP(), b, "POST", "/playlists/sync", in, 200)["id"].(string)
	now = now.Add(time.Hour)
	value = "2"
	s.runPlaylistSyncSweep(context.Background())
	assertPlaylistTracks(t, call(t, s.HTTP(), a, "GET", "/playlists/"+idA, nil, 200), "netease:1")
	now = now.Add(24 * time.Hour)
	s.runPlaylistSyncSweep(context.Background())
	assertPlaylistTracks(t, call(t, s.HTTP(), a, "GET", "/playlists/"+idA, nil, 200), "netease:2")
	assertPlaylistTracks(t, call(t, otherServer.HTTP(), b, "GET", "/playlists/"+idB, nil, 200), "netease:1")
	value = "3"
	s.runPlaylistSyncSweep(context.Background())
	assertPlaylistTracks(t, call(t, s.HTTP(), a, "GET", "/playlists/"+idA, nil, 200), "netease:2")
}

func TestSyncRejectsMissingEmptyMetadataAndRecordsTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"playlist":{"id":123,"name":"not a complete response","privacy":0,"trackCount":0}}`)
	}))
	t.Cleanup(upstream.Close)
	_, err := (AnonymousNetease{BaseURL: upstream.URL}).PublicPlaylist(context.Background(), "123")
	if err == nil || err.Error() != "public_playlist_metadata_incomplete" {
		t.Fatalf("ambiguous empty data returned %v", err)
	}

	db := testDB(t)
	now := time.Now()
	importer := fixtureSnapshot(func(ctx context.Context, id string) (PublicPlaylist, error) {
		if _, shortDeadline := ctx.Value(syncTimeoutFixtureKey{}).(bool); shortDeadline {
			<-ctx.Done()
			return PublicPlaylist{}, publicPlaylistSourceError(ctx.Err())
		}
		return snapshotWithTrack(id, "1"), nil
	})
	s := NewServer(db, Options{PlaylistImporter: importer, Now: func() time.Time { return now }})
	a := guest(t, s.HTTP())
	user := call(t, s.HTTP(), a, "GET", "/me", nil, 200)["user"].(map[string]any)["id"].(string)
	p := call(t, s.HTTP(), a, "POST", "/playlists/sync", map[string]string{"urlOrID": "123"}, 200)
	id := p["id"].(string)
	now = now.Add(time.Minute)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), syncTimeoutFixtureKey{}, true), 200*time.Millisecond)
	defer cancel()
	if _, err = s.syncPlaylist(ctx, user, id, false); err == nil || err.Error() != "public_playlist_source_unavailable" {
		t.Fatalf("timeout: %v", err)
	}
	after := call(t, s.HTTP(), a, "GET", "/playlists/"+id, nil, 200)
	assertPlaylistTracks(t, after, "netease:1")
	beforeStatus, afterStatus := p["sync"].(map[string]any), after["sync"].(map[string]any)
	if afterStatus["lastSuccessAt"] != beforeStatus["lastSuccessAt"] || afterStatus["lastError"] != "public_playlist_source_unavailable" {
		t.Fatal(afterStatus)
	}
}

type syncTimeoutFixtureKey struct{}

func TestPlaylistPrependConcurrentAddsAndCapacity(t *testing.T) {
	db := testDB(t)
	h := NewServer(db, Options{}).HTTP()
	a := guest(t, h)
	ids := seed(t, db)
	p := call(t, h, a, "POST", "/playlists", map[string]any{"name": "nearly full"}, 200)
	id := p["id"].(string)
	path := "/playlists/" + id
	err := db.Exec(`INSERT INTO music_tracks (id, source, external_id, title, canonical_verified)
		SELECT 'fixture:cap' || n, 'fixture', 'cap' || n, 'Song ' || n, true FROM generate_series(1, 998) n`).Error
	if err != nil {
		t.Fatal(err)
	}
	err = db.Exec(`INSERT INTO music_entries (playlist_id, track_id, position)
		SELECT ?, 'fixture:cap' || n, n FROM generate_series(1, 998) n`, id).Error
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan int, 2)
	for _, track := range ids[:2] {
		go func() {
			results <- request(h, a, "POST", path+"/tracks", map[string]any{"trackIDs": []string{track}}).Code
		}()
	}
	if first, second := <-results, <-results; first != 200 || second != 200 {
		t.Fatalf("concurrent add returned %d and %d", first, second)
	}
	got := call(t, h, a, "POST", path+"/tracks", map[string]any{"trackIDs": []string{ids[0], ids[2]}}, 200)
	if !reflect.DeepEqual(got["overflowIDs"], []any{ids[2]}) || !reflect.DeepEqual(got["existingIDs"], []any{ids[0]}) {
		t.Fatal("cap or existing result incorrect")
	}
	tracks := got["playlist"].(map[string]any)["tracks"].([]any)
	if len(tracks) != 1000 {
		t.Fatal("concurrent write lost tracks or overflowed")
	}
	top := []any{tracks[0].(map[string]any)["id"], tracks[1].(map[string]any)["id"]}
	if top[0] == top[1] || (top[0] != ids[0] && top[0] != ids[1]) || (top[1] != ids[0] && top[1] != ids[1]) {
		t.Fatal(top)
	}
	if tracks[2].(map[string]any)["id"] != "fixture:cap1" || tracks[999].(map[string]any)["id"] != "fixture:cap998" {
		t.Fatal("previous relative order changed")
	}
}

func TestNewSubscriptionCanSyncImmediatelyThenAppliesCooldown(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	var pulls int
	s := NewServer(db, Options{
		Now: func() time.Time { return now },
		PlaylistImporter: fixtureSnapshot(func(_ context.Context, id string) (PublicPlaylist, error) {
			pulls++
			return snapshotWithTrack(id, strconv.Itoa(pulls)), nil
		}),
	})
	h := s.HTTP()
	a := guest(t, h)
	p := call(t, h, a, "POST", "/playlists/sync", map[string]string{"urlOrID": "123"}, 200)
	path := "/playlists/" + p["id"].(string) + "/sync"
	assertPlaylistTracks(t, call(t, h, a, "POST", path, struct{}{}, 200), "netease:2")
	call(t, h, a, "POST", path, struct{}{}, 429)
	if pulls != 2 {
		t.Fatal("cooldown triggered another source request")
	}
}

func TestPlaylistPrependSkipsLegacyTracksWithoutMetadataLookup(t *testing.T) {
	db := testDB(t)
	h := NewServer(db, Options{}).HTTP()
	a := guest(t, h)
	ids := seed(t, db)
	p := call(t, h, a, "POST", "/playlists", map[string]any{"name": "legacy", "trackIDs": ids[:2]}, 200)
	if err := db.Exec("UPDATE music_tracks SET canonical_verified=false WHERE id=?", ids[0]).Error; err != nil {
		t.Fatal(err)
	}
	added := call(t, h, a, "POST", "/playlists/"+p["id"].(string)+"/tracks",
		map[string]any{"trackIDs": []string{ids[2], ids[0]}}, 200)
	assertPlaylistTracks(t, added["playlist"].(map[string]any), ids[2], ids[0], ids[1])
	if !reflect.DeepEqual(added["existingIDs"], []any{ids[0]}) {
		t.Fatal(added)
	}
}
