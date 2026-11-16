package music

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// PublicPlaylist contains anonymous catalogue metadata only. Tracks retain the
// upstream order and duplicates, allowing the owned import to report deduplication.
type PublicPlaylist struct {
	ID     string
	Name   string
	Tracks []Track
}

type PublicPlaylistImporter interface {
	PublicPlaylist(context.Context, string) (PublicPlaylist, error)
}

func parseNeteasePlaylistID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	valid := func(id string) bool {
		_, err := strconv.ParseInt(id, 10, 64)
		return numeric.MatchString(id) && err == nil
	}
	if valid(raw) {
		return raw, nil
	}
	if len(raw) > 2048 || strings.Contains(raw, "\\") {
		return "", invalid("invalid_netease_playlist")
	}
	if strings.HasPrefix(raw, "music.163.com/") || strings.HasPrefix(raw, "y.music.163.com/") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") || u.Port() != "" {
		return "", invalid("invalid_netease_playlist")
	}
	host := strings.ToLower(u.Hostname())
	if host == "163cn.tv" || host == "163cn.com" {
		return "", invalid("netease_playlist_full_url_required")
	}
	if host != "music.163.com" && host != "y.music.163.com" {
		return "", invalid("invalid_netease_playlist")
	}
	if u.Fragment != "" {
		if host != "music.163.com" || (u.Path != "/" && u.Path != "") || u.RawQuery != "" {
			return "", invalid("invalid_netease_playlist")
		}
		fragment, err := url.Parse(u.Fragment)
		if err != nil || fragment.Path != "/playlist" || fragment.Fragment != "" || fragment.Host != "" {
			return "", invalid("invalid_netease_playlist")
		}
		u = fragment
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", invalid("invalid_netease_playlist")
	}
	if u.Path == "/playlist" && host == "music.163.com" || u.Path == "/m/playlist" && host == "y.music.163.com" {
		ids := query["id"]
		if len(ids) == 1 && valid(ids[0]) {
			return ids[0], nil
		}
	} else if host == "music.163.com" && strings.HasPrefix(u.Path, "/playlist/") && len(query["id"]) == 0 {
		id := strings.TrimPrefix(u.Path, "/playlist/")
		if valid(id) {
			return id, nil
		}
	}
	return "", invalid("invalid_netease_playlist")
}

func publicPlaylistSourceError(err error) error {
	var ae *apiError
	if errors.As(err, &ae) {
		if ae.Status == 401 || ae.Status == 403 {
			return &apiError{403, "public_playlist_access_denied"}
		}
		if ae.Status == 429 {
			return &apiError{429, "provider_rate_limited"}
		}
	}
	return &apiError{503, "public_playlist_source_unavailable"}
}

func publicPlaylistResponseCode(code int) error {
	if code == 200 {
		return nil
	}
	if code == 301 || code == 401 || code == 403 {
		return &apiError{403, "public_playlist_access_denied"}
	}
	if code == 429 {
		return &apiError{429, "provider_rate_limited"}
	}
	return &apiError{503, "public_playlist_source_unavailable"}
}

func validPublicPlaylistTrack(t Track) bool {
	_, err := strconv.ParseInt(t.ExternalID, 10, 64)
	return err == nil && numeric.MatchString(t.ExternalID) && t.Source == "netease" && t.ID == "netease:"+t.ExternalID &&
		strings.TrimSpace(t.Title) != "" && len(t.Title) <= 1000 && strings.TrimSpace(t.Artist) != "" && len(t.Artist) <= 2000 &&
		len(t.Album) <= 2000 && len(t.Picture) <= 4000 && t.DurationMs > 0
}

// PublicPlaylist never uses the operator account, browser cookies, provider
// fallback, or a URL supplied by the caller. Only two fixed API paths are read.
func (p AnonymousNetease) PublicPlaylist(ctx context.Context, id string) (PublicPlaylist, error) {
	out := PublicPlaylist{Tracks: []Track{}}
	if parsed, err := parseNeteasePlaylistID(id); err != nil || parsed != id {
		return out, invalid("invalid_netease_playlist")
	}
	ctx, stop := context.WithTimeout(ctx, 18*time.Second)
	defer stop()
	p.ServerCookie = ""
	var detail struct {
		Code     int
		Playlist struct {
			ID         int64
			Name       string
			Privacy    int
			TrackCount int
			TrackIDs   []struct{ ID int64 } `json:"trackIds"`
		}
	}
	if err := p.get(ctx, "/playlist/detail", url.Values{"id": {id}}, &detail); err != nil {
		return out, publicPlaylistSourceError(err)
	}
	if err := publicPlaylistResponseCode(detail.Code); err != nil {
		return out, err
	}
	pl := detail.Playlist
	if pl.Privacy != 0 {
		return out, &apiError{403, "public_playlist_access_denied"}
	}
	if pl.TrackCount > 1000 || len(pl.TrackIDs) > 1000 {
		return out, &apiError{413, "public_playlist_too_large"}
	}
	if len(pl.TrackIDs) == 0 && pl.TrackCount == 0 {
		return out, &apiError{422, "public_playlist_empty"}
	}
	if strconv.FormatInt(pl.ID, 10) != id || strings.TrimSpace(pl.Name) == "" || len(pl.Name) > 200 ||
		pl.TrackCount != len(pl.TrackIDs) || len(pl.TrackIDs) == 0 {
		return out, &apiError{422, "public_playlist_metadata_incomplete"}
	}
	ids := make([]string, 0, len(pl.TrackIDs))
	for _, track := range pl.TrackIDs {
		if track.ID <= 0 {
			return out, &apiError{422, "public_playlist_metadata_incomplete"}
		}
		ids = append(ids, strconv.FormatInt(track.ID, 10))
	}
	ordered := unique(ids)
	batchCount := (len(ordered) + 49) / 50
	results := make([][]netSong, batchCount)
	jobs := make(chan int, batchCount)
	for i := 0; i < batchCount; i++ {
		jobs <- i
	}
	close(jobs)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var firstErr error
	var once sync.Once
	failed := func(err error) { once.Do(func() { firstErr = err; cancel() }) }
	var wg sync.WaitGroup
	for worker := 0; worker < min(4, batchCount); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				lo, hi := batch*50, min((batch+1)*50, len(ordered))
				var response struct {
					Code  int
					Songs []netSong
				}
				if err := p.get(ctx, "/song/detail", url.Values{"ids": {strings.Join(ordered[lo:hi], ",")}}, &response); err != nil {
					failed(publicPlaylistSourceError(err))
					continue
				}
				if err := publicPlaylistResponseCode(response.Code); err != nil {
					failed(err)
					continue
				}
				results[batch] = response.Songs
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return out, firstErr
	}
	metadata := make(map[string]Track, len(ordered))
	for batch, songs := range results {
		lo, hi := batch*50, min((batch+1)*50, len(ordered))
		wanted := map[string]bool{}
		for _, id := range ordered[lo:hi] {
			wanted[id] = true
		}
		for _, song := range songs {
			t := song.track()
			if !wanted[t.ExternalID] || !validPublicPlaylistTrack(t) || metadata[t.ExternalID].ID != "" {
				return out, &apiError{422, "public_playlist_metadata_incomplete"}
			}
			metadata[t.ExternalID] = t
		}
	}
	if len(metadata) != len(ordered) {
		return out, &apiError{422, "public_playlist_metadata_incomplete"}
	}
	out.ID, out.Name = id, strings.TrimSpace(pl.Name)
	for _, id := range ids {
		out.Tracks = append(out.Tracks, metadata[id])
	}
	return out, nil
}

func (s *Server) importNeteasePlaylist(r *http.Request, a account) (any, error) {
	var in struct {
		URLOrID string `json:"urlOrID"`
		Name    string `json:"name"`
	}
	if err := body(r, &in); err != nil {
		return nil, err
	}
	id, err := parseNeteasePlaylistID(in.URLOrID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 200 {
		return nil, invalid("invalid_name")
	}
	if s.opts.PlaylistImporter == nil {
		return nil, &apiError{503, "public_playlist_import_not_configured"}
	}
	public, err := s.opts.PlaylistImporter.PublicPlaylist(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if len(public.Tracks) > 1000 {
		return nil, &apiError{413, "public_playlist_too_large"}
	}
	if len(public.Tracks) == 0 {
		return nil, &apiError{422, "public_playlist_empty"}
	}
	if public.ID != id {
		return nil, &apiError{422, "public_playlist_metadata_incomplete"}
	}
	if name == "" {
		name = strings.TrimSpace(public.Name)
	}
	if name == "" || len(name) > 200 {
		return nil, &apiError{422, "public_playlist_metadata_incomplete"}
	}
	tracks := make([]Track, 0, len(public.Tracks))
	ids := make([]string, 0, len(public.Tracks))
	seen := map[string]bool{}
	for _, track := range public.Tracks {
		if !validPublicPlaylistTrack(track) {
			return nil, &apiError{422, "public_playlist_metadata_incomplete"}
		}
		if !seen[track.ID] {
			seen[track.ID] = true
			tracks = append(tracks, track)
			ids = append(ids, track.ID)
		}
	}
	var imported Playlist
	err = s.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		for _, track := range tracks {
			if err := saveTrack(tx, track); err != nil {
				return err
			}
		}
		id, err := createPlaylist(tx, a.ID, name, ids)
		if err != nil {
			return err
		}
		imported, err = playlist(tx, a.ID, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"playlist": imported, "imported": len(tracks), "duplicatesSkipped": len(public.Tracks) - len(tracks), "sourcePlaylistID": id}, nil
}
