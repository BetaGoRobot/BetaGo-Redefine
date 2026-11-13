// Package music owns personal music data independently of catalogue providers.
package music

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	sqlfiles "github.com/BetaGoRobot/BetaGo-Redefine/script/sql"
	"gorm.io/gorm"
)

type Track struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	ExternalID string `json:"externalID"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	Picture    string `json:"picture"`
	DurationMs int64  `json:"durationMs"`
}
type Playlist struct {
	ID       string  `json:"id"`
	OwnerID  string  `json:"-"`
	Name     string  `json:"name"`
	Revision int64   `json:"revision"`
	Deleted  bool    `json:"-"`
	Tracks   []Track `json:"tracks" gorm:"-"`
}
type Queue struct {
	Revision     int64    `json:"revision"`
	TrackIDs     []string `json:"trackIDs" gorm:"-"`
	CurrentIndex int      `json:"currentIndex"`
	PositionMs   int64    `json:"positionMs"`
}
type Resolution struct {
	TrackID   string     `json:"trackID"`
	Status    string     `json:"status"`
	URL       string     `json:"url,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Reason    string     `json:"reason,omitempty"`
}
type Lyrics struct {
	TrackID       string `json:"trackID"`
	LRC           string `json:"lrc"`
	TranslatedLRC string `json:"translatedLrc"`
}
type ExportPlaylist struct {
	Name   string  `json:"name"`
	Tracks []Track `json:"tracks"`
}
type Export struct {
	Format    string           `json:"format"`
	Version   int              `json:"version"`
	Playlists []ExportPlaylist `json:"playlists"`
	Likes     []Track          `json:"likes"`
}
type Provider interface {
	Search(context.Context, string) ([]Track, error)
	Detail(context.Context, string) (Track, error)
	Resolve(context.Context, Track) (Resolution, error)
	Lyrics(context.Context, Track) (Lyrics, error)
}
type Identity struct{ AppID, TenantKey, Subject string }
type IdentityVerifier interface {
	Verify(context.Context, string) (Identity, error)
}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return e.Message }
func invalid(s string) error      { return &apiError{400, s} }

var missing = &apiError{404, "not_found"}
var conflict = &apiError{409, "revision_conflict: reload before editing"}
var unavailable = &apiError{503, "catalogue_unavailable"}
var trackIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}:[A-Za-z0-9._-]{1,180}$`)
var devicePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func validateID(id string) error {
	if !trackIDPattern.MatchString(id) {
		return invalid("invalid_track_id")
	}
	return nil
}
func secret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Migrate(db *gorm.DB) error {
	b, err := sqlfiles.Files.ReadFile("20261113_music.sql")
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error { return tx.Exec(string(b)).Error })
}

// Raw SQL plus the same bypass setting as infrastructure/db avoids its generic query cache.
func uncached(db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true}).Set("betago:bypass_query_cache", true)
}
func ensureTrack(ctx context.Context, db *gorm.DB, p Provider, id string) (Track, error) {
	if err := validateID(id); err != nil {
		return Track{}, err
	}
	var t Track
	r := db.Raw("SELECT * FROM music_tracks WHERE id=?", id).Scan(&t)
	if r.Error != nil {
		return t, r.Error
	}
	if r.RowsAffected > 0 {
		return t, nil
	}
	if p == nil {
		return t, missing
	}
	t, err := p.Detail(ctx, id)
	if err != nil {
		return t, err
	}
	if t.ID != id {
		return t, errors.New("provider identity mismatch")
	}
	return t, saveTrack(db, t)
}
func saveTrack(db *gorm.DB, t Track) error {
	if validateID(t.ID) != nil || t.ID != t.Source+":"+t.ExternalID || len(t.Title) > 1000 || len(t.Artist) > 2000 || len(t.Picture) > 4000 || t.DurationMs < 0 {
		return invalid("invalid_track")
	}
	return db.Exec(`INSERT INTO music_tracks(id,source,external_id,title,artist,album,picture,duration_ms) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, t.ID, t.Source, t.ExternalID, t.Title, t.Artist, t.Album, t.Picture, t.DurationMs).Error
}
func playlist(db *gorm.DB, owner, id string) (Playlist, error) {
	p := Playlist{Tracks: []Track{}}
	r := db.Raw("SELECT * FROM music_playlists WHERE id=? AND owner_id=? AND deleted=false", id, owner).Scan(&p)
	if r.Error != nil {
		return p, r.Error
	}
	if r.RowsAffected == 0 {
		return p, missing
	}
	err := db.Raw(`SELECT t.* FROM music_tracks t JOIN music_entries e ON e.track_id=t.id WHERE e.playlist_id=? ORDER BY e.position`, id).Scan(&p.Tracks).Error
	if p.Tracks == nil {
		p.Tracks = []Track{}
	}
	return p, err
}
func unique(ids []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}
func entries(tx *gorm.DB, id string, ids []string) error {
	if err := tx.Exec("DELETE FROM music_entries WHERE playlist_id=?", id).Error; err != nil {
		return err
	}
	for n, t := range unique(ids) {
		if err := tx.Exec("INSERT INTO music_entries(playlist_id,track_id,position) VALUES(?,?,?)", id, t, n).Error; err != nil {
			return err
		}
	}
	return nil
}
func createPlaylist(tx *gorm.DB, owner, name string, ids []string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || len(ids) > 1000 {
		return "", invalid("invalid_playlist")
	}
	id := secret()
	if err := tx.Exec("INSERT INTO music_playlists(id,owner_id,name) VALUES(?,?,?)", id, owner, name).Error; err != nil {
		return "", err
	}
	return id, entries(tx, id, ids)
}
