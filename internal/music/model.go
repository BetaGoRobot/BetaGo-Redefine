// Package music owns personal music data independently of catalogue providers.
package music

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

// All track-bearing responses, including old verified library rows, share the
// same artwork guard without destructively rewriting personal catalogue data.
func (t Track) MarshalJSON() ([]byte, error) {
	type trackJSON Track
	out := trackJSON(t)
	out.Picture = securePicture(t.Picture)
	return json.Marshal(out)
}

type Playlist struct {
	ID                string        `json:"id"`
	OwnerID           string        `json:"-"`
	Name              string        `json:"name"`
	Revision          int64         `json:"revision"`
	Deleted           bool          `json:"-"`
	Tracks            []Track       `json:"tracks" gorm:"-"`
	Kind              string        `json:"kind,omitempty" gorm:"-"`
	Sync              *PlaylistSync `json:"sync,omitempty" gorm:"-"`
	SyncSourceID      string        `json:"-"`
	SyncLastSuccessAt *time.Time    `json:"-"`
	SyncLastAttemptAt *time.Time    `json:"-"`
	SyncLastError     string        `json:"-"`
	SyncLeaseUntil    *time.Time    `json:"-"`
}
type Queue struct {
	Revision     int64    `json:"revision"`
	TrackIDs     []string `json:"trackIDs" gorm:"-"`
	CurrentIndex int      `json:"currentIndex"`
	PositionMs   int64    `json:"positionMs"`
}
type Resolution struct {
	Delivery        string     `json:"delivery,omitempty"`
	TrackID         string     `json:"trackID"`
	Status          string     `json:"status"`
	URL             string     `json:"url,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	MediaSource     string     `json:"mediaSource,omitempty"`
	OriginTrackID   string     `json:"originTrackID,omitempty"`
	MatchMethod     string     `json:"matchMethod,omitempty"`
	Verification    string     `json:"verification,omitempty"`
	accountEligible bool
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
type Identity struct {
	AppID, TenantKey, Subject string
	DisplayName, AvatarURL    string
}
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
	return db.Transaction(func(tx *gorm.DB) error {
		for _, name := range []string{"20261113_music.sql", "20261113_music_canonical.sql", "20261116_music_profile.sql", "20261116_music_recommendations.sql", "20261123_music_playlist_sync.sql"} {
			b, err := sqlfiles.Files.ReadFile(name)
			if err != nil {
				return err
			}
			if err = tx.Exec(string(b)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Raw SQL plus the same bypass setting as infrastructure/db avoids its generic query cache.
func uncached(db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true}).Set("betago:bypass_query_cache", true)
}

// ensureTrack only returns shared metadata that a server-side provider verified.
// Older cache rows are deliberately unverified until the provider repairs them.
func ensureTrack(ctx context.Context, db *gorm.DB, p Provider, id string) (Track, error) {
	if err := validateID(id); err != nil {
		return Track{}, err
	}
	var row struct {
		Track             `gorm:"embedded"`
		CanonicalVerified bool
	}
	r := db.Raw("SELECT * FROM music_tracks WHERE id=?", id).Scan(&row)
	if r.Error != nil {
		return Track{}, r.Error
	}
	if r.RowsAffected > 0 && row.CanonicalVerified {
		return row.Track, nil
	}
	if p == nil {
		return Track{}, missing
	}
	t, err := p.Detail(ctx, id)
	if err != nil {
		return Track{}, err
	}
	if t.ID != id {
		return Track{}, errors.New("provider identity mismatch")
	}
	return t, saveTrack(db, t)
}

// Client metadata can identify a recording but cannot define the shared catalogue.
func canonicalInputTrack(ctx context.Context, db *gorm.DB, p Provider, t Track) (Track, error) {
	if validateID(t.ID) != nil || t.ID != t.Source+":"+t.ExternalID {
		return Track{}, invalid("invalid_track")
	}
	return ensureTrack(ctx, db, p, t.ID)
}

// saveTrack is only for trusted server-side provider results, not client JSON.
func saveTrack(db *gorm.DB, t Track) error {
	if validateID(t.ID) != nil || t.ID != t.Source+":"+t.ExternalID || len(t.Title) > 1000 || len(t.Artist) > 2000 || len(t.Album) > 2000 || len(t.Picture) > 4000 || t.DurationMs < 0 {
		return invalid("invalid_track")
	}
	return db.Exec(`INSERT INTO music_tracks(id,source,external_id,title,artist,album,picture,duration_ms,canonical_verified) VALUES(?,?,?,?,?,?,?,?,true)
 ON CONFLICT(id) DO UPDATE SET title=EXCLUDED.title,artist=EXCLUDED.artist,album=EXCLUDED.album,picture=EXCLUDED.picture,duration_ms=EXCLUDED.duration_ms,canonical_verified=true`, t.ID, t.Source, t.ExternalID, t.Title, t.Artist, t.Album, t.Picture, t.DurationMs).Error
}

// One statement gives the revision, sync status and ordered tracks the same MVCC snapshot.
func readPlaylists(db *gorm.DB, owner, id string) ([]Playlist, error) {
	type row struct {
		Playlist     `gorm:"embedded"`
		TrackPayload []byte
	}
	query := `SELECT p.id, p.owner_id, p.name, p.revision, p.sync_source_id,
		p.sync_last_success_at, p.sync_last_attempt_at, p.sync_last_error, p.sync_lease_until,
		COALESCE((
			SELECT jsonb_agg(jsonb_build_object(
				'id', t.id, 'source', t.source, 'externalID', t.external_id,
				'title', t.title, 'artist', t.artist, 'album', t.album,
				'picture', t.picture, 'durationMs', t.duration_ms
			) ORDER BY entry.position)
			FROM (
				SELECT track_id, position::bigint FROM music_entries
				WHERE playlist_id = p.id AND p.sync_source_id IS NULL
				UNION ALL
				SELECT x.track_id, x.position FROM jsonb_array_elements_text(p.sync_track_ids)
					WITH ORDINALITY AS x(track_id, position)
			) entry JOIN music_tracks t ON t.id = entry.track_id
		), '[]'::jsonb) AS track_payload
		FROM music_playlists p WHERE p.owner_id = ? AND NOT p.deleted`
	args := []any{owner}
	if id != "" {
		query += " AND p.id = ?"
		args = append(args, id)
	}
	var rows []row
	if err := db.Raw(query+" ORDER BY p.id", args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Playlist, 0, len(rows))
	for _, r := range rows {
		p := r.Playlist
		if err := json.Unmarshal(r.TrackPayload, &p.Tracks); err != nil {
			return nil, err
		}
		p.setSyncInfo()
		out = append(out, p)
	}
	return out, nil
}

func playlist(db *gorm.DB, owner, id string) (Playlist, error) {
	if id == "" {
		return Playlist{}, missing
	}
	rows, err := readPlaylists(db, owner, id)
	if err != nil {
		return Playlist{}, err
	}
	if len(rows) == 0 {
		return Playlist{}, missing
	}
	return rows[0], nil
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
	encoded, err := json.Marshal(unique(ids))
	if err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO music_entries(playlist_id,track_id,position)
		SELECT ?, track_id, position - 1
		FROM jsonb_array_elements_text(CAST(? AS jsonb)) WITH ORDINALITY AS x(track_id,position)`, id, string(encoded)).Error
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
