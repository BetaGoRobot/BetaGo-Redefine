package music

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	neteaseShareURL  = regexp.MustCompile(`https?://[^\s<>"'（）【】《》。，、]+`)
	neteaseSharePath = regexp.MustCompile(`^/playlist/([1-9][0-9]*)(?:/[1-9][0-9]*)?/?$`)
	neteaseShortPath = regexp.MustCompile(`^/[A-Za-z0-9]{1,32}$`)
)

func playlistSyncID(ctx context.Context, raw string, shortClient *http.Client) (string, error) {
	invalidLink := invalid("invalid_netease_playlist")
	if len(raw) > 2048 || strings.Contains(raw, "\\") {
		return "", invalidLink
	}
	raw = strings.TrimSpace(raw)
	if matches := neteaseShareURL.FindAllString(raw, 2); len(matches) == 1 {
		raw = strings.TrimRight(matches[0], ").,]")
	} else if len(matches) > 1 {
		return "", invalidLink
	}
	if numeric.MatchString(raw) {
		return parseNeteasePlaylistID(raw)
	}
	if strings.HasPrefix(raw, "music.163.com/") || strings.HasPrefix(raw, "y.music.163.com/") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.RawPath != "" ||
		(u.Scheme != "https" && u.Scheme != "http") {
		return "", invalidLink
	}
	switch strings.ToLower(u.Host) {
	case "music.163.com", "y.music.163.com":
		return fullPlaylistShareID(u)
	case "163cn.tv":
		if !neteaseShortPath.MatchString(u.Path) || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return "", invalidLink
		}
		// Construct the request from an allowlisted host and validated opaque code.
		// Never follow Location. Only parse it as a canonical public playlist.
		return resolvePlaylistShortCode(ctx, strings.ToLower(u.Host), u.Path, shortClient)
	default:
		return "", invalidLink
	}
}

func fullPlaylistShareID(u *url.URL) (string, error) {
	invalidLink := invalid("invalid_netease_playlist")
	host := strings.ToLower(u.Host)
	if u.User != nil || u.Opaque != "" || u.RawPath != "" ||
		(u.Scheme != "https" && u.Scheme != "http") || (host != "music.163.com" && host != "y.music.163.com") {
		return "", invalidLink
	}
	if id, err := parseNeteasePlaylistID(u.String()); err == nil {
		return id, nil
	}
	if u.Fragment != "" {
		return "", invalidLink
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", invalidLink
	}
	ids := q["id"]
	if (u.Path == "/playlist" || u.Path == "/m/playlist") && len(ids) == 1 && numeric.MatchString(ids[0]) {
		return parseNeteasePlaylistID(ids[0])
	}
	matches := neteaseSharePath.FindStringSubmatch(u.Path)
	if len(matches) != 2 || len(ids) > 1 || len(ids) == 1 && ids[0] != matches[1] {
		return "", invalidLink
	}
	return parseNeteasePlaylistID(matches[1])
}

func resolvePlaylistShortCode(ctx context.Context, host, path string, client *http.Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	shortURL := url.URL{Scheme: "https", Host: host, Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, shortURL.String(), nil)
	if err != nil {
		return "", invalid("invalid_netease_playlist")
	}
	safe := http.Client{CheckRedirect: rejectSourceRedirect}
	if client != nil {
		safe.Transport = client.Transport
	}
	res, err := safe.Do(req)
	if err != nil {
		return "", &apiError{503, "netease_playlist_short_link_unavailable"}
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case 301, 302, 303, 307, 308:
		location, err := res.Location()
		if err == nil {
			return fullPlaylistShareID(location)
		}
	case 429:
		return "", &apiError{429, "provider_rate_limited"}
	case 500, 502, 503, 504:
		return "", &apiError{503, "netease_playlist_short_link_unavailable"}
	}
	return "", invalid("invalid_netease_playlist")
}
