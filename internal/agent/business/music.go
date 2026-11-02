package business

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type MusicConfig struct {
	BaseURL, Cookie string
	HTTPClient      *http.Client
	Timeout         time.Duration
}
type MusicClient struct {
	base, cookie string
	http         *http.Client
}
type MusicSearch struct {
	Kind, Keywords string
	Limit, Offset  int
}
type MusicItem struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Artists       []string `json:"artists,omitempty"`
	ImageURL      string   `json:"image_url,omitempty"`
	AlbumID       int64    `json:"album_id,omitempty"`
	Album         string   `json:"album,omitempty"`
	DurationMS    int      `json:"duration_ms,omitempty"`
	PublishedAtMS int64    `json:"published_at_ms,omitempty"`
}
type MusicResult struct {
	Kind   string      `json:"kind"`
	Items  []MusicItem `json:"items"`
	Total  int         `json:"total"`
	Offset int         `json:"offset"`
}
type MusicDetail struct {
	Kind   string      `json:"kind"`
	ID     int64       `json:"id"`
	Name   string      `json:"name"`
	Tracks []MusicItem `json:"tracks"`
	Total  int         `json:"total"`
	Offset int         `json:"offset"`
}
type songWire struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Ar   []struct {
		Name string `json:"name"`
	} `json:"ar"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Duration    int   `json:"duration"`
	DT          int   `json:"dt"`
	PublishTime int64 `json:"publishTime"`
	Album       struct {
		ID     int64
		Name   string
		PicURL string `json:"picUrl"`
	} `json:"album"`
	Al struct {
		ID     int64
		Name   string
		PicURL string `json:"picUrl"`
	} `json:"al"`
	PicURL string `json:"picUrl"`
	Artist struct {
		Name string `json:"name"`
	} `json:"artist"`
}

func (s songWire) item() MusicItem {
	out := MusicItem{ID: s.ID, Name: s.Name, ImageURL: s.PicURL, AlbumID: s.Al.ID, Album: s.Al.Name, DurationMS: s.DT, PublishedAtMS: s.PublishTime}
	if out.DurationMS == 0 {
		out.DurationMS = s.Duration
	}
	if out.AlbumID == 0 {
		out.AlbumID = s.Album.ID
		out.Album = s.Album.Name
	}
	if out.ImageURL == "" {
		out.ImageURL = s.Album.PicURL
	}
	if out.ImageURL == "" {
		out.ImageURL = s.Al.PicURL
	}
	for _, a := range s.Ar {
		out.Artists = append(out.Artists, a.Name)
	}
	for _, a := range s.Artists {
		out.Artists = append(out.Artists, a.Name)
	}
	if s.Artist.Name != "" {
		out.Artists = append(out.Artists, s.Artist.Name)
	}
	return out
}
func NewMusicClient(cfg MusicConfig) (*MusicClient, error) {
	if _, err := endpoint(cfg.BaseURL); err != nil {
		return nil, domain.ErrInvalid
	}
	c, err := httpClient(cfg.HTTPClient, cfg.Timeout)
	if err != nil || strings.ContainsAny(cfg.Cookie, "\r\n") {
		return nil, domain.ErrInvalid
	}
	return &MusicClient{base: strings.TrimRight(cfg.BaseURL, "/"), cookie: cfg.Cookie, http: c}, nil
}
func (c *MusicClient) Fingerprint() string { return domain.StableID("netease-v1", c.base, c.cookie) }

// MusicProviderError exposes only safe status metadata, never provider bodies or credentials.
type MusicProviderError struct {
	Operation        string
	HTTPStatus, Code int
	Kind             string
}

func (e *MusicProviderError) Error() string {
	return fmt.Sprintf("music provider %s failed (%s, http=%d, code=%d)", e.Operation, e.Kind, e.HTTPStatus, e.Code)
}
func (e *MusicProviderError) Is(target error) bool {
	return target == ErrProvider || (target == domain.ErrNotFound && (e.HTTPStatus == 404 || e.Code == 404))
}
func (c *MusicClient) call(ctx context.Context, path string, form url.Values, out any) error {
	fail := func(kind string, status, code int) error {
		return &MusicProviderError{Operation: path, Kind: kind, HTTPStatus: status, Code: code}
	}
	r, err := http.NewRequestWithContext(ctx, "POST", c.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return fail("request", 0, 0)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.cookie != "" {
		r.Header.Set("Cookie", c.cookie)
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return fail("transport", 0, 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fail("http", resp.StatusCode, 0)
	}
	if resp.ContentLength > 2<<20 {
		return fail("limit", resp.StatusCode, 0)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 {
		return fail("body", resp.StatusCode, 0)
	}
	var status struct {
		Code int `json:"code"`
	}
	if json.Unmarshal(b, &status) != nil {
		return fail("decode", resp.StatusCode, 0)
	}
	if status.Code != 200 {
		return fail("status", resp.StatusCode, status.Code)
	}
	if json.Unmarshal(b, out) != nil {
		return fail("decode", resp.StatusCode, status.Code)
	}
	return nil
}
func page(limit, offset int) bool { return limit >= 1 && limit <= 50 && offset >= 0 && offset <= 10000 }
func (c *MusicClient) Search(ctx context.Context, a MusicSearch) (MusicResult, error) {
	if a.Limit == 0 {
		a.Limit = 10
	}
	if !page(a.Limit, a.Offset) || strings.TrimSpace(a.Keywords) == "" || len(a.Keywords) > 1024 {
		return MusicResult{}, domain.ErrInvalid
	}
	types := map[string]string{"song": "1", "album": "10", "playlist": "1000"}
	typ, ok := types[a.Kind]
	if !ok {
		return MusicResult{}, domain.ErrInvalid
	}
	var wire struct {
		Result struct {
			Songs, Albums, Playlists             []songWire
			SongCount, AlbumCount, PlaylistCount int
		}
	}
	err := c.call(ctx, "/cloudsearch", url.Values{"keywords": {a.Keywords}, "type": {typ}, "limit": {strconv.Itoa(a.Limit)}, "offset": {strconv.Itoa(a.Offset)}}, &wire)
	if err != nil {
		return MusicResult{}, err
	}
	items, total := wire.Result.Songs, wire.Result.SongCount
	if a.Kind == "album" {
		items, total = wire.Result.Albums, wire.Result.AlbumCount
	}
	if a.Kind == "playlist" {
		items, total = wire.Result.Playlists, wire.Result.PlaylistCount
	}
	out := MusicResult{Kind: a.Kind, Total: total, Offset: a.Offset, Items: []MusicItem{}}
	for _, s := range items {
		if len(out.Items) == a.Limit {
			break
		}
		if s.ID > 0 {
			out.Items = append(out.Items, s.item())
		}
	}
	return out, nil
}
func (c *MusicClient) songs(ctx context.Context, ids []int64) ([]MusicItem, error) {
	if len(ids) == 0 {
		return []MusicItem{}, nil
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	var w struct{ Songs []songWire }
	if err := c.call(ctx, "/song/detail", url.Values{"ids": {strings.Join(parts, ",")}}, &w); err != nil {
		return nil, err
	}
	byID := map[int64]MusicItem{}
	for _, s := range w.Songs {
		byID[s.ID] = s.item()
	}
	out := []MusicItem{}
	for _, id := range ids {
		if item, ok := byID[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}
func (c *MusicClient) Detail(ctx context.Context, kind string, id int64, limit, offset int) (MusicDetail, error) {
	if limit == 0 {
		limit = 20
	}
	if id <= 0 || !page(limit, offset) {
		return MusicDetail{}, domain.ErrInvalid
	}
	out := MusicDetail{Kind: kind, ID: id, Offset: offset, Tracks: []MusicItem{}}
	form := url.Values{"id": {strconv.FormatInt(id, 10)}}
	switch kind {
	case "song":
		items, err := c.songs(ctx, []int64{id})
		if err != nil {
			return out, err
		}
		if len(items) != 1 {
			return out, domain.ErrNotFound
		}
		out.Name = items[0].Name
		out.Tracks = items
		out.Total = 1
	case "album":
		var w struct {
			Songs []songWire
			Album struct{ Name string }
		}
		if err := c.call(ctx, "/album", form, &w); err != nil {
			return out, err
		}
		out.Name = w.Album.Name
		out.Total = len(w.Songs)
		for i := min(offset, len(w.Songs)); i < min(offset+limit, len(w.Songs)); i++ {
			out.Tracks = append(out.Tracks, w.Songs[i].item())
		}
	case "playlist":
		var w struct {
			Playlist struct {
				Name     string
				TrackIDs []struct{ ID int64 }
				Tracks   []songWire
			}
		}
		if err := c.call(ctx, "/playlist/detail", form, &w); err != nil {
			return out, err
		}
		out.Name = w.Playlist.Name
		ids := []int64{}
		for _, t := range w.Playlist.TrackIDs {
			ids = append(ids, t.ID)
		}
		if len(ids) == 0 {
			for _, t := range w.Playlist.Tracks {
				ids = append(ids, t.ID)
			}
		}
		out.Total = len(ids)
		items, err := c.songs(ctx, ids[min(offset, len(ids)):min(offset+limit, len(ids))])
		if err != nil {
			return out, err
		}
		out.Tracks = items
	default:
		return out, domain.ErrInvalid
	}
	return out, nil
}

type PlaybackOptions struct{ Refresh bool }

func (c *MusicClient) PlaybackURL(ctx context.Context, id int64, options ...PlaybackOptions) (string, error) {
	if id <= 0 {
		return "", domain.ErrInvalid
	}
	var w struct {
		Data []struct {
			ID  int64
			URL string
		}
	}
	if err := c.call(ctx, "/song/url/v1", url.Values{"id": {strconv.FormatInt(id, 10)}, "level": {"standard"}, "unblock": {"true"}, "timestamp": {strconv.FormatInt(time.Now().UnixNano(), 10)}}, &w); err != nil {
		return "", err
	}
	for _, item := range w.Data {
		if item.ID == id {
			u, e := url.Parse(item.URL)
			if e == nil && u.Host != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http") {
				return item.URL, nil
			}
		}
	}
	return "", domain.ErrNotFound
}

// MusicLyrics retains the original timed LRC for external players and a full merged display.
type MusicLyrics struct {
	Original   string `json:"original"`
	Translated string `json:"translated,omitempty"`
	Full       string `json:"full"`
}
type MusicComment struct {
	Content string `json:"content"`
	Author  string `json:"author,omitempty"`
	TimeMS  int64  `json:"time_ms"`
}

func (c *MusicClient) Lyrics(ctx context.Context, id int64) (MusicLyrics, error) {
	if id <= 0 {
		return MusicLyrics{}, domain.ErrInvalid
	}
	var w struct {
		LRC    struct{ Lyric string }
		TLyric struct{ Lyric string }
	}
	if err := c.call(ctx, "/lyric", url.Values{"id": {strconv.FormatInt(id, 10)}, "timestamp": {strconv.FormatInt(time.Now().UnixNano(), 10)}}, &w); err != nil {
		return MusicLyrics{}, err
	}
	return MusicLyrics{Original: w.LRC.Lyric, Translated: w.TLyric.Lyric, Full: mergeMusicLyrics(w.LRC.Lyric, w.TLyric.Lyric)}, nil
}
func mergeMusicLyrics(original, translated string) string {
	lines := map[string][]string{}
	plain := []string{}
	for _, part := range []string{original, translated} {
		for _, line := range strings.Split(part, "\n") {
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "[") {
				if i := strings.IndexByte(line, ']'); i > 1 {
					lines[line[1:i]] = append(lines[line[1:i]], line[i+1:])
					continue
				}
			}
			plain = append(plain, line)
		}
	}
	keys := make([]string, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		plain = append(plain, strings.Join(lines[k], "\n"))
	}
	return strings.Join(plain, "\n")
}
func (c *MusicClient) Comment(ctx context.Context, kind string, id int64) (MusicComment, error) {
	typ, ok := map[string]string{"song": "0", "album": "3", "playlist": "2"}[kind]
	if id <= 0 || !ok {
		return MusicComment{}, domain.ErrInvalid
	}
	var w struct {
		Data struct {
			Comments []struct {
				Content string
				Time    int64
				User    struct{ Nickname string }
			}
		}
	}
	if err := c.call(ctx, "/comment/new/", url.Values{"id": {strconv.FormatInt(id, 10)}, "type": {typ}, "pageSize": {"1"}, "pageNo": {"1"}, "sortType": {"2"}, "timestamp": {strconv.FormatInt(time.Now().UnixNano(), 10)}}, &w); err != nil {
		return MusicComment{}, err
	}
	if len(w.Data.Comments) == 0 {
		return MusicComment{}, domain.ErrNotFound
	}
	v := w.Data.Comments[0]
	return MusicComment{Content: v.Content, Author: v.User.Nickname, TimeMS: v.Time}, nil
}
