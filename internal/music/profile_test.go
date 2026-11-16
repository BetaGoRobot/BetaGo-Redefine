package music

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

type profileFixture struct{ Tenant, Subject, Name, EnglishName, Thumb, Avatar, Middle string }
type verifiedProfileFixture struct {
	mu    sync.RWMutex
	users map[string]profileFixture
	calls int
}

func (f *verifiedProfileFixture) set(code string, profile profileFixture) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[code] = profile
}
func (f *verifiedProfileFixture) count() int { f.mu.RLock(); defer f.mu.RUnlock(); return f.calls }
func newVerifiedProfileFixture(t *testing.T) (*verifiedProfileFixture, string) {
	t.Helper()
	f := &verifiedProfileFixture{users: map[string]profileFixture{}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/oauth/v3/token" {
			_ = r.ParseForm()
			code := r.Form.Get("code")
			f.mu.Lock()
			_, ok := f.users[code]
			f.calls++
			f.mu.Unlock()
			if !ok {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"code":20003,"error":"invalid_grant"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "access_token": "fixture-token:" + code})
			return
		}
		if r.URL.Path == "/open-apis/authen/v1/user_info" {
			code := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer fixture-token:")
			f.mu.RLock()
			profile, ok := f.users[code]
			f.mu.RUnlock()
			if !ok {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"open_id": profile.Subject, "tenant_key": profile.Tenant, "name": profile.Name, "en_name": profile.EnglishName, "avatar_thumb": profile.Thumb, "avatar_url": profile.Avatar, "avatar_middle": profile.Middle, "email": "must-not-persist@example.test", "mobile": "must-not-persist"}})
			return
		}
		t.Errorf("unexpected profile upstream path %s", r.URL.Path)
		w.WriteHeader(404)
	}))
	t.Cleanup(upstream.Close)
	return f, upstream.URL
}
func profileUser(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	user, ok := response["user"].(map[string]any)
	if !ok {
		t.Fatal("missing session user")
	}
	return user
}
func profileDate(t *testing.T, v any) time.Time {
	t.Helper()
	raw, ok := v.(string)
	if !ok {
		t.Fatal("missing verified profile timestamp")
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatal("invalid profile timestamp")
	}
	return parsed
}

func TestVerifiedProfilePersistsAndNeverEntersExportsOrOtherAccounts(t *testing.T) {
	db := testDB(t)
	fixture, base := newVerifiedProfileFixture(t)
	fixture.set("alice", profileFixture{Tenant: "tenant-a", Subject: "alice", Name: "  Alice\n Verified  ", Thumb: "https://avatars.example.test/alice-72.png", Avatar: "https://avatars.example.test/alice.png"})
	fixture.set("bob", profileFixture{Tenant: "tenant-a", Subject: "bob", EnglishName: "Bob", Thumb: "https://avatars.example.test/bob.png"})
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	opts := Options{AppID: "profile-app", Now: func() time.Time { return now }, Verifier: FeishuVerifier{AppID: "profile-app", AppSecret: "fixture-secret", BaseURL: base}}
	handler := NewServer(db, opts).HTTP()
	login := call(t, handler, "", "POST", "/auth/feishu/exchange", map[string]any{"code": "alice", "transport": "bearer"}, 200)
	token := login["sessionToken"].(string)
	user := profileUser(t, login)
	if user["displayName"] != "Alice Verified" || user["avatarURL"] != "https://avatars.example.test/alice-72.png" || user["profileNeedsRefresh"] != false {
		t.Fatal("verified user_info profile was discarded or not normalized")
	}
	if !profileDate(t, user["profileUpdatedAt"]).Equal(now) {
		t.Fatal("profile freshness did not use server time")
	}
	id := user["id"]
	restarted := NewServer(db, opts).HTTP()
	me := profileUser(t, call(t, restarted, token, "GET", "/me", nil, 200))
	if me["displayName"] != user["displayName"] || me["id"] != id || me["avatarURL"] != user["avatarURL"] {
		t.Fatal("profile did not persist across server instances")
	}
	bob := call(t, handler, "", "POST", "/auth/feishu/exchange", map[string]any{"code": "bob", "transport": "bearer"}, 200)["sessionToken"].(string)
	bobMe := profileUser(t, call(t, handler, bob, "GET", "/me", nil, 200))
	if bobMe["displayName"] != "Bob" || bobMe["avatarURL"] == user["avatarURL"] {
		t.Fatal("profile crossed personal accounts")
	}
	otherApp := NewServer(db, Options{AppID: "other-profile-app"}).HTTP()
	call(t, otherApp, token, "GET", "/me", nil, 401)
	playlist := call(t, handler, token, "POST", "/playlists", map[string]any{"name": "Profile privacy fixture"}, 200)
	share := call(t, handler, token, "POST", "/shares", map[string]any{"kind": "playlist", "targetID": playlist["id"]}, 200)
	shared := call(t, handler, "", "GET", "/shared/"+share["token"].(string), nil, 200)
	exported := call(t, handler, token, "GET", "/export", nil, 200)
	encoded, _ := json.Marshal([]any{exported, shared})
	for _, private := range []string{"Alice", "avatars.example.test", "displayName", "avatarURL", "profileUpdatedAt", "must-not-persist"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("personal profile or contact data entered library export")
		}
	}
	now = now.Add(time.Minute)
	fixture.set("alice", profileFixture{Tenant: "tenant-a", Subject: "alice", Name: "Alice New", Thumb: "http://insecure.example.test/avatar", Avatar: "https://avatars.example.test/new.png"})
	again := call(t, handler, "", "POST", "/auth/feishu/exchange", map[string]any{"code": "alice", "transport": "bearer"}, 200)
	if profileUser(t, again)["id"] != id || profileUser(t, again)["displayName"] != "Alice New" || profileUser(t, again)["avatarURL"] != "https://avatars.example.test/new.png" {
		t.Fatal("same identity login did not update only its canonical profile")
	}
}

func TestProfileRefreshKeepsSessionAndRequiresTheExactVerifiedIdentity(t *testing.T) {
	db := testDB(t)
	fixture, base := newVerifiedProfileFixture(t)
	fixture.set("self", profileFixture{Tenant: "tenant-a", Subject: "alice", Name: "Original"})
	fixture.set("other-subject", profileFixture{Tenant: "tenant-a", Subject: "bob", Name: "Wrong Subject"})
	fixture.set("other-tenant", profileFixture{Tenant: "tenant-b", Subject: "alice", Name: "Wrong Tenant"})
	now := time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)
	opts := Options{AppID: "refresh-app", Now: func() time.Time { return now }, Verifier: FeishuVerifier{AppID: "refresh-app", AppSecret: "fixture-secret", BaseURL: base}}
	handler := NewServer(db, opts).HTTP()
	login := call(t, handler, "", "POST", "/auth/feishu/exchange", map[string]any{"code": "self", "transport": "bearer"}, 200)
	token := login["sessionToken"].(string)
	id := profileUser(t, login)["id"]
	var sessions, users int64
	db.Table("music_sessions").Count(&sessions)
	db.Table("music_users").Count(&users)
	var oldHash string
	db.Raw("SELECT hash FROM music_sessions WHERE user_id=?", id).Scan(&oldHash)
	for _, code := range []string{"other-subject", "other-tenant"} {
		call(t, handler, token, "POST", "/auth/feishu/profile", map[string]any{"code": code}, 403)
	}
	mismatch := NewServer(db, Options{AppID: "refresh-app", Now: opts.Now, Verifier: fakeVerifier{Identity{AppID: "other-app", TenantKey: "tenant-a", Subject: "alice"}}}).HTTP()
	call(t, mismatch, token, "POST", "/auth/feishu/profile", map[string]any{"code": "self"}, 403)
	guestToken := guest(t, handler)
	before := fixture.count()
	call(t, handler, "", "POST", "/auth/feishu/profile", map[string]any{"code": "self"}, 401)
	call(t, handler, guestToken, "POST", "/auth/feishu/profile", map[string]any{"code": "self"}, 403)
	if fixture.count() != before {
		t.Fatal("guest session reached Feishu profile verifier")
	}
	for _, forged := range []map[string]any{{"code": "self", "displayName": "Forged"}, {"code": "self", "avatarURL": "https://evil.example.test/x"}, {"code": "self", "owner_id": id}, {"code": "self", "open_id": "alice"}} {
		call(t, handler, token, "POST", "/auth/feishu/profile", forged, 400)
	}
	if fixture.count() != before {
		t.Fatal("client profile fields were passed to verifier")
	}
	beforeRefresh := call(t, handler, token, "GET", "/me", nil, 200)
	unchanged := profileUser(t, beforeRefresh)
	if unchanged["displayName"] != "Original" {
		t.Fatal("denied refresh changed existing profile")
	}
	// Exclude the deliberately created guest from the later same-session invariants.
	db.Table("music_sessions").Count(&sessions)
	db.Table("music_users").Count(&users)
	now = now.Add(time.Minute)
	fixture.set("self", profileFixture{Tenant: "tenant-a", Subject: "alice", Name: " Refreshed ", Thumb: "https://user:password@avatars.example.test/leak", Avatar: "javascript:invalid", Middle: "https://avatars.example.test/middle.png"})
	response := request(handler, token, "POST", "/auth/feishu/profile", map[string]any{"code": "self"})
	if response.Code != http.StatusOK {
		t.Fatalf("same-user profile refresh failed: status=%d", response.Code)
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("profile refresh rotated or cleared the existing session cookie")
	}
	var out map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	updated := profileUser(t, out)
	if updated["id"] != id || out["expiresAt"] != beforeRefresh["expiresAt"] || !profileDate(t, out["expiresAt"]).Equal(profileDate(t, login["expiresAt"])) || out["sessionToken"] != nil {
		t.Fatal("profile refresh changed account/session/expiry")
	}
	if updated["displayName"] != "Refreshed" || updated["avatarURL"] != "https://avatars.example.test/middle.png" || updated["profileNeedsRefresh"] != false {
		t.Fatal("profile refresh did not normalize trusted fields")
	}
	if !profileDate(t, updated["profileUpdatedAt"]).Equal(now) {
		t.Fatal("profile refresh timestamp did not advance")
	}
	var afterSessions, afterUsers int64
	db.Table("music_sessions").Count(&afterSessions)
	db.Table("music_users").Count(&afterUsers)
	if afterSessions != sessions || afterUsers != users {
		t.Fatal("profile refresh created an identity or session")
	}
	var newHash string
	db.Raw("SELECT hash FROM music_sessions WHERE user_id=?", id).Scan(&newHash)
	if newHash != oldHash {
		t.Fatal("profile refresh changed the stored session hash")
	}
	call(t, handler, token, "GET", "/me", nil, 200)
}

func TestLegacyProfileRefreshMarksEmptyVerifiedProfileAttempted(t *testing.T) {
	db := testDB(t)
	fixture, base := newVerifiedProfileFixture(t)
	fixture.set("empty", profileFixture{Tenant: "legacy-tenant", Subject: "legacy-subject"})
	expires := time.Date(2026, 12, 16, 0, 0, 0, 0, time.UTC)
	if err := db.Exec("INSERT INTO music_users(id,kind,app_id,tenant_key,subject) VALUES('legacy-profile','feishu','legacy-app','legacy-tenant','legacy-subject')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO music_sessions(hash,user_id,expires_at) VALUES(?,'legacy-profile',?)", digest("legacy-fixture-token"), expires).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewServer(db, Options{AppID: "legacy-app", Verifier: FeishuVerifier{AppID: "legacy-app", AppSecret: "fixture-secret", BaseURL: base}}).HTTP()
	before := profileUser(t, call(t, handler, "legacy-fixture-token", "GET", "/me", nil, 200))
	if before["profileUpdatedAt"] != nil || before["profileNeedsRefresh"] != true {
		t.Fatal("legacy Feishu profile was not marked for one-time refresh")
	}
	updated := profileUser(t, call(t, handler, "legacy-fixture-token", "POST", "/auth/feishu/profile", map[string]any{"code": "empty"}, 200))
	if updated["profileNeedsRefresh"] != false || updated["profileUpdatedAt"] == nil {
		t.Fatal("empty verified profile causes an endless refresh loop")
	}
	for _, key := range []string{"displayName", "avatarURL"} {
		if value, exists := updated[key]; exists && value != "" {
			t.Fatal("missing provider profile became client or placeholder data")
		}
	}
	after := profileUser(t, call(t, NewServer(db, Options{AppID: "legacy-app"}).HTTP(), "legacy-fixture-token", "GET", "/me", nil, 200))
	if after["profileNeedsRefresh"] != false || after["profileUpdatedAt"] == nil {
		t.Fatal("refresh attempt was not durable")
	}
}

func TestProfileMigrationPreservesAccountsAndSessionsAndGuardsStartup(t *testing.T) {
	db := testDB(t)
	if err := db.Exec("ALTER TABLE music_users DROP COLUMN IF EXISTS display_name,DROP COLUMN IF EXISTS avatar_url,DROP COLUMN IF EXISTS profile_updated_at").Error; err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := db.Exec("INSERT INTO music_users(id,kind,app_id,tenant_key,subject,recovery_hash) VALUES('migration-profile','feishu','migration-profile-app','real-tenant','real-subject','fixture-recovery-hash')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO music_sessions(hash,user_id,expires_at) VALUES(?,'migration-profile',?)", digest("migration-fixture-token"), exp).Error; err != nil {
		t.Fatal(err)
	}
	opts := Options{DBProvider: func() *gorm.DB { return db }, AppID: "migration-profile-app", Origin: "http://music.test", Addr: "127.0.0.1:0", SkipMigration: true}
	if err := NewModule(opts).Init(context.Background()); err == nil || !strings.Contains(err.Error(), "profile") {
		t.Error("pre-migrated startup accepted a schema missing profile columns")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var columns int64
	if err := db.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='music_users' AND column_name IN ('display_name','avatar_url','profile_updated_at')").Scan(&columns).Error; err != nil || columns != 3 {
		t.Fatal("additive profile migration did not create every required column")
	}
	row := struct {
		ID, Kind, AppID, TenantKey, Subject, RecoveryHash, DisplayName, AvatarURL string
		ProfileUpdatedAt                                                          *time.Time
	}{}
	if err := db.Raw("SELECT * FROM music_users WHERE id='migration-profile'").Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ID != "migration-profile" || row.Kind != "feishu" || row.AppID != "migration-profile-app" || row.TenantKey != "real-tenant" || row.Subject != "real-subject" || row.RecoveryHash != "fixture-recovery-hash" || row.DisplayName != "" || row.AvatarURL != "" || row.ProfileUpdatedAt != nil {
		t.Fatal("profile migration rewrote identity fields or prefilled fake profile data")
	}
	if err := db.Exec("UPDATE music_users SET display_name='Retained',profile_updated_at=now() WHERE id='migration-profile'").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	own := profileUser(t, call(t, NewServer(db, opts).HTTP(), "migration-fixture-token", "GET", "/me", nil, 200))
	if own["displayName"] != "Retained" || own["profileNeedsRefresh"] != false {
		t.Fatal("repeated migration cleared saved profile or existing session")
	}
	var sessionExpiry time.Time
	db.Raw("SELECT expires_at FROM music_sessions WHERE hash=?", digest("migration-fixture-token")).Scan(&sessionExpiry)
	if !sessionExpiry.Equal(exp) {
		t.Fatal("profile migration changed existing session expiry")
	}
	if err := NewModule(opts).Init(context.Background()); err != nil {
		t.Fatal("fully migrated startup was rejected")
	}
}
