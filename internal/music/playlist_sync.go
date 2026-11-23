package music

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

const (
	playlistSyncDay       = 24 * time.Hour
	playlistSyncCooldown  = 30 * time.Second
	playlistSyncLease     = 45 * time.Second
	playlistSyncLimit     = 25
	playlistSyncSweepSize = 12
)

var (
	playlistSyncDisabled      = &apiError{503, "playlist_sync_not_configured"}
	playlistSyncBusy          = &apiError{429, "playlist_sync_busy"}
	playlistSyncDuplicate     = &apiError{409, "playlist_sync_already_exists"}
	playlistSyncNotSubscribed = &apiError{409, "not_a_sync_playlist"}
	playlistSyncInProgress    = &apiError{409, "playlist_sync_in_progress"}
	playlistSyncTooSoon       = &apiError{429, "playlist_sync_too_soon"}
)

type PlaylistSync struct {
	Source           string     `json:"source"`
	SourcePlaylistID string     `json:"sourcePlaylistID"`
	LastSuccessAt    *time.Time `json:"lastSuccessAt,omitempty"`
	LastAttemptAt    *time.Time `json:"lastAttemptAt,omitempty"`
	LastError        string     `json:"lastError,omitempty"`
	RunningUntil     *time.Time `json:"runningUntil,omitempty"`
}

func (p *Playlist) setSyncInfo() {
	if p.SyncSourceID != "" {
		p.Kind = "sync"
		p.Sync = &PlaylistSync{
			Source: "netease", SourcePlaylistID: p.SyncSourceID,
			LastSuccessAt: p.SyncLastSuccessAt, LastAttemptAt: p.SyncLastAttemptAt,
			LastError: p.SyncLastError, RunningUntil: p.SyncLeaseUntil,
		}
	}
}

type playlistSyncLimiter struct {
	inFlight chan struct{}
	starts   *rate.Limiter
}

func newPlaylistSyncLimiter() playlistSyncLimiter {
	return playlistSyncLimiter{inFlight: make(chan struct{}, 2), starts: rate.NewLimiter(rate.Every(time.Second), 4)}
}

func (l playlistSyncLimiter) enter(now time.Time) bool {
	select {
	case l.inFlight <- struct{}{}:
		if l.starts.AllowN(now, 1) {
			return true
		}
		<-l.inFlight
	default:
	}
	return false
}

func (l playlistSyncLimiter) leave() { <-l.inFlight }

type playlistSnapshot struct {
	name   string
	ids    string
	tracks []Track
}

func (s *Server) fetchPlaylistSnapshot(ctx context.Context, id string) (playlistSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 14*time.Second)
	defer cancel()
	public, err := s.opts.PlaylistImporter.PublicPlaylist(ctx, id)
	if err != nil {
		return playlistSnapshot{}, err
	}
	incomplete := &apiError{422, "public_playlist_metadata_incomplete"}
	if public.ID != id || strings.TrimSpace(public.Name) == "" || len(public.Name) > 200 || public.Tracks == nil {
		return playlistSnapshot{}, incomplete
	}
	if len(public.Tracks) > 1000 {
		return playlistSnapshot{}, &apiError{413, "public_playlist_too_large"}
	}
	ids := make([]string, 0, len(public.Tracks))
	byID := make(map[string]Track, len(public.Tracks))
	for _, track := range public.Tracks {
		if !validPublicPlaylistTrack(track) {
			return playlistSnapshot{}, incomplete
		}
		ids = append(ids, track.ID)
		byID[track.ID] = track
	}
	tracks := make([]Track, 0, len(byID))
	for _, track := range byID {
		tracks = append(tracks, track)
	}
	// Converge on one lock order if concurrent snapshots share catalogue tracks.
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].ID < tracks[j].ID })
	encoded, err := json.Marshal(ids)
	return playlistSnapshot{name: public.Name, ids: string(encoded), tracks: tracks}, err
}

func (p playlistSnapshot) saveCatalogue(tx *gorm.DB) error {
	for _, track := range p.tracks {
		if err := saveTrack(tx, track); err != nil {
			return err
		}
	}
	return nil
}

func checkSubscriptionLimit(db *gorm.DB, owner, source string) error {
	var current struct{ Total, Same int }
	err := db.Raw(`SELECT count(*) AS total, count(*) FILTER (WHERE sync_source_id=?) AS same
		FROM music_playlists WHERE owner_id=? AND NOT deleted AND sync_source_id IS NOT NULL`, source, owner).Scan(&current).Error
	if err != nil {
		return err
	}
	if current.Same != 0 {
		return playlistSyncDuplicate
	}
	if current.Total >= playlistSyncLimit {
		return &apiError{409, "playlist_sync_limit"}
	}
	return nil
}

func (s *Server) createPlaylistSync(r *http.Request, a account) (any, error) {
	if !s.authOptions.Library.NeteasePlaylistSync {
		return nil, playlistSyncDisabled
	}
	var in struct {
		URLOrID string `json:"urlOrID"`
	}
	if err := body(r, &in); err != nil {
		return nil, err
	}
	if !s.playlistSyncLimiter.enter(s.opts.Now()) {
		return nil, playlistSyncBusy
	}
	defer s.playlistSyncLimiter.leave()
	ctx := r.Context()
	source, err := playlistSyncID(ctx, in.URLOrID, nil)
	if err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	if err := checkSubscriptionLimit(db, a.ID, source); err != nil {
		return nil, err
	}
	snapshot, err := s.fetchPlaylistSnapshot(ctx, source)
	if err != nil {
		return nil, err
	}
	var created Playlist
	err = db.Transaction(func(tx *gorm.DB) error {
		// The account lock serializes duplicate and capacity checks across instances.
		if err := tx.Exec("SELECT 1 FROM music_users WHERE id=? FOR NO KEY UPDATE", a.ID).Error; err != nil {
			return err
		}
		if err := checkSubscriptionLimit(tx, a.ID, source); err != nil {
			return err
		}
		if err := snapshot.saveCatalogue(tx); err != nil {
			return err
		}
		id, now := secret(), s.opts.Now()
		if err := tx.Exec(`INSERT INTO music_playlists
			(id, owner_id, name, sync_source_id, sync_track_ids, sync_last_success_at, sync_next_auto_at)
			VALUES (?, ?, ?, ?, CAST(? AS jsonb), ?, ?)`,
			id, a.ID, snapshot.name, source, snapshot.ids, now, now.Add(playlistSyncDay)).Error; err != nil {
			return err
		}
		var err error
		created, err = playlist(tx, a.ID, id)
		return err
	})
	return created, err
}

func (s *Server) claimPlaylistSync(db *gorm.DB, owner, id, token string, now time.Time, automatic bool) (string, error) {
	query := `UPDATE music_playlists SET sync_lease_token=?, sync_lease_until=?, sync_last_attempt_at=?
		WHERE owner_id=? AND id=? AND NOT deleted AND sync_source_id IS NOT NULL
		AND (sync_lease_until IS NULL OR sync_lease_until <= ?)
		AND (sync_last_attempt_at IS NULL OR sync_last_attempt_at <= ?)`
	args := []any{token, now.Add(playlistSyncLease), now, owner, id, now, now.Add(-playlistSyncCooldown)}
	if automatic {
		query += " AND sync_next_auto_at <= ?"
		args = append(args, now)
	}
	var source string
	claimed := db.Raw(query+" RETURNING sync_source_id", args...).Scan(&source)
	if claimed.Error != nil || claimed.RowsAffected != 0 {
		return source, claimed.Error
	}
	if automatic {
		return "", nil
	}
	p, err := playlistRow(db, owner, id, false)
	if err != nil {
		return "", err
	}
	if p.SyncSourceID == "" {
		return "", playlistSyncNotSubscribed
	}
	if p.SyncLeaseUntil != nil && p.SyncLeaseUntil.After(now) {
		return "", playlistSyncInProgress
	}
	return "", playlistSyncTooSoon
}

// Manual and daily sync share the same owner-scoped claim, fetch and compare-and-swap.
func (s *Server) syncPlaylist(ctx context.Context, owner, id string, automatic bool) (Playlist, error) {
	if !s.authOptions.Library.NeteasePlaylistSync {
		return Playlist{}, playlistSyncDisabled
	}
	now := s.opts.Now()
	if !s.playlistSyncLimiter.enter(now) {
		return Playlist{}, playlistSyncBusy
	}
	defer s.playlistSyncLimiter.leave()
	db, token := s.db.WithContext(ctx), secret()
	source, err := s.claimPlaylistSync(db, owner, id, token, now, automatic)
	if err != nil || source == "" {
		return Playlist{}, err
	}
	snapshot, err := s.fetchPlaylistSnapshot(ctx, source)
	var updated Playlist
	if err == nil {
		err = db.Transaction(func(tx *gorm.DB) error {
			finished := s.opts.Now()
			res := tx.Exec(`UPDATE music_playlists SET
				revision=revision + CASE WHEN name IS DISTINCT FROM ? OR sync_track_ids IS DISTINCT FROM CAST(? AS jsonb) THEN 1 ELSE 0 END,
				name=?, sync_track_ids=CAST(? AS jsonb), sync_last_success_at=?, sync_last_error=NULL,
				sync_lease_token=NULL, sync_lease_until=NULL,
				sync_next_auto_at=CASE WHEN ? THEN ? ELSE sync_next_auto_at END
				WHERE id=? AND owner_id=? AND NOT deleted AND sync_lease_token=? AND sync_lease_until > ?`,
				snapshot.name, snapshot.ids, snapshot.name, snapshot.ids, finished, automatic,
				finished.Add(playlistSyncDay), id, owner, token, finished)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return &apiError{409, "playlist_sync_superseded"}
			}
			if err := snapshot.saveCatalogue(tx); err != nil {
				return err
			}
			var err error
			updated, err = playlist(tx, owner, id)
			return err
		})
	}
	if err != nil {
		s.recordPlaylistSyncFailure(ctx, owner, id, token, automatic, err)
	}
	return updated, err
}

func (s *Server) recordPlaylistSyncFailure(ctx context.Context, owner, id, token string, automatic bool, err error) {
	code := "playlist_sync_update_failed"
	var e *apiError
	if errors.As(err, &e) {
		code = e.Message
	}
	// Preserve failure status even when the original browser or provider timed out.
	advanceAuto := automatic && ctx.Err() == nil
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	res := s.db.WithContext(ctx).Exec(`UPDATE music_playlists SET sync_last_error=?,
		sync_lease_token=NULL, sync_lease_until=NULL,
		sync_next_auto_at=CASE WHEN ? THEN ? ELSE sync_next_auto_at END
		WHERE id=? AND owner_id=? AND NOT deleted AND sync_lease_token=?`,
		code, advanceAuto, s.opts.Now().Add(playlistSyncDay), id, owner, token)
	if res.Error != nil {
		log.Print("music playlist sync could not persist failure status")
	}
}

func (s *Server) runPlaylistSyncSweep(ctx context.Context) {
	now := s.opts.Now()
	var due []struct{ ID, OwnerID string }
	err := s.db.WithContext(ctx).Raw(`SELECT p.id, p.owner_id FROM music_playlists p
		JOIN music_users u ON u.id=p.owner_id
		WHERE u.app_id=? AND NOT p.deleted AND p.sync_source_id IS NOT NULL
		AND p.sync_next_auto_at <= ? AND (p.sync_last_attempt_at IS NULL OR p.sync_last_attempt_at <= ?)
		AND (p.sync_lease_until IS NULL OR p.sync_lease_until <= ?)
		ORDER BY p.sync_next_auto_at, p.id LIMIT ?`, s.opts.AppID, now, now.Add(-playlistSyncCooldown), now, playlistSyncSweepSize).Scan(&due).Error
	if err != nil {
		if ctx.Err() == nil {
			log.Print("music playlist sync could not list due subscriptions")
		}
		return
	}
	succeeded, failed := 0, 0
	for _, p := range due {
		if ctx.Err() != nil {
			break
		}
		synced, err := s.syncPlaylist(ctx, p.OwnerID, p.ID, true)
		if errors.Is(err, playlistSyncBusy) {
			break
		}
		if err != nil {
			failed++
		} else if synced.ID != "" {
			succeeded++
		}
	}
	if succeeded+failed != 0 {
		log.Printf("music playlist sync daily sweep: success=%d failed=%d", succeeded, failed)
	}
}
