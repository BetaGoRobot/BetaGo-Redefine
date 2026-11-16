package music

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// KuwoProvider calls an independently managed, anonymous, read-only Kuwo adapter.
// It never matches a Netease ID or forwards application/account credentials.
// BaseURL must be an explicitly configured loopback endpoint (MUSIC_KUWO_API_URL).
type KuwoProvider struct {
	BaseURL string
	Client  *http.Client
}

func (p KuwoProvider) getKuwo(ctx context.Context, route string, query url.Values, out any) error {
	base, err := url.Parse(p.BaseURL)
	if err != nil || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "http" && base.Scheme != "https") {
		return unavailable
	}
	host := base.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return unavailable
	}
	base.Path = strings.TrimRight(base.Path, "/") + route
	base.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return unavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-store")
	client := http.Client{Timeout: 10 * time.Second}
	if p.Client != nil {
		client = *p.Client
	}
	if client.Timeout <= 0 || client.Timeout > 10*time.Second {
		client.Timeout = 10 * time.Second
	}
	client.Jar = nil
	client.CheckRedirect = rejectSourceRedirect
	response, err := client.Do(request)
	if err != nil {
		return unavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return &apiError{http.StatusForbidden, "source_access_denied"}
	}
	if response.StatusCode == http.StatusNotFound {
		return missing
	}
	if response.StatusCode != http.StatusOK {
		return unavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return unavailable
	}
	if err = json.Unmarshal(body, out); err != nil {
		return unavailable
	}
	return nil
}

func canonicalKuwoTrack(track Track) bool {
	return track.Source == "kuwo" && numeric.MatchString(track.ExternalID) && track.ID == "kuwo:"+track.ExternalID &&
		strings.TrimSpace(track.Title) != "" && len(track.Title) <= 1000 && len(track.Artist) <= 2000 && len(track.Album) <= 2000 &&
		track.DurationMs > 0 && track.DurationMs <= 86400000
}

func (p KuwoProvider) Search(ctx context.Context, query string) ([]Track, error) {
	return p.SearchSource(ctx, "kuwo", query)
}

// SearchSource is explicit source selection; no fallback to another recording.
func (p KuwoProvider) SearchSource(ctx context.Context, source, query string) ([]Track, error) {
	if source != "kuwo" {
		return nil, missing
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []Track{}, nil
	}
	if len([]rune(query)) > 200 {
		return nil, invalid("invalid_query")
	}
	var body struct {
		Tracks []Track `json:"tracks"`
	}
	if err := p.getKuwo(ctx, "/search", url.Values{"q": {query}}, &body); err != nil {
		return nil, err
	}
	if len(body.Tracks) > 50 {
		return nil, unavailable
	}
	result := make([]Track, 0, len(body.Tracks))
	seen := map[string]bool{}
	for _, track := range body.Tracks {
		if !canonicalKuwoTrack(track) {
			return nil, unavailable
		}
		if seen[track.ID] {
			continue
		}
		seen[track.ID] = true
		track.Picture = securePicture(track.Picture)
		result = append(result, track)
	}
	return result, nil
}

func (p KuwoProvider) Detail(ctx context.Context, id string) (Track, error) {
	external := strings.TrimPrefix(id, "kuwo:")
	if !strings.HasPrefix(id, "kuwo:") || !numeric.MatchString(external) {
		return Track{}, missing
	}
	var body struct {
		Track Track `json:"track"`
	}
	if err := p.getKuwo(ctx, "/tracks/"+external, nil, &body); err != nil {
		return Track{}, err
	}
	if !canonicalKuwoTrack(body.Track) || body.Track.ID != id {
		return Track{}, unavailable
	}
	body.Track.Picture = securePicture(body.Track.Picture)
	return body.Track, nil
}

func (p KuwoProvider) Resolve(ctx context.Context, track Track) (Resolution, error) {
	out := Resolution{TrackID: track.ID, Status: "unavailable", Reason: "source_not_supported"}
	if !canonicalKuwoTrack(track) {
		return out, nil
	}
	var body struct {
		Resolution
		Audio struct {
			TLSVerified bool   `json:"tlsVerified"`
			DurationMs  int64  `json:"durationMs"`
			SizeBytes   int64  `json:"sizeBytes"`
			ContentType string `json:"contentType"`
		} `json:"audio"`
	}
	if err := p.getKuwo(ctx, "/resolve/"+track.ExternalID, nil, &body); err != nil {
		if ae, ok := err.(*apiError); ok && ae.Message == "source_access_denied" {
			out.Reason = "provider_access_denied"
			return out, nil
		}
		return out, err
	}
	out.Reason = "invalid_source_resolution"
	if body.TrackID != track.ID {
		return out, nil
	}
	if body.Status == "unavailable" {
		out.Reason = body.Reason
		if out.Reason == "" {
			out.Reason = "provider_unavailable_or_restricted"
		}
		return out, nil
	}
	if body.Status != "available" && body.Status != "preview" {
		return out, nil
	}
	u, err := url.Parse(body.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		out.Reason = "unsafe_source_audio_url"
		return out, nil
	}
	host := strings.ToLower(u.Hostname())
	if host != "kuwo.cn" && !strings.HasSuffix(host, ".kuwo.cn") {
		out.Reason = "source_mismatch"
		return out, nil
	}
	if !body.Audio.TLSVerified || body.Audio.DurationMs <= 0 || body.Audio.SizeBytes < 8192 || body.Audio.ContentType != "audio/mpeg" {
		out.Reason = "source_audio_not_verified"
		return out, nil
	}
	delta := body.Audio.DurationMs - track.DurationMs
	if delta < 0 {
		delta = -delta
	}
	tolerance := track.DurationMs * 3 / 100
	if tolerance < 5000 {
		tolerance = 5000
	}
	if body.Status == "available" && delta > tolerance {
		out.Reason = "audio_duration_mismatch"
		return out, nil
	}
	if body.Status == "preview" && body.Reason != "provider_explicit_preview" {
		out.Reason = "unverified_preview"
		return out, nil
	}
	now := time.Now()
	if body.ExpiresAt == nil || !body.ExpiresAt.After(now) {
		out.Reason = "source_url_expired_or_unknown"
		return out, nil
	}
	// This is a local recheck lease; never keep an unbounded upstream URL.
	deadline := now.Add(2 * time.Minute)
	if body.ExpiresAt.Before(deadline) {
		deadline = *body.ExpiresAt
	}
	return Resolution{TrackID: track.ID, Status: body.Status, URL: u.String(), ExpiresAt: &deadline, Reason: body.Reason}, nil
}

func (p KuwoProvider) Lyrics(_ context.Context, track Track) (Lyrics, error) {
	if !canonicalKuwoTrack(track) {
		return Lyrics{}, missing
	}
	return Lyrics{TrackID: track.ID, LRC: "", TranslatedLRC: ""}, nil
}
