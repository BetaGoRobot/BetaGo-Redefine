package music

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

func (s *Server) playlists(r *http.Request, a account, path string) (any, error) {
	db := s.db.WithContext(r.Context())
	if path == "playlists" && r.Method == "GET" {
		ps := []Playlist{}
		if err := db.Raw("SELECT * FROM music_playlists WHERE owner_id=? AND deleted=false ORDER BY id", a.ID).Scan(&ps).Error; err != nil {
			return nil, err
		}
		for n := range ps {
			p, err := playlist(db, a.ID, ps[n].ID)
			if err != nil {
				return nil, err
			}
			ps[n] = p
		}
		return map[string]any{"playlists": ps}, nil
	}
	var in struct {
		Name     string   `json:"name"`
		TrackIDs []string `json:"trackIDs"`
		Revision int64    `json:"revision"`
	}
	if r.Method != "GET" {
		if err := body(r, &in); err != nil {
			return nil, err
		}
	}
	if len(in.TrackIDs) > 1000 {
		return nil, invalid("too_many_tracks")
	}
	id := strings.TrimPrefix(path, "playlists/")
	if path == "playlists" && r.Method == "POST" {
		for _, tid := range unique(in.TrackIDs) {
			if _, err := ensureTrack(r.Context(), db, s.opts.Provider, tid); err != nil {
				return nil, err
			}
		}
		err := db.Transaction(func(tx *gorm.DB) error { var e error; id, e = createPlaylist(tx, a.ID, in.Name, in.TrackIDs); return e })
		if err != nil {
			return nil, err
		}
		return playlist(db, a.ID, id)
	}
	if path == "playlists" {
		return nil, missing
	}
	if r.Method == "GET" {
		return playlist(db, a.ID, id)
	}
	if r.Method != "PUT" && r.Method != "DELETE" {
		return nil, missing
	}
	if r.Method == "PUT" {
		if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 {
			return nil, invalid("invalid_name")
		}
		for _, tid := range unique(in.TrackIDs) {
			if _, err := ensureTrack(r.Context(), db, s.opts.Provider, tid); err != nil {
				return nil, err
			}
		}
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		p := Playlist{}
		res := tx.Raw("SELECT * FROM music_playlists WHERE id=? AND owner_id=? FOR UPDATE", id, a.ID).Scan(&p)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 || p.Deleted {
			return missing
		}
		if p.Revision != in.Revision {
			return conflict
		}
		if r.Method == "DELETE" {
			return tx.Exec("UPDATE music_playlists SET deleted=true,revision=revision+1 WHERE id=?", id).Error
		}
		if err := tx.Exec("UPDATE music_playlists SET name=?,revision=revision+1 WHERE id=?", strings.TrimSpace(in.Name), id).Error; err != nil {
			return err
		}
		return entries(tx, id, in.TrackIDs)
	})
	if err != nil {
		return nil, err
	}
	if r.Method == "DELETE" {
		return map[string]bool{"ok": true}, nil
	}
	return playlist(db, a.ID, id)
}
func (s *Server) likes(r *http.Request, a account, parts []string) (any, error) {
	db := s.db.WithContext(r.Context())
	if len(parts) == 1 && r.Method == "GET" {
		ts := []Track{}
		err := db.Raw("SELECT t.* FROM music_tracks t JOIN music_likes l ON l.track_id=t.id WHERE l.owner_id=? ORDER BY t.id", a.ID).Scan(&ts).Error
		return map[string]any{"tracks": ts}, err
	}
	if len(parts) != 2 {
		return nil, missing
	}
	id := parts[1]
	if r.Method == "PUT" {
		if _, err := ensureTrack(r.Context(), db, s.opts.Provider, id); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, db.Exec("INSERT INTO music_likes(owner_id,track_id) VALUES(?,?) ON CONFLICT DO NOTHING", a.ID, id).Error
	}
	if r.Method == "DELETE" {
		return map[string]bool{"ok": true}, db.Exec("DELETE FROM music_likes WHERE owner_id=? AND track_id=?", a.ID, id).Error
	}
	return nil, missing
}
func (s *Server) history(r *http.Request, a account) (any, error) {
	db := s.db.WithContext(r.Context())
	if r.Method == "GET" {
		type row struct {
			TrackID    string
			PositionMs int64
			PlayedAt   time.Time
		}
		rows := []row{}
		if err := db.Raw("SELECT track_id,position_ms,played_at FROM music_history WHERE owner_id=? ORDER BY played_at DESC LIMIT 200", a.ID).Scan(&rows).Error; err != nil {
			return nil, err
		}
		out := []any{}
		for _, v := range rows {
			var t Track
			if err := db.Raw("SELECT * FROM music_tracks WHERE id=?", v.TrackID).Scan(&t).Error; err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"track": t, "positionMs": v.PositionMs, "playedAt": v.PlayedAt})
		}
		return map[string]any{"history": out}, nil
	}
	if r.Method == "DELETE" {
		return map[string]bool{"ok": true}, db.Exec("DELETE FROM music_history WHERE owner_id=?", a.ID).Error
	}
	if r.Method == "POST" {
		var in struct {
			TrackID    string `json:"trackID"`
			PositionMs int64  `json:"positionMs"`
		}
		if err := body(r, &in); err != nil {
			return nil, err
		}
		if in.PositionMs < 0 || in.PositionMs > 86400000 {
			return nil, invalid("invalid_position")
		}
		if _, err := ensureTrack(r.Context(), db, s.opts.Provider, in.TrackID); err != nil {
			return nil, err
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`INSERT INTO music_history(owner_id,track_id,position_ms,played_at) VALUES(?,?,?,?) ON CONFLICT(owner_id,track_id) DO UPDATE SET position_ms=EXCLUDED.position_ms,played_at=EXCLUDED.played_at`, a.ID, in.TrackID, in.PositionMs, s.opts.Now()).Error; err != nil {
				return err
			}
			return tx.Exec(`DELETE FROM music_history WHERE owner_id=? AND track_id NOT IN (SELECT track_id FROM music_history WHERE owner_id=? ORDER BY played_at DESC LIMIT 200)`, a.ID, a.ID).Error
		})
		return map[string]bool{"ok": true}, err
	}
	return nil, missing
}
func readQueue(db *gorm.DB, owner, device string) (Queue, error) {
	var row struct {
		Revision     int64
		TrackIDs     string
		CurrentIndex int
		PositionMs   int64
	}
	res := db.Raw("SELECT revision,track_ids::text, current_index,position_ms FROM music_queues WHERE owner_id=? AND device_id=?", owner, device).Scan(&row)
	q := Queue{Revision: row.Revision, TrackIDs: []string{}, CurrentIndex: row.CurrentIndex, PositionMs: row.PositionMs}
	if res.Error != nil {
		return q, res.Error
	}
	if row.TrackIDs != "" {
		if err := json.Unmarshal([]byte(row.TrackIDs), &q.TrackIDs); err != nil {
			return q, err
		}
	}
	return q, nil
}
func (s *Server) queue(r *http.Request, a account, device string) (any, error) {
	if !devicePattern.MatchString(device) {
		return nil, invalid("invalid_device")
	}
	db := s.db.WithContext(r.Context())
	if r.Method == "GET" {
		return readQueue(db, a.ID, device)
	}
	if r.Method != "PUT" {
		return nil, missing
	}
	var q Queue
	if err := body(r, &q); err != nil {
		return nil, err
	}
	if len(q.TrackIDs) > 1000 || q.PositionMs < 0 || q.PositionMs > 86400000 || q.CurrentIndex < 0 || (len(q.TrackIDs) == 0 && (q.CurrentIndex != 0 || q.PositionMs != 0)) || (len(q.TrackIDs) > 0 && q.CurrentIndex >= len(q.TrackIDs)) {
		return nil, invalid("invalid_queue")
	}
	if q.TrackIDs == nil {
		q.TrackIDs = []string{}
	}
	for _, id := range unique(q.TrackIDs) {
		if _, err := ensureTrack(r.Context(), db, s.opts.Provider, id); err != nil {
			return nil, err
		}
	}
	ids, _ := json.Marshal(q.TrackIDs)
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("INSERT INTO music_queues(owner_id,device_id) VALUES(?,?) ON CONFLICT DO NOTHING", a.ID, device).Error; err != nil {
			return err
		}
		res := tx.Exec("UPDATE music_queues SET revision=revision+1,track_ids=?::jsonb,current_index=?,position_ms=? WHERE owner_id=? AND device_id=? AND revision=?", string(ids), q.CurrentIndex, q.PositionMs, a.ID, device, q.Revision)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return conflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	q.Revision++
	return q, nil
}
func (s *Server) export(db *gorm.DB, owner string) (Export, error) {
	out := Export{Format: "betago-music", Version: 1, Playlists: []ExportPlaylist{}, Likes: []Track{}}
	ps := []Playlist{}
	if err := db.Raw("SELECT * FROM music_playlists WHERE owner_id=? AND deleted=false ORDER BY id", owner).Scan(&ps).Error; err != nil {
		return out, err
	}
	for _, p := range ps {
		v, err := playlist(db, owner, p.ID)
		if err != nil {
			return out, err
		}
		out.Playlists = append(out.Playlists, ExportPlaylist{Name: p.Name, Tracks: v.Tracks})
	}
	return out, db.Raw("SELECT t.* FROM music_tracks t JOIN music_likes l ON l.track_id=t.id WHERE l.owner_id=? ORDER BY t.id", owner).Scan(&out.Likes).Error
}
func (s *Server) importData(db *gorm.DB, owner string, in Export) (any, error) {
	if in.Format != "betago-music" || in.Version != 1 || len(in.Playlists) > 50 || len(in.Likes) > 1000 {
		return nil, invalid("unsupported_export")
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, p := range in.Playlists {
			if len(p.Tracks) > 1000 {
				return invalid("too_many_tracks")
			}
			ids := []string{}
			for _, t := range p.Tracks {
				if err := saveTrack(tx, t); err != nil {
					return err
				}
				ids = append(ids, t.ID)
			}
			if _, err := createPlaylist(tx, owner, p.Name, ids); err != nil {
				return err
			}
		}
		for _, t := range in.Likes {
			if err := saveTrack(tx, t); err != nil {
				return err
			}
			if err := tx.Exec("INSERT INTO music_likes(owner_id,track_id) VALUES(?,?) ON CONFLICT DO NOTHING", owner, t.ID).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return map[string]int{"imported": len(in.Playlists)}, err
}
func (s *Server) shares(r *http.Request, a account, parts []string) (any, error) {
	db := s.db.WithContext(r.Context())
	if len(parts) == 1 && r.Method == "GET" {
		type row struct {
			ID        string    `json:"id"`
			Kind      string    `json:"kind"`
			TargetID  string    `json:"targetID"`
			Revoked   bool      `json:"revoked"`
			ExpiresAt time.Time `json:"expiresAt"`
		}
		rows := []row{}
		err := db.Raw("SELECT id,kind,target_id,revoked,expires_at FROM music_shares WHERE owner_id=? ORDER BY id", a.ID).Scan(&rows).Error
		return map[string]any{"shares": rows}, err
	}
	if len(parts) == 2 && r.Method == "DELETE" {
		res := db.Exec("UPDATE music_shares SET revoked=true WHERE id=? AND owner_id=?", parts[1], a.ID)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			return nil, missing
		}
		return map[string]bool{"ok": true}, nil
	}
	if len(parts) == 1 && r.Method == "POST" {
		var in struct {
			Kind     string `json:"kind"`
			TargetID string `json:"targetID"`
		}
		if err := body(r, &in); err != nil {
			return nil, err
		}
		if in.Kind == "playlist" {
			if _, err := playlist(db, a.ID, in.TargetID); err != nil {
				return nil, err
			}
		} else if in.Kind == "track" {
			if _, err := ensureTrack(r.Context(), db, s.opts.Provider, in.TargetID); err != nil {
				return nil, err
			}
		} else {
			return nil, invalid("invalid_kind")
		}
		id, token := secret(), secret()
		err := db.Exec("INSERT INTO music_shares(id,owner_id,hash,kind,target_id,expires_at) VALUES(?,?,?,?,?,?)", id, a.ID, digest(token), in.Kind, in.TargetID, s.opts.Now().Add(30*24*time.Hour)).Error
		return map[string]string{"id": id, "token": token}, err
	}
	return nil, missing
}
func (s *Server) shared(r *http.Request, token string) (Export, error) {
	out := Export{Format: "betago-music", Version: 1, Playlists: []ExportPlaylist{}, Likes: []Track{}}
	if len(token) < 40 {
		return out, missing
	}
	var row struct{ OwnerID, Kind, TargetID string }
	db := s.db.WithContext(r.Context())
	res := db.Raw("SELECT ms.owner_id,ms.kind,ms.target_id FROM music_shares ms JOIN music_users u ON u.id=ms.owner_id WHERE ms.hash=? AND ms.revoked=false AND ms.expires_at>? AND u.app_id=?", digest(token), s.opts.Now(), s.opts.AppID).Scan(&row)
	if res.Error != nil {
		return out, res.Error
	}
	if res.RowsAffected == 0 {
		return out, missing
	}
	if row.Kind == "playlist" {
		p, err := playlist(db, row.OwnerID, row.TargetID)
		if err != nil {
			return out, err
		}
		out.Playlists = append(out.Playlists, ExportPlaylist{Name: p.Name, Tracks: p.Tracks})
	} else {
		var t Track
		res := db.Raw("SELECT * FROM music_tracks WHERE id=?", row.TargetID).Scan(&t)
		if res.Error != nil {
			return out, res.Error
		}
		if res.RowsAffected == 0 {
			return out, missing
		}
		out.Playlists = append(out.Playlists, ExportPlaylist{Name: t.Title, Tracks: []Track{t}})
	}
	return out, nil
}
