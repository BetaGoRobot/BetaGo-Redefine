package music

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"
)

type canonicalTestProvider struct {
	tracks  map[string]Track
	offline bool
}

func (p *canonicalTestProvider) Search(context.Context, string) ([]Track, error) {
	return nil, unavailable
}
func (p *canonicalTestProvider) Detail(_ context.Context, id string) (Track, error) {
	if p.offline {
		return Track{}, unavailable
	}
	if v, ok := p.tracks[id]; ok {
		return v, nil
	}
	return Track{}, missing
}
func (p *canonicalTestProvider) Resolve(_ context.Context, t Track) (Resolution, error) {
	return Resolution{TrackID: t.ID, Status: "unavailable", Reason: "test_no_audio"}, nil
}
func (p *canonicalTestProvider) Lyrics(_ context.Context, t Track) (Lyrics, error) {
	return Lyrics{TrackID: t.ID}, nil
}
func canonicalFixture(id string) Track {
	return Track{ID: "netease:" + id, Source: "netease", ExternalID: id, Title: "Provider title " + id, Artist: "Provider artist", Album: "Provider album", Picture: "https://p1.music.126.net/canonical.jpg", DurationMs: 245000}
}
func forgedFixture(id string) Track {
	t := canonicalFixture(id)
	t.Title = "Attacker title"
	t.Artist = "Attacker artist"
	t.Picture = "https://attacker.invalid/pixel"
	t.DurationMs = 1
	return t
}
func requireCanonical(t *testing.T, out map[string]any, want Track) {
	t.Helper()
	b, _ := json.Marshal(out)
	var got Track
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("untrusted metadata reached shared catalogue: got %+v want %+v", got, want)
	}
}
func TestGuestRegistrationUsesCanonicalMetadata(t *testing.T) {
	db := testDB(t)
	want := canonicalFixture("700001")
	p := &canonicalTestProvider{tracks: map[string]Track{want.ID: want}}
	h := NewServer(db, Options{AppID: "canonical-app", Provider: p}).HTTP()
	a, b := guest(t, h), guest(t, h)
	out := call(t, h, a, "POST", "/tracks/register", map[string]any{"track": forgedFixture(want.ExternalID)}, 200)
	requireCanonical(t, out, want)
	requireCanonical(t, call(t, h, b, "GET", "/tracks/"+want.ID, nil, 200), want)
	p.offline = true
	requireCanonical(t, call(t, h, b, "GET", "/tracks/"+want.ID, nil, 200), want)
}
func TestJSONImportDoesNotSeedSharedMetadata(t *testing.T) {
	db := testDB(t)
	want := canonicalFixture("700002")
	p := &canonicalTestProvider{tracks: map[string]Track{want.ID: want}}
	h := NewServer(db, Options{AppID: "canonical-app", Provider: p}).HTTP()
	a, b := guest(t, h), guest(t, h)
	payload := Export{Format: "betago-music", Version: 1, Playlists: []ExportPlaylist{{Name: "attacker copy", Tracks: []Track{forgedFixture(want.ExternalID)}}}, Likes: []Track{forgedFixture(want.ExternalID)}}
	call(t, h, a, "POST", "/import", payload, 200)
	requireCanonical(t, call(t, h, b, "GET", "/tracks/"+want.ID, nil, 200), want)
	ps := call(t, h, a, "GET", "/playlists", nil, 200)["playlists"].([]any)
	id := ps[0].(map[string]any)["id"].(string)
	call(t, h, b, "GET", "/playlists/"+id, nil, 404)
	other := NewServer(db, Options{AppID: "other-app", Provider: p}).HTTP()
	c := guest(t, other)
	requireCanonical(t, call(t, other, c, "GET", "/tracks/"+want.ID, nil, 200), want)
	call(t, other, c, "GET", "/playlists/"+id, nil, 404)
}
func TestOldUnverifiedRowsAreRepaired(t *testing.T) {
	db := testDB(t)
	want := canonicalFixture("700003")
	bad := forgedFixture(want.ExternalID)
	if err := db.Exec("INSERT INTO music_tracks(id,source,external_id,title,artist,album,picture,duration_ms) VALUES(?,?,?,?,?,?,?,?)", bad.ID, bad.Source, bad.ExternalID, bad.Title, bad.Artist, bad.Album, bad.Picture, bad.DurationMs).Error; err != nil {
		t.Fatal(err)
	}
	p := &canonicalTestProvider{tracks: map[string]Track{want.ID: want}}
	h := NewServer(db, Options{AppID: "canonical-app", Provider: p}).HTTP()
	a := guest(t, h)
	requireCanonical(t, call(t, h, a, "GET", "/tracks/"+want.ID, nil, 200), want)
	// Reopening the server proves the canonical row, rather than only an in-memory response, was repaired.
	p.offline = true
	h = NewServer(db, Options{AppID: "canonical-app", Provider: p}).HTTP()
	requireCanonical(t, call(t, h, a, "GET", "/tracks/"+want.ID, nil, 200), want)
}
func TestUnresolvedMetadataIsRejectedAtomically(t *testing.T) {
	db := testDB(t)
	want := canonicalFixture("700004")
	p := &canonicalTestProvider{tracks: map[string]Track{want.ID: want}}
	h := NewServer(db, Options{AppID: "canonical-app", Provider: p}).HTTP()
	a := guest(t, h)
	unsupported := Track{ID: "legacy:unverified-private", Source: "legacy", ExternalID: "unverified-private", Title: "private label"}
	call(t, h, a, "POST", "/tracks/register", map[string]any{"track": unsupported}, 404)
	payload := Export{Format: "betago-music", Version: 1, Playlists: []ExportPlaylist{{Name: "rollback", Tracks: []Track{forgedFixture(want.ExternalID), unsupported}}}}
	call(t, h, a, "POST", "/import", payload, 404)
	var n int64
	db.Raw("SELECT COUNT(*) FROM music_playlists").Scan(&n)
	if n != 0 {
		t.Fatal("unresolved import left a partial playlist")
	}
	db.Raw("SELECT COUNT(*) FROM music_tracks").Scan(&n)
	if n != 0 {
		t.Fatal("unresolved import left shared metadata behind")
	}
	p.offline = true
	call(t, h, a, "POST", "/tracks/register", map[string]any{"track": forgedFixture("700099")}, 503)
}
func TestUnverifiedLegacyMetadataCannotBeReadByAnotherGuest(t *testing.T) {
	db := testDB(t)
	p := &canonicalTestProvider{tracks: map[string]Track{}}
	h := NewServer(db, Options{AppID: "canonical-app", Provider: p}).HTTP()
	a := guest(t, h)
	if err := db.Exec("INSERT INTO music_tracks(id,source,external_id,title) VALUES('legacy:private','legacy','private','private owner metadata')").Error; err != nil {
		t.Fatal(err)
	}
	call(t, h, a, "GET", "/tracks/legacy:private", nil, 404)
	call(t, h, a, "POST", "/playlists", map[string]any{"name": "claim foreign metadata", "trackIDs": []string{"legacy:private"}}, 404)
}
func TestAnonymousCompatibilityDenialIsNotAnOutage(t *testing.T) {
	for _, mode := range []string{"compat-http-denied", "item-denied"} {
		t.Run(mode, func(t *testing.T) {
			compatibilityCalls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/song/url/v1" {
					if mode == "compat-http-denied" {
						http.Error(w, "transport decode failure", 502)
						return
					}
					fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":403,"url":"https://m1.music.126.net/denied.mp3","expi":100,"freeTrialInfo":null}]}`)
					return
				}
				if r.URL.Path == "/song/url" {
					compatibilityCalls++
					http.Error(w, "access denied", 403)
					return
				}
				t.Errorf("unexpected endpoint %s", r.URL.Path)
			}))
			defer upstream.Close()
			res, err := (AnonymousNetease{BaseURL: upstream.URL}).Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
			if err != nil || res.Status != "unavailable" || res.URL != "" || !strings.Contains(res.Reason, "denied") {
				t.Fatalf("explicit source denial became an outage or playable URL: %+v err=%v", res, err)
			}
			if mode == "item-denied" && compatibilityCalls != 0 {
				t.Fatal("valid item permission denial fell through to compatibility source")
			}
		})
	}
}

func TestCanonicalRegistrationPreservesProviderDenial(t *testing.T) {
	db := testDB(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/song/detail" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		http.Error(w, "access denied", http.StatusForbidden)
	}))
	defer upstream.Close()
	h := NewServer(db, Options{AppID: "canonical-app", Provider: AnonymousNetease{BaseURL: upstream.URL}}).HTTP()
	token := guest(t, h)
	call(t, h, token, "POST", "/tracks/register", map[string]any{"track": forgedFixture("700006")}, http.StatusForbidden)
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM music_tracks").Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("source denial persisted submitted metadata")
	}
}

func TestCanonicalMigrationPreservesExistingLibrary(t *testing.T) {
	db := testDB(t)
	track := canonicalFixture("700005")
	provider := &canonicalTestProvider{tracks: map[string]Track{track.ID: track}}
	h := NewServer(db, Options{AppID: "migration-app", Provider: provider}).HTTP()
	token := guest(t, h)
	p := call(t, h, token, "POST", "/playlists", map[string]any{"name": "keep existing library", "trackIDs": []string{track.ID}}, 200)
	// Reproduce the schema before the additive canonical-verification migration.
	if err := db.Exec("ALTER TABLE music_tracks DROP COLUMN canonical_verified").Error; err != nil {
		t.Fatal(err)
	}
	opts := Options{DBProvider: func() *gorm.DB { return db }, AppID: "migration-app", Origin: "http://music.test", Addr: "127.0.0.1:0", SkipMigration: true}
	if err := NewModule(opts).Init(context.Background()); err == nil {
		t.Fatal("pre-migrated startup silently accepted the old unverified schema")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var verified bool
	if err := db.Raw("SELECT canonical_verified FROM music_tracks WHERE id=?", track.ID).Scan(&verified).Error; err != nil {
		t.Fatal(err)
	}
	if verified {
		t.Fatal("migration trusted an old catalogue row without provider verification")
	}
	if err := NewModule(opts).Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := call(t, h, token, "GET", "/playlists/"+p["id"].(string), nil, 200)
	if got["name"] != "keep existing library" || len(got["tracks"].([]any)) != 1 {
		t.Fatal("schema upgrade changed existing personal library")
	}
	requireCanonical(t, call(t, h, token, "GET", "/tracks/"+track.ID, nil, 200), track)
}
