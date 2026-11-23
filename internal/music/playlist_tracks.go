package music

import (
	"net/http"
	"slices"

	"gorm.io/gorm"
)

var syncPlaylistReadOnly = &apiError{409, "sync_playlist_read_only"}

func playlistRow(db *gorm.DB, owner, id string, lock bool) (Playlist, error) {
	query := "SELECT id, revision, sync_source_id, sync_lease_until FROM music_playlists WHERE id=? AND owner_id=? AND NOT deleted"
	if lock {
		query += " FOR UPDATE"
	}
	var p Playlist
	res := db.Raw(query, id, owner).Scan(&p)
	if res.Error != nil {
		return p, res.Error
	}
	if res.RowsAffected == 0 {
		return p, missing
	}
	return p, nil
}

func editablePlaylist(db *gorm.DB, owner, id string, lock bool) (Playlist, error) {
	p, err := playlistRow(db, owner, id, lock)
	if err == nil && p.SyncSourceID != "" {
		return p, syncPlaylistReadOnly
	}
	return p, err
}

type playlistAddResult struct {
	Playlist    Playlist `json:"playlist"`
	AddedIDs    []string `json:"addedIDs"`
	ExistingIDs []string `json:"existingIDs"`
	OverflowIDs []string `json:"overflowIDs"`
}

func (s *Server) prependPlaylistTracks(r *http.Request, owner, id string) (any, error) {
	db := s.db.WithContext(r.Context())
	if _, err := editablePlaylist(db, owner, id, false); err != nil {
		return nil, err
	}
	var in struct {
		TrackIDs []string `json:"trackIDs"`
	}
	if err := body(r, &in); err != nil {
		return nil, err
	}
	if len(in.TrackIDs) == 0 || len(in.TrackIDs) > 1000 {
		return nil, invalid("invalid_track_count")
	}
	wanted := unique(in.TrackIDs)
	for _, trackID := range wanted {
		if err := validateID(trackID); err != nil {
			return nil, err
		}
	}
	result := playlistAddResult{AddedIDs: []string{}, ExistingIDs: []string{}, OverflowIDs: []string{}}
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := editablePlaylist(tx, owner, id, true); err != nil {
			return err
		}
		var old []string
		if err := tx.Raw("SELECT track_id FROM music_entries WHERE playlist_id=? ORDER BY position", id).Scan(&old).Error; err != nil {
			return err
		}
		present := make(map[string]bool, len(old))
		for _, id := range old {
			present[id] = true
		}
		for _, id := range wanted {
			switch {
			case present[id]:
				result.ExistingIDs = append(result.ExistingIDs, id)
			case len(old)+len(result.AddedIDs) >= 1000:
				result.OverflowIDs = append(result.OverflowIDs, id)
			default:
				result.AddedIDs = append(result.AddedIDs, id)
			}
		}
		if len(result.AddedIDs) != 0 {
			// Lock catalogue writes in a stable order, independent of playlist order.
			// Existing and overflow IDs require no metadata lookups.
			toVerify := slices.Clone(result.AddedIDs)
			slices.Sort(toVerify)
			for _, trackID := range toVerify {
				if _, err := ensureTrack(r.Context(), tx, s.opts.Provider, trackID); err != nil {
					return err
				}
			}
			ordered := make([]string, 0, len(old)+len(result.AddedIDs))
			ordered = append(ordered, result.AddedIDs...)
			if err := entries(tx, id, append(ordered, old...)); err != nil {
				return err
			}
			if err := tx.Exec("UPDATE music_playlists SET revision=revision+1 WHERE id=?", id).Error; err != nil {
				return err
			}
		}
		var err error
		result.Playlist, err = playlist(tx, owner, id)
		return err
	})
	return result, err
}
