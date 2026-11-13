package music

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_MUSIC_DSN")
	if dsn == "" {
		t.Skip("requires isolated TEST_MUSIC_DSN")
	}
	if !strings.Contains(dsn, "music_test") {
		t.Fatal("test DSN must target music_test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("TRUNCATE music_sessions,music_shares,music_queues,music_history,music_likes,music_entries,music_playlists,music_tracks,music_users CASCADE").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	return db
}
func request(h http.Handler, token, method, path string, in any) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if in != nil {
		_ = json.NewEncoder(&b).Encode(in)
	}
	r := httptest.NewRequest(method, "http://music.test/music-api/v1"+path, &b)
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "127.0.0.1:12345"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func call(t *testing.T, h http.Handler, token, method, path string, in any, status int) map[string]any {
	t.Helper()
	w := request(h, token, method, path, in)
	if w.Code != status {
		t.Fatalf("%s %s: got %d expected %d body=%s", method, path, w.Code, status, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func guest(t *testing.T, h http.Handler) string {
	t.Helper()
	return call(t, h, "", "POST", "/auth/guest", map[string]string{"transport": "bearer"}, 200)["sessionToken"].(string)
}
func seed(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	ids := []string{"fixture:first", "fixture:second", "fixture:third"}
	for _, id := range ids {
		if err := saveTrack(db, Track{ID: id, Source: "fixture", ExternalID: strings.Split(id, ":")[1], Title: "Same title", Artist: "Artist", DurationMs: 60000}); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}
func TestPersistentIsolationConcurrencySharingAndTransfer(t *testing.T) {
	db := testDB(t)
	s := NewServer(db, Options{AppID: "web-app", Origin: "http://music.test"})
	h := s.HTTP()
	a, b := guest(t, h), guest(t, h)
	ids := seed(t, db)
	empty := call(t, h, a, "POST", "/playlists", map[string]any{"name": "empty"}, 200)
	if tracks, ok := empty["tracks"].([]any); !ok || len(tracks) != 0 {
		t.Fatal("empty playlist must encode tracks as []")
	}
	call(t, h, a, "DELETE", "/playlists/"+empty["id"].(string), map[string]any{"revision": 1}, 200)

	me := call(t, h, a, "GET", "/me?open_id=attacker", nil, 200)
	owner := me["user"].(map[string]any)["id"].(string)
	var hashes []string
	db.Raw("SELECT hash FROM music_sessions").Scan(&hashes)
	for _, hash := range hashes {
		if hash == a || hash == b || len(hash) != 64 {
			t.Fatal("raw session credential persisted")
		}
	}
	p := call(t, h, a, "POST", "/playlists", map[string]any{"name": "私有歌单", "trackIDs": []string{ids[0], ids[0], ids[1]}}, 200)
	id := p["id"].(string)
	if len(p["tracks"].([]any)) != 2 {
		t.Fatal("stable-ID dedup failed")
	}
	call(t, h, b, "GET", "/playlists/"+id, nil, 404)
	call(t, h, b, "PUT", "/playlists/"+id, map[string]any{"name": "stolen", "trackIDs": ids, "revision": 1}, 404)
	// New HTTP instance, same PostgreSQL, same credentials: persistence is real.
	h = NewServer(db, Options{AppID: "web-app", Origin: "http://music.test"}).HTTP()
	persisted := call(t, h, a, "GET", "/playlists/"+id, nil, 200)
	if persisted["name"] != "私有歌单" {
		t.Fatal("persistence lost")
	}
	start := make(chan struct{})
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, order := range [][]string{{ids[1], ids[0]}, {ids[0], ids[2]}} {
		wg.Add(1)
		go func(order []string) {
			defer wg.Done()
			<-start
			codes <- request(h, a, "PUT", "/playlists/"+id, map[string]any{"name": "ordered", "trackIDs": order, "revision": 1}).Code
		}(order)
	}
	close(start)
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent writes not guarded: %v", counts)
	}
	call(t, h, a, "PUT", "/likes/"+ids[0], map[string]any{}, 200)
	call(t, h, a, "POST", "/history", map[string]any{"trackID": ids[1], "positionMs": 4000}, 200)
	if n := len(call(t, h, b, "GET", "/likes", nil, 200)["tracks"].([]any)); n != 0 {
		t.Fatal("likes leaked")
	}
	if n := len(call(t, h, b, "GET", "/history", nil, 200)["history"].([]any)); n != 0 {
		t.Fatal("history leaked")
	}
	q := map[string]any{"revision": 0, "trackIDs": []string{ids[0], ids[1], ids[0]}, "currentIndex": 1, "positionMs": 12500}
	call(t, h, a, "PUT", "/queue/device-1234", q, 200)
	call(t, h, a, "PUT", "/queue/device-1234", q, 409)
	qb := call(t, h, b, "GET", "/queue/device-1234", nil, 200)
	if qb["revision"].(float64) != 0 {
		t.Fatal("queue leaked")
	}
	qa := call(t, h, a, "GET", "/queue/device-1234", nil, 200)
	if qa["positionMs"].(float64) != 12500 || len(qa["trackIDs"].([]any)) != 3 {
		t.Fatal("queue not persisted")
	}
	share := call(t, h, a, "POST", "/shares", map[string]any{"kind": "playlist", "targetID": id}, 200)
	token, sid := share["token"].(string), share["id"].(string)
	pub := call(t, h, "", "GET", "/shared/"+token, nil, 200)
	call(t, NewServer(db, Options{AppID: "other-app"}).HTTP(), "", "GET", "/shared/"+token, nil, 404)
	blob, _ := json.Marshal(pub)
	if bytes.Contains(blob, []byte(owner)) || bytes.Contains(blob, []byte("session")) {
		t.Fatal("share leaks identity")
	}
	call(t, h, b, "POST", "/shared/"+token+"/copy", map[string]any{}, 200)
	call(t, h, b, "DELETE", "/shares/"+sid, nil, 404)
	call(t, h, a, "DELETE", "/shares/"+sid, nil, 200)
	call(t, h, "", "GET", "/shared/"+token, nil, 404)
	expShare := call(t, h, a, "POST", "/shares", map[string]any{"kind": "track", "targetID": ids[0]}, 200)
	db.Exec("UPDATE music_shares SET expires_at=? WHERE id=?", time.Now().Add(-time.Hour), expShare["id"])
	call(t, h, "", "GET", "/shared/"+expShare["token"].(string), nil, 404)

	ex := call(t, h, a, "GET", "/export", nil, 200)
	raw, _ := json.Marshal(ex)
	for _, bad := range []string{a, "expiresAt", "MusicURL", "sessionToken", "recoveryCode", "http://"} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Fatal("unsafe export contains " + bad)
		}
	}
	call(t, h, b, "POST", "/import", ex, 200)
	ps := call(t, h, b, "GET", "/playlists", nil, 200)["playlists"].([]any)
	if len(ps) != 2 {
		t.Fatal("import must copy owned playlists")
	}
	ex["owner_id"] = "attacker"
	call(t, h, b, "POST", "/import", ex, 400)
	call(t, h, a, "DELETE", "/history", nil, 200)
	if len(call(t, h, a, "GET", "/history", nil, 200)["history"].([]any)) != 0 {
		t.Fatal("history clear failed")
	}
	p = call(t, h, a, "GET", "/playlists/"+id, nil, 200)
	call(t, h, a, "DELETE", "/playlists/"+id, map[string]any{"revision": p["revision"]}, 200)
	call(t, h, a, "PUT", "/playlists/"+id, map[string]any{"name": "resurrect", "trackIDs": ids, "revision": p["revision"]}, 404)
	// App boundary: the same raw credential cannot read another app's data.
	other := NewServer(db, Options{AppID: "other-app"}).HTTP()
	call(t, other, a, "GET", "/me", nil, 401)
}
func TestRecoveryCookiesExpiryCSRFAndAtomicImport(t *testing.T) {
	db := testDB(t)
	s := NewServer(db, Options{AppID: "web", Origin: "https://music.test", SecureCookie: true})
	h := s.HTTP()
	a := guest(t, h)
	w := request(h, "", "POST", "/auth/guest", map[string]any{})
	cs := w.Result().Cookies()
	if len(cs) != 1 || !cs[0].HttpOnly || !cs[0].Secure || cs[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe browser session cookie")
	}
	var raw map[string]any
	json.Unmarshal(w.Body.Bytes(), &raw)
	if raw["sessionToken"] != nil {
		t.Fatal("browser session token exposed")
	}
	r := httptest.NewRequest("POST", "http://music.test/music-api/v1/playlists", strings.NewReader(`{"name":"x"}`))
	r.Header.Set("Origin", "https://attacker.test")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cs[0])
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 403 {
		t.Fatal("CSRF not rejected")
	}
	code := call(t, h, a, "POST", "/auth/recovery", map[string]any{}, 200)["recoveryCode"].(string)
	recovered := call(t, h, "", "POST", "/auth/recover", map[string]any{"recoveryCode": code, "transport": "bearer"}, 200)["sessionToken"].(string)
	ma := call(t, h, a, "GET", "/me", nil, 200)
	mb := call(t, h, recovered, "GET", "/me", nil, 200)
	if fmt.Sprint(ma["user"]) != fmt.Sprint(mb["user"]) {
		t.Fatal("recovery changed identity")
	}
	call(t, h, a, "POST", "/auth/recovery", map[string]any{}, 200)
	call(t, h, "", "POST", "/auth/recover", map[string]any{"recoveryCode": code}, 401)
	ids := seed(t, db)
	bad := Export{Format: "betago-music", Version: 1, Playlists: []ExportPlaylist{{Name: "valid", Tracks: []Track{{ID: ids[0], Source: "fixture", ExternalID: "first", Title: "ok"}}}, {Name: "", Tracks: []Track{}}}, Likes: []Track{}}
	call(t, h, a, "POST", "/import", bad, 400)
	if len(call(t, h, a, "GET", "/playlists", nil, 200)["playlists"].([]any)) != 0 {
		t.Fatal("partial import survived rollback")
	}
	db.Exec("UPDATE music_sessions SET expires_at=? WHERE hash=?", time.Now().Add(-time.Hour), digest(a))
	call(t, h, a, "GET", "/me", nil, 401)
	call(t, h, recovered, "POST", "/auth/logout", map[string]any{}, 200)
	call(t, h, recovered, "GET", "/me", nil, 401)
}

type fakeVerifier struct{ identity Identity }

func (v fakeVerifier) Verify(context.Context, string) (Identity, error) { return v.identity, nil }
func TestVerifiedAccountTenantAndApplicationBoundaries(t *testing.T) {
	db := testDB(t)
	login := func(app, tenant string) string {
		h := NewServer(db, Options{AppID: app, Verifier: fakeVerifier{Identity{AppID: app, TenantKey: tenant, Subject: "same-open-id"}}}).HTTP()
		return call(t, h, "", "POST", "/auth/feishu/exchange", map[string]any{"code": "verified-code", "transport": "bearer"}, 200)["sessionToken"].(string)
	}
	a, b, c := login("app-a", "tenant-a"), login("app-a", "tenant-b"), login("app-b", "tenant-a")
	h := NewServer(db, Options{AppID: "app-a"}).HTTP()
	p := call(t, h, a, "POST", "/playlists", map[string]any{"name": "tenant-private"}, 200)
	call(t, h, b, "GET", "/playlists/"+p["id"].(string), nil, 404)
	call(t, h, c, "GET", "/me", nil, 401)
	a2 := login("app-a", "tenant-a")
	call(t, h, a2, "GET", "/playlists/"+p["id"].(string), nil, 200)
	call(t, h, "", "POST", "/auth/feishu/exchange", map[string]any{"code": "no-permission"}, 503)
	call(t, h, "", "POST", "/auth/feishu/exchange", map[string]any{"code": "fake", "open_id": "same-open-id"}, 400)
}
func TestAnonymousProviderRestrictionsAndIdentityVerification(t *testing.T) {
	var mode string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("provider received cookie")
		}
		switch r.URL.Path {
		case "/song/url/v1":
			if mode == "v1denied" {
				http.Error(w, "permission denied", 403)
				return
			}
			if mode == "v1error" {
				http.Error(w, "upstream decode failure", 502)
				return
			}
			if r.URL.Query().Get("id") != "42" || r.URL.Query().Get("unblock") != "false" {
				t.Error("wrong ID or unblock enabled")
			}
			if mode == "unavailable" {
				fmt.Fprint(w, `{"code":200,"data":[{"id":42,"url":null}]}`)
			} else {
				fmt.Fprint(w, `{"code":200,"data":[{"id":42,"url":"https://audio.test/42.mp3","expi":60,"freeTrialInfo":{"start":0,"end":30}}]}`)
			}
		case "/song/url":
			if mode != "v1error" {
				t.Error("must not fall back from a valid restriction response")
			}
			fmt.Fprint(w, `{"code":200,"data":[{"id":42,"url":"https://audio.test/42.mp3","expi":60,"freeTrialInfo":null}]}`)
		case "/lyric":
			fmt.Fprintf(w, `{"code":200,"lrc":{"lyric":"[00:00]track %s"}}`, r.URL.Query().Get("id"))
		case "/open-apis/authen/v2/oauth/token":
			fmt.Fprint(w, `{"code":0,"access_token":"server-only-token"}`)
		case "/open-apis/authen/v1/user_info":
			if r.Header.Get("Authorization") != "Bearer server-only-token" {
				t.Error("missing verified token")
			}
			fmt.Fprint(w, `{"code":0,"data":{"open_id":"verified-user","tenant_key":"real-tenant"}}`)
		default:
			t.Error("unexpected provider endpoint " + r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	p := AnonymousNetease{BaseURL: upstream.URL, OwnedMediaHost: "audio.test"}
	track := Track{ID: "netease:42", Source: "netease", ExternalID: "42"}
	res, err := p.Resolve(context.Background(), track)
	if err != nil || res.Status != "preview" || res.ExpiresAt == nil || time.Until(*res.ExpiresAt) > 61*time.Second {
		t.Fatalf("preview/expiry lost: %+v %v", res, err)
	}
	l, err := p.Lyrics(context.Background(), track)
	if err != nil || l.TrackID != "netease:42" || !strings.Contains(l.LRC, "42") {
		t.Fatal("lyric identity lost")
	}
	mode = "unavailable"
	res, err = p.Resolve(context.Background(), track)
	if err != nil || res.Status != "unavailable" || res.URL != "" {
		t.Fatal("restrictions bypassed")
	}
	mode = "v1denied"
	res, err = p.Resolve(context.Background(), track)
	if err != nil || res.Status != "unavailable" || res.Reason != "provider_access_denied" {
		t.Fatal("permission denial must never fall through to compatibility resolver")
	}
	mode = "v1error"
	p.OwnedMediaHost = ""
	res, err = p.Resolve(context.Background(), track)
	if err != nil || res.Status != "unavailable" || res.URL != "" || !strings.Contains(res.Reason, "source_mismatch") {
		t.Fatal("unapproved third-party media host was accepted")
	}
	p.OwnedMediaHost = "audio.test"
	mode = "v1error"
	res, err = p.Resolve(context.Background(), track)
	if err != nil || res.Status != "available" || res.TrackID != track.ID {
		t.Fatal("same-source anonymous compatibility resolution failed")
	}
	ident, err := (FeishuVerifier{AppID: "app", AppSecret: "test-secret", BaseURL: upstream.URL}).Verify(context.Background(), "temporary-code")
	if err != nil || ident.TenantKey != "real-tenant" || ident.Subject != "verified-user" {
		t.Fatalf("identity not verified: %+v %v", ident, err)
	}
}

func TestModuleLifecycleAndPreMigratedDatabase(t *testing.T) {
	db := testDB(t)
	opts := Options{DBProvider: func() *gorm.DB { return db }, AppID: "module-app", Origin: "http://music.test", Addr: "127.0.0.1:0", SkipMigration: true}
	m := NewModule(opts)
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(ctx)
	resp, err := http.Post("http://"+m.listener.Addr().String()+"/music-api/v1/auth/guest", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("live module not usable")
	}
	opts.AppID = ""
	if err := NewModule(opts).Init(ctx); err == nil {
		t.Fatal("enabled module requires explicit app boundary")
	}
}
